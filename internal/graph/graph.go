// Package graph implements ArcDB's graph kernel: an immutable directed graph
// stored in compressed sparse row (CSR) form.
//
// A graph is built once from an edge list with BuildFromEdges and only read
// afterwards. Vertex IDs are dense integers in [0, VertexCount). The physical
// representation lives in csr.go, construction in builder.go, and this file
// holds the read-side API.
package graph

import (
	"errors"
	"fmt"
)

// ErrVertexOutOfRange is returned when a vertex ID does not exist in the graph,
// whether in an input edge list (BuildFromEdges) or in a lookup (Neighbors).
var ErrVertexOutOfRange = errors.New("graph: vertex out of range")

// VertexCount returns the number of vertices in g.
//
// Ids are dense and 0-based, so the valid IDs are [0, VertexCount).
func (g *Graph) VertexCount() int {
	if len(g.Offsets) == 0 {
		// The zero value of Graph is an empty graph.
		return 0
	}
	return len(g.Offsets) - 1
}

// EdgeCount returns the number of stored directed edges.
//
// BuildFromEdges collapses duplicate edges, so this is the number of unique
// edges, which may be smaller than the number of input rows.
func (g *Graph) EdgeCount() int {
	return len(g.Edges)
}

// Neighbors returns the out-neighbors of v.
//
// The result is a read-only view into the graph's edge array, not a copy. It
// stays valid for as long as g is reachable and must not be modified. A vertex
// with no outgoing edges yields an empty slice, which may be nil.
//
// Looking up the range is O(1); reading all neighbors is O(degree(v)).
func (g *Graph) Neighbors(v uint32) ([]uint32, error) {
	if uint64(v) >= uint64(g.VertexCount()) {
		return nil, fmt.Errorf("%w: %d (vertex count %d)", ErrVertexOutOfRange, v, g.VertexCount())
	}
	start, end := g.row(uint64(v))
	return g.Edges[start:end], nil
}
