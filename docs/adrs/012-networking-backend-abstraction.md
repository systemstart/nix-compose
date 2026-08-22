# ADR-012: Networking backend abstraction

**Status:** Accepted
**Date:** 2026-04-13
**Context:** Orchestrate engine import (M11)

## Context

The three-mode privilege model (ADR-010) means networking works
differently depending on the execution context:

- **Root mode:** CNI plugins (bridge, macvlan, portmap, etc.)
- **Rootless mode:** `pasta` or `slirp4netns` (user-space networking)

The `Network` resource definition should not know or care which
backend is in use. The manifest format must be identical in both modes.

## Decision

Define a `NetworkBackend` interface between the deployment pipeline
and the actual network setup:

```
Deployment Pipeline
       |
  Network Definition
       |
  NetworkBackend interface
       |
  ├── CNIBackend     (root mode: CNI plugin exec)
  └── PastaBackend   (rootless mode: pasta CLI)
```

The backend is selected at startup based on the detected privilege
mode. CNI is implemented first (M11). Pasta support is a separate
future phase.

## Consequences

- `Network` resource type delegates to the backend interface, not
  directly to CNI
- Adding pasta does not require refactoring the network definition
  or the deployment pipeline
- The manifest `ports:` and `networks:` fields map to both backends
- Feature parity between backends is not guaranteed — CNI supports
  macvlan/ipvlan; pasta does not

## Alternatives Considered

1. **CNI only** — require root for networking. Rejected: contradicts
   the rootless goal of Mode 3.

2. **pasta only** — simpler, but loses CNI plugin ecosystem and
   near-line-rate bridge performance.

3. **Runtime-provided networking** — let containerd handle it.
   Rejected: containerd's CRI plugin delegates to CNI anyway; no
   built-in user-space fallback.
