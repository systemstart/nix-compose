package cri

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// mockRuntimeService implements just the Version RPC for testing.
type mockRuntimeService struct {
	runtimev1.UnimplementedRuntimeServiceServer
	name    string
	version string
}

func (m *mockRuntimeService) Version(_ context.Context, _ *runtimev1.VersionRequest) (*runtimev1.VersionResponse, error) {
	return &runtimev1.VersionResponse{
		Version:           "0.1.0",
		RuntimeName:       m.name,
		RuntimeVersion:    m.version,
		RuntimeApiVersion: "v1",
	}, nil
}

// startMockCRI starts a gRPC server on a unix socket in a temp directory
// and returns the socket path. The caller must call the returned cleanup
// function when done.
func startMockCRI(t *testing.T, name, version string) string {
	t.Helper()

	sock := filepath.Join(t.TempDir(), "cri.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := grpc.NewServer()
	runtimev1.RegisterRuntimeServiceServer(srv, &mockRuntimeService{
		name:    name,
		version: version,
	})

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	return sock
}

func TestDial(t *testing.T) {
	sock := startMockCRI(t, "containerd", "1.7.0")

	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	v, err := c.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v.RuntimeName != "containerd" {
		t.Errorf("RuntimeName = %q, want %q", v.RuntimeName, "containerd")
	}
	if v.RuntimeVersion != "1.7.0" {
		t.Errorf("RuntimeVersion = %q, want %q", v.RuntimeVersion, "1.7.0")
	}
	if v.APIVersion != "v1" {
		t.Errorf("APIVersion = %q, want %q", v.APIVersion, "v1")
	}
	if c.Socket() != sock {
		t.Errorf("Socket() = %q, want %q", c.Socket(), sock)
	}
}

func TestDialBadSocket(t *testing.T) {
	ctx := context.Background()
	_, err := Dial(ctx, "/tmp/nonexistent-cri-test.sock")
	if err == nil {
		t.Fatal("expected error dialing non-existent socket, got nil")
	}
}

func TestDetectNoSockets(t *testing.T) {
	ctx := context.Background()
	_, err := DetectWithPaths(ctx, []string{"/tmp/does-not-exist-1.sock", "/tmp/does-not-exist-2.sock"})
	if err == nil {
		t.Fatal("expected error when no sockets exist, got nil")
	}
}

func TestDetectFindsSocket(t *testing.T) {
	sock := startMockCRI(t, "cri-o", "1.30.0")

	// Put a bogus path first so Detect skips it.
	paths := []string{"/tmp/does-not-exist.sock", sock}

	ctx := context.Background()
	c, err := DetectWithPaths(ctx, paths)
	if err != nil {
		t.Fatalf("DetectWithPaths: %v", err)
	}
	defer func() { _ = c.Close() }()

	if c.Socket() != sock {
		t.Errorf("Socket() = %q, want %q", c.Socket(), sock)
	}

	v, err := c.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v.RuntimeName != "cri-o" {
		t.Errorf("RuntimeName = %q, want %q", v.RuntimeName, "cri-o")
	}
}

func TestDetectSkipsStaleSocket(t *testing.T) {
	// Create a socket file that exists but has no listener.
	dir := t.TempDir()
	stale := filepath.Join(dir, "stale.sock")
	lis, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_ = lis.Close() // close immediately — file still exists

	// Start a real mock as the second candidate.
	good := startMockCRI(t, "containerd", "2.0.0")

	ctx := context.Background()
	c, err := DetectWithPaths(ctx, []string{stale, good})
	if err != nil {
		t.Fatalf("DetectWithPaths: %v", err)
	}
	defer func() { _ = c.Close() }()

	if c.Socket() != good {
		t.Errorf("Socket() = %q, want %q", c.Socket(), good)
	}
}

func TestClose(t *testing.T) {
	sock := startMockCRI(t, "containerd", "1.7.0")

	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestRuntimeAndImageClients(t *testing.T) {
	sock := startMockCRI(t, "containerd", "1.7.0")

	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	if c.RuntimeClient() == nil {
		t.Error("RuntimeClient() returned nil")
	}
	if c.ImageClient() == nil {
		t.Error("ImageClient() returned nil")
	}
}

func TestDefaultSocketPathsExist(t *testing.T) {
	// Sanity check: the default list should have at least one entry.
	if len(DefaultSocketPaths) == 0 {
		t.Fatal("DefaultSocketPaths is empty")
	}
}

func TestDetectWithEmptyPaths(t *testing.T) {
	ctx := context.Background()
	_, err := DetectWithPaths(ctx, nil)
	if err == nil {
		t.Fatal("expected error with nil paths, got nil")
	}

	_, err = DetectWithPaths(ctx, []string{})
	if err == nil {
		t.Fatal("expected error with empty paths, got nil")
	}
}

// TestDialVerifiesConnection ensures that Dial actually performs a Version
// call and fails if the server doesn't implement RuntimeService.
func TestDialVerifiesConnection(t *testing.T) {
	// Start a bare gRPC server with no services registered.
	sock := filepath.Join(t.TempDir(), "bare.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	ctx := context.Background()
	_, err = Dial(ctx, sock)
	if err == nil {
		t.Fatal("expected error dialing server with no CRI service, got nil")
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
