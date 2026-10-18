// Package algorithms implements graph traversals over the immutable CSR graph
// kernel in internal/graph.
//
// Everything here is read-only: a traversal allocates its own bookkeeping and
// never modifies the graph it walks.
package algorithms

import (
	"fmt"

	"github.com/s3arthak/arcdb/internal/graph"
)

// BFS returns the vertices reachable from source in breadth-first visit order,
// with source first.
//
// BFS explores level by level: every vertex at distance d is reported before
// any vertex at distance d+1. Each reachable vertex is reported exactly once,
// so cycles and self-loops terminate.
//
// The returned slice is newly allocated and owned by the caller. The error
// wraps graph.ErrVertexOutOfRange when source is not a vertex of g.
//
// Time is O(V + E) over the reachable part of the graph; memory is O(V) for the
// visited set, queue, and result.
func BFS(g *graph.Graph, source uint32) ([]uint32, error) {
	if err := validateVertex(g, source, "source"); err != nil {
		return nil, err
	}
	return traverse(g, source)
}

// traverse runs the BFS loop and returns the visited vertices in visit order.
// source must be a valid vertex of g.
func traverse(g *graph.Graph, source uint32) ([]uint32, error) {
	vertexCount := g.VertexCount()

	// All three slices are sized for the worst case once, so the traversal
	// never reallocates while walking.
	visited := make([]bool, vertexCount)
	queue := make([]uint32, 0, vertexCount)
	order := make([]uint32, 0, vertexCount)

	visited[source] = true
	queue = append(queue, source)
	order = append(order, source)

	// head turns the slice into a FIFO queue without moving elements; the
	// queue grows at the tail and is forgotten at the front.
	for head := 0; head < len(queue); head++ {
		vertex := queue[head]

		neighbors, err := g.Neighbors(vertex)
		if err != nil {
			// Unreachable for a valid graph: every queued vertex is in
			// range and every neighbor points inside the graph.
			return nil, fmt.Errorf("algorithms: reading neighbors of %d: %w", vertex, err)
		}

		for _, next := range neighbors {
			if visited[next] {
				continue
			}
			visited[next] = true
			order = append(order, next)
			queue = append(queue, next)
		}
	}

	return order, nil
}

// validateVertex returns an error wrapping graph.ErrVertexOutOfRange when v is
// not a vertex of g. role names the argument in the error message.
func validateVertex(g *graph.Graph, v uint32, role string) error {
	if uint64(v) < uint64(g.VertexCount()) {
		return nil
	}
	return fmt.Errorf("algorithms: %s vertex %d: %w", role, v, graph.ErrVertexOutOfRange)
}
