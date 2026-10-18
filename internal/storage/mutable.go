package storage

import (
	"fmt"

	"github.com/s3arthak/arcdb/internal/graph"
)

// walWriter is the write surface MutableGraph needs from a WAL. Keeping it an
// interface lets tests inject append, sync, reset, and close failures
// deterministically.
type walWriter interface {
	AppendAddEdge(sequence uint64, source, destination uint32) error
	Sync() error
	Reset() error
	Close() error
}

// MutableGraph is the logical graph: an immutable CSR snapshot plus a mutable
// delta of edges recorded in the WAL.
//
// Write ordering is fixed. AddEdge validates the endpoints, reserves the next
// sequence number, appends the record to the WAL, syncs it, and only then
// publishes the edge in the delta. Reading merges snapshot and delta; the CSR
// arrays are never modified.
//
// A MutableGraph is not safe for concurrent use.
type MutableGraph struct {
	view         *graph.View
	delta        *graph.Delta
	wal          walWriter
	snapshotPath string

	// nextSequence is the sequence number the next successful append will use.
	nextSequence uint64

	// checkpoint is the highest sequence known to be represented by the
	// snapshot plus the fully applied delta. It only advances when sequence
	// numbers are contiguous and every write succeeded, so a failed append or
	// sync can never be skipped by a later checkpoint.
	checkpoint uint64

	// files isolates compaction's filesystem steps. Nil means the real OS.
	files snapshotFS
}

// OpenMutableGraph loads a snapshot and recovers the logical graph by replaying
// WAL records newer than the snapshot checkpoint into an empty delta.
//
// Recovery deliberately does not rebuild the CSR. Records with a sequence at or
// below the snapshot checkpoint are skipped because the snapshot already
// contains them; the rest are validated against the snapshot's vertex count and
// collected in the delta. The WAL is created when the path does not exist.
func OpenMutableGraph(snapshotPath, walPath string) (*MutableGraph, error) {
	snapshot, checkpoint, err := Load(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("storage: opening mutable graph: %w", err)
	}

	wal, err := OpenWAL(walPath)
	if err != nil {
		return nil, fmt.Errorf("storage: opening mutable graph: %w", err)
	}

	mutable, err := recoverDelta(snapshot, checkpoint, wal, snapshotPath)
	if err != nil {
		// The original error explains the failure; closing is best effort.
		wal.Close()
		return nil, fmt.Errorf("storage: opening mutable graph: %w", err)
	}
	return mutable, nil
}

// recoverDelta replays wal into a fresh delta over snapshot, skipping records
// already covered by checkpoint.
func recoverDelta(snapshot *graph.Graph, checkpoint uint64, wal *WAL, snapshotPath string) (*MutableGraph, error) {
	records, err := wal.Replay()
	if err != nil {
		return nil, err
	}

	vertexCount := uint64(snapshot.VertexCount())
	highest := checkpoint
	delta := graph.NewDelta()

	for i, record := range records {
		if record.Sequence > highest {
			highest = record.Sequence
		}
		if record.Sequence <= checkpoint {
			// The snapshot already contains this mutation.
			continue
		}
		if uint64(record.Source) >= vertexCount {
			return nil, fmt.Errorf(
				"%w: record %d has source %d, vertex count %d",
				ErrInvalidWAL, i, record.Source, vertexCount,
			)
		}
		if uint64(record.Destination) >= vertexCount {
			return nil, fmt.Errorf(
				"%w: record %d has destination %d, vertex count %d",
				ErrInvalidWAL, i, record.Destination, vertexCount,
			)
		}
		delta.AddEdge(record.Source, record.Destination)
	}

	if highest == ^uint64(0) {
		return nil, fmt.Errorf("storage: sequence numbers exhausted")
	}

	return &MutableGraph{
		view:         graph.NewView(snapshot, delta),
		delta:        delta,
		wal:          wal,
		snapshotPath: snapshotPath,
		nextSequence: highest + 1,
		checkpoint:   highest,
	}, nil
}

// AddEdge appends source -> destination to the WAL, syncs it, and then exposes
// the edge in the delta.
//
// The ordering makes the durability boundary explicit: the delta is not updated
// until Sync succeeds. If validation, the append, or the sync fails, the error
// is returned and the logical graph is unchanged. A failed sync means the
// record's durability is unknown — it may or may not survive a restart — but it
// is never reported as a successful mutation.
//
// Sequence numbers are reserved before the append so a failed write can never
// reuse a number; gaps are harmless to replay.
//
// Edges keep set semantics. Duplicate edges are still logged, but reads collapse
// them against the snapshot and the delta, so the logical graph never exposes
// the same edge twice.
func (m *MutableGraph) AddEdge(source, destination uint32) error {
	if err := m.validateVertex(source, "source"); err != nil {
		return err
	}
	if err := m.validateVertex(destination, "destination"); err != nil {
		return err
	}

	sequence := m.nextSequence
	m.nextSequence++

	if err := m.wal.AppendAddEdge(sequence, source, destination); err != nil {
		return fmt.Errorf("storage: adding edge %d -> %d: %w", source, destination, err)
	}
	if err := m.wal.Sync(); err != nil {
		return fmt.Errorf("storage: adding edge %d -> %d: %w", source, destination, err)
	}

	m.delta.AddEdge(source, destination)
	if sequence == m.checkpoint+1 {
		m.checkpoint = sequence
	}
	return nil
}

// VertexCount returns the number of vertices in the logical graph. Phase 7
// cannot create vertices, so this equals the snapshot's vertex count.
func (m *MutableGraph) VertexCount() int {
	return m.view.VertexCount()
}

// Neighbors returns the logical out-neighbors of vertex: snapshot edges
// followed by delta edges, without duplicates.
func (m *MutableGraph) Neighbors(vertex uint32) ([]uint32, error) {
	return m.view.Neighbors(vertex)
}

// Checkpoint returns the highest WAL sequence known to be fully represented by
// the current snapshot plus applied delta.
func (m *MutableGraph) Checkpoint() uint64 {
	return m.checkpoint
}

// Close closes the underlying WAL. It does not write a new snapshot; edges that
// have only been synced to the WAL remain in the delta on the next recovery.
func (m *MutableGraph) Close() error {
	if err := m.wal.Close(); err != nil {
		return fmt.Errorf("storage: closing mutable graph: %w", err)
	}
	return nil
}

// validateVertex rejects IDs that are not vertices of the snapshot.
func (m *MutableGraph) validateVertex(vertex uint32, role string) error {
	if uint64(vertex) < uint64(m.view.VertexCount()) {
		return nil
	}
	return fmt.Errorf("storage: %s vertex %d: %w", role, vertex, graph.ErrVertexOutOfRange)
}
