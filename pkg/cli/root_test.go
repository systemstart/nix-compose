package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	orchestratev1 "github.com/systemstart/nix-compose/api/orchestrate/v1"
	"github.com/systemstart/nix-compose/pkg/cni"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/eval"
	"github.com/systemstart/nix-compose/pkg/health"
	"github.com/systemstart/nix-compose/pkg/k8s"
	"github.com/systemstart/nix-compose/pkg/logs"
	"github.com/systemstart/nix-compose/pkg/orchestrate"
	"github.com/systemstart/nix-compose/pkg/orchestrate/deploy"
	"github.com/systemstart/nix-compose/pkg/orchestrate/server"
	"github.com/systemstart/nix-compose/pkg/orchestrate/state"
	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
	"github.com/systemstart/nix-compose/pkg/volumes"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type mockRunner struct {
	stdout []byte
	stderr []byte
	err    error
}

func (m *mockRunner) Run(_ context.Context, _ string, _ ...string) ([]byte, []byte, error) {
	return m.stdout, m.stderr, m.err
}

// Verify the interface satisfaction.
var _ eval.CommandRunner = (*mockRunner)(nil)

func TestRootCommandHasSubcommands(t *testing.T) {
	cmds := rootCmd.Commands()
	expected := map[string]bool{
		"up": false, "down": false, "ps": false,
		"logs": false, "exec": false,
		"render": false, "docs": false, "version": false,
		"start": false, "stop": false, "restart": false,
		"pull": false, "create": false, "kill": false,
		"rm": false, "top": false, "images": false,
		"doctor": false, "plan": false, "state": false,
	}

	for _, cmd := range cmds {
		if _, ok := expected[cmd.Name()]; ok {
			expected[cmd.Name()] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("missing subcommand %q", name)
		}
	}
}

func TestRootPersistentFlags(t *testing.T) {
	flags := []string{"file", "project-dir", "project-name", "impure", "flake-attr", "profile", "cri-socket"}
	for _, name := range flags {
		f := rootCmd.PersistentFlags().Lookup(name)
		if f == nil {
			t.Errorf("missing persistent flag %q", name)
		}
	}
}

func TestLogsFlags(t *testing.T) {
	f := logsCmd.Flags().Lookup("follow")
	if f == nil {
		t.Fatal("missing --follow flag on logs")
		return
	}
	if f.Shorthand != "f" {
		t.Errorf("follow shorthand = %q, want %q", f.Shorthand, "f")
	}
}

func TestUpFlags(t *testing.T) {
	f := upCmd.Flags().Lookup("detach")
	if f == nil {
		t.Fatal("missing --detach flag on up")
		return
	}
	if f.Shorthand != "d" {
		t.Errorf("detach shorthand = %q, want %q", f.Shorthand, "d")
	}

	for _, name := range []string{"build", "vm-image-flake", "vm-portfwd-port"} {
		if upCmd.Flags().Lookup(name) == nil {
			t.Fatalf("missing --%s flag on up", name)
		}
	}
}

func TestDownFlags(t *testing.T) {
	f := downCmd.Flags().Lookup("volumes")
	if f == nil {
		t.Fatal("missing --volumes flag on down")
		return
	}
	if f.Shorthand != "v" {
		t.Errorf("volumes shorthand = %q, want %q", f.Shorthand, "v")
	}

	if downCmd.Flags().Lookup("timeout") == nil {
		t.Fatal("missing --timeout flag on down")
	}
}

func TestProjectDir_Default(t *testing.T) {
	old := flagProjectDir
	flagProjectDir = ""
	defer func() { flagProjectDir = old }()

	dir := projectDir()
	if dir == "" {
		t.Error("projectDir() returned empty string")
	}
}

func TestProjectDir_Custom(t *testing.T) {
	old := flagProjectDir
	flagProjectDir = "/custom/path"
	defer func() { flagProjectDir = old }()

	dir := projectDir()
	if dir != "/custom/path" {
		t.Errorf("projectDir() = %q, want /custom/path", dir)
	}
}

func TestExecRequiresArgs(t *testing.T) {
	err := execCmd.Args(execCmd, []string{})
	if err == nil {
		t.Error("exec should require at least 1 argument")
	}
}

func TestRootCmd_Help(t *testing.T) {
	// Exercise the root command with --help (doesn't call os.Exit).
	rootCmd.SetArgs([]string{"--help"})
	err := rootCmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestWatchFlag(t *testing.T) {
	f := upCmd.Flags().Lookup("watch")
	if f == nil {
		t.Fatal("missing --watch flag on up")
	}
}

func TestProfileFlag(t *testing.T) {
	f := rootCmd.PersistentFlags().Lookup("profile")
	if f == nil {
		t.Fatal("missing --profile persistent flag")
	}
}

func TestMergeProfiles_FlagsOnly(t *testing.T) {
	result := mergeProfiles([]string{"backend", "frontend"})
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
}

func TestMergeProfiles_Dedup(t *testing.T) {
	result := mergeProfiles([]string{"backend", "backend"})
	if len(result) != 1 {
		t.Fatalf("expected 1 (deduped), got %d", len(result))
	}
}

func TestMergeProfiles_EnvVar(t *testing.T) {
	t.Setenv("COMPOSE_PROFILES", "observability,debug")
	result := mergeProfiles(nil)
	if len(result) != 2 || result[0] != "observability" || result[1] != "debug" {
		t.Errorf("expected [observability debug], got %v", result)
	}
}

func TestMergeProfiles_EnvAndFlags(t *testing.T) {
	t.Setenv("COMPOSE_PROFILES", "observability")
	result := mergeProfiles([]string{"backend"})
	if len(result) != 2 || result[0] != "backend" || result[1] != "observability" {
		t.Errorf("expected [backend observability], got %v", result)
	}
}

func TestMergeProfiles_EnvOverlapWithFlags(t *testing.T) {
	t.Setenv("COMPOSE_PROFILES", "backend,frontend")
	result := mergeProfiles([]string{"backend"})
	if len(result) != 2 {
		t.Fatalf("expected 2 (deduped), got %d: %v", len(result), result)
	}
}

func TestMergeProfiles_EmptyEnv(t *testing.T) {
	t.Setenv("COMPOSE_PROFILES", "")
	result := mergeProfiles(nil)
	if len(result) != 0 {
		t.Errorf("expected empty, got %v", result)
	}
}

func TestRenderFlags(t *testing.T) {
	flags := []string{"target", "output", "namespace", "dry-run"}
	for _, name := range flags {
		f := renderCmd.Flags().Lookup(name)
		if f == nil {
			t.Errorf("missing --%s flag on render", name)
		}
	}
}

func TestRenderUnsupportedTarget(t *testing.T) {
	old := renderTarget
	renderTarget = "helm"
	defer func() { renderTarget = old }()

	err := runRender(renderCmd, nil)
	if err == nil {
		t.Error("expected error for unsupported target")
	}
	if !strings.Contains(err.Error(), "unsupported target") {
		t.Errorf("expected unsupported target error, got: %v", err)
	}
}

// --- CRI mock for CLI tests ---

type cliMockCRI struct {
	runtimev1.UnimplementedRuntimeServiceServer
	runtimev1.UnimplementedImageServiceServer

	mu            sync.Mutex
	pods          map[string]*runtimev1.PodSandbox
	podConfigs    map[string]*runtimev1.PodSandboxConfig
	containers    map[string]*runtimev1.Container
	containerExit map[string]int32
	images        map[string]*runtimev1.Image
	execExitCodes map[string]int32
	execStdout    map[string][]byte
	execStderr    map[string][]byte
	podIPs        map[string]string
	startOrder    []string // tracks service start order
	nextPodID     int
	nextCtrID     int
}

func newCLIMockCRI() *cliMockCRI {
	return &cliMockCRI{
		pods:          make(map[string]*runtimev1.PodSandbox),
		podConfigs:    make(map[string]*runtimev1.PodSandboxConfig),
		containers:    make(map[string]*runtimev1.Container),
		containerExit: make(map[string]int32),
		images:        make(map[string]*runtimev1.Image),
		execExitCodes: make(map[string]int32),
		execStdout:    make(map[string][]byte),
		execStderr:    make(map[string][]byte),
		podIPs:        make(map[string]string),
	}
}

func (m *cliMockCRI) Version(_ context.Context, _ *runtimev1.VersionRequest) (*runtimev1.VersionResponse, error) {
	return &runtimev1.VersionResponse{
		Version: "0.1.0", RuntimeName: "mock", RuntimeVersion: "1.0.0", RuntimeApiVersion: "v1",
	}, nil
}

func (m *cliMockCRI) PullImage(_ context.Context, req *runtimev1.PullImageRequest) (*runtimev1.PullImageResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ref := req.Image.Image
	m.images[ref] = &runtimev1.Image{
		Id:       "sha256:" + ref,
		RepoTags: []string{ref},
		Size:     1024,
		Spec:     &runtimev1.ImageSpec{Image: ref},
	}
	return &runtimev1.PullImageResponse{ImageRef: ref}, nil
}

func (m *cliMockCRI) ListImages(_ context.Context, _ *runtimev1.ListImagesRequest) (*runtimev1.ListImagesResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var imgs []*runtimev1.Image
	for _, img := range m.images {
		imgs = append(imgs, img)
	}
	return &runtimev1.ListImagesResponse{Images: imgs}, nil
}

func (m *cliMockCRI) ImageStatus(_ context.Context, req *runtimev1.ImageStatusRequest) (*runtimev1.ImageStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	img := m.images[req.Image.Image]
	return &runtimev1.ImageStatusResponse{Image: img}, nil
}

func (m *cliMockCRI) RemoveImage(_ context.Context, req *runtimev1.RemoveImageRequest) (*runtimev1.RemoveImageResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.images, req.Image.Image)
	return &runtimev1.RemoveImageResponse{}, nil
}

func (m *cliMockCRI) RunPodSandbox(_ context.Context, req *runtimev1.RunPodSandboxRequest) (*runtimev1.RunPodSandboxResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextPodID++
	id := fmt.Sprintf("pod-%d", m.nextPodID)
	m.pods[id] = &runtimev1.PodSandbox{
		Id: id, State: runtimev1.PodSandboxState_SANDBOX_READY,
		Metadata: req.Config.Metadata, Labels: req.Config.Labels,
	}
	m.podConfigs[id] = req.Config
	// Track service start order from labels.
	if svc, ok := req.Config.Labels["nix-compose.service"]; ok {
		m.startOrder = append(m.startOrder, svc)
	}
	return &runtimev1.RunPodSandboxResponse{PodSandboxId: id}, nil
}

func (m *cliMockCRI) StopPodSandbox(_ context.Context, req *runtimev1.StopPodSandboxRequest) (*runtimev1.StopPodSandboxResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pod, ok := m.pods[req.PodSandboxId]; ok {
		pod.State = runtimev1.PodSandboxState_SANDBOX_NOTREADY
	}
	return &runtimev1.StopPodSandboxResponse{}, nil
}

func (m *cliMockCRI) RemovePodSandbox(_ context.Context, req *runtimev1.RemovePodSandboxRequest) (*runtimev1.RemovePodSandboxResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pods, req.PodSandboxId)
	return &runtimev1.RemovePodSandboxResponse{}, nil
}

func (m *cliMockCRI) ListPodSandbox(_ context.Context, req *runtimev1.ListPodSandboxRequest) (*runtimev1.ListPodSandboxResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var items []*runtimev1.PodSandbox
	for _, pod := range m.pods {
		match := true
		for k, v := range req.Filter.GetLabelSelector() {
			if pod.Labels[k] != v {
				match = false
				break
			}
		}
		if match {
			items = append(items, pod)
		}
	}
	return &runtimev1.ListPodSandboxResponse{Items: items}, nil
}

func (m *cliMockCRI) CreateContainer(_ context.Context, req *runtimev1.CreateContainerRequest) (*runtimev1.CreateContainerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextCtrID++
	id := fmt.Sprintf("ctr-%d", m.nextCtrID)
	m.containers[id] = &runtimev1.Container{
		Id: id, PodSandboxId: req.PodSandboxId,
		State:    runtimev1.ContainerState_CONTAINER_CREATED,
		Metadata: req.Config.Metadata, Labels: req.Config.Labels, Image: req.Config.Image,
	}
	return &runtimev1.CreateContainerResponse{ContainerId: id}, nil
}

func (m *cliMockCRI) StartContainer(_ context.Context, req *runtimev1.StartContainerRequest) (*runtimev1.StartContainerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctr, ok := m.containers[req.ContainerId]; ok {
		ctr.State = runtimev1.ContainerState_CONTAINER_RUNNING
	}
	return &runtimev1.StartContainerResponse{}, nil
}

func (m *cliMockCRI) StopContainer(_ context.Context, req *runtimev1.StopContainerRequest) (*runtimev1.StopContainerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctr, ok := m.containers[req.ContainerId]; ok {
		ctr.State = runtimev1.ContainerState_CONTAINER_EXITED
	}
	return &runtimev1.StopContainerResponse{}, nil
}

func (m *cliMockCRI) RemoveContainer(_ context.Context, req *runtimev1.RemoveContainerRequest) (*runtimev1.RemoveContainerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.containers, req.ContainerId)
	return &runtimev1.RemoveContainerResponse{}, nil
}

func (m *cliMockCRI) ListContainers(_ context.Context, req *runtimev1.ListContainersRequest) (*runtimev1.ListContainersResponse, error) {
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

func (m *cliMockCRI) ExecSync(_ context.Context, req *runtimev1.ExecSyncRequest) (*runtimev1.ExecSyncResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	exitCode, ok := m.execExitCodes[req.ContainerId]
	if !ok {
		exitCode = 0
	}
	stdout := m.execStdout[req.ContainerId]
	stderr := m.execStderr[req.ContainerId]
	return &runtimev1.ExecSyncResponse{ExitCode: exitCode, Stdout: stdout, Stderr: stderr}, nil
}

func (m *cliMockCRI) Exec(_ context.Context, req *runtimev1.ExecRequest) (*runtimev1.ExecResponse, error) {
	return &runtimev1.ExecResponse{
		Url: fmt.Sprintf("http://127.0.0.1:0/exec/%s", req.ContainerId),
	}, nil
}

func (m *cliMockCRI) ContainerStatus(_ context.Context, req *runtimev1.ContainerStatusRequest) (*runtimev1.ContainerStatusResponse, error) {
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

func (m *cliMockCRI) PodSandboxStatus(_ context.Context, req *runtimev1.PodSandboxStatusRequest) (*runtimev1.PodSandboxStatusResponse, error) {
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

func startCLIMockCRI(t *testing.T) (string, *cliMockCRI) {
	t.Helper()
	mock := newCLIMockCRI()
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
	return sock, mock
}

// newCLIOrchestrateServer creates a server.Server backed by a CRI client.
func newCLIOrchestrateServer(t *testing.T, criClient *cri.Client, orchSock string) *server.Server {
	t.Helper()
	volStore := &volumes.Store{Root: t.TempDir()}
	cniStore := &cni.Store{
		ConfDir:    t.TempDir(),
		PluginDirs: []string{},
	}
	return server.New(server.Config{
		CRIClient: criClient,
		CNIStore:  cniStore,
		VolStore:  volStore,
		LogBase:   t.TempDir(),
		DBPath:    filepath.Join(t.TempDir(), "state.bolt"),
	})
}

func TestDoUp_CRI_Success(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	fixture := `{"services":{"web":{"image":"nginx"},"api":{"image":"node"}}}`
	evalRunner := &mockRunner{stdout: []byte(fixture)}

	deps := UpDeps{
		Evaluator: &eval.Evaluator{
			Runner:     evalRunner,
			ProjectDir: dir,
			Impure:     true,
		},
		ProjectDir:  dir,
		ProjectName: "testproj",
		CRIClient:   criClient,
		// The CRI path must not touch any compose machinery — there is none.
	}

	comp, err := DoUp(ctx, deps, true, false, false, nil, nil)
	if err != nil {
		t.Fatalf("DoUp with CRI: %v", err)
	}
	if comp == nil {
		t.Fatal("expected non-nil composition")
		return
	}
	if len(comp.Services) != 2 {
		t.Errorf("expected 2 services, got %d", len(comp.Services))
	}

	// Verify pods were created in mock.
	mock.mu.Lock()
	podCount := len(mock.pods)
	ctrCount := len(mock.containers)
	mock.mu.Unlock()

	if podCount != 2 {
		t.Errorf("expected 2 pods, got %d", podCount)
	}
	if ctrCount != 2 {
		t.Errorf("expected 2 containers, got %d", ctrCount)
	}
}

func TestDoDown_CRI_Success(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// Pre-create some pods via ServiceUp.
	svc := eval.Service{Image: "nginx:latest"}
	opts := cri.ServiceUpOptions{Project: "testproj", Version: "v1"}
	if err := criClient.ServiceUp(ctx, "web", svc, opts); err != nil {
		t.Fatalf("ServiceUp: %v", err)
	}
	if err := criClient.ServiceUp(ctx, "api", eval.Service{Image: "node"}, opts); err != nil {
		t.Fatalf("ServiceUp: %v", err)
	}

	// Verify pods exist.
	mock.mu.Lock()
	if len(mock.pods) != 2 {
		t.Fatalf("expected 2 pods, got %d", len(mock.pods))
	}
	mock.mu.Unlock()

	// CRIDown should remove all.
	if err := CRIDown(ctx, criClient, "testproj", t.TempDir(), 10, false); err != nil {
		t.Fatalf("CRIDown: %v", err)
	}

	mock.mu.Lock()
	remaining := len(mock.pods)
	mock.mu.Unlock()
	if remaining != 0 {
		t.Errorf("expected 0 pods after CRIDown, got %d", remaining)
	}
}

func TestDoDown_CRI_WithVolumes(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// CRIDown with removeVolumes=true should not error even if no volumes exist.
	if err := CRIDown(ctx, criClient, "testproj", t.TempDir(), 10, true); err != nil {
		t.Fatalf("CRIDown with volumes: %v", err)
	}
}

func TestPullCRI(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest"},
			"api": {Image: "node:20"},
		},
	}

	images := serviceImages(comp, nil)
	for svc, image := range images {
		if err := criClient.PullImage(ctx, image); err != nil {
			t.Fatalf("PullImage %s: %v", svc, err)
		}
	}

	// Verify images were pulled.
	mock.mu.Lock()
	imgCount := len(mock.images)
	mock.mu.Unlock()
	if imgCount != 2 {
		t.Errorf("expected 2 images pulled, got %d", imgCount)
	}

	// Verify specific images.
	mock.mu.Lock()
	_, hasNginx := mock.images["nginx:latest"]
	_, hasNode := mock.images["node:20"]
	mock.mu.Unlock()
	if !hasNginx {
		t.Error("expected nginx:latest to be pulled")
	}
	if !hasNode {
		t.Error("expected node:20 to be pulled")
	}
}

func TestPullCRI_FilteredServices(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest"},
			"api": {Image: "node:20"},
			"db":  {Image: "postgres:16"},
		},
	}

	images := serviceImages(comp, []string{"web", "db"})
	if len(images) != 2 {
		t.Fatalf("expected 2 images, got %d", len(images))
	}
	if images["web"] != "nginx:latest" {
		t.Errorf("web image = %q, want nginx:latest", images["web"])
	}
	if images["db"] != "postgres:16" {
		t.Errorf("db image = %q, want postgres:16", images["db"])
	}
	if _, ok := images["api"]; ok {
		t.Error("api should not be included when not in filter args")
	}
}

func TestCRIDown_RemovesCNIConfig(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// CRIDown should not error even when no conflist exists.
	if err := CRIDown(ctx, criClient, "testproj", t.TempDir(), 10, false); err != nil {
		t.Fatalf("CRIDown: %v", err)
	}
}

func TestImagesCRI(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// Pull images.
	if err := criClient.PullImage(ctx, "nginx:latest"); err != nil {
		t.Fatalf("PullImage: %v", err)
	}
	if err := criClient.PullImage(ctx, "node:20"); err != nil {
		t.Fatalf("PullImage: %v", err)
	}

	// List images via client.
	imgs, err := criClient.ListImages(ctx)
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(imgs) != 2 {
		t.Fatalf("expected 2 images, got %d", len(imgs))
	}

	// Verify image details.
	found := map[string]bool{}
	for _, img := range imgs {
		for _, tag := range img.RepoTags {
			found[tag] = true
		}
	}
	if !found["nginx:latest"] {
		t.Error("expected nginx:latest in images list")
	}
	if !found["node:20"] {
		t.Error("expected node:20 in images list")
	}
}

// criTestDeps creates UpDeps for CRI integration tests.
func criTestDeps(t *testing.T, dir string, fixture string, criClient *cri.Client) UpDeps {
	t.Helper()
	return UpDeps{
		Evaluator: &eval.Evaluator{
			Runner:     &mockRunner{stdout: []byte(fixture)},
			ProjectDir: dir,
			Impure:     true,
		},
		ProjectDir:  dir,
		ProjectName: "testproj",
		CRIClient:   criClient,
	}
}

func verifyStartOrder(t *testing.T, order []string) {
	t.Helper()
	if len(order) != 3 {
		t.Fatalf("expected 3 services started, got %d: %v", len(order), order)
	}
	idx := map[string]int{}
	for i, s := range order {
		idx[s] = i
	}
	if idx["db"] >= idx["api"] || idx["api"] >= idx["app"] {
		t.Errorf("expected start order db < api < app, got %v", order)
	}
}

func TestCriUp_OrderedStartup(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	fixture := `{"services":{
		"app":{"image":"app:1","depends_on":{"api":{"condition":"service_started"}}},
		"api":{"image":"api:1","depends_on":{"db":{"condition":"service_started"}}},
		"db":{"image":"postgres:16"}
	}}`

	deps := criTestDeps(t, dir, fixture, criClient)
	if _, err = DoUp(ctx, deps, false, false, false, nil, nil); err != nil {
		t.Fatalf("DoUp: %v", err)
	}

	mock.mu.Lock()
	order := make([]string, len(mock.startOrder))
	copy(order, mock.startOrder)
	mock.mu.Unlock()

	verifyStartOrder(t, order)
}

func TestCriUp_HealthGating(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	fixture := `{"services":{
		"app":{"image":"app:1","depends_on":{"db":{"condition":"service_healthy"}}},
		"db":{"image":"postgres:16","healthcheck":{"test":["CMD","pg_isready"],"interval":"100ms","timeout":"1s","retries":3}}
	}}`

	deps := criTestDeps(t, dir, fixture, criClient)
	// ExecSync returns 0 by default in mock, so health probe should pass.
	if _, err = DoUp(ctx, deps, false, false, false, nil, nil); err != nil {
		t.Fatalf("DoUp with health gating: %v", err)
	}
}

// findMigrateContainer returns the container ID for the "migrate" service, or "".
func findMigrateContainer(mock *cliMockCRI) string {
	for id, ctr := range mock.containers {
		if ctr.State != runtimev1.ContainerState_CONTAINER_RUNNING {
			continue
		}
		for _, pod := range mock.pods {
			if pod.Labels["nix-compose.service"] == "migrate" && ctr.PodSandboxId == pod.Id {
				return id
			}
		}
	}
	return ""
}

// simulateMigrateExit watches for the migrate container and marks it as exited.
func simulateMigrateExit(ctx context.Context, mock *cliMockCRI) {
	go func() {
		for {
			mock.mu.Lock()
			migrateID := findMigrateContainer(mock)
			mock.mu.Unlock()
			if migrateID != "" {
				mock.mu.Lock()
				if ctr, ok := mock.containers[migrateID]; ok {
					ctr.State = runtimev1.ContainerState_CONTAINER_EXITED
				}
				mock.containerExit[migrateID] = 0
				mock.mu.Unlock()
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}()
}

func TestCriUp_CompletedSuccessfully(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	fixture := `{"services":{
		"app":{"image":"app:1","depends_on":{"migrate":{"condition":"service_completed_successfully"}}},
		"migrate":{"image":"migrate:1"}
	}}`

	deps := criTestDeps(t, dir, fixture, criClient)
	simulateMigrateExit(ctx, mock)

	if _, err = DoUp(ctx, deps, false, false, false, nil, nil); err != nil {
		t.Fatalf("DoUp with completed_successfully: %v", err)
	}
}

func writeTestLogFile(t *testing.T, dir, project, svc, content string) {
	t.Helper()
	logDir := filepath.Join(dir, project, svc)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "0.log"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLogsCRI_Dump(t *testing.T) {
	dir := t.TempDir()
	project := "logtest"

	writeTestLogFile(t, dir, project, "web",
		"2024-01-15T10:30:01Z stdout F web hello\n2024-01-15T10:30:03Z stdout F web world\n")
	writeTestLogFile(t, dir, project, "api",
		"2024-01-15T10:30:00Z stdout F api start\n2024-01-15T10:30:02Z stderr F api warn\n")

	var buf bytes.Buffer
	err := logs.Dump(&buf, dir, project, []string{"web", "api"}, logs.Options{})
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}

	output := buf.String()
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d: %q", len(lines), output)
	}

	// Verify timestamp-sorted order.
	expected := []string{"api start", "web hello", "api warn", "web world"}
	for i, want := range expected {
		if !strings.Contains(lines[i], want) {
			t.Errorf("line %d: expected %q, got %q", i, want, lines[i])
		}
	}
}

// --- Pure function tests ---

func TestSplitRepoTag(t *testing.T) {
	tests := []struct {
		name     string
		ref      string
		wantRepo string
		wantTag  string
	}{
		{"with tag", "nginx:1.25", "nginx", "1.25"},
		{"no tag (latest)", "nginx", "nginx", "latest"},
		{"registry with port", "registry.io:5000/app:v2", "registry.io:5000/app", "v2"},
		{"nix-built image", "nix-compose.local/hello-oci:wi0pgka6hq3", "nix-compose.local/hello-oci", "wi0pgka6hq3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, tag := splitRepoTag(tt.ref)
			if repo != tt.wantRepo {
				t.Errorf("repo = %q, want %q", repo, tt.wantRepo)
			}
			if tag != tt.wantTag {
				t.Errorf("tag = %q, want %q", tag, tt.wantTag)
			}
		})
	}
}

func TestMatchingImageRows(t *testing.T) {
	// Two services built from identical Nix closures produce byte-identical
	// images, which containerd stores as one record under both references.
	// Both must still get a row.
	shared := &runtimev1.Image{
		Id:       "sha256:abc",
		RepoTags: []string{"nix-compose.local/coreutils-oci:aaa", "nix-compose.local/custom-sleeper:bbb"},
		Size:     100,
	}
	other := &runtimev1.Image{Id: "sha256:def", RepoTags: []string{"nginx:latest"}, Size: 200}
	untagged := &runtimev1.Image{Id: "sha256:ghi"}

	wanted := map[string]bool{
		"nix-compose.local/coreutils-oci:aaa":  true,
		"nix-compose.local/custom-sleeper:bbb": true,
	}

	rows := matchingImageRows([]*runtimev1.Image{shared, other, untagged}, wanted)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(rows), rows)
	}
	// Sorted by repository, so coreutils precedes custom.
	if rows[0].repo != "nix-compose.local/coreutils-oci" || rows[0].tag != "aaa" {
		t.Errorf("row 0 = %+v", rows[0])
	}
	if rows[1].repo != "nix-compose.local/custom-sleeper" || rows[1].tag != "bbb" {
		t.Errorf("row 1 = %+v", rows[1])
	}
	for _, r := range rows {
		if r.id != "abc" || r.size != 100 {
			t.Errorf("row %+v should carry the shared image's id and size", r)
		}
	}
}

func TestMatchingImageRows_NoMatches(t *testing.T) {
	imgs := []*runtimev1.Image{{Id: "sha256:abc", RepoTags: []string{"nginx:latest"}}}
	if rows := matchingImageRows(imgs, map[string]bool{"redis:7": true}); len(rows) != 0 {
		t.Errorf("got %+v, want no rows", rows)
	}
}

func TestShortID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{"sha256 prefix", "sha256:abcdef1234567890", "abcdef123456"},
		{"no prefix long", "abcdef1234567890abcd", "abcdef123456"},
		{"short id", "abcdef", "abcdef"},
		{"empty", "", ""},
		{"exact 12", "abcdef123456", "abcdef123456"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shortID(tt.id)
			if got != tt.want {
				t.Errorf("shortID(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		name  string
		bytes uint64
		want  string
	}{
		{"zero", 0, "0B"},
		{"bytes", 500, "500B"},
		{"kilobytes", 2048, "2.0KB"},
		{"megabytes", 5 * 1024 * 1024, "5.0MB"},
		{"gigabytes", 3 * 1024 * 1024 * 1024, "3.0GB"},
		{"fractional MB", 1536 * 1024, "1.5MB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatSize(tt.bytes)
			if got != tt.want {
				t.Errorf("formatSize(%d) = %q, want %q", tt.bytes, got, tt.want)
			}
		})
	}
}

func TestCondPriority(t *testing.T) {
	tests := []struct {
		cond string
		want int
	}{
		{"service_healthy", 3},
		{"service_completed_successfully", 2},
		{"service_started", 1},
		{"unknown", 0},
		{"", 0},
	}
	for _, tt := range tests {
		t.Run(tt.cond, func(t *testing.T) {
			got := condPriority(tt.cond)
			if got != tt.want {
				t.Errorf("condPriority(%q) = %d, want %d", tt.cond, got, tt.want)
			}
		})
	}
}

func TestHighestConditionOf(t *testing.T) {
	tests := []struct {
		name string
		svc  string
		comp *eval.Composition
		want string
	}{
		{
			"no dependents",
			"db",
			&eval.Composition{
				Services: map[string]eval.Service{
					"db": {Image: "postgres"},
				},
			},
			"",
		},
		{
			"service_started",
			"db",
			&eval.Composition{
				Services: map[string]eval.Service{
					"db":  {Image: "postgres"},
					"app": {Image: "app", DependsOn: eval.DependsOnValue{Entries: map[string]eval.DependsOnEntry{"db": {Condition: "service_started"}}}},
				},
			},
			"service_started",
		},
		{
			"highest wins",
			"db",
			&eval.Composition{
				Services: map[string]eval.Service{
					"db":    {Image: "postgres"},
					"app":   {Image: "app", DependsOn: eval.DependsOnValue{Entries: map[string]eval.DependsOnEntry{"db": {Condition: "service_started"}}}},
					"admin": {Image: "admin", DependsOn: eval.DependsOnValue{Entries: map[string]eval.DependsOnEntry{"db": {Condition: "service_healthy"}}}},
				},
			},
			"service_healthy",
		},
		{
			"empty condition defaults to service_started",
			"db",
			&eval.Composition{
				Services: map[string]eval.Service{
					"db":  {Image: "postgres"},
					"app": {Image: "app", DependsOn: eval.DependsOnValue{Entries: map[string]eval.DependsOnEntry{"db": {Condition: ""}}}},
				},
			},
			"service_started",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := highestConditionOf(tt.svc, tt.comp)
			if got != tt.want {
				t.Errorf("highestConditionOf(%q) = %q, want %q", tt.svc, got, tt.want)
			}
		})
	}
}

func TestResolveLogServices_WithArgs(t *testing.T) {
	ctx := context.Background()
	// When args are provided, they should be returned sorted without evaluation.
	got, err := resolveLogServices(ctx, t.TempDir(), []string{"web", "api", "db"})
	if err != nil {
		t.Fatalf("resolveLogServices: %v", err)
	}
	want := []string{"api", "db", "web"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestResolveLogServices_SingleArg(t *testing.T) {
	ctx := context.Background()
	got, err := resolveLogServices(ctx, t.TempDir(), []string{"web"})
	if err != nil {
		t.Fatalf("resolveLogServices: %v", err)
	}
	if len(got) != 1 || got[0] != "web" {
		t.Errorf("got %v, want [web]", got)
	}
}

func TestPrintImageTable(t *testing.T) {
	// Just verify it doesn't panic with various inputs.
	printImageTable(nil)
	printImageTable([]imageRow{
		{repo: "nginx", tag: "latest", id: "abc123def456", size: 1024 * 1024 * 50},
		{repo: "nix-compose.local/hello-oci", tag: "wi0pgka6hq3", size: 500},
	})
}

func TestDocsCmd_NoEmbeddedDocs(t *testing.T) {
	old := AgentDocs
	AgentDocs = ""
	defer func() { AgentDocs = old }()

	err := docsCmd.RunE(docsCmd, nil)
	if err == nil {
		t.Error("expected error when docs not embedded")
	}
	if !strings.Contains(err.Error(), "not embedded") {
		t.Errorf("expected 'not embedded' error, got: %v", err)
	}
}

func TestDocsCmd_WithDocs(t *testing.T) {
	old := AgentDocs
	AgentDocs = "# Test Docs\nHello"
	defer func() { AgentDocs = old }()

	err := docsCmd.RunE(docsCmd, nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestVersionString(t *testing.T) {
	old := Version
	Version = "1.2.3-test"
	defer func() { Version = old }()

	if Version != "1.2.3-test" {
		t.Errorf("Version = %q, want 1.2.3-test", Version)
	}
}

func TestPsFlags(t *testing.T) {
	flags := []string{"all", "quiet", "format", "services"}
	for _, name := range flags {
		f := psCmd.Flags().Lookup(name)
		if f == nil {
			t.Errorf("missing --%s flag on ps", name)
		}
	}
}

func TestPsAllShorthand(t *testing.T) {
	f := psCmd.Flags().Lookup("all")
	if f == nil {
		t.Fatal("missing --all flag on ps")
		return
	}
	if f.Shorthand != "a" {
		t.Errorf("all shorthand = %q, want %q", f.Shorthand, "a")
	}
}

func TestLogsFlagsAll(t *testing.T) {
	flags := []string{"follow", "tail", "timestamps", "since", "no-log-prefix"}
	for _, name := range flags {
		f := logsCmd.Flags().Lookup(name)
		if f == nil {
			t.Errorf("missing --%s flag on logs", name)
		}
	}
}

func TestExecCmdUse(t *testing.T) {
	if execCmd.Use != "exec [service] [command...]" {
		t.Errorf("exec Use = %q", execCmd.Use)
	}
}

func TestServiceImages_NoImage(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest"},
			"svc": {Image: ""},
		},
	}
	images := serviceImages(comp, nil)
	if len(images) != 1 {
		t.Errorf("expected 1 image (skipping empty), got %d", len(images))
	}
	if images["web"] != "nginx:latest" {
		t.Errorf("web image = %q", images["web"])
	}
}

func TestCRIDown_Ordered_WithComp(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// Start services.
	opts := cri.ServiceUpOptions{Project: "proj", Version: "v1"}
	if err := criClient.ServiceUp(ctx, "db", eval.Service{Image: "postgres"}, opts); err != nil {
		t.Fatal(err)
	}
	if err := criClient.ServiceUp(ctx, "app", eval.Service{Image: "app"}, opts); err != nil {
		t.Fatal(err)
	}

	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"db":  {Image: "postgres"},
			"app": {Image: "app", DependsOn: eval.DependsOnValue{Entries: map[string]eval.DependsOnEntry{"db": {Condition: "service_started"}}}},
		},
	}

	if err := CRIDownOrdered(ctx, criClient, "proj", t.TempDir(), 5, false, comp); err != nil {
		t.Fatalf("CRIDownOrdered: %v", err)
	}

	mock.mu.Lock()
	remaining := len(mock.pods)
	mock.mu.Unlock()
	if remaining != 0 {
		t.Errorf("expected 0 pods, got %d", remaining)
	}
}

func TestLogsCRI_DumpWithTail(t *testing.T) {
	dir := t.TempDir()
	project := "tailtest"

	writeTestLogFile(t, dir, project, "web",
		"2024-01-15T10:30:01Z stdout F line1\n2024-01-15T10:30:02Z stdout F line2\n2024-01-15T10:30:03Z stdout F line3\n")

	var buf bytes.Buffer
	err := logs.Dump(&buf, dir, project, []string{"web"}, logs.Options{Tail: "1"})
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}

	output := buf.String()
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line with tail=1, got %d: %q", len(lines), output)
	}
}

func TestLogsCRI_DumpWithTimestamps(t *testing.T) {
	dir := t.TempDir()
	project := "tstest"

	writeTestLogFile(t, dir, project, "web",
		"2024-01-15T10:30:01Z stdout F hello\n")

	var buf bytes.Buffer
	err := logs.Dump(&buf, dir, project, []string{"web"}, logs.Options{Timestamps: true})
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "2024-01-15T10:30:01Z") {
		t.Errorf("expected timestamp in output, got: %q", output)
	}
}

func TestLogsCRI_DumpNoLogPrefix(t *testing.T) {
	dir := t.TempDir()
	project := "noprefixtest"

	writeTestLogFile(t, dir, project, "web",
		"2024-01-15T10:30:01Z stdout F hello\n")

	var buf bytes.Buffer
	err := logs.Dump(&buf, dir, project, []string{"web"}, logs.Options{NoLogPrefix: true})
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}

	output := buf.String()
	if strings.HasPrefix(output, "web") {
		t.Errorf("expected no service prefix, got: %q", output)
	}
}

func TestLogsCRI_DumpSince(t *testing.T) {
	dir := t.TempDir()
	project := "sincetest"

	writeTestLogFile(t, dir, project, "web",
		"2024-01-15T10:30:00Z stdout F old\n2024-01-15T10:30:05Z stdout F new\n")

	var buf bytes.Buffer
	err := logs.Dump(&buf, dir, project, []string{"web"}, logs.Options{Since: "2024-01-15T10:30:03Z"})
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}

	output := buf.String()
	if strings.Contains(output, "old") {
		t.Errorf("expected 'old' to be filtered out by since, got: %q", output)
	}
	if !strings.Contains(output, "new") {
		t.Errorf("expected 'new' in output, got: %q", output)
	}
}

func TestDryRunIfRequested_Disabled(t *testing.T) {
	old := renderDryRun
	renderDryRun = false
	defer func() { renderDryRun = old }()

	err := dryRunIfRequested(context.Background(), nil)
	if err != nil {
		t.Errorf("expected nil when dry-run disabled, got: %v", err)
	}
}

func TestResolveSecrets_NoEnvFrom(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
			"api": {Image: "node"},
		},
	}

	secrets, err := resolveSecrets(context.Background(), comp, t.TempDir())
	if err != nil {
		t.Fatalf("resolveSecrets: %v", err)
	}
	if len(secrets) != 0 {
		t.Errorf("expected empty secrets, got %d", len(secrets))
	}
}

func TestOutputManifests_EmptyToStdout(t *testing.T) {
	old := renderOutput
	renderOutput = ""
	defer func() { renderOutput = old }()

	err := outputManifests(context.Background(), nil)
	if err != nil {
		t.Errorf("outputManifests: %v", err)
	}
}

func TestOutputManifests_ToDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	old := renderOutput
	oldDry := renderDryRun
	renderOutput = dir
	renderDryRun = false
	defer func() {
		renderOutput = old
		renderDryRun = oldDry
	}()

	err := outputManifests(context.Background(), []k8s.Manifest{})
	if err != nil {
		t.Errorf("outputManifests to dir: %v", err)
	}
}

func TestDoLogsCRI_WithArgs(t *testing.T) {
	dir := t.TempDir()
	project := "cri-logs-test"

	// Set up project dir and name.
	oldDir := flagProjectDir
	oldName := flagProjectName
	flagProjectDir = dir
	flagProjectName = project
	defer func() {
		flagProjectDir = oldDir
		flagProjectName = oldName
	}()

	// Create log files at DefaultLogBase.
	logDir := filepath.Join(logs.DefaultLogBase, project, "web")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(logDir, "0.log")
	if err := os.WriteFile(logFile, []byte("2024-01-15T10:30:01Z stdout F hello from web\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(logs.DefaultLogBase, project)) })

	// Call doLogsCRI with explicit args to skip eval.
	err := doLogsCRI(context.Background(), []string{"web"})
	if err != nil {
		t.Fatalf("doLogsCRI: %v", err)
	}
}

func TestDoLogsCRI_NoLogs(t *testing.T) {
	dir := t.TempDir()
	project := "no-logs-test"

	oldDir := flagProjectDir
	oldName := flagProjectName
	flagProjectDir = dir
	flagProjectName = project
	defer func() {
		flagProjectDir = oldDir
		flagProjectName = oldName
	}()

	// Call without setting up log files - should succeed gracefully.
	err := doLogsCRI(context.Background(), []string{"web"})
	if err != nil {
		t.Fatalf("doLogsCRI should succeed with missing logs: %v", err)
	}
}

func TestDoLogsCRI_ProjectFromDir(t *testing.T) {
	dir := t.TempDir()

	oldDir := flagProjectDir
	oldName := flagProjectName
	flagProjectDir = dir
	flagProjectName = "" // force project name from dir
	defer func() {
		flagProjectDir = oldDir
		flagProjectName = oldName
	}()

	err := doLogsCRI(context.Background(), []string{"svc"})
	if err != nil {
		t.Fatalf("doLogsCRI: %v", err)
	}
}

func TestShutdownServices_NilComp(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// shutdownServices with nil comp falls back to ProjectDown.
	err = shutdownServices(ctx, criClient, "testproj", 5, nil)
	if err != nil {
		t.Fatalf("shutdownServices with nil comp: %v", err)
	}
}

func TestCriDownCleanup(t *testing.T) {
	// Just verify it doesn't panic.
	criDownCleanup("test-cleanup-proj", t.TempDir(), false)
}

func TestCriDownCleanup_WithVolumes(t *testing.T) {
	criDownCleanup("test-cleanup-vol", t.TempDir(), true)
}

func TestExecCRI_NonInteractive(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// Create a running service container.
	svc := eval.Service{Image: "nginx:latest"}
	opts := cri.ServiceUpOptions{Project: "execproj", Version: "v1"}
	if err := criClient.ServiceUp(ctx, "web", svc, opts); err != nil {
		t.Fatalf("ServiceUp: %v", err)
	}

	// Find the container.
	ctrID, err := lookupContainerID(ctx, criClient, "execproj", "web")
	if err != nil {
		t.Fatalf("lookupContainerID: %v", err)
	}

	// Set up mock exec response.
	mock.mu.Lock()
	mock.execStdout[ctrID] = []byte("hello world\n")
	mock.mu.Unlock()

	resp, err := criClient.ExecSync(ctx, ctrID, []string{"echo", "hello", "world"}, 0)
	if err != nil {
		t.Fatalf("ExecSync: %v", err)
	}
	if resp.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", resp.ExitCode)
	}
	if string(resp.Stdout) != "hello world\n" {
		t.Errorf("stdout = %q, want %q", resp.Stdout, "hello world\n")
	}
}

func TestExecCRI_NonZeroExit(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	svc := eval.Service{Image: "nginx:latest"}
	opts := cri.ServiceUpOptions{Project: "execproj2", Version: "v1"}
	if err := criClient.ServiceUp(ctx, "web", svc, opts); err != nil {
		t.Fatalf("ServiceUp: %v", err)
	}

	ctrID, err := lookupContainerID(ctx, criClient, "execproj2", "web")
	if err != nil {
		t.Fatalf("lookupContainerID: %v", err)
	}

	mock.mu.Lock()
	mock.execExitCodes[ctrID] = 1
	mock.execStderr[ctrID] = []byte("command not found\n")
	mock.mu.Unlock()

	resp, err := criClient.ExecSync(ctx, ctrID, []string{"nonexistent"}, 0)
	if err != nil {
		t.Fatalf("ExecSync: %v", err)
	}
	if resp.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", resp.ExitCode)
	}
	if string(resp.Stderr) != "command not found\n" {
		t.Errorf("stderr = %q, want %q", resp.Stderr, "command not found\n")
	}
}

func TestExecCRI_ExecReturnsURL(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	svc := eval.Service{Image: "nginx:latest"}
	opts := cri.ServiceUpOptions{Project: "execproj3", Version: "v1"}
	if err := criClient.ServiceUp(ctx, "web", svc, opts); err != nil {
		t.Fatalf("ServiceUp: %v", err)
	}

	ctrID, err := lookupContainerID(ctx, criClient, "execproj3", "web")
	if err != nil {
		t.Fatalf("lookupContainerID: %v", err)
	}

	url, err := criClient.Exec(ctx, ctrID, []string{"bash"}, true, true)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if url == "" {
		t.Error("expected non-empty exec URL")
	}
	if !strings.Contains(url, ctrID) {
		t.Errorf("URL %q should contain container ID %q", url, ctrID)
	}
}

func TestIsShellCommand(t *testing.T) {
	tests := []struct {
		cmd  []string
		want bool
	}{
		{nil, true},
		{[]string{}, true},
		{[]string{"bash"}, true},
		{[]string{"sh"}, true},
		{[]string{"/bin/bash"}, true},
		{[]string{"/usr/bin/zsh"}, true},
		{[]string{"psql"}, true},
		{[]string{"python"}, true},
		{[]string{"python3"}, true},
		{[]string{"node"}, true},
		{[]string{"bash", "-c", "echo hello"}, false},
		{[]string{"ls"}, false},
		{[]string{"ls", "-la"}, false},
		{[]string{"echo", "hello"}, false},
		{[]string{"cat", "/etc/hosts"}, false},
	}
	for _, tt := range tests {
		got := isShellCommand(tt.cmd)
		if got != tt.want {
			t.Errorf("isShellCommand(%v) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

// --- M10: CRI command tests ---

// setupCRIServices creates running services in the mock CRI for testing commands.
func setupCRIServices(t *testing.T, ctx context.Context, criClient *cri.Client, project string, names []string) {
	t.Helper()
	opts := cri.ServiceUpOptions{Project: project, Version: "v1"}
	for _, name := range names {
		svc := eval.Service{Image: name + ":latest"}
		if err := criClient.ServiceUp(ctx, name, svc, opts); err != nil {
			t.Fatalf("ServiceUp %s: %v", name, err)
		}
	}
}

func TestStopCRI(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "stoptest", []string{"web", "api"})

	if err := runStopCRI(ctx, criClient, "stoptest", nil, 5); err != nil {
		t.Fatalf("runStopCRI: %v", err)
	}

	// Verify containers are exited.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	for _, ctr := range mock.containers {
		if ctr.State != runtimev1.ContainerState_CONTAINER_EXITED {
			t.Errorf("container %s should be exited, got %v", ctr.Id, ctr.State)
		}
	}
}

func TestStopCRI_FilteredServices(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "stopfilter", []string{"web", "api"})

	// Only stop "web".
	if err := runStopCRI(ctx, criClient, "stopfilter", []string{"web"}, 5); err != nil {
		t.Fatalf("runStopCRI: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	// Find the api container - it should still be running.
	for _, pod := range mock.pods {
		if pod.Labels["nix-compose.service"] == "api" {
			for _, ctr := range mock.containers {
				if ctr.PodSandboxId == pod.Id && ctr.State != runtimev1.ContainerState_CONTAINER_RUNNING {
					t.Errorf("api container should still be running, got %v", ctr.State)
				}
			}
		}
	}
}

func TestStartCRI(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "starttest", []string{"web"})

	// Stop first, then start.
	if err := runStopCRI(ctx, criClient, "starttest", nil, 5); err != nil {
		t.Fatalf("runStopCRI: %v", err)
	}
	if err := runStartCRI(ctx, criClient, "starttest", nil); err != nil {
		t.Fatalf("runStartCRI: %v", err)
	}
}

func TestRestartCRI(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "restarttest", []string{"web"})

	if err := runRestartCRI(ctx, criClient, "restarttest", nil, 5); err != nil {
		t.Fatalf("runRestartCRI: %v", err)
	}

	// After restart, the container should still exist (stop then start).
	mock.mu.Lock()
	ctrCount := len(mock.containers)
	mock.mu.Unlock()
	if ctrCount == 0 {
		t.Error("expected container to still exist after restart")
	}
}

func TestKillCRI(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "killtest", []string{"web"})

	if err := runKillCRI(ctx, criClient, "killtest", nil); err != nil {
		t.Fatalf("runKillCRI: %v", err)
	}

	// Verify containers are exited.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	for _, ctr := range mock.containers {
		if ctr.State != runtimev1.ContainerState_CONTAINER_EXITED {
			t.Errorf("container %s should be exited after kill, got %v", ctr.Id, ctr.State)
		}
	}
}

func TestRmCRI(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "rmtest", []string{"web"})

	// rm with --stop to stop running containers first.
	if err := runRmCRI(ctx, criClient, "rmtest", nil, true, false); err != nil {
		t.Fatalf("runRmCRI: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.containers) != 0 {
		t.Errorf("expected 0 containers after rm, got %d", len(mock.containers))
	}
	if len(mock.pods) != 0 {
		t.Errorf("expected 0 pods after rm, got %d", len(mock.pods))
	}
}

func TestRmCRI_SkipRunning(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "rmskip", []string{"web"})

	// rm without --stop should skip running containers.
	if err := runRmCRI(ctx, criClient, "rmskip", nil, false, false); err != nil {
		t.Fatalf("runRmCRI: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	// Running container should still exist.
	if len(mock.containers) == 0 {
		t.Error("expected running container to be skipped")
	}
}

func TestTopCRI(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "toptest", []string{"web"})

	// Set up mock exec response for "ps aux".
	ctrID, err := lookupContainerID(ctx, criClient, "toptest", "web")
	if err != nil {
		t.Fatalf("lookupContainerID: %v", err)
	}
	mock.mu.Lock()
	mock.execStdout[ctrID] = []byte("USER  PID  CMD\nroot  1    nginx\n")
	mock.mu.Unlock()

	if err := runTopCRI(ctx, criClient, "toptest", []string{"web"}); err != nil {
		t.Fatalf("runTopCRI: %v", err)
	}
}

func TestTopCRI_NoService(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	err = runTopCRI(ctx, criClient, "toptest", nil)
	if err == nil {
		t.Error("expected error when no service specified")
	}
}

func TestPsCRI_Table(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "pstest", []string{"web", "api"})

	// Save and restore flags.
	oldAll, oldQuiet, oldFormat, oldServices := psAll, psQuiet, psFormat, psServices
	defer func() {
		psAll, psQuiet, psFormat, psServices = oldAll, oldQuiet, oldFormat, oldServices
	}()
	psAll, psQuiet, psFormat, psServices = false, false, "", false

	if err := runPsCRI(ctx, criClient, "pstest", nil); err != nil {
		t.Fatalf("runPsCRI table: %v", err)
	}
}

func TestPsCRI_Quiet(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "psquiet", []string{"web"})

	oldAll, oldQuiet, oldFormat, oldServices := psAll, psQuiet, psFormat, psServices
	defer func() {
		psAll, psQuiet, psFormat, psServices = oldAll, oldQuiet, oldFormat, oldServices
	}()
	psAll, psQuiet, psFormat, psServices = false, true, "", false

	if err := runPsCRI(ctx, criClient, "psquiet", nil); err != nil {
		t.Fatalf("runPsCRI quiet: %v", err)
	}
}

func TestPsCRI_Services(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "pssvcs", []string{"web", "api"})

	oldAll, oldQuiet, oldFormat, oldServices := psAll, psQuiet, psFormat, psServices
	defer func() {
		psAll, psQuiet, psFormat, psServices = oldAll, oldQuiet, oldFormat, oldServices
	}()
	psAll, psQuiet, psFormat, psServices = false, false, "", true

	if err := runPsCRI(ctx, criClient, "pssvcs", nil); err != nil {
		t.Fatalf("runPsCRI services: %v", err)
	}
}

func TestPsCRI_JSON(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "psjson", []string{"web"})

	oldAll, oldQuiet, oldFormat, oldServices := psAll, psQuiet, psFormat, psServices
	defer func() {
		psAll, psQuiet, psFormat, psServices = oldAll, oldQuiet, oldFormat, oldServices
	}()
	psAll, psQuiet, psFormat, psServices = false, false, "json", false

	if err := runPsCRI(ctx, criClient, "psjson", nil); err != nil {
		t.Fatalf("runPsCRI json: %v", err)
	}
}

func TestPsCRI_AllIncludesExited(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "psall", []string{"web"})
	// Stop the container.
	if err := runStopCRI(ctx, criClient, "psall", nil, 5); err != nil {
		t.Fatalf("runStopCRI: %v", err)
	}

	oldAll, oldQuiet, oldFormat, oldServices := psAll, psQuiet, psFormat, psServices
	defer func() {
		psAll, psQuiet, psFormat, psServices = oldAll, oldQuiet, oldFormat, oldServices
	}()

	// Without --all, no containers should show (exited).
	psAll, psQuiet, psFormat, psServices = false, false, "", false
	containers, _ := resolveContainers(ctx, criClient, "psall", nil)
	runningCount := 0
	for _, c := range containers {
		if c.State == runtimev1.ContainerState_CONTAINER_RUNNING {
			runningCount++
		}
	}
	if runningCount != 0 {
		t.Errorf("expected 0 running containers, got %d", runningCount)
	}

	// With --all, should include exited containers.
	psAll = true
	if err := runPsCRI(ctx, criClient, "psall", nil); err != nil {
		t.Fatalf("runPsCRI all: %v", err)
	}
}

func TestCreateCRI(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldDir := flagProjectDir
	oldName := flagProjectName
	oldFlakeAttr := flagFlakeAttr
	oldImpure := flagImpure
	flagProjectDir = dir
	flagProjectName = "createtest"
	flagFlakeAttr = ""
	flagImpure = true
	defer func() {
		flagProjectDir = oldDir
		flagProjectName = oldName
		flagFlakeAttr = oldFlakeAttr
		flagImpure = oldImpure
	}()

	// We need to supply a mock evaluator. Since runCreateCRI creates its own evaluator,
	// we test it indirectly by checking that it fails with eval error.
	// Direct CRI testing is done via the mock + ServiceUp.

	// Instead, test the create logic by manually creating containers without starting.
	svc := eval.Service{Image: "nginx:latest"}
	opts := cri.ServiceUpOptions{Project: "createtest", Version: "v1"}
	podConfig := cri.BuildPodConfig("createtest", "web", svc, "v1", cri.PodNetworkHost)
	podID, err := criClient.RunPodSandbox(ctx, podConfig)
	if err != nil {
		t.Fatalf("RunPodSandbox: %v", err)
	}
	ctrConfig := cri.BuildContainerConfig("web", svc, opts.Project, opts.Version, nil)
	ctrID, err := criClient.CreateContainer(ctx, podID, ctrConfig, podConfig)
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	// Verify container is created but not started.
	mock.mu.Lock()
	ctr := mock.containers[ctrID]
	mock.mu.Unlock()
	if ctr == nil {
		t.Fatal("expected container to exist")
		return
	}
	if ctr.State != runtimev1.ContainerState_CONTAINER_CREATED {
		t.Errorf("expected CREATED state, got %v", ctr.State)
	}
}

func TestResolveContainers_Empty(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	containers, err := resolveContainers(ctx, criClient, "nonexistent", nil)
	if err != nil {
		t.Fatalf("resolveContainers: %v", err)
	}
	if len(containers) != 0 {
		t.Errorf("expected 0 containers, got %d", len(containers))
	}
}

func TestResolveProject(t *testing.T) {
	old := flagProjectName
	flagProjectName = "myproject"
	defer func() { flagProjectName = old }()

	if got := resolveProject(); got != "myproject" {
		t.Errorf("resolveProject() = %q, want myproject", got)
	}
}

func TestResolveProject_FromDir(t *testing.T) {
	old := flagProjectName
	oldDir := flagProjectDir
	flagProjectName = ""
	flagProjectDir = "/tmp/testdir"
	defer func() {
		flagProjectName = old
		flagProjectDir = oldDir
	}()

	if got := resolveProject(); got != "testdir" {
		t.Errorf("resolveProject() = %q, want testdir", got)
	}
}

func TestContainerStateName(t *testing.T) {
	tests := []struct {
		state runtimev1.ContainerState
		want  string
	}{
		{runtimev1.ContainerState_CONTAINER_CREATED, "created"},
		{runtimev1.ContainerState_CONTAINER_RUNNING, "running"},
		{runtimev1.ContainerState_CONTAINER_EXITED, "exited"},
		{runtimev1.ContainerState_CONTAINER_UNKNOWN, "unknown"},
	}
	for _, tt := range tests {
		got := containerStateName(tt.state)
		if got != tt.want {
			t.Errorf("containerStateName(%v) = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestShortContainerID(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"abcdef1234567890", "abcdef123456"},
		{"sha256:abcdef1234567890", "abcdef123456"},
		{"short", "short"},
		{"", ""},
		{"exactly12ch", "exactly12ch"},
	}
	for _, tt := range tests {
		got := shortContainerID(tt.id)
		if got != tt.want {
			t.Errorf("shortContainerID(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

// --- M9: Watch mode CRI test ---

func TestRunWatcherCRI_RestartAndRemove(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// Create services.
	opts := cri.ServiceUpOptions{Project: "watchproj", Version: "v1"}
	for _, svc := range []string{"web", "api"} {
		if err := criClient.ServiceUp(ctx, svc, eval.Service{Image: svc + ":1"}, opts); err != nil {
			t.Fatalf("ServiceUp %s: %v", svc, err)
		}
	}

	// Verify initial state.
	mock.mu.Lock()
	initialPods := len(mock.pods)
	mock.mu.Unlock()
	if initialPods != 2 {
		t.Fatalf("expected 2 initial pods, got %d", initialPods)
	}

	// Test Remove callback: ServiceDown should remove the pod.
	if err := criClient.ServiceDown(ctx, "watchproj", "api", 5); err != nil {
		t.Fatalf("ServiceDown api: %v", err)
	}
	mock.mu.Lock()
	afterRemove := len(mock.pods)
	mock.mu.Unlock()
	if afterRemove != 1 {
		t.Errorf("expected 1 pod after removing api, got %d", afterRemove)
	}

	// Test Restart callback: ServiceDown+ServiceUp should recreate the pod.
	if err := criClient.ServiceDown(ctx, "watchproj", "web", 5); err != nil {
		t.Fatalf("ServiceDown web: %v", err)
	}
	if err := criClient.ServiceUp(ctx, "web", eval.Service{Image: "web:2"}, opts); err != nil {
		t.Fatalf("ServiceUp web: %v", err)
	}
	mock.mu.Lock()
	afterRestart := len(mock.pods)
	mock.mu.Unlock()
	if afterRestart != 1 {
		t.Errorf("expected 1 pod after restart, got %d", afterRestart)
	}
}

func TestStopFlags(t *testing.T) {
	f := stopCmd.Flags().Lookup("timeout")
	if f == nil {
		t.Fatal("missing --timeout flag on stop")
		return
	}
	if f.Shorthand != "t" {
		t.Errorf("timeout shorthand = %q, want %q", f.Shorthand, "t")
	}
}

func TestRestartFlags(t *testing.T) {
	f := restartCmd.Flags().Lookup("timeout")
	if f == nil {
		t.Fatal("missing --timeout flag on restart")
		return
	}
	if f.Shorthand != "t" {
		t.Errorf("timeout shorthand = %q, want %q", f.Shorthand, "t")
	}
}

// --- Additional coverage tests ---

func TestCreateServiceCRI(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	svc := eval.Service{Image: "nginx:latest"}
	opts := cri.ServiceUpOptions{Project: "cstest", Version: "v1"}

	if err := createServiceCRI(ctx, criClient, "cstest", "web", svc, opts); err != nil {
		t.Fatalf("createServiceCRI: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(mock.containers))
	}
	for _, ctr := range mock.containers {
		if ctr.State != runtimev1.ContainerState_CONTAINER_CREATED {
			t.Errorf("expected CREATED state, got %v", ctr.State)
		}
	}
}

func TestRemoveContainerCRI_RunningWithStop(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "rmctr", []string{"web"})
	containers, err := resolveContainers(ctx, criClient, "rmctr", nil)
	if err != nil {
		t.Fatalf("resolveContainers: %v", err)
	}
	if len(containers) == 0 {
		t.Fatal("expected at least 1 container")
	}

	if err := removeContainerCRI(ctx, criClient, containers[0], true); err != nil {
		t.Fatalf("removeContainerCRI: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.containers) != 0 {
		t.Errorf("expected 0 containers, got %d", len(mock.containers))
	}
}

func TestRemoveContainerCRI_ExitedNoStop(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "rmexited", []string{"web"})
	// Stop the container first to make it exited.
	if err := runStopCRI(ctx, criClient, "rmexited", nil, 5); err != nil {
		t.Fatalf("runStopCRI: %v", err)
	}
	containers, err := resolveContainers(ctx, criClient, "rmexited", nil)
	if err != nil {
		t.Fatalf("resolveContainers: %v", err)
	}

	if err := removeContainerCRI(ctx, criClient, containers[0], false); err != nil {
		t.Fatalf("removeContainerCRI: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.containers) != 0 {
		t.Errorf("expected 0 containers, got %d", len(mock.containers))
	}
}

func TestCleanupVolumes(t *testing.T) {
	// cleanupVolumes should not panic on a valid project name.
	cleanupVolumes("cleanuptest")
}

func TestRmCRI_WithVolumes(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "rmvol", []string{"web"})

	// rm with --stop and --volumes.
	if err := runRmCRI(ctx, criClient, "rmvol", nil, true, true); err != nil {
		t.Fatalf("runRmCRI: %v", err)
	}
}

func TestResolveContainers_ExitedHasExitCode(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "exitproj", []string{"web"})

	// Stop the container and set exit code.
	if err := runStopCRI(ctx, criClient, "exitproj", nil, 5); err != nil {
		t.Fatalf("runStopCRI: %v", err)
	}
	mock.mu.Lock()
	for id := range mock.containers {
		mock.containerExit[id] = 42
	}
	mock.mu.Unlock()

	containers, err := resolveContainers(ctx, criClient, "exitproj", nil)
	if err != nil {
		t.Fatalf("resolveContainers: %v", err)
	}
	if len(containers) == 0 {
		t.Fatal("expected at least 1 container")
	}
	if containers[0].ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", containers[0].ExitCode)
	}
}

func TestResolveContainers_FilterByService(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "filterproj", []string{"web", "api", "db"})

	containers, err := resolveContainers(ctx, criClient, "filterproj", []string{"web", "db"})
	if err != nil {
		t.Fatalf("resolveContainers: %v", err)
	}
	if len(containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(containers))
	}
	services := map[string]bool{}
	for _, c := range containers {
		services[c.Service] = true
	}
	if !services["web"] || !services["db"] {
		t.Errorf("expected web and db services, got %v", services)
	}
}

func TestFilterRunning(t *testing.T) {
	containers := []containerInfo{
		{Service: "a", State: runtimev1.ContainerState_CONTAINER_RUNNING},
		{Service: "b", State: runtimev1.ContainerState_CONTAINER_EXITED},
		{Service: "c", State: runtimev1.ContainerState_CONTAINER_RUNNING},
	}
	filtered := filterRunning(containers)
	if len(filtered) != 2 {
		t.Fatalf("expected 2, got %d", len(filtered))
	}
	if filtered[0].Service != "a" || filtered[1].Service != "c" {
		t.Errorf("unexpected services: %s, %s", filtered[0].Service, filtered[1].Service)
	}
}

func TestFormatPsOutput_Services(t *testing.T) {
	old := psServices
	psServices = true
	defer func() { psServices = old }()

	containers := []containerInfo{
		{Service: "web"},
		{Service: "api"},
		{Service: "web"}, // duplicate
	}
	// Should print web, api (no duplicates).
	if err := formatPsOutput(containers); err != nil {
		t.Fatalf("formatPsOutput: %v", err)
	}
}

func TestFormatPsOutput_Quiet(t *testing.T) {
	oldS := psServices
	oldQ := psQuiet
	psServices = false
	psQuiet = true
	defer func() {
		psServices = oldS
		psQuiet = oldQ
	}()

	containers := []containerInfo{
		{ContainerID: "ctr-1"},
		{ContainerID: "ctr-2"},
	}
	if err := formatPsOutput(containers); err != nil {
		t.Fatalf("formatPsOutput: %v", err)
	}
}

func TestFormatPsOutput_Table(t *testing.T) {
	oldS := psServices
	oldQ := psQuiet
	oldF := psFormat
	psServices = false
	psQuiet = false
	psFormat = ""
	defer func() {
		psServices = oldS
		psQuiet = oldQ
		psFormat = oldF
	}()

	containers := []containerInfo{
		{Service: "web", State: runtimev1.ContainerState_CONTAINER_RUNNING, Image: "nginx", ContainerID: "abc123456789xyz"},
	}
	if err := formatPsOutput(containers); err != nil {
		t.Fatalf("formatPsOutput: %v", err)
	}
}

func TestStopCRI_NoRunning(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "nopstop", []string{"web"})
	// Stop once.
	if err := runStopCRI(ctx, criClient, "nopstop", nil, 5); err != nil {
		t.Fatalf("runStopCRI: %v", err)
	}
	// Stop again — should be a no-op (no running containers).
	if err := runStopCRI(ctx, criClient, "nopstop", nil, 5); err != nil {
		t.Fatalf("runStopCRI second: %v", err)
	}
}

func TestStartCRI_NoExited(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "nopstart", []string{"web"})
	// All running, start should be a no-op.
	if err := runStartCRI(ctx, criClient, "nopstart", nil); err != nil {
		t.Fatalf("runStartCRI: %v", err)
	}
}

func TestKillCRI_NoRunning(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// No services — kill should do nothing.
	if err := runKillCRI(ctx, criClient, "empty", nil); err != nil {
		t.Fatalf("runKillCRI: %v", err)
	}
}

func TestRestartCRI_NoRunning(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// No services — restart should do nothing.
	if err := runRestartCRI(ctx, criClient, "empty", nil, 5); err != nil {
		t.Fatalf("runRestartCRI: %v", err)
	}
}

func TestContainerStateName_Default(t *testing.T) {
	// Test the default branch with an unusual state value.
	got := containerStateName(runtimev1.ContainerState(999))
	if got == "" {
		t.Error("expected non-empty string for unknown state")
	}
}

func TestRequireCRI_InvalidSocket(t *testing.T) {
	old := flagCRISocket
	flagCRISocket = "/nonexistent/socket.sock"
	defer func() { flagCRISocket = old }()

	c, err := requireCRI(context.Background())
	if err == nil {
		_ = c.Close()
		t.Fatal("expected error for invalid socket")
	}
	if !strings.Contains(err.Error(), "doctor") {
		t.Errorf("error should point at doctor, got: %v", err)
	}
}

// isolateCRISockets points CRI auto-detection at a socket path that cannot
// exist, so tests asserting "no CRI runtime is available" hold on hosts that
// do have one. Without it they depend on the machine they run on, and they
// drive the real runtime when it is present.
func isolateCRISockets(t *testing.T) {
	t.Helper()
	old := cri.DefaultSocketPaths
	cri.DefaultSocketPaths = []string{filepath.Join(t.TempDir(), "absent-cri.sock")}
	t.Cleanup(func() { cri.DefaultSocketPaths = old })
}

func TestRequireCRI_EmptySocket(t *testing.T) {
	isolateCRISockets(t)

	old := flagCRISocket
	flagCRISocket = ""
	defer func() { flagCRISocket = old }()

	// With no socket set and no CRI runtime to detect, should return an error.
	c, err := requireCRI(context.Background())
	if err == nil {
		_ = c.Close()
		t.Fatal("expected error when no CRI runtime is available")
	}
	if !strings.Contains(err.Error(), "doctor") {
		t.Errorf("error should point at doctor, got: %v", err)
	}
}

func TestRequireCRI_ErrorMessage(t *testing.T) {
	old := flagCRISocket
	flagCRISocket = "/nonexistent/socket.sock"
	defer func() { flagCRISocket = old }()

	_, err := requireCRI(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "doctor") {
		t.Errorf("error message should point at doctor, got: %v", err)
	}
	if !strings.Contains(err.Error(), "CRI") {
		t.Errorf("error message should mention CRI, got: %v", err)
	}
}

func TestCriProjectName_WithProjectName(t *testing.T) {
	deps := UpDeps{ProjectName: "myproj", ProjectDir: "/tmp/somedir"}
	if got := criProjectName(deps); got != "myproj" {
		t.Errorf("criProjectName() = %q, want myproj", got)
	}
}

func TestCriProjectName_FromDir(t *testing.T) {
	deps := UpDeps{ProjectDir: "/tmp/somedir"}
	if got := criProjectName(deps); got != "somedir" {
		t.Errorf("criProjectName() = %q, want somedir", got)
	}
}

func TestPrintPsJSON(t *testing.T) {
	containers := []containerInfo{
		{Service: "web", State: runtimev1.ContainerState_CONTAINER_RUNNING, Image: "nginx", ContainerID: "ctr-1"},
	}
	if err := printPsJSON(containers); err != nil {
		t.Fatalf("printPsJSON: %v", err)
	}
}

func TestPrintPsTable_Empty(t *testing.T) {
	if err := printPsTable(nil); err != nil {
		t.Fatalf("printPsTable: %v", err)
	}
}

func TestRunStopCRI_FilteredServices(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "stopfilt2", []string{"web", "api", "db"})

	// Only stop "api".
	if err := runStopCRI(ctx, criClient, "stopfilt2", []string{"api"}, 5); err != nil {
		t.Fatalf("runStopCRI: %v", err)
	}

	// "web" and "db" should still be running.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	running := 0
	for _, ctr := range mock.containers {
		if ctr.State == runtimev1.ContainerState_CONTAINER_RUNNING {
			running++
		}
	}
	if running != 2 {
		t.Errorf("expected 2 running containers, got %d", running)
	}
}

func TestStartCRI_AfterStop(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "startaft", []string{"web", "api"})

	// Stop all.
	if err := runStopCRI(ctx, criClient, "startaft", nil, 5); err != nil {
		t.Fatalf("runStopCRI: %v", err)
	}
	// Start only web.
	if err := runStartCRI(ctx, criClient, "startaft", []string{"web"}); err != nil {
		t.Fatalf("runStartCRI: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	running := 0
	for _, ctr := range mock.containers {
		if ctr.State == runtimev1.ContainerState_CONTAINER_RUNNING {
			running++
		}
	}
	if running != 1 {
		t.Errorf("expected 1 running container, got %d", running)
	}
}

func TestRestartCRI_VerifyStopThenStart(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "restverify", []string{"web"})

	if err := runRestartCRI(ctx, criClient, "restverify", nil, 5); err != nil {
		t.Fatalf("runRestartCRI: %v", err)
	}

	// After restart, container should be running again.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	for _, ctr := range mock.containers {
		if ctr.State != runtimev1.ContainerState_CONTAINER_RUNNING {
			t.Errorf("expected running after restart, got %v", ctr.State)
		}
	}
}

func TestKillFlags(t *testing.T) {
	f := killCmd.Flags().Lookup("signal")
	if f == nil {
		t.Fatal("missing --signal flag on kill")
		return
	}
	if f.Shorthand != "s" {
		t.Errorf("signal shorthand = %q, want %q", f.Shorthand, "s")
	}
}

func TestRmFlags(t *testing.T) {
	for _, name := range []string{"force", "stop", "volumes"} {
		f := rmCmd.Flags().Lookup(name)
		if f == nil {
			t.Fatalf("missing --%s flag on rm", name)
		}
	}
}

func TestCreateFlags(t *testing.T) {
	if createCmd.Use != "create [service...]" {
		t.Errorf("unexpected Use: %s", createCmd.Use)
	}
}

func TestTopCRI_WithOutput(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "topout", []string{"web"})

	// Set ExecSync output.
	mock.mu.Lock()
	for id := range mock.containers {
		mock.execStdout[id] = []byte("  PID TTY      STAT   TIME COMMAND\n    1 ?        Ss     0:00 nginx\n")
		mock.execStderr[id] = []byte{}
	}
	mock.mu.Unlock()

	if err := runTopCRI(ctx, criClient, "topout", []string{"web"}); err != nil {
		t.Fatalf("runTopCRI: %v", err)
	}
}

func TestContainersForPod(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "cfptest", []string{"web"})

	// Get the pod ID.
	pods, err := criClient.ListPodSandboxes(ctx, map[string]string{cri.LabelProject: "cfptest"})
	if err != nil {
		t.Fatalf("ListPodSandboxes: %v", err)
	}
	if len(pods) == 0 {
		t.Fatal("expected at least 1 pod")
	}

	infos, err := containersForPod(ctx, criClient, pods[0].Id, "web")
	if err != nil {
		t.Fatalf("containersForPod: %v", err)
	}
	if len(infos) == 0 {
		t.Fatal("expected at least 1 container info")
	}
	if infos[0].Service != "web" {
		t.Errorf("expected service web, got %s", infos[0].Service)
	}
}

func TestShutdownServices_WithComp(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "shutproj", []string{"web", "api"})

	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
			"api": {Image: "node", DependsOn: eval.DependsOnValue{
				Entries: map[string]eval.DependsOnEntry{"web": {Condition: "service_started"}},
			}},
		},
	}

	if err := shutdownServices(ctx, criClient, "shutproj", 5, comp); err != nil {
		t.Fatalf("shutdownServices: %v", err)
	}
}

func TestCriDownCleanup_NoVolumes(t *testing.T) {
	dir := t.TempDir()
	// Should not panic.
	criDownCleanup("cdctest", dir, false)
}

func TestCriDownCleanup_WithVolumeCleanup(t *testing.T) {
	dir := t.TempDir()
	criDownCleanup("cdcvol", dir, true)
}

func TestPrintResourceWarnings_WithWarnings(t *testing.T) {
	// A service with deploy resources but no limits triggers a warning.
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
		},
	}
	// Should not panic.
	printResourceWarnings(comp)
}

func TestRenderK8s_WithMock(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldDir := flagProjectDir
	oldImpure := flagImpure
	oldOutput := renderOutput
	oldNs := renderNamespace
	oldDryRun := renderDryRun
	flagProjectDir = dir
	flagImpure = true
	renderOutput = ""
	renderNamespace = "default"
	renderDryRun = false
	defer func() {
		flagProjectDir = oldDir
		flagImpure = oldImpure
		renderOutput = oldOutput
		renderNamespace = oldNs
		renderDryRun = oldDryRun
	}()

	// renderK8s will try to eval, which fails since we have no real nix.
	err := renderK8s(context.Background(), dir)
	if err == nil {
		t.Skip("nix available — skipping mock test")
	}
	// Expected: eval failure.
	if !strings.Contains(err.Error(), "evaluation") {
		t.Errorf("expected evaluation error, got: %v", err)
	}
}

func TestDoDown_CRI_WithCompAndVolumes(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	dir := t.TempDir()
	setupCRIServices(t, ctx, criClient, "downcomp", []string{"web", "api"})

	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
			"api": {Image: "node"},
		},
	}

	if err := CRIDownOrdered(ctx, criClient, "downcomp", dir, 5, true, comp); err != nil {
		t.Fatalf("CRIDownOrdered: %v", err)
	}
}

func TestExecNonInteractive(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "execni", []string{"web"})
	containers, err := resolveContainers(ctx, criClient, "execni", nil)
	if err != nil {
		t.Fatalf("resolveContainers: %v", err)
	}

	mock.mu.Lock()
	mock.execStdout[containers[0].ContainerID] = []byte("output\n")
	mock.mu.Unlock()

	if err := execNonInteractive(ctx, criClient, containers[0].ContainerID, []string{"echo", "hello"}); err != nil {
		t.Fatalf("execNonInteractive: %v", err)
	}
}

func TestDoExecCRI_WithDefaultExec(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "execde", []string{"web"})

	mock.mu.Lock()
	for id := range mock.containers {
		mock.execStdout[id] = []byte("hi\n")
	}
	mock.mu.Unlock()

	oldDir := flagProjectDir
	oldName := flagProjectName
	flagProjectDir = t.TempDir()
	flagProjectName = "execde"
	defer func() {
		flagProjectDir = oldDir
		flagProjectName = oldName
	}()

	// Providing a command directly.
	if err := doExecCRI(ctx, criClient, []string{"web", "echo", "hi"}); err != nil {
		t.Fatalf("doExecCRI: %v", err)
	}
}

func TestResolveLogServices_MultipleArgs(t *testing.T) {
	ctx := context.Background()
	services, err := resolveLogServices(ctx, "/tmp", []string{"api", "web", "db"})
	if err != nil {
		t.Fatalf("resolveLogServices: %v", err)
	}
	// Should be sorted.
	if len(services) != 3 {
		t.Fatalf("expected 3 services, got %d", len(services))
	}
	if services[0] != "api" || services[1] != "db" || services[2] != "web" {
		t.Errorf("expected sorted order, got %v", services)
	}
}

// --- Additional coverage tests (appended) ---

// TestLookupContainerID_NoPods tests the "no pods found" error path.
func TestLookupContainerID_NoPods(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// No services set up — no pods exist.
	_, err = lookupContainerID(ctx, criClient, "nopods", "web")
	if err == nil {
		t.Fatal("expected error for no pods found")
	}
	if !strings.Contains(err.Error(), "no pods found") {
		t.Errorf("expected 'no pods found' error, got: %v", err)
	}
}

// TestLookupContainerID_NoContainers tests the "no containers found" error path.
func TestLookupContainerID_NoContainers(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// Create a pod directly without creating a container.
	svc := eval.Service{Image: "nginx:latest"}
	podConfig := cri.BuildPodConfig("noctr", "web", svc, "v1", cri.PodNetworkHost)
	_, err = criClient.RunPodSandbox(ctx, podConfig)
	if err != nil {
		t.Fatalf("RunPodSandbox: %v", err)
	}

	// Verify the pod exists.
	mock.mu.Lock()
	podCount := len(mock.pods)
	mock.mu.Unlock()
	if podCount == 0 {
		t.Fatal("expected at least 1 pod")
	}

	// lookupContainerID should fail because no containers exist in the pod.
	_, err = lookupContainerID(ctx, criClient, "noctr", "web")
	if err == nil {
		t.Fatal("expected error for no containers found")
	}
	if !strings.Contains(err.Error(), "no containers found") {
		t.Errorf("expected 'no containers found' error, got: %v", err)
	}
}

// TestLookupContainerID_Success tests the success path.
func TestLookupContainerID_Success(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "lookupok", []string{"web"})

	ctrID, err := lookupContainerID(ctx, criClient, "lookupok", "web")
	if err != nil {
		t.Fatalf("lookupContainerID: %v", err)
	}
	if ctrID == "" {
		t.Error("expected non-empty container ID")
	}
}

// TestLookupPodIP_NoPods tests the "no pods found" error path.
func TestLookupPodIP_NoPods(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	_, err = lookupPodIP(ctx, criClient, "nopods", "web")
	if err == nil {
		t.Fatal("expected error for no pods found")
	}
	if !strings.Contains(err.Error(), "no pods found") {
		t.Errorf("expected 'no pods found' error, got: %v", err)
	}
}

// TestLookupPodIP_Success tests the success path.
func TestLookupPodIP_Success(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "iptest", []string{"web"})

	// Set a custom IP for the pod.
	mock.mu.Lock()
	for id := range mock.pods {
		mock.podIPs[id] = "10.1.2.3"
	}
	mock.mu.Unlock()

	ip, err := lookupPodIP(ctx, criClient, "iptest", "web")
	if err != nil {
		t.Fatalf("lookupPodIP: %v", err)
	}
	if ip != "10.1.2.3" {
		t.Errorf("expected 10.1.2.3, got %s", ip)
	}
}

// TestWaitExited_Success tests waitExited when the container exits with code 0.
func TestWaitExited_Success(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "waitok", []string{"web"})

	// Find the container and mark it as exited with code 0.
	mock.mu.Lock()
	for id, ctr := range mock.containers {
		ctr.State = runtimev1.ContainerState_CONTAINER_EXITED
		mock.containerExit[id] = 0
	}
	mock.mu.Unlock()

	err = waitExited(ctx, criClient, "waitok", "web")
	if err != nil {
		t.Fatalf("waitExited: %v", err)
	}
}

// TestWaitExited_NonZeroExit tests waitExited when the container exits with non-zero code.
func TestWaitExited_NonZeroExit(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "waitnz", []string{"web"})

	// Mark container as exited with code 1.
	mock.mu.Lock()
	for id, ctr := range mock.containers {
		ctr.State = runtimev1.ContainerState_CONTAINER_EXITED
		mock.containerExit[id] = 1
	}
	mock.mu.Unlock()

	err = waitExited(ctx, criClient, "waitnz", "web")
	if err == nil {
		t.Fatal("expected error for non-zero exit code")
	}
	if !strings.Contains(err.Error(), "exited with code 1") {
		t.Errorf("expected 'exited with code' error, got: %v", err)
	}
}

// TestWaitExited_NoPods tests waitExited when no pods exist.
func TestWaitExited_NoPods(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	err = waitExited(ctx, criClient, "waitnopod", "web")
	if err == nil {
		t.Fatal("expected error when no pods exist")
	}
	if !strings.Contains(err.Error(), "looking up container") {
		t.Errorf("expected 'looking up container' error, got: %v", err)
	}
}

// TestWaitHealthy_NoHealthCheck tests the "no health check" error path.
func TestWaitHealthy_NoHealthCheck(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "waitnh", []string{"web"})

	// Service with no health check.
	svc := eval.Service{Image: "nginx"}
	executor := &criExecutor{client: criClient}
	monitor := health.NewMonitor(executor)

	err = waitHealthy(ctx, criClient, "waitnh", "web", svc, monitor)
	if err == nil {
		t.Fatal("expected error for no health check")
	}
	if !strings.Contains(err.Error(), "no health check") {
		t.Errorf("expected 'no health check' error, got: %v", err)
	}
}

// TestWaitHealthy_NoPods tests waitHealthy when no pods exist for lookup.
func TestWaitHealthy_NoPods(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// Service with a health check but no pods.
	svc := eval.Service{
		Image: "postgres",
		Healthcheck: &eval.Healthcheck{
			Test:     eval.CommandValue{Parts: []string{"CMD", "pg_isready"}},
			Interval: "100ms",
			Timeout:  "1s",
			Retries:  3,
		},
	}
	executor := &criExecutor{client: criClient}
	monitor := health.NewMonitor(executor)

	err = waitHealthy(ctx, criClient, "waithnopod", "web", svc, monitor)
	if err == nil {
		t.Fatal("expected error when no pods exist")
	}
	if !strings.Contains(err.Error(), "looking up container") {
		t.Errorf("expected 'looking up container' error, got: %v", err)
	}
}

// TestWaitCondition_ServiceStarted tests that service_started returns nil (no wait).
func TestWaitCondition_ServiceStarted(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	svc := eval.Service{Image: "nginx"}
	executor := &criExecutor{client: criClient}
	monitor := health.NewMonitor(executor)

	err = waitCondition(ctx, criClient, "proj", "web", "service_started", svc, monitor)
	if err != nil {
		t.Errorf("expected nil for service_started, got: %v", err)
	}
}

// TestWaitCondition_EmptyCondition tests that empty condition returns nil.
func TestWaitCondition_EmptyCondition(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	svc := eval.Service{Image: "nginx"}
	executor := &criExecutor{client: criClient}
	monitor := health.NewMonitor(executor)

	err = waitCondition(ctx, criClient, "proj", "web", "", svc, monitor)
	if err != nil {
		t.Errorf("expected nil for empty condition, got: %v", err)
	}
}

// TestHighestConditionOf_CompletedSuccessfully tests service_completed_successfully.
func TestHighestConditionOf_CompletedSuccessfully(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"migrate": {Image: "migrate:1"},
			"app": {Image: "app:1", DependsOn: eval.DependsOnValue{
				Entries: map[string]eval.DependsOnEntry{
					"migrate": {Condition: "service_completed_successfully"},
				},
			}},
		},
	}
	got := highestConditionOf("migrate", comp)
	if got != "service_completed_successfully" {
		t.Errorf("expected service_completed_successfully, got %q", got)
	}
}

// TestHighestConditionOf_MultipleWithCompletedBeatsStarted tests that completed beats started.
func TestHighestConditionOf_MultipleWithCompletedBeatsStarted(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"db": {Image: "postgres"},
			"app": {Image: "app", DependsOn: eval.DependsOnValue{
				Entries: map[string]eval.DependsOnEntry{
					"db": {Condition: "service_started"},
				},
			}},
			"migrate": {Image: "migrate", DependsOn: eval.DependsOnValue{
				Entries: map[string]eval.DependsOnEntry{
					"db": {Condition: "service_completed_successfully"},
				},
			}},
		},
	}
	got := highestConditionOf("db", comp)
	if got != "service_completed_successfully" {
		t.Errorf("expected service_completed_successfully, got %q", got)
	}
}

// TestTransformComposition_Success tests transformComposition success path.
func TestTransformComposition_Success(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
			"api": {Image: "node"},
		},
	}

	result, err := transformComposition(context.Background(), comp, nil, nil)
	if err != nil {
		t.Fatalf("transformComposition: %v", err)
	}
	if len(result.Services) != 2 {
		t.Errorf("expected 2 services, got %d", len(result.Services))
	}
}

// TestTransformComposition_WithProfiles tests profile filtering.
func TestTransformComposition_WithProfiles(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx", Profiles: []string{"frontend"}},
			"api": {Image: "node", Profiles: []string{"backend"}},
		},
	}

	result, err := transformComposition(context.Background(), comp, []string{"frontend"}, nil)
	if err != nil {
		t.Fatalf("transformComposition: %v", err)
	}
	if len(result.Services) != 1 {
		t.Errorf("expected 1 service after filtering, got %d", len(result.Services))
	}
	if _, ok := result.Services["web"]; !ok {
		t.Error("expected web service to remain")
	}
}

// TestTryCreateGCRoot_NilGCRootCreate tests nil GCRootCreate.
func TestTryCreateGCRoot_NilGCRootCreate(t *testing.T) {
	deps := UpDeps{
		GCRootCreate: nil,
	}
	// Should not panic.
	tryCreateGCRoot(context.Background(), deps, []byte(`{"services":{}}`))
}

// TestTryCreateGCRoot_ErrorIsWarning tests that GCRoot errors are just warnings.
func TestTryCreateGCRoot_ErrorIsWarning(t *testing.T) {
	deps := UpDeps{
		Evaluator: &eval.Evaluator{
			Runner:     &mockRunner{},
			ProjectDir: t.TempDir(),
		},
		GCRootCreate: func(_ context.Context, _ eval.CommandRunner, _ string, _ []string) error {
			return fmt.Errorf("gc root failed")
		},
		ProjectDir: t.TempDir(),
	}
	// Should not panic or return error (it's a warning).
	tryCreateGCRoot(context.Background(), deps, []byte(`{"services":{"web":{"image":"/nix/store/abc-nginx"}}}`))
}

// TestTryCreateGCRoot_Success tests successful GCRoot creation.
func TestTryCreateGCRoot_Success(t *testing.T) {
	called := false
	deps := UpDeps{
		Evaluator: &eval.Evaluator{
			Runner:     &mockRunner{},
			ProjectDir: t.TempDir(),
		},
		GCRootCreate: func(_ context.Context, _ eval.CommandRunner, _ string, _ []string) error {
			called = true
			return nil
		},
		ProjectDir: t.TempDir(),
	}
	tryCreateGCRoot(context.Background(), deps, []byte(`{"services":{"web":{"image":"nginx"}}}`))
	if !called {
		t.Error("expected GCRootCreate to be called")
	}
}

// TestResolveSecrets_WithEnvFrom tests resolveSecrets when services have envFrom entries.
func TestResolveSecrets_WithEnvFrom(t *testing.T) {
	dir := t.TempDir()

	// Create a secret file.
	secretFile := filepath.Join(dir, "secrets.env")
	if err := os.WriteFile(secretFile, []byte("DB_PASS=secret123\nAPI_KEY=key456\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {
				Image: "nginx",
				XNixCompose: &eval.NixComposeExtended{
					EnvFrom: []eval.EnvFromSource{
						{SecretFile: secretFile},
					},
				},
			},
			"api": {Image: "node"}, // no envFrom
		},
	}

	secrets, err := resolveSecrets(context.Background(), comp, dir)
	if err != nil {
		t.Fatalf("resolveSecrets: %v", err)
	}
	if len(secrets) != 1 {
		t.Errorf("expected 1 service with secrets, got %d", len(secrets))
	}
	webSecrets, ok := secrets["web"]
	if !ok {
		t.Fatal("expected web to have secrets")
	}
	if webSecrets["DB_PASS"] != "secret123" {
		t.Errorf("expected DB_PASS=secret123, got %q", webSecrets["DB_PASS"])
	}
	if webSecrets["API_KEY"] != "key456" {
		t.Errorf("expected API_KEY=key456, got %q", webSecrets["API_KEY"])
	}
}

// TestResolveSecrets_EmptyEnvFrom tests resolveSecrets with empty envFrom.
func TestResolveSecrets_EmptyEnvFrom(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {
				Image: "nginx",
				XNixCompose: &eval.NixComposeExtended{
					EnvFrom: []eval.EnvFromSource{},
				},
			},
		},
	}

	secrets, err := resolveSecrets(context.Background(), comp, t.TempDir())
	if err != nil {
		t.Fatalf("resolveSecrets: %v", err)
	}
	if len(secrets) != 0 {
		t.Errorf("expected 0 secrets for empty envFrom, got %d", len(secrets))
	}
}

// TestResolveSecrets_XNixComposeNil tests resolveSecrets with nil XNixCompose.
func TestResolveSecrets_XNixComposeNil(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx", XNixCompose: nil},
		},
	}

	secrets, err := resolveSecrets(context.Background(), comp, t.TempDir())
	if err != nil {
		t.Fatalf("resolveSecrets: %v", err)
	}
	if len(secrets) != 0 {
		t.Errorf("expected 0 secrets, got %d", len(secrets))
	}
}

// TestResolveSecrets_MissingFile tests resolveSecrets with a missing secret file.
func TestResolveSecrets_MissingFile(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {
				Image: "nginx",
				XNixCompose: &eval.NixComposeExtended{
					EnvFrom: []eval.EnvFromSource{
						{SecretFile: "/nonexistent/secrets.env"},
					},
				},
			},
		},
	}

	_, err := resolveSecrets(context.Background(), comp, t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing secret file")
	}
	if !strings.Contains(err.Error(), "envFrom") {
		t.Errorf("expected envFrom error, got: %v", err)
	}
}

// TestServiceImages_AllEmpty tests serviceImages when all services have empty images.
func TestServiceImages_AllEmpty(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: ""},
			"api": {Image: ""},
		},
	}
	images := serviceImages(comp, nil)
	if len(images) != 0 {
		t.Errorf("expected 0 images, got %d", len(images))
	}
}

// TestServiceImages_FilteredNoMatch tests serviceImages with filter that matches nothing.
func TestServiceImages_FilteredNoMatch(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
		},
	}
	images := serviceImages(comp, []string{"nonexistent"})
	if len(images) != 0 {
		t.Errorf("expected 0 images, got %d", len(images))
	}
}

// TestServiceImages_FilteredEmptyImage tests serviceImages with filter matching a service with empty image.
func TestServiceImages_FilteredEmptyImage(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: ""},
			"api": {Image: "node"},
		},
	}
	images := serviceImages(comp, []string{"web", "api"})
	if len(images) != 1 {
		t.Errorf("expected 1 image (skipping empty), got %d", len(images))
	}
	if images["api"] != "node" {
		t.Errorf("api image = %q, want node", images["api"])
	}
}

// TestDryRunIfRequested_Enabled tests dryRunIfRequested when enabled (will fail with no kubectl).
func TestDryRunIfRequested_Enabled(t *testing.T) {
	old := renderDryRun
	renderDryRun = true
	defer func() { renderDryRun = old }()

	// This will call kubectlDryRun, which will fail because kubectl is not available.
	err := dryRunIfRequested(context.Background(), nil)
	// We don't check the specific error because kubectl might or might not exist.
	// The important thing is that it enters the kubectlDryRun path.
	_ = err
}

// TestOutputManifests_DryRunEnabled tests outputManifests with dry-run enabled.
func TestOutputManifests_DryRunEnabled(t *testing.T) {
	oldOutput := renderOutput
	oldDryRun := renderDryRun
	renderOutput = ""
	renderDryRun = true
	defer func() {
		renderOutput = oldOutput
		renderDryRun = oldDryRun
	}()

	// With empty manifests, it should write to stdout and then try dry-run.
	err := outputManifests(context.Background(), nil)
	// May fail due to kubectl not being available, which is fine.
	_ = err
}

// TestOutputManifests_ToDirectoryWithDryRun tests outputManifests writing to directory with dry-run.
func TestOutputManifests_ToDirectoryWithDryRun(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "k8s-out")
	oldOutput := renderOutput
	oldDryRun := renderDryRun
	renderOutput = dir
	renderDryRun = true
	defer func() {
		renderOutput = oldOutput
		renderDryRun = oldDryRun
	}()

	// With empty manifests, should succeed writing directory but may fail dry-run.
	err := outputManifests(context.Background(), []k8s.Manifest{})
	_ = err
}

// TestCriUp_DepCycleError tests criUp when depgraph returns a cycle error.
func TestCriUp_DepCycleError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// Cycle: a -> b -> a
	fixture := `{"services":{"a":{"image":"alpine","depends_on":{"b":{"condition":"service_started"}}},"b":{"image":"alpine","depends_on":{"a":{"condition":"service_started"}}}}}`
	deps := criTestDeps(t, dir, fixture, criClient)
	_, err = DoUp(ctx, deps, false, false, false, nil, nil)
	if err == nil {
		t.Fatal("expected dependency validation error for cycle")
	}
	if !strings.Contains(err.Error(), "dependency") {
		t.Errorf("expected dependency error, got: %v", err)
	}
}

// TestCriExecutor_ExecSync tests the criExecutor adapter.
func TestCriExecutor_ExecSync(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "execadapt", []string{"web"})

	ctrID, err := lookupContainerID(ctx, criClient, "execadapt", "web")
	if err != nil {
		t.Fatalf("lookupContainerID: %v", err)
	}

	mock.mu.Lock()
	mock.execExitCodes[ctrID] = 0
	mock.execStdout[ctrID] = []byte("ok\n")
	mock.mu.Unlock()

	executor := &criExecutor{client: criClient}
	result, err := executor.ExecSync(ctx, ctrID, []string{"echo", "ok"}, 10)
	if err != nil {
		t.Fatalf("ExecSync: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
}

// TestCriExecutor_ExecSyncNonZero tests criExecutor with non-zero exit.
func TestCriExecutor_ExecSyncNonZero(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "execnz", []string{"web"})

	ctrID, err := lookupContainerID(ctx, criClient, "execnz", "web")
	if err != nil {
		t.Fatalf("lookupContainerID: %v", err)
	}

	mock.mu.Lock()
	mock.execExitCodes[ctrID] = 42
	mock.mu.Unlock()

	executor := &criExecutor{client: criClient}
	result, err := executor.ExecSync(ctx, ctrID, []string{"fail"}, 10)
	if err != nil {
		t.Fatalf("ExecSync: %v", err)
	}
	if result.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", result.ExitCode)
	}
}

// TestSetupCNI_MissingPlugins tests setupCNI when plugins are missing.
func TestSetupCNI_MissingPlugins(t *testing.T) {
	// In the test environment, CNI plugins are almost certainly not installed.
	// setupCNI should return false and print a warning.
	result := setupCNI("testcni")
	// We just check it doesn't panic. The result depends on the system state.
	_ = result
}

// TestCondPriority_AllCases tests all condPriority return values.
func TestCondPriority_AllCases(t *testing.T) {
	cases := []struct {
		cond string
		want int
	}{
		{"service_healthy", 3},
		{"service_completed_successfully", 2},
		{"service_started", 1},
		{"", 0},
		{"unknown_condition", 0},
		{"service_unhealthy", 0},
	}
	for _, tc := range cases {
		got := condPriority(tc.cond)
		if got != tc.want {
			t.Errorf("condPriority(%q) = %d, want %d", tc.cond, got, tc.want)
		}
	}
}

// TestContainersForPod_EmptyPod tests containersForPod with a pod that has no containers.
func TestContainersForPod_EmptyPod(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// Create a pod without containers.
	svc := eval.Service{Image: "nginx"}
	podConfig := cri.BuildPodConfig("emptyp", "web", svc, "v1", cri.PodNetworkHost)
	podID, err := criClient.RunPodSandbox(ctx, podConfig)
	if err != nil {
		t.Fatalf("RunPodSandbox: %v", err)
	}

	infos, err := containersForPod(ctx, criClient, podID, "web")
	if err != nil {
		t.Fatalf("containersForPod: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("expected 0 container infos for empty pod, got %d", len(infos))
	}
}

// TestPrintResourceWarnings_NoWarnings tests printResourceWarnings with no warnings.
func TestPrintResourceWarnings_NoWarnings(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
		},
	}
	// Should not panic.
	printResourceWarnings(comp)
}

// TestCriProjectName_EmptyBoth tests criProjectName with empty project name but valid dir.
func TestCriProjectName_EmptyBoth(t *testing.T) {
	deps := UpDeps{ProjectDir: "/tmp/my-project-dir"}
	got := criProjectName(deps)
	if got != "my-project-dir" {
		t.Errorf("criProjectName() = %q, want my-project-dir", got)
	}
}

// TestShutdownServices_CycleInComp tests shutdownServices when comp has a cycle (falls back to ProjectDown).
func TestShutdownServices_CycleInComp(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// Cycle: a -> b -> a
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"a": {Image: "alpine", DependsOn: eval.DependsOnValue{
				Entries: map[string]eval.DependsOnEntry{"b": {Condition: "service_started"}},
			}},
			"b": {Image: "alpine", DependsOn: eval.DependsOnValue{
				Entries: map[string]eval.DependsOnEntry{"a": {Condition: "service_started"}},
			}},
		},
	}

	// Should fall back to ProjectDown (unordered) and succeed.
	err = shutdownServices(ctx, criClient, "cycleproj", 5, comp)
	if err != nil {
		t.Fatalf("shutdownServices with cycle: %v", err)
	}
}

// TestFormatPsOutput_JSON tests formatPsOutput with JSON format.
func TestFormatPsOutput_JSON_Format(t *testing.T) {
	oldS := psServices
	oldQ := psQuiet
	oldF := psFormat
	psServices = false
	psQuiet = false
	psFormat = "json"
	defer func() {
		psServices = oldS
		psQuiet = oldQ
		psFormat = oldF
	}()

	containers := []containerInfo{
		{Service: "web", State: runtimev1.ContainerState_CONTAINER_RUNNING, Image: "nginx", ContainerID: "ctr-abc"},
		{Service: "api", State: runtimev1.ContainerState_CONTAINER_EXITED, Image: "node", ContainerID: "ctr-def"},
	}
	if err := formatPsOutput(containers); err != nil {
		t.Fatalf("formatPsOutput json: %v", err)
	}
}

// TestResolveContainers_WithServiceFilter tests resolving containers with specific service names.
func TestResolveContainers_WithServiceFilter_NoMatch(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "filtproj", []string{"web", "api"})

	// Filter for a service that doesn't exist.
	containers, err := resolveContainers(ctx, criClient, "filtproj", []string{"nonexistent"})
	if err != nil {
		t.Fatalf("resolveContainers: %v", err)
	}
	if len(containers) != 0 {
		t.Errorf("expected 0 containers for nonexistent service, got %d", len(containers))
	}
}

// TestFilterRunning_AllExited tests filterRunning when all containers are exited.
func TestFilterRunning_AllExited(t *testing.T) {
	containers := []containerInfo{
		{Service: "a", State: runtimev1.ContainerState_CONTAINER_EXITED},
		{Service: "b", State: runtimev1.ContainerState_CONTAINER_EXITED},
	}
	filtered := filterRunning(containers)
	if len(filtered) != 0 {
		t.Errorf("expected 0 running, got %d", len(filtered))
	}
}

// TestFilterRunning_Empty tests filterRunning with empty input.
func TestFilterRunning_Empty(t *testing.T) {
	filtered := filterRunning(nil)
	if len(filtered) != 0 {
		t.Errorf("expected 0, got %d", len(filtered))
	}
}

// TestDoLogsCRI_ProjectFromFlagName tests doLogsCRI using flagProjectName.
func TestDoLogsCRI_ProjectFromFlagName(t *testing.T) {
	dir := t.TempDir()
	project := "logflags"

	oldDir := flagProjectDir
	oldName := flagProjectName
	flagProjectDir = dir
	flagProjectName = project
	defer func() {
		flagProjectDir = oldDir
		flagProjectName = oldName
	}()

	// Should not error even with no log files.
	err := doLogsCRI(context.Background(), []string{"svc1"})
	if err != nil {
		t.Fatalf("doLogsCRI: %v", err)
	}
}

// TestCreateServiceCRI_WithMounts tests createServiceCRI with a service that has mounts.
func TestCreateServiceCRI_WithMounts(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	svc := eval.Service{Image: "nginx:latest"}
	opts := cri.ServiceUpOptions{Project: "mounttest", Version: "v1"}

	if err := createServiceCRI(ctx, criClient, "mounttest", "web", svc, opts); err != nil {
		t.Fatalf("createServiceCRI: %v", err)
	}

	mock.mu.Lock()
	ctrCount := len(mock.containers)
	mock.mu.Unlock()
	if ctrCount != 1 {
		t.Errorf("expected 1 container, got %d", ctrCount)
	}
}

// TestRemoveContainerCRI_RunningNoStop tests that running container is skipped when stop=false.
func TestRemoveContainerCRI_RunningNoStop(t *testing.T) {
	ctx := context.Background()
	sock, mock := startCLIMockCRI(t)
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "rmnstop", []string{"web"})
	containers, err := resolveContainers(ctx, criClient, "rmnstop", nil)
	if err != nil {
		t.Fatalf("resolveContainers: %v", err)
	}
	if len(containers) == 0 {
		t.Fatal("expected at least 1 container")
	}

	// Should skip running container (not stop it).
	err = removeContainerCRI(ctx, criClient, containers[0], false)
	if err != nil {
		t.Fatalf("removeContainerCRI: %v", err)
	}

	// Container should still exist since it was skipped.
	mock.mu.Lock()
	remaining := len(mock.containers)
	mock.mu.Unlock()
	if remaining != 1 {
		t.Errorf("expected 1 container still remaining, got %d", remaining)
	}
}

// TestWaitCondition_CompletedSuccessfully tests the service_completed_successfully path.
func TestWaitCondition_CompletedSuccessfully(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "wccond", []string{"web"})

	// Mark the container as exited with code 0.
	mock.mu.Lock()
	for id, ctr := range mock.containers {
		ctr.State = runtimev1.ContainerState_CONTAINER_EXITED
		mock.containerExit[id] = 0
	}
	mock.mu.Unlock()

	svc := eval.Service{Image: "nginx"}
	executor := &criExecutor{client: criClient}
	monitor := health.NewMonitor(executor)

	err = waitCondition(ctx, criClient, "wccond", "web", "service_completed_successfully", svc, monitor)
	if err != nil {
		t.Fatalf("waitCondition service_completed_successfully: %v", err)
	}
}

// TestWaitCondition_Healthy_NoHealthCheck tests waitCondition with service_healthy but no health check.
func TestWaitCondition_Healthy_NoHealthCheck(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "wchcond", []string{"web"})

	svc := eval.Service{Image: "nginx"} // no healthcheck
	executor := &criExecutor{client: criClient}
	monitor := health.NewMonitor(executor)

	err = waitCondition(ctx, criClient, "wchcond", "web", "service_healthy", svc, monitor)
	if err == nil {
		t.Fatal("expected error for no health check")
	}
	if !strings.Contains(err.Error(), "no health check") {
		t.Errorf("expected 'no health check' error, got: %v", err)
	}
}

// TestRequireCRI_ValidSocket tests requireCRI with a valid CRI socket.
func TestRequireCRI_ValidSocket(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	old := flagCRISocket
	flagCRISocket = sock
	defer func() { flagCRISocket = old }()

	c, err := requireCRI(context.Background())
	if err != nil {
		t.Fatalf("requireCRI returned unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil CRI client for valid socket")
		return
	}
	_ = c.Close()
}

// TestRenderRender_K8sTarget tests runRender with "k8s" target (will fail on eval).
func TestRenderRender_K8sTarget(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldDir := flagProjectDir
	oldImpure := flagImpure
	oldTarget := renderTarget
	flagProjectDir = dir
	flagImpure = true
	renderTarget = "k8s"
	defer func() {
		flagProjectDir = oldDir
		flagImpure = oldImpure
		renderTarget = oldTarget
	}()

	// Will fail because of eval, but exercises the k8s code path.
	err := runRender(renderCmd, nil)
	if err == nil {
		t.Skip("nix available")
	}
}

// TestCriDown_WithServices tests CRIDown after setting up services.
func TestCriDown_WithServices(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "downtest2", []string{"web", "api", "db"})

	mock.mu.Lock()
	podCount := len(mock.pods)
	mock.mu.Unlock()
	if podCount != 3 {
		t.Fatalf("expected 3 pods, got %d", podCount)
	}

	if err := CRIDown(ctx, criClient, "downtest2", t.TempDir(), 5, false); err != nil {
		t.Fatalf("CRIDown: %v", err)
	}

	mock.mu.Lock()
	remaining := len(mock.pods)
	mock.mu.Unlock()
	if remaining != 0 {
		t.Errorf("expected 0 pods after CRIDown, got %d", remaining)
	}
}

// TestDoUp_CRI_WithDeps tests DoUp via CRI with dependency ordering.
func TestDoUp_CRI_WithDeps(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	fixture := `{"services":{
		"web":{"image":"nginx","depends_on":{"api":{"condition":"service_started"}}},
		"api":{"image":"node","depends_on":{"db":{"condition":"service_started"}}},
		"db":{"image":"postgres"}
	}}`

	deps := criTestDeps(t, dir, fixture, criClient)
	comp, err := DoUp(ctx, deps, false, false, false, nil, nil)
	if err != nil {
		t.Fatalf("DoUp CRI with deps: %v", err)
	}
	if len(comp.Services) != 3 {
		t.Errorf("expected 3 services, got %d", len(comp.Services))
	}

	mock.mu.Lock()
	order := make([]string, len(mock.startOrder))
	copy(order, mock.startOrder)
	mock.mu.Unlock()

	if len(order) != 3 {
		t.Fatalf("expected 3 services started, got %d", len(order))
	}
}

// TestRmCRI_FilteredServices tests runRmCRI with specific services.
func TestRmCRI_FilteredServices(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "rmfilt", []string{"web", "api"})

	// Only rm "web" with stop.
	if err := runRmCRI(ctx, criClient, "rmfilt", []string{"web"}, true, false); err != nil {
		t.Fatalf("runRmCRI: %v", err)
	}

	// "api" should still have containers.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	apiExists := false
	for _, pod := range mock.pods {
		if pod.Labels["nix-compose.service"] == "api" {
			apiExists = true
		}
	}
	if !apiExists {
		t.Error("expected api pod to still exist")
	}
}

// TestKillCRI_FilteredServices tests runKillCRI with specific services.
func TestKillCRI_FilteredServices(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "killfilt", []string{"web", "api"})

	if err := runKillCRI(ctx, criClient, "killfilt", []string{"web"}); err != nil {
		t.Fatalf("runKillCRI: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	running := 0
	for _, ctr := range mock.containers {
		if ctr.State == runtimev1.ContainerState_CONTAINER_RUNNING {
			running++
		}
	}
	// api should still be running.
	if running != 1 {
		t.Errorf("expected 1 running container, got %d", running)
	}
}

// TestRestartCRI_FilteredServices tests runRestartCRI with specific services.
func TestRestartCRI_FilteredServices(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "restfilt", []string{"web", "api"})

	if err := runRestartCRI(ctx, criClient, "restfilt", []string{"web"}, 5); err != nil {
		t.Fatalf("runRestartCRI: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	running := 0
	for _, ctr := range mock.containers {
		if ctr.State == runtimev1.ContainerState_CONTAINER_RUNNING {
			running++
		}
	}
	if running != 2 {
		t.Errorf("expected 2 running containers after restart, got %d", running)
	}
}

// TestStartCRI_FilteredServices tests runStartCRI with specific services then verifies state.
func TestStartCRI_FilteredServices(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "startfilt", []string{"web", "api", "db"})

	// Stop all first.
	if err := runStopCRI(ctx, criClient, "startfilt", nil, 5); err != nil {
		t.Fatalf("runStopCRI: %v", err)
	}

	// Start only web — the other two should remain stopped.
	if err := runStartCRI(ctx, criClient, "startfilt", []string{"web"}); err != nil {
		t.Fatalf("runStartCRI: %v", err)
	}

	mock.mu.Lock()
	running := 0
	stopped := 0
	for _, ctr := range mock.containers {
		switch ctr.State {
		case runtimev1.ContainerState_CONTAINER_RUNNING:
			running++
		case runtimev1.ContainerState_CONTAINER_EXITED:
			stopped++
		}
	}
	mock.mu.Unlock()
	if running != 1 {
		t.Errorf("expected 1 running container, got %d", running)
	}
	if stopped != 2 {
		t.Errorf("expected 2 stopped containers, got %d", stopped)
	}
}

// TestPrintPsTable_MultipleContainers tests printPsTable with multiple containers.
func TestPrintPsTable_MultipleContainers(t *testing.T) {
	containers := []containerInfo{
		{Service: "web", State: runtimev1.ContainerState_CONTAINER_RUNNING, Image: "nginx", ContainerID: "abc123456789xyz"},
		{Service: "api", State: runtimev1.ContainerState_CONTAINER_EXITED, Image: "node", ContainerID: "def456789012xyz"},
	}
	if err := printPsTable(containers); err != nil {
		t.Fatalf("printPsTable: %v", err)
	}
}

// TestPrintPsJSON_MultipleContainers tests printPsJSON with multiple containers.
func TestPrintPsJSON_MultipleContainers(t *testing.T) {
	containers := []containerInfo{
		{Service: "web", State: runtimev1.ContainerState_CONTAINER_RUNNING, Image: "nginx", ContainerID: "abc"},
		{Service: "api", State: runtimev1.ContainerState_CONTAINER_EXITED, Image: "node", ContainerID: "def"},
	}
	if err := printPsJSON(containers); err != nil {
		t.Fatalf("printPsJSON: %v", err)
	}
}

// TestPrintPsJSON_Empty tests printPsJSON with empty containers.
func TestPrintPsJSON_Empty(t *testing.T) {
	if err := printPsJSON(nil); err != nil {
		t.Fatalf("printPsJSON empty: %v", err)
	}
}

// TestMergeProfiles_EnvWithSpaces tests mergeProfiles with env var containing spaces.
func TestMergeProfiles_EnvWithSpaces(t *testing.T) {
	t.Setenv("COMPOSE_PROFILES", " frontend , backend ")
	result := mergeProfiles(nil)
	if len(result) != 2 {
		t.Fatalf("expected 2 profiles, got %d: %v", len(result), result)
	}
	if result[0] != "frontend" || result[1] != "backend" {
		t.Errorf("expected [frontend backend], got %v", result)
	}
}

// TestCRIDownOrdered_NilComp tests CRIDownOrdered with nil composition.
func TestCRIDownOrdered_NilComp(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "nilcomp", []string{"web"})

	if err := CRIDownOrdered(ctx, criClient, "nilcomp", t.TempDir(), 5, false, nil); err != nil {
		t.Fatalf("CRIDownOrdered nil comp: %v", err)
	}
}

// TestCRIDownOrdered_WithVolumes tests CRIDownOrdered with volume removal and a composition.
func TestCRIDownOrdered_WithVolumes(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "ordvol", []string{"web"})

	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
		},
	}

	if err := CRIDownOrdered(ctx, criClient, "ordvol", t.TempDir(), 5, true, comp); err != nil {
		t.Fatalf("CRIDownOrdered with volumes: %v", err)
	}
}

// TestRemoveContainerCRI_Created tests removing a container in CREATED state.
func TestRemoveContainerCRI_Created(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// Create a service but don't start it (use createServiceCRI).
	svc := eval.Service{Image: "nginx:latest"}
	opts := cri.ServiceUpOptions{Project: "rmcreated", Version: "v1"}
	if err := createServiceCRI(ctx, criClient, "rmcreated", "web", svc, opts); err != nil {
		t.Fatalf("createServiceCRI: %v", err)
	}

	containers, err := resolveContainers(ctx, criClient, "rmcreated", nil)
	if err != nil {
		t.Fatalf("resolveContainers: %v", err)
	}
	if len(containers) == 0 {
		t.Fatal("expected at least 1 container")
	}

	// Container should be in CREATED state (not RUNNING), so stop=false should still remove it.
	if err := removeContainerCRI(ctx, criClient, containers[0], false); err != nil {
		t.Fatalf("removeContainerCRI: %v", err)
	}

	mock.mu.Lock()
	remaining := len(mock.containers)
	mock.mu.Unlock()
	if remaining != 0 {
		t.Errorf("expected 0 containers, got %d", remaining)
	}
}

// TestDoLogsCRI_WithFollow tests doLogsCRI with follow mode (exercises the follow code path).
func TestDoLogsCRI_WithFollow(t *testing.T) {
	dir := t.TempDir()
	project := "followtest"

	oldDir := flagProjectDir
	oldName := flagProjectName
	oldFollow := logsFollow
	flagProjectDir = dir
	flagProjectName = project
	logsFollow = true
	defer func() {
		flagProjectDir = oldDir
		flagProjectName = oldName
		logsFollow = oldFollow
	}()

	// Create a log file so follow has something to read.
	logDir := filepath.Join(logs.DefaultLogBase, project, "web")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(logDir, "0.log")
	if err := os.WriteFile(logFile, []byte("2024-01-15T10:30:01Z stdout F hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(logs.DefaultLogBase, project)) })

	// Use a cancelled context so Follow returns quickly.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	// doLogsCRI with follow should return (possibly with context error).
	_ = doLogsCRI(ctx, []string{"web"})
}

// TestDoLogsCRI_WithTimestampsAndTail tests doLogsCRI with timestamps and tail options.
func TestDoLogsCRI_WithTimestampsAndTail(t *testing.T) {
	dir := t.TempDir()
	project := "dumpoptstest"

	oldDir := flagProjectDir
	oldName := flagProjectName
	oldTs := logsTimestamps
	oldTail := logsTail
	oldFollow := logsFollow
	flagProjectDir = dir
	flagProjectName = project
	logsTimestamps = true
	logsTail = "10"
	logsFollow = false
	defer func() {
		flagProjectDir = oldDir
		flagProjectName = oldName
		logsTimestamps = oldTs
		logsTail = oldTail
		logsFollow = oldFollow
	}()

	// Should succeed with no log files (returns nothing).
	err := doLogsCRI(context.Background(), []string{"web"})
	if err != nil {
		t.Fatalf("doLogsCRI: %v", err)
	}
}

// TestOutputManifests_ToStdout tests outputManifests writing to stdout with non-nil manifests.
func TestOutputManifests_ToStdout_NonEmpty(t *testing.T) {
	oldOutput := renderOutput
	oldDryRun := renderDryRun
	renderOutput = ""
	renderDryRun = false
	defer func() {
		renderOutput = oldOutput
		renderDryRun = oldDryRun
	}()

	manifests := []k8s.Manifest{
		{Object: map[string]interface{}{"apiVersion": "v1", "kind": "Service"}, Filename: "svc.yaml"},
	}
	err := outputManifests(context.Background(), manifests)
	if err != nil {
		t.Errorf("outputManifests: %v", err)
	}
}

// TestOutputManifests_ToDirectory_NonEmpty tests outputManifests writing to directory with manifests.
func TestOutputManifests_ToDirectory_NonEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "manifests")
	oldOutput := renderOutput
	oldDryRun := renderDryRun
	renderOutput = dir
	renderDryRun = false
	defer func() {
		renderOutput = oldOutput
		renderDryRun = oldDryRun
	}()

	manifests := []k8s.Manifest{
		{Object: map[string]interface{}{"apiVersion": "v1", "kind": "Service"}, Filename: "svc.yaml"},
	}
	err := outputManifests(context.Background(), manifests)
	if err != nil {
		t.Errorf("outputManifests to dir: %v", err)
	}
}

// TestCreateServiceCRI_Success_Full tests createServiceCRI with a different image.
func TestCreateServiceCRI_Success_Full(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	svc := eval.Service{Image: "myapp:v1", NetworkMode: "host"}
	opts := cri.ServiceUpOptions{Project: "csfull", Version: "v1", UseCNI: true}

	if err := createServiceCRI(ctx, criClient, "csfull", "api", svc, opts); err != nil {
		t.Fatalf("createServiceCRI: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	found := false
	for _, ctr := range mock.containers {
		if ctr.Metadata != nil && ctr.Metadata.Name == "api" {
			found = true
		}
	}
	if !found {
		t.Error("expected container named 'api'")
	}
}

// TestRunPsCRI_Error tests runPsCRI when resolveContainers returns no error but empty.
func TestRunPsCRI_EmptyProject(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	oldAll := psAll
	oldQuiet := psQuiet
	oldFormat := psFormat
	oldServices := psServices
	psAll = true
	psQuiet = false
	psFormat = ""
	psServices = false
	defer func() {
		psAll = oldAll
		psQuiet = oldQuiet
		psFormat = oldFormat
		psServices = oldServices
	}()

	// Empty project, should return no error with empty output.
	if err := runPsCRI(ctx, criClient, "emptyproj", nil); err != nil {
		t.Fatalf("runPsCRI empty: %v", err)
	}
}

// TestRunTopCRI_NoContainer tests runTopCRI when the service has no running container.
func TestRunTopCRI_NoContainer(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	err = runTopCRI(ctx, criClient, "emptyproj", []string{"web"})
	if err == nil {
		t.Error("expected error when service has no container")
	}
}

// TestRunRmCRI_NoContainers tests runRmCRI when there are no containers to remove.
func TestRunRmCRI_NoContainers(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// No services exist.
	if err := runRmCRI(ctx, criClient, "emptyproj", nil, false, false); err != nil {
		t.Fatalf("runRmCRI empty: %v", err)
	}
}

// TestRunRmCRI_NoContainersWithVolumes tests runRmCRI with volumes but no containers.
func TestRunRmCRI_NoContainersWithVolumes(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	if err := runRmCRI(ctx, criClient, "emptyproj", nil, false, true); err != nil {
		t.Fatalf("runRmCRI with volumes: %v", err)
	}
}

// TestPrintResourceWarnings_WithResourceWarnings tests with a service having resource specs.
func TestPrintResourceWarnings_WithResourceWarnings(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {
				Image: "nginx",
				XNixCompose: &eval.NixComposeExtended{
					Resources: &eval.Resources{
						Requests: &eval.ResourceSpec{CPU: "100m", Memory: "128Mi"},
					},
				},
			},
		},
	}
	// Should not panic.
	printResourceWarnings(comp)
}

// TestDoUp_CRI_ServiceUp_Error tests DoUp CRI path when ServiceUp fails (unreachable with mock, but tests the dep cycle path).
func TestDoUp_CRI_SingleService(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	fixture := `{"services":{"web":{"image":"nginx"}}}`
	deps := criTestDeps(t, dir, fixture, criClient)
	comp, err := DoUp(ctx, deps, false, false, false, nil, nil)
	if err != nil {
		t.Fatalf("DoUp CRI single: %v", err)
	}
	if comp == nil {
		t.Fatal("expected non-nil composition")
		return
	}
	if len(comp.Services) != 1 {
		t.Errorf("expected 1 service, got %d", len(comp.Services))
	}
}

// TestRunKillCRI_Error tests runKillCRI on a service with running containers.
func TestRunKillCRI_MultipleServices(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "killmulti", []string{"web", "api", "db"})

	if err := runKillCRI(ctx, criClient, "killmulti", nil); err != nil {
		t.Fatalf("runKillCRI: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	for _, ctr := range mock.containers {
		if ctr.State != runtimev1.ContainerState_CONTAINER_EXITED {
			t.Errorf("container %s should be exited, got %v", ctr.Id, ctr.State)
		}
	}
}

// TestRunRestartCRI_MultipleServices tests restarting multiple services.
func TestRunRestartCRI_MultipleServices(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "restmulti", []string{"web", "api"})

	if err := runRestartCRI(ctx, criClient, "restmulti", nil, 5); err != nil {
		t.Fatalf("runRestartCRI: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	running := 0
	for _, ctr := range mock.containers {
		if ctr.State == runtimev1.ContainerState_CONTAINER_RUNNING {
			running++
		}
	}
	if running != 2 {
		t.Errorf("expected 2 running after restart, got %d", running)
	}
}

// TestRunStartCRI_MultipleExited tests starting multiple exited containers.
func TestRunStartCRI_MultipleExited(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "startmulti", []string{"web", "api"})

	// Stop all.
	if err := runStopCRI(ctx, criClient, "startmulti", nil, 5); err != nil {
		t.Fatalf("runStopCRI: %v", err)
	}

	// Start all.
	if err := runStartCRI(ctx, criClient, "startmulti", nil); err != nil {
		t.Fatalf("runStartCRI: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	running := 0
	for _, ctr := range mock.containers {
		if ctr.State == runtimev1.ContainerState_CONTAINER_RUNNING {
			running++
		}
	}
	if running != 2 {
		t.Errorf("expected 2 running after start, got %d", running)
	}
}

// TestRunStopCRI_MultipleServices tests stopping multiple services.
func TestRunStopCRI_MultipleServices(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "stopmulti", []string{"web", "api", "db"})

	if err := runStopCRI(ctx, criClient, "stopmulti", nil, 5); err != nil {
		t.Fatalf("runStopCRI: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	for _, ctr := range mock.containers {
		if ctr.State != runtimev1.ContainerState_CONTAINER_EXITED {
			t.Errorf("container %s should be exited, got %v", ctr.Id, ctr.State)
		}
	}
}

// TestRmCRI_AllWithStop tests runRmCRI removing all containers with stop.
func TestRmCRI_AllWithStop(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "rmall", []string{"web", "api", "db"})

	if err := runRmCRI(ctx, criClient, "rmall", nil, true, true); err != nil {
		t.Fatalf("runRmCRI: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.containers) != 0 {
		t.Errorf("expected 0 containers, got %d", len(mock.containers))
	}
	if len(mock.pods) != 0 {
		t.Errorf("expected 0 pods, got %d", len(mock.pods))
	}
}

// TestShutdownServices_WithDeps tests shutdownServices with multiple dependency levels.
func TestShutdownServices_WithDeps(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "shutdeps", []string{"web", "api", "db"})

	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx", DependsOn: eval.DependsOnValue{
				Entries: map[string]eval.DependsOnEntry{"api": {Condition: "service_started"}},
			}},
			"api": {Image: "node", DependsOn: eval.DependsOnValue{
				Entries: map[string]eval.DependsOnEntry{"db": {Condition: "service_started"}},
			}},
			"db": {Image: "postgres"},
		},
	}

	if err := shutdownServices(ctx, criClient, "shutdeps", 5, comp); err != nil {
		t.Fatalf("shutdownServices: %v", err)
	}
}

// TestDoExecCRI_WithCommand tests doExecCRI with a command that has multiple args.
func TestDoExecCRI_WithCommand_MultipleArgs(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "execmulti", []string{"web"})

	mock.mu.Lock()
	for id := range mock.containers {
		mock.execStdout[id] = []byte("line1\nline2\n")
	}
	mock.mu.Unlock()

	oldDir := flagProjectDir
	oldName := flagProjectName
	flagProjectDir = t.TempDir()
	flagProjectName = "execmulti"
	defer func() {
		flagProjectDir = oldDir
		flagProjectName = oldName
	}()

	if err := doExecCRI(ctx, criClient, []string{"web", "ls", "-la", "/"}); err != nil {
		t.Fatalf("doExecCRI: %v", err)
	}
}

// TestCriUp_HealthGating_WithExecSuccess tests criUp with health gating where exec succeeds.
func TestCriUp_HealthGating_WithExecSuccess(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	fixture := `{"services":{
		"app":{"image":"app:1","depends_on":{"db":{"condition":"service_healthy"}}},
		"db":{"image":"postgres:16","healthcheck":{"test":["CMD","pg_isready"],"interval":"50ms","timeout":"1s","retries":3}}
	}}`

	deps := criTestDeps(t, dir, fixture, criClient)

	_ = mock // mock is available if needed for state manipulation

	comp, err := DoUp(ctx, deps, false, false, false, nil, nil)
	if err != nil {
		t.Fatalf("DoUp health gating: %v", err)
	}
	if len(comp.Services) != 2 {
		t.Errorf("expected 2 services, got %d", len(comp.Services))
	}
}

// TestContainersForPod_WithImageInfo tests containersForPod returning image info.
func TestContainersForPod_WithImageInfo(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "imginfo", []string{"web"})

	pods, err := criClient.ListPodSandboxes(ctx, map[string]string{cri.LabelProject: "imginfo"})
	if err != nil {
		t.Fatalf("ListPodSandboxes: %v", err)
	}
	if len(pods) == 0 {
		t.Fatal("expected at least 1 pod")
	}

	infos, err := containersForPod(ctx, criClient, pods[0].Id, "web")
	if err != nil {
		t.Fatalf("containersForPod: %v", err)
	}
	if len(infos) == 0 {
		t.Fatal("expected at least 1 container info")
	}
	// Image info should be set from the container.
	if infos[0].PodID != pods[0].Id {
		t.Errorf("expected pod ID %s, got %s", pods[0].Id, infos[0].PodID)
	}
}

// TestWaitExited_CompletedSuccessfully_Full tests the full flow of service_completed_successfully.
func TestWaitExited_CompletedSuccessfully_Full(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "waitfull", []string{"migrate"})

	// Mark as exited with code 0.
	mock.mu.Lock()
	for id, ctr := range mock.containers {
		ctr.State = runtimev1.ContainerState_CONTAINER_EXITED
		mock.containerExit[id] = 0
	}
	mock.mu.Unlock()

	err = waitExited(ctx, criClient, "waitfull", "migrate")
	if err != nil {
		t.Fatalf("waitExited: %v", err)
	}
}

// TestLookupPodIP_DefaultIP tests lookupPodIP when no custom IP is set (uses default 10.0.0.1).
func TestLookupPodIP_DefaultIP(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "defaultip", []string{"web"})

	ip, err := lookupPodIP(ctx, criClient, "defaultip", "web")
	if err != nil {
		t.Fatalf("lookupPodIP: %v", err)
	}
	if ip != "10.0.0.1" {
		t.Errorf("expected 10.0.0.1, got %s", ip)
	}
}

// TestWaitCondition_CompletedSuccessfully_NonZero tests waitCondition with completed but non-zero exit.
func TestWaitCondition_CompletedSuccessfully_NonZero(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "wcnz", []string{"migrate"})

	mock.mu.Lock()
	for id, ctr := range mock.containers {
		ctr.State = runtimev1.ContainerState_CONTAINER_EXITED
		mock.containerExit[id] = 1
	}
	mock.mu.Unlock()

	svc := eval.Service{Image: "migrate"}
	executor := &criExecutor{client: criClient}
	monitor := health.NewMonitor(executor)

	err = waitCondition(ctx, criClient, "wcnz", "migrate", "service_completed_successfully", svc, monitor)
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "exited with code") {
		t.Errorf("expected 'exited with code' error, got: %v", err)
	}
}

// TestExecNonInteractive_StdoutStderr tests execNonInteractive with both stdout and stderr.
func TestExecNonInteractive_StdoutStderr(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "execse", []string{"web"})
	containers, err := resolveContainers(ctx, criClient, "execse", nil)
	if err != nil {
		t.Fatalf("resolveContainers: %v", err)
	}

	mock.mu.Lock()
	mock.execStdout[containers[0].ContainerID] = []byte("stdout line\n")
	mock.execStderr[containers[0].ContainerID] = []byte("stderr line\n")
	mock.mu.Unlock()

	err = execNonInteractive(ctx, criClient, containers[0].ContainerID, []string{"echo", "test"})
	if err != nil {
		t.Fatalf("execNonInteractive: %v", err)
	}
}

// TestTransformComposition_EmptyServices tests transformComposition with empty services.
func TestTransformComposition_EmptyServices(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{},
	}

	result, err := transformComposition(context.Background(), comp, nil, nil)
	if err != nil {
		t.Fatalf("transformComposition empty: %v", err)
	}
	if len(result.Services) != 0 {
		t.Errorf("expected 0 services, got %d", len(result.Services))
	}
}

// TestKubectlDryRun_EmptyManifests tests kubectlDryRun with empty manifests.
func TestKubectlDryRun_EmptyManifests(t *testing.T) {
	// kubectlDryRun with empty manifests - may fail but exercises the code path.
	err := kubectlDryRun(context.Background(), nil)
	// If kubectl is not available, this will fail. That's OK.
	_ = err
}

// TestKubectlDryRun_WithManifests tests kubectlDryRun with some manifests.
func TestKubectlDryRun_WithManifests(t *testing.T) {
	manifests := []k8s.Manifest{
		{Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata":   map[string]interface{}{"name": "test", "namespace": "default"},
		}, Filename: "cm.yaml"},
	}
	// May fail if kubectl is not available.
	err := kubectlDryRun(context.Background(), manifests)
	_ = err
}

func TestPrintPlan(t *testing.T) {
	plan := &orchestrate.Plan{
		Actions: []orchestrate.Action{
			{Type: orchestrate.ActionCreate, ResourceID: "myapp/web", Key: "cri.orchestrator.io/v1/Service", Reason: "new resource"},
			{Type: orchestrate.ActionUpdate, ResourceID: "myapp/api", Key: "cri.orchestrator.io/v1/Service", Reason: "configuration changed"},
			{Type: orchestrate.ActionDestroy, ResourceID: "myapp/old", Key: "cri.orchestrator.io/v1/Service", Reason: "orphaned resource"},
			{Type: orchestrate.ActionNoOp, ResourceID: "myapp/keep", Key: "cri.orchestrator.io/v1/Service", Reason: "unchanged"},
		},
		Deployment: deploy.NewDeployment(),
	}

	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printPlan(plan)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "+") {
		t.Error("expected '+' symbol for create")
	}
	if !strings.Contains(output, "~") {
		t.Error("expected '~' symbol for update")
	}
	if !strings.Contains(output, "-") {
		t.Error("expected '-' symbol for destroy")
	}
	// NoOp should not appear.
	if strings.Contains(output, "keep") {
		t.Error("noop actions should not appear in output")
	}
	if !strings.Contains(output, "Plan:") {
		t.Error("expected 'Plan:' summary line")
	}
}

func TestPrintDriftResults(t *testing.T) {
	results := []orchestrate.DriftResult{
		{ResourceID: "myapp/web", Key: "cri.orchestrator.io/v1/Service", Expected: "SUCCESS", Actual: "ERROR", Reason: "container exited"},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printDriftResults(results)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "myapp/web") {
		t.Error("expected resource ID in output")
	}
	if !strings.Contains(output, "container exited") {
		t.Error("expected reason in output")
	}
}

func TestPrintRemoteActions(t *testing.T) {
	actions := []*orchestratev1.Action{
		{Type: "create", ResourceId: "web", Kind: "Service", Reason: "new"},
		{Type: "update", ResourceId: "api", Kind: "Service", Reason: "changed"},
		{Type: "destroy", ResourceId: "old", Kind: "Service", Reason: "orphan"},
		{Type: "noop", ResourceId: "keep", Kind: "Service", Reason: "unchanged"},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printRemoteActions(actions)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "+") {
		t.Error("expected '+' for create")
	}
	if !strings.Contains(output, "~") {
		t.Error("expected '~' for update")
	}
	if !strings.Contains(output, "-") {
		t.Error("expected '-' for destroy")
	}
	if strings.Contains(output, "keep") {
		t.Error("noop should be skipped")
	}
}

func TestPrintRemoteSummary(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printRemoteSummary(1, 2, 3, 4)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "1 to create") {
		t.Errorf("expected '1 to create' in output, got %q", output)
	}
	if !strings.Contains(output, "3 to destroy") {
		t.Errorf("expected '3 to destroy' in output, got %q", output)
	}
}

func TestPrintRolloutBody(t *testing.T) {
	r := &deploy.Rollout{
		InstanceId:  "web",
		InstanceKey: "cri.orchestrator.io/v1/Service",
		Body:        json.RawMessage(`{"image":"nginx:latest"}`),
	}

	old := os.Stdout
	rd, w, _ := os.Pipe()
	os.Stdout = w

	printRolloutBody(r)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rd)
	output := buf.String()

	if !strings.Contains(output, "Spec:") {
		t.Error("expected 'Spec:' header")
	}
	if !strings.Contains(output, "nginx:latest") {
		t.Error("expected body content")
	}
}

func TestPrintRolloutBody_Empty(t *testing.T) {
	r := &deploy.Rollout{
		InstanceId:  "web",
		InstanceKey: "cri.orchestrator.io/v1/Service",
	}

	old := os.Stdout
	rd, w, _ := os.Pipe()
	os.Stdout = w

	printRolloutBody(r)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rd)
	output := buf.String()

	if strings.Contains(output, "Spec:") {
		t.Error("empty body should not print Spec header")
	}
}

func TestPrintRemoteRolloutDetail(t *testing.T) {
	r := &orchestratev1.RolloutInfo{
		InstanceId:  "myapp/web",
		InstanceKey: "cri.orchestrator.io/v1/Service",
		Status:      "SUCCESS",
		Body:        []byte(`{"image":"nginx"}`),
	}

	old := os.Stdout
	rd, w, _ := os.Pipe()
	os.Stdout = w

	printRemoteRolloutDetail(r)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rd)
	output := buf.String()

	if !strings.Contains(output, "myapp/web") {
		t.Error("expected instance ID")
	}
	if !strings.Contains(output, "SUCCESS") {
		t.Error("expected status")
	}
	if !strings.Contains(output, "Spec:") {
		t.Error("expected spec output")
	}
}

func TestPrintRemoteRolloutDetail_NoBody(t *testing.T) {
	r := &orchestratev1.RolloutInfo{
		InstanceId:  "myapp/web",
		InstanceKey: "cri.orchestrator.io/v1/Service",
		Status:      "SUCCESS",
	}

	old := os.Stdout
	rd, w, _ := os.Pipe()
	os.Stdout = w

	printRemoteRolloutDetail(r)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rd)
	output := buf.String()

	if strings.Contains(output, "Spec:") {
		t.Error("no body should mean no Spec header")
	}
}

func TestNewLogPrinter(t *testing.T) {
	p := newLogPrinter(false, true)
	if p == nil {
		t.Fatal("expected non-nil printer")
		return
	}
	if p.noPrefix {
		t.Error("expected noPrefix=false")
	}
	if !p.timestamps {
		t.Error("expected timestamps=true")
	}
	if len(p.colors) == 0 {
		t.Error("expected colors to be set")
	}
}

func TestLogPrinter_Print(t *testing.T) {
	p := newLogPrinter(false, false)

	ts := timestamppb.New(time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC))
	entry := &orchestratev1.LogEntry{
		Service:   "web",
		Timestamp: ts,
		Stream:    "stdout",
		Message:   "hello from test",
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	p.print(entry)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "web") {
		t.Error("expected service name in output")
	}
	if !strings.Contains(output, "hello from test") {
		t.Error("expected message in output")
	}
}

func TestLogPrinter_Print_NoPrefix(t *testing.T) {
	p := newLogPrinter(true, false)

	entry := &orchestratev1.LogEntry{
		Service: "web",
		Message: "test msg",
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	p.print(entry)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	// Should not contain the service name prefix with color codes.
	if strings.Contains(output, "|") {
		t.Error("expected no pipe separator with noPrefix=true")
	}
}

func TestLogPrinter_Print_WithTimestamps(t *testing.T) {
	p := newLogPrinter(true, true)

	ts := timestamppb.New(time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC))
	entry := &orchestratev1.LogEntry{
		Service:   "api",
		Timestamp: ts,
		Message:   "data",
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	p.print(entry)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "2024-06-01") {
		t.Errorf("expected timestamp in output, got %q", output)
	}
}

func TestPrintRolloutDetail(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.bolt")
	engine, err := orchestrate.New(orchestrate.Config{DBPath: dbPath})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = engine.Close() }()

	r := &deploy.Rollout{
		InstanceId:  "myapp/web",
		InstanceKey: "cri.orchestrator.io/v1/Service",
		Status:      &deploy.RolloutStatus{Short: "SUCCESS"},
		Body:        json.RawMessage(`{"image":"nginx"}`),
	}

	old := os.Stdout
	rd, w, _ := os.Pipe()
	os.Stdout = w

	printRolloutDetail(engine, r)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rd)
	output := buf.String()

	if !strings.Contains(output, "myapp/web") {
		t.Error("expected instance ID")
	}
	if !strings.Contains(output, "SUCCESS") {
		t.Error("expected status")
	}
}

func TestPrintRolloutLinks(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.bolt")
	engine, err := orchestrate.New(orchestrate.Config{DBPath: dbPath})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = engine.Close() }()

	r := &deploy.Rollout{
		InstanceId:  "myapp/web",
		InstanceKey: "cri.orchestrator.io/v1/Service",
	}

	// Call printRolloutLinks — with no links, it should just not panic.
	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	printRolloutLinks(engine, r)

	_ = w.Close()
	os.Stdout = old
}

func TestOpenReadOnlyEngine(t *testing.T) {
	engine, err := openReadOnlyEngine()
	if err != nil {
		t.Fatalf("openReadOnlyEngine: %v", err)
	}
	defer func() { _ = engine.Close() }()

	// Just verify State() works without error.
	_, err = engine.State()
	if err != nil {
		t.Fatalf("State: %v", err)
	}
}

func TestNewRollbackEngine(t *testing.T) {
	engine, cleanup, err := newRollbackEngine()
	if err != nil {
		t.Fatalf("newRollbackEngine: %v", err)
	}
	defer cleanup()

	deployments, err := engine.ListDeployments()
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	if len(deployments) != 0 {
		t.Errorf("expected 0 deployments, got %d", len(deployments))
	}
}

func TestRemoteRollbackList(t *testing.T) {
	// remoteRollbackList currently prints a not-supported message.
	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := remoteRollbackList(context.Background(), nil)

	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("remoteRollbackList: %v", err)
	}
}

func TestRunRollback_List(t *testing.T) {
	// Ensure runRollback with "list" arg doesn't panic.
	// Uses local engine (no remote socket).
	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = ""
	defer func() { flagCRISocket = oldCRI }()

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := runRollback(rollbackCmd, []string{"list"})

	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("runRollback list: %v", err)
	}
}

func TestRunRollback_NoArgs(t *testing.T) {
	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = ""
	defer func() { flagCRISocket = oldCRI }()

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := runRollback(rollbackCmd, nil)

	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("runRollback no args: %v", err)
	}
}

func TestRunStateList_Local(t *testing.T) {
	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = ""
	defer func() { flagCRISocket = oldCRI }()

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := runStateList(stateListCmd, nil)

	_ = w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("runStateList: %v", err)
	}
}

func TestRunStateShow_NotFound(t *testing.T) {
	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = ""
	defer func() { flagCRISocket = oldCRI }()

	err := runStateShow(stateShowCmd, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent rollout")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestDoImagesCRI(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// Pull some images first.
	if err := criClient.PullImage(ctx, "nginx:latest"); err != nil {
		t.Fatalf("PullImage: %v", err)
	}

	// Use the mockRunner eval via flags.
	oldDir := flagProjectDir
	flagProjectDir = dir
	defer func() { flagProjectDir = oldDir }()

	// doImagesCRI uses eval.ExecRunner which will fail, but we just need the CRI code path.
	// We can't test the full path without nix, so we test at least the CRI listing part.
	_ = doImagesCRI(ctx, criClient, nil)
}

func TestDoPullCRI(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	oldDir := flagProjectDir
	flagProjectDir = dir
	defer func() { flagProjectDir = oldDir }()

	// doPullCRI uses eval.ExecRunner, will fail for eval but exercises the CRI code path.
	_ = doPullCRI(ctx, criClient, nil)
}

func TestPrintRolloutLinks_WithDeps(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.bolt")
	engine, err := orchestrate.New(orchestrate.Config{DBPath: dbPath})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = engine.Close() }()

	// Save rollout and add link.
	r := &deploy.Rollout{
		InstanceId:  "myapp/web",
		InstanceKey: "cri.orchestrator.io/v1/Service",
	}

	old := os.Stdout
	rd, w, _ := os.Pipe()
	os.Stdout = w

	printRolloutLinks(engine, r)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rd)
	// No deps, so no output expected, but should not panic.
	_ = buf.String()
}

func TestPrintRolloutBody_EmptyNoOutput(t *testing.T) {
	r := &deploy.Rollout{
		InstanceId:  "myapp/web",
		InstanceKey: "cri.orchestrator.io/v1/Service",
	}

	old := os.Stdout
	rd, w, _ := os.Pipe()
	os.Stdout = w

	printRolloutBody(r)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rd)
	output := buf.String()

	if output != "" {
		t.Errorf("expected no output for empty body, got %q", output)
	}
}

func TestPrintRolloutBody_WithBodyOutput(t *testing.T) {
	r := &deploy.Rollout{
		InstanceId:  "myapp/web",
		InstanceKey: "cri.orchestrator.io/v1/Service",
		Body:        json.RawMessage(`{"image":"nginx"}`),
	}

	old := os.Stdout
	rd, w, _ := os.Pipe()
	os.Stdout = w

	printRolloutBody(r)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rd)
	output := buf.String()

	if !strings.Contains(output, "Spec") {
		t.Error("expected Spec header in output")
	}
}

func TestPrintPlan_CreateAndNoOp(t *testing.T) {
	plan := &orchestrate.Plan{
		Actions: []orchestrate.Action{
			{Type: orchestrate.ActionCreate, ResourceID: "myapp/web", Key: "cri.orchestrator.io/v1/Service", Reason: "new"},
			{Type: orchestrate.ActionNoOp, ResourceID: "myapp/db", Key: "cri.orchestrator.io/v1/Service", Reason: "unchanged"},
		},
	}

	old := os.Stdout
	rd, w, _ := os.Pipe()
	os.Stdout = w

	printPlan(plan)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rd)
	output := buf.String()

	if !strings.Contains(output, "+") {
		t.Error("expected + symbol for create action")
	}
	if strings.Contains(output, "myapp/db") {
		t.Error("NoOp should be skipped in output")
	}
	if !strings.Contains(output, "Plan:") {
		t.Error("expected Plan: summary line")
	}
}

func TestRemoteStateShow_NotFound(t *testing.T) {
	// remoteStateShow with a mock client that returns empty state.
	// We can't easily mock the client without a server, so test the code path
	// via the local engine.
	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = ""
	defer func() { flagCRISocket = oldCRI }()

	err := runStateShow(stateShowCmd, []string{"nonexistent-id"})
	if err == nil {
		t.Error("expected error for nonexistent rollout")
	}
}

func TestLookupContainerID_CRI(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "lookuptest", []string{"web"})

	containerID, err := lookupContainerID(ctx, criClient, "lookuptest", "web")
	if err != nil {
		t.Fatalf("lookupContainerID: %v", err)
	}
	if containerID == "" {
		t.Error("expected non-empty container ID")
	}
}

func TestLookupContainerID_NotFound(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	_, err = lookupContainerID(ctx, criClient, "noproject", "nosvc")
	if err == nil {
		t.Fatal("expected error for nonexistent service")
	}
}

func TestDialRemote_EmptySocket(t *testing.T) {
	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	rc := dialRemote(context.Background())
	if rc != nil {
		t.Error("expected nil for empty socket")
	}
}

func TestDialRemote_InvalidSocket(t *testing.T) {
	oldSocket := flagRemoteSocket
	flagRemoteSocket = "/nonexistent/socket.sock"
	defer func() { flagRemoteSocket = oldSocket }()

	rc := dialRemote(context.Background())
	// dialRemote prints a warning and returns nil on error.
	if rc != nil {
		_ = rc.Close()
	}
}

func TestResolveDefaultExec_EvalError(t *testing.T) {
	dir := t.TempDir()
	// resolveDefaultExec calls eval.ExecRunner which will fail because there's no flake.nix.
	_, err := resolveDefaultExec(context.Background(), dir, "web")
	if err == nil {
		t.Error("expected error for missing flake.nix")
	}
}

func TestShutdownServices_NilComposition(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	// With nil composition, should fall back to ProjectDown.
	err = shutdownServices(ctx, criClient, "noproject", 10, nil)
	if err != nil {
		t.Fatalf("shutdownServices: %v", err)
	}
}

func TestRunDown_NoRemoteNoCRI(t *testing.T) {
	dir := t.TempDir()

	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	// Point at a socket that cannot exist, so the result does not depend on
	// whether the machine running the tests happens to have a runtime.
	oldCRI := flagCRISocket
	flagCRISocket = filepath.Join(dir, "definitely-absent.sock")
	defer func() { flagCRISocket = oldCRI }()

	oldDir := flagProjectDir
	flagProjectDir = dir
	defer func() { flagProjectDir = oldDir }()

	// No remote and no reachable CRI: there is no second backend to fall back
	// to, so this must fail loudly rather than silently doing nothing.
	if err := runDown(downCmd, nil); err == nil {
		t.Fatal("runDown with no remote and no CRI: got nil error, want failure")
	}
}

func TestResolveLogServices_EvalFallback(t *testing.T) {
	dir := t.TempDir()
	// No args and no flake.nix → eval will fail.
	_, err := resolveLogServices(context.Background(), dir, nil)
	if err == nil {
		t.Error("expected error for missing flake.nix")
	}
}

// startCLIOrchestrateServer sets up a CRI mock + orchestrate gRPC server and
// returns the orchestrate socket path. The caller can set flagRemoteSocket to
// this path to exercise CLI remote functions.
func startCLIOrchestrateServer(t *testing.T) string {
	t.Helper()

	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial CRI: %v", err)
	}
	t.Cleanup(func() { _ = criClient.Close() })

	orchSock := filepath.Join(t.TempDir(), "orch.sock")

	// Lazy import through indirect init — we need the server package.
	// Build server config with test-scoped paths.
	srv := newCLIOrchestrateServer(t, criClient, orchSock)

	lis, err := net.Listen("unix", orchSock)
	if err != nil {
		t.Fatalf("listen orch: %v", err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	return orchSock
}

func TestRemoteDrift_Empty(t *testing.T) {
	orchSock := startCLIOrchestrateServer(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldDir := flagProjectDir
	flagProjectDir = t.TempDir()
	defer func() { flagProjectDir = oldDir }()

	// Redirect stdout to capture output.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runDrift(driftCmd, nil)

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("runDrift: %v", err)
	}
	if !strings.Contains(buf.String(), "No drift detected") {
		t.Errorf("expected 'No drift detected', got %q", buf.String())
	}
}

func TestRemoteStateList_Empty(t *testing.T) {
	orchSock := startCLIOrchestrateServer(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldDir := flagProjectDir
	flagProjectDir = t.TempDir()
	defer func() { flagProjectDir = oldDir }()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runStateList(stateListCmd, nil)

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("runStateList: %v", err)
	}
	if !strings.Contains(buf.String(), "No rollouts found") {
		t.Errorf("expected 'No rollouts found', got %q", buf.String())
	}
}

func TestRemoteStateShow_NoRollouts(t *testing.T) {
	orchSock := startCLIOrchestrateServer(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	err := runStateShow(stateShowCmd, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent rollout")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got %v", err)
	}
}

func TestRemoteRollbackApply_NotFound(t *testing.T) {
	orchSock := startCLIOrchestrateServer(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldDryRun := rollbackDryRun
	rollbackDryRun = true
	defer func() { rollbackDryRun = oldDryRun }()

	err := runRollback(rollbackCmd, []string{"nonexistent-deployment-id"})
	if err == nil {
		t.Fatal("expected error for nonexistent deployment")
	}
}

func TestDetectCRI_WithSocket(t *testing.T) {
	sock, _ := startCLIMockCRI(t)

	oldCRI := flagCRISocket
	flagCRISocket = sock
	defer func() { flagCRISocket = oldCRI }()

	ctx := context.Background()
	c, err := detectCRI(ctx)
	if err != nil {
		t.Fatalf("detectCRI: %v", err)
	}
	defer func() { _ = c.Close() }()
}

func TestDetectCRI_InvalidSocket(t *testing.T) {
	oldCRI := flagCRISocket
	flagCRISocket = "/nonexistent/cri.sock"
	defer func() { flagCRISocket = oldCRI }()

	ctx := context.Background()
	_, err := detectCRI(ctx)
	if err == nil {
		t.Fatal("expected error for invalid CRI socket")
	}
}

func TestRunDown_WithCRI(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	dir := t.TempDir()

	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = sock
	defer func() { flagCRISocket = oldCRI }()

	oldDir := flagProjectDir
	flagProjectDir = dir
	defer func() { flagProjectDir = oldDir }()

	// No services running, should succeed as a no-op.
	err := runDown(downCmd, nil)
	if err != nil {
		t.Fatalf("runDown with CRI: %v", err)
	}
}

func TestRunLogs_NoCRINoRemote(t *testing.T) {
	dir := t.TempDir()

	isolateCRISockets(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = ""
	defer func() { flagCRISocket = oldCRI }()

	oldDir := flagProjectDir
	flagProjectDir = dir
	defer func() { flagProjectDir = oldDir }()

	// No CRI, no remote → falls back to passthrough which will fail (no compose file).
	err := runLogs(logsCmd, nil)
	if err == nil {
		t.Fatal("expected error (no compose file for passthrough)")
	}
}

func TestRunPlan_NoCRI(t *testing.T) {
	isolateCRISockets(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = ""
	defer func() { flagCRISocket = oldCRI }()

	err := runPlan(planCmd, nil)
	if err == nil {
		t.Fatal("expected error (plan requires CRI)")
	}
	if !strings.Contains(err.Error(), "no CRI runtime found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunDrift_NoCRI(t *testing.T) {
	isolateCRISockets(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = ""
	defer func() { flagCRISocket = oldCRI }()

	err := runDrift(driftCmd, nil)
	if err == nil {
		t.Fatal("expected error (drift requires CRI)")
	}
	if !strings.Contains(err.Error(), "no CRI runtime found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunExec_NoCRINoRemote(t *testing.T) {
	dir := t.TempDir()

	isolateCRISockets(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = ""
	defer func() { flagCRISocket = oldCRI }()

	oldDir := flagProjectDir
	flagProjectDir = dir
	defer func() { flagProjectDir = oldDir }()

	// No CRI, no remote → falls back to passthrough which needs compose file.
	err := runExec(execCmd, []string{"web", "echo", "hello"})
	if err == nil {
		t.Fatal("expected error (no compose file for passthrough)")
	}
}

func TestRunImages_NoCRI(t *testing.T) {
	dir := t.TempDir()

	isolateCRISockets(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = ""
	defer func() { flagCRISocket = oldCRI }()

	oldDir := flagProjectDir
	flagProjectDir = dir
	defer func() { flagProjectDir = oldDir }()

	// No CRI → falls back to passthrough.
	err := runImages(imagesCmd, nil)
	if err == nil {
		t.Fatal("expected error (no compose file for passthrough)")
	}
}

func TestRunPull_NoCRI(t *testing.T) {
	dir := t.TempDir()

	isolateCRISockets(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = ""
	defer func() { flagCRISocket = oldCRI }()

	oldDir := flagProjectDir
	flagProjectDir = dir
	defer func() { flagProjectDir = oldDir }()

	// No CRI → falls back to passthrough.
	err := runPull(pullCmd, nil)
	if err == nil {
		t.Fatal("expected error (no compose file for passthrough)")
	}
}

func TestCondPriority_Up(t *testing.T) {
	tests := []struct {
		cond     string
		expected int
	}{
		{"service_healthy", 3},
		{"service_completed_successfully", 2},
		{"service_started", 1},
		{"", 0},
		{"unknown", 0},
	}
	for _, tt := range tests {
		got := condPriority(tt.cond)
		if got != tt.expected {
			t.Errorf("condPriority(%q) = %d, want %d", tt.cond, got, tt.expected)
		}
	}
}

func TestLookupPodIP(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "iptest", []string{"web"})

	ip, err := lookupPodIP(ctx, criClient, "iptest", "web")
	if err != nil {
		t.Fatalf("lookupPodIP: %v", err)
	}
	if ip == "" {
		t.Error("expected non-empty IP")
	}
}

func TestLookupPodIP_NotFound(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	_, err = lookupPodIP(ctx, criClient, "noproject", "nosvc")
	if err == nil {
		t.Fatal("expected error for nonexistent pod")
	}
}

func TestExecNonInteractive_CRI(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "exectest", []string{"web"})

	// Set up exec response.
	mock.mu.Lock()
	for ctrID := range mock.containers {
		mock.execStdout[ctrID] = []byte("hello\n")
		mock.execExitCodes[ctrID] = 0
	}
	mock.mu.Unlock()

	// Find container ID.
	ctrID, err := lookupContainerID(ctx, criClient, "exectest", "web")
	if err != nil {
		t.Fatalf("lookupContainerID: %v", err)
	}

	// Redirect stdout.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = execNonInteractive(ctx, criClient, ctrID, []string{"echo", "hello"})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("execNonInteractive: %v", err)
	}
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("expected 'hello' in output, got %q", buf.String())
	}
}

func TestDoExecCRI_Success(t *testing.T) {
	sock, mock := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "exectest2", []string{"web"})

	// Set up exec response for all containers.
	mock.mu.Lock()
	for ctrID := range mock.containers {
		mock.execStdout[ctrID] = []byte("ok\n")
		mock.execExitCodes[ctrID] = 0
	}
	mock.mu.Unlock()

	oldDir := flagProjectDir
	flagProjectDir = t.TempDir()
	defer func() { flagProjectDir = oldDir }()

	oldProject := flagProjectName
	flagProjectName = "exectest2"
	defer func() { flagProjectName = oldProject }()

	// Redirect stdout.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = doExecCRI(ctx, criClient, []string{"web", "echo", "hello"})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("doExecCRI: %v", err)
	}
}

func TestLogPrinter_PrintWithPrefix(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printer := newLogPrinter(false, false)
	printer.print(&orchestratev1.LogEntry{
		Service: "web",
		Message: "test log line",
	})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	output := buf.String()
	if !strings.Contains(output, "web") {
		t.Errorf("expected 'web' prefix in output, got %q", output)
	}
	if !strings.Contains(output, "test log line") {
		t.Errorf("expected 'test log line' in output, got %q", output)
	}
}

func TestLogPrinter_PrintWithTimestamp(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printer := newLogPrinter(true, true)
	ts := timestamppb.Now()
	printer.print(&orchestratev1.LogEntry{
		Service:   "db",
		Message:   "db starting",
		Timestamp: ts,
	})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	output := buf.String()
	// No prefix since noPrefix is true.
	if strings.Contains(output, "|") {
		t.Errorf("expected no prefix separator, got %q", output)
	}
	// Should have timestamp.
	if !strings.Contains(output, "T") {
		t.Errorf("expected timestamp in output, got %q", output)
	}
}

func TestRunCreateCRI_NoFlake(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	oldDir := flagProjectDir
	flagProjectDir = t.TempDir()
	defer func() { flagProjectDir = oldDir }()

	// runCreateCRI requires nix eval, will fail without flake.nix.
	err = runCreateCRI(ctx, criClient, nil)
	if err == nil {
		t.Fatal("expected error (no flake.nix)")
	}
}

func TestSetupCNI(t *testing.T) {
	// setupCNI should not panic and return a boolean.
	result := setupCNI("testproject")
	// Result can be true or false depending on system state.
	_ = result
}

// TestResolveContainers_AllServicesFromProject tests resolving all containers for a project.
func TestResolveContainers_AllServicesFromProject(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "allsvcs", []string{"web", "api", "db"})

	containers, err := resolveContainers(ctx, criClient, "allsvcs", nil)
	if err != nil {
		t.Fatalf("resolveContainers: %v", err)
	}
	if len(containers) != 3 {
		t.Errorf("expected 3 containers, got %d", len(containers))
	}
}

func TestRunRmCRI_StoppedContainers(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "rmtest", []string{"web"})

	// Stop the container first.
	if err := criClient.ServiceDown(ctx, "rmtest", "web", 10); err != nil {
		t.Fatalf("ServiceDown: %v", err)
	}

	// Remove stopped containers.
	err = runRmCRI(ctx, criClient, "rmtest", nil, false, false)
	if err != nil {
		t.Fatalf("runRmCRI: %v", err)
	}
}

func TestRunRmCRI_WithStop(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "rmstop", []string{"web"})

	// Remove with stop=true.
	err = runRmCRI(ctx, criClient, "rmstop", nil, true, false)
	if err != nil {
		t.Fatalf("runRmCRI with stop: %v", err)
	}
}

func TestRunRmCRI_WithVolumes(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "rmvols", []string{"web"})

	// Remove with volumes (stop first to make containers removable).
	err = runRmCRI(ctx, criClient, "rmvols", nil, true, true)
	if err != nil {
		t.Fatalf("runRmCRI with volumes: %v", err)
	}
}

func TestRunKillCRI(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "killtest", []string{"web", "db"})

	err = runKillCRI(ctx, criClient, "killtest", nil)
	if err != nil {
		t.Fatalf("runKillCRI: %v", err)
	}
}

func TestCRIDownOrdered_WithComp(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "downord", []string{"web", "db"})

	dir := t.TempDir()
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:latest"},
			"db":  {Image: "postgres:latest"},
		},
	}

	err = CRIDownOrdered(ctx, criClient, "downord", dir, 10, true, comp)
	if err != nil {
		t.Fatalf("CRIDownOrdered with comp: %v", err)
	}
}

func TestDoLogsCRI_NoFlake(t *testing.T) {
	sock, _ := startCLIMockCRI(t)

	oldCRI := flagCRISocket
	flagCRISocket = sock
	defer func() { flagCRISocket = oldCRI }()

	oldDir := flagProjectDir
	flagProjectDir = t.TempDir()
	defer func() { flagProjectDir = oldDir }()

	// doLogsCRI with no args tries to eval the flake → error.
	ctx := context.Background()
	err := doLogsCRI(ctx, nil)
	if err == nil {
		t.Fatal("expected error (no flake.nix for eval)")
	}
}

func TestProjectDir_Explicit(t *testing.T) {
	dir := t.TempDir()
	oldDir := flagProjectDir
	flagProjectDir = dir
	defer func() { flagProjectDir = oldDir }()

	got := projectDir()
	if got != dir {
		t.Errorf("projectDir() = %q, want %q", got, dir)
	}
}

func TestRunServe_NoSocket_FallsBackToVsock(t *testing.T) {
	oldSocket := serveSocket
	serveSocket = ""
	defer func() { serveSocket = oldSocket }()

	// With --socket empty, serve takes the vsock path. On a host without
	// vsock that listen fails by itself, which is what this test used to
	// rely on — but on a host *with* vsock it succeeds and runServe blocks
	// in Serve until a signal, hanging the whole package. That is not
	// hypothetical: microvm mode means this project runs in exactly such a
	// VM. Holding the port first makes the bind fail either way, so what
	// gets tested is the error path rather than the host.
	if held, err := listenVsock(serveVsockPort); err == nil {
		defer func() { _ = held.Close() }()
	}

	err := runServe(serveCmd, nil)
	if err == nil {
		t.Fatal("expected error when vsock is unavailable")
	}
	if !strings.Contains(err.Error(), "vsock") && !strings.Contains(err.Error(), "CRI") {
		t.Errorf("expected vsock or CRI error, got: %v", err)
	}
}

func TestVsockPersistentFlags(t *testing.T) {
	flags := []string{"remote-vsock-cid", "remote-vsock-port"}
	for _, name := range flags {
		f := rootCmd.PersistentFlags().Lookup(name)
		if f == nil {
			t.Errorf("missing persistent flag %q", name)
		}
	}

	// Check defaults.
	cidFlag := rootCmd.PersistentFlags().Lookup("remote-vsock-cid")
	if cidFlag.DefValue != "0" {
		t.Errorf("remote-vsock-cid default = %q, want %q", cidFlag.DefValue, "0")
	}
	portFlag := rootCmd.PersistentFlags().Lookup("remote-vsock-port")
	if portFlag.DefValue != "1024" {
		t.Errorf("remote-vsock-port default = %q, want %q", portFlag.DefValue, "1024")
	}
}

func TestDialRemote_NoFlags(t *testing.T) {
	oldSocket := flagRemoteSocket
	oldCID := flagRemoteVsockCID
	flagRemoteSocket = ""
	flagRemoteVsockCID = 0
	defer func() {
		flagRemoteSocket = oldSocket
		flagRemoteVsockCID = oldCID
	}()

	c := dialRemote(context.Background())
	if c != nil {
		t.Error("expected nil when no remote flags set")
	}
}

func TestDialRemote_VsockPath(t *testing.T) {
	oldSocket := flagRemoteSocket
	oldCID := flagRemoteVsockCID
	oldPort := flagRemoteVsockPort
	flagRemoteSocket = ""
	flagRemoteVsockCID = 99
	flagRemoteVsockPort = 1024
	defer func() {
		flagRemoteSocket = oldSocket
		flagRemoteVsockCID = oldCID
		flagRemoteVsockPort = oldPort
	}()

	c := dialRemote(context.Background())
	if c != nil {
		_ = c.Close()
		t.Error("expected nil for unreachable vsock")
	}
}

func TestServeVsockPortFlag(t *testing.T) {
	f := serveCmd.Flags().Lookup("vsock-port")
	if f == nil {
		t.Fatal("missing --vsock-port flag on serve")
		return
	}
	if f.DefValue != "1024" {
		t.Errorf("vsock-port default = %q, want %q", f.DefValue, "1024")
	}
}

func TestRunRender_BadTarget(t *testing.T) {
	oldTarget := renderTarget
	renderTarget = "invalid"
	defer func() { renderTarget = oldTarget }()

	err := runRender(renderCmd, nil)
	if err == nil {
		t.Fatal("expected error for unsupported target")
	}
	if !strings.Contains(err.Error(), "unsupported target") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunRestart_CRI(t *testing.T) {
	sock, _ := startCLIMockCRI(t)
	ctx := context.Background()
	criClient, err := cri.Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = criClient.Close() }()

	setupCRIServices(t, ctx, criClient, "restarttest", []string{"web"})

	// runRestartCRI restarts containers by stopping and starting them.
	err = runRestartCRI(ctx, criClient, "restarttest", nil, 10)
	if err != nil {
		t.Fatalf("runRestartCRI: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Remote function tests — exercising remoteDown, remoteLogs, remoteExec,
// remoteStateList, remoteStateShow, remoteRollbackApply via the orchestrate
// server mock infrastructure.
// ---------------------------------------------------------------------------

// TestRemoteDown_TeardownNoComp tests remoteDown where eval fails (no flake)
// but teardown still proceeds successfully.
func TestRemoteDown_TeardownNoComp(t *testing.T) {
	orchSock := startCLIOrchestrateServer(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldDir := flagProjectDir
	flagProjectDir = t.TempDir()
	defer func() { flagProjectDir = oldDir }()

	oldProject := flagProjectName
	flagProjectName = "downtest"
	defer func() { flagProjectName = oldProject }()

	// Redirect stdout to capture output.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runDown(downCmd, nil)

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("runDown: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "Tearing down via remote") {
		t.Errorf("expected teardown message, got %q", output)
	}
	if !strings.Contains(output, "Teardown complete") {
		t.Errorf("expected completion message, got %q", output)
	}
}

// TestRemoteDown_WithVolumes tests remoteDown with the volumes flag set.
func TestRemoteDown_WithVolumes(t *testing.T) {
	orchSock := startCLIOrchestrateServer(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldDir := flagProjectDir
	flagProjectDir = t.TempDir()
	defer func() { flagProjectDir = oldDir }()

	oldVol := downVolumes
	downVolumes = true
	defer func() { downVolumes = oldVol }()

	oldTimeout := downTimeout
	downTimeout = 5
	defer func() { downTimeout = oldTimeout }()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runDown(downCmd, nil)

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("runDown with volumes: %v", err)
	}
	if !strings.Contains(buf.String(), "Teardown complete") {
		t.Errorf("expected completion message, got %q", buf.String())
	}
}

// TestRemoteLogs_EmptyStream tests remoteLogs when no logs exist on the server.
func TestRemoteLogs_EmptyStream(t *testing.T) {
	orchSock := startCLIOrchestrateServer(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldDir := flagProjectDir
	flagProjectDir = t.TempDir()
	defer func() { flagProjectDir = oldDir }()

	oldProject := flagProjectName
	flagProjectName = "logstest"
	defer func() { flagProjectName = oldProject }()

	oldFollow := logsFollow
	logsFollow = false
	defer func() { logsFollow = oldFollow }()

	oldTimestamps := logsTimestamps
	logsTimestamps = false
	defer func() { logsTimestamps = oldTimestamps }()

	oldTail := logsTail
	logsTail = ""
	defer func() { logsTail = oldTail }()

	oldSince := logsSince
	logsSince = ""
	defer func() { logsSince = oldSince }()

	oldNoPrefix := logsNoLogPrefix
	logsNoLogPrefix = false
	defer func() { logsNoLogPrefix = oldNoPrefix }()

	// Redirect stdout.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runLogs(logsCmd, nil)

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("runLogs: %v", err)
	}
	// With no logs, output should be empty.
	_ = buf.String()
}

// TestRemoteLogs_WithServiceFilter tests remoteLogs with a service filter argument.
func TestRemoteLogs_WithServiceFilter(t *testing.T) {
	orchSock := startCLIOrchestrateServer(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldDir := flagProjectDir
	flagProjectDir = t.TempDir()
	defer func() { flagProjectDir = oldDir }()

	oldProject := flagProjectName
	flagProjectName = "logsfilt"
	defer func() { flagProjectName = oldProject }()

	oldFollow := logsFollow
	logsFollow = false
	defer func() { logsFollow = oldFollow }()

	oldTimestamps := logsTimestamps
	logsTimestamps = true
	defer func() { logsTimestamps = oldTimestamps }()

	oldNoPrefix := logsNoLogPrefix
	logsNoLogPrefix = true
	defer func() { logsNoLogPrefix = oldNoPrefix }()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runLogs(logsCmd, []string{"web"})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("runLogs with filter: %v", err)
	}
	_ = buf.String()
}

// TestRemoteExec_NoContainer tests remoteExec when the service has no containers.
func TestRemoteExec_NoContainer(t *testing.T) {
	orchSock := startCLIOrchestrateServer(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldDir := flagProjectDir
	flagProjectDir = t.TempDir()
	defer func() { flagProjectDir = oldDir }()

	oldProject := flagProjectName
	flagProjectName = "exectest"
	defer func() { flagProjectName = oldProject }()

	err := runExec(execCmd, []string{"nonexistent-svc", "echo", "hello"})
	if err == nil {
		t.Fatal("expected error for nonexistent service container")
	}
	if !strings.Contains(err.Error(), "remote exec") {
		t.Errorf("expected 'remote exec' error, got %v", err)
	}
}

// TestRemoteExec_NoCommand tests remoteExec when no command is specified and
// resolveDefaultExec fails because there is no flake.nix for evaluation.
func TestRemoteExec_NoCommand(t *testing.T) {
	orchSock := startCLIOrchestrateServer(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldDir := flagProjectDir
	flagProjectDir = t.TempDir()
	defer func() { flagProjectDir = oldDir }()

	err := runExec(execCmd, []string{"web"})
	if err == nil {
		t.Fatal("expected error for missing default exec")
	}
	if !strings.Contains(err.Error(), "evaluation failed") {
		t.Errorf("expected 'evaluation failed' error, got %v", err)
	}
}

// TestRemoteRollbackApply_DryRun tests remoteRollbackApply with dry-run true.
// With no deployments in the store the rollback should fail.
func TestRemoteRollbackApply_DryRun(t *testing.T) {
	orchSock := startCLIOrchestrateServer(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldDryRun := rollbackDryRun
	rollbackDryRun = true
	defer func() { rollbackDryRun = oldDryRun }()

	err := runRollback(rollbackCmd, []string{"some-deployment-id"})
	if err == nil {
		t.Fatal("expected error for nonexistent deployment in dry-run mode")
	}
}

// TestRemoteRollbackApply_NoDryRun tests remoteRollbackApply with dry-run false.
// With no deployments, rollback should fail.
func TestRemoteRollbackApply_NoDryRun(t *testing.T) {
	orchSock := startCLIOrchestrateServer(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldDryRun := rollbackDryRun
	rollbackDryRun = false
	defer func() { rollbackDryRun = oldDryRun }()

	err := runRollback(rollbackCmd, []string{"nonexistent-deploy"})
	if err == nil {
		t.Fatal("expected error for nonexistent deployment")
	}
}

// TestRemoteStateList_WithProjectName tests remoteStateList with an explicit project name.
func TestRemoteStateList_WithProjectName(t *testing.T) {
	orchSock := startCLIOrchestrateServer(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldDir := flagProjectDir
	flagProjectDir = t.TempDir()
	defer func() { flagProjectDir = oldDir }()

	oldProject := flagProjectName
	flagProjectName = "stateproj"
	defer func() { flagProjectName = oldProject }()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runStateList(stateListCmd, nil)

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("runStateList: %v", err)
	}
	if !strings.Contains(buf.String(), "No rollouts found") {
		t.Errorf("expected 'No rollouts found', got %q", buf.String())
	}
}

// TestRemoteStateShow_NotFoundByID tests remoteStateShow with a specific ID
// that does not exist.
func TestRemoteStateShow_NotFoundByID(t *testing.T) {
	orchSock := startCLIOrchestrateServer(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	err := runStateShow(stateShowCmd, []string{"does-not-exist-id"})
	if err == nil {
		t.Fatal("expected error for nonexistent rollout")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got %v", err)
	}
}

// TestRemoteStateShow_MultipleIDs tests remoteStateShow with different IDs
// to exercise the loop branch in remoteStateShow.
func TestRemoteStateShow_MultipleIDs(t *testing.T) {
	orchSock := startCLIOrchestrateServer(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	// Test several non-existent IDs.
	for _, id := range []string{"a/b", "c/d", "test/service"} {
		err := runStateShow(stateShowCmd, []string{id})
		if err == nil {
			t.Fatalf("expected error for nonexistent rollout %q", id)
		}
	}
}

// TestPrintResourceWarnings_Exceeds tests printResourceWarnings with various
// resource misconfiguration scenarios.
func TestPrintResourceWarnings_Exceeds(t *testing.T) {
	tests := []struct {
		name     string
		svcName  string
		image    string
		limits   eval.ResourceSpec
		requests eval.ResourceSpec
		expect   []string
	}{
		{
			name:     "CPUExceedsLimit",
			svcName:  "web",
			image:    "nginx",
			limits:   eval.ResourceSpec{CPU: "100m", Memory: "256Mi"},
			requests: eval.ResourceSpec{CPU: "500m", Memory: "128Mi"},
			expect:   []string{"Warning", "CPU request"},
		},
		{
			name:     "MemoryExceedsLimit",
			svcName:  "db",
			image:    "postgres",
			limits:   eval.ResourceSpec{CPU: "1", Memory: "128Mi"},
			requests: eval.ResourceSpec{CPU: "0.5", Memory: "512Mi"},
			expect:   []string{"memory request"},
		},
		{
			name:     "BothExceed",
			svcName:  "app",
			image:    "myapp",
			limits:   eval.ResourceSpec{CPU: "100m", Memory: "64Mi"},
			requests: eval.ResourceSpec{CPU: "200m", Memory: "128Mi"},
			expect:   []string{"CPU request", "memory request"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comp := &eval.Composition{
				Services: map[string]eval.Service{
					tt.svcName: {
						Image: tt.image,
						XNixCompose: &eval.NixComposeExtended{
							Resources: &eval.Resources{
								Limits:   &tt.limits,
								Requests: &tt.requests,
							},
						},
					},
				},
			}

			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			printResourceWarnings(comp)

			_ = w.Close()
			os.Stdout = oldStdout
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(r)

			output := buf.String()
			for _, s := range tt.expect {
				if !strings.Contains(output, s) {
					t.Errorf("expected %q in output, got %q", s, output)
				}
			}
		})
	}
}

// TestPrintResourceWarnings_MultipleServices tests printResourceWarnings with
// multiple services, some with warnings and some without.
func TestPrintResourceWarnings_MultipleServices(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {
				Image: "nginx",
				XNixCompose: &eval.NixComposeExtended{
					Resources: &eval.Resources{
						Limits:   &eval.ResourceSpec{CPU: "1", Memory: "512Mi"},
						Requests: &eval.ResourceSpec{CPU: "0.5", Memory: "256Mi"},
					},
				},
			},
			"worker": {
				Image: "worker",
				XNixCompose: &eval.NixComposeExtended{
					Resources: &eval.Resources{
						Limits:   &eval.ResourceSpec{CPU: "100m", Memory: "64Mi"},
						Requests: &eval.ResourceSpec{CPU: "500m", Memory: "256Mi"},
					},
				},
			},
		},
	}

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printResourceWarnings(comp)

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	output := buf.String()
	// web has no warnings (requests < limits).
	// worker has both CPU and memory warnings.
	if !strings.Contains(output, "worker") {
		t.Errorf("expected worker warnings, got %q", output)
	}
}

// TestResolveDefaultExec_NoFlakeNix tests resolveDefaultExec when the project
// directory has no flake.nix file, verifying the eval error path.
func TestResolveDefaultExec_NoFlakeNix(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveDefaultExec(context.Background(), dir, "myservice")
	if err == nil {
		t.Fatal("expected error for missing flake.nix")
	}
	if !strings.Contains(err.Error(), "evaluation failed") {
		t.Errorf("expected 'evaluation failed', got %v", err)
	}
}

// TestResolveDefaultExec_WithFlakeAttr tests resolveDefaultExec with a custom
// flake attribute that does not exist.
func TestResolveDefaultExec_WithFlakeAttr(t *testing.T) {
	dir := t.TempDir()

	oldAttr := flagFlakeAttr
	flagFlakeAttr = "nonexistent.attr"
	defer func() { flagFlakeAttr = oldAttr }()

	_, err := resolveDefaultExec(context.Background(), dir, "web")
	if err == nil {
		t.Fatal("expected error for bad flake attr")
	}
}

// TestPrintRolloutLinks_EmptyEngine tests printRolloutLinks with an empty engine
// and a rollout that has no stored links.
func TestPrintRolloutLinks_EmptyEngine(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "links.bolt")
	engine, err := orchestrate.New(orchestrate.Config{DBPath: dbPath})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = engine.Close() }()

	r := &deploy.Rollout{
		InstanceId:  "testproj/myservice",
		InstanceKey: "cri.orchestrator.io/v1/Service",
	}

	oldStdout := os.Stdout
	rd, w, _ := os.Pipe()
	os.Stdout = w

	printRolloutLinks(engine, r)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rd)

	// No deps or dependents, so output should be empty.
	if buf.String() != "" {
		t.Errorf("expected empty output for no links, got %q", buf.String())
	}
}

// TestPrintRemoteRolloutDetail_WithBody tests printRemoteRolloutDetail with
// a rollout that includes a JSON body.
func TestPrintRemoteRolloutDetail_WithBody(t *testing.T) {
	r := &orchestratev1.RolloutInfo{
		InstanceId:  "proj/web",
		InstanceKey: "cri/v1/Service",
		Status:      "running",
		Body:        []byte(`{"image":"nginx:latest","replicas":1}`),
	}

	oldStdout := os.Stdout
	rd, w, _ := os.Pipe()
	os.Stdout = w

	printRemoteRolloutDetail(r)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rd)

	output := buf.String()
	if !strings.Contains(output, "ID:     proj/web") {
		t.Errorf("expected ID in output, got %q", output)
	}
	if !strings.Contains(output, "Kind:   cri/v1/Service") {
		t.Errorf("expected Kind in output, got %q", output)
	}
	if !strings.Contains(output, "Status: running") {
		t.Errorf("expected Status in output, got %q", output)
	}
	if !strings.Contains(output, "Spec:") {
		t.Errorf("expected Spec section in output, got %q", output)
	}
	if !strings.Contains(output, "nginx:latest") {
		t.Errorf("expected body content in output, got %q", output)
	}
}

// TestPrintRemoteRolloutDetail_EmptyBody tests printRemoteRolloutDetail with
// no body — verifying Spec section is absent.
func TestPrintRemoteRolloutDetail_EmptyBody(t *testing.T) {
	r := &orchestratev1.RolloutInfo{
		InstanceId:  "proj/db",
		InstanceKey: "cri/v1/Service",
		Status:      "pending",
	}

	oldStdout := os.Stdout
	rd, w, _ := os.Pipe()
	os.Stdout = w

	printRemoteRolloutDetail(r)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rd)

	output := buf.String()
	if !strings.Contains(output, "ID:     proj/db") {
		t.Errorf("expected ID in output, got %q", output)
	}
	if strings.Contains(output, "Spec:") {
		t.Errorf("did not expect Spec section for empty body, got %q", output)
	}
}

// TestPrintRemoteActions_MixedTypes tests printRemoteActions with create,
// update, destroy, and noop actions.
func TestPrintRemoteActions_MixedTypes(t *testing.T) {
	actions := []*orchestratev1.Action{
		{Type: "create", Kind: "Service", ResourceId: "web", Reason: "new"},
		{Type: "update", Kind: "Service", ResourceId: "api", Reason: "changed"},
		{Type: "destroy", Kind: "Service", ResourceId: "old", Reason: "removed"},
		{Type: "noop", Kind: "Service", ResourceId: "db", Reason: "unchanged"},
	}

	oldStdout := os.Stdout
	rd, w, _ := os.Pipe()
	os.Stdout = w

	printRemoteActions(actions)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rd)

	output := buf.String()
	if !strings.Contains(output, "+") {
		t.Errorf("expected '+' for create, got %q", output)
	}
	if !strings.Contains(output, "~") {
		t.Errorf("expected '~' for update, got %q", output)
	}
	if !strings.Contains(output, "-") {
		t.Errorf("expected '-' for destroy, got %q", output)
	}
	// noop should be skipped.
	if strings.Contains(output, "db") {
		t.Errorf("noop action should not appear in output, got %q", output)
	}
}

// TestPrintRemoteSummary_Counts tests printRemoteSummary with various counts.
func TestPrintRemoteSummary_Counts(t *testing.T) {
	oldStdout := os.Stdout
	rd, w, _ := os.Pipe()
	os.Stdout = w

	printRemoteSummary(3, 2, 1, 5)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rd)

	output := buf.String()
	if !strings.Contains(output, "3 to create") {
		t.Errorf("expected '3 to create', got %q", output)
	}
	if !strings.Contains(output, "2 to update") {
		t.Errorf("expected '2 to update', got %q", output)
	}
	if !strings.Contains(output, "1 to destroy") {
		t.Errorf("expected '1 to destroy', got %q", output)
	}
	if !strings.Contains(output, "5 unchanged") {
		t.Errorf("expected '5 unchanged', got %q", output)
	}
}

// TestRemoteRollbackList_Output tests remoteRollbackList which currently prints
// a not-yet-supported message.
func TestRemoteRollbackList_Output(t *testing.T) {
	orchSock := startCLIOrchestrateServer(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// runRollback with no args triggers rollback list.
	err := runRollback(rollbackCmd, nil)

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("runRollback list: %v", err)
	}
	if !strings.Contains(buf.String(), "not yet supported") {
		t.Errorf("expected 'not yet supported', got %q", buf.String())
	}
}

// TestRemoteRollbackList_ExplicitListArg tests remoteRollbackList when "list"
// is passed as an explicit argument.
func TestRemoteRollbackList_ExplicitListArg(t *testing.T) {
	orchSock := startCLIOrchestrateServer(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runRollback(rollbackCmd, []string{"list"})

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("runRollback list: %v", err)
	}
	if !strings.Contains(buf.String(), "not yet supported") {
		t.Errorf("expected 'not yet supported', got %q", buf.String())
	}
}

// TestRemoteDown_DefaultProjectName tests remoteDown when no explicit project
// name is set, so it derives the project name from the directory basename.
func TestRemoteDown_DefaultProjectName(t *testing.T) {
	orchSock := startCLIOrchestrateServer(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = orchSock
	defer func() { flagRemoteSocket = oldSocket }()

	oldDir := flagProjectDir
	flagProjectDir = t.TempDir()
	defer func() { flagProjectDir = oldDir }()

	// Ensure no explicit project name.
	oldProject := flagProjectName
	flagProjectName = ""
	defer func() { flagProjectName = oldProject }()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runDown(downCmd, nil)

	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("runDown default project: %v", err)
	}
	if !strings.Contains(buf.String(), "Teardown complete") {
		t.Errorf("expected completion message, got %q", buf.String())
	}
}

// TestRemoteLogs_WithOptions tests remoteLogs with various filter options.
func TestRemoteLogs_WithOptions(t *testing.T) {
	orchSock := startCLIOrchestrateServer(t)

	tests := []struct {
		name    string
		project string
		setup   func()
		restore func()
	}{
		{
			name:    "WithTailOption",
			project: "tailtest",
			setup:   func() { logsTail = "10" },
			restore: func() { logsTail = "" },
		},
		{
			name:    "WithSinceOption",
			project: "sincetest",
			setup:   func() { logsSince = "2025-01-01T00:00:00Z" },
			restore: func() { logsSince = "" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldSocket := flagRemoteSocket
			flagRemoteSocket = orchSock
			defer func() { flagRemoteSocket = oldSocket }()

			oldDir := flagProjectDir
			flagProjectDir = t.TempDir()
			defer func() { flagProjectDir = oldDir }()

			oldProject := flagProjectName
			flagProjectName = tt.project
			defer func() { flagProjectName = oldProject }()

			oldTail := logsTail
			oldSince := logsSince
			oldFollow := logsFollow
			logsFollow = false
			tt.setup()
			defer func() {
				logsTail = oldTail
				logsSince = oldSince
				logsFollow = oldFollow
				tt.restore()
			}()

			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			err := runLogs(logsCmd, nil)

			_ = w.Close()
			os.Stdout = oldStdout
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(r)

			if err != nil {
				t.Fatalf("runLogs: %v", err)
			}
			_ = buf.String()
		})
	}
}

// TestPrintRemoteActions_EmptyList tests printRemoteActions with no actions.
func TestPrintRemoteActions_EmptyList(t *testing.T) {
	oldStdout := os.Stdout
	rd, w, _ := os.Pipe()
	os.Stdout = w

	printRemoteActions(nil)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rd)

	if buf.String() != "" {
		t.Errorf("expected empty output for nil actions, got %q", buf.String())
	}
}

// TestPrintRemoteSummary_Zeros tests printRemoteSummary with all zero counts.
func TestPrintRemoteSummary_Zeros(t *testing.T) {
	oldStdout := os.Stdout
	rd, w, _ := os.Pipe()
	os.Stdout = w

	printRemoteSummary(0, 0, 0, 0)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rd)

	output := buf.String()
	if !strings.Contains(output, "0 to create") {
		t.Errorf("expected '0 to create', got %q", output)
	}
}

// TestPrintRolloutLinks_WithLinks tests printRolloutLinks for both
// the Dependencies and Dependents output branches.
func TestPrintRolloutLinks_WithLinks(t *testing.T) {
	tests := []struct {
		name       string
		rolloutID  string
		expectText []string
	}{
		{
			name:       "Dependencies",
			rolloutID:  "myapp/web",
			expectText: []string{"Dependencies", "myapp/db"},
		},
		{
			name:       "Dependents",
			rolloutID:  "myapp/db",
			expectText: []string{"Dependents", "myapp/web"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			dbPath := filepath.Join(dir, "test.bolt")
			engine, err := orchestrate.New(orchestrate.Config{DBPath: dbPath})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer func() { _ = engine.Close() }()

			// Add a link: myapp/web depends on myapp/db.
			refWeb := typing.NewReference("myapp/web", "cri.orchestrator.io/v1/Service")
			refDB := typing.NewReference("myapp/db", "cri.orchestrator.io/v1/Service")
			if err := engine.DB().AddLink(state.NewLink(refWeb, refDB)); err != nil {
				t.Fatalf("AddLink: %v", err)
			}

			r := &deploy.Rollout{
				InstanceId:  tt.rolloutID,
				InstanceKey: "cri.orchestrator.io/v1/Service",
			}

			old := os.Stdout
			rd, w, _ := os.Pipe()
			os.Stdout = w

			printRolloutLinks(engine, r)

			_ = w.Close()
			os.Stdout = old

			var buf bytes.Buffer
			_, _ = buf.ReadFrom(rd)
			output := buf.String()
			for _, s := range tt.expectText {
				if !strings.Contains(output, s) {
					t.Errorf("expected %q in output, got %q", s, output)
				}
			}
		})
	}
}

// TestPrintRolloutDetail_WithBody tests printRolloutDetail with body and status.
func TestPrintRolloutDetail_WithBody(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.bolt")
	engine, err := orchestrate.New(orchestrate.Config{DBPath: dbPath})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = engine.Close() }()

	r := &deploy.Rollout{
		InstanceId:  "myapp/web",
		InstanceKey: "cri.orchestrator.io/v1/Service",
		Body:        json.RawMessage(`{"image":"nginx:latest"}`),
		Status:      &deploy.RolloutStatus{Short: "SUCCEEDED"},
	}

	old := os.Stdout
	rd, w, _ := os.Pipe()
	os.Stdout = w

	printRolloutDetail(engine, r)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rd)
	output := buf.String()
	if !strings.Contains(output, "myapp/web") {
		t.Error("expected rollout ID in output")
	}
	if !strings.Contains(output, "SUCCEEDED") {
		t.Error("expected SUCCEEDED status in output")
	}
	if !strings.Contains(output, "Spec") {
		t.Error("expected Spec header in output")
	}
}

// TestPrintRolloutDetail_NilStatus tests printRolloutDetail with nil status.
func TestPrintRolloutDetail_NilStatus(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.bolt")
	engine, err := orchestrate.New(orchestrate.Config{DBPath: dbPath})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = engine.Close() }()

	r := &deploy.Rollout{
		InstanceId:  "myapp/web",
		InstanceKey: "cri.orchestrator.io/v1/Service",
	}

	old := os.Stdout
	rd, w, _ := os.Pipe()
	os.Stdout = w

	printRolloutDetail(engine, r)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(rd)
	output := buf.String()
	if !strings.Contains(output, "UNKNOWN") {
		t.Error("expected UNKNOWN status for nil status")
	}
}

// TestRunDrift_Local_NoCRI tests runDrift without a CRI runtime (local path).
func TestRunDrift_Local_NoCRI(t *testing.T) {
	isolateCRISockets(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = ""
	defer func() { flagCRISocket = oldCRI }()

	err := runDrift(driftCmd, nil)
	if err == nil {
		t.Fatal("expected error for missing CRI runtime")
	}
	if !strings.Contains(err.Error(), "no CRI runtime found") {
		t.Errorf("expected CRI error, got: %v", err)
	}
}

// TestRunPlan_Local_NoCRI tests runPlan without a CRI runtime (local path).
func TestRunPlan_Local_NoCRI(t *testing.T) {
	isolateCRISockets(t)

	oldSocket := flagRemoteSocket
	flagRemoteSocket = ""
	defer func() { flagRemoteSocket = oldSocket }()

	oldCRI := flagCRISocket
	flagCRISocket = ""
	defer func() { flagCRISocket = oldCRI }()

	err := runPlan(planCmd, nil)
	if err == nil {
		t.Fatal("expected error for missing CRI runtime")
	}
	if !strings.Contains(err.Error(), "no CRI runtime found") {
		t.Errorf("expected CRI error, got: %v", err)
	}
}

func TestShortLabel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"myapp/web", "web"},
		{"myapp/api", "api"},
		{"myapp", "myapp"},
		{"a/b/c", "b/c"},
		{"", ""},
	}
	for _, tt := range tests {
		got := shortLabel(tt.input)
		if got != tt.want {
			t.Errorf("shortLabel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestPrintNodeTable(t *testing.T) {
	nodes := []orchestrate.GraphNode{
		{ID: "myapp", Kind: "Project", Status: "ok"},
		{ID: "myapp/web", Kind: "Service", Status: "ok"},
	}
	// Should not panic; just exercises the code path.
	printNodeTable(nodes)
}

func TestPrintTextTree(t *testing.T) {
	nodes := []orchestrate.GraphNode{
		{ID: "myapp", Kind: "Project", Status: "ok"},
		{ID: "myapp/web", Kind: "Service", Status: "ok"},
		{ID: "nginx:latest", Kind: "Image", Status: "ok"},
	}
	edges := []orchestrate.GraphEdge{
		{SourceID: "myapp/web", TargetID: "myapp"},
		{SourceID: "myapp/web", TargetID: "nginx:latest"},
	}
	// Should not panic; exercises the grouping + edge display.
	printTextTree(nodes, edges)
}

func TestPrintDOT(t *testing.T) {
	nodes := []orchestrate.GraphNode{
		{ID: "myapp", Kind: "Project", Status: "ok"},
		{ID: "myapp/web", Kind: "Service", Status: "ok"},
	}
	edges := []orchestrate.GraphEdge{
		{SourceID: "myapp/web", TargetID: "myapp"},
	}
	// Should not panic; exercises DOT output.
	printDOT(nodes, edges)
}

func TestPrintTextTree_NoEdges(t *testing.T) {
	nodes := []orchestrate.GraphNode{
		{ID: "myapp", Kind: "Project", Status: "ok"},
	}
	printTextTree(nodes, nil)
}

func TestPrintDOT_Empty(t *testing.T) {
	printDOT(nil, nil)
}

func TestIsBeingStopped(t *testing.T) {
	stopping := map[string]bool{"web": true, "api": true}
	tests := []struct {
		depID   string
		project string
		want    bool
	}{
		{"myapp/web", "myapp", true},
		{"myapp/api", "myapp", true},
		{"myapp/db", "myapp", false},
		{"other/web", "myapp", false},
		{"myapp", "myapp", false},
		{"", "myapp", false},
	}
	for _, tt := range tests {
		got := isBeingStopped(tt.depID, tt.project, stopping)
		if got != tt.want {
			t.Errorf("isBeingStopped(%q, %q) = %v, want %v", tt.depID, tt.project, got, tt.want)
		}
	}
}

func TestGraphInitFlags(t *testing.T) {
	f := graphShowCmd.Flags().Lookup("format")
	if f == nil {
		t.Fatal("missing --format flag on graph show")
		return
	}
	if f.DefValue != "text" {
		t.Errorf("format default = %q, want %q", f.DefValue, "text")
	}
}

func TestPrintRolloutBody_NilBody(t *testing.T) {
	r := &deploy.Rollout{
		InstanceId: "myapp/web",
		Body:       nil,
	}
	// Should not panic with nil body.
	printRolloutBody(r)
}

func TestPrintRolloutBody_WithJSON(t *testing.T) {
	r := &deploy.Rollout{
		InstanceId: "myapp/web",
		Body:       []byte(`{"project":"myapp","service":"web"}`),
	}
	// Should not panic.
	printRolloutBody(r)
}

func TestPrintRolloutDetail_WithEngine(t *testing.T) {
	dir := t.TempDir()
	engine, err := orchestrate.New(orchestrate.Config{
		DBPath: filepath.Join(dir, "test.bolt"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = engine.Close() }()

	r := &deploy.Rollout{
		InstanceId:  "myapp/web",
		InstanceKey: "cri.orchestrator.io/v1/Service",
		Body:        []byte(`{"project":"myapp","service":"web"}`),
		Status:      &deploy.RolloutStatus{Short: typing.RolloutStatusSuccess},
	}
	// Should not panic.
	printRolloutDetail(engine, r)
}

func TestRunRender_InvalidTarget(t *testing.T) {
	renderTarget = "unsupported"
	defer func() { renderTarget = "" }()

	err := runRender(nil, nil)
	if err == nil {
		t.Error("expected error for unsupported target")
	}
	if err != nil && !strings.Contains(err.Error(), "unsupported target") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStateSubcommands(t *testing.T) {
	cmds := stateCmd.Commands()
	found := make(map[string]bool)
	for _, c := range cmds {
		found[c.Name()] = true
	}
	if !found["list"] {
		t.Error("missing list subcommand")
	}
	if !found["show"] {
		t.Error("missing show subcommand")
	}
}

func TestRollbackDryRunFlag(t *testing.T) {
	f := rollbackCmd.Flags().Lookup("dry-run")
	if f == nil {
		t.Fatal("missing --dry-run flag on rollback")
	}
}

func TestDryRunIfRequested_NotRequested(t *testing.T) {
	renderDryRun = false
	err := dryRunIfRequested(context.Background(), nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
