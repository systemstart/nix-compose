package logs

import (
	"strings"
	"testing"
	"time"
)

func TestParseLine(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    LogEntry
		wantErr bool
	}{
		{
			name:  "stdout full",
			input: "2024-01-15T10:30:00.123456789Z stdout F hello world",
			want: LogEntry{
				Timestamp: time.Date(2024, 1, 15, 10, 30, 0, 123456789, time.UTC),
				Stream:    "stdout",
				Message:   "hello world",
			},
		},
		{
			name:  "stderr full",
			input: "2024-01-15T10:30:00Z stderr F error occurred",
			want: LogEntry{
				Timestamp: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
				Stream:    "stderr",
				Message:   "error occurred",
			},
		},
		{
			name:  "partial line",
			input: "2024-01-15T10:30:00Z stdout P partial",
			want: LogEntry{
				Timestamp: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
				Stream:    "stdout",
				IsPartial: true,
				Message:   "partial",
			},
		},
		{
			name:  "empty message",
			input: "2024-01-15T10:30:00Z stdout F ",
			want: LogEntry{
				Timestamp: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
				Stream:    "stdout",
				Message:   "",
			},
		},
		{
			name:  "no message field",
			input: "2024-01-15T10:30:00Z stdout F",
			want: LogEntry{
				Timestamp: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
				Stream:    "stdout",
			},
		},
		{
			name:    "too few fields",
			input:   "2024-01-15T10:30:00Z stdout",
			wantErr: true,
		},
		{
			name:    "invalid timestamp",
			input:   "not-a-timestamp stdout F hello",
			wantErr: true,
		},
		{
			name:    "empty line",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLine(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Timestamp.Equal(tt.want.Timestamp) {
				t.Errorf("Timestamp = %v, want %v", got.Timestamp, tt.want.Timestamp)
			}
			if got.Stream != tt.want.Stream {
				t.Errorf("Stream = %q, want %q", got.Stream, tt.want.Stream)
			}
			if got.IsPartial != tt.want.IsPartial {
				t.Errorf("IsPartial = %v, want %v", got.IsPartial, tt.want.IsPartial)
			}
			if got.Message != tt.want.Message {
				t.Errorf("Message = %q, want %q", got.Message, tt.want.Message)
			}
		})
	}
}

func TestReadEntries(t *testing.T) {
	input := strings.Join([]string{
		"2024-01-15T10:30:00Z stdout F line one",
		"bad line",
		"2024-01-15T10:30:01Z stderr F line two",
		"",
		"2024-01-15T10:30:02Z stdout F line three",
	}, "\n")

	entries := ReadEntries(strings.NewReader(input))
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Message != "line one" {
		t.Errorf("entry 0 Message = %q", entries[0].Message)
	}
	if entries[1].Stream != "stderr" {
		t.Errorf("entry 1 Stream = %q", entries[1].Stream)
	}
	if entries[2].Message != "line three" {
		t.Errorf("entry 2 Message = %q", entries[2].Message)
	}
}

func TestReadEntries_Empty(t *testing.T) {
	entries := ReadEntries(strings.NewReader(""))
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestFilterSince(t *testing.T) {
	entries := []LogEntry{
		{Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), Message: "old"},
		{Timestamp: time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC), Message: "mid"},
		{Timestamp: time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC), Message: "new"},
	}
	since := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	result := FilterSince(entries, since)
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}
	if result[0].Message != "mid" {
		t.Errorf("first = %q, want mid", result[0].Message)
	}
}

func TestFilterSince_Empty(t *testing.T) {
	result := FilterSince(nil, time.Now())
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

func TestTailEntries(t *testing.T) {
	entries := []LogEntry{
		{Message: "a"},
		{Message: "b"},
		{Message: "c"},
		{Message: "d"},
	}

	result := TailEntries(entries, 2)
	if len(result) != 2 || result[0].Message != "c" || result[1].Message != "d" {
		t.Errorf("TailEntries(4, 2) = %v", result)
	}

	// n larger than len.
	result = TailEntries(entries, 10)
	if len(result) != 4 {
		t.Errorf("expected all 4 entries, got %d", len(result))
	}

	// n = 0 returns all.
	result = TailEntries(entries, 0)
	if len(result) != 4 {
		t.Errorf("expected all 4 entries for n=0, got %d", len(result))
	}
}

func TestJoinPartials(t *testing.T) {
	ts := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

	t.Run("P+F merge", func(t *testing.T) {
		entries := []LogEntry{
			{Timestamp: ts, Stream: "stdout", IsPartial: true, Message: "hello "},
			{Timestamp: ts, Stream: "stdout", IsPartial: true, Message: "world"},
			{Timestamp: ts, Stream: "stdout", IsPartial: false, Message: "!"},
		}
		result := JoinPartials(entries)
		if len(result) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(result))
		}
		if result[0].Message != "hello world!" {
			t.Errorf("Message = %q, want %q", result[0].Message, "hello world!")
		}
		if result[0].IsPartial {
			t.Error("merged entry should not be partial")
		}
	})

	t.Run("trailing partial", func(t *testing.T) {
		entries := []LogEntry{
			{Timestamp: ts, Stream: "stdout", IsPartial: true, Message: "incomplete"},
		}
		result := JoinPartials(entries)
		if len(result) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(result))
		}
		if result[0].Message != "incomplete" {
			t.Errorf("Message = %q", result[0].Message)
		}
	})

	t.Run("no partials", func(t *testing.T) {
		entries := []LogEntry{
			{Timestamp: ts, Stream: "stdout", Message: "line1"},
			{Timestamp: ts, Stream: "stdout", Message: "line2"},
		}
		result := JoinPartials(entries)
		if len(result) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(result))
		}
	})

	t.Run("empty input", func(t *testing.T) {
		result := JoinPartials(nil)
		if len(result) != 0 {
			t.Errorf("expected 0, got %d", len(result))
		}
	})
}

func TestParseError_Error(t *testing.T) {
	pe := &ParseError{Line: "bad log line"}
	got := pe.Error()
	want := "malformed CRI log line: bad log line"
	if got != want {
		t.Errorf("ParseError.Error() = %q, want %q", got, want)
	}
}
