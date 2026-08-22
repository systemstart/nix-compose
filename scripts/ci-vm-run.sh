#!/usr/bin/env bash
# ci-vm-run.sh — Boot a microVM, run a CI command inside it, report the exit code.
#
# Usage: scripts/ci-vm-run.sh [--workspace DIR] [--image DIR]
#
# The VM expects:
#   <workspace>/.ci-command  — script to execute inside the VM
# And will produce:
#   <workspace>/.ci-exit-code — exit code of the command
#
# Requirements on the host: cloud-hypervisor, virtiofsd, ip, iptables (for networking)
set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────
WORKSPACE="$PWD"
IMAGE=""
RUN_ID="ci-$$"
CLEANUP_PIDS=()
TAP_DEV=""
BRIDGE_DEV="ci-br0"
HAS_NETWORK=false

# ── Argument parsing ──────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --workspace) WORKSPACE="$2"; shift 2 ;;
    --image)     IMAGE="$2";     shift 2 ;;
    *)           echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done

WORKSPACE="$(cd "$WORKSPACE" && pwd -P)"

if [[ -z "$IMAGE" ]]; then
  echo "ci-vm-run: building CI VM image via nix..." >&2
  nix build .#ci-vm-image --out-link "${WORKSPACE}/.ci-vm-image"
  IMAGE="$(readlink -f "${WORKSPACE}/.ci-vm-image")"
else
  IMAGE="$(cd "$IMAGE" && pwd -P)"
fi

# Resolve kernel and rootfs paths through symlinks
KERNEL_PATH="$(readlink -f "$IMAGE/kernel")"
ROOTFS_PATH="$(readlink -f "$IMAGE/rootfs")"

if [[ ! -e "$KERNEL_PATH" ]] || [[ ! -e "$ROOTFS_PATH" ]]; then
  echo "ci-vm-run: ERROR — cannot resolve image contents" >&2
  echo "ci-vm-run: IMAGE=$IMAGE" >&2
  echo "ci-vm-run: KERNEL_PATH=$KERNEL_PATH" >&2
  echo "ci-vm-run: ROOTFS_PATH=$ROOTFS_PATH" >&2
  ls -la "$IMAGE" >&2 2>/dev/null || true
  exit 1
fi

# Verify .ci-command exists
if [[ ! -f "$WORKSPACE/.ci-command" ]]; then
  echo "ci-vm-run: ERROR — $WORKSPACE/.ci-command not found" >&2
  exit 1
fi

# ── Socket / temp paths ──────────────────────────────────────────────
SOCK_DIR="$(mktemp -d "/tmp/ci-vm-${RUN_ID}.XXXXXX")"
VFSD_WORKSPACE_SOCK="${SOCK_DIR}/virtiofs-workspace.sock"
VFSD_NIXSTORE_SOCK="${SOCK_DIR}/virtiofs-nixstore.sock"

# ── Cleanup ───────────────────────────────────────────────────────────
cleanup() {
  echo "ci-vm-run: cleaning up..." >&2

  # Kill child processes (virtiofsd, cloud-hypervisor)
  for pid in "${CLEANUP_PIDS[@]}"; do
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done

  # Tear down networking
  if [[ "$HAS_NETWORK" == "true" ]]; then
    if [[ -n "$TAP_DEV" ]]; then
      ip link set "$TAP_DEV" down 2>/dev/null || true
      ip link delete "$TAP_DEV" 2>/dev/null || true
    fi
    # Remove NAT rule
    iptables -t nat -D POSTROUTING -s 172.30.0.0/24 ! -o "$BRIDGE_DEV" -j MASQUERADE 2>/dev/null || true
    ip link set "$BRIDGE_DEV" down 2>/dev/null || true
    ip link delete "$BRIDGE_DEV" 2>/dev/null || true
  fi

  # Remove socket directory
  rm -rf "$SOCK_DIR"
}
trap cleanup EXIT

# ── Helper: wait for socket ───────────────────────────────────────────
wait_for_socket() {
  local sock="$1"
  local label="$2"
  local i
  for i in $(seq 1 50); do
    if [[ -S "$sock" ]]; then
      return 0
    fi
    sleep 0.1
  done
  echo "ci-vm-run: ERROR — timed out waiting for $label socket ($sock)" >&2
  return 1
}

# ── Start virtiofsd: workspace ────────────────────────────────────────
echo "ci-vm-run: starting virtiofsd for workspace..." >&2
virtiofsd \
  --socket-path="$VFSD_WORKSPACE_SOCK" \
  --shared-dir="$WORKSPACE" \
  --sandbox=none \
  --cache=never &
CLEANUP_PIDS+=($!)

wait_for_socket "$VFSD_WORKSPACE_SOCK" "workspace virtiofsd"

# ── Start virtiofsd: /nix/store ───────────────────────────────────────
echo "ci-vm-run: starting virtiofsd for /nix/store..." >&2
virtiofsd \
  --socket-path="$VFSD_NIXSTORE_SOCK" \
  --shared-dir=/nix/store \
  --sandbox=none \
  --cache=never &
CLEANUP_PIDS+=($!)

wait_for_socket "$VFSD_NIXSTORE_SOCK" "nix-store virtiofsd"

# ── Set up networking (TAP + bridge + NAT) ────────────────────────────
NET_ARGS=""
TAP_DEV="ci-tap-$$"

setup_networking() {
  # Create bridge
  ip link add "$BRIDGE_DEV" type bridge 2>/dev/null || return 1
  ip addr add 172.30.0.1/24 dev "$BRIDGE_DEV"
  ip link set "$BRIDGE_DEV" up

  # Create TAP device
  ip tuntap add "$TAP_DEV" mode tap 2>/dev/null || return 1
  ip link set "$TAP_DEV" master "$BRIDGE_DEV"
  ip link set "$TAP_DEV" up

  # NAT masquerade for outbound traffic
  iptables -t nat -A POSTROUTING -s 172.30.0.0/24 ! -o "$BRIDGE_DEV" -j MASQUERADE

  HAS_NETWORK=true
  NET_ARGS="--net tap=${TAP_DEV}"
  return 0
}

if setup_networking; then
  echo "ci-vm-run: networking configured (bridge=$BRIDGE_DEV, tap=$TAP_DEV)" >&2
else
  echo "ci-vm-run: WARNING — could not set up networking, proceeding without it" >&2
  NET_ARGS=""
  # Reset in case partial setup occurred
  ip link delete "$TAP_DEV" 2>/dev/null || true
  ip link delete "$BRIDGE_DEV" 2>/dev/null || true
  TAP_DEV=""
  HAS_NETWORK=false
fi

# ── Remove stale exit-code file ───────────────────────────────────────
rm -f "$WORKSPACE/.ci-exit-code"

# ── Launch cloud-hypervisor ───────────────────────────────────────────
echo "ci-vm-run: launching cloud-hypervisor..." >&2

# Build the command as an array for clarity
CHV_CMD=(
  cloud-hypervisor
  --kernel "$KERNEL_PATH"
  --disk "path=$ROOTFS_PATH,readonly=on"
  --fs "tag=project,socket=$VFSD_WORKSPACE_SOCK"
       "tag=nix-store,socket=$VFSD_NIXSTORE_SOCK"
  --memory size=4096M
  --cpus boot=4
  --serial tty
  --console off
)

if [[ -n "$NET_ARGS" ]]; then
  CHV_CMD+=($NET_ARGS)
fi

"${CHV_CMD[@]}" &
CHV_PID=$!
CLEANUP_PIDS+=($CHV_PID)

echo "ci-vm-run: cloud-hypervisor PID=$CHV_PID, waiting for VM to complete..." >&2
wait "$CHV_PID" || true

# ── Read exit code ────────────────────────────────────────────────────
if [[ -f "$WORKSPACE/.ci-exit-code" ]]; then
  EXIT_CODE="$(cat "$WORKSPACE/.ci-exit-code")"
  echo "ci-vm-run: VM finished with exit code $EXIT_CODE" >&2
  exit "${EXIT_CODE:-1}"
else
  echo "ci-vm-run: ERROR — .ci-exit-code not found; VM may have crashed" >&2
  exit 1
fi
