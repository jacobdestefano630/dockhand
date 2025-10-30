package main

import (
	"html/template"
	"log"
	"net/http"
	"os"

	dockerc "dockhand/internal/docker"
	"dockhand/internal/web"
)

func main() {
	// Config via env (simple)
	host := getenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	addr := getenv("ADDR", ":8088")
	grafanaURL := os.Getenv("GRAFANA_URL") // optional

	dc, err := dockerc.New(host)
	if err != nil {
		log.Fatal(err)
	}

	tpl := template.Must(template.New("all").
		ParseFiles(
			"internal/ui/templates/layout.tmpl.html",
			"internal/ui/templates/row.tmpl.html",
			"internal/ui/templates/index.tmpl.html",
			"internal/ui/templates/logs.tmpl.html",
		))

	srv := web.New(dc, tpl, grafanaURL)
	mux := http.NewServeMux()
	srv.Routes(mux)

	log.Printf("Dockhand listening on %s (DOCKER_HOST=%s)", addr, host)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
