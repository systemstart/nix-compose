package microvm

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type vm struct {
	cfg    Config
	mu     sync.Mutex
	status Status

	tmpDir    string
	chvCmd    *exec.Cmd
	virtiofsd []*exec.Cmd
	vsockSock string // vsock UDS path for cloud-hypervisor
	waitCh    chan error
}

func newVM(cfg Config) *vm {
	return &vm{
		cfg:    cfg,
		status: Stopped,
		waitCh: make(chan error, 1),
	}
}

func (v *vm) Status() Status {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.status
}

func (v *vm) CID() uint32 {
	return v.cfg.CID
}

func (v *vm) VsockPort() uint32 {
	return v.cfg.VsockPort
}

func (v *vm) Start(ctx context.Context) error {
	v.mu.Lock()
	if v.status != Stopped && v.status != Failed {
		v.mu.Unlock()
		return fmt.Errorf("microvm: cannot start VM in state %s", v.status)
	}
	v.status = Starting
	v.mu.Unlock()

	if err := v.start(ctx); err != nil {
		v.cleanup()
		v.mu.Lock()
		v.status = Failed
		v.mu.Unlock()
		return err
	}

	v.mu.Lock()
	v.status = Running
	v.mu.Unlock()
	return nil
}

func (v *vm) start(ctx context.Context) error {
	var err error
	v.tmpDir, err = os.MkdirTemp("", "microvm-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}

	v.vsockSock = filepath.Join(v.tmpDir, "vsock.sock")

	// Start virtiofsd for each share.
	for i := range v.cfg.Shares {
		share := &v.cfg.Shares[i]
		share.Socket = filepath.Join(v.tmpDir, fmt.Sprintf("virtiofsd-%d.sock", i))

		if err := v.startVirtiofsd(share); err != nil {
			return fmt.Errorf("starting virtiofsd for share %q: %w", share.Tag, err)
		}
	}

	// Build and start cloud-hypervisor.
	args := v.buildCHVArgs()
	log.Printf("microvm: starting cloud-hypervisor: %s %v", v.cfg.HypervisorBin, args)

	v.chvCmd = exec.CommandContext(ctx, v.cfg.HypervisorBin, args...)
	v.chvCmd.Stdout = os.Stdout
	v.chvCmd.Stderr = os.Stderr

	if err := v.chvCmd.Start(); err != nil {
		return fmt.Errorf("starting cloud-hypervisor: %w", err)
	}

	// Monitor cloud-hypervisor process exit.
	go func() {
		v.waitCh <- v.chvCmd.Wait()
	}()

	// Wait for the VM to become healthy via vsock.
	if err := v.waitForHealth(ctx); err != nil {
		return fmt.Errorf("health check: %w", err)
	}

	return nil
}

func (v *vm) startVirtiofsd(share *Share) error {
	args := []string{
		"--socket-path=" + share.Socket,
		"--shared-dir=" + share.SourcePath,
	}
	if share.ReadOnly {
		args = append(args, "--sandbox=none")
	}

	cmd := exec.Command(v.cfg.VirtiofsdBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("exec virtiofsd: %w", err)
	}
	v.virtiofsd = append(v.virtiofsd, cmd)

	// Poll for the socket file to appear (up to 2s).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(share.Socket); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("virtiofsd socket %s did not appear within 2s", share.Socket)
}

func (v *vm) buildCHVArgs() []string {
	args := []string{
		"--kernel", v.cfg.Kernel,
		"--disk", fmt.Sprintf("path=%s,readonly=on", v.cfg.RootFS),
		"--cpus", fmt.Sprintf("boot=%d", v.cfg.VCPUs),
		"--memory", fmt.Sprintf("size=%dM", v.cfg.MemoryMB),
		"--vsock", fmt.Sprintf("cid=%d,socket=%s", v.cfg.CID, v.vsockSock),
		"--console", v.cfg.Console,
		"--serial", v.cfg.Serial,
	}

	if v.cfg.APISocket != "" {
		args = append(args, "--api-socket", v.cfg.APISocket)
	}

	for i := range v.cfg.Shares {
		share := &v.cfg.Shares[i]
		args = append(args, "--fs",
			fmt.Sprintf("tag=%s,socket=%s", share.Tag, share.Socket))
	}

	if v.cfg.TAPDevice != "" {
		netArg := "tap=" + v.cfg.TAPDevice
		if v.cfg.MACAddress != "" {
			netArg += ",mac=" + v.cfg.MACAddress
		}
		args = append(args, "--net", netArg)
	}

	return args
}

func (v *vm) Stop(ctx context.Context) error {
	v.mu.Lock()
	if v.status != Running && v.status != Failed {
		v.mu.Unlock()
		return fmt.Errorf("microvm: cannot stop VM in state %s", v.status)
	}
	v.status = Stopping
	v.mu.Unlock()

	// Attempt graceful teardown over vsock (best-effort).
	v.attemptGracefulTeardown(ctx)

	// Send SIGTERM to cloud-hypervisor.
	if v.chvCmd != nil && v.chvCmd.Process != nil {
		_ = v.chvCmd.Process.Signal(syscall.SIGTERM)

		// Wait up to 10s for graceful exit.
		select {
		case <-v.waitCh:
			// Process exited.
		case <-time.After(10 * time.Second):
			log.Printf("microvm: cloud-hypervisor did not exit after SIGTERM, sending SIGKILL")
			_ = v.chvCmd.Process.Kill()
			<-v.waitCh
		}
	}

	v.cleanup()

	v.mu.Lock()
	v.status = Stopped
	v.mu.Unlock()
	return nil
}

func (v *vm) attemptGracefulTeardown(ctx context.Context) {
	dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	c, err := dialVsockClient(dialCtx, v.cfg.CID, v.cfg.VsockPort)
	if err != nil {
		return
	}
	defer func() { _ = c.Close() }()

	teardownCtx, teardownCancel := context.WithTimeout(ctx, 5*time.Second)
	defer teardownCancel()

	_ = c.Teardown(teardownCtx, "", 5, false, nil)
}

func (v *vm) cleanup() {
	// Kill all virtiofsd processes.
	for _, cmd := range v.virtiofsd {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}
	v.virtiofsd = nil

	// Remove temp directory.
	if v.tmpDir != "" {
		_ = os.RemoveAll(v.tmpDir)
		v.tmpDir = ""
	}
}

func (v *vm) Wait() error {
	return <-v.waitCh
}
