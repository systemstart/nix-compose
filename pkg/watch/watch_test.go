package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/systemstart/nix-compose/pkg/eval"
)

func TestCollectMtimes_Empty(t *testing.T) {
	dir := t.TempDir()
	w := &Watcher{Config: Config{ProjectDir: dir}}
	mtimes := w.collectMtimes()
	if len(mtimes) != 0 {
		t.Errorf("expected 0 mtimes, got %d", len(mtimes))
	}
}

func TestCollectMtimes_NixFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "default.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Non-nix file should be ignored.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Watcher{Config: Config{ProjectDir: dir}}
	mtimes := w.collectMtimes()
	if len(mtimes) != 2 {
		t.Errorf("expected 2 nix files, got %d", len(mtimes))
	}
	if _, ok := mtimes["flake.nix"]; !ok {
		t.Error("missing flake.nix")
	}
	if _, ok := mtimes["default.nix"]; !ok {
		t.Error("missing default.nix")
	}
}

func TestCollectMtimes_NonexistentDir(t *testing.T) {
	w := &Watcher{Config: Config{ProjectDir: "/nonexistent/dir"}}
	mtimes := w.collectMtimes()
	if len(mtimes) != 0 {
		t.Errorf("expected 0 mtimes for nonexistent dir, got %d", len(mtimes))
	}
}

func TestMtimesChanged_Identical(t *testing.T) {
	now := time.Now()
	a := map[string]time.Time{"flake.nix": now}
	b := map[string]time.Time{"flake.nix": now}
	if mtimesChanged(a, b) {
		t.Error("expected no change")
	}
}

func TestMtimesChanged_DifferentTime(t *testing.T) {
	a := map[string]time.Time{"flake.nix": time.Now()}
	b := map[string]time.Time{"flake.nix": time.Now().Add(time.Second)}
	if !mtimesChanged(a, b) {
		t.Error("expected change")
	}
}

func TestMtimesChanged_NewFile(t *testing.T) {
	now := time.Now()
	a := map[string]time.Time{"flake.nix": now}
	b := map[string]time.Time{"flake.nix": now, "extra.nix": now}
	if !mtimesChanged(a, b) {
		t.Error("expected change for new file")
	}
}

func TestMtimesChanged_RemovedFile(t *testing.T) {
	now := time.Now()
	a := map[string]time.Time{"flake.nix": now, "extra.nix": now}
	b := map[string]time.Time{"flake.nix": now}
	if !mtimesChanged(a, b) {
		t.Error("expected change for removed file")
	}
}

func TestMtimesChanged_BothEmpty(t *testing.T) {
	a := map[string]time.Time{}
	b := map[string]time.Time{}
	if mtimesChanged(a, b) {
		t.Error("expected no change for empty maps")
	}
}

func TestWatcher_Run_CancelledContext(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	w := &Watcher{
		Config: Config{
			ProjectDir:   dir,
			PollInterval: 10 * time.Millisecond,
		},
	}

	comp := &eval.Composition{Services: map[string]eval.Service{}}
	err := w.Run(ctx, comp)
	if err != nil {
		t.Errorf("expected nil error on cancel, got: %v", err)
	}
}

func TestWatcher_Run_DetectsChange(t *testing.T) {
	dir := t.TempDir()
	nixFile := filepath.Join(dir, "flake.nix")
	if err := os.WriteFile(nixFile, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	evalCount := 0
	restartedServices := []string{}
	removedServices := []string{}

	initial := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:1.24"},
		},
	}

	updated := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx:1.25"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())

	w := &Watcher{
		Config: Config{
			ProjectDir:    dir,
			PollInterval:  20 * time.Millisecond,
			DebounceDelay: 10 * time.Millisecond,
		},
		Eval: func(_ context.Context) (*eval.Composition, []byte, error) {
			evalCount++
			return updated, nil, nil
		},
		Restart: func(_ context.Context, services []string) error {
			restartedServices = append(restartedServices, services...)
			cancel() // Stop watching after first restart.
			return nil
		},
		Remove: func(_ context.Context, services []string) error {
			removedServices = append(removedServices, services...)
			return nil
		},
	}

	// Modify the nix file after a short delay to trigger the watcher.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(nixFile, []byte("{updated}"), 0o644)
	}()

	err := w.Run(ctx, initial)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}

	if evalCount == 0 {
		t.Error("expected at least one re-evaluation")
	}
	if len(restartedServices) == 0 {
		t.Error("expected restarted services")
	}
}

func TestReEvalAndRestart_NoChanges(t *testing.T) {
	comp := &eval.Composition{
		Services: map[string]eval.Service{
			"web": {Image: "nginx"},
		},
	}

	w := &Watcher{
		Eval: func(_ context.Context) (*eval.Composition, []byte, error) {
			return comp, nil, nil
		},
		Restart: func(_ context.Context, _ []string) error {
			t.Error("restart should not be called when nothing changed")
			return nil
		},
		Remove: func(_ context.Context, _ []string) error {
			t.Error("remove should not be called when nothing changed")
			return nil
		},
	}

	result, err := w.reEvalAndRestart(context.Background(), comp)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result != comp {
		t.Error("expected same composition returned")
	}
}

func TestApplyChanges_RemoveAndRestart(t *testing.T) {
	removedServices := []string{}
	restartedServices := []string{}

	w := &Watcher{
		Remove: func(_ context.Context, services []string) error {
			removedServices = append(removedServices, services...)
			return nil
		},
		Restart: func(_ context.Context, services []string) error {
			restartedServices = append(restartedServices, services...)
			return nil
		},
	}

	diff := &DiffResult{
		Added:   []string{"api"},
		Removed: []string{"old"},
		Changed: []string{"web"},
	}

	err := w.applyChanges(context.Background(), diff)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(removedServices) != 1 || removedServices[0] != "old" {
		t.Errorf("removed = %v, want [old]", removedServices)
	}
	// Changed + Added
	if len(restartedServices) != 2 {
		t.Errorf("restarted = %v, want [web api]", restartedServices)
	}
}
