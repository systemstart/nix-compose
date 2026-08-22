//go:build integration

package integration

import (
	"strings"
	"testing"
	"time"
)

// TestNixBuiltImagesUpDown is the registry-free path end to end: images are
// built from Nix closures, imported straight into containerd, and run — with no
// registry, no Dockerfile, and no daemon build anywhere in the loop.
//
// The fixture covers all three ways to declare one: `package` with an explicit
// entrypoint, `package` alone (entrypoint from meta.mainProgram), and a
// hand-built `ociImage`.
func TestNixBuiltImagesUpDown(t *testing.T) {
	dir := fixtureDir(t, "nix-built")
	proj := uniqueProjectName(t, "nixbuilt")
	cleanup(t, proj, dir)

	out := mustRun(t, proj, dir, "up", "-d")
	t.Logf("up output:\n%s", out)

	// The two sleepers stay up; `hello` prints and exits.
	time.Sleep(2 * time.Second)

	out = mustRun(t, proj, dir, "ps")
	t.Logf("ps output:\n%s", out)
	for _, svc := range []string{"sleeper", "custom"} {
		if !strings.Contains(out, svc) {
			t.Errorf("expected %q in ps output, got:\n%s", svc, out)
		}
	}

	// Every image must be registered under the local domain. That reference
	// cannot resolve against any registry, so its presence is the proof that
	// nothing was pulled.
	out = mustRun(t, proj, dir, "images")
	t.Logf("images output:\n%s", out)
	for _, want := range []string{
		"nix-compose.local/coreutils-oci",
		"nix-compose.local/hello-oci",
		"nix-compose.local/custom-sleeper",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in images output, got:\n%s", want, out)
		}
	}

	mustRun(t, proj, dir, "down", "-v", "--timeout", "5")

	out, _ = run(t, proj, dir, "ps")
	if strings.Contains(out, "running") {
		t.Errorf("expected no running services after down, got:\n%s", out)
	}
}

// TestNixBuiltImagesAreIdempotent covers the content-addressed shortcut: the
// image reference carries the store hash, so a second up must reuse what is
// already in the runtime rather than importing it again.
func TestNixBuiltImagesAreIdempotent(t *testing.T) {
	dir := fixtureDir(t, "nix-built")
	proj := uniqueProjectName(t, "nixbuilt-idem")
	cleanup(t, proj, dir)

	mustRun(t, proj, dir, "up", "-d")
	time.Sleep(2 * time.Second)

	first := mustRun(t, proj, dir, "images")

	// A second up over the same composition.
	out := mustRun(t, proj, dir, "up", "-d")
	t.Logf("second up output:\n%s", out)
	time.Sleep(2 * time.Second)

	second := mustRun(t, proj, dir, "images")
	if first != second {
		t.Errorf("images changed across a repeated up:\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	out = mustRun(t, proj, dir, "ps")
	for _, svc := range []string{"sleeper", "custom"} {
		if !strings.Contains(out, svc) {
			t.Errorf("expected %q still running after a repeated up, got:\n%s", svc, out)
		}
	}
}

// TestBuildDirectiveRejected covers the error ADR-006 promised: CRI has no
// build API, and the message has to name the Nix options that replace it.
func TestBuildDirectiveRejected(t *testing.T) {
	dir := fixtureDir(t, "build-directive")
	proj := uniqueProjectName(t, "builddirective")
	cleanup(t, proj, dir)

	out, err := run(t, proj, dir, "up", "-d")
	if err == nil {
		t.Fatalf("expected up to fail for a service using build:, got:\n%s", out)
	}
	t.Logf("up output:\n%s", out)

	for _, want := range []string{"build:", "services.web.package", "ociImage", "externally"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected the error to mention %q, got:\n%s", want, out)
		}
	}
}
