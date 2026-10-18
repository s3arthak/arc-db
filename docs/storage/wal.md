# Write-ahead log and recovery

Phase 5 adds a minimal write-ahead log so graph mutations survive restarts
without waiting for a new snapshot. It supports exactly one mutation:
`ADD_EDGE(source, destination)`.

Implementation: `internal/storage/wal.go`.

## Why a WAL

CSR arrays are immutable and expensive to rewrite. A snapshot captures a full
graph, but rewriting the whole file for every new edge would be slow and would
risk losing the previous snapshot mid-write. Instead:

```
snapshot (checkpoint)  +  WAL (mutations since)  ->  recovered graph
```

The log is append-only, so a mutation costs one small write instead of a full
graph rewrite.

## Write-ahead ordering

The durability contract is explicit:

1. Encode the mutation as a WAL record.
2. Append the record to the WAL (`WAL.AppendAddEdge`).
3. Sync the WAL (`WAL.Sync`).
4. Only after `Sync` returns nil is the mutation considered durable.

`AppendAddEdge` alone only hands bytes to the operating system. `Close` does not
sync, so a caller that needs durability must call `Sync` before closing. ArcDB
does not claim more than the OS and hardware actually provide: `Sync` requests a
flush to stable storage, and the filesystem is trusted to honor it.

## File format

All integers are **little-endian**.

### File header (12 bytes)

| Offset | Size | Field | Value |
| ------ | ---- | ----- | ----- |
| 0 | 8 | magic | ASCII `ARCDBWAL` |
| 8 | 4 | version | `uint32`, currently `1` |

A newly created WAL is written with this header and synced immediately, so a
zero-length file is only ever a WAL that was created but not initialized.

### Record framing

| Offset | Size | Field |
| ------ | ---- | ----- |
| 0 | 4 | record type (`uint32`) |
| 4 | 4 | payload size in bytes (`uint32`) |
| 8 | declared size | payload |

The length field makes the framing extensible: future record types can define
their own payload size, and old readers reject unknown types instead of guessing
where the next record starts.

### Record types

| Value | Name | Payload |
| ----- | ---- | ------- |
| 1 | `ADD_EDGE` | `source uint32`, `destination uint32` (8 bytes) |

An `ADD_EDGE` record is therefore exactly 16 bytes: an 8-byte record header plus
an 8-byte payload. Records follow the file header with no padding.

## API

```go
wal, err := storage.OpenWAL("graph.wal") // creates and initializes if absent
defer wal.Close()

err = wal.AppendAddEdge(0, 1) // append, not yet durable
err = wal.Sync()              // now durable

records, err := wal.Replay()  // []storage.AddEdge in file order
```

- `OpenWAL` validates an existing header and rejects bad magic or version.
- `AppendAddEdge` writes one record; a single writer is assumed.
- `Sync` flushes the file.
- `Replay` seeks to the start, validates the header and every complete record,
  and returns `[]AddEdge`.
- `Close` closes the file without syncing.

## Replay policy

Replay is strict about complete records and lenient about a torn tail:

- **Incomplete final record is ignored.** If the file ends inside a record
  header or inside a payload, that is the signature of an interrupted append.
  The records before it are returned without an error. Replay is read-only and
  does not repair the file.
- **Complete malformed records are errors.** An unknown record type, or an
  `ADD_EDGE` whose declared payload size is not 8, returns an error wrapping
  `ErrInvalidWAL`, with the byte offset of the record.
- **Header problems are errors.** Bad magic and truncation wrap `ErrInvalidWAL`;
  an unsupported version wraps `ErrUnsupportedWALVersion`.
- Replay never panics on malformed input and never allocates from a length field
  (the only length a record may declare is the fixed `ADD_EDGE` payload).
- **Vertex IDs are checked during recovery,** where the snapshot's vertex count
  is known. A record that names a vertex outside the snapshot returns
  `ErrInvalidWAL`.

## Recovery

`storage.Recover(snapshotPath, walPath)`:

1. Load and validate the snapshot (`Load`).
2. Open the WAL, creating it if missing.
3. Replay every complete `ADD_EDGE` record in order.
4. Reject records whose source or destination is not a vertex in the snapshot.
5. Read the snapshot's edges through `Graph.Neighbors` (never by editing CSR
   arrays), append the mutations, and rebuild the graph with
   `graph.BuildFromEdges`.

Rebuilding rather than mutating keeps the CSR immutable; duplicate edges
collapse exactly as they do during a normal build. The result is a normal
immutable `Graph`, ready for traversal or a new snapshot.

## Limitations

- **One writer.** No locking, group commit, or concurrent appends.
- **No checksums.** Corruption that still parses as a valid record is not
  detected; this matches the snapshot format's current trade-off.
- **No torn-tail repair.** Replay ignores an incomplete final record, but
  `OpenWAL` does not truncate it. Appending to a WAL with a torn tail would place
  new records behind unreadable bytes, so a torn WAL should be recovered and
  replaced rather than appended to. There is no checkpoint or rotation logic yet.
- **Unbounded growth.** The WAL is never compacted; a future phase will replace
  it after a checkpoint.
- **No transactions or deletes.** Only `ADD_EDGE`, one record per mutation.
- **No directory fsync.** Creating the WAL file and its directory entry are not
  separately forced to disk.
- **Vertex IDs must already exist** in the snapshot. `ADD_EDGE` cannot introduce
  new vertices in this phase.
- **Full scan replay.** Recovery reads the whole WAL and rebuilds the whole
  graph, which is `O(V + E + W)`.
