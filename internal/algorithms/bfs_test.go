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

func TestShortestPath(t *testing.T) {
	tests := []struct {
		name        string
		vertexCount int64
		edges       []graph.Edge
		source      uint32
		target      uint32
		wantFound   bool
		wantEdges   int      // length of the expected path in edges
		wantExact   []uint32 // nil means "any shortest path is acceptable"
	}{
		{
			name:        "direct edge",
			vertexCount: 2,
			edges:       []graph.Edge{{From: 0, To: 1}},
			source:      0,
			target:      1,
			wantFound:   true,
			wantEdges:   1,
			wantExact:   []uint32{0, 1},
		},
		{
			name:        "source equals target",
			vertexCount: 3,
			source:      2,
			target:      2,
			wantFound:   true,
			wantEdges:   0,
			wantExact:   []uint32{2},
		},
		{
			name:        "source equals target with self loop",
			vertexCount: 2,
			edges:       []graph.Edge{{From: 0, To: 0}},
			source:      0,
			target:      0,
			wantFound:   true,
			wantEdges:   0,
			wantExact:   []uint32{0},
		},
		{
			name:        "diamond has two equally short paths",
			vertexCount: 4,
			edges: []graph.Edge{
				{From: 0, To: 1},
				{From: 0, To: 2},
				{From: 1, To: 3},
				{From: 2, To: 3},
			},
			source:    0,
			target:    3,
			wantFound: true,
			wantEdges: 2,
		},
		{
			name:        "shorter path discovered later wins",
			vertexCount: 4,
			edges: []graph.Edge{
				{From: 0, To: 1},
				{From: 1, To: 2},
				{From: 2, To: 3},
				{From: 0, To: 3},
			},
			source:    0,
			target:    3,
			wantFound: true,
			wantEdges: 1,
			wantExact: []uint32{0, 3},
		},
		{
			name:        "unreachable target",
			vertexCount: 4,
			edges: []graph.Edge{
				{From: 0, To: 1},
				{From: 2, To: 3},
			},
			source:    0,
			target:    3,
			wantFound: false,
			wantEdges: -1,
		},
		{
			name:        "isolated target",
			vertexCount: 3,
			edges:       []graph.Edge{{From: 0, To: 1}},
			source:      0,
			target:      2,
			wantFound:   false,
			wantEdges:   -1,
		},
		{
			name:        "path through a cycle",
			vertexCount: 3,
			edges: []graph.Edge{
				{From: 0, To: 1},
				{From: 1, To: 2},
				{From: 2, To: 0},
			},
			source:    1,
			target:    0,
			wantFound: true,
			wantEdges: 2,
			wantExact: []uint32{1, 2, 0},
		},
		{
			name:        "longest chain",
			vertexCount: 5,
			edges: []graph.Edge{
				{From: 0, To: 1},
				{From: 1, To: 2},
				{From: 2, To: 3},
				{From: 3, To: 4},
			},
			source:    0,
			target:    4,
			wantFound: true,
			wantEdges: 4,
			wantExact: []uint32{0, 1, 2, 3, 4},
		},
		{
			name:        "self loop does not shorten a path",
			vertexCount: 2,
			edges: []graph.Edge{
				{From: 0, To: 0},
				{From: 0, To: 1},
			},
			source:    0,
			target:    1,
			wantFound: true,
			wantEdges: 1,
			wantExact: []uint32{0, 1},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := mustBuild(t, tc.vertexCount, tc.edges)

			path, found, err := algorithms.ShortestPath(g, tc.source, tc.target)
			if err != nil {
				t.Fatalf("ShortestPath(%d, %d) error = %v, want nil", tc.source, tc.target, err)
			}
			if found != tc.wantFound {
				t.Fatalf("ShortestPath(%d, %d) found = %t, want %t", tc.source, tc.target, found, tc.wantFound)
			}
			if !found {
				if path != nil {
					t.Fatalf("ShortestPath() path = %v, want nil when target is unreachable", path)
				}
				return
			}

			if got, want := len(path)-1, tc.wantEdges; got != want {
				t.Fatalf("path %v has %d edges, want %d", path, got, want)
			}
			assertValidPath(t, g, path, tc.source, tc.target)
			if tc.wantExact != nil && !slices.Equal(path, tc.wantExact) {
				t.Fatalf("path = %v, want %v", path, tc.wantExact)
			}
		})
	}
}

func TestShortestPathRejectsInvalidVertices(t *testing.T) {
	g := mustBuild(t, 2, []graph.Edge{{From: 0, To: 1}})

	tests := []struct {
		name           string
		source, target uint32
	}{
		{name: "invalid source", source: 2, target: 1},
		{name: "invalid target", source: 0, target: 2},
		{name: "both invalid", source: 5, target: 5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, found, err := algorithms.ShortestPath(g, tc.source, tc.target)
			if !errors.Is(err, graph.ErrVertexOutOfRange) {
				t.Fatalf("ShortestPath() error = %v, want %v", err, graph.ErrVertexOutOfRange)
			}
			if found {
				t.Errorf("ShortestPath() found = true, want false on error")
			}
			if path != nil {
				t.Errorf("ShortestPath() path = %v, want nil on error", path)
			}
		})
	}

	var empty graph.Graph
	if _, _, err := algorithms.ShortestPath(&empty, 0, 0); !errors.Is(err, graph.ErrVertexOutOfRange) {
		t.Fatalf("ShortestPath() on empty graph error = %v, want %v", err, graph.ErrVertexOutOfRange)
	}
}

// assertValidPath checks that path is a simple walk from source to target where
// every consecutive pair is a real edge of g.
func assertValidPath(t *testing.T, g *graph.Graph, path []uint32, source, target uint32) {
	t.Helper()

	if len(path) == 0 {
		t.Fatal("path is empty")
	}
	if path[0] != source {
		t.Fatalf("path starts at %d, want %d", path[0], source)
	}
	if last := path[len(path)-1]; last != target {
		t.Fatalf("path ends at %d, want %d", last, target)
	}

	seen := make(map[uint32]bool, len(path))
	for _, vertex := range path {
		if seen[vertex] {
			t.Fatalf("path visits vertex %d twice: %v", vertex, path)
		}
		seen[vertex] = true
	}

	for i := 0; i+1 < len(path); i++ {
		neighbors, err := g.Neighbors(path[i])
		if err != nil {
			t.Fatalf("Neighbors(%d) error = %v", path[i], err)
		}
		if !slices.Contains(neighbors, path[i+1]) {
			t.Fatalf("path %v has no edge %d -> %d", path, path[i], path[i+1])
		}
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
