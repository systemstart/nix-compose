package eval

import "strings"

// MountOptionsReadOnly reports whether the option field of a compose volume
// string ("source:dest:options") marks the mount read-only.
//
// Options are a comma-separated list, so "ro", "ro,z" and "z,ro" are all
// read-only while "rw", "z" and "" are not. Comparing the field to "ro"
// whole silently drops the read-only flag from any mount that also carries
// an SELinux label or a propagation option.
func MountOptionsReadOnly(options string) bool {
	for _, opt := range strings.Split(options, ",") {
		if strings.TrimSpace(opt) == "ro" {
			return true
		}
	}
	return false
}
