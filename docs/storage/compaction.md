# Compaction and checkpointing

Compaction folds the durable state into a fresh checkpoint:

```
old snapshot + delta (WAL)  ->  new snapshot with checkpoint, empty delta, empty WAL
```

It is explicit — `storage.MutableGraph.Compact()` — and is not scheduled in the
background.

## Why compaction exists

Every `ADD_EDGE` appends a WAL record, and recovery replays every record above
the snapshot checkpoint. Without compaction the WAL and the in-memory delta grow
without bound, and recovery time grows with the number of mutations since the
last snapshot.

Compaction writes the current logical graph (snapshot + delta) as a new
immutable CSR snapshot and records the WAL position it represents, so those
records no longer need replaying.

## Invariant

Compaction must not change the logical graph:

```
Neighbors_before(v) == Neighbors_after(v)   for every vertex v
```

The same vertex count, edge count, duplicate-edge semantics, self-loops, and
disconnected vertices must survive. BFS and shortest-path results are therefore
unchanged; this is covered by tests.

## Sequence numbers and the checkpoint

Every WAL record carries a monotonic sequence number (`uint64`, starting at 1).
A snapshot stores a checkpoint `C`, meaning:

> every WAL record with `sequence <= C` is already represented in this snapshot.

Recovery skips records `<= C` and replays only newer ones. The checkpoint is
written into the snapshot header (format version 2), so it survives restarts
without any side file.

`MutableGraph` only advances its in-memory checkpoint through contiguous,
successful writes. If an append or sync fails, the checkpoint stops advancing
even though later writes may succeed, so compaction can never claim a record was
folded when it might not have been. The checkpoint may therefore lag; replaying
an already-folded record is harmless because edges are a set.

## Ordering

`Compact` performs, in order:

1. Read the logical graph (snapshot + delta) and build a new CSR with
   `graph.BuildFromEdges`.
2. Create a temporary file **in the snapshot's directory**.
3. Write the new snapshot (including checkpoint) to the temporary file.
4. Flush and `Sync` the temporary file.
5. Close it.
6. Atomically rename the temporary file over the authoritative snapshot.
7. Reset (truncate and sync) the WAL.
8. Clear the in-memory delta and swap the view to the new snapshot.

The WAL is never deleted before the replacement snapshot is durable. The delta
is only cleared after the snapshot is authoritative.

## Temporary file and atomic replacement

Writing directly over the authoritative snapshot would let a crash leave a
half-written graph. Compaction writes `graph.arcdb.tmp-*` next to the snapshot
and renames it:

- **Unix:** `rename(2)` atomically replaces the destination.
- **Windows:** Go's `os.Rename` calls `MoveFileEx` with
  `MOVEFILE_REPLACE_EXISTING`. It replaces an existing file, but can fail while
  the destination is open elsewhere, and Go does not promise Unix-style
  atomicity on non-Unix platforms. The same-directory temporary file avoids
  cross-volume moves and keeps the replacement as close to atomic as the
  platform allows.

If the process stops at any point before the rename, the old snapshot and the
WAL are still consistent, and an orphaned temporary file is harmless.

## WAL cleanup

After the rename, the snapshot contains every record through the checkpoint. The
WAL is truncated to its header (`WAL.Reset`) and the truncation is synced. If the
reset fails or the process stops between the rename and the reset, recovery
filters those old records by the snapshot checkpoint, so the graph is still
correct. The error is returned to the caller; the new snapshot is already
authoritative and the in-memory delta is left in place for that case.

`Reset` discards the whole log rather than a prefix, which is safe because
compaction folds the entire current logical graph: every record in the WAL is at
or below the new checkpoint.

## Crash-safety reasoning

| Crash point | Result on restart |
| ----------- | ----------------- |
| before/during temporary write | old snapshot authoritative, WAL intact, delta replayed |
| after temp sync, before rename | old snapshot authoritative, WAL intact |
| during rename | either old or new snapshot; both are valid graphs |
| after rename, before WAL reset | new snapshot authoritative; old WAL records skipped by checkpoint |
| during WAL reset | snapshot checkpoint still skips any surviving records |
| after reset, before delta clear | process state is lost; snapshot + empty WAL is correct |

No step removes the only durable copy of an acknowledged mutation.

## API

```go
mutable, err := storage.OpenMutableGraph("graph.arcdb", "graph.wal")
...
if err := mutable.AddEdge(0, 3); err != nil { ... }
if err := mutable.Compact(); err != nil { ... } // snapshot + checkpoint + WAL reset
```

`MutableGraph.Checkpoint()` exposes the highest sequence fully represented by
the current snapshot plus applied delta.

## Limitations

- **Explicit only.** No background or automatic compaction; no scheduling.
- **Single writer.** No concurrent compaction or locking; readers must not hold
  the snapshot open on Windows while it is replaced.
- **Whole-graph rewrite.** Compaction rebuilds and rewrites the entire CSR; its
  cost is `O(V + E)` plus file I/O, independent of how many delta edges exist.
- **No partial WAL truncation.** The whole log is reset rather than a prefix.
- **No directory fsync.** The rename itself is not separately forced to disk, so
  a crash immediately after rename relies on filesystem ordering.
- **No checksums.** Corruption that survives structural validation is not
  detected.
- **No compression.** The new snapshot is the same uncompressed format.
