package graph

import "fmt"

// Graph is an immutable directed graph in compressed sparse row (CSR) form.
//
// The representation is two flat arrays. For a graph with the edges
//
//	0 -> 1, 0 -> 2, 0 -> 3, 1 -> 3, 2 -> 3
//
// the arrays are
//
//	Offsets: [0, 3, 4, 5, 5]
//	Edges:   [1, 2, 3, 3, 3]
//
// and vertex v owns the half-open range Edges[Offsets[v]:Offsets[v+1]]:
//
//	vertex 0 -> Edges[0:3] = [1 2 3]
//	vertex 1 -> Edges[3:4] = [3]
//	vertex 2 -> Edges[4:5] = [3]
//	vertex 3 -> Edges[5:5] = []      (no outgoing edges)
//
// The fields are exported so that a future storage layer can serialize the
// arrays directly, without a conversion step. Treat a Graph as read-only once
// BuildFromEdges returns it: mutating either slice breaks the invariants that
// Validate checks.
type Graph struct {
	// Offsets holds one entry per vertex plus a terminating entry, so
	// len(Offsets) == VertexCount()+1. Vertex v's neighbors are
	// Edges[Offsets[v]:Offsets[v+1]].
	Offsets []uint64

	// Edges concatenates every vertex's neighbor list in vertex order.
	Edges []uint32
}

// row returns the half-open range of Edges owned by v.
//
// The caller must have checked that v is in range; row does no bounds checking
// of its own beyond what the slice indexing already does.
func (g *Graph) row(v uint64) (start, end uint64) {
	return g.Offsets[v], g.Offsets[v+1]
}

// Validate reports whether g satisfies the CSR invariants:
//
//   - the offsets are non-decreasing and start at 0,
//   - the final offset equals len(Edges), and
//   - every edge target is a vertex in [0, VertexCount).
//
// The zero value is a valid empty graph. BuildFromEdges always returns a valid
// graph; Validate exists for tests, for hand-built graphs, and for data loaded
// from disk once persistence lands.
func (g *Graph) Validate() error {
	// The zero value has no offsets at all; treat it as an empty graph.
	if len(g.Offsets) == 0 {
		if len(g.Edges) != 0 {
			return fmt.Errorf("graph: invalid CSR: %d edges but no offsets", len(g.Edges))
		}
		return nil
	}

	if g.Offsets[0] != 0 {
		return fmt.Errorf("graph: invalid CSR: offsets[0] = %d, want 0", g.Offsets[0])
	}

	vertexCount := len(g.Offsets) - 1
	for v := 0; v < vertexCount; v++ {
		if g.Offsets[v] > g.Offsets[v+1] {
			return fmt.Errorf(
				"graph: invalid CSR: offsets decrease at vertex %d (%d > %d)",
				v, g.Offsets[v], g.Offsets[v+1],
			)
		}
	}

	if last, edges := g.Offsets[vertexCount], uint64(len(g.Edges)); last != edges {
		return fmt.Errorf(
			"graph: invalid CSR: offsets[%d] = %d, want %d (edge count)",
			vertexCount, last, edges,
		)
	}

	for i, to := range g.Edges {
		if uint64(to) >= uint64(vertexCount) {
			return fmt.Errorf(
				"graph: invalid CSR: edge %d targets vertex %d, vertex count %d",
				i, to, vertexCount,
			)
		}
	}

	return nil
}
