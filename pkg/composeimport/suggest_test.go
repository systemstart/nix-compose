package composeimport

import (
	"slices"
	"testing"
)

func TestImageCandidates(t *testing.T) {
	tests := []struct {
		image string
		want  []string
	}{
		{"nginx", []string{"nginx"}},
		{"nginx:1.27", []string{"nginx"}},
		{"docker.io/library/nginx:1.27", []string{"nginx"}},
		{"redis:7-alpine", []string{"redis"}},
		{"nginx@sha256:abc123", []string{"nginx"}},

		// A colon before the last slash is a registry port, not a tag.
		{"registry.example.com:5000/team/myapp:2.1", []string{"myapp"}},

		// The aliases carry the weight: none of these names exist in
		// nixpkgs as written, and they are what people actually run.
		{"postgres:16", []string{"postgres", "postgresql"}},
		{"node:22", []string{"node", "nodejs"}},
		{"python:3.12", []string{"python", "python3"}},
	}

	for _, tc := range tests {
		t.Run(tc.image, func(t *testing.T) {
			got := ImageCandidates(tc.image)
			if !slices.Equal(got, tc.want) {
				t.Errorf("ImageCandidates(%q) = %v, want %v", tc.image, got, tc.want)
			}
		})
	}
}

func TestTagOf(t *testing.T) {
	tests := map[string]string{
		"nginx:1.27":                        "1.27",
		"nginx":                             "",
		"redis:7-alpine":                    "7-alpine",
		"registry.example.com:5000/app":     "",
		"registry.example.com:5000/app:2.1": "2.1",
		"nginx@sha256:abc":                  "",
	}
	for image, want := range tests {
		if got := tagOf(image); got != want {
			t.Errorf("tagOf(%q) = %q, want %q", image, got, want)
		}
	}
}

func TestMajorOf(t *testing.T) {
	tests := map[string]string{
		"7-alpine": "7",
		"16":       "16",
		"1.27.3":   "1",
		"latest":   "",
		"":         "",
		"v2":       "", // no leading digit; not worth guessing at
	}
	for in, want := range tests {
		if got := majorOf(in); got != want {
			t.Errorf("majorOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestMajorMismatch guards the warning that stops a suggestion from being a
// silent major upgrade — nixpkgs redis is 8 while plenty of compose files
// still say redis:7.
func TestMajorMismatch(t *testing.T) {
	tests := []struct {
		name string
		s    Suggestion
		want bool
	}{
		{"same major", Suggestion{TagMajor: "16", PkgMajor: "16"}, false},
		{"different major", Suggestion{TagMajor: "7", PkgMajor: "8"}, true},
		{"unknown tag", Suggestion{TagMajor: "", PkgMajor: "8"}, false},
		{"unknown package version", Suggestion{TagMajor: "7", PkgMajor: ""}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.MajorMismatch(); got != tc.want {
				t.Errorf("MajorMismatch() = %v, want %v", got, tc.want)
			}
		})
	}
}
