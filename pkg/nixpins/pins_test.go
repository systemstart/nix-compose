package nixpins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPinsAreTheLocksRevisions: the pins are the revisions the repository's
// flake.lock locks for the flake's own nixpkgs and nix-oci inputs, read here
// independently of lockedRevs (with jq-like plain decoding of the file).
func TestPinsAreTheLocksRevisions(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "flake.lock"))
	if err != nil {
		t.Fatalf("reading flake.lock: %v", err)
	}
	var lock struct {
		Root  string
		Nodes map[string]map[string]json.RawMessage
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatalf("parsing flake.lock: %v", err)
	}
	var rootInputs map[string]string
	if err := json.Unmarshal(lock.Nodes[lock.Root]["inputs"], &rootInputs); err != nil {
		t.Fatalf("root inputs: %v", err)
	}
	for input, got := range map[string]string{"nixpkgs": NixpkgsRev, "nix-oci": NixOCIRev} {
		var locked struct{ Rev string }
		if err := json.Unmarshal(lock.Nodes[rootInputs[input]]["locked"], &locked); err != nil {
			t.Fatalf("%s locked: %v", input, err)
		}
		if got != locked.Rev || len(got) != 40 {
			t.Errorf("%s pin is %q, flake.lock locks %q", input, got, locked.Rev)
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

func TestLockedRevs(t *testing.T) {
	const a, b = "0123456789abcdef0123456789abcdef01234567", "89abcdef0123456789abcdef0123456789abcdef"
	lock := func(nodes string) []byte {
		return []byte(`{"root":"root","version":7,"nodes":{` + nodes + `}}`)
	}
	for _, tc := range []struct {
		name, nodes string
		want        []string
		err         string
	}{
		{
			name: "through the root's inputs, not by node name",
			nodes: `"root":{"inputs":{"nixpkgs":"nixpkgs_2","nix-oci":"nix-oci"}},
				"nixpkgs":{"locked":{"rev":"` + b + `"}},
				"nixpkgs_2":{"locked":{"rev":"` + a + `"}},
				"nix-oci":{"locked":{"rev":"` + b + `"}}`,
			want: []string{a, b},
		},
		{
			name:  "missing input",
			nodes: `"root":{"inputs":{"nixpkgs":"nixpkgs"}},"nixpkgs":{"locked":{"rev":"` + a + `"}}`,
			err:   `no input "nix-oci"`,
		},
		{
			name: "input that follows another",
			nodes: `"root":{"inputs":{"nixpkgs":"nixpkgs","nix-oci":["x","nixpkgs"]}},
				"nixpkgs":{"locked":{"rev":"` + a + `"}}`,
			err: `"nix-oci" follows another input`,
		},
		{
			name: "no full revision",
			nodes: `"root":{"inputs":{"nixpkgs":"nixpkgs","nix-oci":"nix-oci"}},
				"nixpkgs":{"locked":{"rev":"` + a + `"}},"nix-oci":{"locked":{"rev":"main"}}`,
			err: `no full locked revision`,
		},
		{name: "no root node", nodes: `"x":{}`, err: `no root node`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := lockedRevs(lock(tc.nodes), "nixpkgs", "nix-oci")
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want one containing %q", err, tc.err)
				}
				return
			}
			if err != nil || len(got) != 2 || got[0] != tc.want[0] || got[1] != tc.want[1] {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	if _, err := lockedRevs([]byte(`{`)); err == nil {
		t.Error("malformed JSON: want an error")
	}
}
