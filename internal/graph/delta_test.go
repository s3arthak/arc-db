package graph_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/s3arthak/arcdb/internal/graph"
)

func TestDeltaAddEdgeUsesSetSemantics(t *testing.T) {
	var delta graph.Delta // the zero value is a usable empty delta

	delta.AddEdge(0, 1)
	delta.AddEdge(0, 1) // duplicate
	delta.AddEdge(0, 2)
	delta.AddEdge(1, 0)
	delta.AddEdge(2, 2) // self-loop

	tests := []struct {
		name   string
		vertex uint32
		want   []uint32
	}{
		{"duplicates collapse, insertion order kept", 0, []uint32{1, 2}},
		{"second source", 1, []uint32{0}},
		{"self-loop retained once", 2, []uint32{2}},
		{"vertex without delta edges", 3, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := delta.Neighbors(tc.vertex); !slices.Equal(got, tc.want) {
				t.Errorf("Neighbors(%d) = %v, want %v", tc.vertex, got, tc.want)
			}
		})
	}
}

func TestViewNeighborsMergesSnapshotAndDelta(t *testing.T) {
	snapshot := mustBuildGraph(t, 4, []graph.Edge{
		{From: 0, To: 2},
		{From: 1, To: 0},
	})

	delta := graph.NewDelta()
	delta.AddEdge(0, 1) // new
	delta.AddEdge(0, 2) // already in snapshot
	delta.AddEdge(0, 3) // new
	delta.AddEdge(2, 1) // source with no snapshot edges
	delta.AddEdge(3, 3) // self-loop on an isolated vertex

	view := graph.NewView(snapshot, delta)

	tests := []struct {
		name   string
		vertex uint32
		want   []uint32
	}{
		{"snapshot edges then delta edges", 0, []uint32{2, 1, 3}},
		{"snapshot-only vertex", 1, []uint32{0}},
		{"delta-only source", 2, []uint32{1}},
		{"self-loop from delta", 3, []uint32{3}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := view.Neighbors(tc.vertex)
			if err != nil {
				t.Fatalf("Neighbors(%d) error = %v", tc.vertex, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Neighbors(%d) = %v, want %v", tc.vertex, got, tc.want)
			}
		})
	}

	if got, want := view.VertexCount(), snapshot.VertexCount(); got != want {
		t.Errorf("VertexCount() = %d, want %d", got, want)
	}
}

func TestViewWithEmptyDeltaReturnsSnapshotView(t *testing.T) {
	snapshot := mustBuildGraph(t, 3, []graph.Edge{{From: 0, To: 2}})
	view := graph.NewView(snapshot, nil)

	got, err := view.Neighbors(0)
	if err != nil {
		t.Fatalf("Neighbors(0) error = %v", err)
	}
	if !slices.Equal(got, []uint32{2}) {
		t.Fatalf("Neighbors(0) = %v, want [2]", got)
	}
}

func TestViewRejectsInvalidVertex(t *testing.T) {
	snapshot := mustBuildGraph(t, 2, nil)
	view := graph.NewView(snapshot, graph.NewDelta())

	if _, err := view.Neighbors(2); !errors.Is(err, graph.ErrVertexOutOfRange) {
		t.Fatalf("Neighbors(2) error = %v, want %v", err, graph.ErrVertexOutOfRange)
	}
}

func TestViewDoesNotModifySnapshot(t *testing.T) {
	snapshot := mustBuildGraph(t, 3, []graph.Edge{{From: 0, To: 1}})
	offsetsBefore := slices.Clone(snapshot.Offsets)
	edgesBefore := slices.Clone(snapshot.Edges)

	delta := graph.NewDelta()
	delta.AddEdge(0, 2)
	delta.AddEdge(1, 0)

	view := graph.NewView(snapshot, delta)
	if _, err := view.Neighbors(0); err != nil {
		t.Fatalf("Neighbors(0) error = %v", err)
	}
	if _, err := view.Neighbors(1); err != nil {
		t.Fatalf("Neighbors(1) error = %v", err)
	}

	if !slices.Equal(snapshot.Offsets, offsetsBefore) {
		t.Errorf("snapshot Offsets changed: %v, want %v", snapshot.Offsets, offsetsBefore)
	}
	if !slices.Equal(snapshot.Edges, edgesBefore) {
		t.Errorf("snapshot Edges changed: %v, want %v", snapshot.Edges, edgesBefore)
	}

	neighbors, err := snapshot.Neighbors(0)
	if err != nil {
		t.Fatalf("snapshot Neighbors(0) error = %v", err)
	}
	if !slices.Equal(neighbors, []uint32{1}) {
		t.Errorf("snapshot Neighbors(0) = %v, want [1]", neighbors)
	}
}

// mustBuildGraph builds a graph or fails the test.
func mustBuildGraph(t *testing.T, vertexCount int64, edges []graph.Edge) *graph.Graph {
	t.Helper()

	g, err := graph.BuildFromEdges(vertexCount, edges)
	if err != nil {
		t.Fatalf("BuildFromEdges(%d, %d edges) error = %v", vertexCount, len(edges), err)
	}
	return g
}
