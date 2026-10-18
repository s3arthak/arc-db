# Architecture overview

ArcDB is grown layer by layer. Each layer may only depend on the layers below it.

```
                ┌─────────────────┐
                │    Query API    │  internal/server, internal/query
                └────────┬────────┘
                         ↓
                ┌─────────────────┐
                │ Query / Planner │  internal/query, internal/planner
                └────────┬────────┘
                         ↓
                ┌─────────────────┐
                │ Adaptive Engine │  internal/planner
                └────────┬────────┘
                         ↓
                ┌─────────────────┐
                │ Graph Algorithms│  internal/algorithms
                └────────┬────────┘
                         ↓
                ┌─────────────────┐
                │  Graph Kernel   │  internal/graph
                └────────┬────────┘
                         ↓
             ┌───────────────────────┐
             │     Storage / WAL     │  internal/storage
             └───────────────────────┘
```

## Package boundaries

| Package | Owns | Must not know about |
| ------- | ---- | ------------------- |
| `internal/graph` | vertex/edge representation, CSR building, delta overlay, logical view | files, HTTP, planning |
| `internal/algorithms` | traversals over the graph interface | storage, wire formats |
| `internal/planner` | cost estimation, strategy selection | parsing, I/O |
| `internal/query` | lexer, parser, AST, validation | storage internals |
| `internal/storage` | snapshot format, WAL, mutable graph, recovery | query syntax, planners |
| `internal/server` | request handling, wiring | algorithm details |

The dependency direction is one-way: upper layers import lower layers, never the
reverse. This keeps the graph kernel testable in isolation.

## Logical graph

`internal/graph` holds the immutable CSR snapshot and a mutable `Delta` of edges
added since it was loaded; `View` merges both for reads. `internal/storage`
owns the durable side: snapshots, the WAL, and `MutableGraph`, which enforces the
order validate -> WAL append -> Sync -> delta update. Algorithms depend on a
small `NeighborGraph` interface instead of a concrete graph type, so BFS and
shortest path read the logical graph without importing storage. `Compact` folds
the logical graph into a new snapshot with a WAL checkpoint and resets the log;
see [compaction.md](../storage/compaction.md).

## Phase gates

A phase is complete only when it has: implementation, colocated tests covering
edge cases, a benchmark if it makes a performance claim, and a doc or ADR
recording the trade-off. No phase starts before the previous one is committed.

## Open questions

- CSR vs adjacency list for the mutable layer (deferred to the delta design).
- How much of the cost model is static heuristics before measuring.
- Binary format versioning strategy (header magic + version + checksum).
