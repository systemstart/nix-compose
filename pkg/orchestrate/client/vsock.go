//go:build linux

package client

import (
	"context"
	"fmt"
	"net"

	"github.com/mdlayher/vsock"
	orchestratev1 "github.com/systemstart/nix-compose/api/orchestrate/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// DialVsockRaw opens a raw vsock connection, returning a net.Conn
// suitable for non-gRPC protocols such as port forwarding.
func DialVsockRaw(cid, port uint32) (net.Conn, error) {
	conn, err := vsock.Dial(cid, port, nil)
	if err != nil {
		return nil, fmt.Errorf("vsock dial raw cid=%d port=%d: %w", cid, port, err)
	}
	return conn, nil
}

// DialVsock connects to the orchestrate gRPC server over a vsock transport.
func DialVsock(ctx context.Context, cid, port uint32) (*Client, error) {
	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return vsock.Dial(cid, port, nil)
	}

	conn, err := grpc.NewClient(
		"passthrough:///vsock",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer),
	)
	if err != nil {
		return nil, fmt.Errorf("orchestrate client: vsock dial cid=%d port=%d: %w", cid, port, err)
	}

	c := &Client{
		conn: conn,
		rpc:  orchestratev1.NewOrchestrateServiceClient(conn),
	}

	if _, err := c.Health(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("orchestrate client: vsock health check cid=%d port=%d: %w", cid, port, err)
	}

	return c, nil
}
