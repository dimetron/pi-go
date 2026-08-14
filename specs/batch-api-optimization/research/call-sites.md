# Where pi-go calls a model, and which calls could tolerate 24 hours

A batch API is only useful for work whose result nobody is waiting for. This is
the complete inventory, read from the code on 2026-08-14 at `c98d55f`.

## The inventory

| # | Site | Trigger | Who waits | Batchable |
|---|------|---------|-----------|-----------|
| 1 | Main agent turn (`internal/agent/agent.go`, via ADK `Flow`) | Every user message and every tool result | The user, live | **No** |
| 2 | `internal/session/compaction.go:157` `LLMSummarizer` | Context reaches `SummarizePercent` (88–90%) of the window | The user, mid-turn | **No** |
| 3 | `internal/tools/subagent.go:224,365,506` `SpawnWithInput` | `subagent` tool call | The parent turn | **No** |
| 4 | `internal/tui/commit.go:268` | `/commit` — commit message generation | The user, live | **No** |
| 5 | `internal/tui/ping.go:76` | `pi ping` — connectivity check | The user, live | **No** |
| 6 | `internal/memory/compress.go:46` `CompressObservation` | Every recorded tool observation | **Nobody** | **Yes, in principle** |
| 7 | `internal/memory/compress.go:169` `SummarizeSession` | — **no production caller** | — | Moot |
| 8 | `internal/eval/judge.go:96` `Judge` | LLM-as-judge over a run report | The eval run | Marginal |

That is the whole list. Sites 1–5 are ruled out by definition: a request that
returns in up to 24 hours cannot serve a turn the user is watching.

### C1 — The compressor is a child `pi` process, and that is the real cost

`SubagentCompressor.CompressObservation` (`internal/memory/compress.go:36`)
calls `orchestrator.Spawn`. The dispatch at
`internal/subagent/orchestrator.go:466-470` reads:

> "ACP-bundled agents (claude/gemini/cursor/copilot) launch their own CLI
> binary via the ACP adapter; codex agents launch `codex app-server` … **everyone
> else runs as a child `pi --mode json`**."

`memory-compressor` is in the "everyone else" branch, and the child is a real
process: `spawnArgs` builds `{"--mode", "json", …}`
(`internal/subagent/spawner.go:110`) and `Spawn` runs
`exec.CommandContext(procCtx, s.PiBinary, args...)`
(`internal/subagent/spawner.go:145`). Each compression is a full `pi` boot, not
a bare model call.

The agent definition (`internal/subagent/bundled/memory-compressor.md`):

```yaml
name: memory-compressor
role: smol
worktree: false
tools: []
timeout: 600000
```

Two of those five lines do not do what they appear to do.

**`tools: []` is inert.** `AgentConfig.Tools` is populated from frontmatter at
`internal/subagent/agents.go:142` and **never read again anywhere in the repo**:

```
$ grep -rn "\.Tools" --include='*.go' internal/subagent/ | grep -v _test
internal/subagent/agents.go:142:   cfg.Tools = append(cfg.Tools, t)
```

`SpawnOpts` (`internal/subagent/orchestrator.go:446-458`) carries `Model`,
`WorkDir`, `Prompt`, `Instruction`, `Timeout`, `Env`, `BaseURL` and `LSP` — no
tool list. The child therefore boots with pi's **default full toolset and full
system prompt**. The per-observation request is not the 4 KB the payload cap
suggests.

**`role: smol` resolves to the frontier model.** `ResolveRole`
(`internal/config/config.go:186`) falls back to `Roles["default"]` when the
named role is absent, and `Defaults()` (`internal/config/config.go:165`) ships
exactly one role:

```go
Roles: map[string]RoleConfig{
    "default": {Model: "gpt-5.6-sol"},
},
```

There is no `smol` role out of the box, and none in this machine's
`$HOME/.pi-go/config.json` either. **On shipped defaults the memory compressor
runs on `gpt-5.6-sol` at $5/$30 per MTok** — the frontier model, once per tool
call, in a child process carrying the full system prompt.

**`memory.compression_model_role` is dead code.** The config key exists
(`internal/config/config.go:37`) and defaults to `"smol"`
(`internal/config/config.go:48`), but nothing reads it:

```
$ grep -rn "CompressionRole" --include='*.go' . | grep -v _test
internal/config/config.go:37:  CompressionRole  string `json:"compression_model_role,omitempty"`
internal/config/config.go:48:  CompressionRole: "smol",
```

The role actually used comes from the agent's own frontmatter, not from config.

Prompt payload, for what it is worth: `buildCompressionPrompt`
(`internal/memory/compress.go:75-92`) marshals `tool_name`, `tool_input` and
`tool_output`. Only `tool_output` is truncated, at `maxPromptOutput = 4096`
bytes (`internal/memory/compress.go:14,81`). **`tool_input` is not truncated at
all**, so a `write` or `edit` with a large `content` argument goes in whole.

Delivery shape, which bounds how much any of this can cost at once: `Enqueue`
is non-blocking and **drops** when the channel is full
(`internal/memory/worker.go:55-64`, buffer = `max_pending_observations`,
default 100). `Start` drains with a **single goroutine**
(`internal/memory/worker.go:67-76`), so compressions are serial — one child
process at a time — and the orchestrator pool it competes for is
`DefaultPoolSize = 3` (`internal/subagent/orchestrator.go:26`), **shared with
user-facing subagents**. Shutdown gives the drain 5 seconds
(`internal/cli/cli.go:975-979`); anything still queued is discarded. There are
no retries — `compress.go:46` calls `Spawn`, not `SpawnWithRetry`.

**This site is already scheduled for repair.** `specs/memory-fixes` R4 requires
that compression "does not spawn a `pi` child process per tool call", moves it
in-process against the resolved `smol` role, and requires that "if no `smol`
role is configured, the fallback must be logged once per session, **not
silently billed at frontier prices**". That last clause is exactly the defect
above, and R4 was written before it was traced to `ResolveRole`.

Whatever a batch API could save here, it saves on the *residue* after R4 — and
R4 is worth roughly 40× more than the batching (`design.md` § D1).

**And today it runs zero times** — see `research/measurements.md` § M7. One
observation exists in the database, written during the memory-fixes work itself.

### C2 — `SummarizeSession` has no caller

`grep -rn SummarizeSession --include='*.go' .` returns exactly two hits, both
the definition and its doc comment at `internal/memory/compress.go:160-161`,
plus tests. Nothing in production invokes it. It cannot be optimised because it
does not run.

This contradicts the framing that session summarisation is a live batch
candidate. It is a batch candidate for code that would have to be written first.

### C3 — Palace KG extraction makes no LLM call at all

`internal/palace/tool_kg_extract.go` was listed as a batch candidate. It is not
one, and the file says so itself (`internal/palace/tool_kg_extract.go:25-30`):

> "The tool is heuristic: **it does not call an LLM. That is deliberate** — the
> extraction runs in the hot path of tool use and would otherwise add an extra
> model call per observation. The agent already has a model; it can reject bad
> candidates."

Triple extraction is pure heuristics —
`extractTriples` (line 178) matches Go-style function declarations with regexes,
`emitImport` (line 114) parses import paths, `isStdlibImport` (line 340) and
`isCommonWord` (line 375) filter with static lists.

```
$ grep -ln "Spawn\|GenerateContent\|LookupAgent\|llm\." internal/palace/*.go | grep -v _test
(no output)
```

**No file in `internal/palace` calls a model.** The miners
(`miner.go`, `miner_convo.go`, `miner_project.go`) are heuristic too. Embeddings
go through `embedder_ollama.go` (local) or the ONNX/Go backends
(`embedder_backend_ort.go`, `embedder_backend_go.go`) — all local inference,
where batch pricing does not exist.

The palace has nothing to batch. This is the clearest correction in the spec.

### C4 — Eval is real but rare

`internal/eval/judge.go:96` `Judge(ctx, complete, model, r, digest)` takes a
`CompleteFunc`, so it is provider-agnostic. Its only caller in the tree is
`internal/tui/run_eval_e2e_test.go:268` — a build-tagged e2e test. There is no
`pi eval` CLI command.

One judge call per run report. Even a large eval sweep is hundreds of calls, not
thousands, and a sweep is exactly the shape batch APIs were designed for
(submit N, collect later). But the volume does not currently justify the
machinery, and the harness that would generate that volume
(`specs/features/…/eval-run-harness`) is not merged.

### C5 — Auto-compaction cannot be batched, and is worth naming

`internal/session/compaction.go` documents its own cost model:

> "with an LLM-written handoff summary. **This invalidates the prompt cache**" —
> `internal/session/compaction.go:26`

Compaction fires at `SummarizePercent` (88%, "88 leaves headroom for the
summarization call itself", line 67) and the user is blocked on it. It is not a
batch candidate. It is listed because it is the one background-*feeling* LLM
call that is in fact synchronous and on the critical path, and because it
discards the prompt cache — a cost worth its own investigation, outside this
spec's scope.

## What this means for direction 1

Of eight model call sites, five are user-blocking, one is dead code, one has no
LLM call at all, and one (eval) is rare. The single genuine candidate — the
memory compressor — currently issues **zero** requests per day and is already
being rebuilt by another spec.

There is no meaningful batch-API opportunity in pi-go today. See
`design.md` § D1 for the quantified version of that claim and the conditions
under which it would change.

## Direction 2: the plumbing already exists

The premise of direction 2 was that batching tool calls needs new machinery. It
does not.

**ADK executes multiple function calls concurrently.**
`internal/llminternal/base_flow.go:1063` `handleFunctionCalls` builds one task
per call and hands the slice to the platform task runner:

```go
fnResponseEvents := make([]*session.Event, len(fnCalls))

// Tool calls run via the context's task runner: concurrent goroutines by
// default, or a caller-installed runner (platform.WithTaskRunner).
tasks := make([]func(context.Context), len(fnCalls))
```

The default runner (`platform/exec.go:68` `RunTasks`) fans out one goroutine per
task and joins on a `sync.WaitGroup`. **pi-go installs no custom runner** —
`grep -rn "WithTaskRunner\|TaskRunner" --include='*.go' .` returns nothing in
this repo — so the concurrent default applies.

Results are written to `fnResponseEvents[i]` by index, so response ordering is
positional and deterministic regardless of completion order. Each call gets its
own span, its own error path, and its own `functionResponse` part.

**The providers already surface multiple calls.** Not asserted — measured:
responses carrying 2, 3, 4, 5 and 6 function calls all appear in the session
logs (`research/measurements.md` § M3), against the OpenAI provider. 25.7% of
responses already carry more than one call.

**The system prompt already permits it, weakly.** `internal/agent/agent.go:185`:

```
# Parallel execution

You can call multiple tools in a single response when they are independent. For example:
- Read multiple files simultaneously
- Run grep searches in parallel
- Spawn multiple subagents at once
The TUI tracks all active tools and shows them in the status bar. Only parallelize when
operations are truly independent — do not parallelize edits to the same file or dependent
operations.
```

Note the asymmetry with the subagent guidance 44 lines later
(`internal/agent/agent.go:235`), which is emphatic:

```
- **Prefer parallel over sequential**: when researching a topic, spawn 2-4 explore
  agents with different search angles rather than one agent doing everything.
```

Tool batching gets "you can". Subagent batching gets "**Prefer**". Neither
section gives the model a reason, and the measured behaviour — 1.307 calls per
response, 71.3% of responses carrying exactly one — matches the weaker
instruction.

**Conclusion:** direction 2 needs no new tool, no provider work and no ADK
change. It is a prompt problem and a measurement problem.
