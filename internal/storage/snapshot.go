// Package storage persists ArcDB's in-memory structures.
//
// Phase 7 adds explicit compaction: an immutable CSR snapshot plus a mutable
// delta are folded into a new snapshot carrying a WAL checkpoint, and the log
// is reset afterwards.
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
//	8       4           format version (uint32), currently 2
//	12      8           vertex count (uint64)
//	20      8           edge count (uint64)
//	28      8           checkpoint sequence (uint64)
//	36      8*(V+1)     CSR offsets (uint64 each)
//	36+8*(V+1) 4*E      CSR edges (uint32 each)
//
// Version 2 added the checkpoint field. A checkpoint of C means every WAL record
// with sequence <= C is already represented in this snapshot, so recovery may
// skip those records. Version 1 snapshots used the same header without the
// checkpoint and are rejected rather than guessed at.
const (
	// magic identifies an ArcDB snapshot file.
	magic     = "ARCDBSNP"
	magicSize = len(magic)

	// formatVersion is the only snapshot layout this build reads or writes.
	formatVersion uint32 = 2

	versionOffset     = magicSize             // 8
	vertexCountOffset = versionOffset + 4     // 12
	edgeCountOffset   = vertexCountOffset + 8 // 20
	checkpointOffset  = edgeCountOffset + 8   // 28
	headerSize        = checkpointOffset + 8  // 36

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

// Save writes g to path as a snapshot with the given WAL checkpoint, replacing
// any existing file.
//
// The graph is validated before the file is created, so a broken CSR can never
// be persisted. The data is flushed and synced before Close, so a successful
// Save has handed the whole file to the OS with a flush request. The write is
// not atomic; callers that need an all-or-nothing replacement (such as
// compaction) write a temporary file and rename it.
func Save(path string, g *graph.Graph, checkpoint uint64) error {
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
	writeErr := writeSnapshot(writer, g, checkpoint)
	if writeErr == nil {
		writeErr = writer.Flush()
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if closeErr := file.Close(); writeErr == nil && closeErr != nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return fmt.Errorf("storage: saving snapshot %q: %w", path, writeErr)
	}
	return nil
}

// Load reads a snapshot and returns the reconstructed graph together with its
// checkpoint sequence.
//
// The file is treated as untrusted input: header fields, file size, counts, and
// every CSR invariant are checked before a graph is returned. Malformed input
// errors wrap ErrInvalidSnapshot (or ErrUnsupportedVersion); Load never panics.
func Load(path string) (*graph.Graph, uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("storage: loading snapshot %q: %w", path, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, 0, fmt.Errorf("storage: loading snapshot %q: %w", path, err)
	}

	g, checkpoint, err := readSnapshot(file, info.Size())
	if err != nil {
		return nil, 0, fmt.Errorf("storage: loading snapshot %q: %w", path, err)
	}
	return g, checkpoint, nil
}

// writeSnapshot encodes g and its checkpoint in the snapshot format.
func writeSnapshot(w io.Writer, g *graph.Graph, checkpoint uint64) error {
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
	binary.LittleEndian.PutUint64(header[edgeCountOffset:checkpointOffset], uint64(edgeCount))
	binary.LittleEndian.PutUint64(header[checkpointOffset:headerSize], checkpoint)
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
func readSnapshot(r io.Reader, size int64) (*graph.Graph, uint64, error) {
	if size < int64(headerSize) {
		return nil, 0, fmt.Errorf("%w: file is %d bytes, header needs %d", ErrInvalidSnapshot, size, headerSize)
	}

	var header [headerSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, 0, fmt.Errorf("%w: reading header: %w", ErrInvalidSnapshot, err)
	}

	if got := string(header[:magicSize]); got != magic {
		return nil, 0, fmt.Errorf("%w: magic %q, want %q", ErrInvalidSnapshot, got, magic)
	}

	version := binary.LittleEndian.Uint32(header[versionOffset:vertexCountOffset])
	if version != formatVersion {
		return nil, 0, fmt.Errorf("%w: version %d, supported %d", ErrUnsupportedVersion, version, formatVersion)
	}

	vertexCount := binary.LittleEndian.Uint64(header[vertexCountOffset:edgeCountOffset])
	edgeCount := binary.LittleEndian.Uint64(header[edgeCountOffset:checkpointOffset])
	checkpoint := binary.LittleEndian.Uint64(header[checkpointOffset:headerSize])
	if vertexCount > maxVertexCount {
		return nil, 0, fmt.Errorf(
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
		return nil, 0, fmt.Errorf(
			"%w: truncated offsets: need %d bytes for %d vertices, have %d",
			ErrInvalidSnapshot, offsetSize, vertexCount, remaining,
		)
	}

	edgeSize := remaining - offsetSize
	if edgeSize%edgeByteSize != 0 {
		return nil, 0, fmt.Errorf(
			"%w: edge data is %d bytes, not a multiple of %d",
			ErrInvalidSnapshot, edgeSize, edgeByteSize,
		)
	}
	edgesInFile := edgeSize / edgeByteSize
	if edgesInFile != edgeCount {
		return nil, 0, fmt.Errorf(
			"%w: header edge count %d, file holds %d",
			ErrInvalidSnapshot, edgeCount, edgesInFile,
		)
	}

	if offsetCount > uint64(maxInt) || edgeCount > uint64(maxInt) {
		return nil, 0, fmt.Errorf("%w: snapshot does not fit this platform's int", ErrInvalidSnapshot)
	}

	offsets := make([]uint64, int(offsetCount))
	edges := make([]uint32, int(edgeCount))

	var scratch [offsetBytes]byte
	for i := range offsets {
		if _, err := io.ReadFull(r, scratch[:]); err != nil {
			return nil, 0, fmt.Errorf("%w: reading offsets: %w", ErrInvalidSnapshot, err)
		}
		offsets[i] = binary.LittleEndian.Uint64(scratch[:])
	}
	for i := range edges {
		if _, err := io.ReadFull(r, scratch[:edgeByteSize]); err != nil {
			return nil, 0, fmt.Errorf("%w: reading edges: %w", ErrInvalidSnapshot, err)
		}
		edges[i] = binary.LittleEndian.Uint32(scratch[:edgeByteSize])
	}

	g := &graph.Graph{Offsets: offsets, Edges: edges}
	if err := g.Validate(); err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrInvalidSnapshot, err)
	}
	return g, checkpoint, nil
}
