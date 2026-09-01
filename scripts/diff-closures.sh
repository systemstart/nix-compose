#!/usr/bin/env bash
# Show what a flake.lock change actually does to what this repo builds.
#
# `nix flake update` reports which inputs moved, but not whether the move
# reaches anything we ship: a two-day nixpkgs bump is ~1000 upstream commits
# that usually touch nothing in this closure. This answers the question the
# lock diff cannot -- it re-evaluates an attribute against the *previously
# locked* revision of an input and compares the result with the working tree's.
#
# Cheap by default: derivation paths are compared first (evaluation only), and
# nothing is built unless they actually differ. An unchanged .drv is proof the
# update is inert for that attribute -- no build required to know it.
#
# Usage:
#   scripts/diff-closures.sh [-a ATTR] [-i INPUT] [-r OLD_REV] [-p PKG]... [-b]
#
#   -a ATTR    flake attribute to compare, without the leading '.#'
#              (default: packages.<current-system>.default)
#   -i INPUT   flake input to roll back (default: nixpkgs)
#   -r OLD_REV compare against this revision of INPUT instead of the one
#              committed at HEAD
#   -p PKG     also report PKG.version on both sides (repeatable), e.g.
#              -p openssl. Use it for packages whose version is load-bearing.
#              (The Go toolchain is not one of these: it comes from the
#              go-overlay input, so compare it with -i go-overlay.)
#   -b         build and diff closures even when the derivations match
#
# Informational: the exit status reports whether the comparison ran, not
# whether anything differed.
set -euo pipefail

die() {
    echo "diff-closures: $*" >&2
    exit 1
}

usage() {
    sed -n '/^# Usage:/,/^# Informational/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

attr=""
input="nixpkgs"
old_rev=""
force_build=0
witness=()

while getopts ":a:i:r:p:bh" opt; do
    case "$opt" in
        a) attr="$OPTARG" ;;
        i) input="$OPTARG" ;;
        r) old_rev="$OPTARG" ;;
        p) witness+=("$OPTARG") ;;
        b) force_build=1 ;;
        h) usage; exit 0 ;;
        :) die "-$OPTARG needs an argument" ;;
        *) die "unknown flag -$OPTARG (try -h)" ;;
    esac
done

for tool in nix jq git; do
    command -v "$tool" >/dev/null || die "$tool is not on PATH (run inside \`nix develop\`)"
done
[ -f flake.lock ] || die "no flake.lock in $root"

# Renders the flake reference for one revision of INPUT. Only the input types
# this repo actually uses are handled; anything else is an explicit error
# rather than a silently wrong reference.
input_ref() {
    local lock="$1" rev="$2" type owner repo url
    type="$(jq -r --arg i "$input" '.nodes[$i].locked.type // empty' "$lock")"
    case "$type" in
        github|gitlab)
            owner="$(jq -r --arg i "$input" '.nodes[$i].locked.owner' "$lock")"
            repo="$(jq -r --arg i "$input" '.nodes[$i].locked.repo' "$lock")"
            printf '%s:%s/%s/%s' "$type" "$owner" "$repo" "$rev"
            ;;
        git)
            url="$(jq -r --arg i "$input" '.nodes[$i].locked.url' "$lock")"
            printf 'git+%s?rev=%s' "$url" "$rev"
            ;;
        *) die "input '$input' has unsupported type '${type:-none}'" ;;
    esac
}

# `--override-input` on a locked flake makes Nix announce, in four lines, that
# it is not writing the lock file -- restating the override this script has
# already printed. Filter that noise from the evaluation steps, but only when
# the command succeeds: a failure prints its stderr untouched.
run_quiet() {
    local err rc=0
    err="$(mktemp)"
    "$@" 2>"$err" || rc=$?
    [ "$rc" -eq 0 ] || cat "$err" >&2
    rm -f "$err"
    return "$rc"
}

locked_field() {
    jq -r --arg i "$input" --arg f "$2" '.nodes[$i].locked[$f] // empty' "$1"
}

# HEAD's lock is the baseline: "what did my working-tree flake update change?"
head_lock="$(mktemp)"
trap 'rm -f "$head_lock"' EXIT
git show HEAD:flake.lock > "$head_lock" 2>/dev/null ||
    die "cannot read flake.lock at HEAD"

new_rev="$(locked_field flake.lock rev)"
[ -n "$new_rev" ] || die "flake.lock has no locked revision for input '$input'"

baseline="$head_lock"
old_ts=""
if [ -n "$old_rev" ]; then
    # -r replaces the revision only, not the input's identity, and carries no
    # timestamp of its own -- so the date is left blank rather than borrowed.
    baseline=flake.lock
else
    old_rev="$(locked_field "$head_lock" rev)"
    [ -n "$old_rev" ] || die "HEAD's flake.lock has no revision for input '$input'"
    old_ts="$(locked_field "$head_lock" lastModified)"
fi

human_date() {
    local ts="$1"
    [ -n "$ts" ] || { printf 'unknown'; return; }
    date -u -d "@$ts" +%F 2>/dev/null || date -u -r "$ts" +%F 2>/dev/null || printf '%s' "$ts"
}

system="$(nix eval --raw --impure --expr builtins.currentSystem)"
attr="${attr:-packages.$system.default}"

old_ref="$(input_ref "$baseline" "$old_rev")"
new_ref="$(input_ref flake.lock "$new_rev")"

printf 'input : %s\n' "$input"
printf 'old   : %s (%s)\n' "${old_rev:0:12}" "$(human_date "$old_ts")"
printf 'new   : %s (%s)\n' "${new_rev:0:12}" "$(human_date "$(locked_field flake.lock lastModified)")"
printf 'attr  : .#%s\n\n' "$attr"

if [ "$old_rev" = "$new_rev" ]; then
    echo "$input is unchanged against HEAD — nothing to compare."
    exit 0
fi

# Versions of individually load-bearing packages, reported before the closure
# diff because a single name is easier to act on than a list of store paths.
for pkg in ${witness+"${witness[@]}"}; do
    old_v="$(nix eval --raw "$old_ref#$pkg.version" 2>/dev/null || echo '?')"
    new_v="$(nix eval --raw "$new_ref#$pkg.version" 2>/dev/null || echo '?')"
    if [ "$old_v" = "$new_v" ]; then
        printf '%-14s %s (unchanged)\n' "$pkg:" "$old_v"
    else
        printf '%-14s %s -> %s  ** CHANGED **\n' "$pkg:" "$old_v" "$new_v"
    fi
done
[ ${#witness[@]} -eq 0 ] || echo

# Evaluation-only comparison first: same .drv means same output, bit for bit.
new_drv="$(run_quiet nix eval --raw ".#$attr.drvPath")"
old_drv="$(run_quiet nix eval --raw --no-write-lock-file \
    --override-input "$input" "$old_ref" ".#$attr.drvPath")"

if [ "$old_drv" = "$new_drv" ]; then
    printf 'derivation identical: %s\n' "$new_drv"
    if [ "$force_build" -eq 0 ]; then
        printf 'the %s bump is inert for .#%s — nothing rebuilds.\n' "$input" "$attr"
        exit 0
    fi
else
    printf 'derivations differ:\n  old %s\n  new %s\n' "$old_drv" "$new_drv"
fi

echo
echo "building both sides (this can take a while)..."
new_out="$(nix build --no-link --print-out-paths ".#$attr")"
old_out="$(nix build --no-link --print-out-paths --no-write-lock-file \
    --override-input "$input" "$old_ref" ".#$attr")"

echo
if [ "$old_out" = "$new_out" ]; then
    printf 'output paths identical: %s\n' "$new_out"
    exit 0
fi

printf 'nix store diff-closures %s %s\n\n' "$old_out" "$new_out"
nix store diff-closures "$old_out" "$new_out"
