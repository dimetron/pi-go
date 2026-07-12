# Gemini ADK Search Grounding

## Objective

Enable Gemini's built-in Google Search grounding (citations) for pi-go's
Gemini-backed agent loops, with an env-var kill switch. Touches the main
agent loop, the interactive TUI loop, and every `pi` subagent process
(via env-var inheritance). Adds zero new CLI flags or config keys.

## Key Requirements

1. **Always on for Gemini** — whenever the active LLM is a Gemini model
   (detected via `info.Provider == "gemini"`), the
   `geminitool.GoogleSearch` server-side tool is appended to the agent's
   `Tools` slice at agent-construction time. Non-Gemini providers are
   unaffected.
2. **Env-var kill switch** — `PI_NO_GROUNDING=1` (also `true` / `yes` / `on`,
   case-insensitive) disables grounding process-wide. Empty / unset / any
   non-truthy value = enabled (default). The var is inherited by subagent
   `pi` subprocesses automatically because the `"PI_"` prefix is already
   on the subagent env allowlist in `internal/subagent/environ.go`.
3. **No new CLI flag, no config key, no `agent.Config` field.** The
   feature is on by default for Gemini; the only switch is the env var.
4. **No `go.mod` change.** `geminitool` is a subpackage of the
   already-direct `google.golang.org/adk/v2 v2.0.0` require.

## Acceptance Criteria

### Provider gating

- Given `pi --model gemini-3.5-flash` (or any `gemini-*` model), when the
  agent loop starts, then `coreTools` passed to `agent.New` contains a
  tool named `google_search`.
- Given `pi --model claude-sonnet-5` (or any non-Gemini model), when the
  agent loop starts, then `coreTools` does NOT contain `google_search`.

### Env-var opt-out

- Given `PI_NO_GROUNDING=1` set and model is Gemini, when the agent loop
  starts, then `coreTools` does NOT contain `google_search`.
- Given `PI_NO_GROUNDING=0` (or any non-truthy value, or unset) and model
  is Gemini, when the agent loop starts, then `coreTools` contains
  `google_search`.
- Given `PI_NO_GROUNDING=garbage`, when `groundingDisabled()` is called,
  then it returns `false` (garbage is not truthy).

### TUI and non-TUI parity

- Given the TUI starts with `--model gemini-3.5-flash`, when the TUI
  builds the agent, then `coreTools` contains `google_search`. Same as
  the non-interactive path.

### Subagent propagation

- Given the main `pi` has `PI_NO_GROUNDING=1` and spawns a subagent with
  `--model gemini-3.1-flash-lite`, when the subagent's `pi` process
  starts, then the subagent's `coreTools` does NOT contain
  `google_search` (env var inherited via `FilterEnv` allowlist).

## Implementation Slices

1. **Helper + unit tests (load-bearing)** — Create
   `internal/agent/grounding.go` (constant `groundingEnvVar`,
   `groundingDisabled()`, `GeminiGroundingTool(providerName) (tool.Tool, bool)`,
   compile-time `var _ tool.Tool = geminitool.GoogleSearch{}`).
   Create `internal/agent/grounding_test.go` with table-driven tests for
   `groundingDisabled` (truthy and non-truthy tokens) and
   `GeminiGroundingTool` (gemini-on, gemini-off, non-gemini providers,
   empty string). **Verify:** `go test ./internal/agent/... -run Grounding -v`
   passes, `go vet ./internal/agent/...` clean, no regression in existing
   `internal/agent` tests.
2. **Wire into non-interactive CLI** — Edit `internal/cli/cli.go` to insert
   a 4-line conditional immediately before the `agent.New(agent.Config{...})`
   call at line 655. The conditional calls
   `agent.GeminiGroundingTool(info.Provider)` and appends to `coreTools`
   on `ok == true`. **Verify:** `go build ./cmd/pi` succeeds,
   `go test ./internal/cli/...` passes, `git diff --stat` shows one
   ~5-line addition in `cli.go`.
3. **Wire into interactive TUI** — Edit `internal/cli/interactive.go` with
   the same 4-line conditional before the `agent.New(agent.Config{...})`
   call at line 354. **Verify:** `go build ./cmd/pi` succeeds,
   `go test ./internal/cli/... ./internal/tui/...` passes,
   `git diff --stat` shows one ~4-line addition in `interactive.go`.
4. **End-to-end smoke** — `go mod tidy` (should be a no-op), `go build ./cmd/pi`,
   `go vet ./...`, `go test ./...`, `golangci-lint run ./...` (if
   available). Confirm `git status` shows exactly the four expected files
   (2 new, 2 modified).

## Gates

- **build:** `go build ./cmd/pi` (per `Makefile` `build` target; we do not
  touch `pi-sandbox`)
- **test (unit):** `go test ./...` (per `Makefile` `test-unit` target)
- **test (targeted during dev):** `go test ./internal/agent/... -run Grounding -v`
  and `go test ./internal/cli/... ./internal/tui/...`
- **vet:** `go vet ./...` (per `Makefile` `vet` target)
- **lint:** `golangci-lint run ./...` (per `Makefile` `lint` target; only
  if installed)
- **tidy:** `go mod tidy` (should be a no-op; confirms no module change)

## Reference

- Design: `specs/features/TOO/012-gemini-adk-search-grounding/design.md`
- Outline: `specs/features/TOO/012-gemini-adk-search-grounding/outline.md`
- Plan: `specs/features/TOO/012-gemini-adk-search-grounding/plan.md`
- Requirements: `specs/features/TOO/012-gemini-adk-search-grounding/requirements.md`
- Research: `specs/features/TOO/012-gemini-adk-search-grounding/research/grounding-integration-points.md`

## Constraints

- **Branch:** `feature/gemini-adk-grounding` (already created; the
  working tree has uncommitted changes to `internal/tools/grep.go` and
  `internal/tools/sandbox.go` from a prior session — leave them alone,
  the new code does not touch the `internal/tools/` package).
- **No `go.mod` change** — `geminitool` is reachable as a subpackage of
  the existing direct require.
- **No CLI flag, no config key, no `agent.Config` field.** Env var only.
- **Helper lives in `internal/agent/grounding.go`** — not in `internal/cli`
  or `internal/provider`. `internal/agent` is the only package that
  imports `geminitool`.
- **Subagent propagation is automatic** — the `"PI_"` prefix is already
  on the `DefaultEnvAllowlist` in `internal/subagent/environ.go:60`. No
  spawner or env-filter change.
- **No e2e test added** — the feature is provider-gated; e2e tests use
  mock LLMs. Real-API verification is out of scope for this slice.
- **Match existing code style** — Go 1.26, `gofmt`, standard
  `errors.Join` / `fmt.Errorf("...: %w", err)`, package-level doc
  comments (see `internal/agent/agent.go` and
  `internal/tools/registry.go` for the established patterns).
- **Compile-time interface check** is included in slice 1 (one extra
  line, no runtime cost) to catch a hypothetical upstream rename of
  `geminitool.GoogleSearch`.
