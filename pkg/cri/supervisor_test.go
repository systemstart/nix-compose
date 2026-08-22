package cri

import (
	"context"
	"testing"
	"time"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// setupSupervisorEnv creates a mock CRI server, dials a client, and returns
// a ready-to-use SupervisorConfig plus the mock for state manipulation.
func setupSupervisorEnv(t *testing.T) (*Client, *fullMockCRI, SupervisorConfig) {
	t.Helper()
	sock, mock := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	cfg := SupervisorConfig{
		Client:  c,
		Project: "proj",
		Opts: ServiceUpOptions{
			Project: "proj",
			Version: "1",
		},
	}
	return c, mock, cfg
}

// startServiceForSupervisor starts a service via ServiceUp and returns the container ID.
func startServiceForSupervisor(t *testing.T, ctx context.Context, c *Client, mock *fullMockCRI, name string, svc eval.Service, opts ServiceUpOptions) string {
	t.Helper()
	if err := c.ServiceUp(ctx, name, svc, opts); err != nil {
		t.Fatalf("ServiceUp(%s): %v", name, err)
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	for id, ctr := range mock.containers {
		if ctr.Metadata != nil && ctr.Metadata.Name == name {
			return id
		}
	}
	t.Fatalf("container for %s not found after ServiceUp", name)
	return ""
}

func TestSupervisor_RestartOnFailure_ExitOne(t *testing.T) {
	c, mock, cfg := setupSupervisorEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	svc := eval.Service{Image: "nginx", Restart: "on-failure"}
	ctrID := startServiceForSupervisor(t, ctx, c, mock, "web", svc, cfg.Opts)

	sup := NewSupervisor(cfg)
	sup.Register("web", svc)

	go func() {
		time.Sleep(50 * time.Millisecond)
		mock.SetContainerExited(ctrID, 1)
	}()

	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	// Wait for restart: a new container ID for "web" appears.
	newID := waitForNewContainer(t, mock, "web", ctrID)
	// Let the new container exit cleanly so the watcher stops.
	mock.SetContainerExited(newID, 0)

	awaitSupervisorDone(t, done)
}

func TestSupervisor_RestartOnFailure_ExitZero(t *testing.T) {
	c, mock, cfg := setupSupervisorEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	svc := eval.Service{Image: "nginx", Restart: "on-failure"}
	ctrID := startServiceForSupervisor(t, ctx, c, mock, "web", svc, cfg.Opts)

	sup := NewSupervisor(cfg)
	sup.Register("web", svc)

	exitAndExpectStop(t, mock, sup, ctx, ctrID, 0, "exit 0 + on-failure policy")
}

func TestSupervisor_RestartAlways(t *testing.T) {
	c, mock, cfg := setupSupervisorEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	svc := eval.Service{Image: "nginx", Restart: "always"}
	ctrID := startServiceForSupervisor(t, ctx, c, mock, "web", svc, cfg.Opts)

	sup := NewSupervisor(cfg)
	sup.Register("web", svc)

	go func() {
		time.Sleep(50 * time.Millisecond)
		mock.SetContainerExited(ctrID, 0)
	}()

	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	// Wait for at least one restart, then cancel.
	_ = waitForNewContainer(t, mock, "web", ctrID)
	cancel()

	awaitSupervisorDone(t, done)
}

func TestSupervisor_RestartNo(t *testing.T) {
	c, mock, cfg := setupSupervisorEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	svc := eval.Service{Image: "nginx", Restart: "no"}
	ctrID := startServiceForSupervisor(t, ctx, c, mock, "web", svc, cfg.Opts)

	sup := NewSupervisor(cfg)
	sup.Register("web", svc)

	exitAndExpectStop(t, mock, sup, ctx, ctrID, 1, "policy is no")
}

// exitAndExpectStop triggers a container exit and asserts the supervisor stops without restarting.
func exitAndExpectStop(t *testing.T, mock *fullMockCRI, sup *Supervisor, ctx context.Context, ctrID string, exitCode int32, reason string) {
	t.Helper()
	go func() {
		time.Sleep(50 * time.Millisecond)
		mock.SetContainerExited(ctrID, exitCode)
	}()

	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	awaitSupervisorDone(t, done)
}

// waitForNewContainer polls until a container named serviceName with a different ID appears.
func waitForNewContainer(t *testing.T, mock *fullMockCRI, serviceName, oldID string) string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for restart of %s", serviceName)
		default:
		}
		mock.mu.Lock()
		var newID string
		for id, ctr := range mock.containers {
			if ctr.Metadata != nil && ctr.Metadata.Name == serviceName {
				newID = id
			}
		}
		mock.mu.Unlock()
		if newID != "" && newID != oldID {
			return newID
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// awaitSupervisorDone waits for the supervisor to finish without error.
func awaitSupervisorDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for supervisor to finish")
	}
}

func TestSupervisor_UnlessStopped_WithStop(t *testing.T) {
	c, mock, cfg := setupSupervisorEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	svc := eval.Service{Image: "nginx", Restart: "unless-stopped"}
	ctrID := startServiceForSupervisor(t, ctx, c, mock, "web", svc, cfg.Opts)

	sup := NewSupervisor(cfg)
	sup.Register("web", svc)

	// Mark as stopped by user before the container exits.
	sup.Stop()

	go func() {
		time.Sleep(50 * time.Millisecond)
		mock.SetContainerExited(ctrID, 0)
	}()

	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out — supervisor should have stopped since user stopped")
	}
}

func TestSupervisor_ContextCancel(t *testing.T) {
	c, mock, cfg := setupSupervisorEnv(t)
	ctx, cancel := context.WithCancel(context.Background())

	svc := eval.Service{Image: "nginx", Restart: "always"}
	_ = startServiceForSupervisor(t, ctx, c, mock, "web", svc, cfg.Opts)

	sup := NewSupervisor(cfg)
	sup.Register("web", svc)

	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	// Give the supervisor a moment to start watching, then cancel.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out — goroutines should exit promptly on cancel")
	}
}
