//go:build integration

package integration

import (
	"strings"
	"testing"
	"time"
)

// TestYAMLModeUpDown is the registry-free loop reached without writing any
// Nix: a nix-compose.yaml naming packages, images built from their closures
// and imported straight into containerd.
//
// It is the same assertion as TestNixBuiltImagesUpDown, which is the point —
// the YAML is a front-end onto the same evaluation, so it must arrive at the
// same place.
func TestYAMLModeUpDown(t *testing.T) {
	dir := fixtureDir(t, "yaml-mode")
	proj := uniqueProjectName(t, "yamlmode")
	cleanup(t, proj, dir)

	out := mustRun(t, proj, dir, "up", "-d")
	t.Logf("up output:\n%s", out)

	time.Sleep(2 * time.Second)

	out = mustRun(t, proj, dir, "ps")
	t.Logf("ps output:\n%s", out)
	if !strings.Contains(out, "sleeper") {
		t.Errorf("expected sleeper in ps output, got:\n%s", out)
	}

	// A nix-compose.local reference cannot resolve against any registry, so
	// its presence is the proof that nothing was pulled.
	out = mustRun(t, proj, dir, "images")
	t.Logf("images output:\n%s", out)
	for _, want := range []string{
		"nix-compose.local/coreutils-oci",
		"nix-compose.local/hello-oci",
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

// TestYAMLModeEntrypointResolvesAgainstPackage covers the one thing YAML
// cannot express the way a flake can. A flake writes
// `"${pkgs.coreutils}/bin/sleep"`; YAML has no interpolation, and an image
// built from a closure has no PATH, so a bare `sleep` has to be resolved
// against the package or the container cannot start at all.
func TestYAMLModeEntrypointResolvesAgainstPackage(t *testing.T) {
	dir := fixtureDir(t, "yaml-mode")
	proj := uniqueProjectName(t, "yamlentry")
	cleanup(t, proj, dir)

	mustRun(t, proj, dir, "up", "-d")
	time.Sleep(2 * time.Second)

	out := mustRun(t, proj, dir, "ps")
	if !strings.Contains(out, "running") {
		t.Errorf("sleeper should be running — a bare entrypoint command was "+
			"not resolved against its package. ps output:\n%s", out)
	}

	mustRun(t, proj, dir, "down", "-v", "--timeout", "5")
}

// TestYAMLRegistryOnlyNeedsNoNix is the property that makes YAML mode more
// than a second syntax: a document naming no packages is pure data, so nix is
// never executed and the project runs on a machine without Nix at all.
func TestYAMLRegistryOnlyNeedsNoNix(t *testing.T) {
	dir := fixtureDir(t, "yaml-registry-only")
	proj := uniqueProjectName(t, "yamlreg")
	cleanup(t, proj, dir)

	// `plan` evaluates without touching the runtime, which is enough to prove
	// the evaluation path and keeps the test independent of registry reach.
	out := mustRun(t, proj, dir, "plan")
	t.Logf("plan output:\n%s", out)

	if !strings.Contains(out, "nginx:alpine") {
		t.Errorf("expected the registry image in the plan, got:\n%s", out)
	}
	if strings.Contains(out, "nix-compose.local/") {
		t.Errorf("no service names a package, so nothing should have been "+
			"built locally, got:\n%s", out)
	}
}
