package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w

	fn()

	_ = w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("reading captured stderr: %v", err)
	}
	return buf.String()
}

// TestContainerStateDetail covers the exit code that containerInfo has always
// carried but nothing ever printed — the single most useful fact about a
// container that is no longer running.
func TestContainerStateDetail(t *testing.T) {
	tests := []struct {
		name string
		info containerInfo
		want string
	}{
		{"running", containerInfo{State: runtimev1.ContainerState_CONTAINER_RUNNING}, "running"},
		{"created", containerInfo{State: runtimev1.ContainerState_CONTAINER_CREATED}, "created"},
		{"exited clean", containerInfo{State: runtimev1.ContainerState_CONTAINER_EXITED}, "exited (0)"},
		{"exited error", containerInfo{State: runtimev1.ContainerState_CONTAINER_EXITED, ExitCode: 3}, "exited (3)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containerStateDetail(tt.info); got != tt.want {
				t.Errorf("containerStateDetail = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestReportHidden checks that the default (running-only) filter never hides a
// crash in silence: with every container exited, `ps` would otherwise print a
// bare header and exit 0, which is indistinguishable from a project that was
// never started.
func TestReportHidden(t *testing.T) {
	oldAll, oldQuiet, oldFormat, oldServices := psAll, psQuiet, psFormat, psServices
	t.Cleanup(func() { psAll, psQuiet, psFormat, psServices = oldAll, oldQuiet, oldFormat, oldServices })

	all := []containerInfo{
		{ContainerID: "a", Service: "web", State: runtimev1.ContainerState_CONTAINER_RUNNING},
		{ContainerID: "b", Service: "boom", State: runtimev1.ContainerState_CONTAINER_EXITED, ExitCode: 3},
	}
	shown := filterRunning(all)

	psQuiet, psServices, psFormat = false, false, ""
	out := captureStderr(t, func() { reportHidden(all, shown) })
	if !strings.Contains(out, `"boom"`) || !strings.Contains(out, "exited (3)") {
		t.Errorf("warning should name the crashed service and its exit code, got %q", out)
	}
	if strings.Contains(out, `"web"`) {
		t.Errorf("a running service should not be reported as hidden, got %q", out)
	}

	// Machine-readable modes are consumed by other programs; stderr chatter
	// there would be a surprise.
	for _, mode := range []string{"quiet", "services", "json"} {
		psQuiet, psServices, psFormat = mode == "quiet", mode == "services", ""
		if mode == "json" {
			psFormat = "json"
		}
		if out := captureStderr(t, func() { reportHidden(all, shown) }); out != "" {
			t.Errorf("%s mode should stay silent on stderr, got %q", mode, out)
		}
	}

	// Nothing hidden, nothing said.
	psQuiet, psServices, psFormat = false, false, ""
	if out := captureStderr(t, func() { reportHidden(all, all) }); out != "" {
		t.Errorf("no hidden containers should produce no warning, got %q", out)
	}
}
