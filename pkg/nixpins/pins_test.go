package nixpins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestPinsMatchFlakeLock keeps the constants honest. They exist because an
// installed binary has no flake.lock to read, but a copy that silently drifts
// from the lock is worse than no copy: YAML projects would resolve packages
// against a nixpkgs the rest of the repo stopped using.
func TestPinsMatchFlakeLock(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "flake.lock"))
	if err != nil {
		t.Fatalf("reading flake.lock: %v", err)
	}

	var lock struct {
		Nodes map[string]struct {
			Locked struct {
				Rev string `json:"rev"`
			} `json:"locked"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatalf("parsing flake.lock: %v", err)
	}

	for _, tc := range []struct {
		node string
		want string
	}{
		{"nixpkgs", NixpkgsRev},
		{"nix-oci", NixOCIRev},
	} {
		node, ok := lock.Nodes[tc.node]
		if !ok {
			t.Errorf("flake.lock has no %q node", tc.node)
			continue
		}
		if node.Locked.Rev != tc.want {
			t.Errorf("%s pin is stale: flake.lock has %s, nixpins has %s\n"+
				"run `nix flake update` and update pkg/nixpins/pins.go to match",
				tc.node, node.Locked.Rev, tc.want)
		}
	}
}

func TestRefsArePinnedToRevisions(t *testing.T) {
	// A branch ref would make builtins.getFlake impure, which would force
	// --impure on every YAML evaluation. Guard the property, not the string.
	for name, ref := range map[string]string{
		"nixpkgs": NixpkgsRef(),
		"nix-oci": NixOCIRef(),
	} {
		rev := ref[len(ref)-40:]
		for _, c := range rev {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				t.Errorf("%s ref %q does not end in a 40-char revision", name, ref)
				break
			}
		}
	}
}
