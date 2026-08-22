# ADR-009: Daemonless CRI orchestration

**Status:** Accepted
**Date:** 2026-04-13
**Context:** Orchestrate engine import (M11)

## Context

Docker Compose delegates all privileged work to the `dockerd` daemon
(image build, networking, mounts, cgroups). The daemon runs as root,
exposes a REST API socket, and owns all container state in an opaque
directory. Any process with write access to the socket has de-facto
root privileges on the host.

The CRI orchestrator needs to manage containers on a single node. The
question is whether to talk to Docker's API, to `dockerd` via the
Docker socket, or directly to the underlying container runtime.

## Decision

Talk directly to containerd (or CRI-O) via the CRI gRPC API. Skip the
`dockerd` layer entirely. The architecture is:

```
mc CLI → CRI gRPC → containerd → containerd-shim → runc → container
       → CNI exec → CNI plugins
       → BoltDB (orchestrator state)
```

No long-running daemon is introduced. The orchestrator is a CLI tool
that runs, performs its work via CRI calls, and exits. Container
processes are supervised by containerd, not by the orchestrator.

## Consequences

**Positive:**
- No root daemon running 24/7; no single point of failure
- No Docker socket attack surface (CVE-2024-41110, CVE-2026-34040)
- Orchestrator can be stopped and restarted without affecting containers
- State separation: containerd owns container processes; orchestrator
  owns deployment history, dependency graph, rollout status (in BoltDB)
- Runtime-agnostic: works with any CRI-compatible runtime
- Smaller dependency surface than moby/moby (~150k LOC)

**Negative:**
- No `docker build` / BuildKit integration (non-goal; use external builders)
- No Docker volume or logging plugins
- No Swarm mode (non-goal; single-node only)
- Users with Docker-only workflows must install containerd separately

**Trade-offs:**
- CNI replaces libnetwork — industry standard but different plugin ecosystem
- CRI log path replaces Docker logging drivers — simpler but fewer options
- **CRI is not the only API used.** Local image import ([ADR-015](015-virtiofs-image-delivery.md))
  has no CRI equivalent — `ImageService.PullImage` speaks only to registries —
  so `pkg/cri/import.go` opens a *second* gRPC connection to the *same*
  containerd socket and speaks containerd's native API over it. Still no extra
  daemon and no extra socket, but "talk to the runtime purely through CRI" is
  now "talk to the runtime through CRI wherever CRI can express the operation".
  The cost is runtime-specific: a runtime without containerd's native API
  (CRI-O) can pull images but cannot import local ones.

## Alternatives Considered

1. **Docker API wrapper** — talk to `dockerd` via its REST API. Rejected:
   adds a root daemon dependency, couples to Docker-only networking
   (libnetwork), and the socket is a root-equivalent attack surface.

2. **Podman API** — rootless-capable but Podman-specific. Rejected:
   creates runtime lock-in similar to Docker.

3. **OCI runtime direct** — call `runc` directly. Rejected: too low-level;
   would need to reimplement image management, snapshot handling, and
   container supervision that containerd already provides.
