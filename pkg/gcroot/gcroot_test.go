package gcroot

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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

func TestCollectStorePaths_None(t *testing.T) {
	raw := []byte(`{"services":{"web":{"image":"nginx:latest","ports":["8080:80"]}}}`)
	paths := CollectStorePaths(raw)
	if len(paths) != 0 {
		t.Errorf("expected 0 store paths, got %v", paths)
	}
}

func TestCollectStorePaths_Some(t *testing.T) {
	raw := []byte(`{"services":{"app":{"image":"/nix/store/abc12345678901234567890123456789-myapp/bin/app","volumes":["/nix/store/def12345678901234567890123456789-config:/etc/app"]}}}`)
	paths := CollectStorePaths(raw)
	if len(paths) != 2 {
		t.Fatalf("expected 2 store paths, got %v", paths)
	}
}

func TestCollectStorePaths_Dedup(t *testing.T) {
	raw := []byte(`{"a":"/nix/store/abc12345678901234567890123456789-pkg","b":"/nix/store/abc12345678901234567890123456789-pkg"}`)
	paths := CollectStorePaths(raw)
	if len(paths) != 1 {
		t.Errorf("expected 1 deduplicated path, got %v", paths)
	}
}

func TestCreate_NoPaths(t *testing.T) {
	runner := &mockRunner{}
	err := Create(context.Background(), runner, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("create with no paths: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("expected no runner calls, got %d", len(runner.calls))
	}
}

func TestCreate_WithPaths(t *testing.T) {
	dir := t.TempDir()
	runner := &mockRunner{}

	paths := []string{
		"/nix/store/abc12345678901234567890123456789-foo",
		"/nix/store/def12345678901234567890123456789-bar",
	}
	err := Create(context.Background(), runner, dir, paths)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(runner.calls))
	}

	// Verify nix-store --realise --add-root was called.
	for i, call := range runner.calls {
		if call[0] != "nix-store" {
			t.Errorf("call %d: command = %q, want nix-store", i, call[0])
		}
		if call[1] != "--realise" {
			t.Errorf("call %d: expected --realise, got %q", i, call[1])
		}
		if call[2] != "--add-root" {
			t.Errorf("call %d: expected --add-root, got %q", i, call[2])
		}
	}

	// Verify directory was created.
	info, err := os.Stat(filepath.Join(dir, ".nix-compose"))
	if err != nil {
		t.Fatalf("stat .nix-compose: %v", err)
	}
	if !info.IsDir() {
		t.Error(".nix-compose is not a directory")
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	gcDir := filepath.Join(dir, ".nix-compose")
	if err := os.MkdirAll(gcDir, 0o750); err != nil {
		t.Fatal(err)
	}
	// Create fake gc root symlinks.
	for _, name := range []string{"gc-root-0", "gc-root-1"} {
		if err := os.Symlink("/nix/store/fake", filepath.Join(gcDir, name)); err != nil {
			t.Fatal(err)
		}
	}

	if err := Remove(dir); err != nil {
		t.Fatalf("remove: %v", err)
	}

	for _, name := range []string{"gc-root-0", "gc-root-1"} {
		if _, err := os.Lstat(filepath.Join(gcDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s should be removed", name)
		}
	}
}

func TestRemove_NoExist(t *testing.T) {
	dir := t.TempDir()
	if err := Remove(dir); err != nil {
		t.Fatalf("remove nonexistent: %v", err)
	}
}

// TestRemove_OnlyTouchesGCRoots pins Remove to the gc-root symlinks it owns.
// .nix-compose/ is a shared directory, so anything else in it must survive.
func TestRemove_OnlyTouchesGCRoots(t *testing.T) {
	dir := t.TempDir()
	gcDir := filepath.Join(dir, ".nix-compose")
	if err := os.MkdirAll(gcDir, 0o750); err != nil {
		t.Fatal(err)
	}
	// GC root symlink.
	if err := os.Symlink("/nix/store/fake", filepath.Join(gcDir, "gc-root-0")); err != nil {
		t.Fatal(err)
	}
	// An unrelated file the project may keep here.
	if err := os.WriteFile(filepath.Join(gcDir, "notes.txt"), []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Remove(dir); err != nil {
		t.Fatalf("remove: %v", err)
	}

	// gc-root should be gone.
	if _, err := os.Lstat(filepath.Join(gcDir, "gc-root-0")); !os.IsNotExist(err) {
		t.Error("gc-root-0 should be removed")
	}
	// the unrelated file should still exist.
	if _, err := os.Stat(filepath.Join(gcDir, "notes.txt")); err != nil {
		t.Error("notes.txt should be preserved")
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()

	if Exists(dir) {
		t.Error("should not exist initially")
	}

	gcDir := filepath.Join(dir, ".nix-compose")
	if err := os.MkdirAll(gcDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nix/store/fake", filepath.Join(gcDir, "gc-root-0")); err != nil {
		t.Fatal(err)
	}

	if !Exists(dir) {
		t.Error("should exist after creating symlink")
	}
}

func TestEnsureDir(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureDir(dir); err != nil {
		t.Fatalf("ensure dir: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, ".nix-compose"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}
