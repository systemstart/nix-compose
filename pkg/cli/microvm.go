package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/systemstart/nix-compose/pkg/eval"
	"github.com/systemstart/nix-compose/pkg/microvm"
	"github.com/systemstart/nix-compose/pkg/microvm/builder"
	"github.com/systemstart/nix-compose/pkg/orchestrate/client"
)

// newVM is the constructor used to create a microVM. It is a package-level
// variable so tests can replace it with a mock.
var newVM = func(cfg microvm.Config) (microvm.VM, error) {
	return microvm.New(cfg)
}

// dialVsock is the function used to connect to the in-VM gRPC server.
// It is a package-level variable so tests can replace it with a mock.
var dialVsock = func(ctx context.Context, cid, port uint32) (*client.Client, error) {
	return client.DialVsock(ctx, cid, port)
}

// buildVMImage is a package-level variable so tests can replace it with a mock.
var buildVMImage = func(ctx context.Context) (*builder.ImagePaths, error) {
	dir := projectDir()
	b := &builder.Builder{
		Runner:    &eval.ExecRunner{Dir: dir},
		FlakeRef:  vmImageFlakeRef,
		FlakeAttr: "microvm-image",
	}
	return b.Build(ctx)
}

// mergedVMFlags holds the CLI flag values after merging with Nix config.
type mergedVMFlags struct {
	kernel, rootfs string
	vcpus, memory  int
	cid            uint32
}

// mergeNixVMConfig returns CLI flag values merged with Nix-evaluated config.
// CLI flags take precedence; Nix values fill in defaults.
func mergeNixVMConfig(comp *eval.Composition) mergedVMFlags {
	m := mergedVMFlags{
		kernel: vmKernel, rootfs: vmRootFS,
		vcpus: vmVCPUs, memory: vmMemoryMB, cid: vmCID,
	}
	if comp == nil || comp.MicroVM == nil {
		return m
	}
	applyNixOverrides(&m, comp.MicroVM)
	return m
}

// applyNixOverrides fills default-valued CLI flags from Nix config.
func applyNixOverrides(m *mergedVMFlags, nixCfg *eval.MicroVMConfig) {
	m.kernel = stringDefault(m.kernel, nixCfg.Kernel)
	m.rootfs = stringDefault(m.rootfs, nixCfg.RootFS)
	if vmVCPUs == 1 && nixCfg.VCPUs > 0 {
		m.vcpus = nixCfg.VCPUs
	}
	if vmMemoryMB == 512 && nixCfg.MemoryMB > 0 {
		m.memory = nixCfg.MemoryMB
	}
	if vmCID == 3 && nixCfg.CID >= 3 {
		m.cid = nixCfg.CID
	}
}

// stringDefault returns cli if non-empty, otherwise nix.
func stringDefault(cli, nix string) string {
	if cli == "" && nix != "" {
		return nix
	}
	return cli
}

// microvmConfig merges CLI flags with Nix-evaluated MicroVMConfig and
// builds a microvm.Config. CLI flags override Nix values. If kernel or
// rootfs are still unset after merging, it auto-builds the VM image.
func microvmConfig(ctx context.Context, comp *eval.Composition) (microvm.Config, error) {
	m := mergeNixVMConfig(comp)

	// Auto-build VM image if kernel or rootfs is missing.
	if m.kernel == "" || m.rootfs == "" {
		fmt.Println("Building microVM image...")
		paths, err := buildVMImage(ctx)
		if err != nil {
			return microvm.Config{}, fmt.Errorf("microvm: auto-building VM image: %w", err)
		}
		if m.kernel == "" {
			m.kernel = paths.Kernel
		}
		if m.rootfs == "" {
			m.rootfs = paths.RootFS
		}
	}

	dir := projectDir()

	cfg := microvm.Config{
		Kernel:   m.kernel,
		RootFS:   m.rootfs,
		VCPUs:    m.vcpus,
		MemoryMB: m.memory,
		CID:      m.cid,
		Shares: []microvm.Share{
			{Tag: "nix-store", SourcePath: "/nix/store", ReadOnly: true},
			{Tag: "project", SourcePath: dir, ReadOnly: false},
		},
	}

	return cfg, nil
}

// dialVsockRaw is the raw vsock dialer used for port forwarding.
// It is a package-level variable so tests can replace it with a mock.
var dialVsockRaw = func(cid, port uint32) (net.Conn, error) {
	return client.DialVsockRaw(cid, port)
}

// microvmUp evaluates the Nix config, boots a microVM, connects via vsock,
// and delegates orchestration to the in-VM engine.
func microvmUp(ctx context.Context, deps UpDeps, profiles []string) (microvm.VM, *eval.Composition, error) {
	comp, err := evalForOrchestrate(ctx, deps.ProjectDir)
	if err != nil {
		return nil, nil, err
	}

	cfg, err := microvmConfig(ctx, comp)
	if err != nil {
		return nil, nil, err
	}

	fmt.Println("Starting microVM...")
	vm, err := newVM(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("microvm: creating VM: %w", err)
	}

	if err := vm.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("microvm: starting VM: %w", err)
	}

	fmt.Printf("MicroVM running (CID=%d, port=%d). Connecting via vsock...\n",
		vm.CID(), vm.VsockPort())

	rc, err := dialVsock(ctx, vm.CID(), vm.VsockPort())
	if err != nil {
		_ = vm.Stop(ctx)
		return nil, nil, fmt.Errorf("microvm: connecting to in-VM server: %w", err)
	}
	defer func() { _ = rc.Close() }()

	if err := remoteUp(ctx, rc, deps, profiles); err != nil {
		_ = vm.Stop(ctx)
		return nil, nil, fmt.Errorf("microvm: remote up: %w", err)
	}

	return vm, comp, nil
}

// microvmForeground blocks until SIGINT/SIGTERM or VM crash, then
// gracefully shuts down the VM.
func microvmForeground(ctx context.Context, vm microvm.VM) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	waitCh := make(chan error, 1)
	go func() { waitCh <- vm.Wait() }()

	fmt.Println("MicroVM running in foreground. Press Ctrl+C to stop.")

	select {
	case sig := <-sigCh:
		fmt.Printf("\nReceived %s, shutting down microVM...\n", sig)
		if err := vm.Stop(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: error stopping VM: %v\n", err)
		}
		fmt.Println("MicroVM stopped.")
		return nil
	case err := <-waitCh:
		if err != nil {
			return fmt.Errorf("microvm: VM exited unexpectedly: %w", err)
		}
		fmt.Println("MicroVM exited.")
		return nil
	}
}

// microvmDown connects to a running microVM via vsock, tears down services,
// and signals the VM to stop. This is a best-effort path — the VM is
// ephemeral and the primary shutdown mechanism is SIGINT on the foreground
// up process.
func microvmDown(ctx context.Context, dir string) error {
	cid := vmCID
	port := flagRemoteVsockPort

	fmt.Printf("Connecting to microVM (CID=%d, port=%d)...\n", cid, port)
	rc, err := dialVsock(ctx, cid, port)
	if err != nil {
		return fmt.Errorf("microvm: cannot connect to VM: %w (is the VM running?)", err)
	}
	defer func() { _ = rc.Close() }()

	if err := remoteDown(ctx, rc, dir); err != nil {
		return fmt.Errorf("microvm: teardown: %w", err)
	}

	fmt.Println("MicroVM teardown complete.")
	return nil
}
