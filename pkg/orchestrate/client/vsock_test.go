package client_test

import (
	"context"
	"net"
	"testing"

	"github.com/systemstart/nix-compose/internal/testsock"

	orchestratev1 "github.com/systemstart/nix-compose/api/orchestrate/v1"
	"github.com/systemstart/nix-compose/pkg/cni"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/orchestrate/client"
	"github.com/systemstart/nix-compose/pkg/orchestrate/server"
	"github.com/systemstart/nix-compose/pkg/volumes"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestDialerWiring verifies the passthrough + WithContextDialer pattern works
// by connecting a gRPC client through a custom dialer to a unix socket server.
func TestDialerWiring(t *testing.T) {
	criSock := startMockCRI(t)
	ctx := context.Background()

	criClient, err := cri.Dial(ctx, criSock)
	if err != nil {
		t.Fatalf("dial CRI: %v", err)
	}
	t.Cleanup(func() { _ = criClient.Close() })

	sock := testsock.Path(t, "dialer-test.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := server.New(server.Config{
		CRIClient: criClient,
		CNIStore:  &cni.Store{ConfDir: t.TempDir(), PluginDirs: []string{}},
		VolStore:  &volumes.Store{Root: t.TempDir()},
		LogBase:   t.TempDir(),
		DBPath:    t.TempDir() + "/state.bolt",
	})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	// Connect using the passthrough + WithContextDialer pattern (same as DialVsock).
	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}
	conn, err := grpc.NewClient(
		"passthrough:///test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	rpc := orchestratev1.NewOrchestrateServiceClient(conn)
	resp, err := rpc.Health(ctx, &orchestratev1.HealthRequest{})
	if err != nil {
		t.Fatalf("Health via custom dialer: %v", err)
	}
	if !resp.Healthy {
		t.Error("expected healthy=true via custom dialer")
	}
}

// TestDialVsockInvalidTarget verifies DialVsock returns an error when the
// target is unreachable.
func TestDialVsockInvalidTarget(t *testing.T) {
	ctx := context.Background()
	_, err := client.DialVsock(ctx, 0, 1024)
	if err == nil {
		t.Fatal("expected error dialing invalid vsock target")
	}
}
