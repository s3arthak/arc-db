package graph

import (
	"errors"
	"fmt"
)

// ErrInvalidVertexCount is returned when BuildFromEdges receives a vertex count
// outside [0, maxVertexCount].
var ErrInvalidVertexCount = errors.New("graph: invalid vertex count")

// maxVertexCount is the largest accepted number of vertices. Edge targets are
// stored as uint32, so every vertex ID must fit in 32 bits.
const maxVertexCount = int64(1) << 32

// Edge is one directed edge in an input edge list.
//
// IDs are int64 rather than uint32 so that invalid input (negative IDs, or IDs
// past the declared vertex count) is rejected explicitly instead of silently
// wrapping around.
type Edge struct {
	From int64
	To   int64
}

// BuildFromEdges builds an immutable CSR graph from a directed edge list.
//
// vertexCount declares the number of vertices, with IDs 0 through
// vertexCount-1. Every edge endpoint must be one of those IDs; otherwise the
// build fails and no graph is returned.
//
// The builder models a simple directed graph, not a multigraph: identical
// (From, To) pairs are collapsed, keeping the first occurrence. Self-loops are
// kept. Neighbor lists follow the order in which their edges first appear in
// edges, so identical input always produces identical Offsets and Edges.
//
// Building takes O(V + E) expected time and O(V + E) space: one validation
// pass that also deduplicates with a hash set, then a degree-counting pass, a
// prefix-sum pass, and a scatter pass.
func BuildFromEdges(vertexCount int64, edges []Edge) (*Graph, error) {
	if vertexCount < 0 || vertexCount > maxVertexCount {
		return nil, fmt.Errorf(
			"%w: %d (want 0 <= count <= %d)",
			ErrInvalidVertexCount, vertexCount, maxVertexCount,
		)
	}

	unique, err := validateAndDeduplicate(vertexCount, edges)
	if err != nil {
		return nil, err
	}

	return buildCSR(vertexCount, unique), nil
}

// validateAndDeduplicate checks every edge against vertexCount and returns the
// unique edges in input order.
func validateAndDeduplicate(vertexCount int64, edges []Edge) ([]Edge, error) {
	unique := make([]Edge, 0, len(edges))
	seen := make(map[uint64]struct{}, len(edges))

	for i, e := range edges {
		if e.From < 0 || e.From >= vertexCount {
			return nil, fmt.Errorf(
				"%w: edge %d has from=%d (vertex count %d)",
				ErrVertexOutOfRange, i, e.From, vertexCount,
			)
		}
		if e.To < 0 || e.To >= vertexCount {
			return nil, fmt.Errorf(
				"%w: edge %d has to=%d (vertex count %d)",
				ErrVertexOutOfRange, i, e.To, vertexCount,
			)
		}

		// Both IDs are valid vertices and therefore fit in 32 bits, so the
		// pair packs losslessly into one uint64.
		key := uint64(e.From)<<32 | uint64(e.To)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, e)
	}

	return unique, nil
}

// buildCSR converts a validated, duplicate-free edge list into CSR. The classic
// three-step construction keeps it O(V + E):
//
//  1. count the out-degree of every vertex,
//  2. prefix-sum the counts into start offsets,
//  3. scatter each target into its source's region.
func buildCSR(vertexCount int64, edges []Edge) *Graph {
	// Step 1: count out-degrees. Writing to offsets[From+1] lets step 2 run
	// the prefix sum in place, with offsets[0] staying 0.
	offsets := make([]uint64, vertexCount+1)
	for _, e := range edges {
		offsets[e.From+1]++
	}

	// Step 2: prefix sum. After this, offsets[v] is where vertex v's
	// neighbors begin and offsets[v+1] is where they end.
	for v := int64(0); v < vertexCount; v++ {
		offsets[v+1] += offsets[v]
	}

	// Step 3: scatter. next[v] is the first free slot in v's region, so the
	// edges of each vertex land in input order.
	placed := make([]uint32, len(edges))
	next := make([]uint64, vertexCount)
	copy(next, offsets[:vertexCount])
	for _, e := range edges {
		placed[next[e.From]] = uint32(e.To)
		next[e.From]++
	}

	return &Graph{Offsets: offsets, Edges: placed}
}
