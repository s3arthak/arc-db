# Write-ahead log and recovery

The WAL records graph mutations durably between snapshots. It supports exactly
one mutation: `ADD_EDGE(source, destination)`.

Implementation: `internal/storage/wal.go`.

## Why a WAL

Rewriting a whole snapshot for every edge is expensive and risks losing the
previous checkpoint mid-write. Instead:

```
snapshot (checkpoint)  +  WAL (mutations after the checkpoint)  ->  logical graph
```

The log is append-only, so a mutation costs one small write.

## Write-ahead ordering

The durability contract is explicit:

1. Validate the endpoints.
2. Reserve the next sequence number.
3. Append the record to the WAL.
4. Sync the WAL.
5. Only then is the mutation durable and visible in the delta.

`AppendAddEdge` alone only hands bytes to the operating system. `Close` does not
sync. ArcDB does not claim more than the OS and hardware provide.

## File format

All integers are **little-endian**.

### File header (12 bytes)

| Offset | Size | Field | Value |
| ------ | ---- | ----- | ----- |
| 0 | 8 | magic | ASCII `ARCDBWAL` |
| 8 | 4 | version | `uint32`, currently `2` |

### Record framing

| Offset | Size | Field |
| ------ | ---- | ----- |
| 0 | 4 | record type (`uint32`) |
| 4 | 4 | payload size in bytes (`uint32`) |
| 8 | 8 | sequence number (`uint64`) |
| 16 | payload size | payload |

### Record types

| Value | Name | Payload |
| ----- | ---- | ------- |
| 1 | `ADD_EDGE` | `source uint32`, `destination uint32` (8 bytes) |

An `ADD_EDGE` record is exactly 24 bytes: a 16-byte record header plus an 8-byte
payload. Version 2 added the sequence field; version 1 files are rejected.

## Sequence numbers

- Start at `1` for a fresh database (`checkpoint = 0`).
- Strictly increase in file order; replay rejects anything else as corruption.
- Persisted in every record, so recovery can rebuild the next number without
  external state.
- Assigned by `MutableGraph` and reserved **before** the append. A failed append
  or sync may leave a gap; gaps are harmless.
- After recovery the next number is `max(checkpoint, highest replayed) + 1`.

A snapshot's checkpoint `C` means every record with `sequence <= C` is already
represented in the snapshot. Recovery skips those records.

## API

```go
wal, err := storage.OpenWAL("graph.wal") // creates and initializes if absent
defer wal.Close()

err = wal.AppendAddEdge(sequence, 0, 1) // append, not yet durable
err = wal.Sync()                        // now durable

records, err := wal.Replay() // []storage.AddEdge{Sequence, Source, Destination}
err = wal.Reset()            // truncate all records (only after a checkpoint)
```

`OpenWAL` opens the file read-write without `O_APPEND`; appends seek to the end
explicitly. This is required so `Reset` can truncate on Windows, where a handle
opened in append mode cannot shrink the file. A single writer is assumed.

## Replay policy

- **Incomplete final record is ignored.** A file ending inside a record header
  or payload is treated as an interrupted append; earlier records are returned.
  Replay is read-only and does not repair the tail.
- **Complete malformed records are errors.** Unknown types, an `ADD_EDGE` whose
  declared payload size is not 8, or a sequence that does not strictly increase
  returns `ErrInvalidWAL` with the byte offset.
- **Header problems are errors.** Bad magic/truncation wrap `ErrInvalidWAL`; an
  unsupported version wraps `ErrUnsupportedWALVersion`.
- Replay never panics and never allocates from an untrusted length field.
- **Vertex IDs are checked during recovery,** where the snapshot's vertex count
  is known. Records at or below the checkpoint are skipped without ID checks.

## Recovery

`storage.Recover(snapshotPath, walPath)` materializes a new immutable graph:

1. Load the snapshot and its checkpoint.
2. Replay the WAL.
3. Skip records with `sequence <= checkpoint`.
4. Reject surviving records that name vertices outside the snapshot.
5. Rebuild with `graph.BuildFromEdges` from the snapshot edges plus mutations.

`storage.OpenMutableGraph` uses the same filtering but applies surviving
records to a delta instead of rebuilding; see [delta.md](delta.md) and
[compaction.md](compaction.md).

## Reset and cleanup

`WAL.Reset` truncates the file to its 12-byte header and syncs the truncation.
It is only safe after the records it removes are durably represented in a
snapshot checkpoint. Compaction performs that ordering; if the reset fails or
the process stops before it, the old records remain but recovery filters them by
checkpoint, so correctness is unchanged.

## Limitations

- **One writer.** No locking, group commit, or concurrent appends.
- **No checksums.** Corruption that still parses as a valid record is not
  detected.
- **No torn-tail repair.** Replay ignores an incomplete final record, but the
  WAL is not truncated at open. Appending behind a torn tail would make later
  records unreadable; recover and replace such a WAL instead.
- **Reset is all-or-nothing.** `Reset` discards the whole log; partial
  truncation by sequence is not implemented.
- **No directory fsync.** Creating the WAL file and its directory entry are not
  separately forced to disk.
