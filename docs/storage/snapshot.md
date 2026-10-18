# CSR snapshot format

Snapshots are the checkpoint half of ArcDB's durability story: a binary file
that stores an immutable CSR graph plus the WAL sequence up to which the graph
is known to be complete.

```
edge list -> graph.BuildFromEdges -> CSR -> storage.Save -> snapshot
snapshot  -> storage.Load                      -> CSR + checkpoint
```

The implementation lives in `internal/storage/snapshot.go`. Mutations after a
snapshot live in the write-ahead log (see [wal.md](wal.md)); folding them back
into a snapshot is compaction (see [compaction.md](compaction.md)).

## Binary layout

All integers use **little-endian** byte order. There is no padding and no
trailing data. Format version **2** added the checkpoint field.

| Offset | Size | Field | Encoding |
| ------ | ---- | ----- | -------- |
| 0 | 8 | magic | ASCII `ARCDBSNP` |
| 8 | 4 | format version | `uint32`, currently `2` |
| 12 | 8 | vertex count (`V`) | `uint64` |
| 20 | 8 | edge count (`E`) | `uint64` |
| 28 | 8 | checkpoint sequence | `uint64` |
| 36 | `8 * (V + 1)` | CSR offsets | `uint64` each, vertex order |
| `36 + 8*(V+1)` | `4 * E` | CSR edges | `uint32` each, CSR order |

Total file size is exactly:

```
36 + 8*(V + 1) + 4*E bytes
```

## Checkpoint meaning

A snapshot's checkpoint `C` means:

> every WAL record with sequence `<= C` is already represented in this snapshot.

Recovery skips those records instead of replaying them. `C = 0` means the
snapshot has not absorbed any WAL records (the initial snapshot case).

The checkpoint is a monotonic lower bound. After a failed WAL append or sync it
may lag behind newer mutations; replaying records that are already in the
snapshot is harmless because adding an edge is idempotent.

## Saving

`Save(path, graph, checkpoint)`:

1. Rejects a nil graph and validates the CSR (`Graph.Validate`).
2. Creates the file, writes the header, every offset, and every edge.
3. Flushes and **syncs** the file before closing, so a successful call has
   handed the complete file to the OS with a flush request.

`Save` overwrites in place and is not atomic. Compaction does not call it
directly on the authoritative snapshot: it writes a temporary file and renames
it into place, so readers never observe a half-written snapshot.

The zero value of `graph.Graph` is a valid empty graph with no offsets
allocated; it is saved as the canonical single offset `0`. Identical graphs and
checkpoints always produce identical bytes.

## Loading

`Load(path)` returns `(*graph.Graph, checkpoint uint64, error)` and treats the
file as untrusted input. It validates, in order:

1. file exists and is readable;
2. at least `36` header bytes;
3. magic matches `ARCDBSNP`;
4. version is `2`; anything else is `ErrUnsupportedVersion` (no v1 migration);
5. vertex count is at most `2^32`;
6. the file holds `V+1` offsets;
7. the remaining bytes form a whole number of `uint32` edges;
8. the derived edge count matches the header (this also rejects trailing bytes);
9. array sizes fit the platform `int`;
10. only then are arrays allocated and read;
11. the reconstructed CSR passes `Graph.Validate` (offsets start at 0, never
    decrease, end at `E`, and every target is `< V`).

Any failure returns an error and no graph; the checkpoint is only returned when
the whole file is valid. The loader never panics on malformed input.

## Errors

| Error | Meaning |
| ----- | ------- |
| `ErrInvalidSnapshot` | bad magic, truncation, inconsistent counts, or invalid CSR |
| `ErrUnsupportedVersion` | readable header with an unsupported version (including v1) |

## Limitations

- **No checksum.** A bit flip that still satisfies the structural checks is not
  detected. Checksums are still out of scope.
- **Whole-file memory.** Loading allocates the full offsets and edges arrays;
  there is no mmap or streaming mode.
- **Version 2 only.** Version 1 files are rejected rather than migrated.
- **Snapshot writes are only atomic when compaction performs them.** A direct
  `Save` over an existing file can leave a partial file if interrupted.
- Duplicate edges are already collapsed by `BuildFromEdges`; snapshots store the
  in-memory graph exactly.
