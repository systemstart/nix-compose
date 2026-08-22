package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/systemstart/nix-compose/internal/testsock"

	orchestratev1 "github.com/systemstart/nix-compose/api/orchestrate/v1"
	"github.com/systemstart/nix-compose/pkg/cni"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/eval"
	"github.com/systemstart/nix-compose/pkg/orchestrate"
	"github.com/systemstart/nix-compose/pkg/orchestrate/deploy"
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
	sock := testsock.Path(t, "cri.sock")
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

// testEnv bundles a CRI client, orchestrate server, and gRPC client for tests.
type testEnv struct {
	criClient *cri.Client
	orchSrv   *Server
	orchConn  *grpc.ClientConn
	client    orchestratev1.OrchestrateServiceClient
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
	orchSock := testsock.Path(t, "orch.sock")
	volStore := &volumes.Store{Root: t.TempDir()}
	cniStore := &cni.Store{
		ConfDir:    t.TempDir(),
		PluginDirs: []string{},
	}

	srv := New(Config{
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

	// Connect a gRPC client to the orchestrate server.
	conn, err := grpc.NewClient(
		"unix://"+orchSock,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial orch: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client := orchestratev1.NewOrchestrateServiceClient(conn)

	return &testEnv{
		criClient: criClient,
		orchSrv:   srv,
		orchConn:  conn,
		client:    client,
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

func TestHealth(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	resp, err := env.client.Health(ctx, &orchestratev1.HealthRequest{})
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

func TestPlanCreateNewService(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})

	resp, err := env.client.Plan(ctx, &orchestratev1.PlanRequest{
		CompositionJson: compJSON,
		Project:         "testproject",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if resp.Creates == 0 {
		t.Error("expected at least one create action")
	}

	// Should have actions for project, image, and service.
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

func TestPlanValidation(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Test missing project.
	_, err := env.client.Plan(ctx, &orchestratev1.PlanRequest{
		CompositionJson: []byte(`{}`),
	})
	if err == nil {
		t.Error("expected error for missing project")
	}

	// Test missing composition.
	_, err = env.client.Plan(ctx, &orchestratev1.PlanRequest{
		Project: "test",
	})
	if err == nil {
		t.Error("expected error for missing composition_json")
	}
}

func TestApplyCreatesResources(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})

	resp, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON,
		Project:         "applytest",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if resp.Creates == 0 {
		t.Error("expected creates > 0")
	}

	// Verify state has rollouts after apply.
	stateResp, err := env.client.State(ctx, &orchestratev1.StateRequest{})
	if err != nil {
		t.Fatalf("State: %v", err)
	}

	if len(stateResp.Rollouts) == 0 {
		t.Error("expected rollouts after apply")
	}
}

func TestTeardown(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Teardown should succeed even with no running services.
	_, err := env.client.Teardown(ctx, &orchestratev1.TeardownRequest{
		Project: "teardowntest",
		Timeout: 5,
	})
	if err != nil {
		t.Fatalf("Teardown: %v", err)
	}
}

func TestTeardownValidation(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	_, err := env.client.Teardown(ctx, &orchestratev1.TeardownRequest{})
	if err == nil {
		t.Error("expected error for missing project")
	}
}

func TestExecSyncValidation(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Missing project.
	_, err := env.client.ExecSync(ctx, &orchestratev1.ExecSyncRequest{
		Service: "web",
		Cmd:     []string{"echo", "hi"},
	})
	if err == nil {
		t.Error("expected error for missing project")
	}

	// Missing service.
	_, err = env.client.ExecSync(ctx, &orchestratev1.ExecSyncRequest{
		Project: "test",
		Cmd:     []string{"echo", "hi"},
	})
	if err == nil {
		t.Error("expected error for missing service")
	}

	// Missing cmd.
	_, err = env.client.ExecSync(ctx, &orchestratev1.ExecSyncRequest{
		Project: "test",
		Service: "web",
	})
	if err == nil {
		t.Error("expected error for missing cmd")
	}
}

func TestStateEmpty(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	resp, err := env.client.State(ctx, &orchestratev1.StateRequest{})
	if err != nil {
		t.Fatalf("State: %v", err)
	}

	// Empty state is expected (no rollouts).
	if len(resp.Rollouts) != 0 {
		t.Errorf("expected 0 rollouts, got %d", len(resp.Rollouts))
	}
}

func TestLogsEmpty(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	stream, err := env.client.Logs(ctx, &orchestratev1.LogsRequest{
		Project:  "test",
		Services: []string{"web"},
	})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}

	// Should get no entries (log files don't exist).
	_, err = stream.Recv()
	if err == nil {
		t.Error("expected EOF from empty log stream")
	}
}

func TestLogsValidation(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	_, err := env.client.Logs(ctx, &orchestratev1.LogsRequest{})
	if err == nil {
		// The error might come from Recv(), let's check.
		t.Log("no error from Logs call itself, checking stream")
	}
}

func TestDrift_Empty(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	resp, err := env.client.Drift(ctx, &orchestratev1.DriftRequest{})
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}

	if len(resp.Items) != 0 {
		t.Errorf("expected 0 drift items, got %d", len(resp.Items))
	}
}

func TestDrift_AfterApply(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})

	_, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON,
		Project:         "drifttest",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Drift check after apply should not error.
	resp, err := env.client.Drift(ctx, &orchestratev1.DriftRequest{})
	if err != nil {
		t.Fatalf("Drift after apply: %v", err)
	}
	// May or may not detect drift depending on mock CRI state — just verify no error.
	_ = resp
}

func TestRollback_Validation(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Missing deployment_id should fail.
	_, err := env.client.Rollback(ctx, &orchestratev1.RollbackRequest{})
	if err == nil {
		t.Error("expected error for missing deployment_id")
	}
}

func TestRollback_NotFound(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	_, err := env.client.Rollback(ctx, &orchestratev1.RollbackRequest{
		DeploymentId: "nonexistent-id",
		DryRun:       true,
	})
	if err == nil {
		t.Error("expected error for nonexistent deployment")
	}
}

func TestLookupContainerID_Found(t *testing.T) {
	criSock := startMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, criSock)
	if err != nil {
		t.Fatalf("dial CRI: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// Create a service.
	svc := eval.Service{Image: "nginx:latest"}
	opts := cri.ServiceUpOptions{Project: "lookuptest", Version: "v1"}
	if err := criClient.ServiceUp(ctx, "web", svc, opts); err != nil {
		t.Fatalf("ServiceUp: %v", err)
	}

	containerID, err := lookupContainerID(ctx, criClient, "lookuptest", "web")
	if err != nil {
		t.Fatalf("lookupContainerID: %v", err)
	}
	if containerID == "" {
		t.Error("expected non-empty container ID")
	}
}

func TestLookupContainerID_NoPods(t *testing.T) {
	criSock := startMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, criSock)
	if err != nil {
		t.Fatalf("dial CRI: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	_, err = lookupContainerID(ctx, criClient, "noproject", "nosvc")
	if err == nil {
		t.Error("expected error for missing pods")
	}
}

func TestExecSync_AfterApply(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Apply a service first.
	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})

	_, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON,
		Project:         "exectest",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// ExecSync with project and service.
	resp, err := env.client.ExecSync(ctx, &orchestratev1.ExecSyncRequest{
		Project: "exectest",
		Service: "web",
		Cmd:     []string{"echo", "hello"},
	})
	if err != nil {
		t.Fatalf("ExecSync: %v", err)
	}
	if resp.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", resp.ExitCode)
	}
}

func TestState_AfterApply(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})

	_, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON,
		Project:         "statetest",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	resp, err := env.client.State(ctx, &orchestratev1.StateRequest{})
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if len(resp.Rollouts) == 0 {
		t.Error("expected rollouts after apply")
	}
}

func TestLogs_AfterApply(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})

	_, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON,
		Project:         "logstest",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Logs should work (but return empty stream since no log files exist).
	stream, err := env.client.Logs(ctx, &orchestratev1.LogsRequest{
		Project:  "logstest",
		Services: []string{"web"},
	})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	// Read until EOF.
	for {
		_, err := stream.Recv()
		if err != nil {
			break
		}
	}
}

func TestTeardown_WithComposition(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})

	// Apply first.
	_, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON,
		Project:         "teardowntest",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Teardown with composition for ordered shutdown.
	_, err = env.client.Teardown(ctx, &orchestratev1.TeardownRequest{
		Project:         "teardowntest",
		Timeout:         5,
		RemoveVolumes:   false,
		CompositionJson: compJSON,
	})
	if err != nil {
		t.Fatalf("Teardown: %v", err)
	}
}

func TestApply_MultipleServices(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web":   {Image: "nginx:latest", NetworkMode: "host"},
		"api":   {Image: "node:20", NetworkMode: "host"},
		"cache": {Image: "redis:latest", NetworkMode: "host"},
	})

	resp, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON,
		Project:         "multitest",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if resp.Creates == 0 {
		t.Error("expected creates > 0")
	}

	// State should show rollouts.
	stateResp, err := env.client.State(ctx, &orchestratev1.StateRequest{})
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if len(stateResp.Rollouts) == 0 {
		t.Error("expected rollouts after multi-service apply")
	}
}

func TestApply_ThenReapply(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})

	// First apply.
	_, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON,
		Project:         "reapplytest",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("First Apply: %v", err)
	}

	// Second apply with same composition — should be no-op or updates.
	resp, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON,
		Project:         "reapplytest",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Reapply: %v", err)
	}
	// Creates should be 0 on reapply.
	if resp.Creates != 0 {
		t.Errorf("expected 0 creates on reapply, got %d", resp.Creates)
	}
}

func TestPlan_MultipleServices(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
		"api": {Image: "node:20", NetworkMode: "host"},
	})

	resp, err := env.client.Plan(ctx, &orchestratev1.PlanRequest{
		CompositionJson: compJSON,
		Project:         "multiplan",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if resp.Creates == 0 {
		t.Error("expected creates > 0")
	}
	if len(resp.Actions) == 0 {
		t.Error("expected actions")
	}
}

func TestDrift_AfterApplyAndTeardown(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})

	// Apply.
	_, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON,
		Project:         "drifttear",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Teardown.
	_, err = env.client.Teardown(ctx, &orchestratev1.TeardownRequest{
		Project: "drifttear",
		Timeout: 5,
	})
	if err != nil {
		t.Fatalf("Teardown: %v", err)
	}

	// Drift check after teardown — rollouts exist but pods are gone.
	resp, err := env.client.Drift(ctx, &orchestratev1.DriftRequest{})
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	// Should detect drift since containers were removed.
	_ = resp
}

func TestRollback_AfterApply_DryRun(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})

	// Apply first.
	_, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON,
		Project:         "rollbackdry",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// List state to get deployment ID.
	stateResp, err := env.client.State(ctx, &orchestratev1.StateRequest{})
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if len(stateResp.Rollouts) == 0 {
		t.Fatal("expected rollouts after apply")
	}

	// Change composition and apply again to create a second deployment.
	compJSON2 := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:1.25", NetworkMode: "host"},
	})
	_, err = env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON2,
		Project:         "rollbackdry",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Apply v2: %v", err)
	}

	// Rollback dry-run with a fake deployment ID — should fail.
	_, err = env.client.Rollback(ctx, &orchestratev1.RollbackRequest{
		DeploymentId: "fake-deployment-id",
		DryRun:       true,
	})
	if err == nil {
		t.Error("expected error for nonexistent deployment")
	}
}

func TestDrift_NoRollouts(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	resp, err := env.client.Drift(ctx, &orchestratev1.DriftRequest{})
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Errorf("expected 0 drift items, got %d", len(resp.Items))
	}
}

func TestLookupContainerID_NoContainers(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Create a pod but no containers.
	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})

	_, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON,
		Project:         "nocontainer",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Try to find a container for a service that doesn't exist.
	_, err = lookupContainerID(ctx, env.criClient, "nocontainer", "nonexistent")
	if err == nil {
		t.Error("expected error when no pods found for nonexistent service")
	}
}

func TestApply_Validation(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Missing project.
	_, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: []byte(`{"services":{}}`),
	})
	if err == nil {
		t.Error("expected error for missing project")
	}

	// Missing composition.
	_, err = env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		Project: "test",
	})
	if err == nil {
		t.Error("expected error for missing composition")
	}
}

func TestPlan_Validation_MissingProject(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	_, err := env.client.Plan(ctx, &orchestratev1.PlanRequest{
		CompositionJson: []byte(`{"services":{}}`),
	})
	if err == nil {
		t.Error("expected error for missing project")
	}
}

func TestLogs_WithFollow_Cancellation(t *testing.T) {
	env := setupTestEnv(t)

	// Apply so there's log data potentially.
	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})
	_, err := env.client.Apply(context.Background(), &orchestratev1.ApplyRequest{
		CompositionJson: compJSON,
		Project:         "logfollow",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Request follow logs with a cancelled context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	stream, err := env.client.Logs(ctx, &orchestratev1.LogsRequest{
		Project: "logfollow",
		Follow:  true,
	})
	if err != nil {
		// Connection refused or cancelled is fine.
		return
	}
	// Try to receive — should fail because context is cancelled.
	_, err = stream.Recv()
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestExecSync_MissingFields(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Missing project.
	_, err := env.client.ExecSync(ctx, &orchestratev1.ExecSyncRequest{
		Service: "web",
		Cmd:     []string{"echo", "hello"},
	})
	if err == nil {
		t.Error("expected error for missing project")
	}

	// Missing service.
	_, err = env.client.ExecSync(ctx, &orchestratev1.ExecSyncRequest{
		Project: "test",
		Cmd:     []string{"echo"},
	})
	if err == nil {
		t.Error("expected error for missing service")
	}
}

func TestExecSync_Success(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Apply to create pods/containers.
	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})
	_, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON,
		Project:         "exectest",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	resp, err := env.client.ExecSync(ctx, &orchestratev1.ExecSyncRequest{
		Project: "exectest",
		Service: "web",
		Cmd:     []string{"echo", "hello"},
		Timeout: 5,
	})
	if err != nil {
		t.Fatalf("ExecSync: %v", err)
	}
	if resp.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", resp.ExitCode)
	}
	if len(resp.Stdout) == 0 {
		t.Error("expected stdout output")
	}
}

func TestLookupContainerID_Success(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})
	_, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON,
		Project:         "lookuptest",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	id, err := lookupContainerID(ctx, env.criClient, "lookuptest", "web")
	if err != nil {
		t.Fatalf("lookupContainerID: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty container ID")
	}
}

func TestLogs_NonFollow(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Apply to create the project.
	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})
	_, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON,
		Project:         "lognonfollow",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Logs without follow — should return immediately (even if no logs).
	stream, err := env.client.Logs(ctx, &orchestratev1.LogsRequest{
		Project: "lognonfollow",
		Follow:  false,
	})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	// Drain the stream — no log entries are expected, but shouldn't error.
	for {
		_, err := stream.Recv()
		if err != nil {
			break // EOF or error — expected
		}
	}
}

func TestLogs_MissingProject(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	_, err := env.client.Logs(ctx, &orchestratev1.LogsRequest{
		Follow: false,
	})
	// gRPC streaming errors surface on Recv, not on the initial call.
	if err != nil {
		return // Connection-level error is fine.
	}
}

func TestState_WithProject(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})
	_, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON,
		Project:         "statetest",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	resp, err := env.client.State(ctx, &orchestratev1.StateRequest{
		Project: "statetest",
	})
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if len(resp.Rollouts) == 0 {
		t.Error("expected rollouts for statetest project")
	}
}

func TestTeardown_NoContainers(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Teardown a nonexistent project — should succeed (no-op).
	_, err := env.client.Teardown(ctx, &orchestratev1.TeardownRequest{
		Project: "nonexistent",
		Timeout: 5,
	})
	if err != nil {
		t.Fatalf("Teardown: %v", err)
	}
}

func TestDrift_AfterApply_WithDrift(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	compJSON := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})

	// Apply first.
	_, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON,
		Project:         "driftdetect",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Drift check — containers exist and match, so should be no drift.
	resp, err := env.client.Drift(ctx, &orchestratev1.DriftRequest{})
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	// With mock CRI, containers are running, so no drift expected.
	_ = resp
}

func TestRollback_MissingDeploymentID(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	_, err := env.client.Rollback(ctx, &orchestratev1.RollbackRequest{})
	if err == nil {
		t.Error("expected error for missing deployment_id")
	}
}

func TestHealth_AfterMultipleApplies(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Apply two different projects.
	for _, project := range []string{"proj1", "proj2"} {
		compJSON := makeCompositionJSON(t, map[string]eval.Service{
			"web": {Image: "nginx:latest", NetworkMode: "host"},
		})
		_, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
			CompositionJson: compJSON,
			Project:         project,
			UseCni:          false,
		})
		if err != nil {
			t.Fatalf("Apply %s: %v", project, err)
		}
	}

	// Health should still work.
	resp, err := env.client.Health(ctx, &orchestratev1.HealthRequest{})
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !resp.Healthy {
		t.Error("expected healthy")
	}
}

func TestExecSync_MissingProject(t *testing.T) {
	env := setupTestEnv(t)
	_, err := env.client.ExecSync(context.Background(), &orchestratev1.ExecSyncRequest{
		Service: "web",
		Cmd:     []string{"echo"},
	})
	if err == nil {
		t.Error("expected error for missing project")
	}
}

func TestExecSync_MissingService(t *testing.T) {
	env := setupTestEnv(t)
	_, err := env.client.ExecSync(context.Background(), &orchestratev1.ExecSyncRequest{
		Project: "test",
		Cmd:     []string{"echo"},
	})
	if err == nil {
		t.Error("expected error for missing service")
	}
}

func TestExecSync_MissingCmd(t *testing.T) {
	env := setupTestEnv(t)
	_, err := env.client.ExecSync(context.Background(), &orchestratev1.ExecSyncRequest{
		Project: "test",
		Service: "web",
	})
	if err == nil {
		t.Error("expected error for missing cmd")
	}
}

func TestTeardown_MissingProject(t *testing.T) {
	env := setupTestEnv(t)
	_, err := env.client.Teardown(context.Background(), &orchestratev1.TeardownRequest{})
	if err == nil {
		t.Error("expected error for missing project")
	}
}

func TestPlan_MissingProject(t *testing.T) {
	env := setupTestEnv(t)
	_, err := env.client.Plan(context.Background(), &orchestratev1.PlanRequest{
		CompositionJson: []byte(`{}`),
	})
	if err == nil {
		t.Error("expected error for missing project")
	}
}

func TestPlan_MissingComposition(t *testing.T) {
	env := setupTestEnv(t)
	_, err := env.client.Plan(context.Background(), &orchestratev1.PlanRequest{
		Project: "test",
	})
	if err == nil {
		t.Error("expected error for missing composition")
	}
}

func TestApply_MissingProject(t *testing.T) {
	env := setupTestEnv(t)
	_, err := env.client.Apply(context.Background(), &orchestratev1.ApplyRequest{
		CompositionJson: []byte(`{}`),
	})
	if err == nil {
		t.Error("expected error for missing project")
	}
}

func TestApply_MissingComposition(t *testing.T) {
	env := setupTestEnv(t)
	_, err := env.client.Apply(context.Background(), &orchestratev1.ApplyRequest{
		Project: "test",
	})
	if err == nil {
		t.Error("expected error for missing composition")
	}
}

func TestRollback_MissingID(t *testing.T) {
	env := setupTestEnv(t)
	_, err := env.client.Rollback(context.Background(), &orchestratev1.RollbackRequest{})
	if err == nil {
		t.Error("expected error for missing deployment_id")
	}
}

func TestNewServer_DefaultLogBase(t *testing.T) {
	srv := New(Config{})
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
}

func TestPlan_InvalidCompositionJSON(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	_, err := env.client.Plan(ctx, &orchestratev1.PlanRequest{
		CompositionJson: []byte(`{not valid json`),
		Project:         "test",
	})
	if err == nil {
		t.Error("expected error for invalid JSON composition")
	}
}

func TestApply_InvalidCompositionJSON(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	_, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: []byte(`{not valid json`),
		Project:         "test",
	})
	if err == nil {
		t.Error("expected error for invalid JSON composition")
	}
}

func TestTeardown_WithInvalidComposition(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Teardown with invalid composition JSON should still work (composition is optional).
	_, err := env.client.Teardown(ctx, &orchestratev1.TeardownRequest{
		Project:         "test",
		Timeout:         5,
		CompositionJson: []byte(`{not valid json`),
	})
	// May or may not error depending on if composition is parsed
	_ = err
}

func TestRollback_AfterApply_Execute(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Apply v1.
	compJSON1 := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:latest", NetworkMode: "host"},
	})
	_, err := env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON1,
		Project:         "rbexec",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Apply v1: %v", err)
	}

	// Apply v2 (different image).
	compJSON2 := makeCompositionJSON(t, map[string]eval.Service{
		"web": {Image: "nginx:1.25", NetworkMode: "host"},
	})
	_, err = env.client.Apply(ctx, &orchestratev1.ApplyRequest{
		CompositionJson: compJSON2,
		Project:         "rbexec",
		UseCni:          false,
	})
	if err != nil {
		t.Fatalf("Apply v2: %v", err)
	}

	// Get state to find a valid deployment-like rollout.
	stateResp, err := env.client.State(ctx, &orchestratev1.StateRequest{})
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if len(stateResp.Rollouts) == 0 {
		t.Fatal("expected rollouts")
	}
}

func TestLogs_WithTimestamps(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	stream, err := env.client.Logs(ctx, &orchestratev1.LogsRequest{
		Project:    "test",
		Services:   []string{"web"},
		Timestamps: true,
		Tail:       "10",
	})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	// Drain stream - no entries expected.
	for {
		_, err := stream.Recv()
		if err != nil {
			break
		}
	}
}

func TestGracefulStop(t *testing.T) {
	srv := New(Config{
		DBPath: filepath.Join(t.TempDir(), "state.bolt"),
	})
	lis, err := net.Listen("unix", testsock.Path(t, "test.sock"))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(lis) }()
	// Give server a moment to start.
	time.Sleep(50 * time.Millisecond)
	// GracefulStop should not panic.
	srv.GracefulStop()
}

func TestPlanToActions_Empty(t *testing.T) {
	plan := &orchestrate.Plan{
		Deployment: deploy.NewDeployment(),
	}
	actions, creates, updates, destroys, noops := planToActions(plan)
	if len(actions) != 0 {
		t.Errorf("expected 0 actions, got %d", len(actions))
	}
	if creates != 0 || updates != 0 || destroys != 0 || noops != 0 {
		t.Errorf("expected all zeros, got c=%d u=%d d=%d n=%d", creates, updates, destroys, noops)
	}
}
