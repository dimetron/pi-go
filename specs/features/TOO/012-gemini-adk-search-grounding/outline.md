# Outline — Gemini ADK Search Grounding

Vertical-slice outline. Each slice is independently buildable and testable;
each is small enough to review in one pass.

## Slice 1 — Helper module + tests (the load-bearing piece)

**New file:** `internal/agent/grounding.go`

- Package `agent`
- Constant `groundingEnvVar = "PI_NO_GROUNDING"`
- `func groundingDisabled() bool` — truthy token check (case-insensitive
  `1`/`true`/`yes`/`on`)
- `func GeminiGroundingTool(providerName string) (tool.Tool, bool)` — returns
  the tool iff provider is `"gemini"` AND env var not disabled; otherwise
  `(nil, false)`
- Imports: `os`, `strings`, `google.golang.org/adk/v2/tool`,
  `google.golang.org/adk/v2/tool/geminitool`

**New file:** `internal/agent/grounding_test.go`

- Table-driven tests for `groundingDisabled()`:
    - truthy: `"1"`, `"true"`, `"yes"`, `"on"` (lowercase + uppercase + mixed)
    - non-truthy: unset, `""`, `"0"`, `"false"`, `"no"`, `"off"`, `"garbage"`,
      `" "` (whitespace), `"1;rm -rf /"` (defensive: leading digits, not a clean match)
- Tests for `GeminiGroundingTool`:
    - `("gemini")` with env unset → non-nil tool, `ok == true`
    - `("gemini")` with `PI_NO_GROUNDING=1` → `nil`, `false`
    - `("anthropic")` / `("openai")` / `("ollama")` / `("")` with env unset →
      `nil`, `false`
    - `("anthropic")` with `PI_NO_GROUNDING=1` → `nil`, `false` (env var does
      not enable grounding for non-Gemini; defence in depth)
- Compile-time check: assert `geminitool.GoogleSearch{}` satisfies
  `tool.Tool` and `Name() == "google_search"`. Catches a hypothetical
  upstream signature change.

**Verify:** `go test ./internal/agent/... -run Grounding` → all pass.
Also `go vet ./internal/agent/...` clean.

**Dependencies:** none (this is the first slice; it stands alone).

## Slice 2 — Wire into the non-interactive CLI

**Edit:** `internal/cli/cli.go`

- Add import `agent "github.com/dimetron/pi-go/internal/agent"` only if not
  already present (it is — used elsewhere in the file). No new import.
- Add the two-line conditional immediately before `agent.New(agent.Config{
  ... })` at line 655:

  ```go
  // Gemini search grounding: always on for the Gemini provider, kill-switch
  // via PI_NO_GROUNDING=1 (propagates to subagent processes via FilterEnv).
  if gTool, ok := agent.GeminiGroundingTool(info.Provider); ok {
      coreTools = append(coreTools, gTool)
  }
  ```

**Verify:** `go build ./cmd/pi` succeeds. `go test ./internal/cli/...` passes
with no test changes (this slice is a wiring change; the helper is tested in
slice 1).

**Dependencies:** Slice 1.

## Slice 3 — Wire into the interactive TUI

**Edit:** `internal/cli/interactive.go`

- Add the same two-line conditional immediately before `agent.New(
  agent.Config{ ... })` at line 354.
- The `agent` import is already in this file.
- `info` and `coreTools` are both already in scope at this site.

**Verify:** `go build ./cmd/pi` succeeds. `go test ./internal/cli/...`
passes.

**Dependencies:** Slice 2 (so the cli.go and interactive.go changes are
applied together and verified by the same build).

## Slice 4 — End-to-end smoke

**No code changes.** Just a clean build + full test sweep.

- `go build ./cmd/pi`
- `go vet ./...`
- `go test ./...`
- `golangci-lint run ./...` (if available)

The new file `internal/agent/grounding.go` should appear in `git status`;
the two CLI files should show a two-line diff each.

**Verify:** clean output, no new test failures, no lint warnings introduced
by the new code.

**Dependencies:** Slices 1, 2, 3.

---

## Type signatures (the public surface of the change)

```go
// internal/agent/grounding.go
package agent

import (
	"google.golang.org/adk/v2/tool"
)

const groundingEnvVar = "PI_NO_GROUNDING"

func groundingDisabled() bool                                   // unexported
func GeminiGroundingTool(providerName string) (tool.Tool, bool) // exported
```

That's the entire new public surface. Two CLI sites each call
`agent.GeminiGroundingTool(info.Provider)` in a one-line conditional.

## Files touched

| Action | File                               | Approx. lines      |
|--------|------------------------------------|--------------------|
| create | `internal/agent/grounding.go`      | ~35                |
| create | `internal/agent/grounding_test.go` | ~120               |
| edit   | `internal/cli/cli.go`              | +5 (incl. comment) |
| edit   | `internal/cli/interactive.go`      | +5 (incl. comment) |

Total: 2 new files, 2 edited files, ~165 lines added.
