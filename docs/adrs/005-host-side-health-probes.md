# ADR-005: Host-side health probes

**Status:** Accepted
**Date:** 2026-04-01
**Context:** CRI migration (M4)

## Context

CRI has no health check concept — kubelet handles that. Compose
conflates readiness and liveness into a single `healthcheck` directive.

Alternatives considered:
- In-container probes only (exec via `ExecSync`) — requires tools like
  `wget` or `curl` inside the container
- Host-side probes (HTTP GET and TCP dial from the host against the
  pod's IP) — no dependency on container contents
- Both, with host-side preferred

## Decision

Run health probes from the host side whenever possible:

- **Exec probes:** `ExecSync(container_id, command, timeout)` — exit
  code 0 means healthy
- **HTTP probes:** `http.Get` from the host against the pod IP — status
  200-399 means healthy
- **TCP probes:** `net.DialTimeout` from the host against pod IP + port

Probe selection priority: K8s-style readiness > K8s-style liveness >
Compose-style healthcheck.

Readiness gates dependency ordering. Liveness triggers restart.

## Consequences

- HTTP and TCP probes work without `wget`/`curl` installed in containers
- Pod IP is obtained from `PodSandboxStatus`, requiring CNI networking
  or host networking
- True readiness/liveness separation (unlike Compose which conflates them)
- Health monitor runs as goroutines with a state machine
  (Starting -> Healthy -> Unhealthy)
