package graph_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/s3arthak/arcdb/internal/graph"
)

// canonicalEdges is the diamond graph from the CSR design note:
//
//	0 -> 1, 0 -> 2, 0 -> 3, 1 -> 3, 2 -> 3
var canonicalEdges = []graph.Edge{
	{From: 0, To: 1},
	{From: 0, To: 2},
	{From: 0, To: 3},
	{From: 1, To: 3},
	{From: 2, To: 3},
}

func TestBuildFromEdges(t *testing.T) {
	tests := []struct {
		name        string
		vertexCount int64
		edges       []graph.Edge
		wantOffsets []uint64
		wantEdges   []uint32
	}{
		{
			name:        "empty graph",
			vertexCount: 0,
			edges:       nil,
			wantOffsets: []uint64{0},
			wantEdges:   nil,
		},
		{
			name:        "vertices without edges",
			vertexCount: 3,
			wantOffsets: []uint64{0, 0, 0, 0},
			wantEdges:   nil,
		},
		{
			name:        "single edge",
			vertexCount: 2,
			edges:       []graph.Edge{{From: 0, To: 1}},
			wantOffsets: []uint64{0, 1, 1},
			wantEdges:   []uint32{1},
		},
		{
			name:        "canonical diamond",
			vertexCount: 4,
			edges:       canonicalEdges,
			wantOffsets: []uint64{0, 3, 4, 5, 5},
			wantEdges:   []uint32{1, 2, 3, 3, 3},
		},
		{
			name:        "self loop",
			vertexCount: 1,
			edges:       []graph.Edge{{From: 0, To: 0}},
			wantOffsets: []uint64{0, 1},
			wantEdges:   []uint32{0},
		},
		{
			name:        "disconnected vertices",
			vertexCount: 5,
			edges:       []graph.Edge{{From: 1, To: 3}, {From: 4, To: 0}},
			wantOffsets: []uint64{0, 0, 1, 1, 1, 2},
			wantEdges:   []uint32{3, 0},
		},
		{
			name:        "duplicate edges collapse",
			vertexCount: 3,
			edges: []graph.Edge{
				{From: 0, To: 1},
				{From: 0, To: 1},
				{From: 0, To: 2},
			},
			wantOffsets: []uint64{0, 2, 2, 2},
			wantEdges:   []uint32{1, 2},
		},
		{
			name:        "duplicate self loops collapse",
			vertexCount: 1,
			edges:       []graph.Edge{{From: 0, To: 0}, {From: 0, To: 0}},
			wantOffsets: []uint64{0, 1},
			wantEdges:   []uint32{0},
		},
		{
			name:        "same target from different sources is kept",
			vertexCount: 3,
			edges:       []graph.Edge{{From: 0, To: 2}, {From: 1, To: 2}},
			wantOffsets: []uint64{0, 1, 2, 2},
			wantEdges:   []uint32{2, 2},
		},
		{
			name:        "duplicate keeps first occurrence order",
			vertexCount: 3,
			edges: []graph.Edge{
				{From: 1, To: 2},
				{From: 1, To: 0},
				{From: 1, To: 2}, // duplicate, ignored
			},
			wantOffsets: []uint64{0, 0, 2, 2},
			wantEdges:   []uint32{2, 0},
		},
		{
			name:        "chain",
			vertexCount: 6,
			edges: []graph.Edge{
				{From: 0, To: 1},
				{From: 1, To: 2},
				{From: 2, To: 3},
				{From: 3, To: 4},
				{From: 4, To: 5},
			},
			wantOffsets: []uint64{0, 1, 2, 3, 4, 5, 5},
			wantEdges:   []uint32{1, 2, 3, 4, 5},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g, err := graph.BuildFromEdges(tc.vertexCount, tc.edges)
			if err != nil {
				t.Fatalf("BuildFromEdges() error = %v, want nil", err)
			}

			if !slices.Equal(g.Offsets, tc.wantOffsets) {
				t.Errorf("Offsets = %v, want %v", g.Offsets, tc.wantOffsets)
			}
			if !slices.Equal(g.Edges, tc.wantEdges) {
				t.Errorf("Edges = %v, want %v", g.Edges, tc.wantEdges)
			}
			if got, want := g.VertexCount(), int(tc.vertexCount); got != want {
				t.Errorf("VertexCount() = %d, want %d", got, want)
			}
			if got, want := g.EdgeCount(), len(tc.wantEdges); got != want {
				t.Errorf("EdgeCount() = %d, want %d", got, want)
			}
			if err := g.Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestBuildFromEdgesRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name        string
		vertexCount int64
		edges       []graph.Edge
		wantErr     error
	}{
		{
			name:        "negative vertex count",
			vertexCount: -1,
			wantErr:     graph.ErrInvalidVertexCount,
		},
		{
			name:        "vertex count above uint32 range",
			vertexCount: int64(1)<<32 + 1,
			wantErr:     graph.ErrInvalidVertexCount,
		},
		{
			name:        "from equals vertex count",
			vertexCount: 2,
			edges:       []graph.Edge{{From: 2, To: 0}},
			wantErr:     graph.ErrVertexOutOfRange,
		},
		{
			name:        "to equals vertex count",
			vertexCount: 2,
			edges:       []graph.Edge{{From: 0, To: 2}},
			wantErr:     graph.ErrVertexOutOfRange,
		},
		{
			name:        "negative from",
			vertexCount: 2,
			edges:       []graph.Edge{{From: -1, To: 0}},
			wantErr:     graph.ErrVertexOutOfRange,
		},
		{
			name:        "negative to",
			vertexCount: 2,
			edges:       []graph.Edge{{From: 0, To: -1}},
			wantErr:     graph.ErrVertexOutOfRange,
		},
		{
			name:        "edge in graph without vertices",
			vertexCount: 0,
			edges:       []graph.Edge{{From: 0, To: 0}},
			wantErr:     graph.ErrVertexOutOfRange,
		},
		{
			name:        "invalid edge after valid edge",
			vertexCount: 2,
			edges:       []graph.Edge{{From: 0, To: 1}, {From: 0, To: 9}},
			wantErr:     graph.ErrVertexOutOfRange,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g, err := graph.BuildFromEdges(tc.vertexCount, tc.edges)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("BuildFromEdges() error = %v, want %v", err, tc.wantErr)
			}
			if g != nil {
				t.Fatalf("BuildFromEdges() graph = %v, want nil on error", g)
			}
		})
	}
}

func TestNeighbors(t *testing.T) {
	g := mustBuild(t, 4, canonicalEdges)

	tests := []struct {
		name   string
		vertex uint32
		want   []uint32
	}{
		{"high degree", 0, []uint32{1, 2, 3}},
		{"single neighbor", 1, []uint32{3}},
		{"single neighbor again", 2, []uint32{3}},
		{"no outgoing edges", 3, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := g.Neighbors(tc.vertex)
			if err != nil {
				t.Fatalf("Neighbors(%d) error = %v, want nil", tc.vertex, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Neighbors(%d) = %v, want %v", tc.vertex, got, tc.want)
			}
		})
	}
}

func TestNeighborsRejectsInvalidVertex(t *testing.T) {
	g := mustBuild(t, 2, []graph.Edge{{From: 0, To: 1}})

	for _, vertex := range []uint32{2, 3, 100} {
		got, err := g.Neighbors(vertex)
		if !errors.Is(err, graph.ErrVertexOutOfRange) {
			t.Errorf("Neighbors(%d) error = %v, want %v", vertex, err, graph.ErrVertexOutOfRange)
		}
		if got != nil {
			t.Errorf("Neighbors(%d) = %v, want nil on error", vertex, got)
		}
	}
}

func TestZeroValueIsEmptyGraph(t *testing.T) {
	var g graph.Graph

	if got := g.VertexCount(); got != 0 {
		t.Errorf("VertexCount() = %d, want 0", got)
	}
	if got := g.EdgeCount(); got != 0 {
		t.Errorf("EdgeCount() = %d, want 0", got)
	}
	if err := g.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
	if _, err := g.Neighbors(0); !errors.Is(err, graph.ErrVertexOutOfRange) {
		t.Errorf("Neighbors(0) error = %v, want %v", err, graph.ErrVertexOutOfRange)
	}
}

func TestValidate(t *testing.T) {
	valid := mustBuild(t, 3, []graph.Edge{{From: 0, To: 2}, {From: 2, To: 1}})

	tests := []struct {
		name    string
		graph   graph.Graph
		wantErr bool
	}{
		{
			name:  "valid built graph",
			graph: *valid,
		},
		{
			name:  "zero value",
			graph: graph.Graph{},
		},
		{
			name:  "explicit empty graph",
			graph: graph.Graph{Offsets: []uint64{0}},
		},
		{
			name:    "edges without offsets",
			graph:   graph.Graph{Edges: []uint32{0}},
			wantErr: true,
		},
		{
			name:    "offsets do not start at zero",
			graph:   graph.Graph{Offsets: []uint64{1, 1}},
			wantErr: true,
		},
		{
			name:    "offsets decrease",
			graph:   graph.Graph{Offsets: []uint64{0, 2, 1}, Edges: []uint32{0, 0}},
			wantErr: true,
		},
		{
			name:    "final offset does not match edge count",
			graph:   graph.Graph{Offsets: []uint64{0, 1, 2}, Edges: []uint32{0}},
			wantErr: true,
		},
		{
			name:    "edge target outside vertex range",
			graph:   graph.Graph{Offsets: []uint64{0, 1}, Edges: []uint32{1}},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.graph.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("Validate() = nil, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestBuildIsDeterministicAndDoesNotMutateInput(t *testing.T) {
	edges := []graph.Edge{
		{From: 3, To: 1},
		{From: 0, To: 4},
		{From: 2, To: 2},
		{From: 3, To: 1}, // duplicate
		{From: 0, To: 4}, // duplicate
		{From: 1, To: 5},
	}
	original := slices.Clone(edges)

	first := mustBuild(t, 6, edges)
	if err := first.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}

	for i := 0; i < 100; i++ {
		next := mustBuild(t, 6, edges)
		if !slices.Equal(first.Offsets, next.Offsets) {
			t.Fatalf("build %d: Offsets = %v, want %v", i, next.Offsets, first.Offsets)
		}
		if !slices.Equal(first.Edges, next.Edges) {
			t.Fatalf("build %d: Edges = %v, want %v", i, next.Edges, first.Edges)
		}
	}

	if !slices.Equal(edges, original) {
		t.Fatalf("BuildFromEdges mutated its input: %v, want %v", edges, original)
	}
}

// mustBuild builds a graph and fails the test (or benchmark) on error.
func mustBuild(tb testing.TB, vertexCount int64, edges []graph.Edge) *graph.Graph {
	tb.Helper()

	g, err := graph.BuildFromEdges(vertexCount, edges)
	if err != nil {
		tb.Fatalf("BuildFromEdges(%d, %d edges) error = %v", vertexCount, len(edges), err)
	}
	return g
}

// benchmarkEdges builds 16 distinct edges per vertex across 4096 vertices,
// enough to exercise counting, prefix sums, and scattering.
func benchmarkEdges() (int64, []graph.Edge) {
	const vertexCount = 4096
	const edgesPerVertex = 16

	edges := make([]graph.Edge, 0, vertexCount*edgesPerVertex)
	for from := 0; from < vertexCount; from++ {
		for k := 0; k < edgesPerVertex; k++ {
			edges = append(edges, graph.Edge{
				From: int64(from),
				To:   int64((from + k*257) % vertexCount),
			})
		}
	}
	return vertexCount, edges
}

func BenchmarkBuildFromEdges(b *testing.B) {
	vertexCount, edges := benchmarkEdges()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := graph.BuildFromEdges(vertexCount, edges); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNeighbors(b *testing.B) {
	vertexCount, edges := benchmarkEdges()
	g := mustBuild(b, vertexCount, edges)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.Neighbors(uint32(i) % uint32(vertexCount)); err != nil {
			b.Fatal(err)
		}
	}
}
