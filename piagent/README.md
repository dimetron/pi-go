# piagent

Embed pi-go's coding agent in your own Go program.

Every other package in this module is under `internal/`, so `piagent` is the
only importable surface. It assembles the same agent the `pi` CLI runs
headlessly (`--mode print`), minus the terminal UI.

```bash
go get github.com/dimetron/pi-go
```

## The model comes from outside

`piagent` **never constructs a provider**. The model arrives through
`WithModel` as an ADK `model.LLM` — the interface the agent already runs on —
and `New` returns `ErrNoModel` without one. pi-go's providers (credentials,
base URLs, transport options, thinking level, token metering) live in a
separate package:

```go
m, err := pimodels.FromConfig(ctx, "")            // the model a pi session picks
ag, err := piagent.New(ctx, piagent.WithModel(m)) // the agent
```

Neither package imports the other, and `piagent/isolation_test.go` fails the
build if that ever changes. A change to provider handling is therefore not a
breaking change here — and a fake `model.LLM` drives a whole turn in a test
with no network.

## Minimal embed

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/dimetron/pi-go/piagent"
)

func main() {
	ctx := context.Background()

	m, err := pimodels.FromConfig(ctx, "")
	if err != nil {
		log.Fatal(err)
	}

	ag, err := piagent.New(ctx, piagent.WithModel(m))
	if err != nil {
		log.Fatal(err)
	}
	defer ag.Close()

	sessionID, err := ag.NewSession(ctx)
	if err != nil {
		log.Fatal(err)
	}

	answer, err := ag.Ask(ctx, sessionID, "what does this repository do?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(answer)
}
```

## What that one option buys

| Concern | Behaviour |
|---|---|
| Config | Hooks, MCP, A2A, memory, palace and compactor settings from `~/.pi-go/config.json` plus any project override |
| Filesystem | Sandbox rooted at the working directory, plus `~/.pi-go` |
| Tools | read, write, edit, bash (+ supervisor and control tools), grep, find, ls, tree, git tools, session-stats |
| Skills | `.pi-go/skills/`, the user directory and the bundled set, summarized into the prompt |
| Subagents | `.pi-go/agents` plus the bundled set, exposed as tools |
| Project rules | `AGENT.md` / `AGENTS.md` / `CLAUDE.md` / `.pi-go/AGENTS.md`, discovered upward from the working directory |
| Toolsets | MCP servers and A2A agents from configuration |
| Memory | **Off by default** — see below. `WithMemory(true)` opts in |
| Palace | **Off by default.** Once on, tools register only when it holds drawers |
| LSP | Registered only when a language server is installed; two tools by default |
| Sessions | Persisted under `~/.pi-go/sessions`, so `pi --resume` can pick one up |

## Options

```go
ag, err := piagent.New(ctx,
	piagent.WithModel(m),
	piagent.WithWorkingDir("/srv/checkout"),
	piagent.WithExtraInstruction("Answer as a release engineer."),
	piagent.WithLSP(piagent.LSPOff),
	piagent.WithMemory(false),
	piagent.WithPalace(false),
)
```

| Option | Purpose |
|---|---|
| `WithWorkingDir(dir)` | Directory to work in; sandbox root and discovery anchor |
| `WithExtraSandboxDirs(dirs...)` | Grant file access outside the working directory |
| `WithSessionDir(dir)` | Where sessions are persisted |
| `WithModel(m)` | **Required.** The ADK `model.LLM` the agent runs on |
| `WithInstruction(s)` | Replace the built-in system prompt |
| `WithExtraInstruction(s)` | Append your own text to the assembled prompt |
| `WithTools(...)` / `WithToolsets(...)` | Add your own tools |
| `WithBeforeTurn(...)` / `WithAfterTurn(...)` | Bracket whole turns — admission control and metrics |
| `WithBeforeToolCallbacks(...)` / `WithAfterToolCallbacks(...)` | Observe or rewrite tool calls |
| `WithBeforeModelCallbacks(...)` / `WithAfterModelCallbacks(...)` | Observe or rewrite LLM calls |
| `WithLSP(mode)` | `LSPOff`, `LSPMin` (default) or `LSPFull` |
| `WithMemory(bool)` / `WithPalace(bool)` | Opt in to the shared `~/.pi-go` stores (off by default) |
| `WithSkills(bool)` / `WithSubagents(bool)` | Toggle discovery (on by default) |
| `WithAgentEvents(fn)` | Receive subagent and background-bash progress |

## Callbacks

ADK ends its after-tool callback chain at the first callback returning a
non-nil result. pi-go registers several that all return the result map, so a
plain slice would run only the first — a defect that silently disabled the
compactor, dedup, the LSP hook and memory recording for months.

`piagent` composes the whole chain into one ADK callback with explicit
semantics:

```go
func(ctx agent.Context, t tool.Tool, args, result map[string]any, toolErr error) (map[string]any, error)
```

- `(nil, nil)` — leave the result unchanged, continue the chain.
- `(m, nil)` — `m` becomes the result for every later callback and for the model.
- `(_, err)` — abort the chain; the error surfaces on the turn.

Your callbacks run after pi-go's, so they see the compacted and deduplicated
result. Before-tool and model callbacks are handed to ADK as slices, where
"run until one intervenes" is already the semantics you want.

## Testing an embed

`WithModel` takes any `google.golang.org/adk/v2/model.LLM`, so a fake model
drives a whole turn without a network:

```go
ag, err := piagent.New(ctx,
	piagent.WithModel(&fakeLLM{reply: "hello"}),
	piagent.WithWorkingDir(t.TempDir()),
	piagent.WithSessionDir(t.TempDir()),
	piagent.WithMemory(false),
	piagent.WithPalace(false),
	piagent.WithSubagents(false),
	piagent.WithLSP(piagent.LSPOff),
)
```

Nothing wraps the model on the way in — metering, retries and transport belong
to whoever built it.

## Lifetime

`Close()` releases the sandbox, any backgrounded process trees, the subagent
orchestrator, the LSP manager, the memory worker and store, the palace, and the
session log. Always defer it.

## Not included

- **Provider construction, in any form.** No model-name, base-URL, API-key or
  credential option exists here — each would drag provider resolution into this
  package's public surface.
- The CLI's two-stage auto-compaction pre-turn hook. It lives in `internal/cli`
  and would have to be extracted first; install your own via ADK if you need it.
- A process-global HTTP trace sink. A library has no business claiming one.
- The daily-token guardrail, which wraps the model rather than the agent.
- Config-driven shell hooks as a programmatic option. They still load from
  `~/.pi-go/config.json`, but an embedder writing Go gets a Go func rather than
  a subprocess.
- The subagent orchestrator. Subagents are reachable as tools; direct
  orchestration would be a much larger compatibility commitment.

## Turn hooks

Tool and model callbacks fire *inside* a turn. `WithBeforeTurn` and
`WithAfterTurn` bracket the whole thing, which is the level metrics, audit
trails and admission control actually work at.

```go
piagent.New(ctx,
	piagent.WithModel(m),

	// Admission control: returning an error aborts the turn before it
	// reaches the model.
	piagent.WithBeforeTurn(func(ctx context.Context, sessionID, message string) error {
		return budget.Check(sessionID)
	}),

	// Metrics and audit. Cannot change the outcome.
	piagent.WithAfterTurn(func(ctx context.Context, i piagent.TurnInfo) {
		metrics.Record(i.SessionID, i.Duration, i.ToolCalls, i.Err)
	}),
)
```

`TurnInfo` carries `SessionID`, `Message`, `Duration`, `Events`, `ToolCalls`,
`Err` and `Abandoned`.

Three behaviours worth knowing, because each is a bug if you assume otherwise:

- **They fire on iteration, not on the `Run` call.** A sequence nobody ranges
  over is not a turn, and recording one would put a turn that never happened
  into your metrics.
- **An abandoned turn still reports.** Break out of the range loop and the
  after-turn hook still runs, with `Abandoned: true` — a caller that stops
  consuming has still spent the tokens.
- **An aborted turn does not.** If a before-turn hook denies the turn, no
  request is made and there is nothing to report, so the after-turn hook is
  not called.

This is the headless equivalent of pi-go's `turn_complete` lifecycle hook,
which is otherwise dispatched only from the TUI.

## One deliberate difference from the CLI

**Memory and palace are off by default here.** The CLI has both on, and this is
the only place `piagent` departs from it.

They are the only subsystems that *write* to state shared with the user:
`~/.pi-go/memory/claude-mem.db` and `~/.pi-go/palace.db` are the same stores a
real `pi` session reads and writes. An embedder's process is not a `pi`
session, and quietly interleaving its observations with the user's is a
surprise documentation cannot undo. Opt in when your embed is meant to
contribute:

```go
piagent.New(ctx, piagent.WithModel(m), piagent.WithMemory(true))
```

Skills and subagent discovery stay **on**. Those *read* `.pi-go/`, which is the
whole reason to embed this agent rather than write your own. Reading a
convention and writing to someone's store are different things.

## Finding the provider behind a model

Two things need to know which provider a model talks to: the OpenTelemetry
`gen_ai.provider.name` span attribute, and whether Gemini's server-side
grounding tool is worth registering. `piagent` asks the model:

```go
if p, ok := m.(interface{ Provider() string }); ok { ... }
```

The *shape*, never a named type from another package — that is what keeps the
dependency on ADK alone. Models from `pimodels` satisfy it. A model that
cannot answer falls back to a prefix match on its name, and an unrecognized
name gets neither the span attribute nor the grounding tool. Both degrade
quietly; nothing else depends on it.
