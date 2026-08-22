package health

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// mockExecutor implements ContainerExecutor for tests.
type mockExecutor struct {
	exitCode int32
	err      error
}

func (m *mockExecutor) ExecSync(_ context.Context, _ string, _ []string, _ int64) (*ExecResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &ExecResult{ExitCode: m.exitCode}, nil
}

func TestMonitor_ExecProbeHealthy(t *testing.T) {
	exec := &mockExecutor{exitCode: 0}
	mon := NewMonitor(exec)

	cfg := &ProbeConfig{
		Type:             ProbeExec,
		ExecCommand:      []string{"true"},
		Interval:         10 * time.Millisecond,
		Timeout:          5 * time.Second,
		FailureThreshold: 3,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	mon.Register("web", "ctr-1", "", cfg)
	mon.Start(ctx, "web")

	if err := mon.WaitHealthy(ctx, "web"); err != nil {
		t.Fatalf("WaitHealthy: %v", err)
	}

	if s := mon.State("web"); s != StateHealthy {
		t.Errorf("state = %d, want StateHealthy", s)
	}
}

func TestMonitor_ExecProbeUnhealthy(t *testing.T) {
	exec := &mockExecutor{exitCode: 1}
	mon := NewMonitor(exec)

	cfg := &ProbeConfig{
		Type:             ProbeExec,
		ExecCommand:      []string{"false"},
		Interval:         10 * time.Millisecond,
		Timeout:          5 * time.Second,
		FailureThreshold: 2,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	mon.Register("web", "ctr-1", "", cfg)
	mon.Start(ctx, "web")

	// Wait a bit for failures to accumulate.
	time.Sleep(100 * time.Millisecond)

	if s := mon.State("web"); s != StateUnhealthy {
		t.Errorf("state = %d, want StateUnhealthy", s)
	}
}

func TestMonitor_WaitHealthy_Success(t *testing.T) {
	exec := &mockExecutor{exitCode: 0}
	mon := NewMonitor(exec)

	cfg := &ProbeConfig{
		Type:             ProbeExec,
		ExecCommand:      []string{"true"},
		Interval:         10 * time.Millisecond,
		Timeout:          5 * time.Second,
		FailureThreshold: 3,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	mon.Register("web", "ctr-1", "", cfg)
	mon.Start(ctx, "web")

	err := mon.WaitHealthy(ctx, "web")
	if err != nil {
		t.Fatalf("WaitHealthy: %v", err)
	}
}

func TestMonitor_WaitHealthy_Timeout(t *testing.T) {
	exec := &mockExecutor{exitCode: 1}
	mon := NewMonitor(exec)

	cfg := &ProbeConfig{
		Type:             ProbeExec,
		ExecCommand:      []string{"false"},
		Interval:         10 * time.Millisecond,
		Timeout:          5 * time.Second,
		FailureThreshold: 100,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	mon.Register("web", "ctr-1", "", cfg)
	mon.Start(ctx, "web")

	err := mon.WaitHealthy(ctx, "web")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestMonitor_InitialDelay(t *testing.T) {
	exec := &mockExecutor{exitCode: 0}
	mon := NewMonitor(exec)

	cfg := &ProbeConfig{
		Type:             ProbeExec,
		ExecCommand:      []string{"true"},
		Interval:         10 * time.Millisecond,
		Timeout:          5 * time.Second,
		InitialDelay:     50 * time.Millisecond,
		FailureThreshold: 3,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	mon.Register("web", "ctr-1", "", cfg)

	// Before starting, state should be Starting.
	if s := mon.State("web"); s != StateStarting {
		t.Errorf("state before start = %d, want StateStarting", s)
	}

	mon.Start(ctx, "web")

	// Immediately after start, should still be starting (initial delay).
	if s := mon.State("web"); s != StateStarting {
		t.Errorf("state immediately after start = %d, want StateStarting", s)
	}

	// After waiting, should be healthy.
	if err := mon.WaitHealthy(ctx, "web"); err != nil {
		t.Fatalf("WaitHealthy: %v", err)
	}
}

func TestMonitor_FailureThreshold(t *testing.T) {
	exec := &mockExecutor{exitCode: 1}
	mon := NewMonitor(exec)

	cfg := &ProbeConfig{
		Type:             ProbeExec,
		ExecCommand:      []string{"false"},
		Interval:         10 * time.Millisecond,
		Timeout:          5 * time.Second,
		FailureThreshold: 3,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	mon.Register("web", "ctr-1", "", cfg)
	mon.Start(ctx, "web")

	// After 1 interval, should not be unhealthy yet (threshold=3).
	time.Sleep(15 * time.Millisecond)
	if s := mon.State("web"); s == StateUnhealthy {
		t.Error("should not be unhealthy after only 1 probe")
	}

	// Wait long enough for threshold to be reached.
	time.Sleep(60 * time.Millisecond)
	if s := mon.State("web"); s != StateUnhealthy {
		t.Errorf("state = %d, want StateUnhealthy after threshold reached", s)
	}
}

func TestMonitor_HTTPProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Parse host and port from test server.
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	exec := &mockExecutor{} // not used for HTTP probes
	mon := NewMonitor(exec)

	portNum := 0
	if _, err := fmt.Sscanf(port, "%d", &portNum); err != nil {
		t.Fatalf("parsing port: %v", err)
	}

	cfg := &ProbeConfig{
		Type:             ProbeHTTP,
		HTTPScheme:       "http",
		HTTPPort:         portNum,
		HTTPPath:         "/",
		Interval:         10 * time.Millisecond,
		Timeout:          5 * time.Second,
		FailureThreshold: 3,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	mon.Register("web", "ctr-1", host, cfg)
	mon.Start(ctx, "web")

	if err := mon.WaitHealthy(ctx, "web"); err != nil {
		t.Fatalf("WaitHealthy: %v", err)
	}
}

func TestMonitor_TCPProbe(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = lis.Close() }()

	_, port, _ := net.SplitHostPort(lis.Addr().String())
	portNum := 0
	if _, err := fmt.Sscanf(port, "%d", &portNum); err != nil {
		t.Fatalf("parsing port: %v", err)
	}

	exec := &mockExecutor{}
	mon := NewMonitor(exec)

	cfg := &ProbeConfig{
		Type:             ProbeTCP,
		TCPPort:          portNum,
		Interval:         10 * time.Millisecond,
		Timeout:          5 * time.Second,
		FailureThreshold: 3,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	mon.Register("web", "ctr-1", "127.0.0.1", cfg)
	mon.Start(ctx, "web")

	if err := mon.WaitHealthy(ctx, "web"); err != nil {
		t.Fatalf("WaitHealthy: %v", err)
	}
}

func TestMonitor_UnregisteredService(t *testing.T) {
	mon := NewMonitor(&mockExecutor{})

	ctx := context.Background()
	err := mon.WaitHealthy(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unregistered service")
	}
}
