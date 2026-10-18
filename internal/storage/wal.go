package storage

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/s3arthak/arcdb/internal/graph"
)

// WAL layout, all integers little-endian:
//
//	offset  size   field
//	0       8      magic, the ASCII bytes "ARCDBWAL"
//	8       4      format version (uint32), currently 2
//	12      ...    records, back to back
//
// Each record is framed as a fixed 16-byte record header followed by a payload:
//
//	offset  size            field
//	0       4               record type (uint32)
//	4       4               payload size in bytes (uint32)
//	8       8               sequence number (uint64)
//	16      payload size    payload
//
// Version 2 added the sequence number. Sequence numbers start at 1 and strictly
// increase in file order, so recovery can filter records already covered by a
// snapshot checkpoint and recreate the next sequence deterministically.
//
// The only defined record type is ADD_EDGE (1), whose payload is exactly two
// little-endian uint32 values: source, then destination. The explicit payload
// size leaves room for future record types without changing the framing.
const (
	walMagic     = "ARCDBWAL"
	walMagicSize = len(walMagic)

	walVersion       uint32 = 2
	walVersionOffset        = walMagicSize // 8
	walHeaderSize           = walVersionOffset + 4

	recordTypeOffset     = 0
	recordLengthOffset   = 4
	recordSequenceOffset = 8
	recordHeaderSize     = 16

	recordTypeAddEdge uint32 = 1

	addEdgePayloadSize = 8
	recordSize         = recordHeaderSize + addEdgePayloadSize
)

var (
	// ErrInvalidWAL reports a WAL that cannot be replayed: bad magic, a
	// truncated header, an unknown record type, out-of-order sequence numbers,
	// or a record whose framing is impossible for its type.
	ErrInvalidWAL = errors.New("storage: invalid WAL")

	// ErrUnsupportedWALVersion reports a well-formed header whose version this
	// build does not understand.
	ErrUnsupportedWALVersion = errors.New("storage: unsupported WAL version")
)

// AddEdge is one mutation recovered from the WAL: add source -> destination at
// the given sequence number.
//
// Phase 7 has exactly one mutation type. Recovery applies these mutations by
// rebuilding or extending the logical graph, never by modifying CSR arrays.
type AddEdge struct {
	Sequence    uint64
	Source      uint32
	Destination uint32
}

// WAL is an append-only log of graph mutations.
//
// Write ordering is explicit: AppendAddEdge writes the encoded record to the
// file, and only a successful Sync makes it durable. A WAL is intended for a
// single writer; concurrent appends are not supported.
//
// Call Close when finished. Closing does not imply Sync: durability is decided
// by Sync alone.
type WAL struct {
	file *os.File
	path string
}

// OpenWAL opens the WAL at path, creating it with a header if it does not exist.
//
// An existing file must start with a valid ArcDB WAL header; its records are not
// inspected here (Replay does that). Appends seek to the end of the file first,
// so they extend the log even after Replay. The handle is opened without
// O_APPEND because Windows does not allow truncating a handle opened in append
// mode, and Reset needs to truncate.
func OpenWAL(path string) (*WAL, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("storage: opening WAL %q: %w", path, err)
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("storage: opening WAL %q: %w", path, err)
	}

	if info.Size() == 0 {
		// A new WAL starts with a synced header so the file identifies itself
		// before the first record is appended.
		if err := writeWALHeader(file); err != nil {
			file.Close()
			return nil, fmt.Errorf("storage: initializing WAL %q: %w", path, err)
		}
		if err := file.Sync(); err != nil {
			file.Close()
			return nil, fmt.Errorf("storage: syncing WAL header in %q: %w", path, err)
		}
	} else if err := readWALHeader(file); err != nil {
		file.Close()
		return nil, fmt.Errorf("storage: opening WAL %q: %w", path, err)
	}

	return &WAL{file: file, path: path}, nil
}

// AppendAddEdge writes one ADD_EDGE record with the given sequence number.
//
// The record is handed to the operating system but is not durable until Sync
// returns nil. Callers that promise durability must call Sync afterwards.
func (w *WAL) AppendAddEdge(sequence uint64, source, destination uint32) error {
	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("storage: seeking in WAL %q: %w", w.path, err)
	}

	var record [recordSize]byte
	binary.LittleEndian.PutUint32(record[recordTypeOffset:recordLengthOffset], recordTypeAddEdge)
	binary.LittleEndian.PutUint32(record[recordLengthOffset:recordSequenceOffset], addEdgePayloadSize)
	binary.LittleEndian.PutUint64(record[recordSequenceOffset:recordHeaderSize], sequence)
	binary.LittleEndian.PutUint32(record[recordHeaderSize:recordHeaderSize+4], source)
	binary.LittleEndian.PutUint32(record[recordHeaderSize+4:recordSize], destination)

	if _, err := w.file.Write(record[:]); err != nil {
		return fmt.Errorf("storage: appending to WAL %q: %w", w.path, err)
	}
	return nil
}

// Sync flushes the WAL to stable storage.
//
// When Sync returns nil, every record appended before the call has been passed
// to the operating system with a flush request. ArcDB does not claim more than
// the OS and hardware actually guarantee.
func (w *WAL) Sync() error {
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("storage: syncing WAL %q: %w", w.path, err)
	}
	return nil
}

// Replay reads the WAL from the beginning and returns every complete ADD_EDGE
// record in file order.
//
// Sequence numbers must strictly increase; a record at or below the previous
// sequence is corruption and returns an error wrapping ErrInvalidWAL.
//
// An incomplete final record is treated as an interrupted append and ignored:
// the records before it are returned without an error. Complete records with an
// unknown type, impossible framing, or a non-increasing sequence are corruption
// and return an error wrapping ErrInvalidWAL. Replay never panics on malformed
// input.
func (w *WAL) Replay() ([]AddEdge, error) {
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("storage: replaying WAL %q: %w", w.path, err)
	}

	reader := bufio.NewReader(w.file)
	if err := readWALHeader(reader); err != nil {
		return nil, fmt.Errorf("storage: replaying WAL %q: %w", w.path, err)
	}

	var records []AddEdge
	offset := int64(walHeaderSize)
	var lastSequence uint64

	for {
		var recordHeader [recordHeaderSize]byte
		if _, err := io.ReadFull(reader, recordHeader[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				// Clean end of file, or the remains of an interrupted write.
				return records, nil
			}
			return nil, fmt.Errorf("storage: replaying WAL %q at offset %d: %w", w.path, offset, err)
		}

		recordType := binary.LittleEndian.Uint32(recordHeader[recordTypeOffset:recordLengthOffset])
		payloadSize := binary.LittleEndian.Uint32(recordHeader[recordLengthOffset:recordSequenceOffset])
		sequence := binary.LittleEndian.Uint64(recordHeader[recordSequenceOffset:recordHeaderSize])

		if recordType != recordTypeAddEdge {
			return nil, fmt.Errorf(
				"%w: unknown record type %d at offset %d",
				ErrInvalidWAL, recordType, offset,
			)
		}
		if payloadSize != addEdgePayloadSize {
			return nil, fmt.Errorf(
				"%w: ADD_EDGE record at offset %d has payload size %d, want %d",
				ErrInvalidWAL, offset, payloadSize, addEdgePayloadSize,
			)
		}
		if sequence <= lastSequence {
			return nil, fmt.Errorf(
				"%w: sequence %d at offset %d does not increase past %d",
				ErrInvalidWAL, sequence, offset, lastSequence,
			)
		}

		var payload [addEdgePayloadSize]byte
		if _, err := io.ReadFull(reader, payload[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				// The header was fully written but the payload was not: an
				// interrupted final append, so drop this record.
				return records, nil
			}
			return nil, fmt.Errorf("storage: replaying WAL %q at offset %d: %w", w.path, offset, err)
		}

		records = append(records, AddEdge{
			Sequence:    sequence,
			Source:      binary.LittleEndian.Uint32(payload[0:4]),
			Destination: binary.LittleEndian.Uint32(payload[4:8]),
		})
		lastSequence = sequence
		offset += recordSize
	}
}

// Reset truncates the WAL to its header and syncs that truncation, discarding
// every record.
//
// Callers must only reset after the discarded records are durably represented
// in a snapshot checkpoint. If Reset fails or the process crashes after the
// snapshot replacement but before the reset, recovery still filters those
// records by the snapshot's checkpoint.
func (w *WAL) Reset() error {
	if err := w.file.Truncate(int64(walHeaderSize)); err != nil {
		return fmt.Errorf("storage: truncating WAL %q: %w", w.path, err)
	}
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("storage: syncing truncated WAL %q: %w", w.path, err)
	}
	return nil
}

// Close closes the WAL file. It does not sync first; call Sync explicitly when
// the appended records must be durable.
func (w *WAL) Close() error {
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("storage: closing WAL %q: %w", w.path, err)
	}
	return nil
}

// Recover rebuilds the graph state described by a snapshot plus the WAL written
// after it.
//
// Records with a sequence at or below the snapshot checkpoint are skipped,
// because the snapshot already contains them. Remaining records are applied in
// order and the graph is rebuilt with graph.BuildFromEdges. The immutable CSR
// arrays are only read, never modified.
//
// The WAL is created empty when the path does not exist, so a snapshot with no
// mutations is still a valid recovery input.
func Recover(snapshotPath, walPath string) (*graph.Graph, error) {
	base, checkpoint, err := Load(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("storage: recovering: %w", err)
	}

	wal, err := OpenWAL(walPath)
	if err != nil {
		return nil, fmt.Errorf("storage: recovering: %w", err)
	}

	mutations, replayErr := wal.Replay()
	if closeErr := wal.Close(); replayErr == nil && closeErr != nil {
		return nil, fmt.Errorf("storage: recovering: %w", closeErr)
	}
	if replayErr != nil {
		return nil, fmt.Errorf("storage: recovering: %w", replayErr)
	}

	vertexCount := int64(base.VertexCount())
	edges, err := graphEdges(base)
	if err != nil {
		return nil, fmt.Errorf("storage: recovering: reading snapshot edges: %w", err)
	}

	for i, mutation := range mutations {
		if mutation.Sequence <= checkpoint {
			continue
		}
		if int64(mutation.Source) >= vertexCount {
			return nil, fmt.Errorf(
				"%w: record %d has source %d, vertex count %d",
				ErrInvalidWAL, i, mutation.Source, vertexCount,
			)
		}
		if int64(mutation.Destination) >= vertexCount {
			return nil, fmt.Errorf(
				"%w: record %d has destination %d, vertex count %d",
				ErrInvalidWAL, i, mutation.Destination, vertexCount,
			)
		}
		edges = append(edges, graph.Edge{
			From: int64(mutation.Source),
			To:   int64(mutation.Destination),
		})
	}

	recovered, err := graph.BuildFromEdges(vertexCount, edges)
	if err != nil {
		return nil, fmt.Errorf("storage: recovering: rebuilding graph: %w", err)
	}
	return recovered, nil
}

// graphEdges lists the edges of g through the public neighbor API.
func graphEdges(g *graph.Graph) ([]graph.Edge, error) {
	edges := make([]graph.Edge, 0, g.EdgeCount())

	for v := 0; v < g.VertexCount(); v++ {
		neighbors, err := g.Neighbors(uint32(v))
		if err != nil {
			return nil, err
		}
		for _, to := range neighbors {
			edges = append(edges, graph.Edge{From: int64(v), To: int64(to)})
		}
	}
	return edges, nil
}

// writeWALHeader writes the magic and version that prefix every WAL file.
func writeWALHeader(w io.Writer) error {
	var header [walHeaderSize]byte
	copy(header[:walMagicSize], walMagic)
	binary.LittleEndian.PutUint32(header[walVersionOffset:walHeaderSize], walVersion)

	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	return nil
}

// readWALHeader validates the magic and version at the start of a WAL.
func readWALHeader(r io.Reader) error {
	var header [walHeaderSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return fmt.Errorf("%w: truncated header: %w", ErrInvalidWAL, err)
	}

	if got := string(header[:walMagicSize]); got != walMagic {
		return fmt.Errorf("%w: magic %q, want %q", ErrInvalidWAL, got, walMagic)
	}
	if version := binary.LittleEndian.Uint32(header[walVersionOffset:walHeaderSize]); version != walVersion {
		return fmt.Errorf(
			"%w: version %d, supported %d",
			ErrUnsupportedWALVersion, version, walVersion,
		)
	}
	return nil
}
