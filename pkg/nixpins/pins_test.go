package nixpins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPinsAreTheLocksRevisions: the pins are what the repository's flake.lock
// locks for the flake's own nixpkgs and nix-oci inputs, read here
// independently of lockedPins. Only those two root inputs are decoded, so a
// follows elsewhere in the lock does not matter (this repository's lock has
// none for them; lockedPins' own follows handling is tested below).
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
	var rootInputs map[string]json.RawMessage
	if err := json.Unmarshal(lock.Nodes[lock.Root]["inputs"], &rootInputs); err != nil {
		t.Fatalf("root inputs: %v", err)
	}
	for input, got := range map[string]string{"nixpkgs": NixpkgsRef(), "nix-oci": NixOCIRef()} {
		var nodeName string
		if err := json.Unmarshal(rootInputs[input], &nodeName); err != nil {
			t.Fatalf("%s input: %v", input, err)
		}
		var locked struct{ Owner, Repo, Rev string }
		if err := json.Unmarshal(lock.Nodes[nodeName]["locked"], &locked); err != nil {
			t.Fatalf("%s locked: %v", input, err)
		}
		if want := "github:" + locked.Owner + "/" + locked.Repo + "/" + locked.Rev; got != want || len(locked.Rev) != 40 {
			t.Errorf("%s ref is %q, flake.lock locks %q", input, got, want)
		}
	}
	if NixpkgsRev != nixpkgs.Rev || NixOCIRev != nixOCI.Rev {
		t.Error("NixpkgsRev/NixOCIRev are not the pins' revisions")
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

func TestLockedPins(t *testing.T) {
	const a, b = "0123456789abcdef0123456789abcdef01234567", "89abcdef0123456789abcdef0123456789abcdef"
	gh := func(owner, repo, rev string) string {
		return `"locked":{"type":"github","owner":"` + owner + `","repo":"` + repo + `","rev":"` + rev + `"}`
	}
	lock := func(nodes string) []byte {
		return []byte(`{"root":"root","version":7,"nodes":{` + nodes + `}}`)
	}
	// nixpkgs follows c1, c1 follows c2, ... c39 follows c40, which is the node:
	// longer than any depth cap, and no cycle.
	var chain strings.Builder
	chain.WriteString(`"nixpkgs":["c1"]`)
	for i := 1; i < 40; i++ {
		fmt.Fprintf(&chain, `,"c%d":["c%d"]`, i, i+1)
	}
	chain.WriteString(`,"c40":"nixpkgs","nix-oci":"nix-oci"`)
	for _, tc := range []struct {
		name, nodes string
		want        []string // refs
		err         string
	}{
		{
			name: "through the root's inputs, not by node name",
			nodes: `"root":{"inputs":{"nixpkgs":"nixpkgs_2","nix-oci":"nix-oci"}},
				"nixpkgs":{` + gh("NixOS", "nixpkgs", b) + `},
				"nixpkgs_2":{` + gh("NixOS", "nixpkgs", a) + `},
				"nix-oci":{` + gh("nix-gizmos", "nix-oci", b) + `}`,
			want: []string{"github:NixOS/nixpkgs/" + a, "github:nix-gizmos/nix-oci/" + b},
		},
		{
			name: "the locked repository, a fork's included",
			nodes: `"root":{"inputs":{"nixpkgs":"nixpkgs","nix-oci":"nix-oci"}},
				"nixpkgs":{` + gh("NixOS", "nixpkgs", a) + `},
				"nix-oci":{` + gh("someone", "nix-oci-fork", b) + `}`,
			want: []string{"github:NixOS/nixpkgs/" + a, "github:someone/nix-oci-fork/" + b},
		},
		{
			name: "an input that follows another resolves to that one",
			nodes: `"root":{"inputs":{"nixpkgs":["nix-oci","nixpkgs"],"nix-oci":"nix-oci"}},
				"nix-oci":{"inputs":{"nixpkgs":"nixpkgs"},` + gh("nix-gizmos", "nix-oci", b) + `},
				"nixpkgs":{` + gh("NixOS", "nixpkgs", a) + `}`,
			want: []string{"github:NixOS/nixpkgs/" + a, "github:nix-gizmos/nix-oci/" + b},
		},
		{
			name: "a follows path through another follows",
			nodes: `"root":{"inputs":{"nixpkgs":["x","nixpkgs"],"x":["nix-oci"],"nix-oci":"nix-oci"}},
				"nix-oci":{"inputs":{"nixpkgs":"nixpkgs"},` + gh("nix-gizmos", "nix-oci", b) + `},
				"nixpkgs":{` + gh("NixOS", "nixpkgs", a) + `}`,
			want: []string{"github:NixOS/nixpkgs/" + a, "github:nix-gizmos/nix-oci/" + b},
		},
		{
			name: "a long follows chain",
			nodes: `"root":{"inputs":{` + chain.String() + `}},
				"nixpkgs":{` + gh("NixOS", "nixpkgs", a) + `},"nix-oci":{` + gh("nix-gizmos", "nix-oci", b) + `}`,
			want: []string{"github:NixOS/nixpkgs/" + a, "github:nix-gizmos/nix-oci/" + b},
		},
		{
			name: "dir and host qualifiers are kept",
			nodes: `"root":{"inputs":{"nixpkgs":"nixpkgs","nix-oci":"nix-oci"}},
				"nixpkgs":{` + gh("NixOS", "nixpkgs", a) + `},
				"nix-oci":{"locked":{"type":"github","owner":"o","repo":"r","rev":"` + b + `","dir":"sub/flake","host":"git.example.org"}}`,
			want: []string{"github:NixOS/nixpkgs/" + a, "github:o/r/" + b + "?dir=sub%2Fflake&host=git.example.org"},
		},
		{
			name:  "a follows cycle",
			nodes: `"root":{"inputs":{"nixpkgs":["nix-oci"],"nix-oci":["nixpkgs"]}}`,
			err:   "follows cycle",
		},
		{
			name:  "missing input",
			nodes: `"root":{"inputs":{"nixpkgs":"nixpkgs"}},"nixpkgs":{` + gh("NixOS", "nixpkgs", a) + `}`,
			err:   `has no input "nix-oci"`,
		},
		{
			name: "not a github input",
			nodes: `"root":{"inputs":{"nixpkgs":"nixpkgs","nix-oci":"nix-oci"}},
				"nixpkgs":{` + gh("NixOS", "nixpkgs", a) + `},
				"nix-oci":{"locked":{"type":"git","url":"https://example.org/nix-oci","rev":"` + b + `"}}`,
			err: `not a github owner/repo`,
		},
		{
			name: "no full revision",
			nodes: `"root":{"inputs":{"nixpkgs":"nixpkgs","nix-oci":"nix-oci"}},
				"nixpkgs":{` + gh("NixOS", "nixpkgs", a) + `},"nix-oci":{` + gh("nix-gizmos", "nix-oci", "main") + `}`,
			err: `no full locked revision`,
		},
		{name: "no root node", nodes: `"x":{}`, err: `no root node`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := lockedPins(lock(tc.nodes), "nixpkgs", "nix-oci")
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want one containing %q", err, tc.err)
				}
				return
			}
			if err != nil || len(got) != 2 || got[0].ref() != tc.want[0] || got[1].ref() != tc.want[1] {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	if _, err := lockedPins([]byte(`{`)); err == nil {
		t.Error("malformed JSON: want an error")
	}
}
