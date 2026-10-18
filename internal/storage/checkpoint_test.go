package storage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/s3arthak/arcdb/internal/graph"
)

func TestCompactPreservesLogicalGraph(t *testing.T) {
	tests := []struct {
		name        string
		vertexCount int64
		baseEdges   []graph.Edge
		added       [][2]uint32
	}{
		{
			name:        "empty delta",
			vertexCount: 3,
			baseEdges:   []graph.Edge{{From: 0, To: 1}},
		},
		{
			name:        "single delta edge",
			vertexCount: 4,
			baseEdges:   []graph.Edge{{From: 0, To: 1}},
			added:       [][2]uint32{{0, 2}},
		},
		{
			name:        "multiple delta edges",
			vertexCount: 5,
			baseEdges: []graph.Edge{
				{From: 0, To: 1},
				{From: 1, To: 2},
			},
			added: [][2]uint32{{0, 2}, {2, 3}, {3, 0}},
		},
		{
			name:        "duplicate edge already in snapshot",
			vertexCount: 3,
			baseEdges:   []graph.Edge{{From: 0, To: 1}},
			added:       [][2]uint32{{0, 1}},
		},
		{
			name:        "duplicate edge already in delta",
			vertexCount: 3,
			baseEdges:   []graph.Edge{{From: 0, To: 1}},
			added:       [][2]uint32{{0, 2}, {0, 2}},
		},
		{
			name:        "self-loop",
			vertexCount: 3,
			baseEdges:   []graph.Edge{{From: 0, To: 1}},
			added:       [][2]uint32{{2, 2}},
		},
		{
			name:        "disconnected graph",
			vertexCount: 6,
			baseEdges: []graph.Edge{
				{From: 0, To: 1},
				{From: 3, To: 4},
			},
			added: [][2]uint32{{1, 2}, {4, 5}},
		},
		{
			name:        "delta-only vertex",
			vertexCount: 4,
			baseEdges:   []graph.Edge{{From: 0, To: 1}},
			added:       [][2]uint32{{2, 3}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			snapshotPath := filepath.Join(dir, "graph.arcdb")
			walPath := filepath.Join(dir, "graph.wal")

			base := buildGraph(t, tc.vertexCount, tc.baseEdges)
			if err := Save(snapshotPath, base, 0); err != nil {
				t.Fatalf("Save() error = %v", err)
			}

			mutable := openTestMutableGraph(t, snapshotPath, walPath)
			for _, edge := range tc.added {
				if err := mutable.AddEdge(edge[0], edge[1]); err != nil {
					t.Fatalf("AddEdge(%d, %d) error = %v", edge[0], edge[1], err)
				}
			}

			before := snapshotNeighbors(t, mutable)
			checkpoint := mutable.Checkpoint()
			if err := mutable.Compact(); err != nil {
				t.Fatalf("Compact() error = %v", err)
			}

			if got := mutable.Checkpoint(); got != checkpoint {
				t.Errorf("Checkpoint() = %d, want %d", got, checkpoint)
			}
			assertNeighborsMatch(t, mutable, before)

			// The snapshot must hold the same logical graph and the checkpoint.
			loaded, loadedCheckpoint, err := Load(snapshotPath)
			if err != nil {
				t.Fatalf("Load() after compaction error = %v", err)
			}
			if loadedCheckpoint != checkpoint {
				t.Errorf("snapshot checkpoint = %d, want %d", loadedCheckpoint, checkpoint)
			}
			assertEquivalentGraphs(t, graphFromNeighbors(t, tc.vertexCount, before), loaded)

			// The WAL is reset and the delta is empty after compaction.
			wal, err := OpenWAL(walPath)
			if err != nil {
				t.Fatalf("OpenWAL() error = %v", err)
			}
			records, err := wal.Replay()
			if err != nil {
				t.Fatalf("Replay() error = %v", err)
			}
			if len(records) != 0 {
				t.Errorf("WAL holds %d records after compaction, want 0", len(records))
			}
			if err := wal.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}

			// Reopening recovers the same logical graph from snapshot alone.
			if err := mutable.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			reopened := openTestMutableGraph(t, snapshotPath, walPath)
			assertNeighborsMatch(t, reopened, before)
		})
	}
}

func TestCompactLargeDelta(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := filepath.Join(dir, "graph.arcdb")
	walPath := filepath.Join(dir, "graph.wal")

	const vertexCount = 200
	base := buildGraph(t, vertexCount, generatedEdges(vertexCount))
	if err := Save(snapshotPath, base, 0); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	mutable := openTestMutableGraph(t, snapshotPath, walPath)
	for i := 0; i < 500; i++ {
		source := uint32(i % vertexCount)
		destination := uint32((i*7 + 3) % vertexCount)
		if err := mutable.AddEdge(source, destination); err != nil {
			t.Fatalf("AddEdge(%d, %d) error = %v", source, destination, err)
		}
	}

	before := snapshotNeighbors(t, mutable)
	checkpoint := mutable.Checkpoint()
	if err := mutable.Compact(); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	assertNeighborsMatch(t, mutable, before)

	loaded, loadedCheckpoint, err := Load(snapshotPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loadedCheckpoint != checkpoint {
		t.Errorf("snapshot checkpoint = %d, want %d", loadedCheckpoint, checkpoint)
	}
	assertEquivalentGraphs(t, graphFromNeighbors(t, vertexCount, before), loaded)
}

func TestOpenMutableGraphFiltersByCheckpoint(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := filepath.Join(dir, "graph.arcdb")
	walPath := filepath.Join(dir, "graph.wal")

	base := buildGraph(t, 4, []graph.Edge{{From: 0, To: 1}})
	if err := Save(snapshotPath, base, 100); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	wal := openTestWAL(t, walPath)
	appendTestEdges(t, wal,
		AddEdge{Sequence: 99, Source: 0, Destination: 2},  // covered
		AddEdge{Sequence: 100, Source: 0, Destination: 3}, // covered
		AddEdge{Sequence: 101, Source: 1, Destination: 2}, // new
		AddEdge{Sequence: 102, Source: 2, Destination: 3}, // new
	)
	if err := wal.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if err := wal.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	mutable := openTestMutableGraph(t, snapshotPath, walPath)

	assertNeighbors(t, mutable, 0, []uint32{1}) // covered edges skipped
	assertNeighbors(t, mutable, 1, []uint32{2})
	assertNeighbors(t, mutable, 2, []uint32{3})

	if got, want := mutable.Checkpoint(), uint64(102); got != want {
		t.Errorf("Checkpoint() = %d, want %d", got, want)
	}

	// The next mutation continues after the highest replayed sequence.
	if err := mutable.AddEdge(1, 3); err != nil {
		t.Fatalf("AddEdge(1, 3) error = %v", err)
	}
	if err := mutable.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	wal, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("OpenWAL() error = %v", err)
	}
	defer wal.Close()

	records, err := wal.Replay()
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if len(records) == 0 || records[len(records)-1].Sequence != 103 {
		t.Fatalf("last WAL sequence = %v, want 103", records)
	}
}

func TestCompactContinuesSequenceAfterReset(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := filepath.Join(dir, "graph.arcdb")
	walPath := filepath.Join(dir, "graph.wal")

	base := buildGraph(t, 3, nil)
	if err := Save(snapshotPath, base, 0); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	mutable := openTestMutableGraph(t, snapshotPath, walPath)

	if err := mutable.AddEdge(0, 1); err != nil {
		t.Fatalf("AddEdge(0, 1) error = %v", err)
	}
	if err := mutable.Compact(); err != nil {
		t.Fatalf("first Compact() error = %v", err)
	}
	if got, want := mutable.Checkpoint(), uint64(1); got != want {
		t.Fatalf("Checkpoint() after first compaction = %d, want %d", got, want)
	}

	if err := mutable.AddEdge(0, 2); err != nil {
		t.Fatalf("AddEdge(0, 2) error = %v", err)
	}
	if err := mutable.Compact(); err != nil {
		t.Fatalf("second Compact() error = %v", err)
	}
	if got, want := mutable.Checkpoint(), uint64(2); got != want {
		t.Fatalf("Checkpoint() after second compaction = %d, want %d", got, want)
	}

	if err := mutable.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_, checkpoint, err := Load(snapshotPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if checkpoint != 2 {
		t.Errorf("snapshot checkpoint = %d, want 2", checkpoint)
	}
}

func TestCompactWALResetFailureKeepsRecoveryCorrect(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := filepath.Join(dir, "graph.arcdb")
	walPath := filepath.Join(dir, "graph.wal")

	base := buildGraph(t, 4, []graph.Edge{{From: 0, To: 1}})
	if err := Save(snapshotPath, base, 0); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	mutable := openTestMutableGraph(t, snapshotPath, walPath)
	if err := mutable.AddEdge(0, 2); err != nil {
		t.Fatalf("AddEdge(0, 2) error = %v", err)
	}
	if err := mutable.AddEdge(1, 2); err != nil {
		t.Fatalf("AddEdge(1, 2) error = %v", err)
	}
	before := snapshotNeighbors(t, mutable)

	realWAL, ok := mutable.wal.(*WAL)
	if !ok {
		t.Fatalf("wal is %T, want *WAL", mutable.wal)
	}
	mutable.wal = resetFailWAL{WAL: realWAL, err: errResetFailure}

	err := mutable.Compact()
	if !errors.Is(err, errResetFailure) {
		t.Fatalf("Compact() error = %v, want %v", err, errResetFailure)
	}

	if err := mutable.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// The replacement snapshot is authoritative even though the WAL was not
	// cleaned up.
	loaded, checkpoint, err := Load(snapshotPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if checkpoint != 2 {
		t.Errorf("snapshot checkpoint = %d, want 2", checkpoint)
	}
	assertEquivalentGraphs(t, graphFromNeighbors(t, 4, before), loaded)

	// Reopening filters the old WAL records by the snapshot checkpoint.
	reopened := openTestMutableGraph(t, snapshotPath, walPath)
	assertNeighborsMatch(t, reopened, before)
	assertNeighbors(t, reopened, 0, []uint32{1, 2})
}

func TestCompactFailureLeavesStateUntouched(t *testing.T) {
	tests := []struct {
		name          string
		files         *fakeSnapshotFS
		wantTempClean bool
	}{
		{
			name:  "temporary file creation fails",
			files: &fakeSnapshotFS{createErr: errTempFailure},
		},
		{
			name:          "snapshot write fails",
			files:         &fakeSnapshotFS{fileWriteErr: errWriteFailure},
			wantTempClean: true,
		},
		{
			name:          "snapshot sync fails",
			files:         &fakeSnapshotFS{fileSyncErr: errSyncFailure},
			wantTempClean: true,
		},
		{
			name:          "atomic replacement fails",
			files:         &fakeSnapshotFS{renameErr: errRenameFailure},
			wantTempClean: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			snapshotPath := filepath.Join(dir, "graph.arcdb")
			walPath := filepath.Join(dir, "graph.wal")

			base := buildGraph(t, 4, []graph.Edge{{From: 0, To: 1}})
			if err := Save(snapshotPath, base, 0); err != nil {
				t.Fatalf("Save() error = %v", err)
			}

			mutable := openTestMutableGraph(t, snapshotPath, walPath)
			if err := mutable.AddEdge(0, 2); err != nil {
				t.Fatalf("AddEdge(0, 2) error = %v", err)
			}

			before := snapshotNeighbors(t, mutable)
			beforeSnapshot, err := os.ReadFile(snapshotPath)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			beforeWAL, err := os.ReadFile(walPath)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}

			mutable.files = tc.files
			if err := mutable.Compact(); err == nil {
				t.Fatal("Compact() error = nil, want error")
			}

			assertNeighborsMatch(t, mutable, before)
			afterSnapshot, err := os.ReadFile(snapshotPath)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			if !bytes.Equal(beforeSnapshot, afterSnapshot) {
				t.Error("authoritative snapshot changed after a failed compaction")
			}
			afterWAL, err := os.ReadFile(walPath)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			if !bytes.Equal(beforeWAL, afterWAL) {
				t.Error("WAL changed after a failed compaction")
			}

			if tc.wantTempClean {
				if tc.files.file == nil {
					t.Fatal("fake filesystem never created a temporary file")
				}
				if !slices.Contains(tc.files.removed, tc.files.file.name) {
					t.Errorf("temporary file %q was not removed", tc.files.file.name)
				}
			}
		})
	}
}

// neighborSource is the read shape shared by snapshots and the logical view.
type neighborSource interface {
	VertexCount() int
	Neighbors(vertex uint32) ([]uint32, error)
}

// snapshotNeighbors copies every vertex's logical neighbor list.
func snapshotNeighbors(t *testing.T, g neighborSource) [][]uint32 {
	t.Helper()

	neighbors := make([][]uint32, g.VertexCount())
	for v := 0; v < g.VertexCount(); v++ {
		list, err := g.Neighbors(uint32(v))
		if err != nil {
			t.Fatalf("Neighbors(%d) error = %v", v, err)
		}
		neighbors[v] = slices.Clone(list)
	}
	return neighbors
}

// assertNeighborsMatch compares a graph's neighbor lists with a captured copy.
func assertNeighborsMatch(t *testing.T, g neighborSource, want [][]uint32) {
	t.Helper()

	got := snapshotNeighbors(t, g)
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("neighbors = %v, want %v", got, want)
	}
}

// graphFromNeighbors builds a CSR graph from vertex-ordered neighbor lists.
func graphFromNeighbors(t *testing.T, vertexCount int64, neighbors [][]uint32) *graph.Graph {
	t.Helper()

	edges := make([]graph.Edge, 0, vertexCount)
	for v, targets := range neighbors {
		for _, to := range targets {
			edges = append(edges, graph.Edge{From: int64(v), To: int64(to)})
		}
	}
	return buildGraph(t, vertexCount, edges)
}

// resetFailWAL wraps a real WAL and fails only on Reset.
type resetFailWAL struct {
	*WAL
	err error
}

func (w resetFailWAL) Reset() error {
	return w.err
}

// fakeSnapshotFS is an injectable filesystem for compaction failure tests.
type fakeSnapshotFS struct {
	createErr    error
	renameErr    error
	fileWriteErr error
	fileSyncErr  error
	file         *fakeSnapshotFile
	renamed      bool
	removed      []string
}

func (f *fakeSnapshotFS) CreateTemp(dir, pattern string) (snapshotFile, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.file = &fakeSnapshotFile{
		name:     filepath.Join(dir, "fake-snapshot.tmp"),
		writeErr: f.fileWriteErr,
		syncErr:  f.fileSyncErr,
	}
	return f.file, nil
}

func (f *fakeSnapshotFS) Rename(oldPath, newPath string) error {
	if f.renameErr != nil {
		return f.renameErr
	}
	f.renamed = true
	return nil
}

func (f *fakeSnapshotFS) Remove(path string) error {
	f.removed = append(f.removed, path)
	return nil
}

// fakeSnapshotFile records writes and fails on demand.
type fakeSnapshotFile struct {
	name     string
	buffer   bytes.Buffer
	writeErr error
	syncErr  error
	closed   bool
	synced   bool
}

func (f *fakeSnapshotFile) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return f.buffer.Write(p)
}

func (f *fakeSnapshotFile) Sync() error {
	f.synced = true
	return f.syncErr
}

func (f *fakeSnapshotFile) Close() error {
	f.closed = true
	return nil
}

func (f *fakeSnapshotFile) Name() string {
	return f.name
}

var (
	errResetFailure  = errors.New("fake WAL reset failure")
	errTempFailure   = errors.New("fake temporary file failure")
	errWriteFailure  = errors.New("fake snapshot write failure")
	errSyncFailure   = errors.New("fake snapshot sync failure")
	errRenameFailure = errors.New("fake snapshot rename failure")
)
