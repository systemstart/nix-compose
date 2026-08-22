# Roadmap

Completed milestones (M0–M15) are in [CHANGELOG.md](CHANGELOG.md).
Design rationale and architectural decisions: [docs/adrs/](docs/adrs/README.md).

---

## Milestones

#### M16 — YAML projects and migration tooling *(done)*

A project can be written without Nix, and an existing compose project
has a path in. See [ADR-024](docs/adrs/024-yaml-project-format.md) and
[ADR-025](docs/adrs/025-compose-import-and-suggest.md).

| Sub-task | Detail | Status |
|----------|--------|--------|
| `nix-compose.yaml` | Third project mode; compose's fields plus `package:`; unknown keys rejected | done |
| Registry-only fast path | A document naming no `package:` never invokes nix | done |
| `nix/yaml.nix` | Resolves `package: <attr>` via the same `nix/lib.nix` a flake uses | done |
| `pkg/nixpins` | nixpkgs / nix-oci revisions carried by the binary, kept in step with `flake.lock` | done |
| `nix-compose import` | Lossy, reported, one-way conversion from docker-compose | done |
| `nix-compose suggest` | Registry image → nixpkgs attribute, with major-version warnings | done |
| `nix-compose doctor` | Environment preflight; every finding carries a fix | done |
| Lock file / pure evaluation | Generate a flake in `.nix-compose/` instead of `--expr` | **open** — see below |

#### M14 — MicroVM runtime *(done)*

Boot a NixOS microVM as the container execution environment.
nix-compose on the host communicates with the orchestrate engine
inside the VM over vsock.

All phases complete: gRPC API + server (Phase 1-2), client + CLI remote
mode (Phase 3), CLI integration with `--microvm` flag
([ADR-021](docs/adrs/021-microvm-cli-integration.md)), auto-built VM
image ([ADR-022](docs/adrs/022-microvm-image-builder.md)), and
userspace TCP port forwarding over vsock
([ADR-023](docs/adrs/023-microvm-port-forwarding.md)).

| Sub-task | Detail | Status |
|----------|--------|--------|
| `pkg/microvm/` | Boot cloud-hypervisor, manage VM lifecycle (start, stop, health), vsock connection | done |
| vsock gRPC transport | Orchestrate API exposed over vsock inside VM, nix-compose dials from host | done |
| CLI integration | `--microvm` flag on `up`/`down`, Nix config merge, foreground/detach modes | done |
| Nix store sharing | Host `/nix/store` mounted read-only via virtiofs inside VM | done |
| Workspace sharing | Project directory shared read-write for bind mounts | done |
| VM image builder | NixOS closure with containerd + CNI plugins + orchestrate binary + virtiofs `/nix/store` share ([ADR-022](docs/adrs/022-microvm-image-builder.md)) | done |
| Port forwarding | Userspace TCP port forwarding over vsock; published ports reachable from host ([ADR-023](docs/adrs/023-microvm-port-forwarding.md)) | done |
| Graceful shutdown | SIGINT/SIGTERM → orchestrate teardown inside VM → stop VM | done |
| Flake module | `microvm.nix` NixOS module for the orchestrate VM, configurable vcpu/mem/shares | done |

---

## Other future work

These items are **not planned** but have come up as natural extensions.

| Area | Idea | Notes |
|------|------|-------|
| YAML lock file | Generate a flake in `.nix-compose/` and evaluate `nix eval path:…#composition` instead of `--expr` | Would make YAML evaluation **pure** and produce a `flake.lock`, pinning `package:` per project rather than per nix-compose version. The two are one change; `nix store add` does **not** substitute for it. See [ADR-024](docs/adrs/024-yaml-project-format.md) |
| `nix-compose eject` | Write the equivalent `flake.nix` for a YAML project that has outgrown the format | The YAML is a strict subset of `mkComposition`, so the conversion is mechanical |
| YAML JSON Schema | Publish a schema for editor completion and inline validation | Cheap once the key set is fixed; derived from `eval.Service` like the validator |
| CRI-O local images | `skopeo copy oci: containers-storage:` fallback so `package:` works on CRI-O | Documented as unsupported in [docs/limitations.md](docs/limitations.md); needs a CRI-O to test against |
| Restart persistence | `nix-compose daemon` or systemd transient units for `restart: always` in detach mode | Currently restart policies only enforce while nix-compose runs in foreground |
| Registry auth | Read `~/.docker/config.json` or containerd credential helpers for `PullImage` | Currently only public registries work |
| Image builds | Optional buildkit integration for `build:` key | Current stance: build externally (Nix, CI) — see [ADR-006](docs/adrs/006-no-image-builds.md) |
| CNI plugin bundling | Ship required CNI plugins as a Nix derivation in the flake | Currently users must install plugins separately |
| virtiofsd overlay | Patch virtiofsd so a `setgroups` EPERM is non-fatal, and use it for `--microvm` | Single blocker shared by both VM-based paths: the standalone CI-VM runner and `--microvm` on a hardened CI runner. Highest-leverage item for CI support — see [docs/running-in-ci.md](docs/running-in-ci.md) |
| `nix flake check` | Make it pass, so CI can gate on it | It currently fails: the microVM NixOS configurations are evaluated as standalone bootable systems and trip assertions (`boot.loader.grub.devices`, no root password) that do not apply to a direct-kernel guest. CI runs `nix build` instead |
| CRI-O testing | Dedicated integration tests against CRI-O in standalone mode | containerd is the primary test target today |
| Container events | Subscribe to CRI container events instead of polling `ContainerStatus` | Would reduce latency for health checks and watch mode |
| TUI | Interactive operations over the dependency graph | Resource tree, deployment table, live log pane; would sit on top of the existing plan/state/drift commands |

---

## Out of scope

- Interactive REPL
- Helm chart generation (Kustomize overlays are sufficient)
- Remote deployment (CI/CD concern: ArgoCD, Flux, etc.)
- Windows support
