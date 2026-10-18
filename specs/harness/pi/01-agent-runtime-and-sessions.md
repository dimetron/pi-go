# 01 — Agent Runtime & Sessions

## Side-by-side summary

| | pi-go | pi |
|---|---|---|
| Loop ownership | delegated to ADK v2 | own 940-line loop |
| Loop shape | flat `for {}` | two-level (outer follow-up, inner tool+steering) |
| Loop LOC in this repo | 1,761 (config) | 2,513 |
| Steering | ✗ | ✓ |
| Follow-up queue | ✗ | ✓ |
| Session format | JSONL, unversioned | JSONL, v3 + migrations |
| Tree structure | `branches.json` + integer head | `parentId` in every entry |
| Compaction | 2-stage, destructive | 2-stage, append-only record |
| Crash recovery | none | commit-before-execute |
| Fork / clone | ✗ | ✓ |

## 1.1 The agent loop

### pi-go — configuration over ADK

pi-go writes **no agent loop**. `internal/agent/agent.go:476` builds an
`llmagent.Config` and hands off to `runner.Run`. The real loop is
`adk@v2.4.0/internal/llminternal/base_flow.go:122` — flat, with a
`maxConsecutiveThoughtOnlyTurns = 10` guard for thought-only turns.

pi-go's only loop-level hook is `PreTurnHook` (`agent.go:393`), called at
`agent.go:791`/`800` **before** `runner.Run`. Signature:

```go
type PreTurnHook func(ctx context.Context, sessionID string) error
```

It can mutate history via `SessionService.AutoCompact`; an error aborts the
turn. Nothing runs mid-turn.

`internal/agent/retry.go:41` `WithRetry` is a coarse whole-run retry that only
replays when **zero** events were yielded (`retry.go:70`). Its own doc says
"does not help once a turn is underway". The fine-grained retry lives in
`internal/provider`.

### pi — an explicit two-level loop

`packages/agent/src/agent-loop.ts:163` `runLoop` is the whole runtime:

```ts
pendingMessages = await getSteeringMessages()          // :176
while (true) {                                        // :179 OUTER: follow-up
  hasMoreToolCalls = true
  while (hasMoreToolCalls || pendingMessages.length) { // :183 INNER: tools+steering
    prepareNextTurn()                                  // :186
    pendingMessages = await getSteeringMessages()      // :205
    emit turn_start
    requestUpdate = await prepareRequest(...)          // :219
    message = await streamAssistantResponse(...)       // :242
    toolCalls = message.content.filter(type==="toolCall")
    if (toolCalls.length) {
      batch = stopReason === "length"
            ? await failToolCallsFromTruncatedMessage(toolCalls, emit)
            : await executeToolCalls(...)
    }
    decision = await finishTurn(lastCompletedTurn)    // :286
    if (decision?.action === "end") { emit agent_end; return }
    explicitContinuation = decision?.action === "continue"
    pendingMessages = await getSteeringMessages()      // :295
    if (hasMoreToolCalls || pendingMessages.length) explicitContinuation = false
  }
  followUpMessages = await getFollowUpMessages()       // :302
  if (followUpMessages.length) { pendingMessages = followUpMessages; continue }
  if (explicitContinuation) continue                   // :311
  break
}
```

Four mechanisms pi-go has no analogue for:

1. **Steering** — poll `getSteeringMessages()` before the loop, after every
   turn, and again after a long `prepareNextTurn`. Modes
   `"one-at-a-time"` / `"all"`. pi-go's user cannot inject a message into a
   running turn at all.
2. **Deferred `continue`** — `finishTurn: {action:"continue"}` never causes a
   redundant provider call if tool results or steering already satisfied the
   next request (`:296-298`).
3. **Truncation guard** — `failToolCallsFromTruncatedMessage` (`:478`): on
   `stopReason === "length"` **no** tool call runs. Each becomes
   `"Tool call X was not executed: the response hit the output token limit"`,
   `terminate: false`, and the model re-issues.
4. **Tool-declaration reconciliation** — `declareToolChanges` (`:333`) diffs
   executable tools against what the transcript declares and injects a
   `toolsAdded`/`toolsRemoved` system message. pi-go's system prompt is static
   for a session; MCP tools appearing mid-session are invisible to the model
   until a rebuild.

**Batching safety.** pi fails the *whole* tool batch if any tool declares
`executionMode: "sequential"` (`:508-516`). pi-go has no per-tool execution
mode.

### Hook return contract — a real trap

ADK's before-tool chain **short-circuits on first non-nil**:

```go
// adk/v2.4.0 base_flow.go:1421-1434
for _, callback := range f.BeforeToolCallbacks {
    result, err := callback(toolCtx, tool, fArgs)
    if err != nil { return nil, err }
    if result != nil { return result, nil }   // tool never runs
}
```

pi-go's `BuildBeforeToolCallbacks` returns `nil, nil` **unconditionally**
(`internal/extension/hooks.go:156`), even when the hook command fails:
`hookLogf(...); return nil, nil` (`:152-157`). So the hook is advisory and
cannot block.

The same short-circuit on the after-tool chain was a **production bug**:
`internal/extension/hooks.go:184-211` documents that handing ADK the callback
slice ran only OTEL tracing and "dedup and the compactor was dead in production
for months". The fix is `ComposeAfterToolChain` (`hooks.go:245`), which folds
the chain and must be passed as a **single** slice element.

### Review — agent loop

pi-go's reliance on ADK buys a lot (streaming, sessions, callbacks, tool
dispatch, retry classification) and costs the entire mid-turn control surface.
The three mechanisms pi has that pi-go cannot get without forking ADK are
steering, the truncation guard, and tool-delta reconciliation.

**The cheap wins available today** are all pre-turn or post-tool:
`PreTurnHook` for compaction, `ComposeAfterToolChain` for result transforms,
and the compactor/dedup already wired. The expensive ones are inherently
mid-turn.

**One concrete risk**: a `stopReason: "length"` response in pi-go runs whatever
partial tool calls ADK can parse. Combined with pi-go's lack of a replay policy
(§1.5), a truncated tool call that *does* execute is exactly the class of
half-completed side effect pi-go cannot detect.

## 1.2 Session on-disk layout

### pi-go

Base `~/.pi-go/sessions/<id>/` (`internal/tools/session_stats.go:27`). IDs are
time-sortable: `yymmdd-hhmm-xxxxx-xxxxx` (23 chars, `store.go:137`).

| File | Purpose |
|---|---|
| `meta.json` | header, atomic temp+rename (`store.go:1046`) |
| `events.jsonl` | append-only event log (`store.go:1096`, raw `O_APPEND`) |
| `trajectory.atif.json` | ATIF v1.6 export (`atif.Writer`, `store.go:463`) |
| `acp.jsonl` | ACP trace |
| `branches.json` | branch map (`branch.go:209`) |
| `branches/<name>/events.jsonl` | per-branch event copy |

`Meta` (`store.go:48`) records `ID, AppName, UserID, WorkDir, Version, Model,
Provider, BaseURL, Title, CreatedAt, UpdatedAt, Host, PlanContext, Agent`.

`Version` is the **pi-go build string** (`"1.4.2+a1b2c3d"`), *not* a schema
version. Caching is 20 sessions LRU by `updatedAt` (`store.go:111`).

**There is no schema version and no migration path.** Grepping `migrat` across
`internal/session/` returns only the SQLite memory palace and shell-history
conversion. Old sessions are read best-effort by whatever struct tags exist
today.

`HostEnv` (`store.go:83`) snapshots the machine for post-mortem — the doc says
a process that dies with nothing but "signal: killed" can be checked against
the resources it had.

### pi

`~/.pi/agent/sessions/--<cwd-with-slashes-replaced>--/<fileTimestamp>_<id>.jsonl`
(`getDefaultSessionDirPath :589`, filename `:1080`).

`CURRENT_SESSION_VERSION = 3` (`:41`). Header
`SessionHeader{type:"session", version?, id, timestamp, cwd, parentSession?}`.

**Every entry has `parentId`** — the tree is materialized in the log itself.
`SessionEntryBase{type, id, parentId: string|null, timestamp}` (`:57`).

Eleven entry kinds (`:183`):

| Entry | Fields | In LLM context? |
|---|---|---|
| `message` | `message: AgentMessage` | yes |
| `thinking_level_change` | `thinkingLevel` | state only |
| `model_change` | `provider, modelId` | state only |
| `usage` | `kind, provider, model, usage` | no |
| `compaction<T>` | `summary, firstKeptEntryId, tokensBefore, systemMessage?` | yes (summary) |
| `branch_summary<T>` | `fromId, summary` | yes |
| `custom<T>` | `customType, data?` | **no** (state only, `:125`) |
| `custom_message<T>` | `customType, content, display` | **yes** (`:151`) |
| `context_edit` | `targetId, replacement: {content}\|null` | via projection |
| `label` | `targetId, label` | no |
| `session_info` | `name?` | no |

`context_edit` (`:175`) is the structural answer to destructive compaction: an
**append-only** record that either omits a target from context
(`replacement: null`) or replaces only its content. The raw message survives on
disk; only its model-visible projection changes.

**Migration is real and persisted** (`:337`):

```ts
function migrateToCurrentVersion(entries: FileEntry[]): boolean {
  const header = entries.find(e => e.type === "session") as SessionHeader | undefined;
  const version = header?.version ?? 1;
  if (version >= CURRENT_SESSION_VERSION) return false;
  if (version < 2) migrateV1ToV2(entries);
  if (version < 3) migrateV2ToV3(entries);
  return true;
}
```

Called on load; if it returns true the file is rewritten (`:1093`).

`buildSessionProjection` (`:543`) returns entries that each keep `sourceEntry`
**plus** post-edit model-visible `messages` — provenance-preserving.
Compaction entries at `index > 0` are forced to `[]` (`:562`) so only the
newest contributes a summary.

`forkFrom` (`:1815`) creates a new file with a new ID and copied content, linked
via `parentSession` in the header.

### Review — session format

pi-go's session store is competent: atomic meta writes, LRU caching, an
interchange format, and host provenance. Two things are missing that cost real
money when they bite:

1. **No schema version.** Any future field change is a silent behavioral break
   for every existing session. `Meta` already has a natural home for
   `"schemaVersion": 1`, and `readMeta` (`store.go:1084`) is the single load
   point. This is the cheapest fix in the entire report.
2. **Integer branch heads.** `BranchInfo.Head` is a bare `int` event index,
   updated on every append (`store.go:477`). Compaction rewrites the event list,
   so after any summarize the head pointer is stale. Keying on event ID would
   make branches robust to compaction.

pi's `parentId`-in-every-entry costs a few bytes per event and buys tree
navigation, fork, and non-destructive compaction for free. That is a better
trade than a sidecar file with integer indices.

## 1.3 Compaction

Both sides implement two stages: a cheap no-LLM shed, then an LLM summarize.
Both preserve user messages verbatim and drop tool traffic. Both run only
between turns.

### pi-go

`internal/session/compaction.go:81`:

```go
func DefaultAutoCompactConfig() AutoCompactConfig {
  return AutoCompactConfig{Enabled: true, ShedPercent: 60, SummarizePercent: 90,
    KeepUserMessageTokens: 20000, KeepRecentEvents: 10}
}
```

`normalize()` (`:94`) hard-clamps `SummarizePercent ≤ 95` and forces
`ShedPercent ≤ SummarizePercent`, so a partial `config.json` cannot produce
summarize-before-shed. Good defensive design.

Thresholds are measured against **body tokens** — after the stable cached prefix
(`autocompact/meter.go:50`: `BodyTokens = lastPromptTokens - cachePrefixTokens`).
A huge but fully-cached system prompt never triggers compaction. This is better
than a naive percentage-of-window.

**Stage 1 — shed (≥60%)**, `compaction_shed.go:41` `ShedSupersededToolResults`:
walks backwards so the first sighting of a `(tool, args)` key is the newest;
replaces the `FunctionResponse` payload with a short stub, preserving the
call/response pairing shape required by Anthropic and OpenAI-Responses
validators. `shedMinBytes = 400` — below that, shedding costs more than it
reclaims.

**Stage 2 — summarize (≥90%)**, `BuildSummarizedEvents` (`compaction_shed.go:316`):
keeps `events[0]` if it is the initial context (`:391`), then **user text
messages only**, newest-first within a 20k-token budget, restored to
chronological order, then one synthetic `compaction-summary` event. **All tool
calls and tool results are dropped** (`:311-315`).

`advanceToCleanBoundary` adjusts the split so a `FunctionCall` and its
`FunctionResponse` never straddle the boundary.

**Compaction is not recoverable.** `rewriteEvents` (`store.go:1325`) overwrites
`events.jsonl` via temp-file + rename. The pre-compaction event list is gone.
There is no compaction entry recording what was dropped, no inverse operation,
and no branch is created.

### pi

`DEFAULT_COMPACTION_SETTINGS = {enabled:true, reserveTokens:16384,
keepRecentTokens:20000}` (`compaction.ts:126`). Two thresholds, not one:
**blocking** at `window - reserveTokens`, **background** at
`window - reserveTokens - backgroundTokens` (32768). A generation that compacted
starts no other compaction (`generation.ts:54`).

`selectCut` (`compaction.ts:250`) walks back accumulating tokens until
`keepRecentTokens`, then cuts at the first **candidate** — an entry whose
contribution starts with a user or assistant message, never a tool result, and
never a user entry that a result of the preceding assistant's calls still
follows (`:270-289` cross-checks toolCall/toolResult IDs). This is a *semantic*
boundary rule; pi-go uses an index split plus an adjacency adjustment.

Summarization demands rigid sections (`## Goal / ## Constraints & Preferences /
## Progress / ## Key Decisions / ## Next Steps / ## Critical Context`) wrapped in
`<summary>` tags (`compaction.ts:55`), with `cacheRetention: "none"` on the
summarization call (`:167`) so the summary does not poison the cache.

**Compaction is logged, not destructive.** A `compaction` entry records
`firstKeptEntryId` and `tokensBefore`; raw entries stay on disk forever.

`branch-summarization.ts` (382 LOC) summarizes the branch being *abandoned*
when navigating the tree, so context is not lost (`:1-6`). It tracks
`readFiles`/`modifiedFiles` via `extractFileOpsFromMessage`.

### Review — compaction

pi-go's shedding stage is **better than pi's** in one specific way: it
preserves call/response pairing shape, which both Anthropic and OpenAI-Responses
validators require, and the `shedMinBytes` guard avoids pathological
no-op sheds. pi has no shed stage at all — it jumps straight to an LLM
summary.

pi-go's measuring-against-body-tokens rather than percentage-of-window is also
more portable across models with different cache behavior.

pi wins decisively on **recoverability**. A compaction entry is an append-only
record; pi-go's `rewriteEvents` destroys evidence. The minimum viable
improvement is cheap: write a marker event recording `eventsDropped` and
`tokensBefore` before rewriting. The correct answer is `context_edit` — an
append-only record that changes only the projection. pi-go's tool-result stubs
are already projection-only in spirit; they just aren't persisted as such.

## 1.4 Crash mid-turn

This is pi-go's most significant gap.

### pi-go: replay the JSONL, nothing more

`readEvents` (`store.go:1113`) splits on `\n` and unmarshals per line — **any
unparseable line returns an error and the whole load fails** (`:1136`). A
partial final line from a `SIGKILL` mid-write poisons the session permanently.

`appendEventToFile` (`store.go:1096`) is a raw `O_APPEND` write with **no
fsync** and no length prefix. Power loss can lose or tear the tail.

`AppendEvent` (`store.go:416`) drops `event.Partial` events (`:422`), so
streamed deltas never reach disk. It updates memory, appends the line, then
appends to ATIF (non-fatal), then rewrites `meta.json`, then best-effort
updates the branch head. A crash between those leaves `events.jsonl` ahead of
ATIF and `meta.json`.

**No mid-turn recovery exists.** A tool call whose result was never appended
leaves a dangling `FunctionCall` in the transcript. The next `Run` hands that
to the model as-is — the model is told it called a tool, and told nothing about
what happened. There is no `failToolCallsFromTruncatedMessage` equivalent and no
"fail unreconciled tool calls on load" pass.

### pi: commit-before-execute

`packages/durable` commits the tool's final arguments **and** its replay policy
before the first byte of tool output (`harness/tool.ts:85-90`):

```ts
await runtime.commit(async (tx) => {
  const slot = toolSlot(await tx.doc(LiveDoc, runtime.conversationId), runtime.taskId);
  if (slot !== undefined) slot.status = "running";
  const intent = { phase: "execute", arguments: final, replay: tool.replay ?? "unsafe" } as const;
  return { status: "running", checkpoint: intent };
}, context);
await run(runtime, call, tool, final, context);
```

On restart the `execute` phase is entered — and **only recovery enters it**
(`tool.ts:48`). `tool.ts:93-111`:

```ts
execute: async (task, runtime, context) => {
  const { arguments: args, replay } = task.state.checkpoint;
  const call = await readCall(runtime, task.input, context);
  const tool = (await runtime.agent(context)).tools.find(each => each.name === call.name);
  if (replay === "safe" && tool?.replay === "safe") {
    return run(runtime, call, tool, args, context);   // provably re-runnable
  }
  const message = `Tool ${call.name} was interrupted and may have partially run`;
  return settle(runtime, call, { status: "failed", message }, ...);
}
```

The gate is a **logical AND of the stored policy and the current policy** — so
upgrading a tool from `never` to `safe` retroactively makes past interruptions
replayable, and downgrading does the reverse.

The guarantee:

> If a tool's effect is unknown after a crash, the transcript is **never** left
> silently wrong. Either it is provably re-runnable and it re-runs, or the
> result records "interrupted and may have partially run" as an error the model
> sees.

**Storage-level repair.** `storage/jsonl/storage.ts:797-803` detects a missing
trailing `\n` and actively truncates to the last complete line. Invalid UTF-8 in
a *complete* line throws `JsonlCorruptionError` (`:813`) — loud, not silent.

`LiveDoc` (`live.ts:43`) holds presentation state — run status, throttled
partial text, tool slots, compaction status — and is itself checkpointed, so the
TUI rehydrates the in-flight round after a crash. The rendering of a crash is
durable.

**Concurrency contract.** `ReadAfterWrite` — "a transaction read a table after
its first table write" — is a documented, enforced serializability rule
(`errors.ts`). `StorageRejected` is all-or-nothing. `ConversationBusy` makes the
inbox a queue rather than a race. pi-go's `FileService` holds one
`sync.RWMutex` for the whole process (`store.go:115`), which is correct but
coarse and has no cross-process story.

### The durable caveat

`packages/durable` is **not on the production coding-agent path**.
`grep -rn "@earendil-works/pi-durable" packages/coding-agent/src/` returns hits
only under `src/experimental/` (`experimental/vacation/*`,
`experimental/client-tui-chat.ts`). Production uses
`core/session-manager.ts` + `core/agent-session.ts` +
`packages/agent/src/agent-loop.ts`.

So pi ships **two** session systems: the plain JSONL one (production) and the
durable one (experimental). The durable design is the reference, but the claim
"pi has crash-safe sessions" needs that caveat.

### Review — crash recovery

This is the headline gap, and it is asymmetric: pi-go has nothing, pi has a
designed solution that is not yet on its own default path.

**Can pi-go retrofit it?** Not inside ADK — ADK owns tool dispatch. But a
journaling decorator around every tool is feasible and is the natural place:
`internal/tools/registry.go:302` `coercingTool` already wraps every tool. A
wrapper that writes `{tool, args}` intent before delegating, and on next
session load fails any `FunctionCall` with no matching `FunctionResponse`, would
close most of the distance without forking ADK.

**The 5-line fix available today** is the torn-tail repair. `storage/jsonl` in
pi is ~5 lines; porting it to `readEvents` turns a permanently-poisoned session
into a truncated one. Do this first.

## 1.5 Tool replay policy

`AgentTool.replay?: "never" | "safe"` (`packages/agent/src/types.ts:487-488`) is
documented as *"Recovery policy for an effect whose durable intent exists but
whose outcome is unknown."*

**pi-go has no equivalent.** There is no way for a tool to declare "re-running
me is fine." `bash`, `write`, and `edit` are all ambiguous after a crash, and
pi-go has no mechanism to distinguish them from `read`.

This is why §1.4 cannot be fully solved in pi-go without a per-tool declaration.
Adding a `ReplayPolicy() tool.ReplayPolicy` method to the `tool.Tool` interface
would be cheap; nothing would consume it until the journaling decorator lands.

## 1.6 ATIF — interchange format

`internal/atif/types.go:9`: `const SchemaVersion = "ATIF-v1.6"`, "Agent Trajectory
Interchange Format."

`Trajectory{schema_version, session_id, agent, steps[], notes, final_metrics,
continued_trajectory_ref, extra}`; `Step{step_id, timestamp, source, message,
model_name, reasoning_effort, reasoning_content, tool_calls[], observation,
is_copied_context, metrics, extra}`; `Observation{results[]{source_call_id,
content, subagent_trajectory_ref}}`.

`link.go:38` writes `subagent_trajectory_ref` linking child sessions by relative
path — this is how subagent trees are reconstructed.

`Writer` (`writer.go:22`) keeps the whole trajectory in memory and **atomically
rewrites the whole JSON file on every `AppendEvent`** — O(n²) write
amplification, and the file is a derived artifact, not a source of truth.

**Is it consumed externally?** No. Every consumer is inside pi-go:
`internal/eval/metrics.go:182`, `internal/eval/inventory.go:22`, and the ATIF
tests. No external importer, no published schema repo, no conformance suite.

pi has no equivalent artifact — its trajectory story is the JSONL entry tree plus
`usage` entries, which the eval harness reads directly.

### Review — ATIF

ATIF is a genuinely better artifact than what pi has: a typed, versioned schema
with per-step metrics and a documented subagent linkage. If interchange is a
goal, exporting pi's `entries` table to ATIF is modest work and would give
pi-go the better format of the two.

The O(n²) rewrite is worth fixing regardless: appending to the ATIF file rather
than rewriting it costs a small change and removes a real write-amplification
cost from every session turn.

## 1.7 Fork / branch / clone

### pi-go

`internal/session/branch.go` implements in-session branching only:

- `CreateBranch` (`:29`) copies events `[0..head]` into
  `branches/<name>/events.jsonl`, records `BranchInfo{Name, Head, Parent, ForkPoint}`
- `SwitchBranch` (`:76`) saves current events, loads the target, **and rewrites
  the root `events.jsonl`** (`:112` — "Also write to main events.jsonl so ADK
  sees the right events")
- `loadBranches` (`:176`) defaults to a single `"main"`
- `saveBranches` (`:209`) atomic temp+rename

Switching a branch **destructively rewrites the main timeline**. There is no
rebase, no merge of two branches. `merge.go` merges *remote directories*, not
branches.

### pi

Plain session manager: file-level fork via `forkFrom` (`:1815`), plus in-file
navigation by `parentId` tree + `getTree()` → `SessionTreeNode{entry, children,
label?}` (`:200`). Moving to another point does not copy; the abandoned branch
is summarized into a `branch_summary` entry.

Durable goes further — `session/forks.ts:26`
`prepareForkDocumentCopies` copies **documents** in two passes: the source
conversation's rewindable docs `asOf` the fork commit seq, then the parent's
current latest-history docs. Dedupe by `addressId`; a duplicate throws
`Fork selects multiple source documents for <kind>` (`:87`). Semantics are
declared per-document: `AgentDoc` is `fork: "asOf"` — a forked conversation
starts from the agent configuration **as of the fork entry**, not as of now.

### Review — branching

pi-go's `SwitchBranch` rewriting the root timeline is a design that makes branch
state fragile for two reasons: the integer head indices go stale after
compaction, and "which branch am I on" is stored outside the log. `parentId`
per entry solves both, costs bytes, and additionally gives non-destructive
compaction and fork-to-new-file for free.

The durable per-document fork semantics (`asOf` / `current` / `initial`) are
more sophisticated than anything pi-go needs today, but the *idea* — that what a
fork inherits is a property of each piece of state, not one global choice — is
the right shape.

## Review summary — agent runtime & sessions

**pi-go's strengths**: the two-stage shed-then-summarize compaction with
pairing-preserving stubs and a no-op guard; body-token measurement that ignores
the cached prefix; destructive atomic rewrites that never leave a half-file;
host provenance in session metadata; and an honest ADK integration that composes
the after-tool chain correctly.

**pi-go's weaknesses, ranked**:

1. No crash recovery — a torn tail permanently poisons a session
2. No session schema version or migration path
3. No steering, no truncation guard, no tool-delta reconciliation
4. Destructive compaction with no record of what was dropped
5. Integer branch heads invalidated by compaction
6. No `replay` policy on tools, which blocks any correct recovery design

**pi's strengths**: commit-before-execute with a replay policy; `parentId`
materialized tree; versioned schema with real migrations; `context_edit` as a
non-destructive projection primitive; semantic compaction cut selection;
durable documents with migrate hooks and per-document fork semantics.

**pi's weaknesses**: `durable` is not on the production path; the plain session
manager is what actually runs and has no crash recovery either; `agent-session.ts`
at 4,348 LOC is a monolith; and `conversation` scope plus `history` semantics
plus `fork` semantics on every doc is a lot of machinery for a code agent.

**The honest framing**: pi's durability work is better *designed* but not yet
*shipped*. pi-go's session layer is simpler and, apart from the torn-tail
failure, more robust in the paths it does have. The gap is real but narrower
than "pi is durable and pi-go isn't" — it is "pi has a durable design that
isn't on by default, and pi-go has no design at all."