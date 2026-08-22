// Package nixsrc embeds the Nix sources that YAML-mode evaluation needs.
//
// A flake project reaches nix/lib.nix through the nix-compose flake input, and
// a compose.nix project imports whatever it likes. A nix-compose.yaml project
// has no Nix file at all, so the same sources travel inside the binary and are
// written out next to the evaluation that needs them.
//
// They are the *same* files the flake exposes, deliberately: YAML mode is a
// front-end onto nix/lib.nix, not a second implementation of it.
package nixsrc

import "embed"

// Files holds lib.nix and yaml.nix. yaml.nix imports nothing, so writing both
// into one directory is enough to evaluate a document.
//
//go:embed lib.nix yaml.nix
var Files embed.FS
