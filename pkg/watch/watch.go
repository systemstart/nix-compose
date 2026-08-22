package watch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// DefaultPollInterval is the default interval for polling file changes.
const DefaultPollInterval = 500 * time.Millisecond

// DefaultDebounceDelay is the default delay after a change before triggering a re-eval.
const DefaultDebounceDelay = 200 * time.Millisecond

// EvalFunc evaluates the Nix configuration and returns a Composition.
type EvalFunc func(ctx context.Context) (*eval.Composition, []byte, error)

// RestartFunc restarts the given services.
type RestartFunc func(ctx context.Context, services []string) error

// RemoveFunc removes the given services.
type RemoveFunc func(ctx context.Context, services []string) error

// Config holds watcher configuration.
type Config struct {
	PollInterval  time.Duration
	DebounceDelay time.Duration
	ProjectDir    string
}

// Watcher watches for Nix file changes and triggers selective service restarts.
type Watcher struct {
	Config  Config
	Eval    EvalFunc
	Restart RestartFunc
	Remove  RemoveFunc
}

// Run starts the file-watching loop. It blocks until the context is cancelled.
func (w *Watcher) Run(ctx context.Context, initial *eval.Composition) error {
	pollInterval := w.Config.PollInterval
	if pollInterval == 0 {
		pollInterval = DefaultPollInterval
	}
	debounce := w.Config.DebounceDelay
	if debounce == 0 {
		debounce = DefaultDebounceDelay
	}

	current := initial
	lastMtimes := w.collectMtimes()

	fmt.Println("Watch mode: watching for Nix file changes...")

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			newMtimes := w.collectMtimes()
			if !mtimesChanged(lastMtimes, newMtimes) {
				continue
			}
			lastMtimes = newMtimes

			// Debounce: wait a bit for rapid successive changes.
			time.Sleep(debounce)

			updated, err := w.reEvalAndRestart(ctx, current)
			if err != nil {
				fmt.Printf("Watch: re-eval/restart failed: %v\n", err)
				continue
			}
			current = updated
		}
	}
}

// reEvalAndRestart re-evaluates the Nix config, diffs, and restarts changed services.
func (w *Watcher) reEvalAndRestart(ctx context.Context, current *eval.Composition) (*eval.Composition, error) {
	fmt.Println("Watch: change detected, re-evaluating...")
	newComp, _, err := w.Eval(ctx)
	if err != nil {
		return current, fmt.Errorf("re-evaluation: %w", err)
	}

	diff, err := DiffCompositions(current, newComp)
	if err != nil {
		return current, fmt.Errorf("diffing compositions: %w", err)
	}

	if diff.IsEmpty() {
		fmt.Println("Watch: no service changes detected")
		return current, nil
	}

	if err := w.applyChanges(ctx, diff); err != nil {
		return current, err
	}

	return newComp, nil
}

// applyChanges restarts changed/added services and removes deleted ones.
func (w *Watcher) applyChanges(ctx context.Context, diff *DiffResult) error {
	if len(diff.Removed) > 0 {
		fmt.Printf("Watch: removing services: %v\n", diff.Removed)
		if err := w.Remove(ctx, diff.Removed); err != nil {
			return fmt.Errorf("removing services: %w", err)
		}
	}

	restart := append(diff.Changed, diff.Added...)
	if len(restart) > 0 {
		fmt.Printf("Watch: restarting services: %v\n", restart)
		if err := w.Restart(ctx, restart); err != nil {
			return fmt.Errorf("restarting services: %w", err)
		}
	}

	return nil
}

// collectMtimes collects modification times for all *.nix files in the project directory.
func (w *Watcher) collectMtimes() map[string]time.Time {
	mtimes := make(map[string]time.Time)
	entries, err := os.ReadDir(w.Config.ProjectDir)
	if err != nil {
		return mtimes
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".nix" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		mtimes[entry.Name()] = info.ModTime()
	}
	return mtimes
}

// mtimesChanged compares two mtime maps for equality.
func mtimesChanged(old, new map[string]time.Time) bool {
	if len(old) != len(new) {
		return true
	}
	for name, oldTime := range old {
		newTime, ok := new[name]
		if !ok || !oldTime.Equal(newTime) {
			return true
		}
	}
	return false
}
