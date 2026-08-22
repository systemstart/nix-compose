package gcroot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/systemstart/nix-compose/pkg/eval"
)

const (
	gcRootDir = ".nix-compose"
)

var storePathRe = regexp.MustCompile(`/nix/store/[a-z0-9]{32}-[a-zA-Z0-9+._?=-]+`)

// CollectStorePaths scans raw nix eval output for /nix/store/ paths
// and returns deduplicated results.
func CollectStorePaths(raw []byte) []string {
	matches := storePathRe.FindAll(raw, -1)
	seen := make(map[string]struct{}, len(matches))
	var result []string
	for _, m := range matches {
		s := string(m)
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			result = append(result, s)
		}
	}
	return result
}

// Create registers GC roots for the given Nix store paths so that
// nix-collect-garbage does not remove them while containers are running.
// If storePaths is empty, this is a no-op.
func Create(ctx context.Context, runner eval.CommandRunner, projectDir string, storePaths []string) error {
	if len(storePaths) == 0 {
		return nil
	}

	dir := filepath.Join(projectDir, gcRootDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating gc-root directory: %w", err)
	}

	for i, sp := range storePaths {
		link := filepath.Join(dir, fmt.Sprintf("gc-root-%d", i))
		// Remove stale link if present.
		_ = os.Remove(link)

		args := []string{"--realise", "--add-root", link, sp}
		_, stderr, err := runner.Run(ctx, "nix-store", args...)
		if err != nil {
			return fmt.Errorf("adding gc root for %s: %s: %w", sp, string(stderr), err)
		}
	}

	return nil
}

// Remove deletes all GC root symlinks and the .nix-compose directory if empty.
func Remove(projectDir string) error {
	dir := filepath.Join(projectDir, gcRootDir)

	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading gc-root directory: %w", err)
	}

	for _, e := range entries {
		if matched, _ := filepath.Match("gc-root-*", e.Name()); matched {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}

	// Try to remove the directory; ignore error if it's not empty
	// (the compose file may still be there).
	_ = os.Remove(dir)

	return nil
}

// Exists checks whether any GC root symlinks exist.
func Exists(projectDir string) bool {
	dir := filepath.Join(projectDir, gcRootDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if matched, _ := filepath.Match("gc-root-*", e.Name()); matched {
			return true
		}
	}
	return false
}

// EnsureDir creates the .nix-compose directory if it doesn't exist.
func EnsureDir(projectDir string) error {
	dir := filepath.Join(projectDir, gcRootDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating .nix-compose directory: %w", err)
	}
	return nil
}
