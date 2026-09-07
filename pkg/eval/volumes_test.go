package eval

import "testing"

func TestMountOptionsReadOnly(t *testing.T) {
	tests := []struct {
		options string
		want    bool
	}{
		{"ro", true},
		{"ro,z", true},
		{"z,ro", true},
		{"ro,Z,cached", true},
		{"rw", false},
		{"z", false},
		{"", false},
		{"nocopy", false},
		{"read-only", false},
	}
	for _, tt := range tests {
		t.Run(tt.options, func(t *testing.T) {
			if got := MountOptionsReadOnly(tt.options); got != tt.want {
				t.Errorf("MountOptionsReadOnly(%q) = %v, want %v", tt.options, got, tt.want)
			}
		})
	}
}
