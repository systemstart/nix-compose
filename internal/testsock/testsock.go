// Package testsock hands out filesystem paths that are safe to bind a unix
// socket to.
//
// A unix socket path is copied into sockaddr_un.sun_path, which is a fixed
// 108-byte buffer on Linux (104 on macOS). Exceed it and bind(2) fails with
// EINVAL — reported by Go as "bind: invalid argument", which says nothing
// about length and sends you looking for a permissions or address problem.
//
// t.TempDir() alone is not safe for this. It builds a path from TMPDIR, the
// test's full name and a counter, so the limit is breached by a long TMPDIR
// (a nix dev shell sets one), a long test name, or both — which makes it look
// like only *some* tests are broken, and only on *some* machines. See
// docs/limitations.md.
package testsock

import (
	"os"
	"path/filepath"
	"testing"
)

// maxLen is the smallest sun_path across the platforms this runs on (macOS,
// 104), minus room for the socket's own filename. Staying under the smaller
// limit everywhere keeps behaviour identical between them.
const maxLen = 90

// Path returns a path named `name` inside a directory that is removed when the
// test ends, chosen so the total length can hold a unix socket.
//
// It prefers t.TempDir(), so the usual per-test isolation and cleanup apply,
// and only falls back to a short directory when that would be too long.
func Path(tb testing.TB, name string) string {
	tb.Helper()

	if p := filepath.Join(tb.TempDir(), name); len(p) <= maxLen {
		return p
	}

	// Fall back to the shortest base that exists. /tmp is not honoured via
	// TMPDIR here on purpose: TMPDIR being long is the thing being worked
	// around.
	base := "/tmp"
	if _, err := os.Stat(base); err != nil {
		base = os.TempDir()
	}

	dir, err := os.MkdirTemp(base, "nc")
	if err != nil {
		tb.Fatalf("creating a short socket directory under %s: %v", base, err)
	}
	tb.Cleanup(func() { _ = os.RemoveAll(dir) })

	p := filepath.Join(dir, name)
	if len(p) > maxLen {
		tb.Fatalf("socket path %q is %d bytes, over the %d-byte limit even under %s",
			p, len(p), maxLen, base)
	}
	return p
}
