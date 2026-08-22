package cri

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// DefaultSocketPaths is the ordered list of CRI sockets tried by Detect.
var DefaultSocketPaths = []string{
	"/run/containerd/containerd.sock",
	"/run/crio/crio.sock",
	"/tmp/ctrd/containerd.sock",
}

// VersionInfo holds the response from RuntimeService.Version.
type VersionInfo struct {
	RuntimeName    string
	RuntimeVersion string
	APIVersion     string
}

// Client wraps a CRI gRPC connection.
type Client struct {
	conn    *grpc.ClientConn
	runtime runtimev1.RuntimeServiceClient
	image   runtimev1.ImageServiceClient
	socket  string
	cgroupState
}

// Dial connects to the CRI socket at the given path, verifies it with a
// Version call, and returns a ready-to-use Client.
func Dial(ctx context.Context, socket string) (*Client, error) {
	conn, err := grpc.NewClient(
		"unix://"+socket,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("cri: dial %s: %w", socket, err)
	}

	c := &Client{
		conn:    conn,
		runtime: runtimev1.NewRuntimeServiceClient(conn),
		image:   runtimev1.NewImageServiceClient(conn),
		socket:  socket,
	}

	// Verify the connection actually works.
	if _, err := c.Version(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("cri: version check on %s: %w", socket, err)
	}

	return c, nil
}

// Detect tries each path in DefaultSocketPaths and returns the first
// socket that responds to a Version call.
func Detect(ctx context.Context) (*Client, error) {
	return DetectWithPaths(ctx, DefaultSocketPaths)
}

// DetectWithPaths tries each socket path in order and returns the first
// that responds to a Version call.
func DetectWithPaths(ctx context.Context, paths []string) (*Client, error) {
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		c, err := Dial(ctx, p)
		if err == nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("cri: no responsive CRI socket found")
}

// Version calls RuntimeService.Version and returns the result.
func (c *Client) Version(ctx context.Context) (*VersionInfo, error) {
	resp, err := c.runtime.Version(ctx, &runtimev1.VersionRequest{})
	if err != nil {
		return nil, fmt.Errorf("cri: version: %w", err)
	}
	return &VersionInfo{
		RuntimeName:    resp.GetRuntimeName(),
		RuntimeVersion: resp.GetRuntimeVersion(),
		APIVersion:     resp.GetRuntimeApiVersion(),
	}, nil
}

// Close closes the underlying gRPC connection.
func (c *Client) Close() error {
	if err := c.conn.Close(); err != nil {
		return fmt.Errorf("cri: close: %w", err)
	}
	return nil
}

// Socket returns the socket path this client is connected to.
func (c *Client) Socket() string {
	return c.socket
}

// RuntimeClient returns the raw RuntimeServiceClient for advanced use.
func (c *Client) RuntimeClient() runtimev1.RuntimeServiceClient {
	return c.runtime
}

// ImageClient returns the raw ImageServiceClient for advanced use.
func (c *Client) ImageClient() runtimev1.ImageServiceClient {
	return c.image
}
