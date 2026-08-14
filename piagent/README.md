# piagent

Embed pi-go's coding agent in your own Go program.

Every other package in this module is under `internal/`, so `piagent` is the
only importable surface. It assembles the same agent the `pi` CLI runs
headlessly (`--mode print`), minus the terminal UI.

```bash
go get github.com/dimetron/pi-go
```

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

	ag, err := piagent.New(ctx)
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

`New(ctx)` with no options reads `~/.pi-go/config.json` and any project
override, so it needs a configured model and credential — the same ones the CLI
uses. Nothing else has to be set up.

## What the zero-config path gives you

| Concern | Behaviour |
|---|---|
| Model / provider | Resolved from the `default` role, including base URL, API key and thinking level |
| Filesystem | Sandbox rooted at the working directory, plus `~/.pi-go` |
| Tools | read, write, edit, bash (+ supervisor and control tools), grep, find, ls, tree, git tools, session-stats |
| Skills | `.pi-go/skills/`, the user directory and the bundled set, summarized into the prompt |
| Subagents | `.pi-go/agents` plus the bundled set, exposed as tools |
| Project rules | `AGENT.md` / `AGENTS.md` / `CLAUDE.md` / `.pi-go/AGENTS.md`, discovered upward from the working directory |
| Toolsets | MCP servers and A2A agents from configuration |
| Memory | Observation store, background worker, memory search tools |
| Palace | Opened when it exists; tools registered only when it holds drawers |
| LSP | Registered only when a language server is installed; two tools by default |
| Sessions | Persisted under `~/.pi-go/sessions`, so `pi --resume` can pick one up |

## Options

```go
ag, err := piagent.New(ctx,
	piagent.WithWorkingDir("/srv/checkout"),
	piagent.WithModel("claude-sonnet-5"),
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
| `WithModel(name)` / `WithBaseURL(url)` / `WithAPIKey(key)` | Override provider resolution |
| `WithLLM(llm)` | Supply a ready ADK model, bypassing resolution entirely |
| `WithInstruction(s)` | Replace the built-in system prompt |
| `WithExtraInstruction(s)` | Append your own text to the assembled prompt |
| `WithTools(...)` / `WithToolsets(...)` | Add your own tools |
| `WithBeforeToolCallbacks(...)` / `WithAfterToolCallbacks(...)` | Observe or rewrite tool calls |
| `WithBeforeModelCallbacks(...)` / `WithAfterModelCallbacks(...)` | Observe or rewrite LLM calls |
| `WithLSP(mode)` | `LSPOff`, `LSPMin` (default) or `LSPFull` |
| `WithMemory(bool)` / `WithPalace(bool)` / `WithSkills(bool)` / `WithSubagents(bool)` | Toggle optional subsystems |
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

`WithLLM` takes any `google.golang.org/adk/v2/model.LLM`, so a fake model drives
a whole turn without a network:

```go
ag, err := piagent.New(ctx,
	piagent.WithLLM(&fakeLLM{reply: "hello"}),
	piagent.WithWorkingDir(t.TempDir()),
	piagent.WithSessionDir(t.TempDir()),
	piagent.WithMemory(false),
	piagent.WithPalace(false),
	piagent.WithSubagents(false),
	piagent.WithLSP(piagent.LSPOff),
)
```

An injected model is not wrapped in the daily-token guardrail: metering someone
else's model is their decision.

## Lifetime

`Close()` releases the sandbox, any backgrounded process trees, the subagent
orchestrator, the LSP manager, the memory worker and store, the palace, and the
session log. Always defer it.

## Not included

- The CLI's two-stage auto-compaction pre-turn hook. It lives in `internal/cli`
  and would have to be extracted first; install your own via ADK if you need it.
- A process-global HTTP trace sink. A library has no business claiming one.
- The subagent orchestrator is not exposed. Subagents are reachable as tools;
  direct orchestration would be a much larger compatibility commitment.
