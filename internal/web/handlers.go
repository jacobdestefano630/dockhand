package web

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/you/dockhand/internal/docker"
)

type Server struct {
	dc         *dockerc.Client
	templates  *template.Template
	grafanaURL string // optional deep-link to Loki Explore (e.g., http://grafana:3000/explore)
}

func New(dc *dockerc.Client, templates *template.Template, grafanaURL string) *Server {
	return &Server{dc: dc, templates: templates, grafanaURL: grafanaURL}
}

func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/", s.index)
	mux.HandleFunc("/partials/rows", s.rowsPartial)
	mux.HandleFunc("/containers/", s.containerAction)
	mux.HandleFunc("/logs/", s.logsPage)
	mux.HandleFunc("/logs/stream/", s.logsStreamSSE)

	// static htmx
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	// first paint = whole page with table
	ctx := r.Context()
	cs, err := s.dc.ListContainers(ctx, true)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	data := map[string]any{
		"Containers": cs,
		"GrafanaURL": s.grafanaURL,
	}
	if err := s.templates.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

// HTMX target to swap just the tbody rows (auto-refresh etc.)
func (s *Server) rowsPartial(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cs, err := s.dc.ListContainers(ctx, true)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := s.templates.ExecuteTemplate(w, "rows", cs); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func (s *Server) containerAction(w http.ResponseWriter, r *http.Request) {
	// /containers/{id}/{start|stop|restart}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "containers" {
		http.NotFound(w, r)
		return
	}
	id, action := parts[1], parts[2]
	ctx := r.Context()
	var err error
	switch action {
	case "start":
		err = s.dc.Cli().ContainerStart(ctx, id, types.ContainerStartOptions{})
	case "stop":
		timeout := container.StopOptions{Timeout: intPtr(10)}
		err = s.dc.Cli().ContainerStop(ctx, id, timeout)
	case "restart":
		timeout := container.StopOptions{Timeout: intPtr(10)}
		err = s.dc.Cli().ContainerRestart(ctx, id, timeout)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(204) // HTMX: no content
}

func (c *dockerc.Client) Cli() *clientWrapper { return &clientWrapper{c} }

type clientWrapper struct{ *dockerc.Client }

func (w *clientWrapper) ContainerStart(ctx context.Context, container string, options types.ContainerStartOptions) error {
	return w.cli.ContainerStart(ctx, container, options)
}
func (w *clientWrapper) ContainerStop(ctx context.Context, container string, opt container.StopOptions) error {
	return w.cli.ContainerStop(ctx, container, opt)
}
func (w *clientWrapper) ContainerRestart(ctx context.Context, container string, opt container.StopOptions) error {
	return w.cli.ContainerRestart(ctx, container, opt)
}

// Logs page with a <pre> and a Start Stream button
func (s *Server) logsPage(w http.ResponseWriter, r *http.Request) {
	// /logs/{id}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "logs" {
		http.NotFound(w, r)
		return
	}
	id := parts[1]
	data := map[string]any{"ID": id}
	if err := s.templates.ExecuteTemplate(w, "logs", data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

// Server-Sent Events stream of container logs
func (s *Server) logsStreamSSE(w http.ResponseWriter, r *http.Request) {
	// /logs/stream/{id}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "logs" || parts[1] != "stream" {
		http.NotFound(w, r)
		return
	}
	id := parts[2]
	ctx := r.Context()

	rc, err := s.dc.Cli().ContainerLogs(ctx, id, types.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Timestamps: true,
		Tail:       "100",
	})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}

	buf := make([]byte, 4096)
	for {
		n, readErr := rc.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			// Docker multiplexed logs may include headers; Docker SDK usually demuxes for ContainerLogs on recent versions.
			// Send as SSE "data:" lines:
			for _, line := range strings.Split(chunk, "\n") {
				if line == "" {
					continue
				}
				fmt.Fprintf(w, "data: %s\n\n", line)
			}
			flusher.Flush()
		}
		if readErr != nil {
			if readErr != io.EOF {
				log.Printf("log stream error: %v", readErr)
			}
			break
		}
	}
}

func intPtr(i int) *int { return &i }
