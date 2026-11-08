// Package nixcompose holds what has to sit at the repository root to be
// embedded into the binary: flake.lock.
//
// An embed directive reaches only its own package's directory and below, and
// flake.lock lives at the root. pkg/nixpins reads its pins from it, so every
// build of nix-compose (the flake, nixpkgs' package of a release tag,
// goreleaser, go install) carries exactly the revisions its flake.lock locks.
package nixcompose

import _ "embed" // for go:embed

// FlakeLock is the repository's flake.lock, as it was when the binary was
// built.
//
//go:embed flake.lock
var FlakeLock []byte
