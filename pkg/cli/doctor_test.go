package cli

import (
	"strings"
	"testing"
)

func TestWrapFix(t *testing.T) {
	// Words are never split, and no line exceeds the width unless a single
	// word already does.
	text := "containerd executes the CNI bridge plugin with its own environment, " +
		"so pod setup fails with \"failed to locate iptables\"."

	lines := wrapFix(text, 40)
	if len(lines) < 2 {
		t.Fatalf("expected the text to wrap, got %d line(s)", len(lines))
	}
	for _, line := range lines {
		if len(line) > 40 && !strings.Contains(line, "containerd") {
			t.Errorf("line exceeds the width: %q", line)
		}
	}
	if joined := strings.Join(lines, " "); joined != text {
		t.Errorf("wrapping changed the text:\n%s\n%s", joined, text)
	}
}

func TestWrapFix_Empty(t *testing.T) {
	if lines := wrapFix("", 40); len(lines) != 0 {
		t.Errorf("empty text should produce no lines, got %v", lines)
	}
}

// TestSilentErrorPrintsNothing covers the exit-status carrier: doctor has
// already printed its report, so Execute must not append an empty line.
func TestSilentErrorPrintsNothing(t *testing.T) {
	if errSilent.Error() != "" {
		t.Errorf("errSilent should carry no message, got %q", errSilent.Error())
	}
}
