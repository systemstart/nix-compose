package k8s

import (
	"regexp"
	"strings"
	"testing"
)

// rfc1123Label is the regex the API server validates volume and object names
// against.
var rfc1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"named volume unchanged", "db-data", "db-data"},
		{"absolute path", "/var/lib/data", "var-lib-data"},
		{"single segment", "/data", "data"},
		{"relative path keeps no leading dot", "./container/files/auth/hydra/hydra.yml", "container-files-auth-hydra-hydra-yml"},
		{"upper case folded", "Config.YAML", "config-yaml"},
		{"runs collapse", "a//b__c", "a-b-c"},
		{"non-ascii", "конфиг", "v-c08f7822"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeName(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeName(%q) = %q, want %q", tt.input, got, tt.want)
			}
			if !rfc1123Label.MatchString(got) {
				t.Errorf("sanitizeName(%q) = %q, which is not an RFC 1123 label", tt.input, got)
			}
		})
	}
}

func TestSanitizeName_TruncatesToValidLabel(t *testing.T) {
	long := "./" + strings.Repeat("very-long-directory/", 8) + "config.yml"
	got := sanitizeName(long)

	if len(got) > maxLabelLen {
		t.Errorf("sanitizeName(long) = %q (%d chars), want at most %d", got, len(got), maxLabelLen)
	}
	if !rfc1123Label.MatchString(got) {
		t.Errorf("sanitizeName(long) = %q, which is not an RFC 1123 label", got)
	}
	// A different path sharing the truncated prefix must not collide.
	other := sanitizeName(long + ".bak")
	if got == other {
		t.Errorf("two distinct long paths both sanitized to %q", got)
	}
}

func TestConfigMapKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hydra.yml", "hydra.yml"},
		{"my config.yaml", "my-config.yaml"},
		{"..", "file-5ec1f7e7"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := configMapKey(tt.input); got != tt.want {
				t.Errorf("configMapKey(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
