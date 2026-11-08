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
	"net/url"
	"regexp"
	"strings"

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

// pin is a locked GitHub flake input: repository, revision, and the optional
// `dir` (a flake in a subdirectory) and `host` (GitHub Enterprise) qualifiers.
type pin struct{ Owner, Repo, Rev, Dir, Host string }

// ref is the flake reference of exactly what the lock locked. Pinned to a full
// revision rather than a branch so `builtins.getFlake` stays pure; built from
// the locked repository too, so a fork's revision is looked up in the fork.
// `dir` and `host` are carried over when the lock has them.
func (p pin) ref() string {
	ref := "github:" + p.Owner + "/" + p.Repo + "/" + p.Rev
	q := url.Values{}
	if p.Dir != "" {
		q.Set("dir", p.Dir)
	}
	if p.Host != "" {
		q.Set("host", p.Host)
	}
	if len(q) > 0 {
		ref += "?" + q.Encode()
	}
	return ref
}

type lockNode struct {
	Inputs map[string]json.RawMessage `json:"inputs"`
	Locked struct {
		Type  string `json:"type"`
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		Rev   string `json:"rev"`
		Dir   string `json:"dir"`
		Host  string `json:"host"`
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
		name, err := resolve(l.Nodes, l.Root, []string{in}, map[string]bool{})
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
		pins = append(pins, pin{locked.Owner, locked.Repo, locked.Rev, locked.Dir, locked.Host})
	}
	return pins, nil
}

// resolve follows an input path from the root, as nix does: each step takes
// the named input of the current node; an input that is itself a path
// (`follows`) is resolved from the root again. Resolution is deterministic, so a
// follows path met again while it is still being resolved is a cycle; inFlight
// holds those paths. Chains of any length that do not loop resolve.
func resolve(nodes map[string]lockNode, root string, path []string, inFlight map[string]bool) (string, error) {
	key := strings.Join(path, "\x00") // input names are arbitrary strings
	if inFlight[key] {
		return "", fmt.Errorf("follows cycle through %q", strings.Join(path, "/"))
	}
	inFlight[key] = true
	defer delete(inFlight, key)
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
		next, err := resolve(nodes, root, follows, inFlight)
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
