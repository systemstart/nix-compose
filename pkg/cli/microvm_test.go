package cli

import (
	"context"
	"fmt"
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
	"github.com/systemstart/nix-compose/pkg/microvm"
	"github.com/systemstart/nix-compose/pkg/microvm/builder"
)

func TestMicrovmConfig(t *testing.T) {
	// Save and restore global flag state.
	origKernel, origRootFS := vmKernel, vmRootFS
	origVCPUs, origMem, origCID := vmVCPUs, vmMemoryMB, vmCID
	origDir := flagProjectDir
	defer func() {
		vmKernel, vmRootFS = origKernel, origRootFS
		vmVCPUs, vmMemoryMB, vmCID = origVCPUs, origMem, origCID
		flagProjectDir = origDir
	}()

	vmKernel = "/boot/vmlinux"
	vmRootFS = "/images/rootfs.ext4"
	vmVCPUs = 2
	vmMemoryMB = 1024
	vmCID = 5
	flagProjectDir = "/tmp/project"

	cfg, err := microvmConfig(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Kernel != "/boot/vmlinux" {
		t.Errorf("kernel = %q, want /boot/vmlinux", cfg.Kernel)
	}
	if cfg.RootFS != "/images/rootfs.ext4" {
		t.Errorf("rootfs = %q, want /images/rootfs.ext4", cfg.RootFS)
	}
	if cfg.VCPUs != 2 {
		t.Errorf("vcpus = %d, want 2", cfg.VCPUs)
	}
	if cfg.MemoryMB != 1024 {
		t.Errorf("memory = %d, want 1024", cfg.MemoryMB)
	}
	if cfg.CID != 5 {
		t.Errorf("cid = %d, want 5", cfg.CID)
	}
}

func TestMicrovmConfig_MissingKernel(t *testing.T) {
	origKernel, origRootFS := vmKernel, vmRootFS
	origBuild := buildVMImage
	defer func() {
		vmKernel, vmRootFS = origKernel, origRootFS
		buildVMImage = origBuild
	}()

	vmKernel = ""
	vmRootFS = "/images/rootfs.ext4"

	// Auto-build will be triggered since kernel is empty; make it fail.
	buildVMImage = func(_ context.Context) (*builder.ImagePaths, error) {
		return nil, fmt.Errorf("nix build failed: mock error")
	}

	_, err := microvmConfig(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for missing kernel")
	}
}

func TestMicrovmConfig_MissingRootFS(t *testing.T) {
	origKernel, origRootFS := vmKernel, vmRootFS
	origBuild := buildVMImage
	defer func() {
		vmKernel, vmRootFS = origKernel, origRootFS
		buildVMImage = origBuild
	}()

	vmKernel = "/boot/vmlinux"
	vmRootFS = ""

	// Auto-build will be triggered since rootfs is empty; make it fail.
	buildVMImage = func(_ context.Context) (*builder.ImagePaths, error) {
		return nil, fmt.Errorf("nix build failed: mock error")
	}

	_, err := microvmConfig(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for missing rootfs")
	}
}

// microvmNixOverrideTest holds parameters for testing Nix/CLI override behaviour.
type microvmNixOverrideTest struct {
	name                    string
	cliKernel, cliRootFS    string
	cliVCPUs, cliMemoryMB   int
	cliCID                  uint32
	wantKernel, wantRootFS  string
	wantVCPUs, wantMemoryMB int
	wantCID                 uint32
}

func runNixOverrideTest(t *testing.T, tc microvmNixOverrideTest) {
	t.Helper()
	origKernel, origRootFS := vmKernel, vmRootFS
	origVCPUs, origMem, origCID := vmVCPUs, vmMemoryMB, vmCID
	origDir := flagProjectDir
	defer func() {
		vmKernel, vmRootFS = origKernel, origRootFS
		vmVCPUs, vmMemoryMB, vmCID = origVCPUs, origMem, origCID
		flagProjectDir = origDir
	}()

	vmKernel = tc.cliKernel
	vmRootFS = tc.cliRootFS
	vmVCPUs = tc.cliVCPUs
	vmMemoryMB = tc.cliMemoryMB
	vmCID = tc.cliCID
	flagProjectDir = "/tmp/project"

	comp := &eval.Composition{
		Services: map[string]eval.Service{},
		MicroVM: &eval.MicroVMConfig{
			Kernel:   "/nix/store/kernel",
			RootFS:   "/nix/store/rootfs",
			VCPUs:    4,
			MemoryMB: 2048,
			CID:      10,
		},
	}

	cfg, err := microvmConfig(context.Background(), comp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Kernel != tc.wantKernel {
		t.Errorf("kernel = %q, want %q", cfg.Kernel, tc.wantKernel)
	}
	if cfg.RootFS != tc.wantRootFS {
		t.Errorf("rootfs = %q, want %q", cfg.RootFS, tc.wantRootFS)
	}
	if cfg.VCPUs != tc.wantVCPUs {
		t.Errorf("vcpus = %d, want %d", cfg.VCPUs, tc.wantVCPUs)
	}
	if cfg.MemoryMB != tc.wantMemoryMB {
		t.Errorf("memory = %d, want %d", cfg.MemoryMB, tc.wantMemoryMB)
	}
	if cfg.CID != tc.wantCID {
		t.Errorf("cid = %d, want %d", cfg.CID, tc.wantCID)
	}
}

func TestMicrovmConfig_NixOverride(t *testing.T) {
	runNixOverrideTest(t, microvmNixOverrideTest{
		name:      "nix values fill in defaults",
		cliKernel: "", cliRootFS: "",
		cliVCPUs: 1, cliMemoryMB: 512, cliCID: 3,
		wantKernel: "/nix/store/kernel", wantRootFS: "/nix/store/rootfs",
		wantVCPUs: 4, wantMemoryMB: 2048, wantCID: 10,
	})
}

func TestMicrovmConfig_CLIOverridesNix(t *testing.T) {
	runNixOverrideTest(t, microvmNixOverrideTest{
		name:      "CLI overrides Nix",
		cliKernel: "/cli/kernel", cliRootFS: "/cli/rootfs",
		cliVCPUs: 8, cliMemoryMB: 4096, cliCID: 42,
		wantKernel: "/cli/kernel", wantRootFS: "/cli/rootfs",
		wantVCPUs: 8, wantMemoryMB: 4096, wantCID: 42,
	})
}

func TestMicrovmShares(t *testing.T) {
	origKernel, origRootFS := vmKernel, vmRootFS
	origVCPUs, origMem, origCID := vmVCPUs, vmMemoryMB, vmCID
	origDir := flagProjectDir
	defer func() {
		vmKernel, vmRootFS = origKernel, origRootFS
		vmVCPUs, vmMemoryMB, vmCID = origVCPUs, origMem, origCID
		flagProjectDir = origDir
	}()

	vmKernel = "/boot/vmlinux"
	vmRootFS = "/images/rootfs.ext4"
	vmVCPUs = 1
	vmMemoryMB = 512
	vmCID = 3
	flagProjectDir = "/my/project"

	cfg, err := microvmConfig(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Shares) != 2 {
		t.Fatalf("shares count = %d, want 2", len(cfg.Shares))
	}

	nixStore := cfg.Shares[0]
	if nixStore.Tag != "nix-store" {
		t.Errorf("share[0].Tag = %q, want nix-store", nixStore.Tag)
	}
	if nixStore.SourcePath != "/nix/store" {
		t.Errorf("share[0].SourcePath = %q, want /nix/store", nixStore.SourcePath)
	}
	if !nixStore.ReadOnly {
		t.Error("share[0].ReadOnly = false, want true")
	}

	projectShare := cfg.Shares[1]
	if projectShare.Tag != "project" {
		t.Errorf("share[1].Tag = %q, want project", projectShare.Tag)
	}
	if projectShare.SourcePath != "/my/project" {
		t.Errorf("share[1].SourcePath = %q, want /my/project", projectShare.SourcePath)
	}
	if projectShare.ReadOnly {
		t.Error("share[1].ReadOnly = true, want false")
	}
}

func TestMicrovmConfig_NixNil(t *testing.T) {
	origKernel, origRootFS := vmKernel, vmRootFS
	origDir := flagProjectDir
	defer func() {
		vmKernel, vmRootFS = origKernel, origRootFS
		flagProjectDir = origDir
	}()

	vmKernel = "/boot/vmlinux"
	vmRootFS = "/images/rootfs.ext4"
	flagProjectDir = "/tmp/project"

	// Composition with nil MicroVM should work fine with CLI flags.
	comp := &eval.Composition{
		Services: map[string]eval.Service{},
	}

	cfg, err := microvmConfig(context.Background(), comp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = cfg // just verify no panic
}

func TestMicrovmConfig_AutoBuild(t *testing.T) {
	origKernel, origRootFS := vmKernel, vmRootFS
	origDir := flagProjectDir
	origBuild := buildVMImage
	defer func() {
		vmKernel, vmRootFS = origKernel, origRootFS
		flagProjectDir = origDir
		buildVMImage = origBuild
	}()

	// Both empty — should trigger auto-build.
	vmKernel = ""
	vmRootFS = ""
	flagProjectDir = "/tmp/project"

	buildVMImage = func(_ context.Context) (*builder.ImagePaths, error) {
		return &builder.ImagePaths{
			Kernel: "/nix/store/auto-kernel",
			RootFS: "/nix/store/auto-rootfs",
		}, nil
	}

	cfg, err := microvmConfig(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Kernel != "/nix/store/auto-kernel" {
		t.Errorf("kernel = %q, want /nix/store/auto-kernel", cfg.Kernel)
	}
	if cfg.RootFS != "/nix/store/auto-rootfs" {
		t.Errorf("rootfs = %q, want /nix/store/auto-rootfs", cfg.RootFS)
	}
}

func TestMicrovmConfig_AutoBuildPartial(t *testing.T) {
	origKernel, origRootFS := vmKernel, vmRootFS
	origDir := flagProjectDir
	origBuild := buildVMImage
	defer func() {
		vmKernel, vmRootFS = origKernel, origRootFS
		flagProjectDir = origDir
		buildVMImage = origBuild
	}()

	// Kernel set, rootfs empty — should trigger auto-build but keep CLI kernel.
	vmKernel = "/cli/kernel"
	vmRootFS = ""
	flagProjectDir = "/tmp/project"

	buildVMImage = func(_ context.Context) (*builder.ImagePaths, error) {
		return &builder.ImagePaths{
			Kernel: "/nix/store/auto-kernel",
			RootFS: "/nix/store/auto-rootfs",
		}, nil
	}

	cfg, err := microvmConfig(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Kernel != "/cli/kernel" {
		t.Errorf("kernel = %q, want /cli/kernel (CLI should take precedence)", cfg.Kernel)
	}
	if cfg.RootFS != "/nix/store/auto-rootfs" {
		t.Errorf("rootfs = %q, want /nix/store/auto-rootfs", cfg.RootFS)
	}
}

// Verify MockVM implements VM interface (compile-time check).
var _ microvm.VM = (*microvm.MockVM)(nil)
