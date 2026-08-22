//go:build integration

package integration

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// binary returns the path to the nix-compose binary.
// It expects the binary to be built before running integration tests
// (see Makefile test-integration target).
func binary(t *testing.T) string {
	t.Helper()
	// Look for binary in project root first, then PATH.
	if root := projectRoot(t); root != "" {
		p := filepath.Join(root, "nix-compose")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	p, err := exec.LookPath("nix-compose")
	if err != nil {
		t.Fatal("nix-compose binary not found — run 'make build' first")
	}
	return p
}

// projectRoot returns the root of the nix-compose project.
func projectRoot(t *testing.T) string {
	t.Helper()
	_, f, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine project root")
	}
	// test/integration/integration_test.go → project root is ../../
	return filepath.Join(filepath.Dir(f), "..", "..")
}

// fixtureDir returns the absolute path to a test fixture directory.
func fixtureDir(t *testing.T, name string) string {
	t.Helper()
	_, f, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine fixture directory")
	}
	dir := filepath.Join(filepath.Dir(f), "testdata", name)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("fixture directory %q not found: %v", name, err)
	}
	return dir
}

// run executes nix-compose with the given arguments and returns combined output.
func run(t *testing.T, projectName, projectDir string, args ...string) (string, error) {
	t.Helper()
	bin := binary(t)
	fullArgs := []string{
		"--project-name", projectName,
		"--project-dir", projectDir,
	}
	fullArgs = append(fullArgs, args...)

	cmd := exec.Command(bin, fullArgs...)
	cmd.Dir = projectDir

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	return buf.String(), err
}

// mustRun is like run but fails the test on error.
func mustRun(t *testing.T, projectName, projectDir string, args ...string) string {
	t.Helper()
	out, err := run(t, projectName, projectDir, args...)
	if err != nil {
		t.Fatalf("nix-compose %s failed: %v\nOutput:\n%s",
			strings.Join(args, " "), err, out)
	}
	return out
}

// cleanup registers a t.Cleanup that runs nix-compose down to tear down
// the project, ignoring errors (best-effort).
func cleanup(t *testing.T, projectName, projectDir string) {
	t.Helper()
	t.Cleanup(func() {
		out, err := run(t, projectName, projectDir, "down", "-v", "--timeout", "5")
		if err != nil {
			t.Logf("cleanup (down) failed: %v\nOutput:\n%s", err, out)
		}
	})
}

// uniqueProjectName returns a unique project name for test isolation.
func uniqueProjectName(t *testing.T, suffix string) string {
	t.Helper()
	// Sanitize test name for use as a compose project name.
	name := strings.ReplaceAll(t.Name(), "/", "-")
	name = strings.ToLower(name)
	return fmt.Sprintf("nctest-%s-%s", name, suffix)
}
