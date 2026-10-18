package storage

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/s3arthak/arcdb/internal/graph"
)

// snapshotFile is the file surface compaction needs. Tests inject failing
// implementations to exercise write, sync, and close errors.
type snapshotFile interface {
	io.Writer
	Sync() error
	Close() error
	Name() string
}

// snapshotFS isolates the filesystem steps of compaction so each failure
// boundary is testable. The real implementation is osSnapshotFS.
type snapshotFS interface {
	CreateTemp(dir, pattern string) (snapshotFile, error)
	Rename(oldPath, newPath string) error
	Remove(path string) error
}

// osSnapshotFS uses the operating system filesystem.
type osSnapshotFS struct{}

func (osSnapshotFS) CreateTemp(dir, pattern string) (snapshotFile, error) {
	return os.CreateTemp(dir, pattern)
}

func (osSnapshotFS) Rename(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}

func (osSnapshotFS) Remove(path string) error {
	return os.Remove(path)
}

// Compact folds the current logical graph — snapshot plus delta — into a new
// immutable snapshot and resets the WAL.
//
// Ordering is deliberate:
//
//  1. build the new CSR from the logical graph,
//  2. write and sync a temporary snapshot in the snapshot's directory,
//  3. rename it over the authoritative snapshot,
//  4. reset the WAL,
//  5. clear the delta.
//
// The WAL is never removed before the replacement snapshot is durable. If the
// process stops between the rename and the WAL reset, recovery skips records at
// or below the new snapshot checkpoint, so the graph is still correct. A
// failed step before the rename leaves the authoritative snapshot, the WAL, and
// the delta untouched. If the WAL reset fails after the rename, the new
// snapshot is already authoritative and the error is returned with the delta
// left in place; the logical graph remains correct, and a later recovery
// filters the old records by checkpoint.
//
// The checkpoint written is the highest sequence known to be fully represented
// by snapshot plus delta. It can lag behind the newest applied sequence after a
// failed append or sync; replaying already-incorporated records is harmless
// because edges are a set.
func (m *MutableGraph) Compact() error {
	compactGraph, err := m.logicalGraph()
	if err != nil {
		return fmt.Errorf("storage: compacting: building logical graph: %w", err)
	}

	files := m.files
	if files == nil {
		files = osSnapshotFS{}
	}

	directory := filepath.Dir(m.snapshotPath)
	pattern := filepath.Base(m.snapshotPath) + ".tmp-*"
	tempFile, err := files.CreateTemp(directory, pattern)
	if err != nil {
		return fmt.Errorf("storage: compacting: creating temporary snapshot: %w", err)
	}
	tempPath := tempFile.Name()
	replaced := false
	defer func() {
		if !replaced {
			// Best-effort cleanup; the original error is what matters.
			_ = files.Remove(tempPath)
		}
	}()

	writer := bufio.NewWriter(tempFile)
	if err := writeSnapshot(writer, compactGraph, m.checkpoint); err != nil {
		tempFile.Close()
		return fmt.Errorf("storage: compacting: writing temporary snapshot: %w", err)
	}
	if err := writer.Flush(); err != nil {
		tempFile.Close()
		return fmt.Errorf("storage: compacting: writing temporary snapshot: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		tempFile.Close()
		return fmt.Errorf("storage: compacting: syncing temporary snapshot: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("storage: compacting: closing temporary snapshot: %w", err)
	}

	// Rename within the same directory. On Unix this is atomic; on Windows Go
	// uses MoveFileEx with MOVEFILE_REPLACE_EXISTING, which replaces an existing
	// file but can fail while the destination is open elsewhere.
	if err := files.Rename(tempPath, m.snapshotPath); err != nil {
		return fmt.Errorf("storage: compacting: replacing snapshot: %w", err)
	}
	replaced = true

	// The new snapshot is authoritative. If the reset fails, old records stay in
	// the WAL and recovery will filter them out using the checkpoint.
	if err := m.wal.Reset(); err != nil {
		return fmt.Errorf("storage: compacting: resetting WAL: %w", err)
	}

	// Only now is the delta redundant.
	m.delta = graph.NewDelta()
	m.view = graph.NewView(compactGraph, m.delta)
	return nil
}

// logicalGraph materializes the current snapshot+delta view as a fresh CSR
// graph without modifying either source.
func (m *MutableGraph) logicalGraph() (*graph.Graph, error) {
	vertexCount := m.view.VertexCount()
	edges := make([]graph.Edge, 0, vertexCount)

	for v := 0; v < vertexCount; v++ {
		neighbors, err := m.view.Neighbors(uint32(v))
		if err != nil {
			return nil, err
		}
		for _, to := range neighbors {
			edges = append(edges, graph.Edge{From: int64(v), To: int64(to)})
		}
	}

	return graph.BuildFromEdges(int64(vertexCount), edges)
}
