# ADR-016: Pure manifest translation layer

**Status:** Accepted
**Date:** 2026-04-14
**Context:** Manifest translation layer (M12)

## Context

The orchestrate engine (M11) operates on typed manifests under
`cri.orchestrator.io/v1` with a dependency DAG. The Nix evaluator
produces `eval.Composition` (services, networks, volumes). A
translation step is needed to bridge these two representations.

The key design question is where this translation lives, what
responsibilities it has, and how it interacts with pre-processing
steps (profile filtering, init container synthesis).

## Decision

Implement translation as a pure, stateless function in
`pkg/orchestrate/convert/`:

```go
func Convert(comp *eval.Composition, opts Options) (*Result, error)
```

### Pure data transformation

`Convert()` is a pure function: no I/O, no CRI calls, no file access.
Given the same input, it always produces the same output. This makes
it trivially testable and safe to call from any context.

### Caller-runs pre-processing

Profile filtering (`compose.FilterByProfiles`) and init container
synthesis (`compose.SynthesizeInitContainers`) run **before**
`Convert()`. The convert package does not know about profiles or init
container expansion. This keeps each concern in its own package and
avoids coupling convert to compose-layer logic.

### Version hashing excludes ordering-only fields

The `Version` field on each `ContainerSpec` is a SHA-256 hash of the
service config, used by the engine to detect config drift. Two fields
are zeroed before hashing:

- `DependsOn` — affects startup ordering, not runtime behaviour
- `Profiles` — affects service selection, not runtime behaviour

Changing a dependency or profile assignment does not trigger a
container restart. Changing the image, environment, ports, or any
other runtime field does.

### One network per project

Rather than mapping individual Compose networks to separate CNI
networks, a single `Network` resource is created per project
(provided at least one service is not in `network_mode: host`). This
matches the CNI bridge-per-project model established in M2
(ADR-003).

### Named volume detection by path prefix

Volume mount strings are parsed as `source:dest[:options]`. The
source is classified as a bind mount (skipped) if it starts with
`/`, `./`, `~`, or `..`. Otherwise it is treated as a named volume
reference, and a dependency edge is created to the corresponding
Volume resource if it exists in the composition.

Nix store paths (`useHostStore`) are appended as `path:path:ro`
bind mounts and do **not** produce Volume resources or edges.

### Deterministic output ordering

Manifests are emitted in a fixed order: Project, Network, Images
(sorted), Volumes (sorted), Services (sorted). Edges follow the
same service iteration order. This ensures repeated calls produce
identical output for diffing and snapshot comparison.

## Consequences

- The convert package has no dependencies beyond `eval`, `manifest`,
  `resources`, and `typing` — no CRI client, no BoltDB, no CNI
- Unit tests cover the full translation without any infrastructure
- The plan/apply CLI (M13) can call `Convert()` after Nix eval and
  feed the result directly to `Engine.Plan()`
- Condition mapping is explicit and exhaustive: `service_started` →
  `"started"`, `service_healthy` → `"healthy"`,
  `service_completed_successfully` → `"completed"`; unknown
  conditions map to `""` (treated as `"started"` by the engine)

## Alternatives Considered

1. **Translation inside the engine** — `Engine.Plan()` accepts
   `eval.Composition` directly and translates internally. Rejected:
   mixes concerns, makes the engine depend on `eval` types, and
   prevents testing translation independently of deployment.

2. **YAML-based manifest generation** — write manifests to YAML
   files, then load them with `manifest.LoadFile()`. Rejected: adds
   a serialization round-trip, disk I/O, and temp file management
   for no benefit — the engine accepts `[]manifest.Manifest` in
   memory.

3. **Per-service network resources** — one Network per Compose
   network definition. Rejected: nix-compose uses a single CNI
   bridge per project (ADR-003); multiple networks would require
   multi-network CNI support that does not exist yet.
