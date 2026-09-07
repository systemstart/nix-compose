package cli

import (
	"strings"
	"testing"

	"github.com/systemstart/nix-compose/pkg/k8s"
)

// TestPolicyFlagParsers covers both render policy flags in one table: they
// have the same shape — an exact-match enum, an error naming the flag and its
// accepted values — and asserting them separately is the same test twice.
func TestPolicyFlagParsers(t *testing.T) {
	mount := func(v string) (string, error) { p, err := mountPolicy(v); return string(p), err }
	secret := func(v string) (string, error) { p, err := secretMaterialPolicy(v); return string(p), err }

	tests := []struct {
		name    string
		flag    string
		parse   func(string) (string, error)
		input   string
		want    string
		wantErr bool
	}{
		{"mount error", "unrepresentable-mounts", mount, "error", string(k8s.MountPolicyError), false},
		{"mount empty-dir", "unrepresentable-mounts", mount, "empty-dir", string(k8s.MountPolicyEmptyDir), false},
		{"mount camel case", "unrepresentable-mounts", mount, "emptyDir", "", true},
		{"mount empty", "unrepresentable-mounts", mount, "", "", true},
		{"secret error", "secret-material", secret, "error", string(k8s.SecretMaterialError), false},
		{"secret configmap", "secret-material", secret, "configmap", string(k8s.SecretMaterialConfigMap), false},
		{"secret camel case", "secret-material", secret, "ConfigMap", "", true},
		{"secret empty", "secret-material", secret, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parsing %q gave %q, want an error", tt.input, got)
				}
				if !strings.Contains(err.Error(), tt.flag) {
					t.Errorf("error %q does not name --%s", err, tt.flag)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsing %q: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("parsing %q = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestRenderCmd_PolicyDefaults pins the defaults, which are the whole point:
// both flags exist so a project can opt out of a refusal, never so a refusal
// has to be opted into.
func TestRenderCmd_PolicyDefaults(t *testing.T) {
	for flag, want := range map[string]string{
		"unrepresentable-mounts": string(k8s.MountPolicyError),
		"secret-material":        string(k8s.SecretMaterialError),
	} {
		f := renderCmd.Flags().Lookup(flag)
		if f == nil {
			t.Errorf("render has no --%s flag", flag)
			continue
		}
		if f.DefValue != want {
			t.Errorf("--%s default = %q, want %q", flag, f.DefValue, want)
		}
	}
}
