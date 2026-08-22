# ADR-010: Three-mode privilege model

**Status:** Accepted
**Date:** 2026-04-13
**Context:** Orchestrate engine import (M11)

## Context

Because the orchestrator is daemonless (ADR-009), there is no root
daemon to hide behind. The orchestrator process itself performs or
delegates every operation. CRI operations only need socket access, but
CNI operations need real kernel capabilities (`CAP_NET_ADMIN`,
`CAP_SYS_ADMIN`). This split drives the privilege model.

Docker Compose avoids this question by delegating everything to a
root-owned daemon — the socket is the privilege boundary, and any user
in the `docker` group has de-facto root.

## Decision

Support three operating modes, auto-detected at startup:

### Mode 1: Root — full capabilities

The orchestrator runs as root. CRI, CNI, bind mounts, cgroups, and
iptables all work without restriction. State at `/var/lib/mc/`.

**When:** Servers, CI runners, VMs — anywhere simplicity matters more
than isolation.

### Mode 2: Socket group — unprivileged CLI, system runtime

The user is in the `containerd` group. CRI socket access works; CNI,
bind mounts, and iptables do not. Covers image management and basic
container lifecycle. State at `~/.local/share/nix-compose/`.

**When:** System containerd is already running (e.g. K8s node) and
users want to manage containers without sudo.

### Mode 3: Rootless — no root, no daemon, no setuid

containerd runs in rootless mode under the user's UID namespace.
Networking uses `pasta` (or `slirp4netns`) instead of CNI. Storage
uses `fuse-overlayfs`. State at `$XDG_DATA_HOME/nix-compose/`.

**When:** Developer workstations where no root process is acceptable.

### Auto-detection

```go
if os.Getuid() == 0 → ModeRoot
else if rootless containerd socket exists → ModeRootless
else if system socket is writable → ModeSocketGroup
else → error with explanation
```

The orchestrator adapts CRI socket path, state DB path, network
backend (CNI vs pasta), and bind mount validation based on mode.

## Consequences

- Mode 1 (root) is the default for now — matches Docker Compose
  mental model and gives full CNI support
- Mode 3 (rootless) is opt-in — requires `containerd-rootless` setup
- The `NetworkBackend` interface must be designed from the start so
  adding pasta does not require refactoring network definitions
- No configuration flags needed for common cases — the orchestrator
  inspects its own environment

## Alternatives Considered

1. **Root-only** — simplest, but excludes developer workstations
   and contradicts the rootless host goal of the microVM architecture.

2. **CNI helper binary** — a setuid binary that only does CNI ADD/DEL.
   Deferred: Mode 1 and Mode 3 cover most use cases without it.

3. **Always rootless** — would require pasta on every system and
   exclude advanced CNI plugins (macvlan, ipvlan).
