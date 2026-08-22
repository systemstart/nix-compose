# ADR-004: Userspace restart policies

**Status:** Accepted
**Date:** 2026-04-01
**Context:** CRI migration (M1, M8)

## Context

CRI has no restart policy concept — that is a kubelet responsibility.
Compose services declare restart policies (`no`, `always`, `on-failure`,
`unless-stopped`) that need enforcement.

Alternatives considered:
1. **No restart policies in detach mode** — document the limitation
2. **Fork a lightweight daemon** (`nix-compose daemon`) that watches and
   restarts containers in the background
3. **Use systemd transient units** (`systemd-run`) — the host init system
   becomes the restarter

## Decision

Implement restart policies in userspace via a Supervisor
(`pkg/cri/supervisor.go`). Per-service goroutines watch container status
and restart according to policy.

- Exponential backoff: 1s initial, factor 2, capped at 60s
- Backoff resets if the container ran longer than 30 seconds
- The supervisor runs during foreground mode (`up` without `-d`)
- `Stop()` marks `stoppedByUser=true`, affecting `unless-stopped` policy
- In detach mode (`up -d`), restart policies are not enforced after
  nix-compose exits (option 1 for now)

systemd integration (option 3) may be added later as a follow-up.

## Consequences

- Restart policies work in foreground mode
- Detach mode has no restart enforcement (documented limitation)
- No external daemon or systemd dependency required
- Backoff prevents tight restart loops for crashing containers
- Clean integration with signal-based graceful shutdown
