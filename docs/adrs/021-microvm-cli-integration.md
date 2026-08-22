# ADR-021: MicroVM CLI integration

**Status:** Accepted
**Date:** 2026-04-18
**Context:** M14 — MicroVM runtime

## Context

Phases 1-3 of M14 delivered the gRPC orchestrate API, server, client
wrapper, and CLI remote mode. The `pkg/microvm/` package provides VM
lifecycle management (boot cloud-hypervisor, health check over vsock,
graceful shutdown). However, `pkg/microvm/` was entirely isolated — no
other package imported it.

Users need a way to boot a microVM from the CLI and have nix-compose
automatically delegate orchestration to the in-VM engine over vsock.

## Decision

Wire the VM lifecycle into the `up` and `down` commands:

### Configuration

- Add `MicroVMConfig` struct to `pkg/eval/parse.go` so Nix configs can
  declare VM parameters via `x-nix-compose-microvm` (kernel, rootfs,
  vcpus, memory, CID)
- Add `--microvm` flag plus `--vm-kernel`, `--vm-rootfs`, `--vm-vcpus`,
  `--vm-memory`, `--vm-cid` flags to the `up` command
- CLI flags override Nix-evaluated values (same pattern as
  `--project-name` overriding the Nix project name)

### Up flow

When `--microvm` is set, the priority order in `runUp()` becomes:

1. Remote socket/vsock (explicit `--remote-socket` / `--remote-vsock-cid`)
2. **MicroVM mode** — boot VM, then delegate via vsock
3. CRI mode (auto-detected containerd/CRI-O socket)
4. Compose CLI fallback

The microVM up flow:
1. Evaluate Nix config via `evalForOrchestrate()`
2. Merge CLI flags with Nix config to build `microvm.Config`
3. Add nix-store (read-only) and project dir (read-write) shares
4. Call `microvm.New()` and `vm.Start()`
5. Connect via `client.DialVsock()`
6. Delegate to existing `remoteUp()` for orchestration
7. In foreground mode, block on SIGINT/SIGTERM or VM crash

### Down flow

When `--microvm` is set in `runDown()`, connect to the VM via vsock
and delegate to `remoteDown()` for service teardown. This is best-effort
— the VM is ephemeral and the primary shutdown mechanism is SIGINT on
the foreground `up` process.

### Testability

- `newVM` and `dialVsock` are package-level variables, allowing tests
  to inject mocks without real VM or vsock infrastructure
- `microvm.MockVM` serves as the test double for the `VM` interface

## Consequences

- Users can boot microVMs with `nix-compose up --microvm --vm-kernel <path> --vm-rootfs <path>`
- All existing remote-mode commands (plan, state, logs, exec, drift,
  rollback) work against a microVM via `--remote-vsock-cid`
- The MicroVM is a composition-level resource (not per-service),
  matching the architectural constraint that one VM hosts all services
- Detach mode prints the CID for subsequent management via
  `--remote-vsock-cid`
- Future work: persistent VM state file for `down` without explicit CID,
  TAP networking flags, VM image builder integration
