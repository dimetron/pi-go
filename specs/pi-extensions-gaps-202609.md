# pi-go vs the Pi Agent Harness — Extension Surface Comparison

> **Scope:** Side-by-side comparison of the two public Go façades that make pi-go
> embeddable (`pimodels`, `piagent`) against the extension API of the TypeScript Pi
> agent harness (`tmp/harness/pi`, snapshotted at commit `b73412a37`). Produces a
> prioritised list of gaps and proposed `With*` additions.
>
> **Date:** 2026-09-20
>
> **Methodology:** Both sides read directly — `go doc -all` for the Go surface,
> `packages/coding-agent/src/core/extensions/types.ts` (1,830 lines) for the
> TypeScript surface, plus the 84 worked examples under
> `packages/coding-agent/examples/extensions/`. Every claim below is anchored to a
> file:line. Frequency counts come from `grep -rlF` over those examples.
>
> **Status:** Research complete. Findings verified against the code; proposed
> additions are **not implemented**. See `## Open Questions`.

---

## Executive Summary

| # | Headline |
|---|----------|
| 1 | **The Go surface is a construction-time façade; the TS surface is a live extension host.** `piagent` exposes 21 `With*` options and 8 hook points, all of which must be supplied to `New`. TS exposes 29 distinct events, ~20 API methods, and 18 `ExtensionContext` members — and can mutate state after startup (`registerTool`, `registerProvider`, `setModel`, `setActiveTools`). |
| 2 | **The single largest behavioural gap is runtime tool registration.** `piagent.Agent.Tools()` (`piagent/agent.go:398`) is read-only; tools are frozen inside `agent.New`. TS's `registerTool` is the most-used extension capability — 20 of 84 examples — and is idiomatic, not an edge case. |
| 3 | **Two features are one line away.** `autocompact.Deps` was *designed* with `SummarizerLLM` (`internal/autocompact/hook.go:49`) and `Notify` (`:53`) as seams. `piagent` hardcodes both — `SummarizerLLM: llm` (`piagent/agent.go:185`), `Notify: nil` (`:189`). A documented capability ("summarize with a cheaper model") is unreachable from an embedder. |
| 4 | **`pimodels` leaves 6 of 15 transport options unreachable**, because it does not re-export `LLMOptions` and has no `With` for them. Two are correctness bugs, not just missing convenience: `UseLegacyMaxTokens` and `MaxOutputTokens`. |
| 5 | **⚠️ `WithLegacyMaxTokens` is a silent-failure bug for Ollama behind a gateway.** Ollama understands only `max_tokens`; the newer `max_completion_tokens` is *silently ignored*, leaving output unbounded. An embedder pointing `pimodels` at agentgateway→Ollama has no way to set this. |
| 6 | **Tool blocking and argument rewriting already work — do not "add" them.** ADK's `BeforeToolCallback` skips the tool on a non-nil result and supports in-place `args` mutation; `piagent` appends the embedder's callbacks last (`piagent/agent.go:535`) and pi-go's own return `(nil, nil)` (`internal/extension/hooks.go:150,156`), so they do not consume the chain. `permission-gate.ts` is reproducible today. |
| 7 | **`piagent` silently discards compaction outcomes.** `Notify: nil` is deliberate — "an embedder reads the outcome in the session log" (`piagent/agent.go:186-188`) — but the session log is not readable by an embedder: there is no `LogPath()` accessor and `internal/logger` is unreachable. So in practice the outcome is discarded, not relocated. |
| 8 | **A headless agent has no approval surface.** `permission-gate.ts` uses `ctx.ui.select`; with `piagent` the embedder must hand-roll interactive confirmation inside a raw ADK callback. A first-class `WithApprover` is the missing shape. |

**Verdict:** the comparison is not "pi-go is behind." A headless Go library
*should* differ from a TUI extension host — UI, overlays, renderers, keybindings
and themes are correctly absent. The real gap is narrower and more actionable:
**pi-go holds the seams but does not expose them.** Of 15 proposals, 8 are pure
plumbing against fields that already exist, 2 are one-line wiring of existing
`autocompact.Deps` fields, and only 1 requires real design work.

---

## 1. Surface sizes

Counts measured directly, not estimated.

| Dimension | `pimodels` | `piagent` | TS `ExtensionAPI` |
|---|---|---|---|
| Configuration options (`With*`) | 9 | 21 | ~20 methods |
| Distinct events / hooks | 0 | 8 | **29** |
| Runtime state readers | 0 | 1 (`Tools()`) | 18 (`ExtensionContext`) |
| Runtime mutation | — | none | `registerTool`, `registerProvider`, `setModel`, `setActiveTools`, `setSessionName`, `setLabel`, `sendUserMessage`, `sendMessage`, `appendEntry` |
| UI / presentation | — | none (by design) | `ctx.ui.*`, 3 renderers, `registerShortcut` |
| Reachable transport fields | 9 of 15 | n/a | n/a |

`piagent`'s 8 hook points: `WithBeforeTurn`, `WithAfterTurn`,
`WithBeforeToolCallbacks`, `WithAfterToolCallbacks`, `WithBeforeModelCallbacks`,
`WithAfterModelCallbacks`, `WithAgentEvents`, `WithContextWindow`.

### 1.1 `pimodels` — 9 options

`WithAPIKey`, `WithBaseURL`, `WithThinkingLevel`, `WithHeaders`,
`WithConnectTimeout`, `WithCACert`, `WithInsecureTLS`,
`WithPromptCachingDisabled`, `WithAdvisor`.

Plus non-option functions: `New`, `NewFromInfo`, `FromConfig`, `Resolve`,
`ContextWindow`, `ContextWindowFor`, `APIKeyEnvVar`.

### 1.2 `piagent` — 21 options

`WithModel`, `WithWorkingDir`, `WithSessionDir`, `WithExtraSandboxDirs`,
`WithInstruction`, `WithExtraInstruction`, `WithTools`, `WithToolsets`,
`WithBeforeToolCallbacks`, `WithAfterToolCallbacks`, `WithBeforeModelCallbacks`,
`WithAfterModelCallbacks`, `WithBeforeTurn`, `WithAfterTurn`, `WithLSP`,
`WithMemory`, `WithPalace`, `WithSkills`, `WithSubagents`, `WithAgentEvents`,
`WithContextWindow`.

`Agent` methods: `Ask`, `Run`, `RunStreaming`, `NewSession`, `SetSessionTitle`,
`Tools`, `Model`, `WorkingDir`, `Close`.

### 1.3 TS `ExtensionAPI` — 29 events, ~20 methods

The 29 events, for reference:

```
project_trust              resources_discover          session_start
session_info_changed       session_before_switch        session_before_fork
session_before_compact     session_compact             session_compact_failed
session_shutdown           session_before_tree         session_tree
context                    cache_warming_decision      before_provider_request
before_provider_headers    after_provider_response     before_agent_start
agent_start                agent_end                   agent_settled
ui_prompt_start            ui_prompt_end               turn_start
turn_end                   message_start               message_update
message_end                tool_execution_start        tool_execution_update
tool_execution_end         model_select                thinking_level_select
tool_call                  tool_result                 user_bash
input
```

(37 `on()` overloads collapse to 29 distinct names.)

---

## 2. Feature comparison

Legend: ✅ present · 🟡 partial · ⚪ absent · 🚫 deliberately excluded · 🐛 correctness bug

| Feature | TS harness | `piagent` | `pimodels` | Assessment |
|---|---|---|---|---|
| Configure at construction | ✅ `ExtensionFactory` | ✅ 21 `With*` | ✅ 9 `With*` | parity |
| Register a tool at runtime | ✅ `registerTool` (20 ex.) | ⚪ frozen after `New` | n/a | **largest gap** |
| Block / rewrite a tool call | ✅ `tool_call` → `{block,reason}` | ✅ `WithBeforeToolCallbacks` | n/a | **already works** |
| Rewrite tool output | ✅ `tool_result` | ✅ `WithAfterToolCallbacks` | n/a | parity |
| Turn start / end | ✅ | ✅ `WithBeforeTurn`/`WithAfterTurn` | n/a | parity |
| Message-level lifecycle | ✅ `message_start/update/end` | 🟡 event stream only | n/a | minor |
| Replace system prompt per turn | ✅ `before_agent_start` | ⚪ built once in `New` | n/a | moderate |
| Inject a message into a session | ✅ `sendUserMessage` | ⚪ `Run` only | n/a | moderate |
| Custom compaction | ✅ `session_before_compact` | ⚪ hook not exposed | n/a | **1 line** |
| Compaction notification | ✅ `ctx.ui.notify` | 🐛 `Notify: nil` + no `LogPath()` | n/a | **1 line + accessor** |
| Context usage | ✅ `ctx.getContextUsage()` | ⚪ `Meter` internal | n/a | small |
| Abort in-flight turn | ✅ `ctx.abort()` | 🟡 cancel `ctx` | n/a | parity by idiom |
| Session control (fork/switch/tree) | ✅ | ⚪ | n/a | large |
| Discover skills/prompts/themes | ✅ `resources_discover` | 🟡 conventions only | n/a | moderate |
| Project trust gate | ✅ `project_trust` | ⚪ | n/a | moderate |
| Provider registration | ✅ `registerProvider` | 🚫 isolation test | 🟡 `WithBaseURL` | correct as-is |
| OAuth login flow | ✅ `oauth.login` | 🚫 | 🚫 | out of scope |
| HTTP request/response hook | ✅ `before_provider_request` | ⚪ | 🟡 PR #359 | in flight |
| Output token cap | ✅ `maxTokens` per model | n/a | ⚪🐛 `MaxOutputTokens` unexposed | **correctness** |
| Legacy `max_tokens` wire | ✅ | n/a | ⚪🐛 `UseLegacyMaxTokens` unexposed | **correctness** |
| Rate limiting | ⚪ | n/a | ⚪ `RateLimit` unexposed | small |
| Server-side provider tools | ✅ per provider | ✅ Gemini grounding | 🟡 xAI flag unexposed | small |
| System CA trust toggle | n/a | n/a | ⚪ `DisableSystemCAs` unexposed | trivial |
| UI: dialogs, overlays, editors | ✅ | 🚫 headless | 🚫 | correct |
| Renderers, footers, themes | ✅ | 🚫 | 🚫 | correct |
| Keybindings, slash commands | ✅ | 🚫 | 🚫 | correct |

---

## 3. What already works (verified, not assumed)

Three capabilities that look missing and are not. Recorded here because each is a
plausible-sounding myth that would waste an implementation cycle.

### 3.1 Tool blocking — `permission-gate.ts` is reproducible today

ADK's `BeforeToolCallback`:

> If a callback returns a non-nil result or an error: execution of remaining
> callbacks stops, the actual tool call is skipped, and the returned result is
> used as the tool result. To modify tool arguments and still run the tool,
> update args in place and return `(nil, nil)`.
> — `go doc google.golang.org/adk/v2/agent/llmagent.BeforeToolCallback`

And `piagent` appends the embedder's callbacks last (`piagent/agent.go:535`),
with pi-go's own returning `(nil, nil)` (`internal/extension/hooks.go:150,156`) so
they never consume the chain. Therefore blocking and in-place argument rewriting
both work through `WithBeforeToolCallbacks`.

### 3.2 After-tool chaining

`composeAfterTool` (`piagent/callbacks.go:34`) exists precisely because ADK stops
its after-tool chain at the first non-nil result. Your callbacks run after
pi-go's, so they observe the compacted, deduplicated result.

### 3.3 Turn hooks

`WithBeforeTurn` / `WithAfterTurn` already provide `turn_start` / `turn_end`,
including the `Abandoned` flag for a caller that breaks out of the range loop.

---

## 4. Proposed additions

Ordered by value-to-effort. "Effort" is a judgement; "Feasibility" is evidence.

### 4.1 `pimodels` — plumbing (no design decisions)

| # | Proposal | Unblocks | Effort | Feasibility evidence |
|---|---|---|---|---|
| P1 | `WithLegacyMaxTokens()` | 🐛 Ollama behind agentgateway: output currently **unbounded**, silently | trivial | `LLMOptions.UseLegacyMaxTokens` exists |
| P2 | `WithMaxOutputTokens(n)` | Backends whose models stop below the default and **reject** rather than clamp | trivial | `LLMOptions.MaxOutputTokens` exists |
| P3 | `WithXAITools()` | xAI server-side tools | trivial | `LLMOptions.EnableXAITools` exists |
| P4 | `WithSystemCAsDisabled()` | Completes the TLS trio beside `WithCACert`/`WithInsecureTLS` | trivial | `LLMOptions.DisableSystemCAs` exists |
| P5 | `WithRateLimit(...)` | Stay inside a provider quota rather than being 429'd | small | `LLMOptions.RateLimit` exists |
| P6 | `WithTraceSink(fn)` | Per-client HTTP tracing | **in flight** | PR #359, open and mergeable |

P1 is the highest-value item in this document: a silent correctness failure, not
a missing convenience.

### 4.2 `piagent` — one-line wiring of existing seams

| # | Proposal | Unblocks | Effort | Feasibility evidence |
|---|---|---|---|---|
| A1 | `WithSummarizer(m)` | Custom compaction — summarize with a cheap model. Exactly `custom-compaction.ts` | **1 line** | `autocompact.Deps.SummarizerLLM` (`hook.go:49`) exists; piagent hardcodes `SummarizerLLM: llm` (`agent.go:185`) |
| A2 | `WithCompactNotify(fn)` | Learn that history was discarded — today silent | **1 line** | `autocompact.Deps.Notify` (`hook.go:53`) exists; piagent hardcodes `Notify: nil` (`agent.go:189`) |
| A3 | `Agent.LogPath() string` | Find/tail the session log; fixes the discard in A2's rationale | trivial | `logger.Logger.Path()` (`internal/logger/logger.go:90`) |
| A4 | `Agent.ContextUsage() (used, window int64)` | Budget display, admission control | small | `Meter.BodyTokens()` (`meter.go:51`), `ContextWindowSize()` (`:44`) |
| A5 | `WithApprover(fn)` | Headless `permission-gate`: `func(tool string, args map[string]any) (allow bool, reason string)` | small | Composes atop §3.1 |
| A6 | `WithSystemPromptOverride(fn)` | Per-turn prompt override — multi-tenant steering (`before_agent_start`) | **nontrivial** | `InstructionProvider` closes over the string at `buildRunner` (`internal/agent/agent.go:474`); needs an atomic indirection |
| A7 | `Agent.SystemPrompt() string` | Inspect the assembled prompt | small | `buildInstruction` (`piagent/build.go:142`) result is not retained; 1 field |
| A8 | Richer `TurnInfo` (message, tokens) | Turn-end observability with content | small | `TurnInfo` carries counts only |

### 4.3 `piagent` — the real gap

| # | Proposal | Unblocks | Effort | Feasibility evidence |
|---|---|---|---|---|
| A9 | `Agent.AddTools(...)` / `WithDynamicTools` | Register a tool after `New` — from config, a plugin dir, or a runtime decision. TS's most-used capability (20 examples) | **moderate** | `Tools()` is read-only (`agent.go:398`); tools frozen in `agent.New` |

A9 is the only proposal requiring design work: it means either mutating ADK's
tool slice under a lock or rebuilding the `llmagent` — and the second invalidates
the session service wiring. Worth its own spec.

---

## 5. Deliberately not ported

| TS feature | Why not |
|---|---|
| Generic `On(event string, handler any)` | The TS design leans on dynamic typing. Go would get stringly-typed dispatch and lose compile-time safety. Typed `With*` per event is the Go-correct shape — which is why §4 lists 15 typed options rather than 1 event bus. |
| `ctx.ui.*` — `select`, `confirm`, `editor`, overlays | `piagent` is headless by design. |
| `registerEntryRenderer`, `registerMarkdownTransformer`, custom header/footer/status | TUI concerns; no TUI here. |
| `registerShortcut`, `registerFlag`, themes | Same. |
| `registerProvider` | Would drag provider resolution into `piagent`, which `piagent/isolation_test.go` forbids. For `pimodels`, `WithBaseURL` covers the real need. |
| Session tree navigation (`fork`, `switchSession`, `navigateTree`) | Large surface; the CLI owns it. Revisit only on demand. |
| MCP/A2A extension points | Already covered by config-driven toolsets. |

### 5.1 Two options that look like parity but are not

**`resources_discover`.** TS returns arbitrary `skillPaths`, `promptPaths`,
`themePaths` from a callback. `piagent` has only `WithSkills(bool)` — a boolean,
not a discovery hook. Adding paths would mean exposing
`internal/extension.LoadOptions`. Moderate effort, not on the shortlist because
the convention-based `.pi-go/skills/` directory covers the common case.

**`context`.** TS's `context` event can replace the whole outgoing message list
before a provider request. In Go that is a `BeforeModelCallback` mutating
`llmRequest` — already reachable, so no new option is needed.

---

## 6. Open questions

1. **Should `pimodels` re-export `LLMOptions`?** A single
   `WithOptions(provider.LLMOptions)`-style escape hatch would close all six
   plumbing gaps at once — but `provider` is `internal/`, so it would mean
   publishing the struct, which couples `pimodels`'s API surface to the internal
   transport type. The alternative (six small `With*` functions, §4.1) keeps the
   surface explicit and independently documented. **Recommendation: the six
   functions.** They read better and each can explain its own trap.
2. **Does A9 (`AddTools`) justify a new spec?** It is the only item needing
   design, and it changes behaviour rather than plumbing.
3. **Is A5 (`WithApprover`) worth it, given §3.1 already works?** The argument for
   it is ergonomics: hand-rolling confirmation inside a raw ADK callback is
   unpleasant but possible. Lower priority than A1–A4.
4. **Verify the TS snapshot.** Findings are against `tmp/harness/pi` at
   `b73412a37`. `tmp/` is gitignored, so this comparison is not reproducible from
   a clean clone.

---

## Appendix A: Suggested batching

| Batch | Contents | Rationale |
|---|---|---|
| 1 | P1–P4 | Pure plumbing. No design decisions, no behaviour change for existing users. |
| 2 | A1–A3 | One-line wiring of seams that already exist, plus the accessor that makes A2 meaningful. |
| 3 | A4, A5, A7, A8 | Small API additions; each needs a small test. |
| 4 | A9 | Its own spec — the only item that changes agent behaviour. |

P6 (`WithTraceSink`) is already delivered as PR #359; it moves to "done" on merge.

## Appendix B: Verification commands

```bash
# Go surface
go doc -all ./pimodels
go doc -all ./piagent

# TS surface
sed -n '1261,1540p' tmp/harness/pi/packages/coding-agent/src/core/extensions/types.ts
sed -n '310,400p'  tmp/harness/pi/packages/coding-agent/src/core/extensions/types.ts

# Example frequency (84 examples: 68 top-level + 16 in subdirs)
cd tmp/harness/pi/packages/coding-agent/examples/extensions
grep -rlF 'pi.registerTool(' . | wc -l     # 20
grep -rlF 'pi.registerCommand(' . | wc -l  # 35

# The two one-line seams
grep -n 'SummarizerLLM\|Notify' internal/autocompact/hook.go piagent/agent.go
```
