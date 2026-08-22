# ADR-018: gRPC orchestrate API over unix socket / vsock

**Status:** Accepted
**Date:** 2026-04-14
**Context:** MicroVM runtime foundation (M14 Phase 1-2)

## Context

The MicroVM runtime (M14) requires nix-compose on the host to
communicate with the orchestrate engine running inside a NixOS VM.
The host sends a serialised `eval.Composition` (from Nix evaluation)
and the VM runs the full convert → bridge → plan → apply pipeline
using its own CRI client (containerd inside the VM).

The transport needs to work over both unix sockets (for local
testing and non-VM use) and vsock (for host↔VM communication without
networking).

## Decision

Define a protobuf gRPC service (`OrchestrateService`) and implement
a server that runs inside the VM (or on a unix socket for testing).

### Service definition

```protobuf
service OrchestrateService {
  rpc Plan(PlanRequest) returns (PlanResponse);
  rpc Apply(ApplyRequest) returns (ApplyResponse);
  rpc Teardown(TeardownRequest) returns (TeardownResponse);
  rpc State(StateRequest) returns (StateResponse);
  rpc Health(HealthRequest) returns (HealthResponse);
  rpc ExecSync(ExecSyncRequest) returns (ExecSyncResponse);
  rpc Logs(LogsRequest) returns (stream LogEntry);
  rpc Drift(DriftRequest) returns (DriftResponse);
  rpc Rollback(RollbackRequest) returns (RollbackResponse);
}
```

9 RPCs mirror the existing engine API plus exec/logs forwarding. The
host sends composition JSON bytes; the server deserialises and runs
the pipeline locally.

### Server architecture

Each RPC that needs state creates a temporary `orchestrate.Engine`,
runs the pipeline, and closes it. This avoids long-lived engine
state in the server process and keeps the engine lifecycle simple.

The `planFromComposition()` helper centralises the convert → bridge →
plan pipeline shared by Plan and Apply RPCs.

### Transport

- **Unix socket** (`--socket`): available now, used for testing and
  local development
- **vsock** (`--vsock-port`): planned for M14 Phase 3 when
  `pkg/microvm/` is implemented; the gRPC server will listen on a
  vsock port inside the VM

### CLI integration

`nix-compose serve` starts the gRPC server. Flags:

- `--socket` — unix socket path (required for now)
- `--vsock-port` — vsock port (default 1024, not yet active)
- `--log-base` — CRI log directory

Signal handling: SIGINT/SIGTERM trigger graceful gRPC shutdown.

## Alternatives considered

### REST/HTTP API

Simpler tooling (curl) but loses proto type safety, streaming (for
logs), and efficient binary serialisation. gRPC is already used for
CRI communication, so no new dependency.

### Custom binary protocol over vsock

Maximum performance but requires custom serialisation, framing, and
error handling. Protobuf + gRPC provide all of this for free with
code generation.

### Embed engine in host process

Run the engine on the host instead of inside the VM. This would
require the host to have CRI access into the VM's container runtime,
which defeats the isolation purpose of the MicroVM. Rejected because
the engine must run where the CRI runtime lives.

### Persistent engine in server

Keep a single `Engine` instance alive across RPCs. Adds complexity
around concurrent access, stale state, and cleanup. The temporary
engine approach is simpler and each RPC gets a fresh view of BoltDB
state. If performance becomes an issue, engine pooling can be added
later.

## Consequences

- The orchestrate API is transport-agnostic — works over unix sockets
  today, vsock tomorrow
- Proto definitions serve as the contract between host and VM
- Server tests can run without a real VM by using unix sockets
- The `serve` subcommand is self-contained and can be deployed
  independently inside a VM image
- Logs RPC uses server streaming for efficient follow mode
