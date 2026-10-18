package storage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/s3arthak/arcdb/internal/graph"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	tests := []struct {
		name        string
		vertexCount int64
		edges       []graph.Edge
	}{
		{
			name:        "empty graph",
			vertexCount: 0,
		},
		{
			name:        "single vertex",
			vertexCount: 1,
		},
		{
			name:        "single edge",
			vertexCount: 2,
			edges:       []graph.Edge{{From: 0, To: 1}},
		},
		{
			name:        "multiple vertices",
			vertexCount: 5,
			edges: []graph.Edge{
				{From: 0, To: 1},
				{From: 1, To: 2},
				{From: 2, To: 3},
				{From: 3, To: 4},
			},
		},
		{
			name:        "disconnected vertices",
			vertexCount: 5,
			edges:       []graph.Edge{{From: 1, To: 3}, {From: 4, To: 0}},
		},
		{
			name:        "self loop",
			vertexCount: 1,
			edges:       []graph.Edge{{From: 0, To: 0}},
		},
		{
			name:        "multiple edges",
			vertexCount: 4,
			edges: []graph.Edge{
				{From: 0, To: 1},
				{From: 0, To: 2},
				{From: 0, To: 3},
				{From: 1, To: 3},
				{From: 2, To: 3},
			},
		},
		{
			name:        "generated graph",
			vertexCount: 5000,
			edges:       generatedEdges(5000),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			original := buildGraph(t, tc.vertexCount, tc.edges)
			path := filepath.Join(t.TempDir(), "snapshot.arcdb")

			if err := Save(path, original); err != nil {
				t.Fatalf("Save() error = %v, want nil", err)
			}
			loaded, err := Load(path)
			if err != nil {
				t.Fatalf("Load() error = %v, want nil", err)
			}

			assertEquivalentGraphs(t, original, loaded)
		})
	}
}

func TestSaveLoadZeroValueGraph(t *testing.T) {
	var zero graph.Graph

	path := filepath.Join(t.TempDir(), "snapshot.arcdb")
	if err := Save(path, &zero); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	if got := loaded.VertexCount(); got != 0 {
		t.Errorf("VertexCount() = %d, want 0", got)
	}
	if got := loaded.EdgeCount(); got != 0 {
		t.Errorf("EdgeCount() = %d, want 0", got)
	}
	if !slices.Equal(loaded.Offsets, []uint64{0}) {
		t.Errorf("Offsets = %v, want [0]", loaded.Offsets)
	}
	if err := loaded.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestSnapshotLayout(t *testing.T) {
	g := buildGraph(t, 3, []graph.Edge{{From: 0, To: 2}, {From: 2, To: 1}})
	data := encodeGraph(t, g)

	wantSize := headerSize + offsetBytes*(g.VertexCount()+1) + edgeByteSize*g.EdgeCount()
	if len(data) != wantSize {
		t.Fatalf("snapshot is %d bytes, want %d", len(data), wantSize)
	}
	if got := string(data[:magicSize]); got != magic {
		t.Errorf("magic = %q, want %q", got, magic)
	}
	if got := binary.LittleEndian.Uint32(data[versionOffset:vertexCountOffset]); got != formatVersion {
		t.Errorf("version = %d, want %d", got, formatVersion)
	}
	if got := binary.LittleEndian.Uint64(data[vertexCountOffset:edgeCountOffset]); got != 3 {
		t.Errorf("vertex count field = %d, want 3", got)
	}
	if got := binary.LittleEndian.Uint64(data[edgeCountOffset:headerSize]); got != 2 {
		t.Errorf("edge count field = %d, want 2", got)
	}

	position := headerSize
	for i, want := range g.Offsets {
		if got := binary.LittleEndian.Uint64(data[position:]); got != want {
			t.Errorf("stored offset %d = %d, want %d", i, got, want)
		}
		position += offsetBytes
	}
	for i, want := range g.Edges {
		if got := binary.LittleEndian.Uint32(data[position:]); got != want {
			t.Errorf("stored edge %d = %d, want %d", i, got, want)
		}
		position += edgeByteSize
	}
}

func TestSaveIsDeterministic(t *testing.T) {
	g := buildGraph(t, 4, []graph.Edge{
		{From: 0, To: 1},
		{From: 0, To: 2},
		{From: 1, To: 2},
		{From: 2, To: 3},
	})
	dir := t.TempDir()
	first := filepath.Join(dir, "first.arcdb")
	second := filepath.Join(dir, "second.arcdb")

	if err := Save(first, g); err != nil {
		t.Fatalf("Save(%q) error = %v", first, err)
	}
	if err := Save(second, g); err != nil {
		t.Fatalf("Save(%q) error = %v", second, err)
	}

	firstBytes, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", first, err)
	}
	secondBytes, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", second, err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("saving the same graph twice produced different bytes")
	}
}

func TestSaveRejectsInvalidGraph(t *testing.T) {
	tests := []struct {
		name  string
		graph *graph.Graph
	}{
		{
			name:  "nil graph",
			graph: nil,
		},
		{
			name:  "final offset does not match edge count",
			graph: &graph.Graph{Offsets: []uint64{0, 2}, Edges: []uint32{0}},
		},
		{
			name:  "edge target out of range",
			graph: &graph.Graph{Offsets: []uint64{0, 1}, Edges: []uint32{7}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "snapshot.arcdb")
			if err := Save(path, tc.graph); err == nil {
				t.Fatal("Save() error = nil, want error")
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("Save() created a file for an invalid graph")
			}
		})
	}
}

func TestLoadRejectsMalformedSnapshots(t *testing.T) {
	valid := encodeGraph(t, buildGraph(t, 3, []graph.Edge{
		{From: 0, To: 2},
		{From: 2, To: 1},
	}))

	tests := []struct {
		name    string
		data    []byte
		wantErr error
	}{
		{
			name:    "empty file",
			data:    nil,
			wantErr: ErrInvalidSnapshot,
		},
		{
			name:    "truncated header",
			data:    valid[:headerSize-1],
			wantErr: ErrInvalidSnapshot,
		},
		{
			name: "invalid magic",
			data: corrupt(valid, func(b []byte) {
				b[0] = 'X'
			}),
			wantErr: ErrInvalidSnapshot,
		},
		{
			name: "unsupported version",
			data: corrupt(valid, func(b []byte) {
				binary.LittleEndian.PutUint32(b[versionOffset:vertexCountOffset], formatVersion+1)
			}),
			wantErr: ErrUnsupportedVersion,
		},
		{
			name:    "truncated offsets",
			data:    valid[:headerSize+2*offsetBytes+edgeByteSize],
			wantErr: ErrInvalidSnapshot,
		},
		{
			name:    "truncated edge",
			data:    valid[:len(valid)-1],
			wantErr: ErrInvalidSnapshot,
		},
		{
			name:    "trailing byte",
			data:    append(slices.Clone(valid), 0xFF),
			wantErr: ErrInvalidSnapshot,
		},
		{
			name: "header edge count mismatch",
			data: corrupt(valid, func(b []byte) {
				binary.LittleEndian.PutUint64(b[edgeCountOffset:headerSize], 1)
			}),
			wantErr: ErrInvalidSnapshot,
		},
		{
			name:    "missing offsets",
			data:    rawSnapshot(3, 0, []uint64{0, 0}, nil),
			wantErr: ErrInvalidSnapshot,
		},
		{
			name:    "offsets decrease",
			data:    rawSnapshot(2, 2, []uint64{0, 2, 1}, []uint32{1, 1}),
			wantErr: ErrInvalidSnapshot,
		},
		{
			name:    "final offset mismatch",
			data:    rawSnapshot(2, 0, []uint64{0, 1, 1}, nil),
			wantErr: ErrInvalidSnapshot,
		},
		{
			name:    "edge target out of range",
			data:    rawSnapshot(2, 1, []uint64{0, 1, 1}, []uint32{5}),
			wantErr: ErrInvalidSnapshot,
		},
		{
			name:    "vertex count above limit",
			data:    rawSnapshot(maxVertexCount+1, 0, nil, nil),
			wantErr: ErrInvalidSnapshot,
		},
		{
			name:    "counts larger than the file",
			data:    rawSnapshot(maxVertexCount, 0, nil, nil),
			wantErr: ErrInvalidSnapshot,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "snapshot.arcdb")
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			g, err := Load(path)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Load() error = %v, want %v", err, tc.wantErr)
			}
			if g != nil {
				t.Fatalf("Load() graph = %v, want nil on error", g)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent.arcdb"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load() error = %v, want it to wrap %v", err, os.ErrNotExist)
	}
}

// assertEquivalentGraphs checks that two graphs expose identical structure and
// identical neighbor lists.
func assertEquivalentGraphs(t *testing.T, want, got *graph.Graph) {
	t.Helper()

	if got.VertexCount() != want.VertexCount() {
		t.Fatalf("VertexCount() = %d, want %d", got.VertexCount(), want.VertexCount())
	}
	if got.EdgeCount() != want.EdgeCount() {
		t.Fatalf("EdgeCount() = %d, want %d", got.EdgeCount(), want.EdgeCount())
	}
	if !slices.Equal(got.Offsets, want.Offsets) {
		t.Fatalf("Offsets = %v, want %v", got.Offsets, want.Offsets)
	}
	if !slices.Equal(got.Edges, want.Edges) {
		t.Fatalf("Edges = %v, want %v", got.Edges, want.Edges)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}

	for v := 0; v < want.VertexCount(); v++ {
		wantNeighbors, err := want.Neighbors(uint32(v))
		if err != nil {
			t.Fatalf("original Neighbors(%d) error = %v", v, err)
		}
		gotNeighbors, err := got.Neighbors(uint32(v))
		if err != nil {
			t.Fatalf("loaded Neighbors(%d) error = %v", v, err)
		}
		if !slices.Equal(gotNeighbors, wantNeighbors) {
			t.Fatalf("loaded Neighbors(%d) = %v, want %v", v, gotNeighbors, wantNeighbors)
		}
	}
}

// buildGraph creates a graph or fails the test.
func buildGraph(t *testing.T, vertexCount int64, edges []graph.Edge) *graph.Graph {
	t.Helper()

	g, err := graph.BuildFromEdges(vertexCount, edges)
	if err != nil {
		t.Fatalf("BuildFromEdges(%d, %d edges) error = %v", vertexCount, len(edges), err)
	}
	return g
}

// encodeGraph serializes g to memory with the same encoder Save uses.
func encodeGraph(t *testing.T, g *graph.Graph) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := writeSnapshot(&buf, g); err != nil {
		t.Fatalf("writeSnapshot() error = %v", err)
	}
	return buf.Bytes()
}

// corrupt copies data, applies fn to the copy, and returns it.
func corrupt(data []byte, fn func([]byte)) []byte {
	clone := slices.Clone(data)
	fn(clone)
	return clone
}

// rawSnapshot encodes arbitrary header counts and arrays, including values a
// valid graph could never produce. It lets corruption tests target one check at
// a time.
func rawSnapshot(vertexCount, edgeCount uint64, offsets []uint64, edges []uint32) []byte {
	data := make([]byte, headerSize+len(offsets)*offsetBytes+len(edges)*edgeByteSize)

	copy(data[:magicSize], magic)
	binary.LittleEndian.PutUint32(data[versionOffset:vertexCountOffset], formatVersion)
	binary.LittleEndian.PutUint64(data[vertexCountOffset:edgeCountOffset], vertexCount)
	binary.LittleEndian.PutUint64(data[edgeCountOffset:headerSize], edgeCount)

	position := headerSize
	for _, offset := range offsets {
		binary.LittleEndian.PutUint64(data[position:], offset)
		position += offsetBytes
	}
	for _, edge := range edges {
		binary.LittleEndian.PutUint32(data[position:], edge)
		position += edgeByteSize
	}
	return data
}

// generatedEdges builds a deterministic sparse edge list: four edges per vertex
// to (v + 1 + k*7) mod vertices.
func generatedEdges(vertices int) []graph.Edge {
	edges := make([]graph.Edge, 0, vertices*4)
	for v := 0; v < vertices; v++ {
		for k := 0; k < 4; k++ {
			edges = append(edges, graph.Edge{
				From: int64(v),
				To:   int64((v + 1 + k*7) % vertices),
			})
		}
	}
	return edges
}
