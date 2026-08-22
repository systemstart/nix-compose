# ADR-002: Pod-per-service mapping model

**Status:** Accepted
**Date:** 2026-04-01
**Context:** CRI migration (M1)

## Context

CRI thinks in pods (groups of containers sharing a network namespace),
not in individual containers like Docker Compose. A mapping model was
needed to translate Compose service definitions into CRI resources.

## Decision

Each Compose service maps to one CRI PodSandbox containing one main
container. The mapping is:

| Compose concept | CRI concept |
|---|---|
| Project | Set of PodSandboxes, namespaced by labels |
| Service (one container) | PodSandbox + one Container |
| Service + init containers | PodSandbox + init Containers (sequential) + main Container |
| `network_mode: service:X` | Two containers in the same PodSandbox |
| Network | CNI config applied to PodSandbox |
| Volume (bind mount) | Mount in ContainerConfig |
| Volume (named) | Host directory managed by us, mounted as bind |
| Port binding | PortMapping on PodSandbox |

Container naming: `{project}-{service}` as pod name, `{service}` as
container name within the pod.

## Consequences

- Clean 1:1 mapping between services and pods
- Init containers get proper pod-level network and volume sharing
- `network_mode: service:X` maps naturally to shared pods
- Port mappings are on the pod, not the container (CRI requirement)
- Every assumption about compose single-container semantics had to be
  re-examined for pod semantics
