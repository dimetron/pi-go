# Research — Gemini ADK Search Grounding integration

## Scope

This is **objective research** (no opinions, no proposals). It captures what is true
about the codebase today and what the ADK v2.0.0 surface looks like, so the design
step can stay focused on decisions and not fact-finding.

Sources: `internal/cli/cli.go`, `internal/cli/interactive.go`, `internal/agent/agent.go`,
`internal/subagent/spawner.go`, `internal/subagent/environ.go`, `internal/provider/gemini.go`,
`internal/provider/provider.go`, `go.mod`, and the `google.golang.org/adk/v2 v2.0.0`
module cache at `~/go/pkg/mod/google.golang.org/adk/v2@v2.0.0/`.

## Finding 1 — The tool to use

`google.golang.org/adk/v2 v2.0.0` (already a direct require) ships
`google.golang.org/adk/v2/tool/geminitool`. Inside it, `GoogleSearch` is a
ready-to-use `tool.Tool` value:

```go
// geminitool/google_search.go
type GoogleSearch struct{}

func (s GoogleSearch) Name() string { return "google_search" }
func (s GoogleSearch) Description() string { return "Performs a Google search to retrieve information from the web." }
func (s GoogleSearch) ProcessRequest(ctx agent.Context, req *model.LLMRequest) error {
    return setTool(req, &genai.Tool{GoogleSearch: &genai.GoogleSearch{}})
}
func (s GoogleSearch) IsLongRunning() bool { return false }
```

- It is a value type, not a pointer. Construct with `geminitool.GoogleSearch{}`.
- Its `ProcessRequest` is a no-op unless the underlying LLM is a Gemini model — the
  genai SDK and the Gemini backend decide what to do with `genai.Tool{GoogleSearch: ...}`.
- Source is the same in `~/go/pkg/mod/google.golang.org/adk/v2@v2.0.0/tool/geminitool/`
  as in `.scratch/adk-v2/tool/geminitool/`. No fork to worry about.

## Finding 2 — Where to add the tool

There are **exactly two** non-test, non-ACP call sites that build a pi-go `agent.Agent`
from the main `pi` binary:

1. `internal/cli/cli.go:655` — `runNonInteractive` path (print / json / rpc modes).
   `coreTools` is in scope. `info provider.Info` is in scope (line 188).
2. `internal/cli/interactive.go:354` — interactive TUI path.
   `coreTools` is in scope. `info provider.Info` is also in scope (defined higher
   up in the same function).

Both call `agent.New(agent.Config{Tools: coreTools, ...})`. There is no other
non-test `agent.New` site that constructs the main pi-go agent loop.

`google_search` belongs in `Tools` (not `Toolsets`). The ADK examples
(`.scratch/adk-v2/examples/a2a/main.go:57`, `examples/web/main.go:80`,
`examples/tools/multipletools/main.go:57`) all put `geminitool.GoogleSearch{}` directly
in the `Tools` slice of `llmagent.New(llmagent.Config{...})`.

## Finding 3 — How to detect "the LLM is Gemini"

`internal/provider/provider.go:63` defines:

```go
type Info struct {
    Provider string
    Model    string
    Ollama   bool
    Custom   bool
}
```

`info.Provider` is the canonical, source-derived value. For Gemini, it is set
to the literal string `"gemini"` by `provider.ResolveWithBaseURL` (line 188 in
cli.go) and is not reassigned afterwards. `cli.go:209` also uses the literal
`info.Provider == "gemini"` as a key check ("no API key needed for provider
gemini / ollama / azure"). So the same check pattern is already established
in this file.

## Finding 4 — Subagents are separate `pi` processes; env vars inherit

This is the **most important finding** for the Q1/Q2/Q3 design.

`internal/subagent/spawner.go:109` shows that subagents are spawned via
`exec.CommandContext(procCtx, s.PiBinary, args...)`. The spawned process is
a fresh `pi` invocation that runs the same `internal/cli/cli.go` code path.
It is **not** an in-process `agent.New` call inside the parent.

The env for the child is set by `cmd.Env = FilterEnv(nil)`
(`spawner.go:112`). `FilterEnv` lives in
`internal/subagent/environ.go:68` and uses the allowlist at
`environ.go:11` (`DefaultEnvAllowlist`). That allowlist **already includes
the `"PI_"` prefix** (line 60: `"PI_", // prefix: PI_SUBAGENT_TIMEOUT_MS,
PI_SUBAGENT_CONCURRENCY, etc.`).

**Consequence:** If I add a `PI_NO_GROUNDING` env var read in `cli.go` /
`interactive.go`, it is automatically inherited by subagent pi processes
without any change to `environ.go` or the spawner. Setting it on the parent
disables grounding for the parent's main loop *and* every subagent process
the parent spawns.

ACP-based subagents (`internal/subagent/spawner_acp.go`) launch external
binaries (`claude`, `gemini` via ACP, `cursor`, `copilot`, `codex`). These
are out of scope for this feature — they do not use ADK's `agent.New` and
their grounding, if any, is the external tool's concern.

## Finding 5 — Existing `PI_` env-var conventions

From `internal/cli/cli.go`, `internal/cli/upgrade.go`, `internal/subagent/timeout.go`,
`internal/acp/server/runtime.go`:

| Var                      | Where read                                | Convention      |
|--------------------------|-------------------------------------------|-----------------|
| `PI_SANDBOX_ROOT`        | `cli.go:249`                              | path            |
| `PI_WORKTREE_ROOT`       | `cli.go:253`, `acp/server/runtime.go:222` | path            |
| `PI_GO_UPDATE_CHECK=0`   | `upgrade.go:27`                           | `=0` to disable |
| `PI_SUBAGENT_TIMEOUT_MS` | `subagent/timeout.go:32`                  | integer ms      |

The `=0`-to-disable pattern from `PI_GO_UPDATE_CHECK` is the closest precedent
for an opt-out env var. `PI_NO_GROUNDING` would be a new convention but is
arguably clearer (explicit `NO_` prefix). Both options exist.

## Finding 6 — Build and test commands

From the `Makefile` at the repo root:

- **build:** `go build -ldflags "..." ./cmd/pi` (also `./cmd/pi-sandbox`)
- **test (unit):** `go test ./...`
- **test (integration):** `go test -tags integration ./...`
- **test (e2e):** `go test -tags e2e ./...`
- **vet:** `go vet ./...`
- **lint:** `golangci-lint run ./...`

No new `go.mod` change is required — `google.golang.org/adk/v2/tool/geminitool`
is a subpackage of the already-direct `adk/v2` module, so `go mod tidy` will not
add anything.

## Finding 7 — ACP gemini subagent (not in scope, but worth flagging)

`internal/acp/client/gemini/gemini.go:116` reads an env var (`envACPGeminiCmd`)
to locate the external `gemini` CLI binary when used as an ACP subagent. The
grounding behaviour of that external binary is whatever that binary does —
not controlled by pi-go. Not part of this spec, but worth a one-line note in
the design doc so reviewers don't ask "what about the `gemini` ACP subagent?".

## Summary of facts

- The tool to add is `geminitool.GoogleSearch{}` — a zero-value struct that
  implements `tool.Tool`.
- The hook point is two call sites: `internal/cli/cli.go:655` and
  `internal/cli/interactive.go:354`, both of which have `coreTools` and
  `info provider.Info` in scope.
- The provider check is `info.Provider == "gemini"` (already used in
  `cli.go:209`).
- Subagents are fresh `pi` subprocesses; they share the same code path, and
  the `"PI_"` env-var prefix is already on the subagent allowlist.
- The conventional project env-var prefix is `PI_`; the closest opt-out
  precedent is `PI_GO_UPDATE_CHECK=0`.
- No `go.mod` change is required.
- Build is `go build ./cmd/pi`; unit test is `go test ./...`.
