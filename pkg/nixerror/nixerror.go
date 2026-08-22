package nixerror

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// NixEvalError represents a structured error from a nix evaluation failure.
type NixEvalError struct {
	Raw      string
	File     string
	Line     int
	Column   int
	Message  string
	ExitCode int
}

func (e *NixEvalError) Error() string {
	if e.File != "" {
		return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Column, e.Message)
	}
	return e.Message
}

// locationRe matches nix error location lines like:
//
//	at /path/to/file.nix:12:15:
var locationRe = regexp.MustCompile(`at (.+\.nix):(\d+):(\d+):`)

// messageRe matches the final "error: <message>" line.
var messageRe = regexp.MustCompile(`(?m)^\s*error:\s+(.+)$`)

// messageStartRe locates the last "error:" so the whole message can be taken,
// not just its first line. nix-compose's own `throw`s are several lines long —
// they name the service, the offending value, and what to write instead — and
// truncating them to the first line throws away the half that helps.
var messageStartRe = regexp.MustCompile(`(?m)^\s*error:[ \t]*`)

// trailerRe matches the location line nix prints after a message, which ends
// the message text.
var trailerRe = regexp.MustCompile(`^at .+:\d+:\d+:$`)

// ParseStderr extracts structured error information from nix stderr output.
// It returns the last file location and the last error message found.
func ParseStderr(stderr string, exitCode int) *NixEvalError {
	e := &NixEvalError{
		Raw:      stderr,
		ExitCode: exitCode,
	}

	// Find the last location reference (most specific to the actual error).
	locs := locationRe.FindAllStringSubmatch(stderr, -1)
	if len(locs) > 0 {
		last := locs[len(locs)-1]
		e.File = last[1]
		e.Line, _ = strconv.Atoi(last[2])
		e.Column, _ = strconv.Atoi(last[3])
	}

	// Find the last error message.
	msgs := messageRe.FindAllStringSubmatch(stderr, -1)
	if len(msgs) > 0 {
		e.Message = lastMessage(stderr, strings.TrimSpace(msgs[len(msgs)-1][1]))
	} else {
		// Fallback: use first non-empty line.
		for _, line := range strings.Split(stderr, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				e.Message = trimmed
				break
			}
		}
	}

	if e.Message == "" {
		e.Message = "nix evaluation failed"
	}

	return e
}

// lastMessage returns the full text of the final "error:" in stderr, including
// the continuation lines nix indents beneath it. firstLine is what the
// single-line match found; it is returned unchanged when there is nothing to
// continue, so a one-line error keeps its exact previous formatting.
func lastMessage(stderr, firstLine string) string {
	starts := messageStartRe.FindAllStringIndex(stderr, -1)
	if len(starts) == 0 {
		return firstLine
	}

	body := stderr[starts[len(starts)-1][1]:]
	lines := strings.Split(body, "\n")

	// nix appends its own trailer — "at /path.nix:1:2:" and whatever stack
	// follows — after the message text. That is location, not message, and
	// ParseStderr reports it separately.
	for i, line := range lines {
		if i > 0 && trailerRe.MatchString(strings.TrimSpace(line)) {
			lines = lines[:i]
			break
		}
	}
	if len(lines) <= 1 {
		return firstLine
	}

	// nix indents the whole message; the first line has already had its
	// indent consumed by the regexp, so the common indent is measured over
	// the rest and stripped from them.
	indent := -1
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		n := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent < 0 || n < indent {
			indent = n
		}
	}
	if indent <= 0 {
		// Unindented following lines belong to something else, not to this
		// message — nix always indents continuations.
		return firstLine
	}

	out := []string{strings.TrimRight(lines[0], " \t")}
	for _, line := range lines[1:] {
		if len(line) >= indent {
			line = line[indent:]
		} else {
			line = strings.TrimLeft(line, " \t")
		}
		out = append(out, strings.TrimRight(line, " \t"))
	}

	return strings.TrimRight(strings.Join(out, "\n"), "\n ")
}
