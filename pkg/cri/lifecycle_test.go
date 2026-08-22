package cri

import (
	"context"
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestServiceUp(t *testing.T) {
	sock, mock := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	svc := eval.Service{
		Image:       "nginx:latest",
		Ports:       []string{"8080:80"},
		Environment: map[string]string{"ENV": "test"},
	}

	opts := ServiceUpOptions{Project: "testproj", Version: "v1"}
	if err := c.ServiceUp(ctx, "web", svc, opts); err != nil {
		t.Fatalf("ServiceUp: %v", err)
	}

	// Verify pod was created with correct labels.
	pods, err := c.ListPodSandboxes(ctx, map[string]string{
		LabelProject: "testproj",
		LabelService: "web",
	})
	if err != nil {
		t.Fatalf("ListPodSandboxes: %v", err)
	}
	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}

	// Verify container was created and started.
	ctrs, err := c.ListContainers(ctx, pods[0].Id)
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
	if len(ctrs) != 1 {
		t.Fatalf("expected 1 container, got %d", len(ctrs))
	}

	// Container should be running.
	mock.mu.Lock()
	ctr := mock.containers[ctrs[0].Id]
	if ctr.Image.Image != "nginx:latest" {
		t.Errorf("image = %q, want nginx:latest", ctr.Image.Image)
	}
	mock.mu.Unlock()
}

func TestServiceUpIdempotent(t *testing.T) {
	sock, _ := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	svc := eval.Service{Image: "nginx:latest"}
	opts := ServiceUpOptions{Project: "testproj", Version: "v1"}

	// First call.
	if err := c.ServiceUp(ctx, "web", svc, opts); err != nil {
		t.Fatalf("ServiceUp (1st): %v", err)
	}

	// Second call should tear down old pod and create new one.
	if err := c.ServiceUp(ctx, "web", svc, opts); err != nil {
		t.Fatalf("ServiceUp (2nd): %v", err)
	}

	// Should still have exactly 1 pod.
	pods, err := c.ListPodSandboxes(ctx, map[string]string{
		LabelProject: "testproj",
		LabelService: "web",
	})
	if err != nil {
		t.Fatalf("ListPodSandboxes: %v", err)
	}
	if len(pods) != 1 {
		t.Errorf("expected 1 pod after idempotent up, got %d", len(pods))
	}
}

func TestProjectDown(t *testing.T) {
	sock, _ := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	opts := ServiceUpOptions{Project: "testproj", Version: "v1"}

	// Bring up two services.
	svc1 := eval.Service{Image: "nginx:latest"}
	svc2 := eval.Service{Image: "redis:latest"}
	if err := c.ServiceUp(ctx, "web", svc1, opts); err != nil {
		t.Fatalf("ServiceUp web: %v", err)
	}
	if err := c.ServiceUp(ctx, "cache", svc2, opts); err != nil {
		t.Fatalf("ServiceUp cache: %v", err)
	}

	// Verify 2 pods exist.
	pods, err := c.ListPodSandboxes(ctx, map[string]string{LabelProject: "testproj"})
	if err != nil {
		t.Fatalf("ListPodSandboxes: %v", err)
	}
	if len(pods) != 2 {
		t.Fatalf("expected 2 pods, got %d", len(pods))
	}

	// Tear down the project.
	if err := c.ProjectDown(ctx, "testproj", 10); err != nil {
		t.Fatalf("ProjectDown: %v", err)
	}

	// Verify all gone.
	pods, err = c.ListPodSandboxes(ctx, map[string]string{LabelProject: "testproj"})
	if err != nil {
		t.Fatalf("ListPodSandboxes: %v", err)
	}
	if len(pods) != 0 {
		t.Errorf("expected 0 pods after down, got %d", len(pods))
	}
}

func TestCompositionUp(t *testing.T) {
	sock, _ := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web":   {Image: "nginx:latest"},
			"api":   {Image: "node:18"},
			"cache": {Image: "redis:7"},
		},
	}

	opts := ServiceUpOptions{Project: "myapp", Version: "v1"}
	if err := c.CompositionUp(ctx, comp, opts); err != nil {
		t.Fatalf("CompositionUp: %v", err)
	}

	// Verify all 3 pods created.
	pods, err := c.ListPodSandboxes(ctx, map[string]string{LabelProject: "myapp"})
	if err != nil {
		t.Fatalf("ListPodSandboxes: %v", err)
	}
	if len(pods) != 3 {
		t.Errorf("expected 3 pods, got %d", len(pods))
	}

	// Each pod should have 1 container.
	for _, pod := range pods {
		ctrs, err := c.ListContainers(ctx, pod.Id)
		if err != nil {
			t.Fatalf("ListContainers: %v", err)
		}
		if len(ctrs) != 1 {
			t.Errorf("pod %s: expected 1 container, got %d", pod.Id, len(ctrs))
		}
	}
}

func TestServiceUp_WithVolumes(t *testing.T) {
	sock, mock := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	tmpDir := t.TempDir()
	svc := eval.Service{
		Image: "postgres:16",
		Volumes: []string{
			"pgdata:/var/lib/postgresql/data",
			"/host/conf:/etc/conf:ro",
		},
	}

	opts := ServiceUpOptions{
		Project:     "testproj",
		Version:     "v1",
		CompVolumes: map[string]eval.Volume{"pgdata": {}},
		VolumeResolver: func(_, name string) (string, error) {
			return tmpDir + "/" + name, nil
		},
	}

	if err := c.ServiceUp(ctx, "db", svc, opts); err != nil {
		t.Fatalf("ServiceUp: %v", err)
	}

	// Verify container was created.
	pods, err := c.ListPodSandboxes(ctx, map[string]string{
		LabelProject: "testproj",
		LabelService: "db",
	})
	if err != nil {
		t.Fatalf("ListPodSandboxes: %v", err)
	}
	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}

	ctrs, err := c.ListContainers(ctx, pods[0].Id)
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
	if len(ctrs) != 1 {
		t.Fatalf("expected 1 container, got %d", len(ctrs))
	}

	mock.mu.Lock()
	ctr := mock.containers[ctrs[0].Id]
	if ctr.Image.Image != "postgres:16" {
		t.Errorf("image = %q, want postgres:16", ctr.Image.Image)
	}
	mock.mu.Unlock()
}

func TestServiceUp_WithCNI(t *testing.T) {
	sock, mock := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	svc := eval.Service{Image: "nginx:latest"}
	opts := ServiceUpOptions{Project: "testproj", Version: "v1", UseCNI: true}
	if err := c.ServiceUp(ctx, "web", svc, opts); err != nil {
		t.Fatalf("ServiceUp: %v", err)
	}

	// Verify pod was created with NamespaceMode_POD.
	pods, err := c.ListPodSandboxes(ctx, map[string]string{
		LabelProject: "testproj",
		LabelService: "web",
	})
	if err != nil {
		t.Fatalf("ListPodSandboxes: %v", err)
	}
	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}

	mock.mu.Lock()
	podCfg := mock.podConfigs[pods[0].Id]
	mock.mu.Unlock()

	nsMode := podCfg.Linux.SecurityContext.NamespaceOptions.Network
	if nsMode != runtimev1.NamespaceMode_POD {
		t.Errorf("Network = %v, want POD", nsMode)
	}
}

func TestServiceUp_HostNetworkMode(t *testing.T) {
	sock, mock := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	// network_mode: host should force host networking even with UseCNI=true.
	svc := eval.Service{Image: "nginx:latest", NetworkMode: "host"}
	opts := ServiceUpOptions{Project: "testproj", Version: "v1", UseCNI: true}
	if err := c.ServiceUp(ctx, "web", svc, opts); err != nil {
		t.Fatalf("ServiceUp: %v", err)
	}

	pods, err := c.ListPodSandboxes(ctx, map[string]string{
		LabelProject: "testproj",
		LabelService: "web",
	})
	if err != nil {
		t.Fatalf("ListPodSandboxes: %v", err)
	}
	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}

	mock.mu.Lock()
	podCfg := mock.podConfigs[pods[0].Id]
	mock.mu.Unlock()

	nsMode := podCfg.Linux.SecurityContext.NamespaceOptions.Network
	if nsMode != runtimev1.NamespaceMode_NODE {
		t.Errorf("Network = %v, want NODE", nsMode)
	}
}

func assertServicePodCount(t *testing.T, c *Client, project, service string, want int) {
	t.Helper()
	pods, err := c.ListPodSandboxes(context.Background(), map[string]string{
		LabelProject: project,
		LabelService: service,
	})
	if err != nil {
		t.Fatalf("ListPodSandboxes for %s: %v", service, err)
	}
	if len(pods) != want {
		t.Errorf("expected %d %s pods, got %d", want, service, len(pods))
	}
}

func TestServiceDown(t *testing.T) {
	sock, _ := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	opts := ServiceUpOptions{Project: "testproj", Version: "v1"}

	if err := c.ServiceUp(ctx, "web", eval.Service{Image: "nginx:latest"}, opts); err != nil {
		t.Fatalf("ServiceUp web: %v", err)
	}
	if err := c.ServiceUp(ctx, "cache", eval.Service{Image: "redis:latest"}, opts); err != nil {
		t.Fatalf("ServiceUp cache: %v", err)
	}

	if err := c.ServiceDown(ctx, "testproj", "web", 10); err != nil {
		t.Fatalf("ServiceDown: %v", err)
	}

	assertServicePodCount(t, c, "testproj", "web", 0)
	assertServicePodCount(t, c, "testproj", "cache", 1)
}

func TestTeardownPod(t *testing.T) {
	sock, mock := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	svc := eval.Service{Image: "nginx:latest"}
	opts := ServiceUpOptions{Project: "teardown-test", Version: "v1"}
	if err := c.ServiceUp(ctx, "web", svc, opts); err != nil {
		t.Fatalf("ServiceUp: %v", err)
	}

	pods, err := c.ListPodSandboxes(ctx, map[string]string{LabelProject: "teardown-test"})
	if err != nil {
		t.Fatalf("ListPodSandboxes: %v", err)
	}
	require := func(cond bool, msg string) {
		t.Helper()
		if !cond {
			t.Fatal(msg)
		}
	}
	require(len(pods) == 1, "expected 1 pod")
	podID := pods[0].Id

	if err := c.teardownPod(ctx, podID, 10); err != nil {
		t.Fatalf("teardownPod: %v", err)
	}

	mock.mu.Lock()
	_, podExists := mock.pods[podID]
	mock.mu.Unlock()
	if podExists {
		t.Error("pod should have been removed after teardownPod")
	}
}

func TestTeardownPod_EmptyPod(t *testing.T) {
	sock, mock := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	// Create a pod with no containers.
	podConfig := &runtimev1.PodSandboxConfig{
		Metadata: &runtimev1.PodSandboxMetadata{
			Name: "empty-pod", Namespace: "default", Uid: "uid-empty",
		},
		Labels: map[string]string{LabelProject: "teardown-empty"},
	}
	podID, err := c.RunPodSandbox(ctx, podConfig)
	if err != nil {
		t.Fatalf("RunPodSandbox: %v", err)
	}

	// Tear down the pod with no containers — should succeed.
	if err := c.teardownPod(ctx, podID, 10); err != nil {
		t.Fatalf("teardownPod on empty pod: %v", err)
	}

	mock.mu.Lock()
	_, exists := mock.pods[podID]
	mock.mu.Unlock()
	if exists {
		t.Error("pod should have been removed")
	}
}

func TestResolveNetworkMode(t *testing.T) {
	tests := []struct {
		name        string
		networkMode string
		useCNI      bool
		want        PodNetworkMode
	}{
		{"default+CNI", "", true, PodNetworkCNI},
		{"default-CNI", "", false, PodNetworkHost},
		{"host+CNI", "host", true, PodNetworkHost},
		{"host-CNI", "host", false, PodNetworkHost},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := eval.Service{NetworkMode: tt.networkMode}
			got := resolveNetworkMode(svc, tt.useCNI)
			if got != tt.want {
				t.Errorf("resolveNetworkMode() = %v, want %v", got, tt.want)
			}
		})
	}
}
