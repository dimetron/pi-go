# Plan — Gemini ADK Search Grounding

Vertical-slice implementation plan. Each slice is a self-contained unit of
work that builds and passes its own tests before the next slice starts. The
plan is intentionally small — the entire feature is ~165 lines, two new
files, two edited files.

## Gates (discovered from the project `Makefile`)

- **build:** `go build ./cmd/pi` (and `./cmd/pi-sandbox` if changed; we do
  not touch `pi-sandbox` so that build is not required)
- **test (unit):** `go test ./...`
- **test (targeted):** `go test ./internal/agent/... -run Grounding -v` and
  `go test ./internal/cli/...`
- **vet:** `go vet ./...`
- **lint:** `golangci-lint run ./...` (only if available locally; not
  required for CI green if not installed)
- **tidy:** `go mod tidy` — should be a no-op since `geminitool` is a
  subpackage of the already-direct `adk/v2` module, but run to confirm.

## Constraints

- **No `go.mod` change** — `google.golang.org/adk/v2/tool/geminitool` is a
  subpackage of the existing direct require `google.golang.org/adk/v2 v2.0.0`.
- **No new CLI flag, no new config key, no new `agent.Config` field** — per
  requirements.
- **Env-var propagation to subagents is automatic** — the `"PI_"` prefix is
  already on the subagent env allowlist in
  `internal/subagent/environ.go:60`. No spawner or env-filter change.
- **Match existing code style** — see `internal/agent/agent.go` for
  package-level doc comments and `internal/tools/registry.go` for test
  patterns.

## Slices

### Slice 1 — Helper module + unit tests

The load-bearing piece. Once this passes, the rest of the feature is just
wiring.

- [ ] **Create `internal/agent/grounding.go`**
    - Package `agent`
    - `// Package agent — grounding.go` header comment
    - Constant `const groundingEnvVar = "PI_NO_GROUNDING"` (unexported)
    - `func groundingDisabled() bool` — case-insensitive truthy check
      (`"1"`, `"true"`, `"yes"`, `"on"` after `TrimSpace` + `ToLower`)
    - `func GeminiGroundingTool(providerName string) (tool.Tool, bool)` —
      returns `(geminitool.GoogleSearch{}, true)` iff
      `providerName == "gemini"` AND `!groundingDisabled()`
    - Compile-time interface assertion: `var _ tool.Tool = geminitool.GoogleSearch{}`
    - Doc comment on the exported function explaining when it returns the
      tool and what env var disables it
    - Imports: `os`, `strings`, `google.golang.org/adk/v2/tool`,
      `google.golang.org/adk/v2/tool/geminitool`

- [ ] **Create `internal/agent/grounding_test.go`**
    - Package `agent` (same package, white-box test — exercises
      `groundingDisabled` directly; can also be done in `agent_test` for
      black-box — pick the existing convention; `internal/agent/agent_test.go`
      is `package agent`, so use `package agent`)
    - `TestGroundingDisabled`: table-driven with `t.Setenv` for each row
        - truthy: `"1"`, `"TRUE"`, `"True"`, `"yes"`, `"YES"`, `"on"`, `"On"`,
          `" 1 "` (whitespace tolerance)
        - non-truthy: unset (do not call `t.Setenv`), `""`, `"0"`, `"false"`,
          `"no"`, `"off"`, `"garbage"`, `"2"`, `"enabled"`
    - `TestGeminiGroundingTool`: table-driven
        - `("gemini")` with env unset → expect non-nil tool, `ok == true`,
          `Name() == "google_search"`
        - `("gemini")` with `PI_NO_GROUNDING=1` → expect `nil`, `false`
        - `("gemini")` with `PI_NO_GROUNDING=true` → expect `nil`, `false`
        - `("gemini")` with `PI_NO_GROUNDING=0` → expect non-nil tool, `ok == true`
        - `("anthropic")` with env unset → expect `nil`, `false`
        - `("openai")` with env unset → expect `nil`, `false`
        - `("ollama")` with env unset → expect `nil`, `false`
        - `("")` (empty) with env unset → expect `nil`, `false` (defence)
        - `("anthropic")` with `PI_NO_GROUNDING=1` → expect `nil`, `false`
          (env var does not enable grounding for non-Gemini)
    - `TestGeminiGroundingTool_NamesADKInterface`: a single assertion
      `if name := (geminitool.GoogleSearch{}).Name(); name != "google_search" { t.Errorf(...) }`
      — protects against upstream rename

- [ ] **Verify slice 1**
    - `go build ./internal/agent/...` — compiles cleanly
    - `go vet ./internal/agent/...` — no warnings
    - `go test ./internal/agent/... -run Grounding -v` — all pass
    - `go test ./internal/agent/...` — all existing agent tests still pass
      (no regression)

**Dependencies:** none. This is the first slice.

### Slice 2 — Wire into the non-interactive CLI

One small edit, two lines + a comment.

- [ ] **Edit `internal/cli/cli.go`**
    - Locate the `agent.New(agent.Config{...})` call at line 655
    - Immediately before the call, insert:
      ```go
      // Gemini search grounding. Always on for the Gemini provider; kill
      // switch via PI_NO_GROUNDING=1 (propagates to subagent pi processes via
      // FilterEnv's PI_ prefix allowlist).
      if gTool, ok := agent.GeminiGroundingTool(info.Provider); ok {
          coreTools = append(coreTools, gTool)
      }
      ```
    - No import changes needed — `agent` is already imported in this file
      (verify with grep before edit; the package is referenced many times
      throughout the file as `agent.New`, `agent.AppName`, `agent.SystemInstruction`, etc.)
    - `info` is in scope at this site (declared at line 188). `coreTools` is
      in scope (declared at line 434 as `coreTools := runtime.coreTools` and
      appended to in lines 489, 512, 597).

- [ ] **Verify slice 2**
    - `go build ./cmd/pi` — succeeds
    - `go vet ./internal/cli/...` — no warnings
    - `go test ./internal/cli/...` — all existing tests still pass
    - `git diff --stat` shows a single 5-line addition in `internal/cli/cli.go`

**Dependencies:** Slice 1 (so the `agent.GeminiGroundingTool` symbol exists).

### Slice 3 — Wire into the interactive TUI

Symmetric change to slice 2 in a different file.

- [ ] **Edit `internal/cli/interactive.go`**
    - Locate the `agent.New(agent.Config{...})` call at line 354
    - Immediately before the call, insert:
      ```go
      // Gemini search grounding (see agent.GeminiGroundingTool doc).
      if gTool, ok := agent.GeminiGroundingTool(info.Provider); ok {
          coreTools = append(coreTools, gTool)
      }
      ```
    - No import changes needed — `agent` is already imported in this file
    - `info` and `coreTools` are in scope at this site (the TUI builds
      `coreTools` analogously to the non-interactive path)

- [ ] **Verify slice 3**
    - `go build ./cmd/pi` — succeeds
    - `go vet ./internal/cli/...` — no warnings
    - `go test ./internal/cli/...` — all existing tests still pass
    - `go test ./internal/tui/...` — all existing TUI tests still pass
    - `git diff --stat` shows a single 4-line addition in
      `internal/cli/interactive.go`

**Dependencies:** Slice 2.

### Slice 4 — End-to-end smoke

No code changes. Just exercise the full build / vet / test / lint surface
to confirm nothing regressed.

- [ ] **Run full gates**
    - `go mod tidy` (should be a no-op; confirms no module graph change)
    - `go build ./cmd/pi` (per `Makefile` line 3)
    - `go vet ./...`
    - `go test ./...` (per `Makefile` `test-unit` target)
    - `golangci-lint run ./...` (per `Makefile` `lint` target — only if
      `golangci-lint` is on PATH; document if skipped)
- [ ] **Confirm change surface**
    - `git status` — show `internal/agent/grounding.go` and
      `internal/agent/grounding_test.go` as new, `internal/cli/cli.go` and
      `internal/cli/interactive.go` as modified
    - `git diff --stat` — confirm only the four expected files are touched
    - `go test ./... -count=1` (re-run without test cache to confirm the
      result is stable)

**Dependencies:** Slices 1, 2, 3.

## Verification summary

| Step          | Command                                                              | Expected                                                           |
|---------------|----------------------------------------------------------------------|--------------------------------------------------------------------|
| After Slice 1 | `go test ./internal/agent/... -run Grounding -v`                     | All new tests pass; existing `internal/agent/...` tests still pass |
| After Slice 2 | `go build ./cmd/pi && go test ./internal/cli/...`                    | Builds; CLI tests pass                                             |
| After Slice 3 | `go build ./cmd/pi && go test ./internal/cli/... ./internal/tui/...` | Builds; CLI + TUI tests pass                                       |
| After Slice 4 | `go test ./... && go vet ./...`                                      | Full suite green; no vet warnings                                  |
| Optional      | `golangci-lint run ./...`                                            | No new lint findings                                               |

## Reference

- Design: `specs/features/TOO/012-gemini-adk-search-grounding/design.md`
- Outline: `specs/features/TOO/012-gemini-adk-search-grounding/outline.md`
- Requirements: `specs/features/TOO/012-gemini-adk-search-grounding/requirements.md`
- Research: `specs/features/TOO/012-gemini-adk-search-grounding/research/grounding-integration-points.md`
