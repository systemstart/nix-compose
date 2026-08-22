# ADR-023: MicroVM Port Forwarding

- **Status:** Accepted
- **Date:** 2026-04-19
- **Relates to:** ADR-018 (gRPC API over vsock), ADR-021 (MicroVM CLI integration)

## Context

MicroVM mode boots a NixOS VM and delegates container orchestration
inside it. Services running in containers publish ports (e.g.,
`ports: ["8080:80"]`), but those ports are only reachable inside the VM.
The host cannot connect to them because there is no network bridge
between host and VM.

## Decision

Implement **userspace TCP port forwarding over vsock**. For each
published port in the composition:

1. A TCP listener opens on the host at the specified bind address and
   port.
2. Each accepted connection dials the VM's port-forward vsock listener
   (port 1025 by default).
3. A 2-byte big-endian `uint16` target port is written as a header.
4. The VM-side listener reads the header, dials `127.0.0.1:<port>`
   (where the CNI portmap plugin has already set up forwarding to the
   container), and proxies bidirectionally.

### Protocol

```
Host                          VM (vsock port 1025)
 │                             │
 ├─ dial vsock CID:1025 ──────►│
 ├─ write 2-byte port (BE) ───►│
 │                             ├─ dial 127.0.0.1:<port>
 │◄──── bidirectional copy ────►│
 │                             │
```

### Port extraction

`ExtractPorts(comp)` iterates all services and collects TCP port
mappings from both `svc.Ports` strings and `svc.XNixCompose.NamedPorts`.
`VMPort` is always set to `HostPort` because the CNI portmap plugin
inside the VM maps `HostPort → ContainerPort`.

Non-TCP protocols and container-only ports (`HostPort == 0`) are
skipped.

### Lifecycle

- **Host side:** `Forwarder.Start()` opens listeners, returns
  immediately. `Forwarder.Stop()` closes listeners and drains
  in-flight connections.
- **VM side:** `VMListener.Serve(ctx)` runs an accept loop. Cancelled
  via context on shutdown.

## Alternatives Considered

### TAP + bridge + iptables

A TAP interface on the host with a bridge and iptables NAT rules would
make VM ports directly addressable. However:

- Requires root or `CAP_NET_ADMIN`.
- Complex OS-level networking configuration that varies by platform.
- iptables rule management is error-prone and hard to clean up.

### socat / SSH tunneling

External tools could forward ports but add process management overhead
and don't integrate with the composition lifecycle.

## Consequences

- Published TCP ports are reachable from the host without any special
  privileges beyond what vsock already requires.
- UDP forwarding is not supported (logged as a warning).
- The `Dialer` function is injected for testability — production uses
  `vsock.Dial`, tests use a TCP dialer.
- The `--vm-portfwd-port` flag (default 1025) and `--portfwd-port`
  flag on `serve` allow overriding the vsock port.
