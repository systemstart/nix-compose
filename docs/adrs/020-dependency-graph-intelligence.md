# ADR-020: Dependency graph intelligence

**Status:** Accepted
**Date:** 2026-04-16
**Context:** M15 — Expose the orchestrate dependency graph through the CLI

## Context

The orchestrate engine (M11-M14) stores dependency links in BoltDB via
bidirectional `LinksBySourceId` / `LinksByTargetId` collections. The
existing `state show` command displays direct dependencies/dependents for
a single rollout, but there is no way to visualise the full DAG, compute
transitive closures, or get safety warnings when stopping a depended-on
service.

Users need visibility into the dependency graph for debugging, impact
analysis, and safe teardown of services.

## Decision

Add `graph show|deps|impact` commands and teardown safety warnings:

- **`nix-compose graph show`** — visualise the full resource DAG in text
  tree format (grouped by kind with dependency arrows) or DOT format
  for graphviz piping
- **`nix-compose graph deps <resource-id>`** — show transitive
  dependencies of a resource ("what does this need?")
- **`nix-compose graph impact <resource-id>`** — show transitive
  dependents of a resource ("what breaks if I remove this?")
- **Teardown safety** — `stop` prints a warning when a target service
  has running dependents; `--force` suppresses warnings

All graph commands are local-only (read from BoltDB directly),
consistent with how `state` works locally. gRPC remote mode for graph
commands is deferred until protoc becomes available — a TODO is left
for adding a `Graph` RPC.

### Implementation

- `Engine.Graph()` returns all nodes (rollouts) and edges (links) in
  the DAG
- `Engine.TransitiveDeps()` / `Engine.TransitiveDependents()` perform
  BFS traversal over `GetDependencies` / `GetDepending`, tracking a
  visited set to handle cycles

## Consequences

- Users can visualise the full DAG and understand resource relationships
- Impact analysis before removing or stopping services reduces accidental
  breakage
- Teardown safety warnings in `stop` alert users to potential side
  effects without blocking intentional operations
- Graph commands work without a running CRI client (read-only engine)
- gRPC remote mode is deferred; the `graph` subcommands only work
  locally or when the user has direct access to the BoltDB state
