package volumes

import (
	"os"
	"path/filepath"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	return &Store{Root: t.TempDir()}
}

func TestEnsure(t *testing.T) {
	s := testStore(t)
	p, err := s.Ensure("myproj", "pgdata")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	want := filepath.Join(s.Root, "myproj", "pgdata")
	if p != want {
		t.Errorf("path = %q, want %q", p, want)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestEnsureIdempotent(t *testing.T) {
	s := testStore(t)
	p1, err := s.Ensure("proj", "vol")
	if err != nil {
		t.Fatalf("Ensure (1st): %v", err)
	}
	p2, err := s.Ensure("proj", "vol")
	if err != nil {
		t.Fatalf("Ensure (2nd): %v", err)
	}
	if p1 != p2 {
		t.Errorf("paths differ: %q vs %q", p1, p2)
	}
}

func TestRemove(t *testing.T) {
	s := testStore(t)
	if _, err := s.Ensure("proj", "vol1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ensure("proj", "vol2"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("proj", "vol1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	// vol1 should be gone.
	if _, err := os.Stat(s.Path("proj", "vol1")); !os.IsNotExist(err) {
		t.Error("vol1 should have been removed")
	}
	// vol2 should remain.
	if _, err := os.Stat(s.Path("proj", "vol2")); err != nil {
		t.Error("vol2 should still exist")
	}
}

func TestRemoveAll(t *testing.T) {
	s := testStore(t)
	if _, err := s.Ensure("proj", "vol1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ensure("proj", "vol2"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveAll("proj"); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "proj")); !os.IsNotExist(err) {
		t.Error("project directory should have been removed")
	}
}

func TestList(t *testing.T) {
	s := testStore(t)
	if _, err := s.Ensure("proj", "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ensure("proj", "beta"); err != nil {
		t.Fatal(err)
	}
	names, err := s.List("proj")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 volumes, got %d", len(names))
	}
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["alpha"] || !found["beta"] {
		t.Errorf("expected alpha and beta, got %v", names)
	}
}

func TestListEmpty(t *testing.T) {
	s := testStore(t)
	names, err := s.List("nonexistent")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if names != nil {
		t.Errorf("expected nil, got %v", names)
	}
}

func TestPath(t *testing.T) {
	s := &Store{Root: "/data/volumes"}
	got := s.Path("myproj", "pgdata")
	want := "/data/volumes/myproj/pgdata"
	if got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestNewStore(t *testing.T) {
	s, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil store")
		return
	}
	if s.Root == "" {
		t.Error("expected non-empty Root")
	}
}

func TestListFiltersNonDirs(t *testing.T) {
	s := testStore(t)
	// Create a volume directory and a regular file.
	projDir := filepath.Join(s.Root, "proj")
	if err := os.MkdirAll(filepath.Join(projDir, "real-vol"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "not-a-vol.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	names, err := s.List("proj")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 1 {
		t.Fatalf("expected 1 volume (dirs only), got %d: %v", len(names), names)
	}
	if names[0] != "real-vol" {
		t.Errorf("expected 'real-vol', got %q", names[0])
	}
}

func TestRemoveNonexistent(t *testing.T) {
	s := testStore(t)
	// Removing a nonexistent volume should not error.
	if err := s.Remove("proj", "nope"); err != nil {
		t.Errorf("Remove nonexistent: %v", err)
	}
}

func TestRemoveAllNonexistent(t *testing.T) {
	s := testStore(t)
	if err := s.RemoveAll("nonexistent"); err != nil {
		t.Errorf("RemoveAll nonexistent: %v", err)
	}
}

func TestEnsureMultipleProjects(t *testing.T) {
	s := testStore(t)
	p1, err := s.Ensure("proj1", "vol")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := s.Ensure("proj2", "vol")
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Error("different projects should have different paths")
	}

	// Removing one project should not affect the other.
	if err := s.RemoveAll("proj1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p2); err != nil {
		t.Error("proj2 volume should still exist")
	}
}
