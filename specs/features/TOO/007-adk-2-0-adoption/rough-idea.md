# Rough Idea

## Title

Adopt Google ADK for Go v2.0.0

## Source / motivation

- Current dependency: `google.golang.org/adk v1.4.0` (go.mod).
- Upstream released `v2.0.0` (tag 2026-06-30), a major-version bump with breaking changes
  and new capabilities.
- We want to stay current and unlock the new graph-based workflow engine, collaboration
  agents, and unified `agent.Context`.

## What "ADK 2.0" means (verified from upstream release notes)

From the `v2.0.0` GitHub release body and `README-v2.md`:

- **Module path moves to `google.golang.org/adk/v2`.** Every import must change from
  `google.golang.org/adk/...` to `google.golang.org/adk/v2/...`. (Stdlib Go semver rule.)
- **Go 1.25+** required (we are on 1.26.4 — fine).
- **Top-level breaking change:** `session.NewEvent` now requires a `context.Context` as
  the first argument; the `NewEventWithContext` helper is removed. Time/UUID providers
  installed on `ctx` (via `platform.WithTimeProvider`/`WithUUIDProvider`) now control
  event IDs and timestamps.
- **Context unification:** the separate `ToolContext` and `CallbackContext` are merged
  into a single `agent.Context` interface. Mock contexts that were written against the
  v1 surface will be missing methods; either add them by hand or embed the new
  `agent.StrictContextMock` (preferred per migration guide).
- **New major features (not required for the upgrade but available to use):**
  - Graph-based workflow engine (`adk/workflow`) — nodes, edges, JoinNode, retries, timeouts,
    input/output schema validation, human-in-the-loop pause/resume.
  - Collaboration agents — `LlmAgent` gains `chat`, `task`, and `single_turn` modes.
  - New top-level packages: `workflow`, `plugin`, `platform`, `telemetry`, `server`, `artifact`.
  - New sub-imports: `tool/functiontool`, `tool/mcptoolset`, `tool/toolconfirmation`
    carry over (presumed; needs verification during research).

## Scope of v1.4 → v2.0 migration (initial estimate)

Touch points in this repo (~160 import sites):

- `internal/agent/` (agent.go, retry.go, agent_test.go, retry_test.go, e2e_test.go, mock_e2e_test.go)
- `internal/cli/` (cli.go, interactive.go, ping.go, *_test.go)
- `internal/provider/` (provider.go, anthropic, openai, openai_responses, openai_completions,
  openai_azure, ollama, gemini, mistral + tests)
- `internal/session/` (store.go, branch.go, *_test.go)
- `internal/tools/` (registry, all individual tools, subagent, a2a, compactor + tests)
- `internal/extension/` (hooks.go, mcp.go, *_test.go)
- `internal/palace/` (multiple tool files + tests)
- `internal/tui/` (types.go, ping.go, commit.go + tests)
- `internal/atif/` (convert.go, link.go, writer.go + tests)
- `internal/lsp/hooks.go`
- `internal/guardrail/model_wrapper.go` + test
- `internal/memory/worker.go`
- `internal/acp/server/` (runtime.go, adapter/stream.go + tests)
- `internal/jsonrpc/rpc_test.go`

## Open questions

(To be resolved in requirements.md.)

1. Is the scope **migration only** (compiles, existing tests pass, no behavior change) or
   should we **adopt new v2 features** in the same spec (e.g. switch to workflow engine,
   enable collaboration modes, use `StrictContextMock` in test fakes)?
2. Is the goal a single drop-in PR, or staged (e.g. one package at a time, behind a build
   tag) so trunk stays green?
3. What are the acceptance criteria for "done"? (build clean, full test suite green,
   e2e tests, manual smoke test of the TUI / interactive / print / rpc modes, etc.)
4. Do we want to also pin/bump the ADK v2 patch version (e.g. track a specific commit if
   v2.0.x is updated), or is v2.0.0 GA sufficient?
5. Do we keep v1 as a separate `replace`/tag in go.mod for emergency rollback, or
   clean break?
6. CI / lint gates that should be required: `go build ./...`, `go vet ./...`,
   `go test ./...`, race detector, golangci-lint, etc.
