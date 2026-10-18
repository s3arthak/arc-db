# ArcDB

A single-node graph database written in Go, built in public as a learning project.

ArcDB is an experiment in **adaptive graph traversal**: instead of hard-coding one
execution strategy, the engine measures the shape of a query (fan-out, frontier
size, direction balance) and picks the traversal that should be cheapest.

> Status: **Phase 4 — snapshot persistence.** Graphs can be built, traversed,
> and saved to a versioned binary snapshot that loads back as the same CSR.
> WAL, recovery, and query planning come later.

## Why this exists

Two goals at once:

1. **Learn database internals for real** — representation, algorithms, planning,
   storage, durability, concurrency.
2. **Learn Go in context** — structs, slices, memory layout, interfaces, testing,
   benchmarking, goroutines, profiling.

Every commit should be explainable by the person who pushed it.

## Repository layout

```
arc-db/
├── cmd/arcdb/          # executable entry point
├── internal/
│   ├── graph/          # graph kernel: CSR representation, builder
│   ├── algorithms/     # BFS, bidirectional BFS, frontier strategies
│   ├── planner/        # cost model + adaptive strategy selection
│   ├── storage/        # binary format, WAL, recovery
│   ├── query/          # lexer, parser, AST, validation
│   └── server/         # request/response surface
├── tests/
│   ├── integration/    # cross-package tests
│   └── e2e/            # end-to-end tests through the public interface
├── benchmarks/         # reproducible performance experiments
├── docs/
│   ├── architecture/   # how the pieces fit
│   ├── algorithms/     # algorithm notes
│   ├── storage/        # on-disk format notes
│   └── decisions/      # architecture decision records (ADRs)
├── scripts/            # dev helpers
└── .github/workflows/  # CI: build, vet, test, race
```

Unit tests live next to the code they test (`internal/graph/graph_test.go`).
Only cross-cutting tests go in `tests/`.

## Build, test, run

```bash
go run ./cmd/arcdb     # ArcDB 0.2.0 (phase 2: BFS traversal)
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

## Graph kernel

`internal/graph` turns a directed edge list into two flat arrays
(`Offsets` + `Edges`) and answers neighbor lookups without per-vertex
allocations:

```go
edges := []graph.Edge{
	{From: 0, To: 1},
	{From: 0, To: 2},
	{From: 0, To: 3},
	{From: 1, To: 3},
	{From: 2, To: 3},
}

g, err := graph.BuildFromEdges(4, edges)
// g.Offsets == [0 3 4 5 5]
// g.Edges   == [1 2 3 3 3]
// g.VertexCount() == 4, g.EdgeCount() == 5

neighbors, err := g.Neighbors(0) // [1 2 3]
```

Duplicate edges are collapsed (simple directed graph), self-loops are kept,
invalid IDs are rejected with `ErrVertexOutOfRange`, and identical input always
produces identical arrays. Build is `O(V + E)` expected; `Neighbors` is `O(1)`
plus `O(degree(v))` to read.

## Traversals

`internal/algorithms` walks the graph through `Neighbors` only, so traversal
logic is independent of the physical CSR layout:

```go
order, err := algorithms.BFS(g, 0) // [0 1 2 3 4 5]

path, found, err := algorithms.ShortestPath(g, 0, 5)
// path == [0 1 3 5] (or [0 2 4 5]), found == true
```

BFS is `O(V + E)` time and `O(V)` memory. When several shortest paths have the
same length, the one returned follows first-discovery order, which comes from
the edge order used to build the graph. See
[docs/algorithms/bfs.md](docs/algorithms/bfs.md).

## Benchmarks

`benchmarks/graph` times CSR construction, neighbor lookup, BFS, and shortest
path on deterministic sparse graphs (10K, 100K, and 1M vertices with four
outgoing edges per vertex):

```bash
go test -bench=. -benchmem ./benchmarks/graph/...
```

See [benchmarks/README.md](benchmarks/README.md) for the graph generator, what
`ns/op`, `B/op`, and `allocs/op` mean, and how to run a single size.

## Persistence

`internal/storage` saves an immutable CSR graph to a binary snapshot and loads
it back without rebuilding from an edge list:

```go
if err := storage.Save("graph.arcdb", g); err != nil {
	log.Fatal(err)
}

loaded, err := storage.Load("graph.arcdb")
```

The file is little-endian with a 28-byte header (magic `ARCDBSNP`, version 1,
vertex and edge counts) followed by the raw offsets and edges. Loading treats
the file as untrusted and validates the magic, version, size, counts, and every
CSR invariant before returning a graph. The complete layout is documented in
[docs/storage/snapshot.md](docs/storage/snapshot.md). There is no WAL or crash
recovery yet.

## Roadmap

| Phase | Deliverable | Status |
| ----- | ----------- | ------ |
| 0 | Repository, CI, build/test loop | done |
| 1 | Immutable CSR graph + builder + `Neighbors` | done |
| 2 | BFS, shortest path reconstruction | done |
| 3 | Benchmark baseline for kernel, BFS, shortest path | done |
| 4 | CSR snapshot persistence (binary save/load) | done |
| 5 | Bidirectional BFS, profiling | next |
| 6 | Adaptive execution controller (rule-based cost model) | planned |
| 7 | WAL + crash recovery | planned |
| 8 | Query language: lexer → parser → AST → plan → execute | planned |
| 9 | Concurrency: snapshots, copy-on-write, delta layer | planned |

## Design notes

- [Architecture overview](docs/architecture/overview.md)
- [ADR 0001 — CSR as the initial graph representation](docs/decisions/0001-csr-representation.md)
- [Snapshot format](docs/storage/snapshot.md)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and [AGENTS.md](AGENTS.md) (guidance for
AI coding agents working in this repository).

## License

MIT — see [LICENSE](LICENSE).
