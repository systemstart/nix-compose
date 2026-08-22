//go:build integration

package integration

import (
	"strings"
	"testing"
	"time"
)

func TestBasicUpDown(t *testing.T) {
	dir := fixtureDir(t, "basic")
	proj := uniqueProjectName(t, "basic")
	cleanup(t, proj, dir)

	// up -d
	out := mustRun(t, proj, dir, "up", "-d")
	t.Logf("up output:\n%s", out)

	// Give the container a moment to start.
	time.Sleep(2 * time.Second)

	// ps — should list the web service
	out = mustRun(t, proj, dir, "ps")
	t.Logf("ps output:\n%s", out)
	if !strings.Contains(out, "web") {
		t.Errorf("expected 'web' in ps output, got:\n%s", out)
	}

	// down
	out = mustRun(t, proj, dir, "down", "-v", "--timeout", "5")
	t.Logf("down output:\n%s", out)

	// ps after down — should NOT list the web service as running
	out, _ = run(t, proj, dir, "ps")
	// After down, ps may fail (no compose file) or show nothing.
	if strings.Contains(out, "running") {
		t.Errorf("expected no running services after down, got:\n%s", out)
	}
}

func TestUpWithBuild(t *testing.T) {
	dir := fixtureDir(t, "basic")
	proj := uniqueProjectName(t, "build")
	cleanup(t, proj, dir)

	// up -d --build should succeed (nginx doesn't need building but the flag
	// should be accepted without error).
	out := mustRun(t, proj, dir, "up", "-d", "--build")
	t.Logf("up --build output:\n%s", out)

	time.Sleep(2 * time.Second)

	out = mustRun(t, proj, dir, "ps")
	if !strings.Contains(out, "web") {
		t.Errorf("expected 'web' in ps output, got:\n%s", out)
	}
}

func TestPsQuiet(t *testing.T) {
	dir := fixtureDir(t, "basic")
	proj := uniqueProjectName(t, "psq")
	cleanup(t, proj, dir)

	mustRun(t, proj, dir, "up", "-d")
	time.Sleep(2 * time.Second)

	// --quiet should print container IDs only.
	out := mustRun(t, proj, dir, "ps", "--quiet")
	t.Logf("ps --quiet output:\n%s", out)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Error("expected at least one container ID from ps --quiet")
	}
}
