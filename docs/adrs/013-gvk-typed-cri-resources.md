# ADR-013: GVK-typed CRI resource kinds

**Status:** Accepted
**Date:** 2026-04-13
**Context:** Orchestrate engine import (M11)

## Context

The orchestrator manages six kinds of resources: images, networks,
volumes, containers, services, and projects. These resources have
dependencies between them (a container depends on its image, network,
and volumes). The deployment pipeline needs a uniform way to identify,
instantiate, serialize, and track the status of any resource.

The infrastructure-tool used a Group/Version/Kind (GVK) typing system
inspired by Kubernetes API machinery. The question is whether to keep
this system, simplify to plain strings, or use Go interfaces alone.

## Decision

Keep the GVK typing system. All six CRI resource types are registered
under the `cri.orchestrator.io/v1` group:

| DefinitionKey | Kind |
|---|---|
| `cri.orchestrator.io/v1/image` | Image |
| `cri.orchestrator.io/v1/network` | Network |
| `cri.orchestrator.io/v1/volume` | Volume |
| `cri.orchestrator.io/v1/container` | Container |
| `cri.orchestrator.io/v1/service` | Service |
| `cri.orchestrator.io/v1/project` | Project |

Each resource type implements the `Definition` interface:

- `GetKey()` — returns the `DefinitionKey`
- `Instantiate(json.RawMessage)` — creates an `Instance` from spec
- `Load(json.RawMessage)` — deserializes a persisted instance
- `Delete(Reference)` — removes the resource
- `GetStatus(Reference)` / `GetProviderStatus(Reference)` — queries state

The `Registry` is instance-based (not a package-level global) and
injected into the `Engine`.

## Consequences

- Uniform resource identity: every resource is addressable as
  `group/version/kind + id`
- The dependency graph uses `Reference` (key + id) for edges — typed
  and serializable
- Manifest validation can check that references point to registered
  kinds before deployment
- The version component (`v1`) allows future schema evolution without
  breaking existing state in BoltDB
- Slightly more ceremony than plain strings, but the type safety
  prevents cross-kind reference errors at compile time

## Alternatives Considered

1. **Plain string keys** (e.g. `"image"`, `"container"`) — simpler
   but loses group namespacing and version evolution. Rejected.

2. **Go interfaces only** (no string key) — resources identified by
   Go type. Rejected: cannot serialize to BoltDB or manifest YAML.

3. **Kubernetes API types** (`metav1.TypeMeta`) — heavier dependency,
   pulls in `k8s.io/apimachinery`. Rejected: we only need GVK
   identification, not the full API machinery.
