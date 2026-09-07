package k8s

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	// maxLabelLen is the length limit of an RFC 1123 label, which is what
	// the API server validates volume and object names against.
	maxLabelLen = 63
	// hashLen is the length of the disambiguating suffix appended when a
	// name is truncated or would collide.
	hashLen = 8
)

// sanitizeName converts an arbitrary string into a valid RFC 1123 label:
// lower case, every run of disallowed characters collapsed to a single "-",
// no leading or trailing "-", and no longer than 63 characters.
//
// Truncation and the empty result both take a suffix derived from the input,
// so two long paths that share a prefix do not end up with the same name.
func sanitizeName(s string) string {
	var b strings.Builder
	dashed := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dashed = false
			continue
		}
		if !dashed {
			b.WriteByte('-')
			dashed = true
		}
	}

	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "v-" + shortHash(s)
	}
	if len(name) > maxLabelLen {
		name = withSuffix(name[:maxLabelLen-hashLen-1], shortHash(s))
	}
	return name
}

// withSuffix joins a (possibly truncated) name to a hash suffix, keeping the
// result a valid label even when the truncation landed on a "-".
func withSuffix(name, suffix string) string {
	name = strings.Trim(name, "-")
	if name == "" {
		return "v-" + suffix
	}
	if len(name)+len(suffix)+1 > maxLabelLen {
		name = strings.Trim(name[:maxLabelLen-len(suffix)-1], "-")
	}
	return name + "-" + suffix
}

// shortHash returns a stable short hex digest of s, used to disambiguate
// names that sanitization would otherwise collapse together.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:hashLen]
}

// configMapKey converts a filename into a valid ConfigMap key, which allows
// alphanumerics, "-", "_" and "." but is not a label.
func configMapKey(filename string) string {
	var b strings.Builder
	for _, r := range filename {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	key := strings.Trim(b.String(), ".")
	if key == "" {
		return "file-" + shortHash(filename)
	}
	return key
}
