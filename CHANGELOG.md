# Changelog

All notable changes to nix-compose are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

While the major version is 0, the `nix-compose.yaml` schema and the CLI
surface may change between minor releases.

## v0.1.0 — 2026-08-22

First public release.

### Added

- **CRI backend.** Every command speaks CRI gRPC to containerd or CRI-O
  directly — no daemon, no compose file, nothing shelling out to another
  tool. A pod sandbox per service, the full pull → create → start
  lifecycle, and named/bind/tmpfs/Nix-store mounts translated to CRI mount
  specs ([ADR-001](docs/adrs/001-cri-as-the-sole-backend.md),
  [ADR-002](docs/adrs/002-pod-per-service.md)).
- **Three project formats.** `nix-compose.yaml` for a compose-shaped
  document that needs no Nix knowledge, `flake.nix` for real computation,
  or `compose.nix`. All three produce the same composition, and a YAML
  project naming no `package:` never invokes Nix at all
  ([ADR-024](docs/adrs/024-yaml-project-format.md)).
- **Registry-free images.** `package:` builds a service's image from that
  package's Nix closure via nix-oci and imports it straight into
  containerd — no Dockerfile, nothing pushed or pulled
  ([ADR-006](docs/adrs/006-no-image-builds.md),
  [ADR-015](docs/adrs/015-virtiofs-image-delivery.md)).
- **Dependency-ordered startup** with health gating between levels, and
  restart supervision (`always`, `on-failure`, `unless-stopped`) with
  exponential backoff ([ADR-004](docs/adrs/004-userspace-restart-policies.md),
  [ADR-005](docs/adrs/005-host-side-health-probes.md)).
- **CNI networking** — per-project bridge network with DNS, falling back to
  host networking when the plugins are absent
  ([ADR-003](docs/adrs/003-cni-networking.md)).
- **Declarative plan/apply engine.** Typed manifests with a dependency DAG,
  diffed against a BoltDB state store: `plan`, `state`, `drift`, `rollback`
  and `graph show|deps|impact`
  ([ADR-017](docs/adrs/017-plan-apply-declarative-loop.md),
  [ADR-020](docs/adrs/020-dependency-graph-intelligence.md)).
- **Kubernetes manifest emission** — `render --target k8s` produces
  Deployment, Service, Secret, PVC and `kustomization.yaml` as a one-way
  starting point, covering probes, `envFrom` secrets, init containers and
  named ports. `x-nix-compose.resources` is emitted here but not enforced
  on locally running containers
  ([ADR-016](docs/adrs/016-pure-manifest-translation.md)).
- **Migration tooling** — `nix-compose import` converts a
  `docker-compose.yaml` and reports what it dropped; `nix-compose suggest`
  maps registry images to nixpkgs attributes
  ([ADR-025](docs/adrs/025-compose-import-and-suggest.md)).
- **`nix-compose doctor`** — environment preflight where every finding
  carries its fix. It exits non-zero only when something would actually
  stop a composition running, so it can gate a setup script.
- **Watch mode** (`up --watch`) — re-evaluates on file changes and restarts
  only the affected services.
- **Remote mode** (`--remote-socket`) and **microVM mode** (`up --microvm`),
  both experimental: orchestration is delegated to a `nix-compose serve`
  instance, locally over a unix socket or inside a cloud-hypervisor VM over
  vsock ([ADR-018](docs/adrs/018-grpc-orchestrate-api.md),
  [ADR-021](docs/adrs/021-microvm-cli-integration.md)).
- **GC root management** — the Nix store paths a composition references
  stay alive for as long as the project is up.
- Top-level `name:` in a YAML project, so two projects in like-named
  directories do not share a project name.

### Known limitations

See [docs/limitations.md](docs/limitations.md), which is a deliberately
complete account rather than a marketing document. The ones most likely to
bite: `build:` is not honoured, locally built images need containerd
specifically, container logs are unreadable without root on a rootful
runtime, and named volumes inherit the home directory's filesystem — which
breaks images that `chown` their data directory when that filesystem is
virtiofs or 9p.
