// Package integration holds tests that cross package boundaries.
package integration

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/s3arthak/arcdb/internal/algorithms"
	"github.com/s3arthak/arcdb/internal/graph"
	"github.com/s3arthak/arcdb/internal/storage"
)

func TestBFSReachesEdgesAddedThroughWALAndDelta(t *testing.T) {
	mutable := openMutableGraph(t, 4, []graph.Edge{
		{From: 0, To: 1},
		{From: 1, To: 2},
	})
	if err := mutable.AddEdge(2, 3); err != nil {
		t.Fatalf("AddEdge(2, 3) error = %v", err)
	}

	order, err := algorithms.BFS(mutable, 0)
	if err != nil {
		t.Fatalf("BFS(0) error = %v", err)
	}
	want := []uint32{0, 1, 2, 3}
	if !slices.Equal(order, want) {
		t.Fatalf("BFS(0) = %v, want %v", order, want)
	}
}

func TestShortestPathUsesDeltaEdges(t *testing.T) {
	tests := []struct {
		name        string
		vertexCount int64
		edges       []graph.Edge
		added       [][2]uint32
		source      uint32
		target      uint32
		wantPath    []uint32
	}{
		{
			name:        "delta shortcut wins over snapshot chain",
			vertexCount: 4,
			edges: []graph.Edge{
				{From: 0, To: 1},
				{From: 1, To: 2},
				{From: 2, To: 3},
			},
			added:    [][2]uint32{{0, 3}},
			source:   0,
			target:   3,
			wantPath: []uint32{0, 3},
		},
		{
			name:        "delta completes an otherwise unreachable path",
			vertexCount: 3,
			edges:       []graph.Edge{{From: 0, To: 1}},
			added:       [][2]uint32{{1, 2}},
			source:      0,
			target:      2,
			wantPath:    []uint32{0, 1, 2},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mutable := openMutableGraph(t, tc.vertexCount, tc.edges)
			for _, edge := range tc.added {
				if err := mutable.AddEdge(edge[0], edge[1]); err != nil {
					t.Fatalf("AddEdge(%d, %d) error = %v", edge[0], edge[1], err)
				}
			}

			path, found, err := algorithms.ShortestPath(mutable, tc.source, tc.target)
			if err != nil {
				t.Fatalf("ShortestPath(%d, %d) error = %v", tc.source, tc.target, err)
			}
			if !found {
				t.Fatalf("ShortestPath(%d, %d) found = false, want true", tc.source, tc.target)
			}
			if !slices.Equal(path, tc.wantPath) {
				t.Fatalf("ShortestPath(%d, %d) = %v, want %v", tc.source, tc.target, path, tc.wantPath)
			}
		})
	}
}

func TestBFSWithDisconnectedSnapshotAndDelta(t *testing.T) {
	mutable := openMutableGraph(t, 6, []graph.Edge{
		{From: 0, To: 1},
		{From: 2, To: 3},
	})

	// Connect the two snapshot components through the delta.
	if err := mutable.AddEdge(1, 2); err != nil {
		t.Fatalf("AddEdge(1, 2) error = %v", err)
	}
	// This delta edge forms a separate component and must stay unreachable.
	if err := mutable.AddEdge(4, 5); err != nil {
		t.Fatalf("AddEdge(4, 5) error = %v", err)
	}

	order, err := algorithms.BFS(mutable, 0)
	if err != nil {
		t.Fatalf("BFS(0) error = %v", err)
	}
	want := []uint32{0, 1, 2, 3}
	if !slices.Equal(order, want) {
		t.Fatalf("BFS(0) = %v, want %v", order, want)
	}

	other, err := algorithms.BFS(mutable, 4)
	if err != nil {
		t.Fatalf("BFS(4) error = %v", err)
	}
	if want := []uint32{4, 5}; !slices.Equal(other, want) {
		t.Fatalf("BFS(4) = %v, want %v", other, want)
	}

	path, found, err := algorithms.ShortestPath(mutable, 0, 3)
	if err != nil {
		t.Fatalf("ShortestPath(0, 3) error = %v", err)
	}
	if !found || !slices.Equal(path, []uint32{0, 1, 2, 3}) {
		t.Fatalf("ShortestPath(0, 3) = %v, %t, want [0 1 2 3], true", path, found)
	}
}

// openMutableGraph builds a snapshot, saves it, and reopens it with an empty WAL.
func openMutableGraph(t *testing.T, vertexCount int64, edges []graph.Edge) *storage.MutableGraph {
	t.Helper()

	base, err := graph.BuildFromEdges(vertexCount, edges)
	if err != nil {
		t.Fatalf("BuildFromEdges(%d, %d edges) error = %v", vertexCount, len(edges), err)
	}

	dir := t.TempDir()
	snapshotPath := filepath.Join(dir, "graph.arcdb")
	if err := storage.Save(snapshotPath, base, 0); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	mutable, err := storage.OpenMutableGraph(snapshotPath, filepath.Join(dir, "graph.wal"))
	if err != nil {
		t.Fatalf("OpenMutableGraph() error = %v", err)
	}
	t.Cleanup(func() {
		_ = mutable.Close()
	})
	return mutable
}
