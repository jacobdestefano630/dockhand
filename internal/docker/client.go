package dockerc

import (
	"context"

	"github.com/docker/docker/api/types"
	container "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

type Client struct {
	cli *client.Client
}

func New(host string) (*Client, error) {
	opts := []client.Opt{
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	}
	if host != "" {
		opts = append(opts, client.WithHost(host))
	}
	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, err
	}
	return &Client{cli: cli}, nil
}

func (c *Client) ListContainers(ctx context.Context, all bool) ([]types.Container, error) {
	// Note: in newer SDKs, this is container.ListOptions (not types.ContainerListOptions)
	return c.cli.ContainerList(ctx, container.ListOptions{All: all})
}
