package client_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sync"
	"testing"

	"github.com/systemstart/nix-compose/pkg/cni"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/eval"
	"github.com/systemstart/nix-compose/pkg/orchestrate/client"
	"github.com/systemstart/nix-compose/pkg/orchestrate/server"
	"github.com/systemstart/nix-compose/pkg/volumes"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// mockCRI implements RuntimeService and ImageService for testing.
type mockCRI struct {
	runtimev1.UnimplementedRuntimeServiceServer
	runtimev1.UnimplementedImageServiceServer

	mu         sync.Mutex
	pods       map[string]*runtimev1.PodSandbox
	containers map[string]*runtimev1.Container
	nextPodID  int
	nextCtrID  int
}

func newMockCRI() *mockCRI {
	return &mockCRI{
		pods:       make(map[string]*runtimev1.PodSandbox),
		containers: make(map[string]*runtimev1.Container),
	}
}

func (m *mockCRI) Version(_ context.Context, _ *runtimev1.VersionRequest) (*runtimev1.VersionResponse, error) {
	return &runtimev1.VersionResponse{
		Version:           "0.1.0",
		RuntimeName:       "mock-runtime",
		RuntimeVersion:    "2.0.0",
		RuntimeApiVersion: "v1",
	}, nil
}

func (m *mockCRI) PullImage(_ context.Context, req *runtimev1.PullImageRequest) (*runtimev1.PullImageResponse, error) {
	return &runtimev1.PullImageResponse{ImageRef: req.Image.Image}, nil
}

func (m *mockCRI) ImageStatus(_ context.Context, req *runtimev1.ImageStatusRequest) (*runtimev1.ImageStatusResponse, error) {
	return &runtimev1.ImageStatusResponse{
		Image: &runtimev1.Image{
			Id:   "sha256:" + req.Image.Image,
			Spec: &runtimev1.ImageSpec{Image: req.Image.Image},
		},
	}, nil
}

func (m *mockCRI) RunPodSandbox(_ context.Context, req *runtimev1.RunPodSandboxRequest) (*runtimev1.RunPodSandboxResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextPodID++
	id := fmt.Sprintf("pod-%d", m.nextPodID)
	m.pods[id] = &runtimev1.PodSandbox{
		Id:       id,
		State:    runtimev1.PodSandboxState_SANDBOX_READY,
		Metadata: req.Config.Metadata,
		Labels:   req.Config.Labels,
	}
	return &runtimev1.RunPodSandboxResponse{PodSandboxId: id}, nil
}

func (m *mockCRI) StopPodSandbox(_ context.Context, req *runtimev1.StopPodSandboxRequest) (*runtimev1.StopPodSandboxResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pod, ok := m.pods[req.PodSandboxId]; ok {
		pod.State = runtimev1.PodSandboxState_SANDBOX_NOTREADY
	}
	return &runtimev1.StopPodSandboxResponse{}, nil
}

func (m *mockCRI) RemovePodSandbox(_ context.Context, req *runtimev1.RemovePodSandboxRequest) (*runtimev1.RemovePodSandboxResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pods, req.PodSandboxId)
	return &runtimev1.RemovePodSandboxResponse{}, nil
}

func (m *mockCRI) ListPodSandbox(_ context.Context, req *runtimev1.ListPodSandboxRequest) (*runtimev1.ListPodSandboxResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []*runtimev1.PodSandbox
	for _, pod := range m.pods {
		if matchLabels(pod.Labels, req.Filter.GetLabelSelector()) {
			items = append(items, pod)
		}
	}
	return &runtimev1.ListPodSandboxResponse{Items: items}, nil
}

func (m *mockCRI) CreateContainer(_ context.Context, req *runtimev1.CreateContainerRequest) (*runtimev1.CreateContainerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextCtrID++
	id := fmt.Sprintf("ctr-%d", m.nextCtrID)
	m.containers[id] = &runtimev1.Container{
		Id:           id,
		PodSandboxId: req.PodSandboxId,
		State:        runtimev1.ContainerState_CONTAINER_CREATED,
		Metadata:     req.Config.Metadata,
		Labels:       req.Config.Labels,
		Image:        req.Config.Image,
	}
	return &runtimev1.CreateContainerResponse{ContainerId: id}, nil
}

func (m *mockCRI) StartContainer(_ context.Context, req *runtimev1.StartContainerRequest) (*runtimev1.StartContainerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctr, ok := m.containers[req.ContainerId]; ok {
		ctr.State = runtimev1.ContainerState_CONTAINER_RUNNING
	}
	return &runtimev1.StartContainerResponse{}, nil
}

func (m *mockCRI) StopContainer(_ context.Context, req *runtimev1.StopContainerRequest) (*runtimev1.StopContainerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctr, ok := m.containers[req.ContainerId]; ok {
		ctr.State = runtimev1.ContainerState_CONTAINER_EXITED
	}
	return &runtimev1.StopContainerResponse{}, nil
}

func (m *mockCRI) RemoveContainer(_ context.Context, req *runtimev1.RemoveContainerRequest) (*runtimev1.RemoveContainerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.containers, req.ContainerId)
	return &runtimev1.RemoveContainerResponse{}, nil
}

func (m *mockCRI) ListContainers(_ context.Context, req *runtimev1.ListContainersRequest) (*runtimev1.ListContainersResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []*runtimev1.Container
	for _, ctr := range m.containers {
		if req.Filter.GetPodSandboxId() == "" || ctr.PodSandboxId == req.Filter.GetPodSandboxId() {
			items = append(items, ctr)
		}
	}
	return &runtimev1.ListContainersResponse{Containers: items}, nil
}

func (m *mockCRI) ExecSync(_ context.Context, req *runtimev1.ExecSyncRequest) (*runtimev1.ExecSyncResponse, error) {
	return &runtimev1.ExecSyncResponse{
		Stdout:   []byte("hello from " + req.ContainerId),
		Stderr:   nil,
		ExitCode: 0,
	}, nil
}

func matchLabels(podLabels, selector map[string]string) bool {
	for k, v := range selector {
		if podLabels[k] != v {
			return false
		}
	}
	return true
}

// startMockCRI starts a gRPC CRI mock on a unix socket and returns the socket path.
func startMockCRI(t *testing.T) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "cri.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	mock := newMockCRI()
	runtimev1.RegisterRuntimeServiceServer(srv, mock)
	runtimev1.RegisterImageServiceServer(srv, mock)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)
	return sock
}

// testEnv bundles a CRI client, orchestrate server, and the typed Client for tests.
type testEnv struct {
	criClient *cri.Client
	orchSrv   *server.Server
	client    *client.Client
}

func setupTestEnv(t *testing.T) *testEnv {
	t.Helper()

	// Start mock CRI.
	criSock := startMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, criSock)
	if err != nil {
		t.Fatalf("dial CRI: %v", err)
	}
	t.Cleanup(func() { _ = criClient.Close() })

	// Create orchestrate server on its own socket.
	orchSock := filepath.Join(t.TempDir(), "orch.sock")
	volStore := &volumes.Store{Root: t.TempDir()}
	cniStore := &cni.Store{
		ConfDir:    t.TempDir(),
		PluginDirs: []string{},
	}

	srv := server.New(server.Config{
		CRIClient: criClient,
		CNIStore:  cniStore,
		VolStore:  volStore,
		LogBase:   t.TempDir(),
		DBPath:    filepath.Join(t.TempDir(), "state.bolt"),
	})

	lis, err := net.Listen("unix", orchSock)
	if err != nil {
		t.Fatalf("listen orch: %v", err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	// Connect a typed client via Dial — but since Dial does a health check,
	// we need to use the raw grpc.NewClient first to build our client.
	conn, err := grpc.NewClient(
		"unix://"+orchSock,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial orch: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// Use client.Dial for the actual typed client.
	c, err := client.Dial(ctx, orchSock)
	if err != nil {
		t.Fatalf("client.Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	return &testEnv{
		criClient: criClient,
		orchSrv:   srv,
		client:    c,
	}
}

// makeCompositionJSON creates a simple composition JSON for testing.
func makeCompositionJSON(t *testing.T, services map[string]eval.Service) []byte {
	t.Helper()
	comp := eval.Composition{
		Services: services,
	}
	data, err := json.Marshal(comp)
	if err != nil {
		t.Fatalf("marshal composition: %v", err)
	}
	return data
}

func TestClientHealth(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	resp, err := env.client.Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !resp.Healthy {
		t.Error("expected healthy=true")
	}
	if resp.RuntimeName != "mock-runtime" {
		t.Errorf("RuntimeName = %q, want %q", resp.RuntimeName, "mock-runtime")
	}
	if resp.RuntimeVersion != "2.0.0" {
		t.Errorf("RuntimeVersion = %q, want %q", resp.RuntimeVersion, "2.0.0")
	}
}

func TestClientPlan(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})

	resp, err := env.client.Plan(ctx, compJSON, "testproject", false)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if resp.Creates == 0 {
		t.Error("expected at least one create action")
	}

	if len(resp.Actions) < 3 {
		t.Errorf("expected at least 3 actions, got %d", len(resp.Actions))
	}

	// All actions should be creates since this is a fresh state.
	for _, a := range resp.Actions {
		if a.Type != "create" {
			t.Errorf("expected all actions to be 'create', got %q for %s", a.Type, a.ResourceId)
		}
	}
}

func TestClientApply(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})

	resp, err := env.client.Apply(ctx, compJSON, "applytest", false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if resp.Creates == 0 {
		t.Error("expected creates > 0")
	}

	// Verify state has rollouts after apply.
	stateResp, err := env.client.State(ctx)
	if err != nil {
		t.Fatalf("State: %v", err)
	}

	if len(stateResp.Rollouts) == 0 {
		t.Error("expected rollouts after apply")
	}
}

func TestClientTeardown(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Teardown should succeed even with no running services.
	err := env.client.Teardown(ctx, "teardowntest", 5, false, nil)
	if err != nil {
		t.Fatalf("Teardown: %v", err)
	}
}

func TestClientState(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	resp, err := env.client.State(ctx)
	if err != nil {
		t.Fatalf("State: %v", err)
	}

	// Empty state is expected (no rollouts).
	if len(resp.Rollouts) != 0 {
		t.Errorf("expected 0 rollouts, got %d", len(resp.Rollouts))
	}
}

func TestClientExecSync(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// First apply to create containers.
	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})

	_, err := env.client.Apply(ctx, compJSON, "exectest", false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Now exec in the container.
	resp, err := env.client.ExecSync(ctx, "exectest", "web", []string{"echo", "hello"}, 0)
	if err != nil {
		t.Fatalf("ExecSync: %v", err)
	}

	if resp.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", resp.ExitCode)
	}
	if len(resp.Stdout) == 0 {
		t.Error("expected non-empty stdout")
	}
}

func TestClientLogs(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	stream, err := env.client.Logs(ctx, "test", []string{"web"}, client.LogsOpts{})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}

	// Should get no entries (log files don't exist) — EOF.
	_, err = stream.Recv()
	if err == nil {
		t.Error("expected EOF from empty log stream")
	}
	if err != nil && err != io.EOF {
		// gRPC stream ending is acceptable.
		t.Logf("got expected stream end error: %v", err)
	}
}

func TestClientDialInvalidSocket(t *testing.T) {
	ctx := context.Background()
	_, err := client.Dial(ctx, "/tmp/nonexistent-orch-test.sock")
	if err == nil {
		t.Error("expected error dialing invalid socket")
	}
}

func TestClientDrift_Empty(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	resp, err := env.client.Drift(ctx, "")
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}

	if len(resp.Items) != 0 {
		t.Errorf("expected 0 drift items, got %d", len(resp.Items))
	}
}

func TestClientDrift_AfterApply(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})

	_, err := env.client.Apply(ctx, compJSON, "drifttest", false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Drift check should not error.
	resp, err := env.client.Drift(ctx, "")
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	_ = resp
}

func TestClientRollback_NotFound(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	_, err := env.client.Rollback(ctx, "nonexistent-id", true)
	if err == nil {
		t.Error("expected error for nonexistent deployment")
	}
}

func TestClientClose(t *testing.T) {
	env := setupTestEnv(t)
	// Close should be safe to call (it's also called in cleanup).
	err := env.client.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
}
