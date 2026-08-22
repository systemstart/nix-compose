package logs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DefaultLogBase is the default base directory for CRI log files.
const DefaultLogBase = "/tmp/nix-compose-logs"

// Options controls log output behavior.
type Options struct {
	Follow      bool
	Timestamps  bool
	NoLogPrefix bool
	Tail        string
	Since       string
	Services    []string

	// Warn receives a message for each service whose log could not be read for
	// a reason other than "not written yet". Without it those failures are
	// silent, and an unreadable log is indistinguishable from an empty one —
	// containerd creates log files root:root 0640, so this is the normal
	// experience for a non-root user, not an edge case. Nil discards them.
	Warn func(string)
}

// ANSI color codes cycled by service index.
var colors = []string{
	"\033[36m", // cyan
	"\033[33m", // yellow
	"\033[32m", // green
	"\033[35m", // magenta
	"\033[34m", // blue
	"\033[31m", // red
}

const colorReset = "\033[0m"

// LogFilePath returns the expected CRI log file path for a service.
func LogFilePath(base, project, service string) string {
	return filepath.Join(base, project, service, "0.log")
}

// Dump reads all service logs, merges them in timestamp order, and writes
// the formatted output to out.
func Dump(out io.Writer, logBase, project string, services []string, opts Options) error {
	sort.Strings(services)
	colorMap := buildColorMap(services)

	all := collectEntries(logBase, project, services, opts.Warn)

	var err error
	if all, err = applyFilters(all, opts); err != nil {
		return err
	}

	for _, e := range all {
		if _, err := fmt.Fprint(out, formatEntry(e, colorMap, opts)); err != nil {
			return fmt.Errorf("writing log entry: %w", err)
		}
	}
	return nil
}

// CollectAndFilter returns filtered log entries for the given services.
func CollectAndFilter(logBase, project string, services []string, opts Options) []LogEntry {
	all := collectEntries(logBase, project, services, opts.Warn)
	filtered, _ := applyFilters(all, opts)
	return filtered
}

// FollowStream polls for new log entries and calls emit for each new entry
// until ctx is cancelled.
func FollowStream(ctx context.Context, logBase, project string, services []string, emit func(LogEntry) error) error {
	sort.Strings(services)

	offsets := make(map[string]int64)
	for _, svc := range services {
		path := LogFilePath(logBase, project, svc)
		info, err := os.Stat(path)
		if err != nil {
			offsets[svc] = 0
			continue
		}
		offsets[svc] = info.Size()
	}

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for _, svc := range services {
				entries := pollServiceEntries(logBase, project, svc, offsets)
				for _, e := range entries {
					if err := emit(e); err != nil {
						return err
					}
				}
			}
		}
	}
}

// pollServiceEntries checks for new data in a service's log file and returns new entries.
func pollServiceEntries(logBase, project, svc string, offsets map[string]int64) []LogEntry {
	path := LogFilePath(logBase, project, svc)
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil
	}

	offset := offsets[svc]
	if info.Size() <= offset {
		return nil
	}

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil
	}

	entries := ReadEntries(f)
	entries = JoinPartials(entries)
	for i := range entries {
		entries[i].Service = svc
	}

	offsets[svc] = info.Size()
	return entries
}

// collectEntries reads and merges log entries from all services, sorted by timestamp.
func collectEntries(logBase, project string, services []string, warn func(string)) []LogEntry {
	var all []LogEntry
	for _, svc := range services {
		entries, err := readServiceLog(logBase, project, svc)
		if err != nil {
			// A log that does not exist yet is ordinary — the service may not
			// have started. Anything else (most often EACCES on containerd's
			// root:root 0640 log file) is a real failure to report, not a
			// reason to print nothing and exit 0.
			if !errors.Is(err, fs.ErrNotExist) && warn != nil {
				warn(fmt.Sprintf("cannot read logs for %q: %v", svc, err))
			}
			continue
		}
		for i := range entries {
			entries[i].Service = svc
		}
		all = append(all, entries...)
	}

	all = JoinPartials(all)

	sort.SliceStable(all, func(i, j int) bool {
		return all[i].Timestamp.Before(all[j].Timestamp)
	})
	return all
}

// applyFilters applies --since and --tail filters to the entry list.
func applyFilters(entries []LogEntry, opts Options) ([]LogEntry, error) {
	if opts.Since != "" {
		since, err := parseSince(opts.Since)
		if err != nil {
			return nil, fmt.Errorf("invalid --since value %q: %w", opts.Since, err)
		}
		entries = FilterSince(entries, since)
	}

	if opts.Tail != "" {
		n, err := parseTail(opts.Tail)
		if err != nil {
			return nil, fmt.Errorf("invalid --tail value %q: %w", opts.Tail, err)
		}
		if n > 0 {
			entries = TailEntries(entries, n)
		}
	}
	return entries, nil
}

// Follow dumps existing logs then polls for new entries until ctx is cancelled.
func Follow(ctx context.Context, out io.Writer, logBase, project string, services []string, opts Options) error {
	// First dump existing logs.
	if err := Dump(out, logBase, project, services, opts); err != nil {
		return err
	}

	sort.Strings(services)
	colorMap := buildColorMap(services)

	// Track file offsets for each service.
	offsets := make(map[string]int64)
	for _, svc := range services {
		path := LogFilePath(logBase, project, svc)
		info, err := os.Stat(path)
		if err != nil {
			offsets[svc] = 0
			continue
		}
		offsets[svc] = info.Size()
	}

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for _, svc := range services {
				pollService(out, logBase, project, svc, offsets, colorMap, opts)
			}
		}
	}
}

// readServiceLog reads and parses a service's log file.
func readServiceLog(logBase, project, service string) ([]LogEntry, error) {
	path := LogFilePath(logBase, project, service)
	f, err := os.Open(path)
	if err != nil {
		// Returned unwrapped on purpose: os.Open's *PathError already names
		// both the operation and the path, and the caller prefixes it with the
		// service name. Wrapping here produced "open log /p: open /p: ...".
		return nil, err //nolint:wrapcheck // *PathError is already the message we want
	}
	defer func() { _ = f.Close() }()
	return ReadEntries(f), nil
}

// pollService checks for new data in a service's log file and prints new entries.
func pollService(out io.Writer, logBase, project, svc string, offsets map[string]int64, colorMap map[string]string, opts Options) {
	path := LogFilePath(logBase, project, svc)
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return
	}

	offset := offsets[svc]
	if info.Size() <= offset {
		return
	}

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return
	}

	entries := ReadEntries(f)
	entries = JoinPartials(entries)
	for _, e := range entries {
		e.Service = svc
		_, _ = fmt.Fprint(out, formatEntry(e, colorMap, opts))
	}

	offsets[svc] = info.Size()
}

// buildColorMap assigns a color to each service based on sorted index.
func buildColorMap(services []string) map[string]string {
	m := make(map[string]string, len(services))
	for i, svc := range services {
		m[svc] = colors[i%len(colors)]
	}
	return m
}

// formatEntry formats a log entry for output.
func formatEntry(e LogEntry, colorMap map[string]string, opts Options) string {
	var b strings.Builder
	if !opts.NoLogPrefix && e.Service != "" {
		color := colorMap[e.Service]
		fmt.Fprintf(&b, "%s%s%s | ", color, e.Service, colorReset)
	}
	if opts.Timestamps {
		b.WriteString(e.Timestamp.Format(time.RFC3339Nano))
		b.WriteByte(' ')
	}
	b.WriteString(e.Message)
	b.WriteByte('\n')
	return b.String()
}

// parseTail parses the --tail value. Returns -1 for "all" or empty string.
func parseTail(s string) (int, error) {
	if s == "" || s == "all" {
		return -1, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("must be a number or \"all\": %w", err)
	}
	if n < 0 {
		return 0, fmt.Errorf("must be non-negative")
	}
	return n, nil
}

// parseSince parses the --since value as either an RFC3339 timestamp or a
// Go duration string (e.g. "42m", "2h") relative to now.
func parseSince(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	// Try RFC3339 first.
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	// Try as duration relative to now.
	d, err := time.ParseDuration(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("cannot parse as timestamp or duration: %s", s)
	}
	return time.Now().Add(-d), nil
}
