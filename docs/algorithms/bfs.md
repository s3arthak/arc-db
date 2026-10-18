# BFS traversal and shortest paths

Phase 2 adds the first consumer of the graph kernel: a breadth-first traversal
over the immutable CSR graph in `internal/graph`. The traversal itself is
deliberately plain — it is the baseline future strategies will be measured
against.

## Code map

| File | Responsibility |
| ---- | -------------- |
| `internal/algorithms/bfs.go` | `BFS`, `ShortestPath`, the shared traversal loop, source/target validation |
| `internal/algorithms/bfs_test.go` | table-driven tests: chains, branching, cycles, self-loops, disconnected vertices, invalid IDs |

The dependency is one-way:

```
algorithms (BFS, shortest path)
        |
        v
graph (CSR kernel + Neighbors)
```

`algorithms` never indexes `Offsets`/`Edges` itself; it only calls
`Graph.Neighbors`, so a different physical layout could be swapped in later
without touching traversal logic.

## How the traversal works

BFS keeps three pieces of state, each indexed by vertex ID:

- `visited[v]` — has `v` been reached?
- `parent[v]` — which vertex first discovered `v`?
- a FIFO queue of discovered-but-not-yet-expanded vertices.

Starting from the source, the loop pops the head of the queue, scans that
vertex's neighbor list, and appends every unseen neighbor to the queue and to
the visit order. A moving `head` index makes the slice a queue without shifting
elements or allocating per pop.

Exploring level by level is what makes the result useful: every vertex at
distance `d` is expanded before any vertex at distance `d+1`, so the first time
a vertex is reached it is reached by a shortest path.

`visited` is what makes traversal terminate. Without it, a cycle such as
`0 -> 1 -> 2 -> 0` or a self-loop `0 -> 0` would be processed forever.

## Shortest path reconstruction

`ShortestPath` reuses the same traversal and then walks `parent` backwards from
the target:

```
5 -> 3 -> 1 -> 0        (walk parents)
0 -> 1 -> 3 -> 5        (reverse)
```

The path includes both endpoints. The returned path is a slice of vertex IDs and
the function reports reachability separately:

```go
path, found, err := algorithms.ShortestPath(g, 0, 5)
```

- unreachable target: `found == false`, `path == nil`, `err == nil`
- invalid source or target: `err` wraps `graph.ErrVertexOutOfRange`
- source equal to target: `[source]`, `found == true`

## Tie-breaking

An unweighted graph can have many shortest paths of the same length. ArcDB does
not define a separate ranking rule in Phase 2; the path returned follows first
discovery, which is determined by the edge order the graph was built from (and
`BuildFromEdges` preserves input edge order within each source). Tests therefore
assert path *length* and edge validity, and only pin an exact path where it is
unambiguous.

## Complexity

| Operation | Time | Memory |
| --------- | ---- | ------ |
| `BFS` | `O(V + E)` | `O(V)` |
| `ShortestPath` | `O(V + E)` | `O(V)` |
| neighbor scan per vertex | `O(degree(v))`, contiguous in CSR | — |

The complexity is over the reachable part of the graph; unreachable vertices are
never enqueued. The `O(V)` bookkeeping is paid up front by allocating `visited`,
`parent`, queue, and result at full size once, so traversal does not reallocate.

Building the graph is `O(V + E)` expected as described in
[ADR 0001](../decisions/0001-csr-representation.md).

## Explicitly not here

This phase is a correctness baseline. It does not use bitmaps, goroutines,
bidirectional search, parallelism, custom allocators, or adaptive strategy
selection. Those belong to later phases and will be compared against this
implementation with benchmarks.
