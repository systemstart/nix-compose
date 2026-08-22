# ADR-001: CRI as the sole backend

**Status:** Accepted
**Date:** 2026-04-01
**Context:** CRI migration (M0)

## Context

nix-compose originally shelled out to a compose CLI (`docker compose`,
`podman compose`, or `nerdctl compose`) to manage containers. This meant
three compose implementations, three sets of quirks, and a generated YAML
file as the interface boundary.

A proposed `runtime.Backend` interface with three implementations
(containerd, Podman, Docker) was considered but rejected as trading
compose-flavoured fragmentation for API-client-flavoured fragmentation.

## Decision

Use the Container Runtime Interface (CRI) gRPC API as the sole backend
protocol. CRI is a single protobuf-defined API (`k8s.io/cri-api`) with
two services — `RuntimeService` and `ImageService` — implemented by every
major container runtime:

- containerd exposes it natively
- CRI-O exposes it natively
- Docker exposes it through containerd

One socket, one gRPC dial, one set of types.

A compose-CLI fallback was kept for a time, for environments without a
CRI socket. It was removed: it could never reach parity — the plan/apply
engine, drift detection, rollback and the dependency-graph commands have
no compose equivalent — so it offered a second-class tool under the same
name. Requiring a CRI runtime is a cleaner promise than a path that
silently does less.

## Consequences

- Single protocol eliminates per-runtime bug classes
- No compose CLI, no YAML generation in the hot path
- Transitive dependency on `k8s.io/cri-api` (pulls in parts of `k8s.io/`)
- Users need a CRI-capable runtime (containerd or CRI-O)
- Podman-without-CRI users need to start a Podman API socket or use CRI-O
