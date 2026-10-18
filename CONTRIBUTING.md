# Contributing to ArcDB

ArcDB is developed in small, explainable phases. The rule that matters most:

> **Never merge code you cannot explain.**

## Workflow

1. **Understand** the concept for the phase (see `docs/`).
2. **Design** the algorithm and interface before writing code.
3. **Implement** in the smallest package that owns the concern.
4. **Test** — table-driven unit tests next to the code, plus edge cases.
5. **Verify** — `go build ./... && go vet ./... && go test ./... && go test -race ./...`
6. **Commit** using conventional commits, then push to `main`.

## Commit style

```
feat(graph): add immutable CSR builder
test(graph): cover empty graph and self loops
perf(graph): benchmark neighbor scan
docs(decisions): record CSR trade-offs
chore(ci): run race detector on push
```

## Code rules

- No external dependencies without an ADR in `docs/decisions/` explaining why.
- Do not add abstractions "for later". Build what the current milestone needs.
- Every exported symbol gets a doc comment.
- Unit tests are colocated (`internal/graph/graph_test.go`); only
  cross-package tests belong in `tests/`.
- Performance claims require a benchmark in `benchmarks/` and numbers in the PR
  or commit message.

## Milestones

Work is scoped by the roadmap table in `README.md`. If a change does not serve
the current milestone, it waits.
