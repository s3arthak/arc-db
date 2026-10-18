// Package algorithms implements graph traversals over the immutable CSR graph
// kernel in internal/graph.
//
// Everything here is read-only: a traversal allocates its own bookkeeping and
// never modifies the graph it walks.
package algorithms

import (
	"fmt"
	"slices"

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
// visited set, parent links, queue, and result.
func BFS(g *graph.Graph, source uint32) ([]uint32, error) {
	if err := validateVertex(g, source, "source"); err != nil {
		return nil, err
	}

	result, err := traverse(g, source)
	if err != nil {
		return nil, err
	}
	return result.order, nil
}

// ShortestPath returns one shortest path from source to target as vertex IDs,
// inclusive of both ends.
//
// The bool reports whether target is reachable from source. When it is false,
// the path is nil and there is no error. Because the graph is unweighted, the
// BFS parent chain is always a shortest path: the first time BFS reaches a
// vertex it has used the fewest possible edges.
//
// When several shortest paths exist, the one returned follows first-discovery
// order, which is determined by the edge order the graph was built with. There
// is no separate tie-breaking rule.
//
// Errors wrap graph.ErrVertexOutOfRange when source or target is not a vertex
// of g. Time is O(V + E); memory is O(V).
func ShortestPath(g *graph.Graph, source, target uint32) (path []uint32, found bool, err error) {
	if err := validateVertex(g, source, "source"); err != nil {
		return nil, false, err
	}
	if err := validateVertex(g, target, "target"); err != nil {
		return nil, false, err
	}

	result, err := traverse(g, source)
	if err != nil {
		return nil, false, err
	}
	if !result.visited[target] {
		return nil, false, nil
	}

	return result.pathTo(source, target), true, nil
}

// traversal is what one BFS pass learned about the graph. Every slice is
// indexed by vertex ID.
type traversal struct {
	order   []uint32 // visited vertices in visit order, source first
	visited []bool   // visited[v] reports whether v is reachable from source
	parent  []uint32 // parent[v] is the vertex v was first discovered from
}

// pathTo follows the parent chain back from target and reverses it into
// source-to-target order. target must have been visited by this traversal, and
// source must be the traversal's source.
func (t traversal) pathTo(source, target uint32) []uint32 {
	var path []uint32
	for vertex := target; ; vertex = t.parent[vertex] {
		path = append(path, vertex)
		if vertex == source {
			break
		}
	}

	slices.Reverse(path)
	return path
}

// traverse runs the BFS loop and returns what it recorded. source must be a
// valid vertex of g.
func traverse(g *graph.Graph, source uint32) (traversal, error) {
	vertexCount := g.VertexCount()

	// All four slices are sized for the worst case once, so the traversal
	// never reallocates while walking.
	visited := make([]bool, vertexCount)
	parent := make([]uint32, vertexCount)
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
			return traversal{}, fmt.Errorf("algorithms: reading neighbors of %d: %w", vertex, err)
		}

		for _, next := range neighbors {
			if visited[next] {
				continue
			}
			visited[next] = true
			parent[next] = vertex
			order = append(order, next)
			queue = append(queue, next)
		}
	}

	return traversal{order: order, visited: visited, parent: parent}, nil
}

// validateVertex returns an error wrapping graph.ErrVertexOutOfRange when v is
// not a vertex of g. role names the argument in the error message.
func validateVertex(g *graph.Graph, v uint32, role string) error {
	if uint64(v) < uint64(g.VertexCount()) {
		return nil
	}
	return fmt.Errorf("algorithms: %s vertex %d: %w", role, v, graph.ErrVertexOutOfRange)
}
