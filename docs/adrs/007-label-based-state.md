# ADR-007: Label-based state tracking

**Status:** Accepted
**Date:** 2026-04-01
**Context:** CRI migration (M1)

## Context

nix-compose needs to track which pods and containers belong to which
project and service for list, stop, and teardown operations.

Alternatives considered:
- External state file (JSON/SQLite) — requires syncing with runtime
  state, crash recovery, file locking
- Labels on CRI resources — the runtime itself is the state store,
  always consistent

## Decision

Use labels on PodSandboxes and Containers as the sole state tracking
mechanism:

```
nix-compose.project = "myapp"
nix-compose.service = "web"
nix-compose.version = "1"
```

All list and filter operations use these labels via CRI's
`LabelSelector`. No external state file.

## Consequences

- No state file to corrupt, sync, or recover
- CRI runtime is the single source of truth
- Label schema must be right from the start — changing it later means
  orphaned containers
- All operations (list, stop, teardown) use label-filtered CRI queries
- Slightly slower than a local lookup table, but eliminates an entire
  class of consistency bugs
