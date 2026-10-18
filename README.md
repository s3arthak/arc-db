# ArcDB

A single-node graph database written in Go, built in public as a learning project.

ArcDB is an experiment in **adaptive graph traversal**: instead of hard-coding one
execution strategy, the engine measures the shape of a query (fan-out, frontier
size, direction balance) and picks the traversal that should be cheapest.

> Status: **Phase 0 — repository bootstrap.** No database code yet. The plan is
> intentionally sequenced so each phase is understood before it is built.

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
go run ./cmd/arcdb     # ArcDB 0.0.0 (phase 0: repository bootstrap)
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

## Roadmap

| Phase | Deliverable | Status |
| ----- | ----------- | ------ |
| 0 | Repository, CI, build/test loop | done |
| 1 | Immutable CSR graph + builder + `Neighbors` | next |
| 2 | BFS, shortest path reconstruction | planned |
| 3 | Bidirectional BFS, benchmarks, profiling | planned |
| 4 | Adaptive execution controller (rule-based cost model) | planned |
| 5 | Persistence: versioned binary format | planned |
| 6 | WAL + crash recovery | planned |
| 7 | Query language: lexer → parser → AST → plan → execute | planned |
| 8 | Concurrency: snapshots, copy-on-write, delta layer | planned |

## Design notes

- [Architecture overview](docs/architecture/overview.md)
- [ADR 0001 — CSR as the initial graph representation](docs/decisions/0001-csr-representation.md)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and [AGENTS.md](AGENTS.md) (guidance for
AI coding agents working in this repository).

## License

MIT — see [LICENSE](LICENSE).
