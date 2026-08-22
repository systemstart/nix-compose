# ADR-003: CNI plugin networking

**Status:** Accepted
**Date:** 2026-04-01
**Context:** CRI migration (M2)

## Context

CRI delegates networking entirely to CNI plugins. Unlike Docker Compose,
which manages networks and DNS internally, a CRI-based orchestrator must
provide its own network configuration.

Alternatives considered:
- Embedded DNS server (complex, reinvents existing tooling)
- `/etc/hosts` file injection (fragile, no dynamic updates)
- Using CNI plugins with `dnsname` for DNS (standard, well-tested)

## Decision

Use a per-project CNI bridge network with the `dnsname` plugin for DNS
resolution. Each project gets:

- A dedicated Linux bridge (`nc-{project}`) with a deterministic /24 subnet
- A CNI conflist at `/etc/cni/net.d/nix-compose-{project}.conflist`
- Plugin chain: `bridge` + `host-local` IPAM + `portmap` + `firewall` + `dnsname`

The `dnsname` plugin (from `containers/dnsname`) runs a dnsmasq instance
per bridge. Services resolve each other by name.

`network_mode: host` bypasses CNI and uses `NamespaceMode_NODE`.

When CNI plugins are missing, a warning is printed and host networking is
used as a graceful fallback.

## Consequences

- Service-to-service DNS works (`wget http://svcB:8080/`)
- Requires CNI plugins installed on the host (`bridge`, `host-local`,
  `portmap`, `firewall`, `dnsname`)
- Rootless mode is unsupported for CNI bridge (needs `slirp4netns` or
  `pasta` which is a separate concern)
- Stale conflist/bridge cleanup needed if nix-compose crashes
- Deterministic subnet from project name hash can theoretically collide
