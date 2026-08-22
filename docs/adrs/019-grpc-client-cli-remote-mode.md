# ADR-019: gRPC client wrapper + CLI remote mode

**Status:** Accepted
**Date:** 2026-04-14
**Context:** MicroVM runtime client side (M14 Phase 3)

## Context

M14 Phase 1-2 delivered a protobuf service definition and gRPC server
(`nix-compose serve`) exposing Plan, Apply, Teardown, State, Health,
ExecSync, and Logs RPCs over a unix socket. The server runs inside
the VM (or locally for testing).

The missing piece is a client that CLI commands can use to talk to
the remote server instead of running the pipeline locally. This
enables the host-side CLI to delegate all container operations to the
in-VM engine via the gRPC API, which is the foundation for all
subsequent M14 work (VM lifecycle, vsock, integration).

## Decision

### Typed client wrapper

Create `pkg/orchestrate/client/` with a `Client` struct that wraps
the generated `OrchestrateServiceClient`. The wrapper:

- `Dial()` connects via `grpc.NewClient("unix://"+socket, insecure)`
  and verifies with a `Health` RPC (same pattern as `pkg/cri/client.go`)
- Each method constructs the proto request from Go arguments and wraps
  errors with `fmt.Errorf`
- `Close()` closes the underlying `grpc.ClientConn`
- `Logs()` returns the raw `grpc.ServerStreamingClient` for the caller
  to iterate

### `--remote-socket` persistent flag

A persistent flag on the root command. When set, a `dialRemote()`
helper returns a connected `*client.Client`. When unset, `dialRemote()`
returns nil and commands proceed with the local CRI or compose path.

If the remote socket is unreachable, a warning is printed and
`dialRemote()` returns nil (graceful fallback).

### Remote branches in CLI commands

Each of 6 commands gets a remote check near the top of its `run*`
function, before the existing CRI/compose branching:

| Command | Remote function | Behaviour |
|---------|----------------|-----------|
| `up` | `remoteUp()` | Local Nix eval, marshal to JSON, `Apply` RPC |
| `down` | `remoteDown()` | Optional eval for ordered shutdown, `Teardown` RPC |
| `exec` | `remoteExec()` | Non-interactive `ExecSync` only; error for interactive |
| `logs` | `remoteLogs()` | Stream `LogEntry` with colored prefixes; follow via gRPC stream |
| `plan` | `remotePlan()` | Local Nix eval, `Plan` RPC, print diff |
| `state` | `remoteStateList/Show()` | `State` RPC, tabular/detail output |

The pattern is: eval Nix locally (when needed), marshal the
composition to JSON, call the remote RPC, format the response for the
terminal.

### Shared helpers

`pkg/cli/remote.go` contains `printRemoteActions()` and
`printRemoteSummary()` used by `up`, `plan`, and potentially future
commands.

## Alternatives considered

### Direct use of generated client

Call `orchestratev1.NewOrchestrateServiceClient(conn)` directly from
each CLI command. This would scatter connection management, error
wrapping, and proto request construction across 6 files. The wrapper
centralises this and provides a clean Go API.

### Automatic remote detection

Auto-detect a running server by probing well-known socket paths
(e.g. `/run/nix-compose/orch.sock`). Adds implicit behaviour and
makes it harder to reason about which engine is being used. Explicit
`--remote-socket` is clearer and avoids surprising connections.

### Interactive exec over gRPC

Implement bidirectional streaming for interactive exec with TTY
support. This requires a new proto RPC with client streaming for
stdin and terminal resize events. Deferred because SPDY streaming
(used for local CRI exec) is well-tested and the remote interactive
exec use case can be addressed when vsock is available.

### Separate binary for remote CLI

Ship a separate `nix-compose-remote` binary. Rejected because the
remote mode is a flag, not a fundamentally different tool. Users
should be able to switch between local and remote with a single flag.

## Consequences

- The host CLI can now orchestrate containers running on a remote
  `nix-compose serve` instance
- The `--remote-socket` flag is the entry point for MicroVM runtime
  support: once vsock transport is added, the CLI just needs a
  different socket path
- Nix evaluation always runs locally (host has the Nix store and
  flake); only the pipeline from JSON composition onwards runs
  remotely
- Interactive exec and watch mode are not available in remote mode
  (documented limitations)
- The client wrapper can be reused by future Go tooling (e.g. a
  daemon, health monitor, or TUI) without depending on CLI internals
