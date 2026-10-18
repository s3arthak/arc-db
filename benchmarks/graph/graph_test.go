// Package benchmarks measures the ArcDB graph kernel and its traversals.
//
// The benchmarks intentionally live outside internal/ so production packages do
// not grow benchmark-only code. Nothing here changes the algorithm or the CSR
// representation; it only generates deterministic inputs and times the existing
// public API.
package benchmarks

import (
	"testing"

	"github.com/s3arthak/arcdb/internal/algorithms"
	"github.com/s3arthak/arcdb/internal/graph"
)

// outDegree is the fixed number of outgoing edges generated per vertex. A
// small constant keeps the graph sparse: edge count is outDegree * vertexCount.
const outDegree = 4

// stride spaces a vertex's targets apart so that no two edges from the same
// source share a target. It must stay coprime with every benchmark graph size
// (10k, 100k, 1m are powers of ten, so any stride not divisible by 2 or 5
// works), and outDegree*stride must stay below the smallest vertex count.
const stride = 7

// sizes are the graph sizes every benchmark runs against. They stay within the
// memory budget of an 8 GB development machine: the largest graph has 1M
// vertices and 4M edges.
var sizes = []struct {
	name     string
	vertices int
}{
	{name: "10k", vertices: 10_000},
	{name: "100k", vertices: 100_000},
	{name: "1m", vertices: 1_000_000},
}

// Benchmark sinks. A benchmark result that nobody reads may be treated as dead
// code, which would time an empty loop instead of the operation under test.
// Assigning to a package-level variable keeps the result observable; the value
// is never read, which is exactly the point.
var (
	sinkGraph     *graph.Graph
	sinkNeighbors []uint32
	sinkOrder     []uint32
	sinkPath      []uint32
)

// BenchmarkBuildCSR measures converting an edge list into CSR. Input generation
// happens before the timer starts, so only validation, deduplication, counting,
// prefix summing, and scattering are timed.
func BenchmarkBuildCSR(b *testing.B) {
	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			edges := generateEdges(size.vertices)
			vertexCount := int64(size.vertices)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				g, err := graph.BuildFromEdges(vertexCount, edges)
				if err != nil {
					b.Fatalf("BuildFromEdges(%d, %d edges) error = %v", vertexCount, len(edges), err)
				}
				sinkGraph = g
			}
		})
	}
}

// BenchmarkNeighbors measures the neighbor lookup itself: bounds check plus the
// two offset reads that produce the neighbor slice. Reads are spread across
// every vertex so different CSR regions are touched.
func BenchmarkNeighbors(b *testing.B) {
	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			g := mustBuild(b, size.vertices)
			vertexCount := size.vertices

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				neighbors, err := g.Neighbors(uint32(i % vertexCount))
				if err != nil {
					b.Fatalf("Neighbors(%d) error = %v", i%vertexCount, err)
				}
				sinkNeighbors = neighbors
			}
		})
	}
}

// BenchmarkBFS measures a full traversal from vertex 0. The generated graph
// contains v -> v+1 edges, so every vertex is reachable and BFS visits all V
// vertices and scans all E edges.
func BenchmarkBFS(b *testing.B) {
	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			g := mustBuild(b, size.vertices)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				order, err := algorithms.BFS(g, 0)
				if err != nil {
					b.Fatalf("BFS(0) error = %v", err)
				}
				sinkOrder = order
			}
		})
	}
}

// BenchmarkShortestPath measures traversal plus parent-chain reconstruction from
// vertex 0 to the last vertex. The path is several hops long, so the benchmark
// includes walking and reversing the parent chain, not just finding the target.
func BenchmarkShortestPath(b *testing.B) {
	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			g := mustBuild(b, size.vertices)
			source := uint32(0)
			target := uint32(size.vertices - 1)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				path, found, err := algorithms.ShortestPath(g, source, target)
				if err != nil {
					b.Fatalf("ShortestPath(%d, %d) error = %v", source, target, err)
				}
				if !found {
					b.Fatalf("ShortestPath(%d, %d) found = false, want true", source, target)
				}
				sinkPath = path
			}
		})
	}
}

// generateEdges builds the deterministic input edge list: vertex v points at
// (v + 1 + k*stride) mod vertices for k in [0, outDegree). Each vertex therefore
// has exactly outDegree distinct targets, and the +1 edge alone makes every
// vertex reachable from vertex 0.
//
// Generation is pure arithmetic, so the same vertex count always produces the
// same edge list; no random seed can drift between runs.
func generateEdges(vertices int) []graph.Edge {
	edges := make([]graph.Edge, 0, vertices*outDegree)
	for v := 0; v < vertices; v++ {
		for k := 0; k < outDegree; k++ {
			edges = append(edges, graph.Edge{
				From: int64(v),
				To:   int64((v + 1 + k*stride) % vertices),
			})
		}
	}
	return edges
}

// mustBuild prepares a graph outside any timed section and checks its shape, so
// a generator bug cannot quietly change what a benchmark measures.
func mustBuild(b *testing.B, vertices int) *graph.Graph {
	b.Helper()

	g, err := graph.BuildFromEdges(int64(vertices), generateEdges(vertices))
	if err != nil {
		b.Fatalf("BuildFromEdges(%d) error = %v", vertices, err)
	}
	if got, want := g.EdgeCount(), vertices*outDegree; got != want {
		b.Fatalf("graph has %d edges, want %d (generator produced duplicates?)", got, want)
	}
	return g
}
