package algorithms_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/s3arthak/arcdb/internal/algorithms"
	"github.com/s3arthak/arcdb/internal/graph"
)

func TestBFS(t *testing.T) {
	tests := []struct {
		name        string
		vertexCount int64
		edges       []graph.Edge
		source      uint32
		want        []uint32
	}{
		{
			name:        "single vertex",
			vertexCount: 1,
			source:      0,
			want:        []uint32{0},
		},
		{
			name:        "chain",
			vertexCount: 4,
			edges: []graph.Edge{
				{From: 0, To: 1},
				{From: 1, To: 2},
				{From: 2, To: 3},
			},
			source: 0,
			want:   []uint32{0, 1, 2, 3},
		},
		{
			name:        "level order",
			vertexCount: 6,
			edges: []graph.Edge{
				{From: 0, To: 1},
				{From: 0, To: 2},
				{From: 1, To: 3},
				{From: 2, To: 4},
				{From: 3, To: 5},
				{From: 4, To: 5},
			},
			source: 0,
			want:   []uint32{0, 1, 2, 3, 4, 5},
		},
		{
			name:        "diamond",
			vertexCount: 4,
			edges: []graph.Edge{
				{From: 0, To: 1},
				{From: 0, To: 2},
				{From: 1, To: 3},
				{From: 2, To: 3},
			},
			source: 0,
			want:   []uint32{0, 1, 2, 3},
		},
		{
			name:        "disconnected vertices stay unreachable",
			vertexCount: 4,
			edges: []graph.Edge{
				{From: 0, To: 1},
				{From: 2, To: 3},
			},
			source: 0,
			want:   []uint32{0, 1},
		},
		{
			name:        "cycle terminates",
			vertexCount: 3,
			edges: []graph.Edge{
				{From: 0, To: 1},
				{From: 1, To: 2},
				{From: 2, To: 0},
			},
			source: 0,
			want:   []uint32{0, 1, 2},
		},
		{
			name:        "self loop terminates",
			vertexCount: 2,
			edges: []graph.Edge{
				{From: 0, To: 0},
				{From: 0, To: 1},
			},
			source: 0,
			want:   []uint32{0, 1},
		},
		{
			name:        "source with no outgoing edges",
			vertexCount: 3,
			source:      1,
			want:        []uint32{1},
		},
		{
			name:        "source in the middle",
			vertexCount: 3,
			edges: []graph.Edge{
				{From: 0, To: 1},
				{From: 1, To: 2},
			},
			source: 1,
			want:   []uint32{1, 2},
		},
		{
			name:        "neighbor order follows edge order",
			vertexCount: 3,
			edges: []graph.Edge{
				{From: 0, To: 2},
				{From: 0, To: 1},
			},
			source: 0,
			want:   []uint32{0, 2, 1},
		},
		{
			name:        "duplicate edges are harmless",
			vertexCount: 3,
			edges: []graph.Edge{
				{From: 0, To: 1},
				{From: 0, To: 1},
				{From: 1, To: 2},
			},
			source: 0,
			want:   []uint32{0, 1, 2},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := mustBuild(t, tc.vertexCount, tc.edges)

			got, err := algorithms.BFS(g, tc.source)
			if err != nil {
				t.Fatalf("BFS(%d) error = %v, want nil", tc.source, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("BFS(%d) = %v, want %v", tc.source, got, tc.want)
			}
			assertVisitOrder(t, g, got)
		})
	}
}

func TestBFSRejectsInvalidSource(t *testing.T) {
	g := mustBuild(t, 2, []graph.Edge{{From: 0, To: 1}})

	for _, source := range []uint32{2, 3, 100} {
		got, err := algorithms.BFS(g, source)
		if !errors.Is(err, graph.ErrVertexOutOfRange) {
			t.Errorf("BFS(%d) error = %v, want %v", source, err, graph.ErrVertexOutOfRange)
		}
		if got != nil {
			t.Errorf("BFS(%d) = %v, want nil on error", source, got)
		}
	}
}

func TestBFSOnEmptyGraph(t *testing.T) {
	var g graph.Graph

	if _, err := algorithms.BFS(&g, 0); !errors.Is(err, graph.ErrVertexOutOfRange) {
		t.Fatalf("BFS(0) error = %v, want %v", err, graph.ErrVertexOutOfRange)
	}
}

// assertVisitOrder checks the invariants every BFS result must satisfy: the
// order is duplicate-free and contains only real vertices.
func assertVisitOrder(t *testing.T, g *graph.Graph, order []uint32) {
	t.Helper()

	seen := make(map[uint32]bool, len(order))
	for _, vertex := range order {
		if uint64(vertex) >= uint64(g.VertexCount()) {
			t.Fatalf("visit order contains vertex %d, vertex count %d", vertex, g.VertexCount())
		}
		if seen[vertex] {
			t.Fatalf("visit order contains vertex %d twice: %v", vertex, order)
		}
		seen[vertex] = true
	}
}

// mustBuild builds a graph and fails the test on error.
func mustBuild(t *testing.T, vertexCount int64, edges []graph.Edge) *graph.Graph {
	t.Helper()

	g, err := graph.BuildFromEdges(vertexCount, edges)
	if err != nil {
		t.Fatalf("BuildFromEdges(%d, %d edges) error = %v", vertexCount, len(edges), err)
	}
	return g
}
