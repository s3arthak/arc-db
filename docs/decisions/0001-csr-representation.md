# ADR 0001 — CSR as the initial graph representation

- **Status:** accepted, implemented in Phase 1
- **Date:** 2026-10-18
- **Deciders:** project owner

## Context

ArcDB needs a first physical representation for a directed graph with integer
vertex IDs. The expected workload is read-heavy: traversals (BFS, shortest path)
over a graph that is built once and queried many times.

Two candidates:

1. **Adjacency list** — `map[uint64][]uint64` or `[][]uint32`.
2. **CSR (Compressed Sparse Row)** — two flat arrays:

   ```go
   type Graph struct {
       Offsets []uint64 // len = VertexCount + 1
       Edges   []uint32 // len = EdgeCount
   }
   ```

## Decision

Use **CSR** as the immutable, read-optimized representation.

For vertex `v`, neighbors are `Edges[Offsets[v]:Offsets[v+1]]`. `Offsets` has one
extra entry so every vertex has a half-open range, and empty vertices are
representable as repeated offsets.

## Why

- **Locality.** All neighbor lists live in one contiguous `Edges` array, so a
  traversal walks sequential memory instead of chasing pointers or map buckets.
- **Predictable memory.** `O(V + E)` storage with no per-vertex slice headers or
  map overhead. `uint32` edge targets cap a graph at ~4.29B edges while halving
  the memory of `[]uint64`.
- **Cheap construction.** Counting degrees then prefix-summing gives `O(V + E)`
  build time; no rebalancing or allocation per insert.
- **Trivially serializable.** Two flat arrays map cleanly onto a binary format
  later — no pointer fixups.

## Trade-offs

- **Updates are expensive.** Inserting an edge shifts the tail of `Edges`; CSR is
  effectively immutable. Mutable writes will need a separate delta layer
  (see the planned ADR on mutable deltas), not in-place edits.
- **Vertex IDs must be dense** in `[0, V)`. Sparse external IDs need a mapping
  table at the boundary.
- **Degree-proportional scans.** High-degree vertices cost more to scan than in a
  hash-based structure — acceptable for traversal, which usually wants all
  neighbors anyway.

## Alternatives rejected

- `map[uint64][]uint64`: simpler inserts, but pointer chasing, per-entry map
  overhead, and non-deterministic iteration hurt both cache behavior and
  reproducibility.
- `[][]uint32`: contiguous neighbor lists, but `V` slice headers plus `V`
  allocations; CSR is strictly more compact.

## Consequences

Phase 1 builds: `BuildFromEdges`, `Neighbors(v)`, `VertexCount`, `EdgeCount`,
with validation of out-of-range and negative IDs and deterministic output
ordering. Benchmarks comparing CSR against an adjacency list belong in
`benchmarks/` before the representation is considered settled.

## Implementation notes (Phase 1)

These details were settled while writing `internal/graph`.

- **Duplicate edges.** The kernel models a *simple* directed graph, not a
  multigraph: identical `(from, to)` pairs are collapsed during construction,
  keeping the first occurrence. Self-loops are preserved (and duplicate
  self-loops collapse too). `EdgeCount` therefore reports unique edges, which
  may be smaller than the number of input rows.
- **Edge-list IDs are `int64`.** The in-memory CSR stores targets as `uint32`,
  but the builder accepts `int64` so negative IDs can be rejected explicitly
  instead of wrapping. Valid IDs must be in `[0, vertexCount)` and
  `vertexCount` may not exceed 2^32.
- **Ordering.** Neighborhoods follow the order in which their edges first appear
  in the input list. No map iteration order is involved, so identical input
  always produces identical `Offsets` and `Edges`.
- **Complexity.** Construction is `O(V + E)` expected time and `O(V + E)` space:
  deduplication uses a hash set, then degrees are counted, prefix-summed into
  offsets, and the targets are scattered. `Neighbors` is `O(1)` to locate the
  range and `O(degree(v))` to read, with no allocation.
- **Invariants.** `Graph.Validate` re-checks offsets (start at 0, non-decreasing,
  final entry equals `len(Edges)`) and every target (`< VertexCount`). It exists
  for hand-built graphs and for data loaded from disk later.
