# ADR-011: Dependency graph orchestration over systemd/Quadlet

**Status:** Accepted
**Date:** 2026-04-13
**Context:** Orchestrate engine import (M11)

## Context

Podman Quadlet turns `.container` / `.volume` / `.network` files into
systemd units. systemd manages ordering, restarts, and lifecycle with
no extra daemon. This is the recommended approach on RHEL/Fedora for
single-node container workloads.

The question: should nix-compose generate Quadlet files and let systemd
handle orchestration, or should it use its own deployment pipeline with
a purpose-built dependency graph?

## Decision

Use a purpose-built deployment pipeline with a bidirectional dependency
graph in BoltDB. Do not delegate orchestration to systemd.

systemd is a process manager adapted for containers; this orchestrator
is built from scratch around a container dependency graph. They overlap
in surface area but diverge in what they can reason about.

### Key gaps in systemd's model

1. **Ordering is not dependency.** systemd's `After=` + `Requires=`
   must be combined manually per unit. Health-aware ordering
   (`Notify=healthy`) was retrofitted in Podman 4.7+ — it is opt-in,
   not default.

2. **No deployment as a unit of work.** systemd manages individual
   units. There is no "deploy these 5 containers atomically" — no
   rollback, no "what changed in the last deployment?"

3. **No teardown safety.** `Wants=` dependents (Quadlet default) are
   not stopped when a dependency is removed. There is no "refuse to
   stop because something depends on this."

4. **No queryable cross-resource graph.** `systemd-analyze dot` shows
   all units on the system — no scoping to one application, no
   transitive impact analysis.

5. **No plan/diff/dry-run.** `systemctl start` runs immediately.

6. **Manifest format is unit files.** N separate files with manual
   `Requires=`/`After=` wiring; no single-file application description
   and no cross-reference validation.

## Consequences

- The orchestrator gets first-class dependency conditions (`created`,
  `started`, `healthy`, `ready`) — blocking is the default
- `CheckReferences()` prevents teardown of still-referenced resources
- `Deployment` object groups requests into atomic stage/commit units
- `mc plan` shows what would change without executing
- `mc graph show/impact/deps` queries the container-specific DAG
- The orchestrator itself can be started by systemd for boot
  integration — best of both worlds

## Alternatives Considered

1. **Generate Quadlet files** — let systemd handle everything. Rejected:
   loses plan/diff, atomic deployment, teardown safety, and graph
   queries. Suitable for independent services, not inter-dependent
   applications.

2. **Hybrid: Quadlet + sidecar** — generate Quadlet for lifecycle,
   add a sidecar for graph queries. Rejected: splits state between
   systemd and the sidecar; complexity without clear benefit.
