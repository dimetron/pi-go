# Plan A — TUI Non-Blocking Input Queue

Status: implementation plan for Design A
Parent spec: `research/adk-graph-coordinator-A.md`
Addresses review: `research/adk-graph-coordinator-review.md` (Design A section)

## Goal

Make the main chat input always editable. When the user presses Enter while an
agent turn is in flight, the prompt is **enqueued** in FIFO order and dispatched
as a sequential follow-up turn in the same ADK session. The user can keep
typing, see a queue-count badge, and Ctrl+C cancels only the active turn (the
queue is preserved). Subagent architecture is untouched.

## Scope

**In scope**

- A FIFO prompt queue on `model` (`pendingInputs []queuedPrompt`).
- Removing the read-only lock in `handleKey` (tui.go:888) so typing, history,
  and slash command editing work while a turn runs.
- Routing `InputSubmitMsg` to either `submitPrompt` (idle) or `enqueuePrompt`
  (running), and draining one queued prompt at the end of every turn.
- An explicit busy-time policy for every slash command in `slashCommands`
  (`input.go:272-295`) plus the dynamic-skill branch.
- A queue-count badge in the status bar.
- Title semantics, cancellation semantics, and session-switch / restart
  behavior spelled out.
- A bounded queue with a hard cap (with explicit drop policy) — see
  "Backpressure" below.

**Out of scope**

- Changing the subagent execution model or ADK wiring (Design A's "express
  routing via system prompt" is satisfied by the existing system prompt; no
  code change needed).
- Persisting queued prompts to disk across process restarts.
- Running multiple queued prompts in parallel (serialized turns is the
  one-active-loop model that already exists).
- ACP steering / injecting into a live ACP subagent (separate concern,
  flagged in the base research at `adk-graph-coordinator.md:129-132`).
- A `/queue` slash command (can be added later if useful; not required for
  v1).

## Architecture (final)

```
              ┌──────────────────────────┐
   KeyEnter ──► updateTerminal sees    │
              │ InputSubmitMsg          │
              └────────────┬────────────┘
                           │
                  m.running == false ?
                  ┌────────┴────────┐
                yes                no
                  │                 │
                  ▼                 ▼
            submitPrompt      enqueuePrompt
            (start turn)      (push to tail)
                                     │
                                     ▼
                          pendingInputs []queuedPrompt
                          (FIFO, capped at maxQueueDepth)
                                     │
       handleAgentDone(msg)          │
       (end of current turn) ────►   │
                  │                  │
                  ▼                  ▼
            m.running = false    drainNextQueued()
                                 pop head, submit
                                 (advance only on
                                  non-drainable err)
                  │
                  ▼
        status bar shows badge
        when len(pendingInputs) > 0
```

**Backpressure.** `pendingInputs` is capped at `maxQueueDepth = 32`. If a
submit arrives while the queue is full, the oldest queued entry is dropped, a
warning is appended to the chat (`"Queue full — dropped the oldest prompt"`),
and a flash is shown in the status bar. This is preferable to silently
swallowing the user's most recent keystroke or OOM-ing the TUI on a runaway
script.

**Title semantics (resolved).** The current code calls
`m.applySessionTitle(text)` on **every** `submitPrompt` (agent_loop.go:332-336),
which contradicts Design A's claim that "only the first sets the session title."
This plan resolves the contradiction as follows:

- **Rule adopted:** the session title is set on the **first user turn of a
  session** and on **explicit user commands** (`/title`, any non-clear slash
  command, since `handleSlashCommand` already funnels through `setSessionTitle`).
  Subsequent queued user prompts do **not** rewrite the title.
- **Implementation:** a `titleSet bool` flag is added to `model`. `submitPrompt`
  sets it to `true` after the first successful `applySessionTitle`. While it is
  `true`, the per-prompt auto-derive in `submitPrompt` becomes a no-op (we
  still log and append messages, we just skip the title rewrite).
- **Why this rule:** the title is a *session-level* identity, and the user's
  intent in queueing follow-ups is "do this next", not "rename my session".
  The current "every prompt rewrites the title" behavior is a bug — a long
  queue of "fix the lint", "now run the tests", "now commit" would churn the
  terminal title three times in a row. The review's call-out
  (review.md:21) is the authority here; this rule supersedes Design A's
  terser claim.

## File-by-file changes

### 1. `internal/tui/tui.go` — model struct + handleKey

**`model` struct (tui.go:24-128).** Add the queue + flag fields beside the
existing `agentCh` field (line 48). Match the inline comment style of the
neighboring fields.

```go
// Pending user prompts, in FIFO order. Filled by enqueuePrompt when the user
// presses Enter while a turn is running; drained one-at-a-time by
// drainNextQueued at the end of each turn. Ephemeral: cleared on session
// switch and on process restart.
pendingInputs []queuedPrompt

// titleSet records whether applySessionTitle has fired this session. The
// first user prompt (or any explicit /title / non-clear slash command) sets
// it; subsequent queued prompts leave the session title alone. See
// plan-A.md "Title semantics".
titleSet bool
```

**`queuedPrompt` type.** Add a small struct in `tui.go` near the other
helpers, or inside `input.go` — pick `input.go` so the message shape lives
with `InputSubmitMsg`. The struct mirrors `InputSubmitMsg`'s public fields
plus the raw text so we can re-derive mentions on drain if needed (defensive;
we keep the original `mentions` slice to avoid re-parsing `@path`).

```go
// queuedPrompt is one entry in the TUI's pending-input FIFO. Captures the
// raw text and the @mentions the input layer already extracted, so a drain
// does not re-run extractMentions.
type queuedPrompt struct {
    text     string
    mentions []string
}
```

**`handleKey` (tui.go:871-903).** The early-return at `:888-890` blocks
typing while running. Change it to let typing through:

```go
// Editing keys stay live while the agent runs. /clear and other
// "destructive" slash commands are gated by the slash-command busy-time
// policy (see plan-A.md "Slash-command busy-time policy"), not by this
// lock. Overlay keys and Ctrl+C still get first refusal above.
if m.loading {
    return m, nil
}

for _, handle := range []keyHandler{
    m.handleToggleKey,
    m.handleHistoryKey,
    m.handleScrollKey,
} {
    if model, cmd, handled := handle(key); handled {
        return model, cmd
    }
}

return m.handleInputKey(msg)
```

`m.running` is removed from this gate. The "is the agent currently doing
something?" check still belongs in `handleInputKey`/`handlePaste` for
non-text concerns (e.g. paste suppression), and that's already where it
lives in `handlePaste` (tui.go:599-607).

**`handlePaste` (tui.go:599).** No change needed — the `!m.running` check
intentionally suppresses paste into the input while a turn is running, to
avoid pasting a wall of text into an in-flight agent. The review flagged
this concern; we are leaving it in for v1 and documenting it.

### 2. `internal/tui/agent_loop.go` — enqueue, drain, title

**Helper `enqueuePrompt` (new).** Sits next to `submitPrompt`. Appends to
`pendingInputs` and trims / warns when full. Does **not** touch
`m.running`, `m.agentCh`, or any of the per-turn state.

```go
// enqueuePrompt stores text+mentions for later dispatch. Called from
// updateTerminal when InputSubmitMsg arrives while m.running. FIFO order
// matches user typing order. Hard cap of maxQueueDepth; oldest entry is
// dropped (with a warning) when full so a runaway script cannot OOM the
// TUI.
func (m *model) enqueuePrompt(text string, mentions []string) {
    if len(m.pendingInputs) >= maxQueueDepth {
        dropped := m.pendingInputs[0]
        m.pendingInputs = m.pendingInputs[1:]
        warn := fmt.Sprintf("Queue full (%d) — dropped oldest prompt: %q",
            maxQueueDepth, truncate(dropped.text, 40))
        m.chatModel.AppendWarning(warn)
        m.setFlash("queue full")
    }
    m.pendingInputs = append(m.pendingInputs, queuedPrompt{
        text:     text,
        mentions: mentions,
    })
}
```

**Helper `drainNextQueued` (new).** Called from `handleAgentDone`. Pops the
head and re-enters `submitPrompt` with the same shape the user typed. If
the agent is nil (unit tests), the queue is left intact — the next
legitimate turn will still drain it — but we return a no-op for this
drain so tests don't lose pending inputs silently. (This is the explicit
resolution of the codex review's "agent nil no-op underspecified"
complaint, review.md:23.)

```go
// drainNextQueued pops the head of pendingInputs and starts it as the next
// turn. No-op when the queue is empty. When the agent is nil (unit tests
// without a real Agent), the entry is NOT consumed — the next live turn
// will retry the drain. This avoids silently dropping a queued prompt in
// test paths.
func (m *model) drainNextQueued() (tea.Model, tea.Cmd) {
    if len(m.pendingInputs) == 0 {
        return m, nil
    }
    if m.cfg.Agent == nil {
        return m, nil
    }
    next := m.pendingInputs[0]
    m.pendingInputs = m.pendingInputs[1:]
    return m.submitPrompt(next.text, next.mentions)
}
```

**`handleAgentDone` (agent_loop.go:800-825).** Call `drainNextQueued`
before returning. The existing `m.running = false` line clears the active
flag first, so `submitPrompt`'s re-entry will set it back to `true` and
start a fresh loop. Add the drain call right at the end, after
`m.refreshDiffStats()`:

```go
// handleAgentDone processes an agentDoneMsg.
func (m *model) handleAgentDone(msg agentDoneMsg) (tea.Model, tea.Cmd) {
    m.running = false
    m.agentCancel = nil
    m.matrix.clear()
    m.statusModel.ActiveTool = ""
    m.statusModel.ActiveTools = nil
    if msg.err != nil {
        if m.face != nil {
            m.face.SetMood(MoodSad)
        }
        m.chatModel.AppendError(fmt.Sprintf("Error: %v", msg.err))
        m.chatModel.TraceLog = append(m.chatModel.TraceLog, traceEntry{
            time: time.Now(), kind: "error", summary: "Error", detail: msg.err.Error(),
        })
    } else {
        if m.face != nil {
            m.face.SetMood(MoodHappy)
        }
    }
    m.chatModel.Streaming = ""
    m.chatModel.Thinking = ""
    m.agentCh = nil
    m.refreshDiffStats()

    // Advance the FIFO: if a prompt was submitted while this turn was
    // running, start the next one. Error or success both advance — the
    // review's acceptance criterion (A:75-77) is explicit on this.
    return m.drainNextQueued()
}
```

**`submitPrompt` (agent_loop.go:312-351).** Gate the title rewrite on
`titleSet`. The rest of the function is untouched.

```go
// submitPrompt sends a user prompt to the agent.
func (m *model) submitPrompt(text string, mentions []string) (tea.Model, tea.Cmd) {
    // ... mentions / refs / logger unchanged ...

    // First-prompt-of-session sets the title; subsequent prompts (including
    // drained queued prompts) leave it alone. Explicit /title and other
    // non-clear slash commands still drive setSessionTitle through
    // handleSlashCommand.
    if !m.titleSet {
        m.applySessionTitle(text)
        m.titleSet = true
    }

    // ... append messages, set m.running, return batched cmd unchanged ...
}
```

**Constants.** Add to the const block at the top of `agent_loop.go:22-41`:

```go
// maxQueueDepth is the FIFO cap for prompts submitted while an agent
// turn is running. Bounded so a runaway script cannot OOM the TUI.
const maxQueueDepth = 32
```

**`truncate` helper.** Add next to `enqueuePrompt` (or in a small `queue.go`
file in the same package; pick `agent_loop.go` to keep the diff small):

```go
// truncate returns s clipped to n runes with an ellipsis suffix when it
// would otherwise exceed the limit. Used for queue-full warnings so a
// massive queued prompt does not blow up the chat width.
func truncate(s string, n int) string {
    if utf8.RuneCountInString(s) <= n {
        return s
    }
    runes := []rune(s)
    return string(runes[:n-1]) + "…"
}
```

This requires `unicode/utf8` to be added to the import block of
`agent_loop.go` (the file does not currently import it; verify with
`goimports`).

### 3. `internal/tui/tui.go` — InputSubmitMsg routing

**`updateTerminal` `InputSubmitMsg` branch (tui.go:543-549).** This is the
single routing point. The slash-command branch already handles busy-time
gating via the policy table (see §Slash-command busy-time policy), so we
do **not** add a "running?" short-circuit here. The queue-vs-submit
decision lives in the non-slash branch:

```go
case InputSubmitMsg:
    if strings.HasPrefix(msg.Text, "/") {
        model, cmd := m.handleSlashCommand(msg.Text)
        return model, cmd, true
    }
    // Plain prompt: start a turn if idle, otherwise enqueue.
    if m.running {
        m.enqueuePrompt(msg.Text, msg.Mentions)
        return m, nil
    }
    model, cmd := m.submitPrompt(msg.Text, msg.Mentions)
    return model, cmd, true
```

Note: `enqueuePrompt` is called as a side effect, so the message is already
appended to `m.inputModel.History` by `InputModel.HandleKey` (input.go:108-112)
before this point. That is the desired behavior — history is a record of
what the user typed, regardless of dispatch timing.

### 4. `internal/tui/status.go` — queue badge

**`StatusRenderInput` (status.go:27-43).** Add one field:

```go
QueueDepth int // number of prompts waiting in the input FIFO; 0 hides the badge
```

**`StatusModel.Render` (status.go:91-225).** Render the badge next to the
`tools[…] / tool: …` slot (after the active-tools block at status.go:202-214).
Match the existing style: a peach-tinted label `[queued: N]`, only when
`QueueDepth > 0`. The badge lives on `StatusRenderInput` (not
`StatusModel`) because it is a per-frame signal from the root model, the
same way `RunCycle` does it (status.go:218-222):

```go
// Queue depth badge: how many prompts are waiting to be dispatched. Sits
// next to the active-tool indicator so a user can see "tool: bash (2s) │
// queued: 2" and know their work is acknowledged.
if in.QueueDepth > 0 {
    queueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387")) // Mocha peach
    parts = append(parts, queueStyle.Render(fmt.Sprintf("queued: %d", in.QueueDepth)))
}
```

**`statusRenderInput` (tui.go:1804-1834).** Add one line:

```go
return StatusRenderInput{
    // ... existing fields ...
    QueueDepth: len(m.pendingInputs),
    // ... existing fields ...
}
```

This makes the badge a strict function of `m.pendingInputs.length`; no
extra state on `StatusModel` itself, no re-render invalidation needed.

### 5. `internal/tui/input.go` — `View(running)` change

**`View(running)` (input.go:150-161).** Drop the `running` branch; the
input is always rendered. The `running` parameter becomes vestigial; keep
it for the call sites (tui.go:1275, tui.go:1647) but ignore the value.
Two reasonable options:

- **Option A (chosen):** keep the signature, ignore the arg, document
  why:

  ```go
  // View renders the input area. The running parameter is retained for
  // call-site stability; the input is always rendered now (the busy state
  // is communicated by the queue badge and the spinner in the status bar
  // instead). This is part of Plan A: the main chat is always open.
  func (im *InputModel) View(running bool) string {
      im.ensureInput()
      _ = running
      return im.input.View()
  }
  ```

- Option B (rejected for this PR): remove the parameter. Touches two
  call sites and offers no behavioral change; not worth the diff noise.

### 6. `internal/tui/commands.go` — slash-command busy-time policy

Add a small helper near `handleSlashCommand` and gate it. The full policy
is in the table above; this section gives the code shape. There are now
**four** policies:

```go
// slashCommandBusyPolicy describes how a slash command behaves when the
// agent is currently running a turn.
type slashCommandBusyPolicy int

const (
    // slashPassThrough runs the command even while the agent runs.
    // Default for read-only / informational commands.
    slashPassThrough slashCommandBusyPolicy = iota
    // slashQueue accepts the command but defers it: it runs after the
    // current turn finishes, alongside any queued user prompts.
    slashQueue
    // slashBlock ignores the command while a turn is running and shows
    // a status-bar flash explaining why. Default for commands that
    // would corrupt in-flight state in a way that cannot be undone
    // (/restart, /exit, /quit).
    slashBlock
    // slashConfirmAndCancel asks the user to confirm via a y/N flash
    // before running. On 'y' the active turn is cancelled (via
    // m.agentCancel()) and the command runs; on 'N' the command is
    // dropped and the user's typed text is preserved. Default for
    // commands that would cancel or fork the active context (/clear,
    // /model, /branch, /compact, dynamic skills).
    slashConfirmAndCancel
)

// slashCommandBusyPolicy returns the busy-time policy for a slash command.
// Any command not in the table defaults to slashPassThrough, because
// Design A's promise is "input is always editable"; a bare default of
// "blocked" would silently swallow the user's keystroke.
func slashCommandBusyPolicy(cmd string) slashCommandBusyPolicy {
    switch cmd {
    // cancel-context: ask the user to confirm before cancelling the
    // active turn.
    case "/clear", "/model", "/branch", "/compact":
        return slashConfirmAndCancel
    // block: cannot be made safe mid-turn.
    case "/restart", "/exit", "/quit":
        return slashBlock
    // queue: full workflows that must wait for the current turn.
    case "/plan", "/run", "/commit", "/login":
        return slashQueue
    default:
        return slashPassThrough
    }
}
```

**`handleSlashCommand` (commands.go:18-128).** Wrap the body with a policy
check. The `cmd` variable is already extracted at line 20; the policy is
applied right after the logger call (line 25) and before the per-command
`switch`:

```go
func (m *model) handleSlashCommand(input string) (tea.Model, tea.Cmd) {
    parts := strings.Fields(input)
    cmd := strings.ToLower(parts[0])

    if m.cfg.Logger != nil {
        m.cfg.Logger.UserMessage(input)
    }

    // Busy-time policy: a few commands are unsafe mid-turn, some are
    // run-alongside, some cancel context and must ask the user first.
    // See plan-A.md "Slash-command busy-time policy".
    if m.running {
        switch slashCommandBusyPolicy(cmd) {
        case slashBlock:
            m.setFlash(fmt.Sprintf("%s blocked while running", cmd))
            return m, nil
        case slashQueue:
            m.enqueuePrompt(input, nil)
            return m, nil
        case slashConfirmAndCancel:
            return m.requestCancelConfirm(cmd, input)
        case slashPassThrough:
            // fall through
        }
    }

    // ... existing switch unchanged ...
}
```

**`requestCancelConfirm` (new helper, near `setFlash`).** Renders a
`y/N` flash in the chat (e.g. `Cancel the running turn and run /clear?
[y/N]`), arms a one-shot confirmation state on `model` (`m.confirm
*pendingConfirm`), and returns a `tea.Cmd` that listens for the next
keystroke. On `y`/`Y`/`Enter` (yes-default — the user just typed the
command, so the default-yes reflects "I meant to do this"), the
helper calls `m.agentCancel()` and then re-enters
`handleSlashCommand` with the same input. On `N`/any other key, the
helper restores the user's text to the input and clears the confirm
state. The `pendingInputs` queue is preserved in either case.

`/clear`, `/exit`, `/quit` need no extra change: their `slashBlock` /
`slashConfirmAndCancel` policies run before the existing per-command
`switch` ever sees them. The function would not silently enqueue a
destructive command.

The dynamic-skill branch (`default:` at commands.go:115-125) defaults to
`slashPassThrough` in the table, but `handleSlashCommand` checks
`isDynamicSkill(cmd)` separately and applies `slashConfirmAndCancel`
to skills (since a skill may cancel context by design). Per-skill
manifest overrides are a follow-up.

## API / state additions (summary)

| Symbol | File | Purpose |
| --- | --- | --- |
| `queuedPrompt` | `internal/tui/input.go` | FIFO entry: `{text, mentions}` |
| `model.pendingInputs []queuedPrompt` | `internal/tui/tui.go:48` | The FIFO itself |
| `model.titleSet bool` | `internal/tui/tui.go:49` | One-shot flag for the title rule |
| `maxQueueDepth = 32` const | `internal/tui/agent_loop.go:42` | Hard cap |
| `enqueuePrompt(text, mentions)` | `internal/tui/agent_loop.go` | Push (with overflow warn) |
| `drainNextQueued() (tea.Model, tea.Cmd)` | `internal/tui/agent_loop.go` | Pop + `submitPrompt` (no-op when agent nil) |
| `truncate(s, n)` | `internal/tui/agent_loop.go` | Rune-safe clip for warnings |
| `StatusRenderInput.QueueDepth int` | `internal/tui/status.go:28` | Status-bar source |
| `slashCommandBusyPolicy` type + `slashCommandBusyPolicy(cmd)` | `internal/tui/commands.go` | Slash-command gating (4 policies: pass-through / queue / block / confirm-and-cancel) |
| `requestCancelConfirm(cmd, input)` | `internal/tui/commands.go` | y/N flash → cancel-and-run on `y`, drop on `N` |
| `model.pendingConfirm *pendingConfirm` | `internal/tui/tui.go:50` | One-shot confirmation state for `requestCancelConfirm` |

## Slash-command busy-time policy

The default rule is **`slashPassThrough`** (input is always editable; slash
commands generally run). The exceptions are the ones the codex review
called out as missing (review.md:22). Source: grep of `slashCommands`
(input.go:272-295), the `/clear`/`/exit`/`/quit` early-return in
`handleSlashCommand` (commands.go:35), and reading each handler in
`commands.go`.

**New general rule (user feedback):** any slash command that would
**cancel the running turn's context** (kill the active stream, drop
partial output, or fork the conversation) must **ask the user to
confirm** instead of running silently while a turn is in flight. The
confirmation is a simple `y/N` prompt rendered as a flash message in
the chat; on `N` the command is dropped, on `y` the command runs and
cancels the active turn first. The categories below split commands
into:

- **pass-through** — read-only or independent; no confirm needed.
- **confirm-and-cancel** — would interrupt the active turn; require
  `y/N` confirmation, which cancels the active turn on `y`.
- **queue** — workflow-style; defer until the active turn finishes.
- **block** — would corrupt in-flight state in a way that cannot be
  undone; refuse outright while running.

| Command | Policy while running | Reason |
| --- | --- | --- |
| `/help` | pass-through | Pure read; appends a message. |
| `/clear` | **confirm-and-cancel** | Wipes `chatModel.Messages`; the running agent is still appending to it. Confirmed cancel drops the active turn's partial output. |
| `/copy` | pass-through | Reads transcript; safe. |
| `/model` | **confirm-and-cancel** | Swaps the LLM while a turn is in flight against the old one. The model-switch callback (`Config.ModelSwitcher`) is not designed to be hot-swapped; cancelling the active turn keeps the swap coherent. |
| `/session` | pass-through | Prints a message; safe. |
| `/context` | pass-through | Reads context size; safe. |
| `/branch` | **confirm-and-cancel** | Subcommand that calls `git checkout` would race the agent's tool calls on the same worktree. Confirmed cancel lets the branch switch happen on a clean state. |
| `/compact` | **confirm-and-cancel** | Compaction rewrites the session; the running agent is reading from the same session. Confirmed cancel lets compaction proceed on a complete turn. |
| `/subagents` | pass-through | Prints subagent list; safe. |
| `/history` | pass-through | Reads history; safe. |
| `/login` | **queue** | Prompts for an API key; better deferred than interleaved. |
| `/commit` | **queue** | Workflow-style; defers to next turn. |
| `/plan` | **queue** | Whole workflow; defers. |
| `/run` | **queue** | Whole workflow; defers. |
| `/skills` and friends | pass-through | List / load metadata; safe. |
| `/theme` | pass-through | Pure UI; safe. |
| `/ping` | pass-through | Test LLM; runs its own short turn. **Risk:** runs an extra LLM call while one is in flight. Acceptable: `/ping` already runs synchronously without the main agent. |
| `/rtk` | pass-through | Reads metrics; safe. |
| `/mcp` | pass-through | Reads MCP status; safe. |
| `/restart` | **block** | Restarts the process. Anything queued would be lost; better to fail loud. (The process restart itself is the only safe action; a confirm prompt is moot when Ctrl+C × 2 also restarts.) |
| `/exit`, `/quit` | **block** | Tearing the program down while a turn is in flight leaves the session in a half-finalized state. The user's queued prompts vanish with the process — explicit per the session-switch section. The existing quit-on-second-Ctrl+C (tui.go:1022-1024) is the documented escape hatch. |
| Dynamic `/<skill>` | confirm-and-cancel | A skill may cancel context by design (e.g. `/cancel-task`). v1 default: any skill invocation while running requires `y/N`. Per-skill manifest can later override to `pass-through` or `block`. |

**Confirmation UI.** When a `confirm-and-cancel` command is issued
while running, the chat appends a transient assistant message of the
form:

> `Cancel the running turn and run `/clear`? [y/N]`

The next user keystroke is interpreted as the `y/N` answer only — any
other text input is held until the prompt is answered (input is
otherwise always editable, so this is a small detour). On `y` (or
`Y`/`Enter` which defaults to `y` since the default-yes/no
convention here is the user's intent), the active turn is cancelled
(via the existing `m.agentCancel` handle) and the command runs. On
`N`/anything else, the command is dropped and the input is restored
to the user's typed text.

**Implementation.** `slashCommandBusyPolicy(cmd string)
slashCommandBusyPolicy` returns one of `slashPassThrough |
slashConfirmAndCancel | slashQueue | slashBlock` (see code sketch
above). The `slashConfirmAndCancel` branch in `handleSlashCommand`
delegates to `m.requestCancelConfirm(cmd, input)`, which renders a
`y/N` flash, arms `m.pendingConfirm`, and on `y` calls
`m.agentCancel()` and re-enters `handleSlashCommand` with the same
input. The slash command dispatcher otherwise stays unchanged.

**Note on `/exit` and `/quit`.** These already do `m.quitting = true` and
`return m, tea.Quit`; the busy-time block is purely defensive. If a user
ignores the block flash and Ctrl+C's twice, the existing quit-on-second-Ctrl+C
(tui.go:1022-1024) still applies. Net effect: pressing `/exit` while running
shows a "blocked while running" flash; pressing Ctrl+C twice still quits.

**Note on `/clear`.** The review calls out that `/clear` resets
`m.running = false` (commands.go:52). With the new policy we confirm-cancel
`/clear` while running, so this stale line is now unreachable on the busy
path but remains correct for the idle path. No change needed; document
only.

## Title semantics (resolved)

**User rule (confirmed):** "title main only" — set the title from the
first user prompt of a session, then leave it alone. Subsequent prompts
(including drained queued ones) do not re-title.

**The contradiction this resolves.** Design A says "only the first sets
the session title" (`adk-graph-coordinator-A.md:31-32`), but the code
calls `m.applySessionTitle(text)` on every `submitPrompt`
(agent_loop.go:332-336). The codex review flagged this as an internal
inconsistency (review.md:21).

**The current slash-command behavior to preserve.** `handleSlashCommand`
already routes every non-`/clear`/`/exit`/`/quit` command through
`m.setSessionTitle(input)` (commands.go:35-38) — i.e. typing
`/branch foo` rewrites the session title to `/branch foo`. That is the
existing in-app convention; the plan keeps it.

**Full rule (in priority order):**

1. **`/clear`** → reset title to empty (commands.go:35, commands.go:63).
   The `titleSet` flag resets to `false` here so the next user prompt
   re-titles.
2. **`/exit`, `/quit`** → leave the title alone (commands.go:35 skips
   `setSessionTitle` for these).
3. **Any other slash command** (`/help`, `/model`, `/branch foo`, …) →
   set the title to the slash command text via the existing
   `m.setSessionTitle(input)` path (commands.go:37). The `titleSet` flag
   becomes `true`; subsequent plain prompts leave it alone.
4. **First user prompt of a session** → set the title from its first
   line (the existing `applySessionTitle` path, gated by `!titleSet`).
5. **Subsequent user prompts** (including all drained queued prompts) →
   leave the title alone.

**Implementation.** A `titleSet bool` flag on `model`:

- Default `false`; flipped to `true` by the first `applySessionTitle` call
  from `submitPrompt`.
- Flipped to `true` by `setSessionTitle(non-empty)` (covers the slash-
  command case at commands.go:37).
- Flipped to `false` by `setSessionTitle("")` (covers `/clear` at
  commands.go:63).
- Reset to `false` on session switch (see "Session-switch / restart").

The `submitPrompt` call site (agent_loop.go:332-336) becomes
`if !m.titleSet { m.applySessionTitle(text); m.titleSet = true }`. The
slash-command case is already correct — no change to
`handleSlashCommand`.

This supersedes Design A's terser claim, composes correctly with the
existing slash-command → title behavior, and matches the review's
recommendation that we "state the intended rule and its stored flag"
(review.md:21).

## Cancellation semantics

| Trigger | Active turn | Pending queue | Notes |
| --- | --- | --- | --- |
| **Esc** | Cancel (existing, tui.go:1009-1011) | Preserved | Same as today. |
| **Ctrl+C** (1st press while running) | Cancel (existing, tui.go:1014-1019) | Preserved | Shows "Ctrl+C again to quit". |
| **Ctrl+C** (2nd press) | Already cancelled; quits the TUI (tui.go:1022-1024) | Lost | The user has asked to leave; the queue goes with the process. Documented. |
| **Ctrl+C** (1st press, idle) | n/a | Preserved | "Ctrl+C again to quit" warning, no turn running. |
| **`/clear`** | confirm-and-cancel | Preserved (dropped on `y`, kept on `N`) | The flash asks the user to confirm cancelling the active turn; on `y` the queue is preserved but the active turn is cancelled and `/clear` runs. |
| **`/exit`, `/quit`** | Blocked (per policy) | Preserved (in memory; lost on the 2nd Ctrl+C quit) | See policy table. |

**Why preserve the queue on cancel.** Cancel is a turn-level operation
("stop what the current agent is doing"). The queued prompts are user
intent the user explicitly expressed. Dropping them silently on a
mistaken Esc would be worse than surprising; the user can `/clear` the
queue later (a follow-up) or just let the next legitimate turn run the
drain.

**`/queue clear` follow-up.** Not in this PR. The current chat input is
editable; the user can manually inspect `m.pendingInputs` via a future
`/queue` command. For v1, the queue badge tells the user something is
queued; clearing means "let the next prompt run by itself, which will
also drain whatever is left."

## Session-switch / restart

| Event | `m.pendingInputs` | `m.titleSet` | Reasoning |
| --- | --- | --- | --- |
| User runs `/clear` | Preserved | Preserved (`setSessionTitle("")` already does the right thing) | `/clear` is a chat-level reset, not a queue reset. |
| User runs `/model` | Preserved | Preserved | Same session, new model. |
| User runs `/session` (no args) | Preserved | Preserved | Print-only: appends a message showing the current `SessionID` (commands.go:68-72). Doesn't change session. |
| Process start (new session) | Empty | `false` | Brand-new session. |
| Process killed (Ctrl+C × 2) | Lost | Lost | In-memory only. |
| TUI exits (`/exit`, `/quit`) | Lost | Lost | In-memory only. |

**Q1 resolved (user feedback).** There is no in-app "load a different
session" path in the current codebase. The runtime only ever creates
sessions — `m.cfg.SessionID =` is set at exactly one site, the
session-creation event handler at `tui.go:1969`. `/session` is a
print-only echo of the current ID (commands.go:68-72). Resuming a
prior session is `--resume` on the CLI, which is a different
process. So the queue-reset-on-load hook that the plan originally
speculated about **does not exist in v1** and is not needed.

**Implication.** Because sessions are only created in-process, the
only way to lose the queue mid-session is to exit the TUI. The
queue-reset branch on the session-switch path is therefore dormant in
v1; if/when an in-app `/load <id>` is added, that handler is where
`pendingInputs = nil; titleSet = false` will be wired.

**Ephemeral design.** This matches the review (review.md:23, "retain
queued text+mentions, queue limit/backpressure, duplicate/history
behavior") and Design A's `adk-graph-coordinator-A.md:65-67` ("Queued
inputs survive only in-memory; a process restart drops them
(acceptable for v1).").

## Rendering

What the user sees, in each state:

| State | Status bar | Input | Chat |
| --- | --- | --- | --- |
| **Idle** | `[chat] │ ctx: … │ tkn: …` | `> ` + textinput | Current messages. |
| **Running, queue empty** | `[thinking] │ tool: bash (2s)` (or `[chat]` if no tool) | `> ` + textinput (editable) | Streaming assistant message at bottom. |
| **Running, queue has 2** | `[thinking] │ tool: bash (2s) │ queued: 2` | `> ` + textinput (editable) | Streaming assistant message at bottom. |
| **Queue overflow** | `[thinking] │ tool: bash (2s) │ queued: 32 │ queue full` (flash) | `> ` + textinput (editable) | AppendWarning: "Queue full (32) — dropped oldest prompt: …". |

**No "queued: <text>" placeholder in the chat.** Design A is silent on
this (review.md:22 calls it out as missing). The plan: **do not render
queued prompts as ghost messages in the chat** in v1. Reasoning:

- The chat is the *active* conversation. Inserting ghost rows that
  disappear when their turn starts would be confusing — the user sees
  a message, types, and watches the message vanish.
- The status-bar badge is sufficient affordance.
- The chat already shows the user *what they typed in this turn* at the
  moment `submitPrompt` appends it; queued prompts simply haven't been
  appended yet.

The status-bar badge is the single source of truth for "something is
queued." A future enhancement (follow-up, not in this PR) could add a
`/queue` slash command to list the queue with delete-one support, which
is a cleaner fix than chat placeholders.

**Cursor.** The input cursor is shown whenever the input is rendered. In
`View()` (tui.go:1276-1279), the `if !m.running && !m.loading` gate hides
the cursor. **Change it to** `if !m.loading` (drop the `!m.running`),
so the cursor stays visible while the agent runs. The queue badge in
the status bar carries the "busy" affordance, not the input cursor.

## Error handling

| Condition | Behavior |
| --- | --- |
| `pendingInputs` is full on enqueue | Drop the oldest entry, append warning, set flash. Documented in `enqueuePrompt`. |
| Active turn errors (`agentDoneMsg{err: …}`) | Still drain. The error is appended to the chat, the next queued prompt starts immediately. Matches `adk-graph-coordinator-A.md:75-77`. |
| `m.cfg.Agent == nil` on drain | **No-op; do NOT consume the head.** Documented in `drainNextQueued`. This prevents tests that drive `handleAgentDone` without wiring an agent from silently losing queued prompts. |
| Cancel during a queued run | The cancel hits the *active* turn (not the queue). When the active turn's `agentDoneMsg` arrives, drain resumes. |
| Process restart | Queue is lost. Documented above. |
| Paste while running | Suppressed by the existing `!m.running` check in `handlePaste` (tui.go:600). Documented; v1 keeps this. |

## Test plan

### Unit tests (no real LLM)

In a new file `internal/tui/queue_test.go`:

1. `TestEnqueuePrompt_BasicFIFO` — push three prompts, confirm
   `pendingInputs` length and order; pop one, confirm head is correct.
2. `TestEnqueuePrompt_OverflowDropsOldest` — push `maxQueueDepth + 1`,
   confirm a warning is appended and the queue length is
   `maxQueueDepth`.
3. `TestDrainNextQueued_Empty` — call on empty queue, no-op, no cmd
   returned.
4. `TestDrainNextQueued_NilAgent` — set up model with `cfg.Agent = nil`
   and a non-empty queue, call drain, confirm queue is **not** consumed
   (the new no-op contract).
5. `TestDrainNextQueued_AdvancesOnError` — simulate `handleAgentDone`
   with `err: errors.New("boom")`, confirm the next queued prompt is
   started (drain called inside `handleAgentDone`).
6. `TestSubmitPrompt_TitleOnlyFirstTime` — submit "first prompt",
   confirm `titleSet = true` and `m.sessionTitle` is derived. Submit
   "second prompt", confirm `m.sessionTitle` is unchanged.
7. `TestSlashCommandPolicy_BlockWhileRunning` — for `/clear`, `/model`,
   `/exit`, `/quit`, `/restart`, `/compact`: set `m.running = true`,
   invoke the command via `Update(InputSubmitMsg{Text: cmd})`, confirm
   the flash is set and the underlying state (`m.chatModel.Messages`
   for `/clear`, `m.cfg.ModelName` for `/model`, etc.) is untouched.
8. `TestSlashCommandPolicy_QueueWhileRunning` — for `/branch`, `/plan`,
   `/run`, `/commit`, `/login`: confirm `m.pendingInputs` grows.
9. `TestSlashCommandPolicy_PassThrough` — for `/help`, `/session`,
   `/context`, `/subagents`, `/history`, `/ping`, `/rtk`, `/mcp`,
   `/theme`, `/skills`: confirm the command runs as before with
   `m.running = true`.
10. `TestEnqueuePrompt_HistoryIsRecorded` — verify the input model's
    `History` slice records the queued text even before the queue
    dispatches (so Up-arrow recall works for queued prompts too).
11. `TestHandleAgentDone_AdvancesQueueOnError` — call
    `m.Update(agentDoneMsg{err: …})` with a pre-loaded queue and
    `m.running = true`; confirm the queue shrinks by one and a new
    `agentCh` is opened (via the returned cmd batch).

### End-to-end teatest

In a new file `internal/tui/queue_e2e_test.go`, exercising
`teatest.NewProgram` or a hand-rolled `Update` driver (matching the
existing pattern in `teatest_test.go`):

12. `TestE2E_QueueDrainsSequentially` — wire a slow `fnLLM` that
    records each prompt it sees; submit three prompts back-to-back via
    `InputSubmitMsg` while the first is in flight. Assert:
    - The input stayed editable throughout (`m.inputModel.Text` is
      empty after submit; the cursor position can be re-set).
    - The order of prompts seen by the LLM is exactly
      `[first, second, third]`.
    - The status-bar `QueueDepth` went `0 → 2 → 1 → 0` over the
      lifetime of the three turns.
    - After all three, `m.pendingInputs` is empty and `m.running` is
      `false`.

These 12 tests can all live in the same `queue_test.go` /
`queue_e2e_test.go` pair, with `newHandlerModel()` from
`agent_loop_handlers_test.go` (line 12) as the fixture builder.

## Acceptance criteria (cross-checked against `adk-graph-coordinator-A.md:70-77`)

- [ ] **AC1 (Design A §Acceptance line 73):** Given a running agent, when
  the user types a prompt and presses Enter, then the input is accepted
  and the prompt is queued (no error flash). **Verified by:** test #1
  and test #9 (pass-through slash commands), plus manual reproduction.
- [ ] **AC2 (line 75):** Given queued prompts, when the current run
  completes, then the next queued prompt starts automatically in order.
  **Verified by:** test #12 (e2e sequence).
- [ ] **AC3 (line 77):** Given queued prompts, when a turn errors, then
  the next queued prompt still runs. **Verified by:** tests #5 and #11.
- [ ] **AC4 (this plan):** Status bar shows a `queued: N` badge when
  `N > 0`. **Verified by:** test #12's depth probe plus a small
  `TestStatusRender_QueueBadge` that drives `statusModel.Render` with
  `QueueDepth = 0` and `> 0` and asserts the substring.
- [ ] **AC5 (this plan):** Title is set on the first prompt only.
  **Verified by:** test #6.
- [ ] **AC6 (this plan):** All slash commands in `slashCommands`
  follow the busy-time policy table. **Verified by:** tests #7, #8, #9.
- [ ] **AC7 (this plan):** Cursor is visible in the input while the
  agent runs. **Verified by:** assertion in test #12 that the
  `tea.Cursor` returned from `m.View()` is non-nil throughout.

## Risks & follow-ups

Ranked by impact:

1. **Dynamic skill commands default to `slashConfirmAndCancel`.** A
   skill may cancel context by design (e.g. `/cancel-task`). The
   `isDynamicSkill(cmd)` branch in `handleSlashCommand` applies the
   confirm-and-cancel policy uniformly, which is the right default.
   Future per-skill manifests can override to `slashPassThrough` for
   read-only skills. *Addressed in this PR (v1 default); per-skill
   override is a follow-up.*

2. **`/ping` runs an extra LLM call while a turn is in flight.** Cheap
   for the user (one short call) but it does double-bill the token
   budget for that minute. Acceptable; `slashPassThrough` is correct.
   *Documented in the policy table.*

3. **`/clear` resets `m.running = false` (commands.go:52).** This was
   the old "instant reset" path; with our policy, `/clear` while
   running requires `slashConfirmAndCancel`, so the line is unreachable
   on the busy path (the cancel goes through `m.agentCancel()` first)
   but still correct on the idle path. *Documented only.*

4. **No `pendingInputs` UI for the user to inspect/edit the queue.**
   The badge tells you the count, not what's in it. Follow-up: a
   `/queue` slash command that lists the queue and accepts
   `drop <n>` / `clear` subcommands. *Tracked.*

5. **Session-switch queue reset is dormant in v1** because there is
   no in-app session-load path (resolved Q1 — see "Session-switch /
   restart"). When `/load <id>` is added later, the queue-reset hook
   lives at that update site. *Tracked.*

6. **Title rule overrides the existing "every prompt rewrites the
   title" behavior.** Users who relied on the churn to keep the
   terminal title aligned with their latest task will see the title
   freeze. *Documented; the change is intentional.*

7. **`handlePaste` still suppresses paste while running.** Pasting a
   large block of text into the input while a turn is running is
   ignored (tui.go:600). This was the safer default and the review did
   not call for a change, but a user who pastes a long prompt will
   wonder where it went. *Documented; can be revisited.*

8. **No persistence of the queue across process restart.** Matches
   Design A and the review. *Accepted; documented.*

9. **The `m.titleSet` flag is not exposed to `/title <text>`.** That
   command already routes through `setSessionTitle` directly and does
   not need the flag. The flag is per-session and naturally resets on
   session switch. *No action.*

### Open questions

**Q1 (resolved).** Does `/session <id>` load a different session in
the running TUI? **No.** `rg "SessionID\s*=" internal/tui/` shows the
runtime only assigns `m.cfg.SessionID` at one site — the
session-creation event handler at `tui.go:1969`. The `/session` slash
command is print-only (commands.go:68-72). Session resumption is
`--resume` on the CLI, which is a separate process. The original
queue-reset-on-load hook is therefore dormant in v1; track it for
when an in-app load is added.

**Q2.** Does `/exit` while the agent is running need to drain the
queue first, or just bail? The current plan blocks `/exit` while
running. A user who tries `/exit` mid-turn will see a "blocked" flash
and have to Ctrl+C twice. That is fine for v1; a follow-up could drain
gracefully. *No action for this PR.*

**Q3.** Should the queue badge color depend on the count (green
< 8, peach 8-31, red = full)? The plan uses a fixed peach style
(matching `/run`'s `cycle N/N` indicator at status.go:218-222). *Defer
to a styling pass; no behavior impact.*

**Q4.** When the user holds Enter or pastes a long block, could
`maxQueueDepth = 32` be hit in practice? It would take 32 distinct
Enter presses before the agent finishes one turn, which is a long
turn. The cap is a safety net, not a target. *No action.*

## Verification

```bash
# All steps compile and pass tests in isolation.
go build ./...
go test ./internal/tui/... -run 'Queue|Enqueue|Drain|SlashCommandPolicy|Title' -count=1
go test ./internal/tui/... -run 'E2E_QueueDrainsSequentially' -count=1
go test ./internal/tui/... -count=1
go vet ./...
```

A working branch for this PR should land the diff in ~5 commits,
matching the vertical slices below. Each commit compiles and `go
test ./internal/tui/...` passes after it lands.

## Vertical slice plan (commit-by-commit)

The diff is small enough for a single PR but large enough to merit
five reviewable commits. Each is independently green.

1. **Add the queue state and `enqueuePrompt` / `drainNextQueued`
   helpers + unit tests for FIFO, overflow, nil-agent no-op.**
   *Files:* `internal/tui/tui.go` (fields), `internal/tui/input.go`
   (`queuedPrompt`), `internal/tui/agent_loop.go` (helpers + constant
   + `truncate`), new `internal/tui/queue_test.go` (tests 1-4).
   *Verify:* `go test ./internal/tui/... -run 'Enqueue|Drain'`.
2. **Wire `updateTerminal`'s `InputSubmitMsg` branch to
   `enqueuePrompt` when `m.running`, plus the cursor visibility fix
   in `View()`.** *Files:* `internal/tui/tui.go` (routing + cursor
   gate), `internal/tui/input.go` (`View(running)` ignores its arg).
   *Verify:* `go test ./internal/tui/...`.
3. **Resolve the title contradiction: add `titleSet`, gate
   `applySessionTitle` in `submitPrompt`, add the unit test.**
   *Files:* `internal/tui/tui.go` (field), `internal/tui/agent_loop.go`
   (gate), `internal/tui/queue_test.go` (test #6).
   *Verify:* `go test ./internal/tui/... -run 'Title'`.
4. **Add the slash-command busy-time policy + per-command unit
   tests.** *Files:* `internal/tui/commands.go` (policy type, table,
   wrapper around `handleSlashCommand`), `internal/tui/queue_test.go`
   (tests #7-#9).
   *Verify:* `go test ./internal/tui/... -run 'SlashCommand'`.
5. **Add the status-bar queue badge, hook it through
   `statusRenderInput`, add the e2e sequence test.**
   *Files:* `internal/tui/status.go` (field + render line),
   `internal/tui/tui.go` (populate `QueueDepth`),
   new `internal/tui/queue_e2e_test.go` (test #12 + AC4 unit test).
   *Verify:* `go test ./internal/tui/...`.

After commit 5: full `go test ./...`, `go vet ./...`, manual smoke
test of the TUI (type a prompt, press Enter, type another while the
first is running, watch the badge count down).
