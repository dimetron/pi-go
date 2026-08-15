# DeepSeek Harness vs pi-go — Deep Comparative Review

> **Reviewed**: `tmp/harness/deepseek-harness` @ `47f943859b` (2026-08-13, `v0.1.0-rc.5`, `github.com/deepseek-ai/deepseek-harness`)
> **Against**: `pi-go` @ `05f5fe8` (2026-08-14, `main`)
> **Date**: 2026-08-15
> **Method**: read of both repos' architecture/convention docs, generated catalogs, subsystem docs, CI configs, and targeted source reads of the loop, tool pipeline, permission/sandbox, session, provider, subagent, and test layers. Line counts measured with `find | wc -l`, not estimated.

---

## 0. Executive summary

DeepSeek Harness (`dsh`) and pi-go solve the same problem — a coding-agent runtime — from opposite ends.

**dsh is a runtime-composition framework first, a product second.** Everything is a Cordis plugin: the model adapter, the tool registry, the session log, and even the agent loop. There is no privileged core to patch. Its investment is overwhelmingly in *seams, invariants, and gates*: 226 npm packages, a generated config/tool/persistence/module catalog set, a per-file **100% coverage gate**, keyless snapshot replay of the fully assembled app, and 1,372 design records under `.agents/notes/`.

**pi-go is a product first, a framework second.** It is a single Go module with 29 `internal/` packages on top of Google ADK Go. Its investment is in *reach and ergonomics*: 8 LLM provider families, a 16k-line Bubble Tea TUI, semantic memory with local embeddings, ATIF trajectory export, an eval harness with an LLM judge, and the ability to delegate work to Claude Code / Codex / Gemini / Cursor / Copilot as subagents.

The single most important finding: **pi-go has no tool-execution approval gate and no per-call sandbox.** dsh treats "may this action proceed?" as a first-class, fail-closed, logged, replayable seam with three OS backends. pi-go's file tools are confined by `os.Root`, but `bash` is not confined at all, hooks cannot deny a call, and ACP permission requests are auto-approved by policy (`internal/acp/permissions.go`). For a tool that writes files and runs shell commands on a developer's machine, that is the gap that matters most.

The second most important: **pi-go cannot replay a model transcript in tests.** dsh's `llm-replay` + snapshot tier boots the real assembled application against a recorded session and diffs normalized output. pi-go's e2e tests either mock at the wrong level or need a live API key, so the "green unit tests, broken product" class of bug is undetected.

Both gaps are properties of what pi-go built, not consequences of Go or of ADK.

---

## 1. At a glance

| | DeepSeek Harness | pi-go |
|---|---|---|
| Language / runtime | TypeScript (ESM), Node ^22.19 \|\| >=24 | Go 1.26 |
| Composition unit | 226 npm workspace packages + 12 vendored | 29 `internal/` packages, 1 module |
| Framework | Vendored **Cordis** (own plugin/DI/event runtime) | **Google ADK Go** (third-party agent framework) |
| Product source LOC | ~242k TS/TSX | ~69k Go |
| Test LOC | ~278k (115% of source) | ~129k (186% of source) |
| Test files | 759 `.spec.ts` / `.e2e.ts` | 388 `_test.go` |
| Coverage gate | **per-file 100%** on `packages/*/*/src` | Codecov auto target (`codecov.yml` ignores `cmd/`) |
| Model-facing tools | **52** (generated catalog) | ~25 core + LSP + memory + MCP |
| LLM providers | DeepSeek native + `pi-ai` multi-provider lib + replay | **8 families**: Anthropic, OpenAI (completions/responses/codex/azure), Gemini, Ollama, Mistral, xAI, opencode |
| UI surfaces | React web app, ACP, headless, JSON-RPC SDK | **Bubble Tea TUI**, web server, ACP server, JSON-RPC over unix socket, print/json modes |
| Docs | 215 md files, bilingual EN/ZH, 4 generated catalogs | `ARCHITECTURE.md` + `docs/` + 347 spec md files |
| Design records | 1,372 Agent Notes (`proposed`/`implemented`/`archived`/`rejected`) | 347 files under `specs/` |
| CI lanes | Linux, Windows native, Windows/Wine, Node 22/24/26, Python, browser snapshot, serial | **ubuntu-latest only**, 4 jobs |
| Largest source file | 4,751 (generated catalog) | 2,527 (`internal/tui/tui.go`) |

---

## 2. Architecture and extensibility

### dsh: capability seams over a plugin tree

The organizing idea is the **capability seam** — a triple of *Service Definition* (interface), *Service Provider* (implementation), *Consumer* (usually a model-facing tool). A seam is never one role; adding a capability means designing all three. The payoff is stated plainly in `docs/architecture.md`:

> Filesystem and subprocess providers share one execution world, so pointing them at a remote sandbox moves Bash, PTY, and LSP with them, with no provider forks.

A running `dsh` is a plugin tree composed at boot from ordered layers: bundles → profile patch → home patch → `--patch` overlay. `dsh --profile web --dump-config` prints the tree, and any row can be replaced by a patch. That means the *deployment*, not the code, decides whether bash runs locally, in a sandbox, or in an E2B microVM.

Extension points are documented as a table ("Where new behavior goes"), and the convention is enforced socially and mechanically: *"Plugins, not loop changes — changing `agent-loop` requires updating `docs/architecture.md`."*

### pi-go: a layered Go program over ADK

pi-go's structure is a conventional Go dependency graph (`ARCHITECTURE.md`): `cli` wires `agent`, `provider`, `tools`, `extension`, `session`, `tui`, `subagent`, `lsp`. Extensibility is real but *fixed-shape*: you extend by adding a tool to `CoreTools()`, an ADK callback, an MCP server, a hook, or a skill. Swapping the filesystem or shell implementation would require editing `internal/tools`.

This is not automatically worse. pi-go's approach costs far less to reason about, compiles to one static binary, and its `piagent`/`pimodels` public packages give a genuinely clean embedding story with an enforced isolation test (`piagent/isolation_test.go` fails the build if `piagent` ever imports `pimodels`). That is exactly the kind of mechanically-enforced boundary dsh advocates, applied where pi-go actually needed it.

**Where pi-go pays for it:** there is no seam between "the agent wants to run a process" and "a process runs on this host". Adding remote/sandboxed execution today means touching `bash.go`, `bash_supervisor.go`, the LSP manager, and the subagent spawner independently.

**Documentation drift:** `ARCHITECTURE.md` is stale. It lists 17 packages, one of which — `internal/rpc/` — does not exist under that name (actual: `pirpc`), and omits `acp`, `atif`, `browser`, `codex`, `eval`, `httplog`, `jsonrpc`, `otel`, `palace`, `procs`, `retry`, `webserver` — 12 of 29 packages. dsh solves exactly this with generated catalogs (`gen-module-graph.ts`, `gen-doc-graphs.ts`) plus a `verify-doc-refs` gate.

---

## 3. Area-by-area comparison

### 3.1 Agent loop and turn model

| | dsh | pi-go |
|---|---|---|
| Loop ownership | Own `agent-loop` plugin, replaceable | ADK `runner.Runner` |
| Turn/step vocabulary | Explicit: turn = 0..n steps; `turn/start`, `step/start`, `step/end`, `turn/end` are durable events | Implicit in ADK event stream |
| Interception points | `agent/pre-step` (waterfall, can reject or rewrite messages), `agent/request`, `llm/stream`, `agent/request-error`, `agent/turn-stopping` | ADK `Before/AfterModelCallback`, `Before/AfterToolCallback` |
| Mid-turn steering | One inbox with claim semantics; injected context waits until another message wakes the driver | `pendingPrompts` queue in TUI, delivered after the turn |
| Cancellation | `AbortSignal` threaded to approval, tools, streams | `context.CancelFunc` via `m.cancelAgent()`, Esc/Ctrl+C |

dsh's loop is documented to the sequence-diagram level (`docs/agent-lifecycle.md`) and the diagram is *generated*, so it cannot drift. The `agent/pre-step` waterfall is where compaction hooks in — pressure is evaluated *before* request derivation, and `agent/request-error` catches canonical context overflow and opens a fresh retry turn only if pruning actually advanced the surface. That is a considerably more careful recovery story than pi-go's `internal/session/autocompact.go` + provider retry.

pi-go's callbacks are flat `[]llmagent.BeforeToolCallback` slices with **no documented ordering contract** and no ability for one callback to see or modify another's decision. In `internal/agent/agent.go` and the TUI init path, ordering is whatever the append order happens to be. That is fine today and will not stay fine.

**Concrete gap:** pi-go has no equivalent of `agent/turn-stopping` — a serial terminal checkpoint where a plugin can say "not done yet, continue". `internal/sop` and `/run` implement continuation ad hoc in the TUI.

### 3.2 Session log and persistence

dsh's central invariant is stated as a rule and asserted at runtime:

> **Model-visible ⟺ logged.** Anything that reaches a model request must be reconstructable from the session log.

Consequences: `deriveMessages()` projects model history from the log; raw `assistant/chunk` events preserve replay and UI fidelity; fork, resume, transcripts, telemetry, and persistence all derive from one stream. Format changes bump `SESSION_FORMAT_VERSION`; SQLite uses a monotonic `SCHEMA_VERSION`; a build that does not know an event type **refuses the log** unless the envelope carries `ignorable: true`.

pi-go's `internal/session/store.go` writes `meta.json` + `events.jsonl` per session. Comparing honestly:

- **No format version.** `Meta` (store.go:48) has `id`, `appName`, `userID`, `workDir`, `model`, `provider`, `baseURL`, `title`, timestamps, `host`, `planContext` — and no `schemaVersion`. A future layout change has no safe migration signal. `HostEnv` capture is a genuinely good idea dsh does not have.
- **The system prompt is not logged.** `internal/agent/agent.go` rebuilds the instruction at runtime from `AGENTS.md` discovery + skills menu + memory context injection. Resume a session after editing `AGENTS.md` or adding a skill and the model sees a *different* prompt than the transcript implies. dsh's rule exists precisely to prevent this class of silent divergence.
- **No projection layer.** dsh has `session-projection` (host folds events once, clients receive finished values) and `session-query` (cross-session search, tracing, FTS via SQLite provider, exposed to the model as 5 `session_*` tools). pi-go has `session-stats` and `session_sweep` tools and the TUI reads events directly.

**pi-go's counter-strength:** ATIF v1.6 export (`internal/atif`) is a standardized cross-tool trajectory format. dsh has nothing equivalent — its log is proprietary and explicitly carries "no compatibility promise" pre-release.

### 3.3 Tool system — the pipeline

This is where the design distance is largest.

dsh's `docs/tool-execution-pipeline.md` (generated) shows the ordered path every call takes:

```
tool/call logged  →  presentCall (UI pending card)
  →  tools/pre-execute waterfall   (hooks, permission, sandbox — may deny or ask)
  →  ctx.approval one-shot prompt  (absent/unanswerable ⇒ deny)
  →  registered monotonic guards   (deny or abstain; identity protected)
  →  tools/execute waterfall       (timeout, retry, metrics — around dispatch)
  →  tool execute() body           (fs/write-intent, fs/edit-intent gates below tool-fs)
  →  tools/post-execute waterfall  (accept, block, replace, add context)
  →  registry normalization        (throws become isError, losslessly snapshotted)
  →  finalizeContent               (content-only invariant)
  →  tools/result                  (frozen authoritative outcome)
  →  tool/result logged            →  presentResult (UI completed card)
```

Every `ToolDefinition` also declares: a **canonical output JSON Schema** validated on every success, a pure `render(args, value) → ContentBlock[]` projection, `timeoutMs`, `isConcurrencySafe`, and UI presenters. The registry's `schemas()` uses an **explicit allowlist** so host-only fields can never leak into a model request. Tool *render intent* (`generic`/`terminal`/`diff`, `locations`) is a design decision made up front, per `AGENTS.md`.

pi-go's pipeline is:

```
ADK dispatch → BeforeToolCallback[] (fire-and-forget) → tool.Run() → AfterToolCallback[]
```

with the after-callbacks doing real work: `BuildCompactorCallback` (tool-output compaction), `BuildDedupCallback` (elides byte-identical repeats), LSP format/diagnostics, memory capture, OTel spans, ACP tool-call reporting.

What pi-go has that is genuinely good here:

- **`internal/tools/compactor.go`** — a configurable, per-tool-family output compaction pipeline (test aggregation, build-error filtering, git compaction, linter aggregation, search grouping, smart truncation) with 17 tunables and a metrics counter. dsh has `output-retention` + `spill` but nothing this domain-aware.
- **`internal/tools/ledger.go`** — the `ReadLedger`, which tracks *what the agent has actually seen of each file*, distinguishing a partial window from a full read, so an overwrite refusal can say "you have read 2000 of 5000 lines" rather than a blanket "you have not read this file". This is better-reasoned than dsh's `fs/*` read-before-edit gate, and the comment explaining why is the best comment in the repo.
- **`internal/tools/registry.go`'s `coercingTool`** — argument aliasing and type coercion for models that emit `"3"` instead of `3`. `ISSUES.md` documents 253 + 201 + 138 schema-validation failures over 30 days that this addresses. dsh trusts strict schemas and lets the model retry.

What pi-go is missing:

1. **No canonical output contract.** Tools return `map[string]any`; nothing validates the shape. dsh validates every successful value against the tool's declared schema.
2. **No generic timeout enforcement.** `bash` has its own, git tools hardcode 10s, `read_image` hardcodes a fetch timeout — but a hanging MCP tool or LSP call has no backstop. dsh's `dsh-tool-call-timeout-policy` is a single zero-config `tools/execute` wrapper reading each tool's declared `timeoutMs`.
3. **No spill.** Oversized output is truncated/compacted and the excess is gone. dsh's `spill` seam persists the full text and returns a locator plus retrieval guidance, so the model can go get the part it needs.
4. **No concurrency declaration.** dsh classifies calls by `executionMode` and runs a bounded rolling pool with barriers, reclassifying before start. pi-go executes tool calls as ADK schedules them.
5. **No loop-hygiene guard.** dsh's `repeat-tool-reminder` counts consecutive identical `(tool, canonical args)` calls per-agent (`WeakMap<Agent, Chain>`), injects escalating advisories at configured thresholds, counts *denied* calls, and makes excluded bookkeeping tools transparent to the chain so `grep X → todo_write → grep X` still reads as two consecutive `grep X`. pi-go's `dedup.go` elides duplicate *results* but never tells the model it is looping.

### 3.4 Permissions, approval, and sandboxing — **the critical gap**

**dsh** treats this as three composable, independently-configurable layers:

- **`ctx.approval`** (`docs/subsystems/approval.md`) — a fail-closed seam. `ApprovalOutcome` is a closed union: `allowed-once | rejected | cancelled | unavailable`. `allowed-once` is the *only* grant. A missing, non-owning, throwing, or non-conforming answerer becomes `unavailable`, and callers deny. Policy is `ask | never`; `never` is enforced *inside the service before waterfall dispatch* so a later-registered `prepend` answerer cannot bypass it. Every ask appends a paired `approval/asked` / `approval/decided` audit event, and a failure to commit either append rejects the request rather than returning an unlogged decision. The request deliberately omits tool arguments so the UI attaches the prompt to the already-streamed tool call instead of rendering a second copy that could drift.
- **`ctx.sandbox`** (`docs/subsystems/sandbox.md`) — per-call file-effect policy with `read-only | workspace-write | danger-full-access`. Backends: Linux bwrap/Landlock, macOS Seatbelt, Windows ACL restricted token. Critically, **enforcement completeness is a reported fact**: `full | partial`, where `partial` means an older Landlock ABI or Windows Everyone/hard-link boundaries govern only a subset — consumers requiring an absolute boundary must reject or surface the distinction. The workspace root is canonicalized with filesystem semantics *before* lexical normalization, so a cwd containing `symlink/..` identifies where the process actually runs.
- **`ctx.permissionPresets`** — bundles the two knobs into one named UI selector, owning **no enforcement**; a preset switch only writes through each knob's canonical setter, and the current preset is *derived* from the knobs (falling back to `custom`), not from its own event.

**pi-go** has:

- `internal/tools/sandbox.go` — `os.Root`-based path confinement for `read`/`write`/`edit`/`ls`/`tree`/`find`/`grep`. Real and correct as far as it goes: `..` and symlink escapes are blocked at the syscall level.
- `cmd/pi-sandbox` — a **macOS-only, opt-in, whole-process** Seatbelt wrapper (`pi-profile.sb`) that launches the entire `pi` binary confined and tails denial logs. Not per-call, not per-tool, no Linux or Windows backend, no enforcement-completeness reporting.
- `internal/guardrail` — daily *token* limits only. Not a permission system.
- `internal/acp/permissions.go` — **auto-approves** every permission request from ACP subagents, preferring `allow_always` > `allow_once`, and falls back to selecting the *first option* if no allow-kind exists so the turn can proceed.

The gaps in order of severity:

1. **`bash` is unconfined.** It runs with the sandbox's cwd but nothing restricts what the command touches. `rm -rf ~`, `curl … | sh`, and writes anywhere on the filesystem all succeed. `internal/tools/bash.go` has no denylist, no allowlist, no approval.
2. **No approval gate anywhere.** There is no code path in which pi-go asks the user before executing a tool call. `grep -rn "approve|Approve|confirm" internal/tui/` returns only commit-message and skill-overwrite confirmations.
3. **Hooks cannot deny.** `BuildBeforeToolCallbacks` (`internal/extension/hooks.go:141`) always returns `(nil, nil)` and swallows hook errors with a log line and the comment `// Non-fatal: log and continue`. This is a documented deviation from Claude Code hook semantics, where a non-zero exit blocks the call — and it means a user's existing hook-based safety net is silently inert under pi-go.
4. **`AutoApproveOutcome`'s fallback is fail-open.** The final branch selects `req.Options[0]` — whatever it is, including a deny option's neighbour — "so the turn can proceed rather than stalling". dsh's equivalent rule is the opposite: an unanswerable request is `unavailable`, and `unavailable` denies.

The rationale comment in `permissions.go` ("already been authorized at the parent process boundary") is reasonable *for ACP subagents specifically*. The problem is that no such boundary exists for the main agent.

### 3.5 Hooks and interception

dsh splits hooks into a dialect-agnostic `hook-protocol` (matcher, exit-code/stdout codec, `ctx.shell` execution, **most-restrictive merge**, `hook/invoked` + `hook/result` session events) and two dialect bridges: `hooks-claude-code` (with `${CLAUDE_PLUGIN_ROOT}` / `${CLAUDE_PROJECT_DIR}` substitution, CC's 600s default timeout, stderr summary caps) and `hooks-codex`. Hook outcomes map onto **typed Decisions** that the `tools/pre-execute` waterfall honours. The README is explicit that the bridge exists only as a compatibility path — anything bespoke should be a native plugin.

pi-go's `HookConfig` is `{event, command, tools?}` with `before_tool` / `after_tool`, JSON on stdin, output discarded, errors logged. There is no decision protocol, no timeout, no per-hook matcher beyond a tool-name list, and no session event recording that a hook ran.

The `hookLog` indirection (`SetHookLogger`, `internal/extension/hooks.go:114`) is good engineering — it exists because writing to stderr from a callback goroutine paints over the TUI's alternate screen and the renderer never learns those cells were dirtied. That comment documents a non-obvious constraint at the level dsh's subsystem docs do, and it is the exception rather than the rule in pi-go.

### 3.6 Subagents and multi-agent work

| | dsh | pi-go |
|---|---|---|
| Model | `ctx.subagent` seam, providers vary from in-process child agent to delegated turn in another product | Process spawner + orchestrator + pool |
| Isolation | Provider's concern | **Git worktree per agent** (`internal/subagent/worktree.go`) |
| Concurrency | Provider-defined | Pool with configurable budget |
| Live control | `interrupt_agent`, `list_agents`, `send_message`, `report` tools | None — calls block until done |
| Composition | `workflow` tool: model-written orchestration **script** run in a `worker_threads` engine that starts subagents | `subagent` tool with `single` / `parallel` (≤8) / `chain` (≤8, `{previous}` substitution) modes |
| External agents | — | **Claude Code, Codex, Gemini, Cursor, Copilot** via ACP/native protocols |
| Timeouts | Provider | Absolute (10m) + **inactivity** timeout |

pi-go wins decisively on *heterogeneous delegation*: `internal/acp/client/{claudecode,gemini,cursor,copilot}` plus `internal/codex` mean a pi-go session can hand a task to a different vendor's agent and stream its events back. 17 bundled agent definitions in `internal/subagent/bundled/`. dsh has nothing comparable — its subagents are all `dsh`.

pi-go's worktree isolation (with the stash/pop dance in `WorktreeManager.Create` so `worktree add` succeeds on a dirty tree) is a concrete, well-documented mechanism dsh lacks.

dsh wins on *live orchestration*. Its four agent-mesh tools plus `workflow` let the model write a deterministic orchestration script — fan-out, barriers, loop-until-dry — and interrupt or message running agents. pi-go's `chain` mode with `{previous}` string substitution is a much weaker version of this, and once a `parallel` batch is launched the model cannot steer it.

The inactivity timeout with the comment explaining why the absolute timeout was raised from 5 minutes ("a review across a day's commits legitimately runs longer than that, and cutting it off discards everything it had produced") is exactly the kind of operational learning that belongs in code, and dsh's provider-delegated timeouts record no equivalent.

### 3.7 Context management

| Mechanism | dsh | pi-go |
|---|---|---|
| Summary compaction | `ctx.compaction` seam; summary rides on a `user/message` with `surfaceOp: {op:'replace', start, end}`; three log-only `compaction/*` events record lock, range, shadowed seqs, token count, model call | `internal/session/compaction.go` + `autocompact.go` |
| Trigger | `agent/pre-step` pressure **and** `agent/request-error` on canonical overflow | Threshold on token count |
| Pre-summary pruning | `ctx.toolResultPruner` — model-free tool-result pruning runs before summary selection | `compaction_shed.go` |
| Token measurement | `ctx.tokenMeter` — detached replay snapshot with `logRevision`, positional surface pricing | Provider usage counters, `guardrail` daily totals |
| Oversized output | `spill` seam → locator + retrieval hint | Truncate/compact, excess discarded |
| Per-tool output shaping | `output-retention` | **`compactor.go`, 9 stages, 17 tunables** |

pi-go's per-tool compaction is more sophisticated than dsh's; dsh's *lifecycle* around compaction is more sophisticated than pi-go's. The two are complementary and neither blocks the other.

The one real pi-go deficiency is **spill**: discarding output the model might need is a correctness issue, not just an efficiency one. A test run that emits 200 failures gets aggregated to 10 and the other 190 are unrecoverable.

### 3.8 Providers and LLM layer

pi-go is far ahead. `internal/provider/` covers Anthropic (with explicit prompt-caching support in `anthropic_caching.go` and beta headers), OpenAI completions + Responses API + Codex + Azure, Gemini, Ollama (with `think` support and sampling), Mistral, xAI (with server-side tools), and opencode — plus a model catalog (`model_catalog.go`, `modeldata/`), stream cancellation paths, retry with transient classification, and a trace transport.

dsh ships a native DeepSeek adapter, delegates everything else to the `@earendil-works/pi-ai` library behind one `llm-pi-ai` plugin, and adds `llm-retry` and `llm-replay`. Its *seam* is better (one `ctx.llm` registry, `DUPLICATE_ADAPTER` errors, credential references resolved per request via `ctx.credentials` so no secret enters config), but its *coverage* is a fraction of pi-go's.

Two places where dsh's design differs in kind, not just in coverage:

- **Credential references.** `apiKeyEnv: OPENAI_API_KEY` is a *reference* resolved per request, and a configured reference that resolves to nothing fails with `MISSING_CREDENTIAL` rather than falling through to whatever unrelated key the environment holds. pi-go reads `ANTHROPIC_API_KEY` etc. directly and silently uses whatever is present.
- **`llm-replay`.** See §3.14.

### 3.9 Memory and retrieval — **pi-go's clearest lead**

pi-go has two memory systems and dsh has none:

- **`internal/memory/`** — observations captured by `AfterToolCallback` into a buffered channel, compressed by a `memory-compressor` subagent on the `smol` model, stored in SQLite (`modernc.org/sqlite`, pure Go, WAL) with FTS5, retrieved through a deliberate 3-layer workflow (`mem-search` → `mem-timeline` → `mem-get`) sized at ~50-100 tokens/result for the index and ~500-1000 for full detail. Privacy filtering in `privacy.go`.
- **`internal/palace/`** — 5,657 LOC of knowledge graph, drawers, memory-stack layers, project/conversation miners, and **local embeddings** with pluggable backends (pure-Go and ONNX Runtime, plus Ollama), an embedding cache, and a `mempalace.yaml` room/pattern configuration for mining a repository.

dsh's nearest equivalent is `session-query` — SQLite FTS over the session corpus with tracing and 5 model-facing tools. That is a *search* capability, not a *memory* capability: nothing is compressed, distilled, or embedded, and nothing is injected into a new session's context.

This is the one axis where pi-go is doing something dsh has not attempted at all.

### 3.10 Skills, plans, goals, schedules, jobs

| Capability | dsh | pi-go |
|---|---|---|
| Skills | `ctx.skills` provider registry, merged host + per-scope catalogs, local + packaged-badge providers, `skill` tool with replacement catalogs | `internal/extension/skills.go` + bundled skills + `.SKILL.md` discovery + audit scanner |
| Plan mode | `ctx.planMode` — logged per-agent state, pure fold of the log, `exit_plan_mode` tool, `/plan` command; explicitly **soft guidance** decoupled from sandbox/approval | `internal/sop` (PDD 7-phase) + TUI `/plan` |
| Goals | `ctx.goals` — event-sourced, `GoalRef` compare-and-set on exact revision, 3 tools | — |
| Schedules | Durable reminders returning to the live session as ordinary turns; `after_seconds` / absolute `at` / `every_seconds` (≥5min), canonicalized to RFC 3339 UTC; 3 tools | — |
| Background jobs | `ctx.jobs` + `job_list` / `job_output` / `job_kill` | `BashSupervisor` background bash + `bash-wait` / `bash-kill` |
| Todo | `todo_write` tool + `todo/write` session event | — |
| Terminal | `ctx.terminals` persistent PTY, 6 tools, `TerminalWaitReason` independent of session status | `internal/webserver/pty.go` (web UI only, not a model tool) |
| Code mode | `run_code` — model writes a program against host-provided async bindings; sub-calls go through the tool pipeline, log `tool/code-dispatch`, carry the parent token | — |
| Self-modification | 7 `cordis_*` tools: the agent defines, mounts, runs, inspects, and unmounts its own plugins | — |

pi-go's plan/SOP system is deeper on *methodology* (the PDD sequence produces `requirements.md`, `research/`, `design.md`, `outline.md`, `plan.md`, `PROMPT.md`) but shallower on *state* — plan mode is not a fold of the session log, so a resumed session's plan state comes from `Meta.PlanContext`, a mutable field, rather than from replay.

`todo_write` is a notable absence: it is one of the highest-leverage cheap tools in agent harnesses, and pi-go's `ISSUES.md` error analysis shows exactly the kind of drift it prevents.

### 3.11 LSP

Comparable and both good. pi-go's `internal/lsp` runs gopls / typescript-language-server / ruff / rust-analyzer on demand with format-on-write and diagnostics-on-edit hooks plus 5 explicit tools. dsh has one `lsp` tool over a `ctx.lsp` seam. pi-go's automatic format+diagnostics-after-edit is the better product behaviour; dsh's single multiplexed tool is the cheaper prompt.

### 3.12 Shell, process control, and terminals

pi-go's `BashSupervisor` (`internal/tools/bash_supervisor.go`, ~750 LOC with 4 dedicated test files including leak and exit tests) is serious work: background execution, process-tree lifetime, the pipe-holding-descendant problem, budget and limit enforcement, streaming. This is more careful than most harnesses.

dsh splits the same territory across four seams — `ctx.subprocess` (process tree), `ctx.shell` (bash/pwsh providers, with a request/spec split where defaulting is an explicit `resolve(request): Spec` step, never a hidden `?? default` inside `run()`), `ctx.sandbox` (confinement), and `ctx.terminals` (persistent PTY) — plus PowerShell support and a Windows backend.

**Windows:** dsh has native Windows CI, a Wine-based blocking lane, a `pwsh` tool, and a Windows ACL sandbox. pi-go has `process_windows.go` files in `subagent` and `codex` and **no Windows CI at all**. Those files are compiled by nobody.

### 3.13 Interop: MCP, ACP, SDKs, protocols

| | dsh | pi-go |
|---|---|---|
| MCP client | `dsh-mcp-client`, one plugin per server, `mcp__<server>__<tool>` naming | `internal/extension/mcp.go` with **respawn + connection-tracking transports** and a resilient toolset that survives server death |
| ACP | Server (automation-only) | **Server *and* client** — pi-go can be driven by an ACP editor *and* drive other ACP agents |
| Embedding SDK | JSON-RPC protocol + TS client + Python SDK + bundled runtime | `piagent` + `pimodels` Go packages with enforced isolation |
| Wire protocol | Typert type-graph-generated RPC gateway | JSON-RPC 2.0 over unix socket (`internal/pirpc`) |

pi-go's MCP layer is more robust than dsh's on the failure axis — `resilientToolset` with respawn is a real operational feature. pi-go's dual-role ACP is a genuine differentiator.

dsh's `typert` (a type-graph generator + loader + runtime registry driving the API gateway) is over-engineering for pi-go's needs, but the *idea* — generate the wire surface from the type definitions rather than hand-writing both sides — is why its 3,744-line `api-proxy.ts` stays consistent.

### 3.14 Testing — **pi-go's second-biggest gap**

dsh's `docs/testing.md` is the single most transferable artifact in the repository. Its tiers:

1. **Unit** (vitest) — every registry gets an HMR-safety test (dispose the fiber, assert cleanup); permanent contract-regression tests.
2. **Coverage gate** — per-file **100%**, with the stated reasoning: *"An uncovered line is often dead code the gate is correctly flagging for deletion, not a missing test to bolt on."* Exemptions are explicit, listed in `scripts/coverage-exempt.ts`, and justified inline in `vitest.config.ts`.
3. **Real-API e2e** — self-skipping without a key. Policy: *"We are DeepSeek — do not ration real-API tests. A no-key test proves plumbing; only a with-key run proves the agent works against a real model."*
4. **Snapshot (keyless)** — boots the **real assembled application**, replays a recorded session JSONL through `llm-replay`, and diffs normalized JSON-RPC output *and* the re-persisted log. `test:snapshot:record` re-records; `test:snapshot:refresh` reuses valid replay input. One scenario pins the full system-prompt and tool-schema content; the rest tokenize it so an edit churns one line.
5. **Web browser snapshot** — Chromium, required Linux PR gate, CI forces read-only replay so expected outputs are never written by CI.

And four rules stated in `docs/testing.md` that have no counterpart in pi-go's testing practice:

- *"Verify the world, not the self-report."* An e2e assertion re-runs the command or re-reads the file externally; a keyword probe on the agent's own output lets a cheating agent pass. Assert untouched files are byte-identical.
- *"Prefer the real implementation over a mock."* Mock only the expensive or non-deterministic boundary (LLM, network, clock); keep everything downstream real.
- *"Test the real entry path"* — the published artifact, run as the user runs it, not the source tree under a dev loader.
- *"A guard only guards if the regression actually fails it."* Introduce the regression, watch it go red, revert.

pi-go's testing is respectable in volume (129k LOC, 181% of source, 372 files) and has good instincts — build-tagged `e2e`/`integration`, race lane in CI, fuzz test in `internal/acp/client`, table-driven style per `code-guidelines-go`. What it does not have:

- **No LLM record/replay.** `grep -rn "replay|go-vcr|httprecord"` finds only unrelated uses. Every provider-behaviour test either mocks the ADK `model.LLM` interface (proving plumbing, not behaviour) or needs a live key.
- **No assembled-app snapshot tier.** There is no test that boots `pi` end to end against a fixed transcript and diffs the output. The `eval` harness (`internal/eval`) is closest but is manually run, needs a key, and grades qualitatively.
- **No coverage gate with teeth.** `codecov.yml` is 2 lines and only ignores `cmd/`.
- **The self-review is honest and unflattering.** `DESIGN-REVIEW.md` records `go test ./...` failing with a nil-pointer panic in the Cursor ACP finish path at last validation (2026-04-23) and scores error handling 6/10 and concurrency/lifecycle 6/10.

### 3.15 CI and platform coverage

dsh: static gates, exhaustive coverage (with bubblewrap prepared), snapshots + artifacts + Playwright, a Node 22.19/24/26 compatibility matrix, a keyless Python SDK lane, a release-shaped Python runtime lane, **native Windows**, **Windows-via-Wine (blocking)**, and a serial Linux lane.

pi-go: four jobs, all `ubuntu-latest` — lint, test (+race), coverage, build. Release is also Linux-only.

pi-go ships `hostenv_darwin.go`, `hostenv_linux.go`, `hostenv_disk_unix.go`, `process_windows.go`, `process_unix.go`, and a macOS-only `pi-sandbox` binary. Three of the four platform variants are never compiled in CI.

### 3.16 Documentation and knowledge management

dsh's approach: **generate what can be generated, gate what cannot.**

Generated: `config-catalog.md` (3,151 lines — every plugin config field), `tool-catalog.md` (1,873 lines — every tool schema), `module-graph.md` (1,638 lines), `persistence-catalog.md` (944 lines), `capability-seams.md` (471 lines), plus the agent-lifecycle and tool-pipeline Mermaid diagrams.

Gated: `verify-md-links`, `verify-md-wrap`, `verify-doc-refs`, `verify-package-paths`, `verify-export-jsdoc`, `verify-type-equiv`, `verify-doc-budgets`, `verify-mermaid`, `verify-cordis-catalog`, `verify-translation-pairing`, `verify-agent-note-format`, `verify-package-readme-model-experience` — all run by `pnpm run doc-sync`.

Recorded: 1,372 **Agent Notes** under `.agents/notes/{proposed,implemented,archived,rejected}/{architecture,feature,bug-fix,simplification,process,testing}/YYYY-MM-DD-slug.md`, mandatory for every non-trivial PR, with archived notes frozen ("never edit or treat them as current authority"). Every subsystem doc links to the note that owns each decision's rationale — so the doc states the *contract* and the note holds the *reasoning*.

There is also a documented **prose standard**: state contracts, not reasoning transcripts; no metaphors; *"Before writing `contract`, `boundary`, or `shape`, ask whether a more exact term names the subject."*

pi-go has 346 spec files under `specs/`, plus `ARCHITECTURE.md`, `docs/`, `ROADMAP.md`, `ISSUES.md`, `DESIGN-REVIEW.md`, and 11 `.claude/skills/`. The instinct is right; the enforcement is absent. `ARCHITECTURE.md` names a package that no longer exists and misses 11 that do — a `verify-doc-refs` equivalent would have caught it the day it drifted.

### 3.17 Code conventions and type safety

dsh's `AGENTS.md` conventions are unusually specific and mostly mechanically checked. The ones with direct Go analogues, and where pi-go currently stands against each:

- *"Misconfiguration fails loud at load when self-contained, otherwise at the earliest resolvable point; never silently skip a missing referent."* — pi-go's hook errors, MCP server failures, and skill-load failures currently log-and-continue.
- *"No hardcoded tunables in plugins: deployment-varying choices are validated `Config` fields; a `DEFAULT_*` constant or test hook is not configurability."*
- *"Explicit > implicit at package boundaries: defaulting is an explicit `resolve(request): Spec` step, never a hidden `?? default` inside `run()`."*
- *"An empty `catch` names what it swallows and why nothing else can reach it; keep the `try` to one statement."* — the Go form is a bare `_ = err`, of which pi-go has several.
- *"Trust TypeScript at typed same-process boundaries"* + an explicit list of where validation *is* required (parser/config, queued, model/tool JSON, durable/file, worker, process, wire). pi-go's `coercingTool` is exactly the model/tool JSON boundary correctly identified.
- *"Opaque cross-boundary ids are branded, never bare `string`."* — pi-go passes `sessionID string`, `agentID string`, `callID string` interchangeably. A `type SessionID string` costs nothing.
- *"Prefer symmetry for parallel values; unexplained asymmetry usually signals a missed extraction."*

pi-go's `.golangci.yml` is solid (errcheck, staticcheck, errorlint, nilerr, bodyclose, fatcontext, revive) with a documented, defensible decision to keep complexity linters off by default and run them on demand. That is more honest than most repos.

---

## 4. Where pi-go is genuinely stronger

1. **Provider reach.** 8 provider families vs 1 native + 1 library. Prompt caching, thinking/reasoning-effort control, Azure, Ollama, server-side tool use on xAI, a model catalog with metadata.
2. **Heterogeneous delegation.** Spawning Claude Code, Codex, Gemini, Cursor, and Copilot as subagents is unique here and strategically valuable.
3. **Memory.** Two independent systems (compressed observations + knowledge-graph palace with local embeddings). dsh has nothing in this space.
4. **The TUI.** 16k LOC of Bubble Tea with deferred initialization, session restore with call/result pairing, slash commands, plan flow, commit flow, minimap. dsh has no terminal UI at all.
5. **The `ReadLedger`.** Partial-vs-full read tracking makes overwrite refusals accurate rather than merely cautious. Better than dsh's read-before-edit gate.
6. **Tool-output compaction.** Nine domain-aware stages (test aggregation, build filtering, git, linter, search grouping) with metrics. dsh truncates and spills but does not understand the output.
7. **`coercingTool`.** Argument aliasing and type coercion driven by measured production failures (`ISSUES.md`: 592 schema errors in 30 days).
8. **`BashSupervisor`.** Background bash with process-tree lifetime handling, tested for leaks and exit semantics.
9. **Worktree isolation for subagents**, including the stash/pop handling for dirty trees.
10. **ATIF export.** A standardized, interchangeable trajectory format. dsh's log is deliberately proprietary and unversioned pre-release.
11. **Eval harness with an LLM judge** (`internal/eval`, `make eval-run`) measuring trajectory, subagent concurrency, and tool efficiency against a pinned base commit.
12. **Resilient MCP.** Respawning transports and a toolset that survives server death.
13. **Single static binary.** No `pnpm install`, no Node version matrix, no ESM/CJS hazards. dsh needs a whole section of `AGENTS.md` on tsx's ESM-only hook.

---

## 5. Where pi-go is weaker

Ordered by consequence.

1. **No tool-approval gate; `bash` unconfined; hooks cannot deny; ACP permissions auto-approve with a fail-open fallback.** (§3.4)
2. **No LLM replay and no assembled-app snapshot tests.** (§3.14)
3. **No OS-level per-call sandbox on Linux or Windows; macOS confinement is whole-process and opt-in.** (§3.4)
4. **The system prompt is not part of the session log**, so resume can silently diverge from the recorded transcript. (§3.2)
5. **No session format version**, so on-disk layout changes have no migration signal. (§3.2)
6. **CI covers one platform** while shipping code for three. (§3.15)
7. **No generic tool-call timeout**; a hanging MCP or LSP call has no backstop. (§3.3)
8. **No spill** — compacted-away output is unrecoverable. (§3.7)
9. **No loop-hygiene guard**; the model is never told it is repeating itself. (§3.3)
10. **No canonical tool-output contract**; `map[string]any` everywhere, nothing validated. (§3.3)
11. **No `todo_write`**, no goals, no schedules, no persistent PTY as a model tool. (§3.10)
12. **Docs drift with no gate**; `ARCHITECTURE.md` is 11 packages out of date. (§3.16)
13. **No mid-turn steering**; queued prompts land after the turn, not inside it. (§3.1)
14. **Undocumented callback ordering**; `[]BeforeToolCallback` order is append order. (§3.1)
15. **Bare `string` ids across boundaries.** (§3.17)

---

## 6. Scorecard

| Area | dsh | pi-go | Notes |
|---|---|---|---|
| Composition / extensibility | 9 | 6 | Seams vs fixed shape |
| Agent loop design | 9 | 6 | ADK constrains pi-go's interception surface |
| Session log fidelity | 9 | 5 | Prompt unlogged, no version |
| Tool pipeline | 9 | 6 | No approval, timeout, or output contract |
| **Permissions / sandbox** | **9** | **3** | The critical gap |
| Hooks | 8 | 4 | Cannot deny; errors swallowed |
| Subagents | 7 | 8 | pi-go wins on cross-vendor; dsh on live control |
| Context management | 8 | 7 | Better compaction, worse lifecycle |
| Providers | 5 | 9 | pi-go's clearest lead after memory |
| Memory / retrieval | 3 | 9 | dsh has none |
| UI surfaces | 7 | 8 | Web app vs TUI + web + ACP + RPC |
| Interop (MCP/ACP/SDK) | 7 | 8 | Resilient MCP, dual-role ACP |
| Observability | 7 | 7 | Both OTel; pi-go adds pprof + httplog |
| Testing | 10 | 6 | No replay, no snapshot tier |
| CI / platform coverage | 9 | 4 | One platform, three shipped |
| Docs | 9 | 6 | Right instinct, no gates |
| Conventions / type safety | 9 | 7 | Good linting, weak boundary typing |
| Eval / benchmarking | 3 | 8 | pi-go's judge harness has no dsh peer |

---

## 7. Closing

The two projects are not converging on the same design, and neither is a degraded version of the other. dsh spent its budget on *enforcement*: composable seams, fail-closed gates, generated docs, and a replay tier that proves the assembled product works. pi-go spent its budget on *reach*: eight provider families, cross-vendor delegation, two memory systems, and a real terminal UI. Each is strongest exactly where the other invested least.

The distance is widest at the permission/sandbox boundary (dsh 9, pi-go 3) and at testing (10 vs 6); it is widest in the other direction at memory (3 vs 9) and eval (3 vs 8). Those four rows are the substance of the comparison — the rest is a difference in degree rather than in kind.

> **Scope note:** this document compares the two systems as they stand. It deliberately makes no recommendations for pi-go; the gaps recorded in §5 are observations, not a work plan.

---

## Appendix: verification commands

The dsh side of every command below reads a local clone. `tmp/` is gitignored,
so reproduce it with:

```bash
git clone git@github.com:deepseek-ai/deepseek-harness tmp/harness/deepseek-harness
git -C tmp/harness/deepseek-harness checkout 47f943859bef60e4160492346772ded9b24f765a
```

```bash
# Scale
find packages apps -name '*.ts' -o -name '*.tsx' | grep -v node_modules \
  | grep -vE '\.(spec|test|e2e)\.' | xargs wc -l | tail -1     # 241,862
find internal cmd -name '*.go' -not -name '*_test.go' | xargs wc -l | tail -1  # 69,200
git ls-files '*_test.go' | wc -l                                              # 388
git ls-files '*_test.go' | xargs wc -l | tail -1                              # 128,692

# Tool counts
grep -c '^### ' tmp/harness/deepseek-harness/docs/tool-catalog.md            # 52
sed -n '44,88p' internal/tools/registry.go                                    # CoreTools()

# Permission surfaces
grep -rl 'Approval\|permission' --include='*.go' internal/ | grep -v _test
#   → internal/acp/permissions.go only (auto-approve)
grep -rn 'approve\|confirm' --include='*.go' internal/tui/ | grep -v _test
#   → commit + skill-overwrite confirmations only

# Hooks cannot deny
sed -n '141,158p' internal/extension/hooks.go        # returns (nil, nil) always

# Replay infrastructure
grep -rn 'replay\|go-vcr\|httprecord' --include='*.go' internal/ | grep -v _test
#   → no LLM replay

# Platform CI
grep -n 'runs-on' .github/workflows/ci.yml           # ubuntu-latest ×4
grep -n 'runs-on\|name:' tmp/harness/deepseek-harness/.github/workflows/ci.yml

# Doc drift
ls internal/                                          # 29 packages
grep -oE 'internal/[a-z]+' ARCHITECTURE.md | sort -u  # 1 — the tree uses bare names
grep -c '├──\|└──' ARCHITECTURE.md                    # 19 rows, 17 of them internal/
```
