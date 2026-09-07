package cli

import (
	"strings"
	"testing"

	"github.com/systemstart/nix-compose/pkg/k8s"
)

func TestMountPolicy(t *testing.T) {
	tests := []struct {
		input   string
		want    k8s.MountPolicy
		wantErr bool
	}{
		{input: "error", want: k8s.MountPolicyError},
		{input: "empty-dir", want: k8s.MountPolicyEmptyDir},
		{input: "emptyDir", wantErr: true},
		{input: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := mountPolicy(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("mountPolicy(%q) = %q, want an error", tt.input, got)
				}
				if !strings.Contains(err.Error(), "unrepresentable-mounts") {
					t.Errorf("error %q does not name the flag", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("mountPolicy(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("mountPolicy(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRenderCmd_UnrepresentableMountsFlagDefault(t *testing.T) {
	f := renderCmd.Flags().Lookup("unrepresentable-mounts")
	if f == nil {
		t.Fatal("render has no --unrepresentable-mounts flag")
	}
	if f.DefValue != string(k8s.MountPolicyError) {
		t.Errorf("--unrepresentable-mounts default = %q, want %q", f.DefValue, k8s.MountPolicyError)
	}
}
