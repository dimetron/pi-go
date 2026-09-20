# Requirements

## Questions & Answers

### Q1 — Which feature does "async-input-subagent" name?

**Asked:** The name fits at least four readings: (A) async spawn + steer,
(B) async spawn only, (C) human→subagent input from the TUI, (D) non-blocking
parallel batches. Which one? If (A), model-facing only or also user-facing?

**Answer:** **A** — async spawn + steer.

**Interpretation:** v1 adds two capabilities to the subagent system:

1. **Async spawn** — a subagent can be started in the background so the calling
   turn does not block on it.
2. **Steer** — the caller can send *additional input* to that subagent while it
   is still running.

The user-facing sub-question (TUI input into a live subagent) was not answered;
treated as a separate question (Q5 below).

**Evidence that this is a real gap today:**

- `SubagentInput` (`internal/tools/subagent.go:31-42`) has no background field;
  every mode handler blocks until the child's event channel closes
  (`subagent.go:252`, `:411`).
- `SpawnInput.Background` (`internal/subagent/types.go:17`) is declared but
  **never read** — dead at runtime.
- Once a `parallel` batch launches, the model cannot steer it
  (`specs/harness/deepseek/harness-review.md:197`).
- `Orchestrator.List()/Get()/Cancel()` exist for *observing* and *stopping* a
  running agent (`orchestrator.go:713,761,806`) but there is **no way to deliver
  input to one**.
- `docs/DESIGN-COMPARISON.md:140-141,153,396` records TS `steer`/`follow_up`
  and "Missing in Go: stdin/stdout RPC parity (steering, queue control…)";
  `internal/pirpc` accepts `set_steering_mode`/`set_follow_up_mode` as no-ops.

---

### Q2 — Which agent backends must accept input while running?

**Asked:** pi-binary / codex / ACP — which in v1? Options were (i) pi-binary
only, (ii) pi-binary + codex, (iii) all three, (iv) backend-agnostic surface with
pi-binary wired first.

**Answer:** **pi-binary only.**

**Consequences:**

- In scope: `explore`, `plan`, `designer`, `task`, `quick-task`, `worker`,
  `code-reviewer`, `spec-reviewer`, `memory-compressor` — everything routed to
  `o.spawner.Spawn` (default branch of `dispatchSpawn`,
  `internal/subagent/orchestrator.go:575-584`).
- Out of scope: `codex`/`codex-review` (`dispatchCodex`) and
  `claude`/`gemini`/`cursor`/`copilot`/`agy` (`dispatchACP`).
- **Follow-up specs required** for codex (`turn/steer` /
  `turn/start {threadId, input}`) and ACP (repeat `session/prompt` on a retained
  connection). These must be recorded in the plan's Constraints section, not
  silently dropped.
- Unsupported backends must degrade with an explicit result, not a crash
  (see Q6).

**Feasibility evidence — `pi --mode rpc` is already a steerable stdin server:**

- Framing: newline-delimited JSON on stdin, one object per line, max
  `maxCommandBytes = 32<<20` (`internal/pirpc/rpc.go:149,159`); malformed lines
  reply with an error and the loop **keeps serving** (`:172-177`); exits on stdin
  EOF or ctx done (`:161-166,180-183`).
- Commands (`:121-135`, dispatched `:188-257`): `prompt`, `abort`, `get_state`,
  `get_available_models`, `get_session_stats`, `get_commands`, `get_messages`,
  `set_model`, plus accepted no-ops incl. **`set_steering_mode`** and
  `set_follow_up_mode`.
- `prompt` replies then runs the turn on a goroutine (`:190-196`), so **stdin
  keeps being read while a turn streams**. `abort` (`:198-216`) cancels the
  single in-flight turn.
- Session id is fixed at construction (`:63,110`) and never mutated — the natural
  key for "the same child session across multiple inputs".

---

### Q3 — What tool surface does the model get?

**Asked:** (i) one new tool `subagent_input` reusing `subagent`'s shape for async
spawn; (ii) a full handle triple `subagent_start/input/wait/kill`; (iii) two new
modes on `subagent`; (iv) `subagent` gains `background` plus separate handle verbs.

**Answer:** **Add one tool: `subagent_input`.**

**Consequences:**

- Exactly **one** new tool name: `subagent_input`.
- The existing `subagent` tool gains an async spawn affordance; its four existing
  modes and their blocking behaviour stay as they are.
- No `subagent_wait`, no `subagent_kill`, no `subagent_list` tool.
- **`subagent_input` is both the steer and the poll verb** (resolved in Q4).
- Handle-based verbs must follow the `BashControlTools` construction precedent
  (`internal/tools/bash.go:244-276`): a **separate tool list**, appended at each
  assembly site (`interactive.go:418-422`, `cli.go:694-698`,
  `piagent/agent.go:235-239`, `acp/server/runtime.go:452-457`,
  `eval/inventory.go:69-73`). The bash description is kept honest about its
  control tools by a test assertion (`bash_control_test.go:20-43`) — the same must
  hold for the `subagent` description and `subagent_input`.
- **Correction (research finding):** the `BashControlTools` doc comment claims it
  is "only worth advertising when something can background"
  (`bash.go:244-249`), but **every production call site appends it
  unconditionally**. So bash is a precedent for *separate construction*, **not**
  for gating. `CoreOption` cannot gate tools at all — it only injects shared
  state (`registry.go:18-40`). For a genuinely conditional tool, follow the
  memory/palace/LSP pattern: a constructor that returns `(nil, nil)` plus a
  caller-side `if` (`mem_search.go:209-212`, `palace/tools.go:9-12`,
  `lsp.go:262-263`).
- `Orchestrator.Cancel` already exists (`orchestrator.go:806`) and needs no new
  tool; whether the model gets any stop affordance is Q6.

---

### Q4 — How does the model get an async agent's result back?

**Asked:** (i) `subagent_input` doubles as attach/poll with an incremental
cursor, returning `{running, output}`; (ii) also auto-inject the final result as a
user-role message on the next turn via the `PreTurnHook` seam; (iii) poll returns
the whole transcript every time, no cursor. And: on a finished agent, return the
final result then forget the handle, or keep it retrievable?

**Answer:** **(i) — `subagent_input` is the poll, with incremental reads.**

**Specified semantics:**

- An async spawn returns early with an in-band "still running" result carrying
  `agent_id` and a `note` naming `subagent_input`, mirroring
  `BashOutput{Running:true, Handle:"bg_N", Note:…}` (`bash.go:58-63`,
  `bash_supervisor.go:381-407`). It is a **successful** tool result, not an error.
- Calling `subagent_input` **with** a `message` delivers that message to the
  running child **and** returns the output accumulated since the caller's last
  read.
- Calling `subagent_input` **without** a message is a read-only attach: it
  returns output accumulated since the last read, plus current status.
- Output is **incremental**, with the read cursor owned by the **agent**, not by the
  caller — the semantics of `stream.since(off) (string, next int64, dropped int64)`
  (`internal/tools/bash_stream.go:64-75`) and `bashProc.outCur`/`errCur`
  (`bash_supervisor.go:154-158`). Dropped-bytes accounting must be reported the
  way `droppedNote` does (`bash_supervisor.go:451-460`).
  **Cursor ownership was resolved in design:** a tool call carries no caller
  identity, so "the caller's last read" cannot be tracked per invocation. One cursor
  per agent is advanced by `Steer`/`Attach`, so overlapping tool calls share it and
  the second sees only what arrived after the first. A finished agent's final result
  is replayed from `last` outside the cursor, which is what makes repeated polls
  idempotent.
- A wait/park duration is required so a poll can block instead of busy-looping —
  same shape as `bash_wait`'s `wait_sec` with `maxBashWait = 60s` and a
  60s default when omitted (`bash.go:118,103-110,251-258`), and
  `awaitChange`'s select on done/stream/timer (`bash_supervisor.go:640-649`).
- **Rejected for v1:** auto-injecting results into the conversation.
  Consequence: the model must poll, and if it never polls, the result is never
  seen. This is an accepted limitation. It also means the feature must not rely
  on the otherwise-available `PreTurnHook` seam (`internal/agent/agent.go:386`,
  used only by autocompact) — no session/compaction changes in v1.
- Handle lifetime after completion: **NOT YET DECIDED** (asked, unanswered).
  Bash forgets the handle after the final read (`readOutput`,
  `bash_supervisor.go:633`). See Q6.

---

### Q5 — Where does an async subagent appear in the TUI, and can the user
interact with it?

**Asked:** (i) model-only / invisible to the user; (ii) keep the existing repaint
guarantee; (iii) (ii) plus user affordances (`/subagents` while running, a key to
cancel a selected agent); (iv) (iii) plus user-facing steer. And: may the user
stop a background subagent?

**Answer:** **(i) — model-only.** No new TUI affordances in v1.

**Consequences:**

- No new slash commands, no new keybindings, no per-agent cancel UI, no
  user-facing steer. Reading (C) from Q1 is explicitly a **separate future spec**.
- `Orchestrator.Cancel(agentID)` (`orchestrator.go:806`) keeps its current
  status: implemented, tested, **no production caller**. v1 does not wire it.
- Events from an async agent continue to flow through the existing
  `SubagentEventCallback` / `AgentSubEvent` channel into the subagent card, which
  already binds on `kind == "spawn"` (`internal/tui/agent_loop.go:1868-1874`).
  The `bash:` kind prefix (`agent_loop.go:1776,1812`) shows the channel already
  carries non-agent async traffic.
- **Accepted defect for v1:** the TUI's 150 ms repaint ticker runs only while
  `m.running` (`internal/tui/tui.go:756-771`), so an agent that outlives the
  parent turn has **no scheduled repaint**; its sidebar row (`sidebar.go:518`)
  and card will look stale until another `Update` occurs. Likewise slash commands
  are dropped while a turn runs (`tui.go:726-729`), so `/subagents` cannot inspect
  a busy turn. Both must be recorded as known limitations, not fixed here.

---

### Q6 — Is steered input injected into the child's *current* turn or queued as
its *next* turn?

**Asked:** (i) queue as the child's next turn, serialized by the parent;
(ii) true mid-turn injection (requires changing the child); (iii) `abort` then
re-prompt.

**Answer:** **(i) — queue as the child's next turn.**

**Consequences:**

- `subagent_input` deposits the message in a per-agent queue. A per-agent writer
  goroutine sends `{"type":"prompt","id":…,"message":…}` to the child's stdin
  **only after** it observes `agent_settled` for the previous turn.
- Steering means "your next turn will also say X": the child finishes its current
  turn first. This is the honest contract and must be stated in the tool
  description — the tool reports *queued*, not *delivered and acted upon*.
- **Why mid-turn injection is out of scope:** `pi --mode rpc` has no injection
  path. `prompt` starts a new turn (`rpc.go:190-196`); `set_steering_mode` and
  `set_follow_up_mode` are accepted **no-ops** (`rpc.go:244-248`). Naively sending
  a second `prompt` mid-turn runs a **concurrent** turn on the same ADK session
  and clobbers the single `s.cancel` slot (`rpc.go:263-266`), after which `abort`
  reaches only the newer turn. Serializing in the parent is the only correct
  option without changing the child.
- This matches pi-go's own precedent: `pendingPrompts` are never merged into a
  running turn — `startNextPrompt` no-ops while `m.running`
  (`internal/tui/agent_loop.go:916-918`), draining only from `handleAgentDone`
  (`:1942-1943`), each becoming a fresh ADK turn.

---

### Q7 — Handle lifetime, and does an async agent outlive its spawning turn?

**Asked:** (1) should a background subagent survive parent-turn end and the
user's Esc/Ctrl+C? (2) after completion, return the final result once then report
the handle spent (bash's behaviour), or return it idempotently? (3) what bounds
the number of background agents?

**Answer:** **Accepted the recommended defaults for all three.**

**1. Survival — yes for turn end and Esc/Ctrl+C; no beyond session end.**

- A background subagent **must not** be killed when the spawning turn ends or the
  user interrupts the parent turn. Today's behaviour would kill it: `procCtx`
  descends from the spawn ctx (`internal/subagent/spawner.go:243`) and
  `exec.CommandContext` kills the process group on cancellation; the TUI interrupt
  path cancels the parent turn context (`tui.go:1213-1240` →
  `agent_loop.go:823-858`).
- The fix uses the precedent the bash supervisor already established:
  `context.WithoutCancel(ctx)` for detached work
  (`internal/tools/bash_supervisor.go:246`), which is exactly why a background
  bash command is not killed by turn end.
- It **must still die on session end**: orchestrator `Shutdown` /
  `ShutdownWithTimeout` (`orchestrator.go:841-883`) and `KillAll`-style teardown
  (`bash_supervisor.go:569-581`, called from `internal/cli/interactive.go:48-53`,
  `internal/cli/cli.go:748-751`, `piagent/agent.go:229`,
  `internal/acp/server/runtime.go:461-466`). Nothing outlives the session.
- `SpawnInput.Background` (`internal/subagent/types.go:17`) is currently **dead**
  (never read). This feature is where it becomes live.

**2. Handle lifetime — idempotent, not spent-after-one-read.**

- Unlike bash (`readOutput` forgets the handle after the final read,
  `bash_supervisor.go:633`), a **finished** async agent keeps returning its final
  result on subsequent `subagent_input` calls until the entry is evicted. A model
  that polls twice must not see a confusing "unknown handle" error.
- Bounded by the existing orchestrator eviction policy: `maxCompletedAgents = 50`,
  oldest-by-`FinishedAt` (`orchestrator.go:33-36,681-710`). A still-running async
  agent is never evicted.

**3. Capacity — the existing pool is the bound, but async fails fast (revised in Q11).**

- Async spawn is bounded by the same semaphore as a synchronous spawn:
  `o.pool.Acquire(ctx)` (`orchestrator.go:443`), sized by
  `PI_SUBAGENT_CONCURRENCY` (default `DefaultPoolSize = 3`, ceiling 64;
  `concurrency.go:26-49`).
- **Consequence to design around:** the pool slot is released only when the event
  stream closes — `defer o.pool.Release()` inside `forwardAgentEvents`
  (`orchestrator.go:611`). A detached agent therefore occupies a pool slot for its
  whole life. This is intended backpressure.
- **Superseded by Q11:** an async spawn does **not** block on `Acquire`. It makes a
  non-blocking attempt and returns `status: "no_capacity"` when the pool is full.
  Synchronous spawns keep blocking exactly as today.
- Because a detached agent's slot is held long, it gets a **shorter absolute
  timeout** than a synchronous spawn. Defaults today are
  `DefaultAbsoluteTimeout = 20m` / `DefaultInactivityTimeout = 5m`
  (`internal/subagent/timeout.go:22,32`), resolved by `ResolveTimeout`
  (`:44-77`) and overridable per agent via frontmatter and per call via
  `input.Timeout` (`orchestrator.go:461-464`). The exact async default is a design
  detail to pin in `design.md`.
- Also relevant: only the pi-binary spawner owns an `InactivityTimer`
  (`spawner.go:272-299`); ACP/codex paths ignore it — moot here since v1 is
  pi-binary only.

---

### Q8 — How is async spawn requested, and which agents may be detached?

**Asked:** (i) a `background: true` flag on **single mode only**; (ii) a
per-item `background` on `parallel`/`chain` too; (iii) a new `async` mode in
`detectMode`. Also: all pi-binary agents, or only non-worktree ones?

**Answer:** **(i) — `background: true` on single mode only. Smallest surface.**

**Consequences:**

- Request shape: `subagent{agent: "worker", task: "…", background: true}`.
  `parallel` and `chain` remain fully synchronous; each may gain async later
  without changing this shape.
- `detectMode` (`internal/tools/subagent.go:186`) is **unchanged** — it keeps
  resolving chain > parallel > single by field presence, so no new ambiguity.
- A **mixed batch** (some entries handles, some `AgentResult`s) is impossible by
  construction, which keeps `SubagentOutput.Results []AgentResult`
  (`subagent.go:57-62`) coherent.
- **Schema safety:** adding `Background bool \`json:"background,omitempty"\`` to
  `SubagentInput` does not add it to `required` and input schemas are open
  (`internal/tools/registry.go:117-143`; `jsonschema.For` marks a field required
  only when it lacks `omitempty`/`omitzero`). It **does** appear in the
  model-facing declaration, so the `subagent` tool description must explain it.
  Mirror the guard test that keeps the bash description honest about its control
  tools (`internal/tools/bash_control_test.go:20-43`).
- **Backend restriction:** only agents routed to `o.spawner.Spawn` — the default
  branch of `dispatchSpawn` (`orchestrator.go:575-584`). `codex`/`codex-review`
  (`dispatchCodex`) and `claude`/`gemini`/`cursor`/`copilot`/`agy` (`dispatchACP`)
  cannot be detached in v1 and must return an explicit, model-actionable result
  rather than a crash (see Q9 and the Q2 follow-up note).
- **Worktree caveat (accepted, must be a plan constraint):** a detached agent
  configured for a worktree holds that worktree for its whole life — worktree
  lifecycle is `orchestrator.go:518-530`, and cleanup is deliberately **not** done
  by `forwardAgentEvents` (`:636-641`; `SkipCleanup` is read nowhere). Repeated
  background spawns could accumulate checkouts. v1 does not add worktree GC; this
  is recorded as a known limitation.

---

### Q9 — What does `subagent_input` return for an unknown / gone agent, and what
happens at shutdown?

**Asked:** (i) error, naming live handles like bash; (ii) a structured
model-actionable result. And at session end: drop queued-but-undelivered steer
messages, and cancel running async agents as today?

**Answer:** **(ii) — structured result; and yes on both shutdown behaviours.**

**Consequences:**

- `subagent_input` **never returns a Go error for a missing agent.** It returns a
  discriminated status field, e.g. `status: "running" | "completed" | "failed" |
  "unknown"`. `"unknown"` covers never-existed, evicted (> `maxCompletedAgents =
  50`, `orchestrator.go:33-36,681-710`), and not-yet-tracked.
- Rationale is the codebase's own: spawn failures are returned as
  `AgentResult{Status:"failed"}`, "never as an error: the model can act on the
  former" (`internal/tools/subagent.go:381-383`). Only *malformed* calls (missing
  `agent_id`) are errors, mirroring `bash_wait`'s "handle is required (running:
  …)" (`internal/tools/bash.go:251-258`).
- Contrast with bash, which errors on a spent handle with
  `unknown handle %q (running: …)` (`bash_supervisor.go:589-592`) — this feature
  deliberately diverges, because bash handles are single-read (`:633`) whereas
  async agent results are idempotent (Q7.2).
- **Shutdown:** queued-but-undelivered steer messages are **dropped** — never
  flushed into a dying child. Any running async agent is cancelled exactly like a
  synchronous one: `ShutdownWithTimeout` cancels every `"running"` agent and marks
  it `canceled` (`orchestrator.go:848-883`). **No change to that code path.**

---

## Consolidated Requirements

### In scope (v1)

| # | Requirement |
|---|---|
| R1 | `subagent` single mode accepts `background: true` and returns immediately with a handle (`agent_id`) plus a "still running" note, instead of blocking. |
| R2 | A new tool `subagent_input{agent_id, message?, wait_sec?}` both **steers** (delivers `message` to the running child) and **attaches/polls** (returns output accumulated since the caller's last read). |
| R3 | Steered input is **queued as the child's next turn**, serialized by the parent after the child's previous turn settles. The tool reports *queued*, not *acted upon*. |
| R4 | A detached agent **survives** parent-turn end and the user's Esc/Ctrl+C, but **dies on session/orchestrator shutdown**. |
| R5 | A finished async agent returns its final result **idempotently** until evicted (≤ 50 completed entries, oldest-by-`FinishedAt`). |
| R6 | Async spawn is bounded by the existing `PI_SUBAGENT_CONCURRENCY` pool (default 3); its slot is held for the agent's whole life. Full pool ⇒ **non-blocking** `status: "no_capacity"`, never a stall. Synchronous spawns keep blocking. |
| R7 | Missing/gone agents return a **structured** `status: "unknown"` result, never a Go error. |
| R8 | **pi-binary backends only.** codex/ACP return an explicit model-actionable "not steerable" result. |
| R9 | **Model-only.** No new TUI affordances, slash commands, keybindings, or user-facing steer. |
| R10 | Incremental output reads with dropped-byte accounting, reported to the model. |
| R11 | Gated by **`PI_SUBAGENT_ASYNC`, env-only, default off**. Flag off ⇒ no `--mode rpc` child, `subagent_input` unregistered, `background: true` returns a "disabled" result. Flag on ⇒ protocol chosen **per spawn**. |

### Out of scope (v1) — recorded, not dropped

| # | Deferred item | Why |
|---|---|---|
| D1 | **codex steering** (`turn/steer{threadId, expectedTurnId, input[]}`, `turn/start{threadId, input[]}`) | Needs `Session.finish`→`client.close()` (which kills the process group, `session.go:586`, `client.go:349-353`) reworked, `threadID` exposed, and multi-turn `turnID` tracking (`session.go:72,242-246`). Own spec. |
| D2 | **ACP steering** (repeat `session/prompt` on a retained connection) | Needs `RunningSession` to retain `conn` + `io.WriteCloser` stdin, stop closing stdin (`session.go:191,210`), stop `cmd.Wait()`ing after the first prompt, and expose the session id. Own spec. |
| D3 | **Mid-turn injection** into a running child | `pirpc` has no injection path; `set_steering_mode`/`set_follow_up_mode` are no-ops (`rpc.go:244-248`). Requires changing the child, not the parent. |
| D4 | **User-facing steer / per-agent cancel UI** | Reading (C) of Q1; a distinct UI feature. `Orchestrator.Cancel` already exists (`orchestrator.go:806`) and stays unwired. |
| D5 | **Auto-injecting async results into the conversation** | Would use the `PreTurnHook` seam (`internal/agent/agent.go:386`, autocompact-only). Rejected for v1 to avoid touching session/compaction. Cost: a model that never polls never sees the result. |
| D6 | **Async spawn in `parallel`/`chain`** | Q8 chose single-mode-only; the shape leaves room. |
| D7 | **Worktree GC for detached agents** | Repeated background spawns could accumulate checkouts; cleanup is not done by `forwardAgentEvents` (`orchestrator.go:636-641`). |
| D8 | **Live TUI repaint for a detached agent** | The 150 ms ticker runs only while `m.running` (`tui.go:756-771`); a detached agent's card/sidebar row look stale until another `Update`. Accepted in Q5. |
| D9 | **Fixing the sidebar status defect** | `agentRow` handles `"done"` but the orchestrator emits `completed`/`canceled`/`timeout`, so finished agents render a dim `∙` instead of `✓` (`sidebar.go:590-603`). Pre-existing; out of scope. |
| D10 | **`SubagentConfig` in `config.json`** | Q10 chose env-only. Migrating `PI_SUBAGENT_*` into a config section (with `PI_*` kept as the override) is a coherent follow-up, not part of this spec. |
| D11 | **Generic feature-flag system** | Still only proposed (`GAP_ANALYSIS.md:660-666`); `Config.Tools` is dead (`config.go:110`). Out of scope. |

### Non-Regression Criteria

- Given `PI_SUBAGENT_ASYNC` unset, when any `subagent` call is made (single,
  parallel, or chain), then children are spawned as `pi --mode json` with the
  prompt in argv exactly as today, and no `--mode rpc` child is created.
- Given `PI_SUBAGENT_ASYNC` unset, when the tool list is assembled at any entry
  point, then `subagent_input` is absent from it.
- Given `PI_SUBAGENT_ASYNC` unset, when `subagent{background: true}` is called,
  then the result carries an explicit "disabled" status and no process is
  detached.
- Given `PI_SUBAGENT_ASYNC=1`, when a **synchronous** `subagent` call is made,
  then the child still uses `--mode json` (protocol is per-spawn, not global).

### Acceptance Criteria (Given / When / Then)

**Async spawn**
- Given `subagent{agent:"worker", task:"…", background:true}`, when called, then
  it returns promptly with `running: true`, a non-empty `agent_id`, and a note
  naming `subagent_input` — and the child keeps running after the tool returns.
- Given a `background: true` call for a `codex` or ACP-backed agent name, when
  called, then the result carries an explicit "not steerable in this build"
  status and no process is detached.
- Given the concurrency pool is saturated, when a `background: true` spawn is
  requested, then it returns `status: "no_capacity"` **without blocking**, and no
  process is detached.
- Given the concurrency pool is saturated, when a **synchronous** `subagent` call
  is made, then it blocks on `Acquire` exactly as today.

**Steer**
- Given a running async agent, when `subagent_input{agent_id, message:"also do X"}`
  is called, then the message is queued and delivered as the child's **next**
  turn after its current turn settles, and the call reports it as queued.
- Given a running async agent mid-turn, when `subagent_input` is called with a
  message, then the child's **current** turn is not interrupted and no concurrent
  turn is started on the same session.
- Given a completed async agent, when `subagent_input{agent_id, message:"…"}` is
  called, then the message is not delivered and the result reports the terminal
  status.

**Attach / poll**
- Given a running async agent, when `subagent_input{agent_id}` is called with no
  message, then it returns only the output produced since the caller's last read.
- Given output exceeding the retained buffer, when polled, then the result reports
  how many bytes were dropped.
- Given a completed async agent, when polled repeatedly, then each call returns
  the final result — no "spent handle" error.
- Given an unknown or evicted `agent_id`, when polled, then the result carries
  `status: "unknown"` and the call does **not** return an error.
- Given a missing `agent_id`, when called, then it **is** an error naming the live
  agent ids.

**Lifetime**
- Given a background agent still running, when the spawning turn ends, then the
  agent keeps running and its events keep flowing to the event callback.
- Given a background agent still running, when the user presses Esc/Ctrl+C to
  cancel the parent turn, then the agent keeps running.
- Given a background agent still running, when the session shuts down, then the
  agent is cancelled and marked `canceled`, and any queued steer message is
  dropped rather than delivered.

**Non-regression**
- Given a `subagent` call without `background`, when called in single, parallel,
  or chain mode, then behaviour is byte-for-byte unchanged (still blocking).
- Given `detectMode` on any existing input shape, when called, then the resolved
  mode is unchanged.
- Given `PI_SUBAGENT_ASYNC` unset, when the tool list is assembled, then
  `subagent_input` is not present.

**Feature flag**
- Given `PI_SUBAGENT_ASYNC` unset or unparseable, when any async affordance is
  used, then it is off and no error is raised.
- Given `PI_SUBAGENT_ASYNC=1` and `subagent{background:true}`, when called, then
  the child is spawned on `--mode rpc` and the call returns a handle.
- Given `PI_SUBAGENT_ASYNC=1` and a synchronous `subagent` call, when called, then
  the child is still spawned on `--mode json`.
- Given `PI_SUBAGENT_ASYNC=1` and a nested (child-spawned) agent, when the child
  builds its own orchestrator, then it also sees the flag (inherited via the `PI_`
  env allowlist).

---

### Q10 — Can this be a feature flag enabled in config?

**Asked:** (i) new `subagent: {"async": true}` config section; (ii) same with an
explicit `async_enabled` name; (iii) a generic `features: {…}` map. Then narrowed
to: (a) `PI_SUBAGENT_ASYNC` env only, (b) `config.json` only, (c) both.

**Answer:** **(a) — `PI_SUBAGENT_ASYNC` environment variable only. Default off.**

**First, a correction to a premise in the question: pi-go has no feature-flag
system.**

- `grep -rni "experimental|feature|flags"` over `internal/config/*.go` (non-test)
  returns one incidental hit (the word "flag" in a doc comment,
  `env_lookup.go:13`).
- A flags map is only **proposed**, never built:
  `specs/research/003-improvements/GAP_ANALYSIS.md:660-666` ("**Gap**: No feature
  flag system", with a `FeatureFlag` sketch) and
  `specs/research/003-improvements/ARCHITECTURE.md:455,780,798` (listed as
  future/unchecked).
- `config.Config.Tools map[string]any` (`internal/config/config.go:110`) is
  declared but **read nowhere** — no reader exists in the config package.

**The established convention is a per-subsystem `*bool Enabled`, not a flags map:**
`MemoryConfig.Enabled` (`config.go:40`), `PalaceConfig.Enabled` (`:141`),
`CompactorConfig.Enabled` (`:159`), `AutoCompactConfig.Enabled` (`:170`). They are
`*bool` for the reason documented for `RateLimitConfig` (`config.go:64-66`): "a
missing field inherits the built-in default … and an explicit 0 turns that budget
off." Defaults resolve **at the reader**, e.g. `memoryEnabled(cfg)`
(`internal/cli/cli.go:1189-1192`).

**Consequences of choosing (a):**

- New env var `PI_SUBAGENT_ASYNC`, **default off**. Opt-in, not opt-out.
  Rationale: this feature changes the child's protocol (`--mode json` →
  `--mode rpc`), changes process-survival semantics, and holds a concurrency-pool
  slot for a detached agent's whole life — a materially different risk profile
  from `Memory`/`Palace`, which are additive and default-on.
- Read **inside `internal/subagent`**, next to `ConcurrencyFromEnv()`
  (`concurrency.go:26`) and `ResolveTimeout()` (`timeout.go:44`), following their
  defensive style: unparseable or missing → off, never an error.
- **No `SubagentConfig` struct and no `config.json` changes.** Note there is no
  subagent section in config today: the three existing subagent settings are
  env-only (see below).
- Because `PI_` is a forwarded prefix in `DefaultEnvAllowlist`
  (`internal/subagent/environ.go:44`), the flag is **inherited by children**, so a
  nested spawn can also go async. `ChildEnv` (`environ.go:101`) strips and
  rewrites only `PI_SUBAGENT_CONCURRENCY` (`:105-113`), so `PI_SUBAGENT_ASYNC`
  passes through unchanged.
- Works identically in every entry point with no config plumbing: CLI, TUI, ACP
  server, and `piagent`.

**Existing `PI_SUBAGENT_*` variables (the family this joins):** three, all
env-only, none with a `config.json` counterpart.

| Env var | Controls | Default | Resolved by |
|---|---|---|---|
| `PI_SUBAGENT_CONCURRENCY` | How many subagents one process runs at once (semaphore pool size every spawn must `Acquire`) | `DefaultPoolSize = 3` (`orchestrator.go:28`) | `ConcurrencyFromEnv()` (`concurrency.go:26`) |
| `PI_SUBAGENT_TIMEOUT_MS` | Absolute wall-clock cap per subagent | `DefaultAbsoluteTimeout = 20m` (`timeout.go:22`) | `ResolveTimeout()` (`timeout.go:44`) |
| `PI_SUBAGENT_INACTIVITY_MS` | How long a subagent may produce no output before being judged wedged | `DefaultInactivityTimeout = 5m` (`timeout.go:32`) | `ResolveTimeout()` (`timeout.go:44`) |

**Behaviour when the flag is off (must be total non-regression):**

- Children stay on `pi --mode json` exactly as today; **no `--mode rpc` child is
  ever spawned**.
- `subagent_input` is **not registered at all** — the precedent is a separate
  constructor plus a conditional `append` at each assembly site (memory tools:
  `interactive.go:526-535`, `cli.go:915-928`; palace: `palace/tools.go:9-12`;
  LSP: `LSPToolsFor(mgr, LSPOff)` returns `(nil, nil)`, `lsp.go:262-263`). Note
  `CoreOption` **cannot** gate tools — it only injects shared state
  (`registry.go:18-40`).
- `subagent{background: true}` with the flag off returns a **model-actionable
  "disabled" result**, not a Go error and not a hard failure. The closest
  precedents are handlers that return an `Error` string for an unusable service
  (`lsp.go:290,297`) and descriptions that advertise absence
  (`a2a.go:393-395`, `llms.go:539-541`). Nothing in the repo returns a literal
  `{"status":"unsupported"}` today (`grep -rn "unsupported" internal/tools/` is
  empty) — so this shape is new and should be stated plainly in the tool
  description.

**Flag on ⇒ per-spawn protocol selection.** Even with the flag enabled, the
protocol is chosen **per spawn**: `background: true` uses `--mode rpc`;
everything else keeps `--mode json`. This keeps the "no `background` ⇒
byte-for-byte unchanged" non-regression criterion trivially true and avoids
making every synchronous subagent pay for the rpc translation layer.

---

### Q11 — When the concurrency pool is full, does an async spawn block or fail
fast?

**Asked:** (i) block on `Acquire` like a sync spawn (as approved in Q7);
(ii) fail fast for async only, returning a model-actionable `no_capacity` result;
(iii) bounded wait then fail.

**Answer:** **(ii) — fail fast for async only.**

**Supersedes the Q7.3 "block on `Acquire`" decision.**

**Rationale:**

- The pool defaults to **3** (`DefaultPoolSize`, `orchestrator.go:28`) and a slot
  is released only when the agent's event stream closes
  (`forwardAgentEvents:611`). A detached agent can hold its slot for up to its
  absolute timeout (~20m).
- A blocking async spawn would therefore **stall the parent turn** until a slot
  frees — turning a capacity problem into a UI hang whose only escape is
  Esc/Ctrl+C. A blocking spawn named "async" contradicts itself.
- There is **no precedent for a blocking background start** in the codebase: the
  bash supervisor's backgrounding is a *transition* of an already-started command,
  and `register` **evicts** rather than blocks when at `maxBackgroundProcs = 8`
  (`bash_supervisor.go:464-484,511-528`).

**Specified semantics:**

- Async spawn makes a **non-blocking** acquire attempt against the pool and
  returns `status: "no_capacity"` with `running: false` when no slot is free —
  a model-actionable result, not a Go error (consistent with Q9).
- **Synchronous spawns are unchanged:** they keep blocking on `Acquire(ctx)`
  exactly as today (`orchestrator.go:443`, `pool.go:24-33`).
- The failure is recoverable by the model: it can retry later, spawn
  synchronously instead, or wait for a detached agent to finish.
- This adds a second way for an async spawn to fail without spawning (the first
  being `PI_SUBAGENT_ASYNC` off, Q10). Both must be distinguishable in the result
  so the model can react correctly.

---

### Q12 — How does the parent tell one child turn from the next?

**Asked:** (i) rely on parent-side serialization so `agent_settled` delimits a
turn, optionally also tracking a turn counter for reporting; (ii) additionally
expose a turn counter to the model.

**Answer:** **(i) — `agent_settled` ends a turn; serialization is the parent's
invariant.**

**Specified semantics:**

- The child's rpc stream always terminates a turn with `agent_end` then
  `agent_settled` (`internal/pirpc/rpc.go:273-274`), and `agent_settled` is
  documented in the child as "the only signal pi-acp accepts to resolve the turn"
  (`rpc.go:260-261`).
- **The parent's per-agent writer sends the next `prompt` only after observing
  `agent_settled`.** This is a hard invariant, not an optimization.
- Events arriving between two `agent_settled` markers belong to one turn. No turn
  counter is exposed to the model in v1.
- **Why serialization is required anyway (independent reason):** the child keeps a
  single `s.cancel` slot (`rpc.go:101,263-266`). Two concurrent prompts would
  clobber it, after which `abort` reaches only the newer turn — so concurrent
  prompts are incorrect regardless of reporting.
- **Why no correlation id is available:** each command's `response` envelope is
  the first line on the wire for that command (`rpc.go:195`), but the **event
  stream carries no command id** — events are only orderable, not attributable.
  Serialization is therefore what makes attribution possible at all.
- **Must be tested:** assert that no second `{"type":"prompt"}` is written to the
  child's stdin before the first turn's `agent_settled` has been observed.

---

## Consolidated Requirements (final)

### The feature in one sentence

The `subagent` tool gains `background: true` on **single mode** to detach a
pi-binary subagent, and one new tool **`subagent_input`** both **steers** that
agent (messages queued as its next turn) and **attaches** to it (incremental reads
of its output). Gated by the env var `PI_SUBAGENT_ASYNC`, default off.

### In scope (v1)

| # | Requirement |
|---|---|
| R1 | `subagent` single mode accepts `background: true` and returns immediately with a handle (`agent_id`) plus a "still running" note, instead of blocking. |
| R2 | New tool `subagent_input{agent_id, message?, wait_sec?}` both **steers** (delivers `message` to the running child) and **attaches/polls** (returns output accumulated since the caller's last read). |
| R3 | Steered input is **queued as the child's next turn**, serialized by the parent after the child's previous turn settles. The tool reports *queued*, not *acted upon*. |
| R4 | A detached agent **survives** parent-turn end and the user's Esc/Ctrl+C, but **dies on session/orchestrator shutdown**. Implemented with `context.WithoutCancel`. |
| R5 | A finished async agent returns its final result **idempotently** until evicted (≤ 50 completed entries, oldest-by-`FinishedAt`). |
| R6 | Bounded by the existing `PI_SUBAGENT_CONCURRENCY` pool (default 3); the slot is held for the agent's whole life. Full pool ⇒ **non-blocking** `status: "no_capacity"`. Synchronous spawns keep blocking. |
| R7 | Missing/gone agents return a **structured** `status: "unknown"` result, never a Go error. |
| R8 | **pi-binary backends only.** codex/ACP return an explicit model-actionable "not steerable" result. |
| R9 | **Model-only.** No new TUI affordances, slash commands, keybindings, or user-facing steer. |
| R10 | Incremental output reads with dropped-byte accounting, reported to the model. |
| R11 | Gated by **`PI_SUBAGENT_ASYNC`, env-only, default off**. Flag off ⇒ no `--mode rpc` child, `subagent_input` unregistered, `background: true` returns a "disabled" result. Flag on ⇒ protocol chosen **per spawn**. |
| R12 | `agent_settled` delimits a child turn; the parent **never** sends a second `prompt` before the previous turn settles. |

### Out of scope (v1) — recorded, not dropped

| # | Deferred item | Why |
|---|---|---|
| D1 | **codex steering** (`turn/steer`, `turn/start{threadId, input[]}`) | Needs `Session.finish`→`client.close()` (kills the process group, `session.go:586`, `client.go:349-353`) reworked, `threadID` exposed, multi-turn `turnID` tracking (`session.go:72,242-246`). Own spec. |
| D2 | **ACP steering** (repeat `session/prompt` on a retained connection) | Needs `RunningSession` to retain `conn` + `io.WriteCloser` stdin, stop closing stdin (`session.go:191,210`), stop `cmd.Wait()`ing, expose the session id. Own spec. |
| D3 | **Mid-turn injection** into a running child | `pirpc` has no injection path; `set_steering_mode`/`set_follow_up_mode` are no-ops (`rpc.go:244-248`). Requires changing the child. |
| D4 | **User-facing steer / per-agent cancel UI** | Reading (C) of Q1; a distinct UI feature. `Orchestrator.Cancel` (`orchestrator.go:806`) stays unwired. |
| D5 | **Auto-injecting async results into the conversation** | Would use the `PreTurnHook` seam (`agent.go:386`, autocompact-only). Rejected to avoid touching session/compaction. Cost: a model that never polls never sees the result. |
| D6 | **Async spawn in `parallel`/`chain`** | Q8 chose single-mode-only. |
| D7 | **Worktree GC for detached agents** | Repeated background spawns could accumulate checkouts; cleanup is not done by `forwardAgentEvents` (`orchestrator.go:636-641`). |
| D8 | **Live TUI repaint for a detached agent** | The 150 ms ticker runs only while `m.running` (`tui.go:756-771`); the card/sidebar row look stale. Accepted in Q5. |
| D9 | **Fixing the sidebar status defect** | `agentRow` handles `"done"` but the orchestrator emits `completed`/`canceled`/`timeout` (`sidebar.go:590-603`). Pre-existing. |
| D10 | **`SubagentConfig` in `config.json`** | Q10 chose env-only. |
| D11 | **Generic feature-flag system** | Only proposed (`GAP_ANALYSIS.md:660-666`); `Config.Tools` is dead (`config.go:110`). |
| D12 | **Turn-counter reporting to the model** | Q12 chose serialization-only. |
| D13 | **`SpawnWithRetry` for async spawns** | Reachable only from tests today (`orchestrator.go:316-358`); `MaxRetries` exists only on `SpawnInput`, and the retry path **discards events before the first `message_end`/`error`** (`awaitAttemptOutcome`, `:391-402`) — incompatible with incremental reads. |

### Acceptance Criteria (Given / When / Then)

**Async spawn**
- Given `PI_SUBAGENT_ASYNC=1` and `subagent{agent:"worker", task:"…", background:true}`,
  when called, then it returns promptly with `running: true`, a non-empty
  `agent_id`, and a note naming `subagent_input` — and the child keeps running
  after the tool returns.
- Given `background: true` for a `codex` or ACP-backed agent name, when called,
  then the result carries an explicit "not steerable in this build" status and no
  process is detached.
- Given `PI_SUBAGENT_ASYNC` unset, when `background: true` is requested, then the
  result carries an explicit "disabled" status and no process is detached.
- Given the concurrency pool is saturated, when a `background: true` spawn is
  requested, then it returns `status: "no_capacity"` **without blocking**, and no
  process is detached.
- Given the concurrency pool is saturated, when a **synchronous** `subagent` call
  is made, then it blocks on `Acquire` exactly as today.

**Steer**
- Given a running async agent, when `subagent_input{agent_id, message:"also do X"}`
  is called, then the message is queued and delivered as the child's **next** turn
  after its current turn settles, and the call reports it as queued.
- Given a running async agent mid-turn, when `subagent_input` is called with a
  message, then the child's **current** turn is not interrupted and no concurrent
  turn is started on the same session.
- Given a queued steer message is pending, when the child's turn settles, then no
  second `prompt` is written before `agent_settled` was observed.
- Given a completed async agent, when `subagent_input{agent_id, message:"…"}` is
  called, then the message is not delivered and the result reports the terminal
  status.

**Attach / poll**
- Given a running async agent, when `subagent_input{agent_id}` is called with no
  message, then it returns only the output produced since the caller's last read.
- Given output exceeding the retained buffer, when polled, then the result reports
  how many bytes were dropped.
- Given a completed async agent, when polled repeatedly, then each call returns
  the final result — no "spent handle" error.
- Given an unknown or evicted `agent_id`, when polled, then the result carries
  `status: "unknown"` and the call does **not** return an error.
- Given a missing `agent_id`, when called, then it **is** an error naming the live
  agent ids.

**Lifetime**
- Given a background agent still running, when the spawning turn ends, then the
  agent keeps running and its events keep flowing to the event callback.
- Given a background agent still running, when the user presses Esc/Ctrl+C to
  cancel the parent turn, then the agent keeps running.
- Given a background agent still running, when the session shuts down, then the
  agent is cancelled and marked `canceled`, and any queued steer message is
  dropped rather than delivered.

**Feature flag**
- Given `PI_SUBAGENT_ASYNC` unset or unparseable, when any async affordance is
  used, then it is off and no error is raised.
- Given `PI_SUBAGENT_ASYNC=1` and `subagent{background:true}`, when called, then
  the child is spawned on `--mode rpc` and the call returns a handle.
- Given `PI_SUBAGENT_ASYNC=1` and a synchronous `subagent` call, when called, then
  the child is still spawned on `--mode json`.
- Given `PI_SUBAGENT_ASYNC=1` and a nested (child-spawned) agent, when the child
  builds its own orchestrator, then it also sees the flag (inherited via the `PI_`
  env allowlist).

**Non-regression**
- Given `PI_SUBAGENT_ASYNC` unset, when any `subagent` call is made (single,
  parallel, or chain), then children are spawned as `pi --mode json` with the
  prompt in argv exactly as today, and no `--mode rpc` child is created.
- Given `PI_SUBAGENT_ASYNC` unset, when the tool list is assembled at any entry
  point, then `subagent_input` is absent from it.
- Given a `subagent` call without `background`, when called in single, parallel,
  or chain mode, then behaviour is byte-for-byte unchanged (still blocking).
- Given `detectMode` on any existing input shape, when called, then the resolved
  mode is unchanged.

### Open items to pin in `design.md`

1. **Async absolute-timeout default.** Q7 deferred this. Baseline is
   `DefaultAbsoluteTimeout = 20m`; a detached agent holds a pool slot for its whole
   life, which argues for shorter. Decide one value and document the reasoning in
   the same voice as `timeout.go`'s comments.
2. **Where the steer queue and output buffers live.** `agentState`
   (`orchestrator.go:92-103`) has no room today. Either extend it or add a
   parallel per-agent structure; consider reusing `stream` from
   `bash_stream.go:20-114` for incremental reads.
3. **How the spawn path switches protocols.** `spawnArgs` (`spawner.go:110-132`)
   currently emits `--mode json` unconditionally; `SpawnOpts` needs a mode field.
4. **The rpc→`subagent.Event` translation table.** Required because rpc's
   `message_update` (nested `assistantMessageEvent`) and `tool_execution_start`
   (`toolName`, `args`) do not match the existing `jsonEvent` tags
   (`spawner.go:383-393`), so text and tool events would silently vanish. Note rpc
   mode has **no `error` event type** — failures arrive as assistant text
   (`rpc.go:395-402`).

