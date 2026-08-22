package cri

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/systemstart/nix-compose/internal/testsock"

	"google.golang.org/grpc"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// fullMockCRI implements RuntimeService and ImageService with in-memory state.
type fullMockCRI struct {
	runtimev1.UnimplementedRuntimeServiceServer
	runtimev1.UnimplementedImageServiceServer

	mu            sync.Mutex
	pods          map[string]*runtimev1.PodSandbox
	podConfigs    map[string]*runtimev1.PodSandboxConfig
	containers    map[string]*runtimev1.Container
	containerExit map[string]int32 // container ID → exit code (set via SetContainerExited)
	images        map[string]*runtimev1.Image
	execExitCodes map[string]int32  // container ID → exec exit code (default 0)
	podIPs        map[string]string // pod ID → IP address
	nextPodID     int
	nextCtrID     int
}

func newFullMockCRI() *fullMockCRI {
	return &fullMockCRI{
		pods:          make(map[string]*runtimev1.PodSandbox),
		podConfigs:    make(map[string]*runtimev1.PodSandboxConfig),
		containers:    make(map[string]*runtimev1.Container),
		containerExit: make(map[string]int32),
		images:        make(map[string]*runtimev1.Image),
		execExitCodes: make(map[string]int32),
		podIPs:        make(map[string]string),
	}
}

// SetContainerExited marks a container as exited with the given exit code.
func (m *fullMockCRI) SetContainerExited(containerID string, exitCode int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctr, ok := m.containers[containerID]; ok {
		ctr.State = runtimev1.ContainerState_CONTAINER_EXITED
	}
	m.containerExit[containerID] = exitCode
}

// SetExecExitCode configures the exit code for ExecSync on a container.
func (m *fullMockCRI) SetExecExitCode(containerID string, exitCode int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.execExitCodes[containerID] = exitCode
}

// SetPodIP sets the IP address returned by PodSandboxStatus for a pod.
func (m *fullMockCRI) SetPodIP(podID, ip string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.podIPs[podID] = ip
}

func (m *fullMockCRI) Version(_ context.Context, _ *runtimev1.VersionRequest) (*runtimev1.VersionResponse, error) {
	return &runtimev1.VersionResponse{
		Version:           "0.1.0",
		RuntimeName:       "mock",
		RuntimeVersion:    "1.0.0",
		RuntimeApiVersion: "v1",
	}, nil
}

func (m *fullMockCRI) PullImage(_ context.Context, req *runtimev1.PullImageRequest) (*runtimev1.PullImageResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ref := req.Image.Image
	m.images[ref] = &runtimev1.Image{
		Id:       "sha256:" + ref,
		RepoTags: []string{ref},
		Size:     1024,
		Spec:     &runtimev1.ImageSpec{Image: ref},
	}
	return &runtimev1.PullImageResponse{
		ImageRef: ref,
	}, nil
}

func (m *fullMockCRI) ListImages(_ context.Context, _ *runtimev1.ListImagesRequest) (*runtimev1.ListImagesResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var imgs []*runtimev1.Image
	for _, img := range m.images {
		imgs = append(imgs, img)
	}
	return &runtimev1.ListImagesResponse{Images: imgs}, nil
}

func (m *fullMockCRI) ImageStatus(_ context.Context, req *runtimev1.ImageStatusRequest) (*runtimev1.ImageStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	img := m.images[req.Image.Image]
	return &runtimev1.ImageStatusResponse{Image: img}, nil
}

func (m *fullMockCRI) RemoveImage(_ context.Context, req *runtimev1.RemoveImageRequest) (*runtimev1.RemoveImageResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.images, req.Image.Image)
	return &runtimev1.RemoveImageResponse{}, nil
}

func (m *fullMockCRI) RunPodSandbox(_ context.Context, req *runtimev1.RunPodSandboxRequest) (*runtimev1.RunPodSandboxResponse, error) {
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
	m.podConfigs[id] = req.Config
	return &runtimev1.RunPodSandboxResponse{PodSandboxId: id}, nil
}

func (m *fullMockCRI) StopPodSandbox(_ context.Context, req *runtimev1.StopPodSandboxRequest) (*runtimev1.StopPodSandboxResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pod, ok := m.pods[req.PodSandboxId]; ok {
		pod.State = runtimev1.PodSandboxState_SANDBOX_NOTREADY
	}
	return &runtimev1.StopPodSandboxResponse{}, nil
}

func (m *fullMockCRI) RemovePodSandbox(_ context.Context, req *runtimev1.RemovePodSandboxRequest) (*runtimev1.RemovePodSandboxResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pods, req.PodSandboxId)
	delete(m.podConfigs, req.PodSandboxId)
	return &runtimev1.RemovePodSandboxResponse{}, nil
}

func (m *fullMockCRI) ListPodSandbox(_ context.Context, req *runtimev1.ListPodSandboxRequest) (*runtimev1.ListPodSandboxResponse, error) {
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

func (m *fullMockCRI) CreateContainer(_ context.Context, req *runtimev1.CreateContainerRequest) (*runtimev1.CreateContainerResponse, error) {
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

func (m *fullMockCRI) StartContainer(_ context.Context, req *runtimev1.StartContainerRequest) (*runtimev1.StartContainerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctr, ok := m.containers[req.ContainerId]; ok {
		ctr.State = runtimev1.ContainerState_CONTAINER_RUNNING
	}
	return &runtimev1.StartContainerResponse{}, nil
}

func (m *fullMockCRI) StopContainer(_ context.Context, req *runtimev1.StopContainerRequest) (*runtimev1.StopContainerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctr, ok := m.containers[req.ContainerId]; ok {
		ctr.State = runtimev1.ContainerState_CONTAINER_EXITED
	}
	return &runtimev1.StopContainerResponse{}, nil
}

func (m *fullMockCRI) RemoveContainer(_ context.Context, req *runtimev1.RemoveContainerRequest) (*runtimev1.RemoveContainerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.containers, req.ContainerId)
	return &runtimev1.RemoveContainerResponse{}, nil
}

func (m *fullMockCRI) ListContainers(_ context.Context, req *runtimev1.ListContainersRequest) (*runtimev1.ListContainersResponse, error) {
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

func (m *fullMockCRI) ExecSync(_ context.Context, req *runtimev1.ExecSyncRequest) (*runtimev1.ExecSyncResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	exitCode, ok := m.execExitCodes[req.ContainerId]
	if !ok {
		exitCode = 0
	}
	return &runtimev1.ExecSyncResponse{
		ExitCode: exitCode,
	}, nil
}

func (m *fullMockCRI) Exec(_ context.Context, req *runtimev1.ExecRequest) (*runtimev1.ExecResponse, error) {
	return &runtimev1.ExecResponse{
		Url: fmt.Sprintf("http://127.0.0.1:0/exec/%s", req.ContainerId),
	}, nil
}

func (m *fullMockCRI) ContainerStatus(_ context.Context, req *runtimev1.ContainerStatusRequest) (*runtimev1.ContainerStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ctr, ok := m.containers[req.ContainerId]
	if !ok {
		return nil, fmt.Errorf("container %s not found", req.ContainerId)
	}
	exitCode := m.containerExit[req.ContainerId]
	return &runtimev1.ContainerStatusResponse{
		Status: &runtimev1.ContainerStatus{
			Id:       ctr.Id,
			State:    ctr.State,
			ExitCode: exitCode,
		},
	}, nil
}

func (m *fullMockCRI) PodSandboxStatus(_ context.Context, req *runtimev1.PodSandboxStatusRequest) (*runtimev1.PodSandboxStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pod, ok := m.pods[req.PodSandboxId]
	if !ok {
		return nil, fmt.Errorf("pod %s not found", req.PodSandboxId)
	}
	ip := m.podIPs[req.PodSandboxId]
	if ip == "" {
		ip = "10.0.0.1"
	}
	return &runtimev1.PodSandboxStatusResponse{
		Status: &runtimev1.PodSandboxStatus{
			Id:       pod.Id,
			State:    pod.State,
			Metadata: pod.Metadata,
			Network: &runtimev1.PodSandboxNetworkStatus{
				Ip: ip,
			},
		},
	}, nil
}

// matchLabels returns true if all selector labels are present in the target.
func matchLabels(target, selector map[string]string) bool {
	for k, v := range selector {
		if target[k] != v {
			return false
		}
	}
	return true
}

// startFullMockCRI starts a gRPC server with full CRI mock and returns the
// socket path and the mock instance for state inspection.
func startFullMockCRI(t *testing.T) (string, *fullMockCRI) {
	t.Helper()
	mock := newFullMockCRI()
	sock := testsock.Path(t, "cri.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	runtimev1.RegisterRuntimeServiceServer(srv, mock)
	runtimev1.RegisterImageServiceServer(srv, mock)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)
	return sock, mock
}

func TestPullImage(t *testing.T) {
	sock, _ := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	if err := c.PullImage(ctx, "nginx:latest"); err != nil {
		t.Fatalf("PullImage: %v", err)
	}
}

func TestRunAndRemovePodSandbox(t *testing.T) {
	sock, mock := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	config := &runtimev1.PodSandboxConfig{
		Metadata: &runtimev1.PodSandboxMetadata{
			Name:      "test-pod",
			Namespace: "default",
			Uid:       "uid-1",
		},
		Labels: map[string]string{"app": "test"},
	}

	podID, err := c.RunPodSandbox(ctx, config)
	if err != nil {
		t.Fatalf("RunPodSandbox: %v", err)
	}
	if podID == "" {
		t.Fatal("expected non-empty pod ID")
	}

	// Verify pod exists in mock state.
	mock.mu.Lock()
	if _, ok := mock.pods[podID]; !ok {
		t.Error("pod not found in mock state")
	}
	mock.mu.Unlock()

	// Stop and remove.
	if err := c.StopPodSandbox(ctx, podID); err != nil {
		t.Fatalf("StopPodSandbox: %v", err)
	}
	if err := c.RemovePodSandbox(ctx, podID); err != nil {
		t.Fatalf("RemovePodSandbox: %v", err)
	}

	// Verify pod is gone.
	mock.mu.Lock()
	if _, ok := mock.pods[podID]; ok {
		t.Error("pod should have been removed")
	}
	mock.mu.Unlock()
}

// containerLifecycleEnv holds shared state for container lifecycle tests.
type containerLifecycleEnv struct {
	client *Client
	mock   *fullMockCRI
	podID  string
	ctrID  string
}

func setupContainerLifecycle(t *testing.T) *containerLifecycleEnv {
	t.Helper()
	sock, mock := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	podConfig := &runtimev1.PodSandboxConfig{
		Metadata: &runtimev1.PodSandboxMetadata{
			Name: "test-pod", Namespace: "default", Uid: "uid-1",
		},
	}
	podID, err := c.RunPodSandbox(ctx, podConfig)
	if err != nil {
		t.Fatalf("RunPodSandbox: %v", err)
	}

	ctrConfig := &runtimev1.ContainerConfig{
		Metadata: &runtimev1.ContainerMetadata{Name: "web"},
		Image:    &runtimev1.ImageSpec{Image: "nginx"},
		Labels:   map[string]string{"svc": "web"},
	}
	ctrID, err := c.CreateContainer(ctx, podID, ctrConfig, podConfig)
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	if ctrID == "" {
		t.Fatal("expected non-empty container ID")
	}

	return &containerLifecycleEnv{client: c, mock: mock, podID: podID, ctrID: ctrID}
}

func (e *containerLifecycleEnv) assertState(t *testing.T, want runtimev1.ContainerState) {
	t.Helper()
	e.mock.mu.Lock()
	defer e.mock.mu.Unlock()
	got := e.mock.containers[e.ctrID].State
	if got != want {
		t.Errorf("state = %v, want %v", got, want)
	}
}

func TestContainerCreate(t *testing.T) {
	env := setupContainerLifecycle(t)
	env.assertState(t, runtimev1.ContainerState_CONTAINER_CREATED)
}

func TestContainerStart(t *testing.T) {
	env := setupContainerLifecycle(t)
	ctx := context.Background()
	if err := env.client.StartContainer(ctx, env.ctrID); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}
	env.assertState(t, runtimev1.ContainerState_CONTAINER_RUNNING)
}

func TestContainerStop(t *testing.T) {
	env := setupContainerLifecycle(t)
	ctx := context.Background()
	if err := env.client.StartContainer(ctx, env.ctrID); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}
	if err := env.client.StopContainer(ctx, env.ctrID, 10); err != nil {
		t.Fatalf("StopContainer: %v", err)
	}
	env.assertState(t, runtimev1.ContainerState_CONTAINER_EXITED)
}

func TestContainerRemove(t *testing.T) {
	env := setupContainerLifecycle(t)
	ctx := context.Background()
	if err := env.client.RemoveContainer(ctx, env.ctrID); err != nil {
		t.Fatalf("RemoveContainer: %v", err)
	}
	env.mock.mu.Lock()
	defer env.mock.mu.Unlock()
	if _, ok := env.mock.containers[env.ctrID]; ok {
		t.Error("container should have been removed")
	}
}

func TestListPodSandboxesByLabel(t *testing.T) {
	sock, _ := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	// Create two pods with different labels.
	cfg1 := &runtimev1.PodSandboxConfig{
		Metadata: &runtimev1.PodSandboxMetadata{Name: "pod1", Namespace: "ns", Uid: "1"},
		Labels:   map[string]string{LabelProject: "proj-a"},
	}
	cfg2 := &runtimev1.PodSandboxConfig{
		Metadata: &runtimev1.PodSandboxMetadata{Name: "pod2", Namespace: "ns", Uid: "2"},
		Labels:   map[string]string{LabelProject: "proj-b"},
	}

	if _, err := c.RunPodSandbox(ctx, cfg1); err != nil {
		t.Fatalf("RunPodSandbox: %v", err)
	}
	if _, err := c.RunPodSandbox(ctx, cfg2); err != nil {
		t.Fatalf("RunPodSandbox: %v", err)
	}

	// List by project label.
	pods, err := c.ListPodSandboxes(ctx, map[string]string{LabelProject: "proj-a"})
	if err != nil {
		t.Fatalf("ListPodSandboxes: %v", err)
	}
	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}
	if pods[0].Labels[LabelProject] != "proj-a" {
		t.Errorf("expected proj-a, got %q", pods[0].Labels[LabelProject])
	}
}

func TestListContainersByPod(t *testing.T) {
	sock, _ := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	// Create two pods.
	podCfg1 := &runtimev1.PodSandboxConfig{
		Metadata: &runtimev1.PodSandboxMetadata{Name: "pod1", Namespace: "ns", Uid: "1"},
	}
	podCfg2 := &runtimev1.PodSandboxConfig{
		Metadata: &runtimev1.PodSandboxMetadata{Name: "pod2", Namespace: "ns", Uid: "2"},
	}
	pod1, _ := c.RunPodSandbox(ctx, podCfg1)
	pod2, _ := c.RunPodSandbox(ctx, podCfg2)

	// Create containers in each pod.
	ctrCfg := &runtimev1.ContainerConfig{
		Metadata: &runtimev1.ContainerMetadata{Name: "web"},
		Image:    &runtimev1.ImageSpec{Image: "nginx"},
	}
	if _, err := c.CreateContainer(ctx, pod1, ctrCfg, podCfg1); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	if _, err := c.CreateContainer(ctx, pod1, ctrCfg, podCfg1); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	if _, err := c.CreateContainer(ctx, pod2, ctrCfg, podCfg2); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	// List containers in pod1.
	ctrs, err := c.ListContainers(ctx, pod1)
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
	if len(ctrs) != 2 {
		t.Errorf("expected 2 containers in pod1, got %d", len(ctrs))
	}

	// List containers in pod2.
	ctrs, err = c.ListContainers(ctx, pod2)
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
	if len(ctrs) != 1 {
		t.Errorf("expected 1 container in pod2, got %d", len(ctrs))
	}
}

func TestExecSync(t *testing.T) {
	env := setupContainerLifecycle(t)
	ctx := context.Background()
	if err := env.client.StartContainer(ctx, env.ctrID); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}

	resp, err := env.client.ExecSync(ctx, env.ctrID, []string{"echo", "hello"}, 5)
	if err != nil {
		t.Fatalf("ExecSync: %v", err)
	}
	if resp.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", resp.ExitCode)
	}

	// Set non-zero exit code and verify.
	env.mock.SetExecExitCode(env.ctrID, 1)
	resp, err = env.client.ExecSync(ctx, env.ctrID, []string{"false"}, 5)
	if err != nil {
		t.Fatalf("ExecSync: %v", err)
	}
	if resp.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", resp.ExitCode)
	}
}

func TestContainerStatus(t *testing.T) {
	env := setupContainerLifecycle(t)
	ctx := context.Background()
	if err := env.client.StartContainer(ctx, env.ctrID); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}

	resp, err := env.client.ContainerStatus(ctx, env.ctrID)
	if err != nil {
		t.Fatalf("ContainerStatus: %v", err)
	}
	if resp.Status.State != runtimev1.ContainerState_CONTAINER_RUNNING {
		t.Errorf("state = %v, want RUNNING", resp.Status.State)
	}

	env.mock.SetContainerExited(env.ctrID, 0)
	resp, err = env.client.ContainerStatus(ctx, env.ctrID)
	if err != nil {
		t.Fatalf("ContainerStatus: %v", err)
	}
	if resp.Status.State != runtimev1.ContainerState_CONTAINER_EXITED {
		t.Errorf("state = %v, want EXITED", resp.Status.State)
	}
	if resp.Status.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", resp.Status.ExitCode)
	}
}

func TestPodSandboxStatus(t *testing.T) {
	sock, mock := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	config := &runtimev1.PodSandboxConfig{
		Metadata: &runtimev1.PodSandboxMetadata{
			Name: "test-pod", Namespace: "default", Uid: "uid-1",
		},
	}
	podID, err := c.RunPodSandbox(ctx, config)
	if err != nil {
		t.Fatalf("RunPodSandbox: %v", err)
	}

	mock.SetPodIP(podID, "10.0.0.42")

	resp, err := c.PodSandboxStatus(ctx, podID)
	if err != nil {
		t.Fatalf("PodSandboxStatus: %v", err)
	}
	if resp.Status.Network.Ip != "10.0.0.42" {
		t.Errorf("IP = %q, want 10.0.0.42", resp.Status.Network.Ip)
	}
}

func TestWaitExited_Success(t *testing.T) {
	env := setupContainerLifecycle(t)
	ctx := context.Background()
	if err := env.client.StartContainer(ctx, env.ctrID); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}

	// Simulate async exit after a short delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		env.mock.SetContainerExited(env.ctrID, 0)
	}()

	exitCode, err := env.client.WaitExited(ctx, env.ctrID, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitExited: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0", exitCode)
	}
}

func TestWaitExited_NonZeroExit(t *testing.T) {
	env := setupContainerLifecycle(t)
	ctx := context.Background()
	if err := env.client.StartContainer(ctx, env.ctrID); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}

	env.mock.SetContainerExited(env.ctrID, 137)

	exitCode, err := env.client.WaitExited(ctx, env.ctrID, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitExited: %v", err)
	}
	if exitCode != 137 {
		t.Errorf("exit code = %d, want 137", exitCode)
	}
}
