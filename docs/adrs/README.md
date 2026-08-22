# Architecture Decision Records

Each ADR captures a significant design choice, the alternatives that were
considered, and why the decision went the way it did. They are the
canonical rationale — there are no separate design documents.

ADRs 001–008 cover the container runtime layer: how services map onto CRI
primitives, networking, restart policies, health probes, state and logs.
ADRs 009–020 cover the orchestrate engine — the daemonless plan/apply loop,
its privilege model, typed resources, state store and gRPC API. ADRs
021–023 cover the microVM runtime, and 024–025 the YAML project format and
the migration tooling that reads a `docker-compose.yaml`.

## Index

| ADR | Title | Status |
|-----|-------|--------|
| [001](001-cri-as-the-sole-backend.md) | CRI as the sole backend | Accepted |
| [002](002-pod-per-service.md) | Pod-per-service mapping model | Accepted |
| [003](003-cni-networking.md) | CNI plugin networking | Accepted |
| [004](004-userspace-restart-policies.md) | Userspace restart policies | Accepted |
| [005](005-host-side-health-probes.md) | Host-side health probes | Accepted |
| [006](006-no-image-builds.md) | Image builds delegated to nix-oci | Accepted |
| [007](007-label-based-state.md) | Label-based state tracking | Accepted |
| [008](008-file-based-logs.md) | File-based CRI log parsing | Accepted |
| [009](009-daemonless-cri-orchestration.md) | Daemonless CRI orchestration | Accepted |
| [010](010-three-mode-privilege-model.md) | Three-mode privilege model | Accepted |
| [011](011-dependency-graph-over-systemd.md) | Dependency graph orchestration over systemd/Quadlet | Accepted |
| [012](012-networking-backend-abstraction.md) | Networking backend abstraction | Accepted |
| [013](013-gvk-typed-cri-resources.md) | GVK-typed CRI resource kinds | Accepted |
| [014](014-boltdb-state-store.md) | BoltDB for orchestrator state persistence | Accepted |
| [015](015-virtiofs-image-delivery.md) | Virtiofs image delivery for Nix-built images | Accepted |
| [016](016-pure-manifest-translation.md) | Pure manifest translation layer | Accepted |
| [017](017-plan-apply-declarative-loop.md) | Plan/apply declarative loop | Accepted |
| [018](018-grpc-orchestrate-api.md) | gRPC orchestrate API over unix socket / vsock | Accepted |
| [019](019-grpc-client-cli-remote-mode.md) | gRPC client wrapper + CLI remote mode | Accepted |
| [020](020-dependency-graph-intelligence.md) | Dependency graph intelligence | Accepted |
| [021](021-microvm-cli-integration.md) | MicroVM CLI integration | Accepted |
| [022](022-microvm-image-builder.md) | MicroVM image builder | Accepted |
| [023](023-microvm-port-forwarding.md) | MicroVM Port Forwarding | Accepted |
| [024](024-yaml-project-format.md) | YAML project format | Accepted |
| [025](025-compose-import-and-suggest.md) | Compose import and package suggestion | Accepted |
