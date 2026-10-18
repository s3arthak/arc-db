// Package storage persists ArcDB's in-memory structures.
//
// Phase 4 adds one artifact: a binary snapshot of an immutable CSR graph. There
// is no write-ahead log, no mutation, and no crash recovery yet; a snapshot is
// written once and read back whole.
package storage

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/s3arthak/arcdb/internal/graph"
)

// Snapshot layout, all integer fields little-endian:
//
//	offset  size        field
//	0       8           magic, the ASCII bytes "ARCDBSNP"
//	8       4           format version (uint32), currently 1
//	12      8           vertex count (uint64)
//	20      8           edge count (uint64)
//	28      8*(V+1)     CSR offsets (uint64 each)
//	28+8*(V+1) 4*E      CSR edges (uint32 each)
//
// The arrays are stored exactly as the graph holds them, so Load can rebuild the
// Graph without going through BuildFromEdges.
const (
	// magic identifies an ArcDB snapshot file.
	magic     = "ARCDBSNP"
	magicSize = len(magic)

	// formatVersion is the only snapshot layout this build reads or writes.
	formatVersion uint32 = 1

	versionOffset     = magicSize             // 8
	vertexCountOffset = versionOffset + 4     // 12
	edgeCountOffset   = vertexCountOffset + 8 // 20
	headerSize        = edgeCountOffset + 8   // 28

	offsetBytes  = 8 // one uint64 offset
	edgeByteSize = 4 // one uint32 edge target
)

// maxVertexCount mirrors the graph kernel's limit: edge targets are uint32, so
// at most 2^32 vertices can exist. Enforcing it here stops a header from
// driving a huge allocation before the file size has been checked.
const maxVertexCount = uint64(1) << 32

// maxInt is the largest value an int can hold on this platform.
const maxInt = int(^uint(0) >> 1)

var (
	// ErrInvalidSnapshot reports a file that is not a readable ArcDB snapshot:
	// bad magic, truncated data, inconsistent counts, or a CSR that fails
	// validation.
	ErrInvalidSnapshot = errors.New("storage: invalid snapshot")

	// ErrUnsupportedVersion reports a well-formed header whose version this
	// build does not understand.
	ErrUnsupportedVersion = errors.New("storage: unsupported snapshot version")
)

// Save writes g to path as a binary snapshot, replacing any existing file.
//
// The graph is validated before the file is created, so a broken CSR can never
// be persisted. The write is not atomic: if the process stops mid-save, path may
// contain a partial file. The layout is documented in docs/storage/snapshot.md.
func Save(path string, g *graph.Graph) error {
	if g == nil {
		return errors.New("storage: save: graph is nil")
	}
	if err := g.Validate(); err != nil {
		return fmt.Errorf("storage: save: invalid graph: %w", err)
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("storage: saving snapshot %q: %w", path, err)
	}

	writer := bufio.NewWriter(file)
	writeErr := writeSnapshot(writer, g)
	if writeErr == nil {
		writeErr = writer.Flush()
	}
	if closeErr := file.Close(); writeErr == nil && closeErr != nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return fmt.Errorf("storage: saving snapshot %q: %w", path, writeErr)
	}
	return nil
}

// Load reads a snapshot written by Save and returns the reconstructed graph.
//
// The file is treated as untrusted input: header fields, file size, counts, and
// every CSR invariant are checked before a graph is returned. Malformed input
// errors wrap ErrInvalidSnapshot (or ErrUnsupportedVersion); Load never panics.
func Load(path string) (*graph.Graph, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("storage: loading snapshot %q: %w", path, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("storage: loading snapshot %q: %w", path, err)
	}

	g, err := readSnapshot(file, info.Size())
	if err != nil {
		return nil, fmt.Errorf("storage: loading snapshot %q: %w", path, err)
	}
	return g, nil
}

// writeSnapshot encodes g in the snapshot format.
func writeSnapshot(w io.Writer, g *graph.Graph) error {
	vertexCount := g.VertexCount()
	edgeCount := g.EdgeCount()

	offsets := g.Offsets
	if len(offsets) == 0 {
		// The zero value of graph.Graph is a valid empty graph but has no
		// offsets allocated. Persist the canonical single zero offset.
		offsets = []uint64{0}
	}

	var header [headerSize]byte
	copy(header[:magicSize], magic)
	binary.LittleEndian.PutUint32(header[versionOffset:vertexCountOffset], formatVersion)
	binary.LittleEndian.PutUint64(header[vertexCountOffset:edgeCountOffset], uint64(vertexCount))
	binary.LittleEndian.PutUint64(header[edgeCountOffset:headerSize], uint64(edgeCount))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}

	// One small scratch buffer keeps encoding explicit without building a full
	// byte copy of the graph in memory.
	var scratch [offsetBytes]byte
	for _, offset := range offsets {
		binary.LittleEndian.PutUint64(scratch[:], offset)
		if _, err := w.Write(scratch[:]); err != nil {
			return err
		}
	}
	for _, edge := range g.Edges {
		binary.LittleEndian.PutUint32(scratch[:edgeByteSize], edge)
		if _, err := w.Write(scratch[:edgeByteSize]); err != nil {
			return err
		}
	}
	return nil
}

// readSnapshot decodes a snapshot of exactly size bytes from r.
func readSnapshot(r io.Reader, size int64) (*graph.Graph, error) {
	if size < int64(headerSize) {
		return nil, fmt.Errorf("%w: file is %d bytes, header needs %d", ErrInvalidSnapshot, size, headerSize)
	}

	var header [headerSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, fmt.Errorf("%w: reading header: %w", ErrInvalidSnapshot, err)
	}

	if got := string(header[:magicSize]); got != magic {
		return nil, fmt.Errorf("%w: magic %q, want %q", ErrInvalidSnapshot, got, magic)
	}

	version := binary.LittleEndian.Uint32(header[versionOffset:vertexCountOffset])
	if version != formatVersion {
		return nil, fmt.Errorf("%w: version %d, supported %d", ErrUnsupportedVersion, version, formatVersion)
	}

	vertexCount := binary.LittleEndian.Uint64(header[vertexCountOffset:edgeCountOffset])
	edgeCount := binary.LittleEndian.Uint64(header[edgeCountOffset:headerSize])
	if vertexCount > maxVertexCount {
		return nil, fmt.Errorf(
			"%w: vertex count %d exceeds limit %d",
			ErrInvalidSnapshot, vertexCount, maxVertexCount,
		)
	}

	// Derive the array sizes from the header, then check them against the real
	// file size before allocating anything.
	offsetCount := vertexCount + 1
	offsetSize := offsetCount * offsetBytes
	remaining := uint64(size) - uint64(headerSize)
	if remaining < offsetSize {
		return nil, fmt.Errorf(
			"%w: truncated offsets: need %d bytes for %d vertices, have %d",
			ErrInvalidSnapshot, offsetSize, vertexCount, remaining,
		)
	}

	edgeSize := remaining - offsetSize
	if edgeSize%edgeByteSize != 0 {
		return nil, fmt.Errorf(
			"%w: edge data is %d bytes, not a multiple of %d",
			ErrInvalidSnapshot, edgeSize, edgeByteSize,
		)
	}
	edgesInFile := edgeSize / edgeByteSize
	if edgesInFile != edgeCount {
		return nil, fmt.Errorf(
			"%w: header edge count %d, file holds %d",
			ErrInvalidSnapshot, edgeCount, edgesInFile,
		)
	}

	if offsetCount > uint64(maxInt) || edgeCount > uint64(maxInt) {
		return nil, fmt.Errorf("%w: snapshot does not fit this platform's int", ErrInvalidSnapshot)
	}

	offsets := make([]uint64, int(offsetCount))
	edges := make([]uint32, int(edgeCount))

	var scratch [offsetBytes]byte
	for i := range offsets {
		if _, err := io.ReadFull(r, scratch[:]); err != nil {
			return nil, fmt.Errorf("%w: reading offsets: %w", ErrInvalidSnapshot, err)
		}
		offsets[i] = binary.LittleEndian.Uint64(scratch[:])
	}
	for i := range edges {
		if _, err := io.ReadFull(r, scratch[:edgeByteSize]); err != nil {
			return nil, fmt.Errorf("%w: reading edges: %w", ErrInvalidSnapshot, err)
		}
		edges[i] = binary.LittleEndian.Uint32(scratch[:edgeByteSize])
	}

	g := &graph.Graph{Offsets: offsets, Edges: edges}
	if err := g.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSnapshot, err)
	}
	return g, nil
}
