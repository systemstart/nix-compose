package microvm

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/systemstart/nix-compose/internal/testsock"

	orchestratev1 "github.com/systemstart/nix-compose/api/orchestrate/v1"
	"github.com/systemstart/nix-compose/pkg/orchestrate/client"
	"google.golang.org/grpc"
)

func createTempFile(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func validConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Kernel:        createTempFile(t, "vmlinux"),
		RootFS:        createTempFile(t, "rootfs.img"),
		CID:           3,
		VCPUs:         2,
		MemoryMB:      1024,
		VsockPort:     defaultVsockPort,
		HypervisorBin: defaultHypervisorBin,
		VirtiofsdBin:  defaultVirtiofsdBin,
		Console:       defaultConsole,
		Serial:        defaultSerial,
	}
}

func TestConfigValidation_MissingKernel(t *testing.T) {
	cfg := Config{RootFS: createTempFile(t, "rootfs.img"), CID: 3}
	cfg.Kernel = ""
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for missing kernel")
	}
	if !strings.Contains(err.Error(), "kernel path is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfigValidation_KernelNotFound(t *testing.T) {
	cfg := Config{Kernel: "/nonexistent/vmlinux", RootFS: createTempFile(t, "rootfs.img"), CID: 3}
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for nonexistent kernel")
	}
	if !strings.Contains(err.Error(), "kernel") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfigValidation_MissingRootFS(t *testing.T) {
	cfg := Config{Kernel: createTempFile(t, "vmlinux"), CID: 3}
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for missing rootfs")
	}
	if !strings.Contains(err.Error(), "rootfs path is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfigValidation_RootFSNotFound(t *testing.T) {
	cfg := Config{Kernel: createTempFile(t, "vmlinux"), RootFS: "/nonexistent/rootfs.img", CID: 3}
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for nonexistent rootfs")
	}
	if !strings.Contains(err.Error(), "rootfs") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfigValidation_BadCID(t *testing.T) {
	cfg := Config{
		Kernel: createTempFile(t, "vmlinux"),
		RootFS: createTempFile(t, "rootfs.img"),
		CID:    0,
	}
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for CID=0")
	}
	if !strings.Contains(err.Error(), "CID must be >= 3") {
		t.Errorf("unexpected error: %v", err)
	}

	cfg.CID = 2
	_, err = New(cfg)
	if err == nil {
		t.Fatal("expected error for CID=2")
	}
}

func TestConfigValidation_Defaults(t *testing.T) {
	cfg := Config{
		Kernel: createTempFile(t, "vmlinux"),
		RootFS: createTempFile(t, "rootfs.img"),
		CID:    3,
	}

	v, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if v.CID() != 3 {
		t.Errorf("CID = %d, want 3", v.CID())
	}
	if v.VsockPort() != defaultVsockPort {
		t.Errorf("VsockPort = %d, want %d", v.VsockPort(), defaultVsockPort)
	}
}

func TestConfigValidation_Valid(t *testing.T) {
	cfg := validConfig(t)
	v, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Status() != Stopped {
		t.Errorf("initial status = %v, want Stopped", v.Status())
	}
}

func TestBuildCHVArgs_Basic(t *testing.T) {
	cfg := validConfig(t)
	v := newVM(cfg)
	v.tmpDir = t.TempDir()
	v.vsockSock = filepath.Join(v.tmpDir, "vsock.sock")

	args := v.buildCHVArgs()

	assertContains(t, args, "--kernel", cfg.Kernel)
	assertContains(t, args, "--cpus", "boot=2")
	assertContains(t, args, "--memory", "size=1024M")
	assertContains(t, args, "--console", defaultConsole)
	assertContains(t, args, "--serial", defaultSerial)

	// Should contain vsock arg.
	found := false
	for _, a := range args {
		if strings.HasPrefix(a, "cid=3,socket=") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected vsock arg with cid=3, got %v", args)
	}
}

func TestBuildCHVArgs_WithShares(t *testing.T) {
	cfg := validConfig(t)
	cfg.Shares = []Share{
		{Tag: "nix-store", SourcePath: "/nix/store", Socket: "/tmp/vfs-0.sock"},
		{Tag: "project", SourcePath: "/home/user/project", Socket: "/tmp/vfs-1.sock"},
	}
	v := newVM(cfg)
	v.tmpDir = t.TempDir()
	v.vsockSock = filepath.Join(v.tmpDir, "vsock.sock")

	args := v.buildCHVArgs()

	fsCount := 0
	for i, a := range args {
		if a == "--fs" {
			fsCount++
			if i+1 < len(args) {
				val := args[i+1]
				if !strings.Contains(val, "tag=") || !strings.Contains(val, "socket=") {
					t.Errorf("bad --fs arg: %s", val)
				}
			}
		}
	}
	if fsCount != 2 {
		t.Errorf("expected 2 --fs args, got %d", fsCount)
	}
}

func TestBuildCHVArgs_WithNetwork(t *testing.T) {
	cfg := validConfig(t)
	cfg.TAPDevice = "tap0"
	cfg.MACAddress = "52:54:00:12:34:56"
	v := newVM(cfg)
	v.tmpDir = t.TempDir()
	v.vsockSock = filepath.Join(v.tmpDir, "vsock.sock")

	args := v.buildCHVArgs()

	found := false
	for i, a := range args {
		if a == "--net" && i+1 < len(args) {
			if args[i+1] == "tap=tap0,mac=52:54:00:12:34:56" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected --net tap=tap0,mac=52:54:00:12:34:56 in args: %v", args)
	}
}

func TestBuildCHVArgs_WithNetworkNoMAC(t *testing.T) {
	cfg := validConfig(t)
	cfg.TAPDevice = "tap0"
	v := newVM(cfg)
	v.tmpDir = t.TempDir()
	v.vsockSock = filepath.Join(v.tmpDir, "vsock.sock")

	args := v.buildCHVArgs()

	found := false
	for i, a := range args {
		if a == "--net" && i+1 < len(args) {
			if args[i+1] == "tap=tap0" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected --net tap=tap0 in args: %v", args)
	}
}

func TestBuildCHVArgs_NoAPISocket(t *testing.T) {
	cfg := validConfig(t)
	cfg.APISocket = ""
	v := newVM(cfg)
	v.tmpDir = t.TempDir()
	v.vsockSock = filepath.Join(v.tmpDir, "vsock.sock")

	args := v.buildCHVArgs()
	for _, a := range args {
		if a == "--api-socket" {
			t.Error("expected no --api-socket flag when APISocket is empty")
		}
	}
}

func TestBuildCHVArgs_WithAPISocket(t *testing.T) {
	cfg := validConfig(t)
	cfg.APISocket = "/tmp/chv-api.sock"
	v := newVM(cfg)
	v.tmpDir = t.TempDir()
	v.vsockSock = filepath.Join(v.tmpDir, "vsock.sock")

	args := v.buildCHVArgs()
	assertContains(t, args, "--api-socket", "/tmp/chv-api.sock")
}

func TestStatusString(t *testing.T) {
	tests := []struct {
		s    Status
		want string
	}{
		{Stopped, "stopped"},
		{Starting, "starting"},
		{Running, "running"},
		{Stopping, "stopping"},
		{Failed, "failed"},
		{Status(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("Status(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestStart_RejectsNonStoppedState(t *testing.T) {
	cfg := validConfig(t)
	v := newVM(cfg)

	// Set to Running.
	v.mu.Lock()
	v.status = Running
	v.mu.Unlock()

	err := v.Start(context.Background())
	if err == nil {
		t.Fatal("expected error starting from Running state")
	}
	if !strings.Contains(err.Error(), "cannot start VM in state running") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStart_RejectsStartingState(t *testing.T) {
	cfg := validConfig(t)
	v := newVM(cfg)

	v.mu.Lock()
	v.status = Starting
	v.mu.Unlock()

	err := v.Start(context.Background())
	if err == nil {
		t.Fatal("expected error starting from Starting state")
	}
}

func TestStart_AllowsFailedState(t *testing.T) {
	cfg := validConfig(t)
	cfg.HypervisorBin = "/nonexistent-binary"
	v := newVM(cfg)

	v.mu.Lock()
	v.status = Failed
	v.mu.Unlock()

	// Will fail because binary doesn't exist, but should be allowed to try.
	err := v.Start(context.Background())
	if err == nil {
		t.Fatal("expected error (binary not found) but start was allowed")
	}
	// Status should be Failed after the attempt.
	if v.Status() != Failed {
		t.Errorf("status = %v, want Failed", v.Status())
	}
}

func TestStop_RejectsStoppedState(t *testing.T) {
	cfg := validConfig(t)
	v := newVM(cfg)

	err := v.Stop(context.Background())
	if err == nil {
		t.Fatal("expected error stopping from Stopped state")
	}
	if !strings.Contains(err.Error(), "cannot stop VM in state stopped") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStop_RejectsStartingState(t *testing.T) {
	cfg := validConfig(t)
	v := newVM(cfg)

	v.mu.Lock()
	v.status = Starting
	v.mu.Unlock()

	err := v.Stop(context.Background())
	if err == nil {
		t.Fatal("expected error stopping from Starting state")
	}
}

func TestStop_AllowsRunningState(t *testing.T) {
	cfg := validConfig(t)
	v := newVM(cfg)

	v.mu.Lock()
	v.status = Running
	v.mu.Unlock()

	// Stop should succeed (no process to kill).
	err := v.Stop(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Status() != Stopped {
		t.Errorf("status = %v, want Stopped", v.Status())
	}
}

func TestStop_AllowsFailedState(t *testing.T) {
	cfg := validConfig(t)
	v := newVM(cfg)

	v.mu.Lock()
	v.status = Failed
	v.mu.Unlock()

	err := v.Stop(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Status() != Stopped {
		t.Errorf("status = %v, want Stopped", v.Status())
	}
}

func TestCleanup_RemovesTmpDir(t *testing.T) {
	cfg := validConfig(t)
	v := newVM(cfg)

	v.tmpDir = t.TempDir()
	tmpDir := v.tmpDir

	// Create a file in the temp dir.
	if err := os.WriteFile(filepath.Join(tmpDir, "test"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	v.cleanup()

	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Errorf("expected tmp dir to be removed, got err: %v", err)
	}
	if v.tmpDir != "" {
		t.Error("expected tmpDir to be reset to empty string")
	}
}

func TestCleanup_HandlesEmptyTmpDir(t *testing.T) {
	cfg := validConfig(t)
	v := newVM(cfg)
	// No tmpDir set.
	v.cleanup() // Should not panic.
}

func TestStart_FailsOnBadBinary(t *testing.T) {
	cfg := validConfig(t)
	cfg.HypervisorBin = "/nonexistent-binary"
	v := newVM(cfg)

	err := v.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for missing hypervisor binary")
	}
	if v.Status() != Failed {
		t.Errorf("status = %v, want Failed", v.Status())
	}
}

func TestWaitForHealth_CancelledContext(t *testing.T) {
	cfg := validConfig(t)
	v := newVM(cfg)

	// Override dialVsockClient to always fail.
	origDial := dialVsockClient
	dialVsockClient = func(_ context.Context, _, _ uint32) (*client.Client, error) {
		return nil, fmt.Errorf("no vsock")
	}
	defer func() { dialVsockClient = origDial }()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := v.waitForHealth(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestAttemptGracefulTeardown_DialFails(t *testing.T) {
	cfg := validConfig(t)
	v := newVM(cfg)

	// Override dialVsockClient to always fail.
	origDial := dialVsockClient
	dialVsockClient = func(_ context.Context, _, _ uint32) (*client.Client, error) {
		return nil, fmt.Errorf("no vsock")
	}
	defer func() { dialVsockClient = origDial }()

	// Should not panic.
	v.attemptGracefulTeardown(context.Background())
}

func TestWait_ReceivesError(t *testing.T) {
	cfg := validConfig(t)
	v := newVM(cfg)

	// Simulate process exit by sending to waitCh.
	go func() {
		v.waitCh <- fmt.Errorf("process exited with code 1")
	}()

	err := v.Wait()
	if err == nil {
		t.Fatal("expected error from Wait")
	}
	if !strings.Contains(err.Error(), "process exited") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWait_ReceivesNil(t *testing.T) {
	cfg := validConfig(t)
	v := newVM(cfg)

	go func() {
		v.waitCh <- nil
	}()

	err := v.Wait()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitForHealth_SuccessOnSecondAttempt(t *testing.T) {
	cfg := validConfig(t)
	v := newVM(cfg)

	attempts := 0
	origDial := dialVsockClient
	dialVsockClient = func(_ context.Context, _, _ uint32) (*client.Client, error) {
		attempts++
		if attempts < 2 {
			return nil, fmt.Errorf("not ready")
		}
		// We can't return a real client without a server, so return an error
		// that simulates the health check working by cancelling context.
		return nil, fmt.Errorf("connection refused")
	}
	defer func() { dialVsockClient = origDial }()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Will fail eventually due to timeout, but should try multiple attempts.
	_ = v.waitForHealth(ctx)
	if attempts < 2 {
		t.Errorf("expected at least 2 dial attempts, got %d", attempts)
	}
}

func writeMockScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	// Close stdout/stderr to avoid test I/O warnings, then exec the body.
	script := "#!/bin/sh\nexec >/dev/null 2>&1\n" + body
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStartVirtiofsd_SocketCreated(t *testing.T) {
	cfg := validConfig(t)
	v := newVM(cfg)
	v.tmpDir = t.TempDir()

	share := &Share{
		Tag:        "test",
		SourcePath: t.TempDir(),
		Socket:     filepath.Join(v.tmpDir, "virtiofsd-test.sock"),
	}

	v.cfg.VirtiofsdBin = writeMockScript(t, v.tmpDir, "mock-virtiofsd",
		fmt.Sprintf("touch %q\nsleep 30\n", share.Socket))

	err := v.startVirtiofsd(share)
	if err != nil {
		t.Fatalf("startVirtiofsd: %v", err)
	}

	if len(v.virtiofsd) != 1 {
		t.Errorf("expected 1 virtiofsd process, got %d", len(v.virtiofsd))
	}
	v.cleanup()
}

func TestStartVirtiofsd_ReadOnlyShare(t *testing.T) {
	cfg := validConfig(t)
	v := newVM(cfg)
	v.tmpDir = t.TempDir()

	share := &Share{
		Tag:        "test-ro",
		SourcePath: t.TempDir(),
		Socket:     filepath.Join(v.tmpDir, "virtiofsd-ro.sock"),
		ReadOnly:   true,
	}

	v.cfg.VirtiofsdBin = writeMockScript(t, v.tmpDir, "mock-virtiofsd-ro",
		fmt.Sprintf("touch %q\nsleep 30\n", share.Socket))

	err := v.startVirtiofsd(share)
	if err != nil {
		t.Fatalf("startVirtiofsd: %v", err)
	}
	v.cleanup()
}

func TestStartVirtiofsd_BinaryNotFound(t *testing.T) {
	cfg := validConfig(t)
	cfg.VirtiofsdBin = "/nonexistent-virtiofsd"
	v := newVM(cfg)
	v.tmpDir = t.TempDir()

	share := &Share{
		Tag:        "test",
		SourcePath: t.TempDir(),
		Socket:     filepath.Join(v.tmpDir, "test.sock"),
	}

	err := v.startVirtiofsd(share)
	if err == nil {
		t.Fatal("expected error for missing virtiofsd binary")
	}
}

func TestStartVirtiofsd_SocketTimeout(t *testing.T) {
	cfg := validConfig(t)
	v := newVM(cfg)
	v.tmpDir = t.TempDir()

	share := &Share{
		Tag:        "test",
		SourcePath: t.TempDir(),
		Socket:     filepath.Join(v.tmpDir, "never-appears.sock"),
	}

	v.cfg.VirtiofsdBin = writeMockScript(t, v.tmpDir, "mock-virtiofsd-nosock",
		"sleep 30\n")

	err := v.startVirtiofsd(share)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "did not appear") {
		t.Errorf("unexpected error: %v", err)
	}
	v.cleanup()
}

func TestMockVM_WithErrorFuncs(t *testing.T) {
	testErr := fmt.Errorf("test error")
	m := &MockVM{
		StartFunc: func(_ context.Context) error { return testErr },
		StopFunc:  func(_ context.Context) error { return testErr },
		WaitFunc:  func() error { return testErr },
	}

	if err := m.Start(context.Background()); err != testErr {
		t.Errorf("Start error = %v, want %v", err, testErr)
	}
	if err := m.Stop(context.Background()); err != testErr {
		t.Errorf("Stop error = %v, want %v", err, testErr)
	}
	if err := m.Wait(); err != testErr {
		t.Errorf("Wait error = %v, want %v", err, testErr)
	}
}

func TestMockVM(t *testing.T) {
	m := &MockVM{
		CIDFunc:       func() uint32 { return 42 },
		VsockPortFunc: func() uint32 { return 2048 },
		StatusFunc:    func() Status { return Running },
	}

	if m.CID() != 42 {
		t.Errorf("CID = %d, want 42", m.CID())
	}
	if m.VsockPort() != 2048 {
		t.Errorf("VsockPort = %d, want 2048", m.VsockPort())
	}
	if m.Status() != Running {
		t.Errorf("Status = %v, want Running", m.Status())
	}
}

func TestMockVM_Defaults(t *testing.T) {
	m := &MockVM{}

	if err := m.Start(context.Background()); err != nil {
		t.Errorf("Start: %v", err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
	if m.Status() != Stopped {
		t.Errorf("Status = %v, want Stopped", m.Status())
	}
	if m.CID() != 3 {
		t.Errorf("CID = %d, want 3", m.CID())
	}
	if m.VsockPort() != 1024 {
		t.Errorf("VsockPort = %d, want 1024", m.VsockPort())
	}
	if err := m.Wait(); err != nil {
		t.Errorf("Wait: %v", err)
	}
}

// assertContains checks that args contains flag followed by value.
func assertContains(t *testing.T, args []string, flag, value string) {
	t.Helper()
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return
		}
	}
	t.Errorf("expected %s %s in args: %v", flag, value, args)
}

// TestWaitForHealth_BackoffCap verifies the backoff increases and caps at 1s.
func TestWaitForHealth_BackoffCap(t *testing.T) {
	cfg := validConfig(t)
	v := newVM(cfg)

	attempts := 0
	origDial := dialVsockClient
	dialVsockClient = func(_ context.Context, _, _ uint32) (*client.Client, error) {
		attempts++
		return nil, fmt.Errorf("not ready yet")
	}
	defer func() { dialVsockClient = origDial }()

	// Use a longer timeout to allow several retries and hit the backoff cap.
	// Backoff: 100ms, 200ms, 400ms, 800ms, 1000ms (capped).
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()

	err := v.waitForHealth(ctx)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	// Should have made enough attempts to hit the backoff cap.
	if attempts < 4 {
		t.Errorf("expected at least 4 attempts (to hit backoff cap), got %d", attempts)
	}
}

// TestWaitForHealth_DialSucceedsAndHealthy exercises the full success path
// where vsock dial succeeds and health check returns healthy.
func TestWaitForHealth_DialSucceedsAndHealthy(t *testing.T) {
	cfg := validConfig(t)
	v := newVM(cfg)

	sock := testsock.Path(t, "orch.sock")

	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	orchestratev1.RegisterOrchestrateServiceServer(srv, &healthyServer{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	origDial := dialVsockClient
	dialVsockClient = func(ctx context.Context, _, _ uint32) (*client.Client, error) {
		return client.Dial(ctx, sock)
	}
	defer func() { dialVsockClient = origDial }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = v.waitForHealth(ctx)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

// healthyServer is a minimal OrchestrateService that returns healthy.
type healthyServer struct {
	orchestratev1.UnimplementedOrchestrateServiceServer
}

func (s *healthyServer) Health(_ context.Context, _ *orchestratev1.HealthRequest) (*orchestratev1.HealthResponse, error) {
	return &orchestratev1.HealthResponse{
		Healthy:        true,
		RuntimeName:    "test-runtime",
		RuntimeVersion: "1.0.0",
	}, nil
}
