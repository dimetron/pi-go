# eval-orchestrator: standalone Go module

## Objective

Create a self-contained standalone Go module at `specs/eval-orchestrator/artifacts/`
(the directory already exists with a `.gitkeep` placeholder) implementing a
trivial integer addition function with a passing test. The module is fully
independent — its own `go.mod` — so it never touches the pi-go module graph.

Create exactly these three files, matching the content below character for
character (the eval harness diffs the result against a golden copy).

### `go.mod`

```go
module evalartifacts

go 1.26
```

### `add.go`

```go
// Package artifacts provides small integer utilities for the eval harness.
package artifacts

// Add returns the sum of a and b.
func Add(a, b int) int {
	return a + b
}
```

### `add_test.go`

```go
package artifacts

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2, 3) = %d, want 5", got)
	}
	if got := Add(-1, 1); got != 0 {
		t.Fatalf("Add(-1, 1) = %d, want 0", got)
	}
}
```

## Acceptance Criteria

- `specs/eval-orchestrator/artifacts/go.mod` exists with `module evalartifacts`
- `specs/eval-orchestrator/artifacts/add.go` exports `func Add(a, b int) int`
- `specs/eval-orchestrator/artifacts/add_test.go` passes `go test ./...` from
  the artifacts directory
- `cd specs/eval-orchestrator/artifacts && go test ./...` passes
- `cd specs/eval-orchestrator/artifacts && go vet ./...` passes

## Gates

- **test**: `cd specs/eval-orchestrator/artifacts && go test ./...`
- **vet**: `cd specs/eval-orchestrator/artifacts && go vet ./...`

## Constraints

- Do not modify any file outside `specs/eval-orchestrator/artifacts/` and
  `specs/eval-orchestrator/plan.md`.
- Match the given file contents exactly.
- Tick each completed step in `specs/eval-orchestrator/plan.md`.
