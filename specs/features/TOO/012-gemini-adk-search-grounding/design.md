# Design — Gemini ADK Search Grounding

## Summary

When the active LLM is a Gemini model, pi-go automatically registers the
`geminitool.GoogleSearch` server-side tool with the ADK agent. The tool is the
Google-builtin "grounded generation" feature: Gemini calls Google Search
internally and returns cited answers. A single env var, `PI_NO_GROUNDING`,
disables it process-wide. No new CLI flag, no config key, no `agent.Config`
field.

## Current state

- `internal/provider/gemini.go:49` constructs `gemini.NewModel(ctx, modelName, cfg)`
  and returns it. No tool is attached to Gemini at the provider level.
- `internal/cli/cli.go:655` and `internal/cli/interactive.go:354` each call
  `agent.New(agent.Config{Tools: coreTools, ...})`. `coreTools` is the slice
  produced by `tools.CoreTools(sandbox)` plus agent/memory/LSP extras. The
  `google_search` tool is not in any of those builders.
- `internal/agent/agent.go:277` forwards `cfg.Tools` to
  `llmagent.New(llmagent.Config{Tools: cfg.Tools, ...})`, so the ADK runner
  iterates whatever pi-go puts in `coreTools`.
- `internal/subagent/spawner.go:109` spawns subagents as separate `pi`
  subprocesses via `exec.CommandContext`. Their env is filtered through
  `internal/subagent/environ.go:68` (`FilterEnv`), whose allowlist
  (`DefaultEnvAllowlist` at `environ.go:11`) already permits the `"PI_"`
  prefix.

## Desired end state

- A Gemini user (e.g. `pi --model gemini-3.5-flash`) gets grounded answers
  with citations out of the box, with no flags to set.
- A user on a different provider (Anthropic / OpenAI / Ollama / Mistral) sees
  no behaviour change.
- Setting `PI_NO_GROUNDING=1` in the environment (or in a subagent's `Env`
  override) disables grounding for that process and every child `pi` process
  it spawns. Setting it to a falsy value or leaving it unset re-enables
  grounding.
- A subagent's process (spawned as a fresh `pi`) inherits `PI_NO_GROUNDING`
  automatically because the env allowlist passes `PI_*` through. No spawner
  change is needed.

## Architecture

```
                    ┌──────────────────────────────────────────────┐
                    │  pi main process                              │
                    │                                              │
  --model gemini-*  │  cli.go: initNonInteractiveRuntime            │
                    │      └─ coreTools = tools.CoreTools(sandbox)  │
                    │                                              │
                    │  cli.go: runNonInteractive                    │
                    │      └─ if info.Provider == "gemini"          │
                    │             && !groundingDisabled()           │
                    │         coreTools = append(coreTools,         │
                    │             geminitool.GoogleSearch{})        │
                    │      └─ agent.New(agent.Config{Tools:...})    │
                    │                                              │
                    │  interactive.go: TUI path (same conditional)   │
                    └──────────────────────────────────────────────┘
                                       │
                                       │  exec.CommandContext
                                       │  (env filtered by FilterEnv, keeps PI_*)
                                       ▼
                    ┌──────────────────────────────────────────────┐
                    │  pi subagent process (e.g. --model gemini-*)  │
                    │      └─ same code path; same conditional      │
                    │         (PI_NO_GROUNDING inherited)           │
                    └──────────────────────────────────────────────┘
```

The only new logic is the conditional append to `coreTools` in two places.
The env-var check is a tiny helper.

## Components

### New file: `internal/agent/grounding.go`

A single small package-level helper that owns the two questions "should
grounding be enabled for this provider?" and "is the env var set?". Keeping
it in `internal/agent` puts the decision next to the `agent.New` consumer
without polluting the provider package or the CLI package.

```go
// Package agent — grounding.go
package agent

import (
    "os"
    "strings"

    "google.golang.org/adk/v2/tool/geminitool"
)

// groundingEnvVar is the env var that disables Gemini search grounding
// process-wide. Empty / unset / any value other than a recognised truthy
// token means "grounding is enabled (subject to provider check)".
const groundingEnvVar = "PI_NO_GROUNDING"

// groundingDisabled reports whether the PI_NO_GROUNDING env var is set to a
// truthy value. Recognised truthy tokens (case-insensitive): "1", "true",
// "yes", "on". Anything else (including unset and empty string) is treated
// as "not disabled".
func groundingDisabled() bool {
    v := strings.ToLower(strings.TrimSpace(os.Getenv(groundingEnvVar)))
    switch v {
    case "1", "true", "yes", "on":
        return true
    }
    return false
}

// GeminiGroundingTool returns the geminitool.GoogleSearch tool if and only if
// the active provider is "gemini" and grounding has not been disabled by the
// PI_NO_GROUNDING env var. It returns (nil, false) otherwise. Callers append
// the returned tool to the agent's Tools slice only when the second return
// value is true.
func GeminiGroundingTool(providerName string) (tool.Tool, bool) {
    if providerName != "gemini" {
        return nil, false
    }
    if groundingDisabled() {
        return nil, false
    }
    return geminitool.GoogleSearch{}, true
}
```

Rationale for putting the helper in `internal/agent`:

- `internal/agent/agent.go` already owns the `agent.New(...)` boundary.
- Importing `geminitool` from `internal/agent` is clean: `agent` is the only
  package that calls `llmagent.New` and the only place that knows whether
  the LLM is Gemini at agent-construction time.
- Tests for the helper live alongside in `internal/agent/grounding_test.go`.

### Edit: `internal/cli/cli.go`

Two single-line additions around the `agent.New(...)` call at line 655.

**Step 1** — just before `agent.New`, after `coreTools` is finalised:

```go
// Gemini's native search grounding. Always on for the Gemini provider;
// disable process-wide with PI_NO_GROUNDING=1.
if gTool, ok := agent.GeminiGroundingTool(info.Provider); ok {
    coreTools = append(coreTools, gTool)
}
```

**Step 2** — no other change in `cli.go` (the same `coreTools` is shared by
both the JSON-mode and RPC-mode branches; appending once is enough).

The variable `info` is already in scope at this site (declared at
`cli.go:188`). `coreTools` is the same slice built up across the function
and read at line 434 (`coreTools := runtime.coreTools`) and 655. The
append must happen at line 655, after all the other appends (memory,
palace, LSP) and immediately before `agent.New`.

### Edit: `internal/cli/interactive.go`

Symmetric change just before the `agent.New(...)` call at line 354.
`info` and `coreTools` are both already in scope (the TUI builds its
`coreTools` from the same `tools.CoreTools` plus agent/memory/LSP extras).

```go
if gTool, ok := agent.GeminiGroundingTool(info.Provider); ok {
    coreTools = append(coreTools, gTool)
}
```

### Test file: `internal/agent/grounding_test.go`

Covers the helper directly, in isolation from the rest of the agent loop.
Cases:

1. `GeminiGroundingTool("gemini")` with env unset → returns the tool, `true`.
2. `GeminiGroundingTool("gemini")` with `PI_NO_GROUNDING=1` → returns
   `nil, false`.
3. Same as (2) but with `PI_NO_GROUNDING=true` / `=yes` / `=on` (case
   variations).
4. `PI_NO_GROUNDING=0`, `=false`, `=no`, empty string, garbage value → all
   treated as "not disabled", so Gemini returns the tool.
5. `GeminiGroundingTool("anthropic")` / `"openai"` / `"ollama"` → returns
   `nil, false` regardless of env.

Uses `t.Setenv` to control the env (auto-restored on test end, parallel-safe
within the test).

## Data models

None new. The change is purely the addition of one `tool.Tool` value to an
existing `[]tool.Tool` slice at agent-construction time. No config, no DB,
no JSON.

## Patterns to follow

- **Provider branching by string** — `cli.go:209` already uses
  `info.Provider == "gemini"` as a sentinel. Same idiom.
- **Env-var override** — `PI_GO_UPDATE_CHECK=0` in `upgrade.go:27` is the
  closest precedent for "env var toggles a feature off". Our truthy-token
  set (`1`/`true`/`yes`/`on`) is broader but stays compatible with the
  `=0` style: setting `PI_NO_GROUNDING=0` is treated as "not disabled",
  matching the convention that any non-truthy value means "off the switch".
- **Tool-construction style** — `internal/tools/registry.go:20` builds
  `coreTools` via a list of `newXxxTool` constructors. We do not use that
  pattern because grounding is not a normal tool: it has no Go-side
  function, no input schema, no validation, and its presence depends on
  the *provider*, not the sandbox. A one-line conditional in the CLI is
  simpler and more honest.

## Error handling strategy

There is no error to handle. `geminitool.GoogleSearch{}` is a zero-value
struct with no fallible constructor. The env-var check has no side effects.
The append is a pure slice operation. If the helper is wired incorrectly,
`agent.New` would still succeed (the runner just silently ignores an extra
tool that the LLM doesn't recognise), so the worst-case failure mode is
"grounding is unexpectedly on or off" — and that is the *intended* signal.

The only failure mode worth testing is the env-var read itself, which Go's
`os.Getenv` cannot fail on.

## Acceptance criteria

### Provider gating

- **Given** a user runs `pi --model gemini-3.5-flash`,
  **when** the agent loop starts,
  **then** `coreTools` passed to `agent.New` contains a tool named
  `google_search`.

- **Given** a user runs `pi --model claude-sonnet-5` (or any non-Gemini
  model),
  **when** the agent loop starts,
  **then** `coreTools` does **not** contain `google_search` (no regression
  for non-Gemini providers).

### Env-var opt-out

- **Given** `PI_NO_GROUNDING=1` is set and the model is Gemini,
  **when** the agent loop starts,
  **then** `coreTools` does **not** contain `google_search`.

- **Given** `PI_NO_GROUNDING=0` is set (or any non-truthy value, or unset)
  and the model is Gemini,
  **when** the agent loop starts,
  **then** `coreTools` contains `google_search` (default behaviour).

- **Given** `PI_NO_GROUNDING=garbage` is set,
  **when** `groundingDisabled()` is called,
  **then** it returns `false` (garbage is not truthy; default is
  permissive).

### TUI and non-TUI parity

- **Given** a user starts the TUI (`pi --model gemini-3.5-flash`),
  **when** the TUI builds the agent,
  **then** `coreTools` contains `google_search`. (Same as the
  non-interactive path.)

### Subagent propagation

- **Given** the main `pi` process has `PI_NO_GROUNDING=1` and spawns a
  subagent via `subagent.Spawner.Spawn(...)` with model `gemini-3.1-flash-lite`,
  **when** the subagent's `pi` process starts,
  **then** the subagent's `coreTools` does **not** contain
  `google_search` (env var is inherited via `FilterEnv` allowlist).

  This is verified by the existing `FilterEnv` allowlist (which lists
  `"PI_"`) plus the same helper running in the child process — no new
  spawner code is required.

## Testing strategy

1. **Unit tests for the helper** — `internal/agent/grounding_test.go`.
   Pure table-driven tests using `t.Setenv` and a fake provider name. No
   network, no ADK runner, no LLM. These are the load-bearing tests.

2. **Compile-time integration check** — a tiny test in
   `internal/agent/grounding_test.go` that asserts
   `geminitool.GoogleSearch{}` satisfies the `tool.Tool` interface and
   reports `Name() == "google_search"`. This catches a hypothetical
   upstream signature change without waiting for a real Gemini call.

3. **Build / vet / unit test** — `go build ./...`, `go vet ./...`,
   `go test ./...`. (Per the `Makefile`.) The existing `internal/agent`
   tests use mock LLMs and should not flake.

4. **No new e2e** — the change is provider-gated and the e2e test
   infrastructure (per `internal/tui/agent_loop_e2e_test.go`,
   `internal/agent/e2e_test.go`) uses mock LLMs. A real Gemini call
   with grounding would be a flake-prone network test and is out of
   scope for this slice; it can be a follow-up behind the
   `e2e` build tag if a real-API check is ever desired.

## Out of scope (deliberate)

- **URL-context and Google Maps grounding.** ADK v2 also ships
  `geminitool.New("url_context", ...)` and
  `geminitool.New("google_maps_grounding", ...)`. The user asked for
  *search* grounding only; these can be added in a follow-up spec.
- **Provider-specific config keys** (`providers.gemini.grounding`,
  `--no-grounding`, role-level overrides). Deliberately omitted per
  requirements; the env var is the only switch.
- **ACP gemini subagent.** External binary; not under pi-go's control.
- **Citations rendering in the TUI.** Gemini returns citations in the
  model response; surfacing them nicely in Bubble Tea is a separate
  UX task.
- **Streaming-event observation of grounding calls.** The grounding tool
  runs server-side in the Gemini backend; pi-go sees only the final
  text+cited response. There is no local tool call to observe.

## Risks

- **Cost / latency surprise.** Users who don't expect grounding may see
  slower responses or, on metered links, higher data use. Mitigated by
  the `PI_NO_GROUNDING` env var and the `docs/articles/003-web-search.md`
  follow-up that should mention Gemini grounding.
- **Gemini-only model with grounding disabled by Gemini itself.** Some
  Gemini model tiers may not support `google_search`; in that case the
  Gemini backend will return a per-request error, not a pi-go panic.
  Acceptable — the failure surfaces as a model error in the run, just
  like any other Gemini API error.
- **Upstream `geminitool` API drift.** A future ADK major version could
  rename or remove `geminitool.GoogleSearch`. The unit tests catch the
  symbol name; a future migration would be a one-line update.
