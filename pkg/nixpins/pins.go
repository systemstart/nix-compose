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
// nixcompose package at the repository root), so they are always what the
// build's flake.lock locks: repository and revision both. They used to be a
// copy in this file, which Renovate's lock-file maintenance moved past
// without updating.
package nixpins

import (
	"encoding/json"
	"fmt"
	"regexp"

	nixcompose "github.com/systemstart/nix-compose"
)

var (
	// NixpkgsRev is the nixpkgs revision `package:` resolves against by
	// default.
	NixpkgsRev string

	// NixOCIRev is the nix-oci revision that supplies buildOCIImage, which
	// turns a package's closure into an OCI image (ADR-006, ADR-015).
	NixOCIRev string

	nixpkgs, nixOCI pin
)

func init() {
	pins, err := lockedPins(nixcompose.FlakeLock, "nixpkgs", "nix-oci")
	if err != nil {
		// The embedded lock is part of the build: one this cannot read is a
		// broken build, which the package's tests catch before a release.
		panic("nixpins: embedded flake.lock: " + err.Error())
	}
	nixpkgs, nixOCI = pins[0], pins[1]
	NixpkgsRev, NixOCIRev = nixpkgs.Rev, nixOCI.Rev
}

// pin is a locked GitHub flake input: repository and revision.
type pin struct{ Owner, Repo, Rev string }

// ref is the flake reference of exactly what the lock locked. Pinned to a full
// revision rather than a branch so `builtins.getFlake` stays pure; built from
// the locked repository too, so a fork's revision is looked up in the fork.
func (p pin) ref() string { return "github:" + p.Owner + "/" + p.Repo + "/" + p.Rev }

type lockNode struct {
	Inputs map[string]json.RawMessage `json:"inputs"`
	Locked struct {
		Type  string `json:"type"`
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		Rev   string `json:"rev"`
	} `json:"locked"`
}

var fullRev = regexp.MustCompile(`^[0-9a-f]{40}$`)

// lockedPins returns what the lock locks for each named input of the root
// flake. Inputs are resolved through the root node, not looked up by node
// name (nix renames colliding nodes: nixpkgs_2), and an input that `follows`
// another is resolved to the node it follows.
func lockedPins(lock []byte, inputs ...string) ([]pin, error) {
	var l struct {
		Root  string              `json:"root"`
		Nodes map[string]lockNode `json:"nodes"`
	}
	if err := json.Unmarshal(lock, &l); err != nil {
		return nil, fmt.Errorf("parsing: %w", err)
	}
	if _, ok := l.Nodes[l.Root]; !ok {
		return nil, fmt.Errorf("no root node %q", l.Root)
	}
	pins := make([]pin, 0, len(inputs))
	for _, in := range inputs {
		name, err := resolve(l.Nodes, l.Root, []string{in}, 0)
		if err != nil {
			return nil, fmt.Errorf("input %q: %w", in, err)
		}
		locked := l.Nodes[name].Locked
		if locked.Type != "github" || locked.Owner == "" || locked.Repo == "" {
			return nil, fmt.Errorf("input %q (node %q) is locked as %q, not a github owner/repo: its reference cannot be rebuilt from the lock", in, name, locked.Type)
		}
		if !fullRev.MatchString(locked.Rev) {
			return nil, fmt.Errorf("input %q (node %q) has no full locked revision: %q", in, name, locked.Rev)
		}
		pins = append(pins, pin{locked.Owner, locked.Repo, locked.Rev})
	}
	return pins, nil
}

// resolve follows an input path from the root, as nix does: each step takes
// the named input of the current node; an input that is itself a path
// (`follows`) is resolved from the root again. depth bounds a cyclic lock.
func resolve(nodes map[string]lockNode, root string, path []string, depth int) (string, error) {
	if depth > 32 {
		return "", fmt.Errorf("follows chain too deep (a cycle?)")
	}
	node := root
	for _, step := range path {
		raw, ok := nodes[node].Inputs[step]
		if !ok {
			return "", fmt.Errorf("node %q has no input %q", node, step)
		}
		var name string
		if err := json.Unmarshal(raw, &name); err == nil {
			node = name
			continue
		}
		var follows []string
		if err := json.Unmarshal(raw, &follows); err != nil {
			return "", fmt.Errorf("node %q input %q is neither a node nor a follows path", node, step)
		}
		next, err := resolve(nodes, root, follows, depth+1)
		if err != nil {
			return "", err
		}
		node = next
	}
	if _, ok := nodes[node]; !ok {
		return "", fmt.Errorf("no node %q", node)
	}
	return node, nil
}

// NixpkgsRef returns the default nixpkgs flake reference.
func NixpkgsRef() string { return nixpkgs.ref() }

// NixOCIRef returns the nix-oci flake reference.
func NixOCIRef() string { return nixOCI.ref() }
