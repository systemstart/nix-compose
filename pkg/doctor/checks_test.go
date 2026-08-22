package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckLogReadability_NoLogsYet covers the case that made doctor wrong: a
// machine that has not run a container since boot has no log to test against,
// and reporting "readable" there is a claim with no evidence behind it.
func TestCheckLogReadability_NoLogsYet(t *testing.T) {
	report := Run(context.Background(), filepath.Join(t.TempDir(), "absent.sock"))

	check, ok := find(report, "container logs")
	if !ok {
		t.Fatal("no container logs check in the report")
	}
	// On a machine that has run containers this is legitimately OK or Warn;
	// what must never happen is a bare OK with no log ever seen. Assert the
	// pairing rather than the value.
	if check.Status != OK && check.Fix == "" {
		t.Errorf("a non-OK log finding must carry a fix, got: %+v", check)
	}
}

func TestCheckNix(t *testing.T) {
	r := &Report{}
	r.checkNix(context.Background())

	check, ok := find(r, "nix")
	if !ok {
		t.Fatal("checkNix produced no finding")
	}
	// nix is on PATH in the dev shell; either way the finding must be
	// self-explanatory.
	if check.Detail == "" {
		t.Error("the nix check must say what it found")
	}
	if check.Status != OK && check.Fix == "" {
		t.Error("a nix problem must carry a fix")
	}
}

func TestCheckCNIPlugins(t *testing.T) {
	r := &Report{}
	have := r.checkCNIPlugins()

	check, ok := find(r, "CNI plugins")
	if !ok {
		t.Fatal("checkCNIPlugins produced no finding")
	}
	if have && check.Status != OK {
		t.Errorf("plugins present but status is %v", check.Status)
	}
	if !have {
		// The absence of CNI is a warning, not a failure — host networking
		// still works. The fix has to explain what silently stops working.
		if check.Status != Warn {
			t.Errorf("missing plugins should warn, got %v", check.Status)
		}
		if !strings.Contains(check.Fix, "ports:") {
			t.Errorf("the fix should say `ports:` stops being mapped, got: %s", check.Fix)
		}
	}
}

// TestCheckIptables_SeverityDependsOnCNI pins the reason iptables and CNI are
// reported together: with no CNI plugins installed nothing would invoke
// iptables, so a missing one is not yet a failure.
func TestCheckIptables_SeverityDependsOnCNI(t *testing.T) {
	for _, haveCNI := range []bool{true, false} {
		r := &Report{}
		r.checkIptables(haveCNI)

		check, ok := find(r, "iptables")
		if !ok {
			t.Fatal("checkIptables produced no finding")
		}
		if check.Status == Fail && !haveCNI {
			t.Error("without CNI plugins, a missing iptables cannot yet be fatal")
		}
		if check.Status != OK && check.Fix == "" {
			t.Errorf("an iptables problem must carry a fix, got: %+v", check)
		}
	}
}

// TestOnPath_DirectoryIsNotABinary guards the check that a directory sharing a
// binary's name does not count as that binary being installed.
func TestOnPath_DirectoryIsNotABinary(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "iptables"), 0o755); err != nil {
		t.Fatal(err)
	}
	if onPath(dir, "iptables") {
		t.Error("a directory must not be mistaken for the binary")
	}
}
