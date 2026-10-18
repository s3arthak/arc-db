# AGENTS.md — instructions for AI coding agents

ArcDB is a learning project. The human owner must be able to explain every line,
so optimize for **clarity and correctness over cleverness**.

## Before writing code

1. Read `README.md` and the current phase in `docs/architecture/overview.md`.
2. Read any relevant ADR in `docs/decisions/`.
3. Inspect the existing packages you are about to touch.
4. State the proposed change and which files it affects.
5. If the task is outside the current milestone, stop and ask.

## Implementation rules

- Standard library only, unless an ADR explicitly approves a dependency.
- No speculative abstractions, no empty interfaces "for flexibility".
- Keep packages narrow: `internal/graph` knows nothing about storage or HTTP.
- Exported identifiers need doc comments; non-obvious code gets a short comment
  explaining *why*, not *what*.
- Complexity must be stated (time and space) in the PR summary.
- Preserve the existing style; `gofmt` output is mandatory.

## Testing rules

- Table-driven tests for every public function, colocated with the code.
- Always cover: empty input, single element, invalid IDs, duplicates, cycles
  (where meaningful), and boundaries.
- Run: `go build ./... && go vet ./... && go test ./... && go test -race ./...`
- Performance work also needs `go test -bench . -benchmem` numbers.

## After implementation

Report, in this order:

1. What changed and why
2. Complexity (time / space)
3. Important Go concepts used
4. Tests added and what they cover
5. Assumptions and limitations
6. Anything the owner should verify manually before committing
