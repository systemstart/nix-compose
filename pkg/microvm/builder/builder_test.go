package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
)

type mockRunner struct {
	calls  [][]string
	stdout []byte
	stderr []byte
	err    error
}

func (m *mockRunner) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	m.calls = append(m.calls, append([]string{name}, args...))
	return m.stdout, m.stderr, m.err
}

var _ eval.CommandRunner = (*mockRunner)(nil)

// makeImageDir creates a temporary directory with kernel and rootfs symlinks
// pointing to real files, simulating a nix build output.
func makeImageDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	kernelFile := filepath.Join(dir, "vmlinux")
	if err := os.WriteFile(kernelFile, []byte("fake-kernel"), 0o644); err != nil {
		t.Fatal(err)
	}
	rootfsFile := filepath.Join(dir, "rootfs.erofs")
	if err := os.WriteFile(rootfsFile, []byte("fake-rootfs"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(kernelFile, filepath.Join(dir, "kernel")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rootfsFile, filepath.Join(dir, "rootfs")); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestBuild_Success(t *testing.T) {
	dir := makeImageDir(t)
	runner := &mockRunner{
		stdout: []byte(dir + "\n"),
	}

	b := &Builder{Runner: runner}
	paths, err := b.Build(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasSuffix(paths.Kernel, "vmlinux") {
		t.Errorf("kernel = %q, want suffix vmlinux", paths.Kernel)
	}
	if !strings.HasSuffix(paths.RootFS, "rootfs.erofs") {
		t.Errorf("rootfs = %q, want suffix rootfs.erofs", paths.RootFS)
	}
}

func TestBuild_NixBuildFails(t *testing.T) {
	runner := &mockRunner{
		stderr: []byte("error: flake has no attribute 'microvm-image'\n"),
		err:    fmt.Errorf("exit status 1"),
	}

	b := &Builder{Runner: runner}
	_, err := b.Build(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "nix build failed") {
		t.Errorf("error = %q, want to contain 'nix build failed'", err)
	}
	if !strings.Contains(err.Error(), "microvm-image") {
		t.Errorf("error = %q, want to contain stderr", err)
	}
}

func TestBuild_EmptyOutput(t *testing.T) {
	runner := &mockRunner{
		stdout: []byte(""),
	}

	b := &Builder{Runner: runner}
	_, err := b.Build(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no output path") {
		t.Errorf("error = %q, want 'no output path'", err)
	}
}

func TestBuild_MissingKernel(t *testing.T) {
	dir := t.TempDir()
	// Only create rootfs, no kernel.
	rootfsFile := filepath.Join(dir, "rootfs.erofs")
	if err := os.WriteFile(rootfsFile, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rootfsFile, filepath.Join(dir, "rootfs")); err != nil {
		t.Fatal(err)
	}

	runner := &mockRunner{
		stdout: []byte(dir + "\n"),
	}

	b := &Builder{Runner: runner}
	_, err := b.Build(context.Background())
	if err == nil {
		t.Fatal("expected error for missing kernel")
	}
	if !strings.Contains(err.Error(), "kernel") {
		t.Errorf("error = %q, want to mention 'kernel'", err)
	}
}

func TestBuild_MissingRootFS(t *testing.T) {
	dir := t.TempDir()
	// Only create kernel, no rootfs.
	kernelFile := filepath.Join(dir, "vmlinux")
	if err := os.WriteFile(kernelFile, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(kernelFile, filepath.Join(dir, "kernel")); err != nil {
		t.Fatal(err)
	}

	runner := &mockRunner{
		stdout: []byte(dir + "\n"),
	}

	b := &Builder{Runner: runner}
	_, err := b.Build(context.Background())
	if err == nil {
		t.Fatal("expected error for missing rootfs")
	}
	if !strings.Contains(err.Error(), "rootfs") {
		t.Errorf("error = %q, want to mention 'rootfs'", err)
	}
}

func TestBuild_DefaultAttr(t *testing.T) {
	dir := makeImageDir(t)
	runner := &mockRunner{
		stdout: []byte(dir + "\n"),
	}

	b := &Builder{Runner: runner}
	_, _ = b.Build(context.Background())

	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}

	args := runner.calls[0]
	installable := args[2] // nix build <installable>
	if !strings.HasSuffix(installable, "#microvm-image") {
		t.Errorf("installable = %q, want suffix #microvm-image", installable)
	}
}

func TestBuild_CustomAttr(t *testing.T) {
	dir := makeImageDir(t)
	runner := &mockRunner{
		stdout: []byte(dir + "\n"),
	}

	b := &Builder{
		Runner:    runner,
		FlakeAttr: "custom-vm",
	}
	_, _ = b.Build(context.Background())

	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}

	installable := runner.calls[0][2]
	if !strings.HasSuffix(installable, "#custom-vm") {
		t.Errorf("installable = %q, want suffix #custom-vm", installable)
	}
}

func TestBuild_CustomFlakeRef(t *testing.T) {
	dir := makeImageDir(t)
	runner := &mockRunner{
		stdout: []byte(dir + "\n"),
	}

	b := &Builder{
		Runner:   runner,
		FlakeRef: "github:user/repo",
	}
	_, _ = b.Build(context.Background())

	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}

	installable := runner.calls[0][2]
	if !strings.HasPrefix(installable, "github:user/repo#") {
		t.Errorf("installable = %q, want prefix github:user/repo#", installable)
	}
}

func TestBuild_CommandArgs(t *testing.T) {
	dir := makeImageDir(t)
	runner := &mockRunner{
		stdout: []byte(dir + "\n"),
	}

	b := &Builder{
		Runner:    runner,
		FlakeRef:  ".",
		FlakeAttr: "microvm-image",
	}
	_, _ = b.Build(context.Background())

	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}

	got := runner.calls[0]
	want := []string{"nix", "build", ".#microvm-image", "--no-link", "--print-out-paths"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
