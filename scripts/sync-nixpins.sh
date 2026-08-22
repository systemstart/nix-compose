#!/usr/bin/env bash
# Rewrite pkg/nixpins/pins.go from flake.lock.
#
# The pins have to match flake.lock — TestPinsMatchFlakeLock fails when they
# drift — but nothing updates them automatically, so a `nix flake update` (or a
# Renovate lock-file-maintenance PR) leaves the tree red until someone edits
# the constants by hand. This does that edit.
#
# Idempotent: running it on an already-synced tree changes nothing.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
lock="$root/flake.lock"
pins="$root/pkg/nixpins/pins.go"

for f in "$lock" "$pins"; do
    [ -f "$f" ] || { echo "missing $f" >&2; exit 1; }
done

nixpkgs_rev="$(jq -r '.nodes.nixpkgs.locked.rev // empty' "$lock")"
nixoci_rev="$(jq -r '.nodes["nix-oci"].locked.rev // empty' "$lock")"

for pair in "nixpkgs:$nixpkgs_rev" "nix-oci:$nixoci_rev"; do
    name="${pair%%:*}"; rev="${pair#*:}"
    if [ -z "$rev" ]; then
        echo "flake.lock has no locked revision for $name" >&2
        exit 1
    fi
    # A truncated or branch-shaped value would produce an impure flake ref, so
    # insist on a full 40-character revision.
    if ! printf '%s' "$rev" | grep -Eq '^[0-9a-f]{40}$'; then
        echo "$name revision $rev is not a 40-character hash" >&2
        exit 1
    fi
done

sed -i \
    -e "s|^\(\s*NixpkgsRev = \)\"[0-9a-f]*\"|\1\"$nixpkgs_rev\"|" \
    -e "s|^\(\s*NixOCIRev = \)\"[0-9a-f]*\"|\1\"$nixoci_rev\"|" \
    "$pins"

if git -C "$root" diff --quiet -- "$pins"; then
    echo "nixpins already in step with flake.lock"
else
    echo "nixpins updated:"
    echo "  nixpkgs  $nixpkgs_rev"
    echo "  nix-oci  $nixoci_rev"
fi
