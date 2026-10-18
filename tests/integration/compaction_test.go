package integration

import (
	"slices"
	"testing"

	"github.com/s3arthak/arcdb/internal/algorithms"
	"github.com/s3arthak/arcdb/internal/graph"
)

func TestBFSIsUnchangedByCompaction(t *testing.T) {
	mutable := openMutableGraph(t, 6, []graph.Edge{
		{From: 0, To: 1},
		{From: 1, To: 2},
		{From: 2, To: 3},
	})
	if err := mutable.AddEdge(2, 4); err != nil {
		t.Fatalf("AddEdge(2, 4) error = %v", err)
	}
	if err := mutable.AddEdge(4, 5); err != nil {
		t.Fatalf("AddEdge(4, 5) error = %v", err)
	}

	before, err := algorithms.BFS(mutable, 0)
	if err != nil {
		t.Fatalf("BFS before compaction error = %v", err)
	}
	if want := []uint32{0, 1, 2, 3, 4, 5}; !slices.Equal(before, want) {
		t.Fatalf("BFS before compaction = %v, want %v", before, want)
	}

	if err := mutable.Compact(); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}

	after, err := algorithms.BFS(mutable, 0)
	if err != nil {
		t.Fatalf("BFS after compaction error = %v", err)
	}
	if !slices.Equal(after, before) {
		t.Fatalf("BFS after compaction = %v, want %v", after, before)
	}
}

func TestShortestPathIsUnchangedByCompaction(t *testing.T) {
	mutable := openMutableGraph(t, 6, []graph.Edge{
		{From: 0, To: 1},
		{From: 1, To: 2},
		{From: 2, To: 3},
		{From: 3, To: 4},
	})
	// A delta shortcut that must survive compaction.
	if err := mutable.AddEdge(0, 5); err != nil {
		t.Fatalf("AddEdge(0, 5) error = %v", err)
	}
	if err := mutable.AddEdge(4, 5); err != nil {
		t.Fatalf("AddEdge(4, 5) error = %v", err)
	}

	beforePath, beforeFound, err := algorithms.ShortestPath(mutable, 0, 5)
	if err != nil {
		t.Fatalf("ShortestPath before compaction error = %v", err)
	}
	if !beforeFound || !slices.Equal(beforePath, []uint32{0, 5}) {
		t.Fatalf("ShortestPath before compaction = %v, %t, want [0 5], true", beforePath, beforeFound)
	}

	if err := mutable.Compact(); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}

	afterPath, afterFound, err := algorithms.ShortestPath(mutable, 0, 5)
	if err != nil {
		t.Fatalf("ShortestPath after compaction error = %v", err)
	}
	if afterFound != beforeFound || !slices.Equal(afterPath, beforePath) {
		t.Fatalf("ShortestPath after compaction = %v, %t, want %v, %t", afterPath, afterFound, beforePath, beforeFound)
	}

	// Unreachable vertices stay unreachable.
	if _, found, err := algorithms.ShortestPath(mutable, 5, 0); err != nil || found {
		t.Fatalf("ShortestPath(5, 0) = found %t, err %v, want false, nil", found, err)
	}
}
