package dockerc

import (
	"context"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

type Client struct {
	cli *client.Client
}

func New(host string) (*Client, error) {
	opts := []client.Opt{
		client.FromEnv, // <-- typo fixed (FromEnv)
		client.WithAPIVersionNegotiation(),
	}
	if host != "" {
		opts = append(opts, client.WithHost(host)) // <-- opts (not opt)
	}
	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, err
	}
	return &Client{cli: cli}, nil
}

func (c *Client) ListContainers(ctx context.Context, all bool) ([]types.Container, error) {
	return c.cli.ContainerList(ctx, types.ContainerListOptions{All: all})
}
