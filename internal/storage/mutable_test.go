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

func TestMutableGraphAddsEdgesToSnapshotGraph(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := filepath.Join(dir, "graph.arcdb")
	walPath := filepath.Join(dir, "graph.wal")

	base := buildGraph(t, 4, []graph.Edge{
		{From: 0, To: 1},
		{From: 1, To: 2},
	})
	if err := Save(snapshotPath, base); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	mutable := openTestMutableGraph(t, snapshotPath, walPath)
	if err := mutable.AddEdge(0, 2); err != nil {
		t.Fatalf("AddEdge(0, 2) error = %v", err)
	}
	if err := mutable.AddEdge(1, 3); err != nil {
		t.Fatalf("AddEdge(1, 3) error = %v", err)
	}

	assertNeighbors(t, mutable, 0, []uint32{1, 2})
	assertNeighbors(t, mutable, 1, []uint32{2, 3})
	assertNeighbors(t, mutable, 3, nil)

	// Synced mutations must come back after closing and reopening.
	if err := mutable.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened := openTestMutableGraph(t, snapshotPath, walPath)
	assertNeighbors(t, reopened, 0, []uint32{1, 2})
	assertNeighbors(t, reopened, 1, []uint32{2, 3})
}

func TestMutableGraphAddEdgeFromVertexWithoutSnapshotEdges(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := filepath.Join(dir, "graph.arcdb")
	walPath := filepath.Join(dir, "graph.wal")

	base := buildGraph(t, 3, []graph.Edge{{From: 0, To: 1}})
	if err := Save(snapshotPath, base); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	mutable := openTestMutableGraph(t, snapshotPath, walPath)
	if err := mutable.AddEdge(2, 0); err != nil {
		t.Fatalf("AddEdge(2, 0) error = %v", err)
	}

	// Vertex 2 is isolated in the snapshot; its only logical edge is the delta.
	assertNeighbors(t, mutable, 2, []uint32{0})
}

func TestMutableGraphDuplicateEdgesStayUnique(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := filepath.Join(dir, "graph.arcdb")
	walPath := filepath.Join(dir, "graph.wal")

	base := buildGraph(t, 3, []graph.Edge{{From: 0, To: 1}})
	if err := Save(snapshotPath, base); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	mutable := openTestMutableGraph(t, snapshotPath, walPath)

	// Duplicate of an edge that already exists in the snapshot.
	if err := mutable.AddEdge(0, 1); err != nil {
		t.Fatalf("AddEdge(0, 1) error = %v", err)
	}
	// Duplicate within the delta.
	if err := mutable.AddEdge(0, 2); err != nil {
		t.Fatalf("AddEdge(0, 2) error = %v", err)
	}
	if err := mutable.AddEdge(0, 2); err != nil {
		t.Fatalf("AddEdge(0, 2) error = %v", err)
	}

	assertNeighbors(t, mutable, 0, []uint32{1, 2})

	// All three attempts were logged; set semantics apply at read time.
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
	if got, want := len(records), 3; got != want {
		t.Fatalf("WAL holds %d records, want %d", got, want)
	}
}

func TestMutableGraphSelfLoop(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := filepath.Join(dir, "graph.arcdb")
	walPath := filepath.Join(dir, "graph.wal")

	base := buildGraph(t, 2, nil)
	if err := Save(snapshotPath, base); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	mutable := openTestMutableGraph(t, snapshotPath, walPath)
	if err := mutable.AddEdge(1, 1); err != nil {
		t.Fatalf("AddEdge(1, 1) error = %v", err)
	}

	assertNeighbors(t, mutable, 1, []uint32{1})
}

func TestMutableGraphRejectsInvalidVertices(t *testing.T) {
	tests := []struct {
		name                string
		source, destination uint32
	}{
		{name: "invalid source", source: 3, destination: 0},
		{name: "invalid destination", source: 0, destination: 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			snapshotPath := filepath.Join(dir, "graph.arcdb")
			walPath := filepath.Join(dir, "graph.wal")

			base := buildGraph(t, 3, []graph.Edge{{From: 0, To: 1}})
			if err := Save(snapshotPath, base); err != nil {
				t.Fatalf("Save() error = %v", err)
			}

			mutable := openTestMutableGraph(t, snapshotPath, walPath)
			if err := mutable.AddEdge(tc.source, tc.destination); !errors.Is(err, graph.ErrVertexOutOfRange) {
				t.Fatalf("AddEdge(%d, %d) error = %v, want %v", tc.source, tc.destination, err, graph.ErrVertexOutOfRange)
			}

			assertNeighbors(t, mutable, 0, []uint32{1})

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
			if len(records) != 0 {
				t.Fatalf("invalid mutation was logged: %v", records)
			}
		})
	}
}

func TestMutableGraphWALAppendFailureLeavesDeltaUnchanged(t *testing.T) {
	base := buildGraph(t, 3, []graph.Edge{{From: 0, To: 1}})
	delta := graph.NewDelta()
	fake := &fakeWAL{appendErr: errWALAppend}
	mutable := &MutableGraph{
		view:  graph.NewView(base, delta),
		delta: delta,
		wal:   fake,
	}

	err := mutable.AddEdge(0, 2)
	if !errors.Is(err, errWALAppend) {
		t.Fatalf("AddEdge() error = %v, want it to wrap %v", err, errWALAppend)
	}
	if len(fake.appended) != 0 {
		t.Errorf("record was recorded despite append failure: %v", fake.appended)
	}
	if fake.syncs != 0 {
		t.Errorf("Sync() called %d times, want 0", fake.syncs)
	}
	assertNeighbors(t, mutable, 0, []uint32{1})
}

func TestMutableGraphWALSyncFailureLeavesDeltaUnchanged(t *testing.T) {
	base := buildGraph(t, 3, []graph.Edge{{From: 0, To: 1}})
	delta := graph.NewDelta()
	fake := &fakeWAL{syncErr: errWALSync}
	mutable := &MutableGraph{
		view:  graph.NewView(base, delta),
		delta: delta,
		wal:   fake,
	}

	err := mutable.AddEdge(0, 2)
	if !errors.Is(err, errWALSync) {
		t.Fatalf("AddEdge() error = %v, want it to wrap %v", err, errWALSync)
	}
	if len(fake.appended) != 1 {
		t.Errorf("appended %d records, want 1", len(fake.appended))
	}
	if fake.syncs != 1 {
		t.Errorf("Sync() called %d times, want 1", fake.syncs)
	}
	assertNeighbors(t, mutable, 0, []uint32{1})
}

func TestOpenMutableGraphRecoversWALIntoDelta(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := filepath.Join(dir, "graph.arcdb")
	walPath := filepath.Join(dir, "graph.wal")

	base := buildGraph(t, 5, []graph.Edge{
		{From: 0, To: 1},
		{From: 1, To: 2},
	})
	if err := Save(snapshotPath, base); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	wal := openTestWAL(t, walPath)
	appendTestEdges(t, wal,
		AddEdge{Source: 2, Destination: 3},
		AddEdge{Source: 4, Destination: 0},
		AddEdge{Source: 0, Destination: 1}, // already in the snapshot
	)
	if err := wal.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if err := wal.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	mutable := openTestMutableGraph(t, snapshotPath, walPath)
	assertNeighbors(t, mutable, 0, []uint32{1})
	assertNeighbors(t, mutable, 2, []uint32{3})
	assertNeighbors(t, mutable, 4, []uint32{0})
}

func TestOpenMutableGraphRejectsInvalidWALVertices(t *testing.T) {
	tests := []struct {
		name      string
		mutations []AddEdge
	}{
		{
			name:      "invalid destination",
			mutations: []AddEdge{{Source: 0, Destination: 5}},
		},
		{
			name:      "invalid source",
			mutations: []AddEdge{{Source: 5, Destination: 0}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			snapshotPath := filepath.Join(dir, "graph.arcdb")
			walPath := filepath.Join(dir, "graph.wal")

			base := buildGraph(t, 3, []graph.Edge{{From: 0, To: 1}})
			if err := Save(snapshotPath, base); err != nil {
				t.Fatalf("Save() error = %v", err)
			}

			wal := openTestWAL(t, walPath)
			appendTestEdges(t, wal, tc.mutations...)
			if err := wal.Sync(); err != nil {
				t.Fatalf("Sync() error = %v", err)
			}
			if err := wal.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}

			mutable, err := OpenMutableGraph(snapshotPath, walPath)
			if !errors.Is(err, ErrInvalidWAL) {
				t.Fatalf("OpenMutableGraph() error = %v, want %v", err, ErrInvalidWAL)
			}
			if mutable != nil {
				t.Fatalf("OpenMutableGraph() = %v, want nil on error", mutable)
			}
		})
	}
}

func TestMutableGraphDoesNotRewriteSnapshot(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := filepath.Join(dir, "graph.arcdb")
	walPath := filepath.Join(dir, "graph.wal")

	base := buildGraph(t, 3, []graph.Edge{{From: 0, To: 1}})
	if err := Save(snapshotPath, base); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	before, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	mutable := openTestMutableGraph(t, snapshotPath, walPath)
	if err := mutable.AddEdge(0, 2); err != nil {
		t.Fatalf("AddEdge(0, 2) error = %v", err)
	}
	if err := mutable.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	after, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("snapshot file changed after adding an edge")
	}
}

// errWALAppend and errWALSync are injected into fake WALs.
var (
	errWALAppend = errors.New("fake WAL append failure")
	errWALSync   = errors.New("fake WAL sync failure")
)

// fakeWAL records calls and fails on demand so write ordering can be tested
// without relying on filesystem errors.
type fakeWAL struct {
	appendErr error
	syncErr   error
	appended  []AddEdge
	syncs     int
	closed    bool
}

func (f *fakeWAL) AppendAddEdge(source, destination uint32) error {
	if f.appendErr != nil {
		return f.appendErr
	}
	f.appended = append(f.appended, AddEdge{Source: source, Destination: destination})
	return nil
}

func (f *fakeWAL) Sync() error {
	f.syncs++
	return f.syncErr
}

func (f *fakeWAL) Close() error {
	f.closed = true
	return nil
}

// openTestMutableGraph opens a mutable graph and closes it when the test ends.
func openTestMutableGraph(t *testing.T, snapshotPath, walPath string) *MutableGraph {
	t.Helper()

	mutable, err := OpenMutableGraph(snapshotPath, walPath)
	if err != nil {
		t.Fatalf("OpenMutableGraph(%q, %q) error = %v", snapshotPath, walPath, err)
	}
	t.Cleanup(func() {
		_ = mutable.Close()
	})
	return mutable
}

// assertNeighbors checks the logical neighbor list of one vertex.
func assertNeighbors(t *testing.T, mutable *MutableGraph, vertex uint32, want []uint32) {
	t.Helper()

	got, err := mutable.Neighbors(vertex)
	if err != nil {
		t.Fatalf("Neighbors(%d) error = %v", vertex, err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Neighbors(%d) = %v, want %v", vertex, got, want)
	}
}
