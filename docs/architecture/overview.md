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
| `internal/graph` | vertex/edge representation, CSR building, neighbor access | files, HTTP, planning |
| `internal/algorithms` | traversals over the graph interface | storage, wire formats |
| `internal/planner` | cost estimation, strategy selection | parsing, I/O |
| `internal/query` | lexer, parser, AST, validation | storage internals |
| `internal/storage` | on-disk format, WAL, recovery | query syntax, planners |
| `internal/server` | request handling, wiring | algorithm details |

The dependency direction is one-way: upper layers import lower layers, never the
reverse. This keeps the graph kernel testable in isolation.

## Phase gates

A phase is complete only when it has: implementation, colocated tests covering
edge cases, a benchmark if it makes a performance claim, and a doc or ADR
recording the trade-off. No phase starts before the previous one is committed.

## Open questions

- CSR vs adjacency list for the mutable layer (deferred to the delta design).
- How much of the cost model is static heuristics before measuring.
- Binary format versioning strategy (header magic + version + checksum).
