package logs

import (
	"bufio"
	"io"
	"strings"
	"time"
)

// LogEntry represents a single parsed CRI log line.
type LogEntry struct {
	Timestamp time.Time
	Stream    string // "stdout" or "stderr"
	IsPartial bool   // P tag = partial line
	Message   string
	Service   string // set by multiplexer
}

// ParseLine parses a single CRI log line.
// Format: <RFC3339Nano> <stream> <tag> <message>
func ParseLine(line string) (LogEntry, error) {
	// Split into 4 parts: timestamp, stream, tag, message (rest).
	parts := strings.SplitN(line, " ", 4)
	if len(parts) < 3 {
		return LogEntry{}, &ParseError{Line: line}
	}

	ts, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return LogEntry{}, &ParseError{Line: line}
	}

	stream := parts[1]
	tag := parts[2]

	var msg string
	if len(parts) == 4 {
		msg = parts[3]
	}

	return LogEntry{
		Timestamp: ts,
		Stream:    stream,
		IsPartial: tag == "P",
		Message:   msg,
	}, nil
}

// ParseError indicates a malformed CRI log line.
type ParseError struct {
	Line string
}

func (e *ParseError) Error() string {
	return "malformed CRI log line: " + e.Line
}

// ReadEntries reads all CRI log entries from r, skipping malformed lines.
func ReadEntries(r io.Reader) []LogEntry {
	var entries []LogEntry
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		entry, err := ParseLine(scanner.Text())
		if err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

// FilterSince returns entries with timestamps at or after since.
func FilterSince(entries []LogEntry, since time.Time) []LogEntry {
	var result []LogEntry
	for _, e := range entries {
		if !e.Timestamp.Before(since) {
			result = append(result, e)
		}
	}
	return result
}

// TailEntries returns the last n entries. If n <= 0 or n >= len(entries),
// all entries are returned.
func TailEntries(entries []LogEntry, n int) []LogEntry {
	if n <= 0 || n >= len(entries) {
		return entries
	}
	return entries[len(entries)-n:]
}

// JoinPartials merges consecutive partial (P-tagged) entries followed by a
// full (F-tagged) entry into a single entry. A trailing partial without a
// closing F is preserved as-is.
func JoinPartials(entries []LogEntry) []LogEntry {
	var result []LogEntry
	var buf strings.Builder
	var first LogEntry
	inPartial := false

	for _, e := range entries {
		if e.IsPartial {
			if !inPartial {
				first = e
				buf.Reset()
				inPartial = true
			}
			buf.WriteString(e.Message)
			continue
		}
		// Full line (F tag).
		if inPartial {
			buf.WriteString(e.Message)
			first.Message = buf.String()
			first.IsPartial = false
			result = append(result, first)
			inPartial = false
		} else {
			result = append(result, e)
		}
	}

	// Trailing partial without closing F.
	if inPartial {
		first.Message = buf.String()
		result = append(result, first)
	}

	return result
}
