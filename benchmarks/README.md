# Benchmarks

Reproducible Go benchmarks for the graph kernel and its traversals. This suite
is **measurement only**: it does not change the CSR representation, BFS, or
shortest-path code, and it does not compare alternative implementations yet.

## Layout

```
benchmarks/
├── README.md
└── graph/
    └── graph_test.go    # benchmark-only package
```

Keeping benchmarks outside `internal/` means production packages carry no
benchmark-only helpers. Everything measured is the same public API used by
`cmd/arcdb` and future layers.

## What is measured

| Benchmark | Operation |
| --------- | --------- |
| `BenchmarkBuildCSR` | edge list → `Offsets` + `Edges` (validation, deduplication, counting, prefix sum, scatter) |
| `BenchmarkNeighbors` | `Graph.Neighbors(v)` lookup, spread across all vertices |
| `BenchmarkBFS` | full `algorithms.BFS(g, 0)` traversal |
| `BenchmarkShortestPath` | `algorithms.ShortestPath(g, 0, V-1)`, including parent-chain reconstruction |

## Graph sizes and generation

Every benchmark runs three sub-benchmarks:

| Name | Vertices | Edges (out-degree 4) |
| ---- | -------- | -------------------- |
| `10k` | 10,000 | 40,000 |
| `100k` | 100,000 | 400,000 |
| `1m` | 1,000,000 | 4,000,000 |

The generator is deterministic and arithmetic-only — no randomness, no seed to
lose:

```
edge(v, k) = (v + 1 + k*7) mod V    for k in [0, 4)
```

Properties this guarantees:

- exactly four distinct outgoing edges per vertex,
- no duplicate edges (so the build benchmark measures the normal path, not a
  pathological deduplication case),
- vertex `v` always reaches `v+1`, so BFS from vertex 0 visits every vertex and
  scans every edge,
- the last vertex is always reachable, so shortest-path timing includes a real
  parent-chain walk.

Graph generation and construction happen **before** `b.ResetTimer()`, so setup
is never folded into the measured operation. `BenchmarkBuildCSR` is the
exception by design: there, construction *is* the operation under test, while
only edge-list generation is untimed.

## Reading the numbers

`go test -bench` reports three measurements per operation:

- **ns/op** — wall-clock nanoseconds per benchmark iteration. Lower is better;
  compare only runs made on the same machine and Go version.
- **B/op** — bytes allocated per iteration. For the traversals this is the
  `O(V)` bookkeeping (visited set, parent links, queue, result), not the graph.
- **allocs/op** — heap allocation count per iteration. Zero means the operation
  reuses memory already prepared outside the loop.

Run-to-run variation is normal (CPU frequency, background load, GC timing). Use
`-count` and compare medians rather than trusting a single run.

## Benchmark sinks

Each benchmark assigns its result to a package-level sink
(`sinkGraph`, `sinkNeighbors`, `sinkOrder`, `sinkPath`). Without an observable
destination, the compiler may treat a call whose result is unused as dead code
and time an empty loop. The sink makes the result escape, so the measured work
cannot be optimized away. The values themselves are never read.

## Running

```bash
# Everything (about 30s on a laptop)
go test -bench=. -benchmem ./benchmarks/graph/...

# One operation across all sizes
go test -bench=BenchmarkBFS -benchmem ./benchmarks/graph/...

# One size
go test -bench='BenchmarkBFS/1m' -benchmem ./benchmarks/graph/...

# More stable numbers
go test -bench=BenchmarkNeighbors -benchmem -count=5 ./benchmarks/graph/...
```

The standard loop also compiles and type-checks the benchmark package:

```bash
go test ./...
```

## Limitations

- Results are single-machine, single-run measurements; there is no statistical
  harness yet.
- `BenchmarkBuildCSR` includes the temporary hash set used for deduplication,
  so its `B/op` is larger than the final graph size.
- `BenchmarkNeighbors` measures one compiled call path; it is not a cache
  benchmark, although vertex distribution still affects memory locality.
- No baseline comparison (adjacency list, bidirectional BFS, bitmaps) exists
  yet. That arrives with later phases.
