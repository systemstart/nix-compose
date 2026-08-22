package cri

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func testService() eval.Service {
	return eval.Service{Image: "nginx:latest"}
}

// cgroupMockCRI is a runtime that reports a configurable cgroup driver and
// records the pod configs it is asked to run.
type cgroupMockCRI struct {
	runtimev1.UnimplementedRuntimeServiceServer
	runtimev1.UnimplementedImageServiceServer

	driver      runtimev1.CgroupDriver
	unsupported bool // report RuntimeConfig as unimplemented
	lastConfig  *runtimev1.PodSandboxConfig
}

func (m *cgroupMockCRI) Version(_ context.Context, _ *runtimev1.VersionRequest) (*runtimev1.VersionResponse, error) {
	return &runtimev1.VersionResponse{RuntimeName: "mock", RuntimeApiVersion: "v1"}, nil
}

func (m *cgroupMockCRI) RuntimeConfig(_ context.Context, _ *runtimev1.RuntimeConfigRequest) (*runtimev1.RuntimeConfigResponse, error) {
	if m.unsupported {
		return nil, fmt.Errorf("mock runtime: %w", status.Error(codes.Unimplemented, "RuntimeConfig is not implemented"))
	}
	return &runtimev1.RuntimeConfigResponse{
		Linux: &runtimev1.LinuxRuntimeConfiguration{CgroupDriver: m.driver},
	}, nil
}

func (m *cgroupMockCRI) RunPodSandbox(_ context.Context, req *runtimev1.RunPodSandboxRequest) (*runtimev1.RunPodSandboxResponse, error) {
	m.lastConfig = req.GetConfig()
	return &runtimev1.RunPodSandboxResponse{PodSandboxId: "pod-1"}, nil
}

func startCgroupMock(t *testing.T, mock *cgroupMockCRI) *Client {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "cri.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	runtimev1.RegisterRuntimeServiceServer(srv, mock)
	runtimev1.RegisterImageServiceServer(srv, mock)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	c, err := Dial(context.Background(), sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestRunPodSandbox_SystemdCgroupParent covers the sandbox failure a systemd
// runtime returns for the CRI default: "expected cgroupsPath to be of format
// slice:prefix:name".
func TestRunPodSandbox_SystemdCgroupParent(t *testing.T) {
	mock := &cgroupMockCRI{driver: runtimev1.CgroupDriver_SYSTEMD}
	c := startCgroupMock(t, mock)

	cfg := BuildPodConfig("proj", "web", testService(), "v1", PodNetworkHost)
	if _, err := c.RunPodSandbox(context.Background(), cfg); err != nil {
		t.Fatalf("RunPodSandbox: %v", err)
	}
	if got := mock.lastConfig.GetLinux().GetCgroupParent(); got != SystemdSlice {
		t.Errorf("CgroupParent = %q, want %q", got, SystemdSlice)
	}
}

func TestRunPodSandbox_CgroupfsLeavesParentEmpty(t *testing.T) {
	mock := &cgroupMockCRI{driver: runtimev1.CgroupDriver_CGROUPFS}
	c := startCgroupMock(t, mock)

	cfg := BuildPodConfig("proj", "web", testService(), "v1", PodNetworkHost)
	if _, err := c.RunPodSandbox(context.Background(), cfg); err != nil {
		t.Fatalf("RunPodSandbox: %v", err)
	}
	if got := mock.lastConfig.GetLinux().GetCgroupParent(); got != "" {
		t.Errorf("CgroupParent = %q, want empty on the cgroupfs driver", got)
	}
}

// TestRunPodSandbox_RuntimeConfigUnsupported keeps older runtimes, which do not
// implement RuntimeConfig, on their previous behaviour.
func TestRunPodSandbox_RuntimeConfigUnsupported(t *testing.T) {
	mock := &cgroupMockCRI{unsupported: true}
	c := startCgroupMock(t, mock)

	cfg := BuildPodConfig("proj", "web", testService(), "v1", PodNetworkHost)
	if _, err := c.RunPodSandbox(context.Background(), cfg); err != nil {
		t.Fatalf("RunPodSandbox: %v", err)
	}
	if got := mock.lastConfig.GetLinux().GetCgroupParent(); got != "" {
		t.Errorf("CgroupParent = %q, want empty when the runtime cannot be asked", got)
	}
}

// TestRunPodSandbox_CallerCgroupParentWins keeps an explicit choice intact.
func TestRunPodSandbox_CallerCgroupParentWins(t *testing.T) {
	mock := &cgroupMockCRI{driver: runtimev1.CgroupDriver_SYSTEMD}
	c := startCgroupMock(t, mock)

	cfg := BuildPodConfig("proj", "web", testService(), "v1", PodNetworkHost)
	cfg.Linux.CgroupParent = "custom.slice"
	if _, err := c.RunPodSandbox(context.Background(), cfg); err != nil {
		t.Fatalf("RunPodSandbox: %v", err)
	}
	if got := mock.lastConfig.GetLinux().GetCgroupParent(); got != "custom.slice" {
		t.Errorf("CgroupParent = %q, want custom.slice", got)
	}
}
