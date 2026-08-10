# Plan — concurrency budget that composes under nesting

Vertical slices. Each compiles and passes tests on its own.

## Progress

- [x] Step 1: Read the concurrency budget from the environment
- [x] Step 2: Halve the budget for spawned children
- [x] Step 3: Lower the default pool size to 3
- [x] Step 4: Truncate session titles on a rune boundary

### Slice 1: Read the concurrency budget from the environment

Files: `internal/subagent/orchestrator.go`, `internal/subagent/concurrency.go` (new)

Add `ConcurrencyFromEnv()`; use it where `NewPool(DefaultPoolSize)` is called.
Unset, malformed, or out-of-range values fall back to the default; the result
is never below 1.

Verify: `go test ./internal/subagent/`
Parallel-safe: no (Slice 2 builds on it)

### Slice 2: Halve the budget for spawned children

Files: `internal/subagent/environ.go`

Set `PI_SUBAGENT_CONCURRENCY` explicitly in the child environment to
`max(1, parent/2)` instead of letting the inherited value pass through.

Verify: `go test ./internal/subagent/`
Parallel-safe: no (depends on Slice 1)

### Slice 3: Lower the default pool size to 3

Files: `internal/subagent/orchestrator.go`

Change `DefaultPoolSize` and record why in the comment, so the number reads as
evidence-based rather than arbitrary.

Verify: `go test ./internal/subagent/`
Parallel-safe: yes

### Slice 4: Truncate session titles on a rune boundary

Files: `internal/session/store.go`

Replace `title[:MaxSessionTitle]` with a rune-safe cut.

Verify: `go test ./internal/session/`
Parallel-safe: yes

## Gates

- **build**: `go build ./...`
- **vet**: `go vet ./...`
- **test**: `go test ./...`
- **lint**: `golangci-lint run ./internal/subagent/... ./internal/session/...`
