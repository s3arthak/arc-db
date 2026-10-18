# Mutable delta layer

Phase 6 introduces the logical graph ArcDB reads and writes:

```
immutable CSR snapshot
        +
    mutable Delta
        +
    WAL (durability)
```

Code: `internal/graph/delta.go` (Delta, View) and `internal/storage/mutable.go`
(WAL-backed `MutableGraph`).

## Why a delta exists

CSR is a read-optimized layout: neighbors of `v` are one contiguous slice, which
makes traversal fast. The same layout is a poor fit for single-edge writes.
Inserting `0 -> 5` after construction would shift every later edge and invalidate
offsets, or require rewriting the whole file.

So the snapshot stays frozen and new edges accumulate in a delta:

```
snapshot: 0 -> [1, 2]
delta:    0 -> [3, 4]
logical:  0 -> [1, 2, 3, 4]
```

A delta write touches one short slice instead of the CSR arrays. The snapshot
remains a stable checkpoint that multiple readers could eventually share.

## Data structure

`graph.Delta` stores `map[uint32][]uint32`: source vertex to the edges added for
that source, in insertion order.

This is deliberately simple for the phase:

- additions append to one source's slice;
- duplicate detection scans that same, small slice (`O(degree)` per add);
- per-source order is deterministic, which keeps reads reproducible;
- no indexes, allocators, or locks are needed yet.

The zero value is a usable empty delta. A delta does not know the snapshot's
vertex count and does not validate IDs; the durable writer does that.

`graph.View` combines a snapshot and a delta. It exposes the same
`VertexCount`/`Neighbors` shape as `graph.Graph`.

## Write ordering

`storage.MutableGraph.AddEdge` follows a fixed order:

1. Validate that source and destination are vertices of the snapshot.
2. Append the `ADD_EDGE` record to the WAL.
3. `Sync` the WAL.
4. Add the edge to the delta.

The delta is updated only after `Sync` returns nil. If validation, the append,
or the sync fails, the error is returned and the logical graph is unchanged. A
failed sync means the record's durability is unknown — it may or may not appear
after a restart — but it is never reported as a successful mutation and never
becomes visible immediately.

The WAL is the durability source: closing the mutable graph does not write a new
snapshot, so synced edges still live in the WAL on the next recovery.

## Read semantics

`Neighbors(v)` merges:

1. the snapshot's neighbors, in CSR order, then
2. delta edges not already present in the snapshot.

No CSR array is modified. If the delta has nothing for `v`, the snapshot's slice
is returned as-is; otherwise a new, deduplicated slice is allocated and returned
read-only. This means reads allocate for vertices with delta edges, which is
acceptable for a baseline.

Duplicate edges are a set operation:

- adding an edge that already exists in the snapshot leaves the logical graph
  unchanged (the WAL record is still written);
- adding the same edge twice to the delta stores it once (the second append is
  logged but collapsed on read);
- self-loops are ordinary edges and behave the same way.

## Recovery

`storage.OpenMutableGraph(snapshotPath, walPath)`:

1. Loads and validates the snapshot and its checkpoint (`Load`), leaving the
   CSR immutable.
2. Creates an empty `graph.Delta`.
3. Replays the WAL (`WAL.Replay`).
4. Skips records with `sequence <= checkpoint`, because the snapshot already
   contains them.
5. Rejects surviving records naming vertices outside the snapshot's vertex
   count.
6. Applies each surviving record to the delta (set semantics).
7. Exposes `snapshot + delta` through the same `VertexCount`/`Neighbors` API.

The CSR is **not** rebuilt during recovery, so startup does not pay a full graph
rebuild. Rebuilding is still available through Phase 5's `storage.Recover`,
which deliberately materializes a new immutable graph; the delta path is the
one the mutable layer uses.

The WAL may contain an incomplete final record from an interrupted append. As in
Phase 5, that tail is ignored; complete malformed records are errors.

## Traversal integration

Algorithms do not import storage. `internal/algorithms` defines the small
`NeighborGraph` interface:

```go
type NeighborGraph interface {
    VertexCount() int
    Neighbors(vertex uint32) ([]uint32, error)
}
```

`*graph.Graph`, `*graph.View`, and `*storage.MutableGraph` all satisfy it, so
`BFS` and `ShortestPath` traverse the logical graph, including delta edges, with
no special cases.

## Compaction

Phase 7 folds the logical graph back into a new snapshot with
`MutableGraph.Compact`: it writes a temporary snapshot, syncs it, atomically
replaces the authoritative file, records the WAL checkpoint, resets the log, and
only then clears the delta. Sequence numbers and the snapshot checkpoint make
recovery filter records that were already folded in. The ordering and crash
windows are documented in [compaction.md](compaction.md); the delta itself still
has no automatic scheduling.

## Limitations

- **One writer, no concurrency.** No locks, no MVCC, no concurrent appends.
- **Growth between compactions.** The delta and WAL grow until an explicit
  `Compact` call; there is no automatic scheduling or partial truncation.
- **No deletes or vertex creation.** `ADD_EDGE` only, and both endpoints must
  already exist in the snapshot.
- **Merge cost.** Every read of a vertex with delta edges scans the snapshot
  neighbors and allocates a merged slice; duplicate detection inside the delta is
  a linear scan per add.
- **Recovery is not free.** It replays the entire WAL and validates every record,
  which is `O(W)` for `W` records, and `O(V + E)` to read the snapshot.
- **Single-process durability.** The WAL is synced, but there is no directory
  fsync and no crash-recovery protocol beyond replaying the log.
