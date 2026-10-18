package graph

import "slices"

// Delta is a mutable set of edges layered over an immutable snapshot.
//
// It stores only newly added edges, grouped by source and kept in insertion
// order. A map of per-source slices is deliberately simple for Phase 6:
// additions touch one short slice, duplicate detection scans that same slice,
// and the order of delta neighbors is deterministic. Compaction, indexes,
// allocators, and concurrency belong to later phases.
//
// A Delta does not know the snapshot's vertex count, so it does not validate
// IDs; callers such as storage.MutableGraph validate before adding. A Delta is
// not safe for concurrent use. The zero value is an empty delta.
type Delta struct {
	edges map[uint32][]uint32
}

// NewDelta returns an empty delta.
func NewDelta() *Delta {
	return &Delta{edges: make(map[uint32][]uint32)}
}

// AddEdge records source -> destination.
//
// Edges are a set: adding an edge that is already in the delta does nothing.
// New edges keep insertion order per source.
func (d *Delta) AddEdge(source, destination uint32) {
	if d.edges == nil {
		d.edges = make(map[uint32][]uint32)
	}

	row := d.edges[source]
	if slices.Contains(row, destination) {
		return
	}
	d.edges[source] = append(row, destination)
}

// Neighbors returns the delta's outgoing edges for vertex in insertion order.
//
// The result aliases the delta's storage, must not be modified, and is only
// stable until the next AddEdge call.
func (d *Delta) Neighbors(vertex uint32) []uint32 {
	return d.edges[vertex]
}

// View is the logical graph: an immutable CSR snapshot plus a mutable delta.
//
// Reads merge both sources and never modify the snapshot. The snapshot remains
// the source of truth for vertex IDs and edges that existed before the delta;
// the delta can only add edges.
type View struct {
	snapshot *Graph
	delta    *Delta
}

// NewView combines a snapshot with a delta. snapshot must be non-nil; a nil
// delta is treated as empty.
func NewView(snapshot *Graph, delta *Delta) *View {
	return &View{snapshot: snapshot, delta: delta}
}

// VertexCount returns the number of vertices in the logical graph.
//
// Phase 6 cannot add vertices, so this always equals the snapshot's count.
func (v *View) VertexCount() int {
	return v.snapshot.VertexCount()
}

// Neighbors returns the logical out-neighbors of vertex: the snapshot's
// neighbors followed by delta edges that the snapshot does not already contain.
//
// When the delta has nothing for this vertex, the snapshot's slice is returned
// unchanged. Otherwise the result is a newly allocated, deduplicated slice.
// Either way the caller must treat it as read-only.
//
// The error wraps ErrVertexOutOfRange when vertex is outside [0, VertexCount).
func (v *View) Neighbors(vertex uint32) ([]uint32, error) {
	base, err := v.snapshot.Neighbors(vertex)
	if err != nil {
		return nil, err
	}
	if v.delta == nil {
		return base, nil
	}

	added := v.delta.Neighbors(vertex)
	if len(added) == 0 {
		return base, nil
	}

	merged := make([]uint32, 0, len(base)+len(added))
	merged = append(merged, base...)
	for _, target := range added {
		if slices.Contains(base, target) {
			// The snapshot already has this edge; the logical graph is a set.
			continue
		}
		merged = append(merged, target)
	}
	return merged, nil
}
