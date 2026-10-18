# CSR snapshot format

Phase 4 adds the first persistence artifact: a binary file that stores an
immutable CSR graph and loads it back without rebuilding from an edge list.

```
edge list -> graph.BuildFromEdges -> CSR -> storage.Save -> snapshot
snapshot  -> storage.Load                      -> CSR (equivalent graph)
```

The implementation lives in `internal/storage/snapshot.go`. A snapshot is a
checkpoint: it captures a full immutable graph and is written once and read back
whole. Mutations are logged separately in the write-ahead log described in
[wal.md](wal.md); this document covers only the snapshot file.

## Binary layout

All integers use **little-endian** byte order. There is no padding between
fields and no trailing data.

| Offset | Size | Field | Encoding |
| ------ | ---- | ----- | -------- |
| 0 | 8 | magic | ASCII `ARCDBSNP` |
| 8 | 4 | format version | `uint32`, currently `1` |
| 12 | 8 | vertex count (`V`) | `uint64` |
| 20 | 8 | edge count (`E`) | `uint64` |
| 28 | `8 * (V + 1)` | CSR offsets | `uint64` each, in vertex order |
| `28 + 8*(V+1)` | `4 * E` | CSR edges | `uint32` each, in CSR order |

Total file size is exactly:

```
28 + 8*(V + 1) + 4*E bytes
```

The arrays are the graph's arrays, byte for byte. Loading therefore reconstructs
the `Graph` by reading them back, not by re-running `BuildFromEdges`.

The version 1 header is 28 bytes because the magic, version, and counts are
packed without alignment padding; decoders read fixed offsets rather than a
struct.

## Saving

`Save(path, graph)`:

1. Rejects a nil graph and validates the CSR (`Graph.Validate`), so an invalid
   graph is never written.
2. Creates the file, writes the 28-byte header, then every offset and edge.
3. Writes through a buffered writer and checks flush/close errors.

The zero value of `graph.Graph` is a valid empty graph with no offsets
allocated; it is saved as the canonical single offset `0`. Identical graphs
always produce identical bytes.

## Loading

`Load(path)` treats the file as untrusted input and validates in this order:

1. File exists and is readable.
2. File is at least as long as the header (`28` bytes).
3. Magic matches `ARCDBSNP`.
4. Version is `1`; otherwise `ErrUnsupportedVersion`.
5. Vertex count is at most `2^32` (the graph kernel's ID limit).
6. The file is long enough for `V+1` offsets.
7. The remaining bytes form a whole number of `uint32` edges.
8. The derived edge count matches the header edge count (this also rejects
   trailing bytes).
9. Sizes fit the platform's `int`.
10. Only then are arrays allocated and filled.
11. The reconstructed CSR is passed through `Graph.Validate`, which re-checks:
    offsets start at `0`, offsets never decrease, the final offset equals the
    edge count, and every edge target is `< V`.

Any failure returns an error and no graph; the loader never panics on malformed
input.

## Errors

| Error | Meaning |
| ----- | ------- |
| `ErrInvalidSnapshot` | bad magic, truncated or oversized data, inconsistent counts, or an invalid CSR |
| `ErrUnsupportedVersion` | structurally readable header with a version this build cannot read |

Messages include the path and the specific check that failed, for example
`storage: loading snapshot "x.arcdb": storage: invalid snapshot: truncated
offsets: need 40 bytes for 4 vertices, have 12`.

## Limitations

- **No checksum.** Corruption is caught only when it breaks structure: a bit
  flip inside a neighbor list can still decode to a valid-looking graph. A
  checksum is deliberately out of scope for this phase.
- **Not atomic.** `Save` writes in place; a crash mid-write leaves a partial
  file. There is no temp-file-and-rename step or WAL yet.
- **Whole-file memory.** Loading allocates the full offsets and edges arrays;
  there is no mmap or streaming mode.
- **Version 1 only.** Older or newer formats are rejected rather than migrated.
- **Duplicate edges** are already collapsed by `BuildFromEdges`; snapshots store
  the unique edges exactly as the in-memory graph holds them.
