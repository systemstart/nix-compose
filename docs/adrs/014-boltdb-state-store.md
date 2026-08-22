# ADR-014: BoltDB for orchestrator state persistence

**Status:** Accepted
**Date:** 2026-04-13
**Context:** Orchestrate engine import (M11)

## Context

The orchestrate engine needs an embedded database for deployment
history, rollout status, and the bidirectional dependency graph. The
dataset is small (hundreds of entries, not millions), the access
pattern is single-writer with concurrent reads, and the store must
be a single file with no external daemon.

## Decision

Use bbolt (`go.etcd.io/bbolt`) — the etcd-maintained fork of BoltDB.

bbolt is a B+ tree key-value store backed by a single memory-mapped
file. It provides fully serializable ACID transactions with a
single-writer, multiple-reader concurrency model.

### Why bbolt

- **Exact workload match.** Single-writer B+ tree is designed for
  small state stores with read-heavy or balanced access. Range scans
  (dependency graph traversal) are efficient.
- **Proven at scale.** etcd uses bbolt as its storage backend;
  Kubernetes depends on etcd. The library is battle-tested.
- **Pure Go, no CGO.** Cross-compiles trivially. No C compiler in
  the build chain. Works with Nix without special setup.
- **Actively maintained.** v1.4.3 (Aug 2025), v1.3.12 maintenance
  branch. Under the etcd-io / CNCF umbrella.
- **Simple API.** Buckets, keys, values, transactions. No query
  planner, no schema, no garbage collection tuning.
- **MIT license.** Maximally permissive.

## Alternatives Considered

1. **modernc.org/sqlite** (pure Go SQLite) — actively maintained
   (v1.48.2, Apr 2026), no CGO. Full SQL power. Runner-up if
   relational queries are needed later (JOINs across deployment and
   dependency tables). Rejected for now: SQL parsing overhead and
   schema management add complexity without current benefit. Can be
   reconsidered if the state model grows to need ad-hoc queries.

2. **Pebble** (cockroachdb/pebble) — LSM-tree engine built for
   CockroachDB. High performance but API stability is not guaranteed
   for external consumers. Overkill for a small state store.

3. **BadgerDB** (dgraph-io/badger) — LSM + value log, optimized for
   write-heavy large datasets. Higher read latency than bbolt, value
   log GC complexity, larger on-disk footprint. Poor fit for small
   state with balanced reads/writes.

4. **Plain JSON files** — no dependency, but no ACID transactions,
   no concurrent access safety, and manual serialization of the
   dependency graph.

## Consequences

- Single `state.bolt` file at `~/.local/share/nix-compose/state.bolt`
  (or `/var/lib/mc/state.bolt` in root mode)
- Four buckets: `DeploymentsById`, `RolloutsById`, `LinksBySourceId`,
  `LinksByTargetId`
- Dependency on `go.etcd.io/bbolt` added to `go.mod`
- If SQL is needed later, migration to SQLite is straightforward —
  export buckets as tables, links as rows
