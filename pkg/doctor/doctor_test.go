package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func find(r *Report, name string) (Check, bool) {
	for _, c := range r.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

// TestRun_NoCRISocketFails covers the check everything else depends on. The
// socket list is pointed at a directory that cannot contain one, so this holds
// on a machine that does have a runtime.
func TestRun_NoCRISocketFails(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "absent.sock")

	report := Run(context.Background(), absent)

	check, ok := find(report, "CRI socket")
	if !ok {
		t.Fatal("no CRI socket check in the report")
	}
	if check.Status != Fail {
		t.Errorf("status = %v, want Fail", check.Status)
	}
	if !strings.Contains(check.Detail, absent) {
		t.Errorf("the detail should name the socket that was tried, got: %s", check.Detail)
	}
	if !strings.Contains(check.Fix, "no fallback backend") {
		t.Errorf("the fix should say a runtime is required, got: %s", check.Fix)
	}
	if report.Failed() == 0 {
		t.Error("a missing runtime should count as a failure")
	}
}

// TestRun_WithoutRuntimeSkipsRuntimeQuestions guards against a report that
// asserts things it could not have checked: with no client, there is no cgroup
// driver to report.
func TestRun_WithoutRuntimeSkipsRuntimeQuestions(t *testing.T) {
	report := Run(context.Background(), filepath.Join(t.TempDir(), "absent.sock"))

	if _, ok := find(report, "cgroup driver"); ok {
		t.Error("the cgroup driver cannot be known without a connected runtime")
	}

	// The checks that do not need a runtime must still run.
	for _, name := range []string{"nix", "CNI plugins", "iptables", "container logs"} {
		if _, ok := find(report, name); !ok {
			t.Errorf("%q should be checked even without a runtime", name)
		}
	}
}

// TestEveryFindingCarriesAFix is the point of the command. A finding that
// names a problem without saying what to do about it is the thing this
// replaces, not the thing it is.
func TestEveryFindingCarriesAFix(t *testing.T) {
	report := Run(context.Background(), filepath.Join(t.TempDir(), "absent.sock"))

	for _, check := range report.Checks {
		if check.Status == OK {
			continue
		}
		if strings.TrimSpace(check.Fix) == "" {
			t.Errorf("check %q reports a problem with no fix", check.Name)
		}
		if strings.TrimSpace(check.Detail) == "" {
			t.Errorf("check %q has no detail", check.Name)
		}
	}
}

func TestStatusSymbols(t *testing.T) {
	for status, want := range map[Status]string{OK: "✓", Warn: "!", Fail: "✗"} {
		if got := status.Symbol(); got != want {
			t.Errorf("Status(%d).Symbol() = %q, want %q", status, got, want)
		}
	}
}

func TestOnPath(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()

	binary := filepath.Join(dir, "iptables")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A directory of the same name must not count as the binary.
	if err := os.MkdirAll(filepath.Join(other, "arptables"), 0o755); err != nil {
		t.Fatal(err)
	}

	if !onPath(other+":"+dir, "iptables") {
		t.Error("iptables should be found in the second entry")
	}
	if onPath(other, "iptables") {
		t.Error("iptables is not in that directory")
	}
	if onPath(other, "arptables") {
		t.Error("a directory must not be mistaken for the binary")
	}
	if onPath("", "iptables") {
		t.Error("an empty PATH contains nothing")
	}
}

func writeLog(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("entry\n"), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestProbeLogs_NothingThere is the case that made doctor wrong: an empty log
// directory must not report `seen`, because reporting "readable" for a
// directory holding no logs is a claim with no evidence behind it — and that is
// exactly what a freshly booted machine looks like.
func TestProbeLogs_NothingThere(t *testing.T) {
	seen, unreadable, _ := probeLogs(filepath.Join(t.TempDir(), "missing"))
	if seen {
		t.Error("an absent log directory contains no logs to have seen")
	}
	if unreadable {
		t.Error("an absent log directory is not a finding")
	}
}

func TestProbeLogs_Readable(t *testing.T) {
	base := t.TempDir()
	writeLog(t, filepath.Join(base, "proj", "web"), "0.log", 0o644)

	seen, unreadable, path := probeLogs(base)
	if !seen {
		t.Error("a readable log should still count as seen")
	}
	if unreadable {
		t.Errorf("a readable log should not be reported, got %s", path)
	}
}

func TestProbeLogs_Unreadable(t *testing.T) {
	// Root can read anything, so the mode says nothing there.
	if os.Geteuid() == 0 {
		t.Skip("running as root; file modes do not restrict reads")
	}
	base := t.TempDir()
	locked := writeLog(t, filepath.Join(base, "proj", "web"), "0.log", 0o000)

	seen, unreadable, path := probeLogs(base)
	if !seen {
		t.Error("seen should be true when logs exist")
	}
	if !unreadable {
		t.Fatal("an unreadable log should be reported")
	}
	if path != locked {
		t.Errorf("reported %s, want %s", path, locked)
	}
}
