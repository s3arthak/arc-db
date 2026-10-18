package storage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/s3arthak/arcdb/internal/graph"
)

func TestWALReplayEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.wal")
	wal := openTestWAL(t, path)

	records, err := wal.Replay()
	if err != nil {
		t.Fatalf("Replay() error = %v, want nil", err)
	}
	if len(records) != 0 {
		t.Fatalf("Replay() = %v, want no records", records)
	}

	if err := wal.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got, want := info.Size(), int64(walHeaderSize); got != want {
		t.Errorf("empty WAL size = %d, want %d", got, want)
	}
}

func TestWALAppendAndReplay(t *testing.T) {
	tests := []struct {
		name  string
		edges []AddEdge
	}{
		{
			name:  "single record",
			edges: []AddEdge{{Sequence: 1, Source: 0, Destination: 1}},
		},
		{
			name: "multiple records keep order",
			edges: []AddEdge{
				{Sequence: 4, Source: 0, Destination: 1},
				{Sequence: 7, Source: 1, Destination: 2},
				{Sequence: 8, Source: 2, Destination: 0},
				{Sequence: 9, Source: 3, Destination: 3},
			},
		},
		{
			name: "duplicate records are preserved",
			edges: []AddEdge{
				{Sequence: 2, Source: 0, Destination: 1},
				{Sequence: 3, Source: 0, Destination: 1},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "graph.wal")
			wal := openTestWAL(t, path)

			appendTestEdges(t, wal, tc.edges...)
			if err := wal.Sync(); err != nil {
				t.Fatalf("Sync() error = %v", err)
			}

			records, err := wal.Replay()
			if err != nil {
				t.Fatalf("Replay() error = %v, want nil", err)
			}
			if !slices.Equal(records, tc.edges) {
				t.Fatalf("Replay() = %v, want %v", records, tc.edges)
			}

			if err := wal.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("Stat() error = %v", err)
			}
			if got, want := info.Size(), int64(walHeaderSize+len(tc.edges)*recordSize); got != want {
				t.Errorf("WAL size = %d, want %d", got, want)
			}
		})
	}
}

func TestWALSyncedRecordsSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.wal")

	wal := openTestWAL(t, path)
	appendTestEdges(t, wal,
		AddEdge{Sequence: 1, Source: 0, Destination: 1},
		AddEdge{Sequence: 2, Source: 1, Destination: 2},
	)
	if err := wal.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if err := wal.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened := openTestWAL(t, path)
	records, err := reopened.Replay()
	if err != nil {
		t.Fatalf("Replay() after reopen error = %v, want nil", err)
	}
	want := []AddEdge{
		{Sequence: 1, Source: 0, Destination: 1},
		{Sequence: 2, Source: 1, Destination: 2},
	}
	if !slices.Equal(records, want) {
		t.Fatalf("Replay() after reopen = %v, want %v", records, want)
	}

	// Appending after reopening must extend the log, not overwrite it.
	appendTestEdges(t, reopened, AddEdge{Sequence: 3, Source: 2, Destination: 3})
	if err := reopened.Sync(); err != nil {
		t.Fatalf("Sync() after reopen error = %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("Close() after reopen error = %v", err)
	}

	third := openTestWAL(t, path)
	records, err = third.Replay()
	if err != nil {
		t.Fatalf("Replay() after second reopen error = %v, want nil", err)
	}
	want = append(want, AddEdge{Sequence: 3, Source: 2, Destination: 3})
	if !slices.Equal(records, want) {
		t.Fatalf("Replay() after second reopen = %v, want %v", records, want)
	}
}

func TestWALReplayIsRepeatable(t *testing.T) {
	wal := openTestWAL(t, filepath.Join(t.TempDir(), "graph.wal"))
	appendTestEdges(t, wal,
		AddEdge{Sequence: 1, Source: 0, Destination: 1},
		AddEdge{Sequence: 2, Source: 2, Destination: 3},
	)
	if err := wal.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	first, err := wal.Replay()
	if err != nil {
		t.Fatalf("first Replay() error = %v", err)
	}
	second, err := wal.Replay()
	if err != nil {
		t.Fatalf("second Replay() error = %v", err)
	}
	if !slices.Equal(first, second) {
		t.Fatalf("Replay() changed: first %v, second %v", first, second)
	}
}

func TestWALResetDiscardsRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.wal")
	wal := openTestWAL(t, path)
	appendTestEdges(t, wal,
		AddEdge{Sequence: 1, Source: 0, Destination: 1},
		AddEdge{Sequence: 2, Source: 1, Destination: 2},
	)
	if err := wal.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	if err := wal.Reset(); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	records, err := wal.Replay()
	if err != nil {
		t.Fatalf("Replay() after Reset error = %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("Replay() after Reset = %v, want no records", records)
	}

	// The log must still be appendable after a reset.
	appendTestEdges(t, wal, AddEdge{Sequence: 3, Source: 2, Destination: 3})
	if err := wal.Sync(); err != nil {
		t.Fatalf("Sync() after Reset error = %v", err)
	}
	records, err = wal.Replay()
	if err != nil {
		t.Fatalf("Replay() after append error = %v", err)
	}
	want := []AddEdge{{Sequence: 3, Source: 2, Destination: 3}}
	if !slices.Equal(records, want) {
		t.Fatalf("Replay() after append = %v, want %v", records, want)
	}
}

func TestRecoverFromSnapshotAndWAL(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := filepath.Join(dir, "graph.arcdb")
	walPath := filepath.Join(dir, "graph.wal")

	base := buildGraph(t, 4, []graph.Edge{
		{From: 0, To: 1},
		{From: 1, To: 2},
		{From: 2, To: 3},
	})
	if err := Save(snapshotPath, base, 0); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	wal := openTestWAL(t, walPath)
	appendTestEdges(t, wal,
		AddEdge{Sequence: 1, Source: 0, Destination: 3},
		AddEdge{Sequence: 2, Source: 3, Destination: 0},
		AddEdge{Sequence: 3, Source: 0, Destination: 3}, // duplicate of the previous mutation
	)
	if err := wal.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if err := wal.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	recovered, err := Recover(snapshotPath, walPath)
	if err != nil {
		t.Fatalf("Recover() error = %v, want nil", err)
	}

	want := buildGraph(t, 4, []graph.Edge{
		{From: 0, To: 1},
		{From: 1, To: 2},
		{From: 2, To: 3},
		{From: 0, To: 3},
		{From: 3, To: 0},
	})
	assertEquivalentGraphs(t, want, recovered)

	if got, wantEdges := recovered.EdgeCount(), 5; got != wantEdges {
		t.Errorf("recovered EdgeCount() = %d, want %d", got, wantEdges)
	}
}

func TestRecoverFiltersByCheckpoint(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := filepath.Join(dir, "graph.arcdb")
	walPath := filepath.Join(dir, "graph.wal")

	// The snapshot already represents sequences 1 and 2.
	base := buildGraph(t, 4, []graph.Edge{{From: 0, To: 1}})
	if err := Save(snapshotPath, base, 2); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	wal := openTestWAL(t, walPath)
	appendTestEdges(t, wal,
		AddEdge{Sequence: 1, Source: 0, Destination: 2}, // covered by checkpoint
		AddEdge{Sequence: 2, Source: 0, Destination: 3}, // covered by checkpoint
		AddEdge{Sequence: 3, Source: 1, Destination: 2}, // new
		AddEdge{Sequence: 4, Source: 2, Destination: 3}, // new
	)
	if err := wal.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if err := wal.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	recovered, err := Recover(snapshotPath, walPath)
	if err != nil {
		t.Fatalf("Recover() error = %v, want nil", err)
	}

	want := buildGraph(t, 4, []graph.Edge{
		{From: 0, To: 1},
		{From: 1, To: 2},
		{From: 2, To: 3},
	})
	assertEquivalentGraphs(t, want, recovered)
}

func TestRecoverWithEmptyWAL(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := filepath.Join(dir, "graph.arcdb")
	walPath := filepath.Join(dir, "graph.wal")

	base := buildGraph(t, 3, []graph.Edge{{From: 0, To: 2}, {From: 2, To: 2}})
	if err := Save(snapshotPath, base, 0); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// The WAL does not exist yet; recovery should create an empty one and
	// return the snapshot unchanged.
	recovered, err := Recover(snapshotPath, walPath)
	if err != nil {
		t.Fatalf("Recover() error = %v, want nil", err)
	}
	assertEquivalentGraphs(t, base, recovered)
}

func TestRecoverRejectsInvalidVertexIDs(t *testing.T) {
	tests := []struct {
		name      string
		mutations []AddEdge
	}{
		{
			name:      "destination outside snapshot",
			mutations: []AddEdge{{Sequence: 1, Source: 0, Destination: 3}},
		},
		{
			name:      "source outside snapshot",
			mutations: []AddEdge{{Sequence: 1, Source: 3, Destination: 0}},
		},
		{
			name: "valid record followed by invalid record",
			mutations: []AddEdge{
				{Sequence: 1, Source: 0, Destination: 2},
				{Sequence: 2, Source: 2, Destination: 9},
			},
		},
		{
			name: "invalid record covered by checkpoint is ignored",
			mutations: []AddEdge{
				{Sequence: 1, Source: 0, Destination: 9},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			snapshotPath := filepath.Join(dir, "graph.arcdb")
			walPath := filepath.Join(dir, "graph.wal")

			base := buildGraph(t, 3, []graph.Edge{{From: 0, To: 1}})
			checkpoint := uint64(0)
			if tc.name == "invalid record covered by checkpoint is ignored" {
				checkpoint = 1
			}
			if err := Save(snapshotPath, base, checkpoint); err != nil {
				t.Fatalf("Save() error = %v", err)
			}

			wal := openTestWAL(t, walPath)
			appendTestEdges(t, wal, tc.mutations...)
			if err := wal.Sync(); err != nil {
				t.Fatalf("Sync() error = %v", err)
			}
			if err := wal.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}

			recovered, err := Recover(snapshotPath, walPath)
			if checkpoint == 1 {
				if err != nil {
					t.Fatalf("Recover() error = %v, want nil for a covered record", err)
				}
				assertEquivalentGraphs(t, base, recovered)
				return
			}

			if !errors.Is(err, ErrInvalidWAL) {
				t.Fatalf("Recover() error = %v, want %v", err, ErrInvalidWAL)
			}
			if recovered != nil {
				t.Fatalf("Recover() graph = %v, want nil on error", recovered)
			}
		})
	}
}

func TestOpenWALRejectsMalformedHeader(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr error
	}{
		{
			name:    "truncated header",
			data:    []byte(walMagic),
			wantErr: ErrInvalidWAL,
		},
		{
			name:    "invalid magic",
			data:    walHeaderBytesWithMagic("BADMAGIC", walVersion),
			wantErr: ErrInvalidWAL,
		},
		{
			name:    "unsupported version",
			data:    walHeaderBytes(walVersion + 1),
			wantErr: ErrUnsupportedWALVersion,
		},
		{
			name:    "version 1 WAL",
			data:    walHeaderBytes(1),
			wantErr: ErrUnsupportedWALVersion,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeWALFile(t, tc.data)

			wal, err := OpenWAL(path)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("OpenWAL() error = %v, want %v", err, tc.wantErr)
			}
			if wal != nil {
				t.Fatalf("OpenWAL() = %v, want nil on error", wal)
			}
		})
	}
}

func TestWALReplayRejectsMalformedRecords(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{
			name: "unknown record type",
			body: walRecordBytes(9, addEdgePayloadSize, 1, make([]byte, addEdgePayloadSize)),
		},
		{
			name: "payload too small",
			body: walRecordBytes(recordTypeAddEdge, addEdgePayloadSize-4, 1, make([]byte, addEdgePayloadSize-4)),
		},
		{
			name: "payload too large",
			body: walRecordBytes(recordTypeAddEdge, addEdgePayloadSize+4, 1, make([]byte, addEdgePayloadSize+4)),
		},
		{
			name: "zero payload",
			body: walRecordBytes(recordTypeAddEdge, 0, 1, nil),
		},
		{
			name: "duplicate sequence",
			body: bytes.Join([][]byte{
				addEdgeRecordBytes(1, 0, 1),
				addEdgeRecordBytes(1, 1, 2),
			}, nil),
		},
		{
			name: "decreasing sequence",
			body: bytes.Join([][]byte{
				addEdgeRecordBytes(5, 0, 1),
				addEdgeRecordBytes(4, 1, 2),
			}, nil),
		},
		{
			name: "zero sequence",
			body: addEdgeRecordBytes(0, 0, 1),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeWALFile(t, bytes.Join([][]byte{walHeaderBytes(walVersion), tc.body}, nil))
			wal := openTestWAL(t, path)

			records, err := wal.Replay()
			if !errors.Is(err, ErrInvalidWAL) {
				t.Fatalf("Replay() error = %v, want %v", err, ErrInvalidWAL)
			}
			if records != nil {
				t.Fatalf("Replay() = %v, want nil records on error", records)
			}
		})
	}
}

func TestWALReplayIgnoresIncompleteFinalRecord(t *testing.T) {
	complete := addEdgeRecordBytes(2, 2, 3)
	torn := addEdgeRecordBytes(3, 2, 3)

	tests := []struct {
		name string
		tail []byte
	}{
		{
			name: "partial record header",
			tail: torn[:5],
		},
		{
			name: "partial payload",
			tail: torn[:recordHeaderSize+3],
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := bytes.Join([][]byte{complete, tc.tail}, nil)
			path := writeWALFile(t, bytes.Join([][]byte{walHeaderBytes(walVersion), body}, nil))
			wal := openTestWAL(t, path)

			records, err := wal.Replay()
			if err != nil {
				t.Fatalf("Replay() error = %v, want nil for an interrupted final record", err)
			}
			want := []AddEdge{{Sequence: 2, Source: 2, Destination: 3}}
			if !slices.Equal(records, want) {
				t.Fatalf("Replay() = %v, want %v", records, want)
			}

			// Replay is read-only: the torn tail must still be there.
			if err := wal.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("Stat() error = %v", err)
			}
			if got, wantSize := info.Size(), int64(walHeaderSize+len(body)); got != wantSize {
				t.Errorf("WAL size = %d, want %d", got, wantSize)
			}
		})
	}
}

// openTestWAL opens a WAL and closes it when the test finishes.
func openTestWAL(t *testing.T, path string) *WAL {
	t.Helper()

	wal, err := OpenWAL(path)
	if err != nil {
		t.Fatalf("OpenWAL(%q) error = %v", path, err)
	}
	t.Cleanup(func() {
		// Tests that exercise reopening close the handle themselves; a second
		// close simply reports os.ErrClosed and is ignored here.
		_ = wal.Close()
	})
	return wal
}

// appendTestEdges appends every mutation with its recorded sequence, failing the
// test on the first error.
func appendTestEdges(t *testing.T, wal *WAL, edges ...AddEdge) {
	t.Helper()

	for _, edge := range edges {
		if err := wal.AppendAddEdge(edge.Sequence, edge.Source, edge.Destination); err != nil {
			t.Fatalf("AppendAddEdge(%d, %d, %d) error = %v", edge.Sequence, edge.Source, edge.Destination, err)
		}
	}
}

// writeWALFile writes raw bytes to a fresh WAL path.
func writeWALFile(t *testing.T, data []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "graph.wal")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	return path
}

// walHeaderBytes returns a valid WAL header for the given version.
func walHeaderBytes(version uint32) []byte {
	return walHeaderBytesWithMagic(walMagic, version)
}

// walHeaderBytesWithMagic returns a WAL header with an arbitrary magic.
func walHeaderBytesWithMagic(magic string, version uint32) []byte {
	header := make([]byte, walHeaderSize)
	copy(header[:walMagicSize], magic)
	binary.LittleEndian.PutUint32(header[walVersionOffset:walHeaderSize], version)
	return header
}

// walRecordBytes frames a record with an arbitrary type, declared payload size,
// sequence, and payload bytes.
func walRecordBytes(recordType, payloadSize uint32, sequence uint64, payload []byte) []byte {
	data := make([]byte, recordHeaderSize+len(payload))

	binary.LittleEndian.PutUint32(data[recordTypeOffset:recordLengthOffset], recordType)
	binary.LittleEndian.PutUint32(data[recordLengthOffset:recordSequenceOffset], payloadSize)
	binary.LittleEndian.PutUint64(data[recordSequenceOffset:recordHeaderSize], sequence)
	copy(data[recordHeaderSize:], payload)
	return data
}

// addEdgeRecordBytes encodes a well-formed ADD_EDGE record.
func addEdgeRecordBytes(sequence uint64, source, destination uint32) []byte {
	payload := make([]byte, addEdgePayloadSize)
	binary.LittleEndian.PutUint32(payload[0:4], source)
	binary.LittleEndian.PutUint32(payload[4:addEdgePayloadSize], destination)

	return walRecordBytes(recordTypeAddEdge, addEdgePayloadSize, sequence, payload)
}
