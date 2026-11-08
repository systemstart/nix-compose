// Package nixpins carries the flake revisions that YAML-mode projects are
// evaluated against.
//
// A flake or compose.nix project brings its own nixpkgs, so nix-compose never
// had to have an opinion about which one. A nix-compose.yaml project brings
// nothing — `package: nginx` has to mean *some* nginx — so the pins live here
// and travel with the binary. The consequence is worth stating plainly: in
// YAML mode the nix-compose version determines the package versions, the way a
// distro release does. `nixpkgs:` in the YAML overrides it per project.
//
// The pins are read from the flake.lock embedded in the binary (see the
// nixcompose package at the repository root), so they are always the
// revisions the build's flake.lock locks. They used to be a copy in this
// file, which Renovate's lock-file maintenance moved past without updating.
package nixpins

import (
	"encoding/json"
	"fmt"
	"regexp"

	nixcompose "github.com/systemstart/nix-compose"
)

var (
	// NixpkgsRev is the nixpkgs `package:` resolves against by default.
	NixpkgsRev string

	// NixOCIRev supplies buildOCIImage, which turns a package's closure into
	// an OCI image (ADR-006, ADR-015).
	NixOCIRev string
)

func init() {
	revs, err := lockedRevs(nixcompose.FlakeLock, "nixpkgs", "nix-oci")
	if err != nil {
		// The embedded lock is part of the build; a bad one is a broken build,
		// which the package's tests catch before a release does.
		panic("nixpins: embedded flake.lock: " + err.Error())
	}
	NixpkgsRev, NixOCIRev = revs[0], revs[1]
}

var fullRev = regexp.MustCompile(`^[0-9a-f]{40}$`)

// lockedRevs returns the locked revision of each named input of the lock's
// root flake. Inputs are looked up through the root node, not by node name:
// nix renames nodes when names collide (nixpkgs_2), the root's inputs map
// says which node is the flake's own.
func lockedRevs(lock []byte, inputs ...string) ([]string, error) {
	var l struct {
		Root  string `json:"root"`
		Nodes map[string]struct {
			Inputs map[string]json.RawMessage `json:"inputs"`
			Locked struct {
				Rev string `json:"rev"`
			} `json:"locked"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(lock, &l); err != nil {
		return nil, fmt.Errorf("parsing: %w", err)
	}
	root, ok := l.Nodes[l.Root]
	if !ok {
		return nil, fmt.Errorf("no root node %q", l.Root)
	}
	revs := make([]string, 0, len(inputs))
	for _, in := range inputs {
		raw, ok := root.Inputs[in]
		if !ok {
			return nil, fmt.Errorf("the flake has no input %q", in)
		}
		var nodeName string
		if err := json.Unmarshal(raw, &nodeName); err != nil {
			// A list is a `follows`: the input is another input's, not locked
			// for itself.
			return nil, fmt.Errorf("input %q follows another input instead of being locked itself", in)
		}
		rev := l.Nodes[nodeName].Locked.Rev
		if !fullRev.MatchString(rev) {
			return nil, fmt.Errorf("input %q (node %q) has no full locked revision: %q", in, nodeName, rev)
		}
		revs = append(revs, rev)
	}
	return revs, nil
}

// NixpkgsRef returns the default nixpkgs flake reference. It is pinned to a
// full revision rather than a branch so `builtins.getFlake` stays pure — an
// unlocked ref would force --impure on every evaluation.
func NixpkgsRef() string {
	return "github:NixOS/nixpkgs/" + NixpkgsRev
}

// NixOCIRef returns the nix-oci flake reference.
func NixOCIRef() string {
	return "github:systemstart/nix-oci/" + NixOCIRev
}
