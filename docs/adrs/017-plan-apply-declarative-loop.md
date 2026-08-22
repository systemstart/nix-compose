# ADR-017: Plan/apply declarative loop

**Status:** Accepted
**Date:** 2026-04-14
**Context:** Plan/apply/reconcile (M13)

## Context

The orchestrate engine (M11) provides typed resource definitions, a
BoltDB state store, and a worker-pool deployment pipeline. The manifest
translation layer (M12) converts `eval.Composition` into typed manifests
with dependency edges. The missing piece is a declarative reconciliation
loop that diffs desired state against actual state and converges them.

Container orchestrators (Kubernetes, Terraform, Pulumi) all follow a
plan/apply model: compute a diff, preview it, then execute it. The
question is whether nix-compose should adopt the same model or use a
simpler "just apply everything" approach.

## Decision

Implement a plan/apply loop with explicit diff, preview, and
convergence phases.

### Plan phase

`ComputePlan` takes a `deploy.Deployment` (desired state) and compares
each request against existing rollouts in BoltDB:

- **Create**: no existing rollout for this resource ID
- **Update**: existing rollout with different body (detected via JSON
  comparison of the serialised spec)
- **Destroy**: existing rollout with no corresponding desired resource
  (orphan removal)
- **NoOp**: existing rollout with identical body

The plan produces an `Action` list with type, resource ID, GVK kind,
and a human-readable reason string. `Plan.Summary()` returns aggregate
counts.

### Apply phase

`ApplySync` executes a plan synchronously in dependency order:

1. Deletes run in **reverse** topological order (dependents first)
2. Creates run in **forward** topological order (dependencies first)
3. On create failure, previously-created resources are rolled back
   (best-effort reverse delete)

Topological sort uses Kahn's algorithm over the BoltDB dependency
links. Cycles and unreferenced nodes are appended alphabetically.

### CLI integration

- `nix-compose plan` — full eval → convert → bridge → plan pipeline
  with human-readable diff (`+`/`~`/`-` symbols per resource)
- `nix-compose state list` — tabular rollout listing
- `nix-compose state show <id>` — detailed view with dependencies and
  spec body
- `nix-compose up --orchestrate` — gated path that runs plan → apply,
  then falls through to foreground supervision

### Bridge function

`convert.Bridge()` converts `convert.Result` (manifests + edges) into a
`deploy.Deployment` by looking up each manifest's GVK in the registry,
instantiating it, and wiring dependency links. It also returns a
`ConditionMap` used by `waitCondition()` for health-gated dependencies.

## Alternatives considered

### Always-apply (no diff)

Re-create all resources every time. Simpler implementation but causes
unnecessary container restarts, image re-pulls, and network
re-creation. Rejected because version hashing and body comparison
provide cheap no-op detection.

### Event-driven reconciliation

Watch CRI events and reconcile continuously (like a Kubernetes
controller). Adds complexity and requires a long-running daemon.
Rejected for the CLI model — explicit `drift` and `rollback` commands
were implemented instead.

## Consequences

- Users can preview changes before applying (`plan`)
- Idempotent applies — running `up` twice with the same config is a
  no-op
- Orphan removal catches removed services automatically
- The plan/apply model enabled drift detection (`Engine.DriftCheck()`,
  `nix-compose drift`), rollback (`Engine.Rollback()`,
  `nix-compose rollback`), and condition-aware health gating
  (`waitCondition()` in `ApplySync`) — all now implemented
