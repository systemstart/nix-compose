package logs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseTail(t *testing.T) {
	tests := []struct {
		input   string
		want    int
		wantErr bool
	}{
		{"all", -1, false},
		{"", -1, false},
		{"10", 10, false},
		{"0", 0, false},
		{"abc", 0, true},
		{"-1", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseTail(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("parseTail(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseSince(t *testing.T) {
	t.Run("RFC3339", func(t *testing.T) {
		got, err := parseSince("2024-01-15T10:30:00Z")
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("duration", func(t *testing.T) {
		before := time.Now()
		got, err := parseSince("42m")
		if err != nil {
			t.Fatal(err)
		}
		// Should be roughly 42 minutes ago.
		expected := before.Add(-42 * time.Minute)
		if got.Sub(expected).Abs() > 2*time.Second {
			t.Errorf("got %v, expected ~%v", got, expected)
		}
	})

	t.Run("empty", func(t *testing.T) {
		got, err := parseSince("")
		if err != nil {
			t.Fatal(err)
		}
		if !got.IsZero() {
			t.Errorf("expected zero time, got %v", got)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		_, err := parseSince("not-a-time")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestFormatEntry(t *testing.T) {
	ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	e := LogEntry{
		Timestamp: ts,
		Stream:    "stdout",
		Message:   "hello",
		Service:   "web",
	}
	colorMap := buildColorMap([]string{"web"})

	t.Run("with prefix", func(t *testing.T) {
		got := formatEntry(e, colorMap, Options{})
		if !strings.Contains(got, "web") {
			t.Errorf("expected service prefix, got %q", got)
		}
		if !strings.HasSuffix(got, "hello\n") {
			t.Errorf("expected message, got %q", got)
		}
	})

	t.Run("no prefix", func(t *testing.T) {
		got := formatEntry(e, colorMap, Options{NoLogPrefix: true})
		if strings.Contains(got, "web") {
			t.Errorf("should not contain prefix, got %q", got)
		}
	})

	t.Run("with timestamp", func(t *testing.T) {
		got := formatEntry(e, colorMap, Options{Timestamps: true})
		if !strings.Contains(got, "2024-01-15T10:30:00Z") {
			t.Errorf("expected timestamp, got %q", got)
		}
	})

	t.Run("no service set", func(t *testing.T) {
		noSvc := LogEntry{Timestamp: ts, Message: "msg"}
		got := formatEntry(noSvc, colorMap, Options{})
		if strings.Contains(got, "|") {
			t.Errorf("should not have prefix separator, got %q", got)
		}
	})
}

func TestLogFilePath(t *testing.T) {
	got := LogFilePath("/base", "proj", "web")
	want := "/base/proj/web/0.log"
	if got != want {
		t.Errorf("LogFilePath = %q, want %q", got, want)
	}
}

func writeTestLog(t *testing.T, dir, project, service, content string) {
	t.Helper()
	logDir := filepath.Join(dir, project, service)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "0.log"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDump(t *testing.T) {
	dir := t.TempDir()
	writeTestLog(t, dir, "proj", "web",
		"2024-01-15T10:30:01Z stdout F web line 1\n2024-01-15T10:30:03Z stdout F web line 2\n")
	writeTestLog(t, dir, "proj", "api",
		"2024-01-15T10:30:00Z stdout F api line 1\n2024-01-15T10:30:02Z stdout F api line 2\n")

	var buf bytes.Buffer
	err := Dump(&buf, dir, "proj", []string{"web", "api"}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d: %q", len(lines), output)
	}

	// Verify sorted order: api(00), web(01), api(02), web(03).
	if !strings.Contains(lines[0], "api line 1") {
		t.Errorf("line 0 = %q, want api line 1", lines[0])
	}
	if !strings.Contains(lines[1], "web line 1") {
		t.Errorf("line 1 = %q, want web line 1", lines[1])
	}
	if !strings.Contains(lines[2], "api line 2") {
		t.Errorf("line 2 = %q, want api line 2", lines[2])
	}
	if !strings.Contains(lines[3], "web line 2") {
		t.Errorf("line 3 = %q, want web line 2", lines[3])
	}
}

func TestDump_WithTail(t *testing.T) {
	dir := t.TempDir()
	writeTestLog(t, dir, "proj", "web",
		"2024-01-15T10:30:00Z stdout F line 1\n"+
			"2024-01-15T10:30:01Z stdout F line 2\n"+
			"2024-01-15T10:30:02Z stdout F line 3\n")

	var buf bytes.Buffer
	err := Dump(&buf, dir, "proj", []string{"web"}, Options{Tail: "2"})
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "line 2") {
		t.Errorf("line 0 = %q, want line 2", lines[0])
	}
}

func TestDump_WithSince(t *testing.T) {
	dir := t.TempDir()
	writeTestLog(t, dir, "proj", "web",
		"2024-01-15T10:00:00Z stdout F old\n"+
			"2024-01-15T12:00:00Z stdout F new\n")

	var buf bytes.Buffer
	err := Dump(&buf, dir, "proj", []string{"web"}, Options{Since: "2024-01-15T11:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	if strings.Contains(output, "old") {
		t.Error("should not contain old entry")
	}
	if !strings.Contains(output, "new") {
		t.Error("should contain new entry")
	}
}

func TestDump_MissingLogFile(t *testing.T) {
	dir := t.TempDir()
	// Only create one service log.
	writeTestLog(t, dir, "proj", "web", "2024-01-15T10:30:00Z stdout F hello\n")

	var buf bytes.Buffer
	err := Dump(&buf, dir, "proj", []string{"web", "missing"}, Options{})
	if err != nil {
		t.Fatalf("should gracefully skip missing: %v", err)
	}
	if !strings.Contains(buf.String(), "hello") {
		t.Error("should contain web's log")
	}
}

func TestFollow_ContextCancel(t *testing.T) {
	dir := t.TempDir()
	writeTestLog(t, dir, "proj", "web", "2024-01-15T10:30:00Z stdout F initial\n")

	ctx, cancel := context.WithCancel(context.Background())
	var buf bytes.Buffer

	done := make(chan error, 1)
	go func() {
		done <- Follow(ctx, &buf, dir, "proj", []string{"web"}, Options{})
	}()

	// Let it poll at least once.
	time.Sleep(300 * time.Millisecond)
	cancel()

	err := <-done
	if err != nil {
		t.Fatalf("Follow returned error: %v", err)
	}

	if !strings.Contains(buf.String(), "initial") {
		t.Error("should have dumped initial log")
	}
}

func TestCollectAndFilter(t *testing.T) {
	dir := t.TempDir()
	writeTestLog(t, dir, "proj", "web",
		"2024-01-15T10:30:00Z stdout F line 1\n"+
			"2024-01-15T10:30:01Z stdout F line 2\n"+
			"2024-01-15T10:30:02Z stdout F line 3\n")

	entries := CollectAndFilter(dir, "proj", []string{"web"}, Options{Tail: "2"})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Message != "line 2" {
		t.Errorf("expected 'line 2', got %q", entries[0].Message)
	}
}

func TestCollectAndFilter_NoFilter(t *testing.T) {
	dir := t.TempDir()
	writeTestLog(t, dir, "proj", "web",
		"2024-01-15T10:30:00Z stdout F hello\n")

	entries := CollectAndFilter(dir, "proj", []string{"web"}, Options{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestCollectAndFilter_MissingService(t *testing.T) {
	dir := t.TempDir()
	entries := CollectAndFilter(dir, "proj", []string{"nonexist"}, Options{})
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestFollowStream_ContextCancel(t *testing.T) {
	dir := t.TempDir()
	writeTestLog(t, dir, "proj", "web", "2024-01-15T10:30:00Z stdout F initial\n")

	ctx, cancel := context.WithCancel(context.Background())

	var collected []LogEntry
	done := make(chan error, 1)
	go func() {
		done <- FollowStream(ctx, dir, "proj", []string{"web"}, func(e LogEntry) error {
			collected = append(collected, e)
			return nil
		})
	}()

	// Write new data after initial offset capture
	time.Sleep(300 * time.Millisecond)

	// Append new log line
	logPath := LogFilePath(dir, "proj", "web")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	_, _ = f.WriteString("2024-01-15T10:30:01Z stdout F appended\n")
	_ = f.Close()

	// Wait for polling
	time.Sleep(400 * time.Millisecond)
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("FollowStream error: %v", err)
	}

	if len(collected) == 0 {
		t.Fatal("expected at least one entry from FollowStream")
	}
}

func TestPollServiceEntries(t *testing.T) {
	dir := t.TempDir()
	writeTestLog(t, dir, "proj", "api", "2024-01-15T10:30:00Z stdout F first\n")

	offsets := map[string]int64{"api": 0}
	entries := pollServiceEntries(dir, "proj", "api", offsets)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Message != "first" {
		t.Errorf("expected 'first', got %q", entries[0].Message)
	}
	if entries[0].Service != "api" {
		t.Errorf("expected service 'api', got %q", entries[0].Service)
	}

	// Calling again with updated offset should return nothing
	entries = pollServiceEntries(dir, "proj", "api", offsets)
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries on second poll, got %d", len(entries))
	}
}

func TestPollServiceEntries_MissingFile(t *testing.T) {
	dir := t.TempDir()
	offsets := map[string]int64{"missing": 0}
	entries := pollServiceEntries(dir, "proj", "missing", offsets)
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries for missing file, got %d", len(entries))
	}
}

func TestBuildColorMap(t *testing.T) {
	services := []string{"a", "b", "c", "d", "e", "f", "g"}
	m := buildColorMap(services)
	if len(m) != 7 {
		t.Fatalf("expected 7 entries, got %d", len(m))
	}
	// 7th service should wrap around to first color.
	if m["g"] != m["a"] {
		t.Errorf("expected color wrap-around: g=%q, a=%q", m["g"], m["a"])
	}
}

// TestCollectEntries_WarnsOnUnreadable pins the difference between a log that
// does not exist and one that cannot be read. Both used to `continue` in
// silence, so `nix-compose logs` on a root-owned 0640 log file — the normal
// case for a rootful containerd — printed nothing and exited 0, which reads as
// "the service produced no output".
func TestCollectEntries_WarnsOnUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; file modes do not restrict reads")
	}
	base := t.TempDir()

	svcDir := filepath.Join(base, "proj", "locked")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDir, "0.log"), []byte("secret\n"), 0o000); err != nil {
		t.Fatal(err)
	}

	var warnings []string
	collectEntries(base, "proj", []string{"locked"}, func(m string) { warnings = append(warnings, m) })
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings %v, want 1 for an unreadable log", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "locked") {
		t.Errorf("warning %q does not name the service", warnings[0])
	}

	// A service that never started has no log file, and that is not a problem
	// worth reporting — it would fire on every partially-started project.
	warnings = nil
	collectEntries(base, "proj", []string{"absent"}, func(m string) { warnings = append(warnings, m) })
	if len(warnings) != 0 {
		t.Errorf("a missing log should not warn, got %v", warnings)
	}
}
