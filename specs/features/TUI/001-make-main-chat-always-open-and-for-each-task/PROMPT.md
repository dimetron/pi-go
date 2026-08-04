# Plan A: TUI Non-Blocking Input Queue

## Objective

Make the main chat input always editable. When the user presses Enter
while an agent turn is in flight, the prompt is **enqueued** in FIFO
order and dispatched as a sequential follow-up turn in the same ADK
session. The user can keep typing, see a queue-count badge in the
status bar, and Ctrl+C cancels only the active turn (the queue is
preserved). Subagent execution model is untouched; no ADK or ACP
protocol changes.

## Scope

### In scope

- FIFO prompt queue on `model` (`pendingInputs []queuedPrompt`).
- Remove the read-only lock at `internal/tui/tui.go:888` so typing,
  history, and slash-command editing work while a turn runs.
- Route `InputSubmitMsg` to `submitPrompt` (idle) or `enqueuePrompt`
  (running), and drain one queued prompt at the end of every turn via
  `handleAgentDone`.
- Explicit busy-time policy for every slash command in `slashCommands`
  (`internal/tui/input.go:272`) plus the dynamic-skill branch:
  `pass-through` / `queue` / `block` / `confirm-and-cancel`.
- `titleSet bool` flag implementing "title main only" (first user
  prompt + any non-`/clear`/`/exit`/`/quit` slash command set it;
  subsequent plain prompts leave the title alone).
- Queue-count badge in the status bar (`StatusRenderInput.QueueDepth`).
- `/clear`/`/model`/`/branch`/`/compact` and dynamic skills while
  running require `y/N` confirmation before cancelling the active turn
  (calls `m.agentCancel()` on `y`, drops on `N`).

### Out of scope (v1)

- Multi-session / tab-per-session UI (no `ListSessions` exists; see
  `internal/acp/server/agent.go:274`).
- In-app session-load path (Q1 resolved: no such path today; `/session`
  is print-only at `commands.go:68-72`).
- ADK task-mode sub-agents (Design B) or `TaskManager` orchestration
  (Design C) — see `research/adk-graph-coordinator.md` for the
  trade-offs.
- ACP live-session steering (sending a second prompt into a live ACP
  session is not supported by `internal/acp/client/session.go`).
- `/queue inspect|clear` UI for the queue.
- Queue persistence across process restart.
- Per-skill manifest override for `slashConfirmAndCancel`.

## Files to change

| File | Change |
| --- | --- |
| `internal/tui/tui.go` | New model fields `pendingInputs []queuedPrompt`, `titleSet bool`, `pendingConfirm *pendingConfirm`; remove busy gate at `tui.go:888`; route `InputSubmitMsg` (already at `tui.go:543`); add `QueueDepth` to `statusRenderInput` |
| `internal/tui/agent_loop.go` | `enqueuePrompt`, `drainNextQueued`, `truncate`, `maxQueueDepth = 32`; gate `applySessionTitle` with `!titleSet`; call `drainNextQueued` at the end of `handleAgentDone` (`agent_loop.go:801`) |
| `internal/tui/input.go` | `queuedPrompt{text, mentions}` type; `View(running)` ignores the `running` arg so the input stays editable |
| `internal/tui/commands.go` | `slashCommandBusyPolicy` type + 4-policy table; `requestCancelConfirm(cmd, input)` helper; gate `handleSlashCommand` (`commands.go:18`) |
| `internal/tui/status.go` | `StatusRenderInput.QueueDepth int`; render `[queued: N]` badge next to active-tool indicator |
| `internal/tui/queue_test.go` *(new)* | Unit tests (FIFO, overflow, slash-command policy matrix, title rule, error path) |
| `internal/tui/queue_e2e_test.go` *(new)* | teatest e2e: submit 3 prompts while mock agent runs, assert sequential execution and editable input |

## Implementation order (vertical slices)

Each slice is one commit and ends with green tests.

1. **Slice 1 — data + helpers (no UX change yet).** Add `queuedPrompt`
   type (`input.go`), `pendingInputs`/`titleSet` fields on `model`
   (`tui.go`), `enqueuePrompt`/`drainNextQueued`/`truncate`/
   `maxQueueDepth` (`agent_loop.go`). Unit tests for FIFO, overflow,
   nil-agent no-op. Build + test green; no user-visible change.
2. **Slice 2 — wire the gate off.** Remove the `running || loading`
   early-return at `tui.go:888`. Change `InputSubmitMsg` route
   (`tui.go:543`) to call `enqueuePrompt` when `m.running` and
   `submitPrompt` when idle. Call `drainNextQueued` at the end of
   `handleAgentDone` (`agent_loop.go:801`). e2e test #12: submit 3
   prompts, assert sequential dispatch and editable input. Gate
   `applySessionTitle` with `!titleSet`. Build + test green.
3. **Slice 3 — slash-command policy + confirm-and-cancel.** Add
   `slashCommandBusyPolicy` type and 4-policy table in `commands.go`.
   Add `requestCancelConfirm` helper. Gate `handleSlashCommand`. Add
   unit tests for the policy matrix (every command in
   `slashCommands` + dynamic skill). Build + test green.
4. **Slice 4 — status badge.** Add `QueueDepth` to `StatusRenderInput`
   and render `[queued: N]` in `status.go`. Visual-only change; e2e
   asserts badge updates.

Do **not** combine slices. Each commit must leave the build green and
the test suite passing.

## Acceptance criteria

(Mirror of `plan-A.md` §Acceptance, cross-checked against Design A.)

### Functional

- Given a running agent, when the user types a prompt and presses
  Enter, then the input is accepted and the prompt is queued (no error
  flash).
- Given queued prompts, when the current run completes, then the next
  queued prompt starts automatically in order.
- Given queued prompts, when a turn errors, then the next queued
  prompt still runs.
- Given `/clear` (or `/model`, `/branch`, `/compact`, dynamic skill)
  issued while running, when the user types `y`, then the active turn
  is cancelled via `m.agentCancel()` and the command runs.
- Given the same input, when the user types `N`, then the command is
  dropped and the user's typed text is preserved.
- Given `/restart`, `/exit`, or `/quit` while running, when issued,
  then a "blocked while running" flash is shown and the command is
  ignored (Ctrl+C × 2 remains the escape hatch).
- Given the first user prompt of a session, when submitted, then the
  session title is set from its first line.
- Given subsequent user prompts (including drained queued prompts),
  when submitted, then the session title is not changed.
- Given `/clear`, when run, then the title is reset to empty and the
  next user prompt re-titles.

### Non-functional

- Queue is in-memory only; process restart drops it.
- `maxQueueDepth = 32`; over-cap appends are dropped (oldest first)
  with a chat warning, not a panic.
- Input remains editable in all states (idle, running, queue full,
  confirming).
- The `m.running` field remains a single bool on `model` — no
  per-session state.

## Gates

- **build**: `go build ./...`
- **vet**: `go vet ./...`
- **test**: `go test ./internal/tui/...`
- **e2e**: `go test -tags e2e ./internal/tui/...` (the queue_e2e_test.go
  uses teatest; tag if needed to match existing TUI e2e conventions)
- **pi-release**: full lint + coverage gate before merge (per
  `specs/AGENTS.md`)

## Reference

- Design: `research/adk-graph-coordinator-A.md` (Design A source of
  truth)
- Plan: `plan-A.md` (this directory — full file-by-file change set,
  code sketches, cancellation table, test plan)
- Code review: `research/adk-graph-coordinator-review.md` (issues
  addressed in this plan)
- Rough idea: `rough-idea.md`

## Constraints

- Match existing `internal/tui/` style: file:line anchors, inline
  comments in the same voice as surrounding code, no new external
  dependencies.
- Do not change the subagent execution model, the ADK runner, or the
  ACP server.
- Do not add new flags or config — this is a UX change, not an
  optioned one.
- Keep the diff small: target ~+170 / −14 production lines across 5
  files, ~+400 lines of new tests across 2 new files.

## Open questions

None blocking v1. Deferred to follow-ups:

- Q2 — `/exit` while running: drain first or bail? Currently blocked
  with a flash; Ctrl+C × 2 is the escape hatch.
- Q3 — Color the queue badge by depth? Fixed peach in v1; styling
  pass later.
- Q4 — Is `maxQueueDepth = 32` ever hit in practice? 32 distinct
  Enters before a turn ends is unlikely; revisit if it ever triggers.

## Notes for the implementer

- **The `titleSet` flag is the only state that crosses the
  `applySessionTitle` boundary.** Read `plan-A.md` §"Title semantics
  (resolved)" before touching `agent_loop.go:332-336`.
- **`requestCancelConfirm` is the only stateful confirm helper.** It
  arms `m.pendingConfirm` and returns a `tea.Cmd` that listens for the
  next keystroke; on `y`/`Y`/`Enter` it calls `m.agentCancel()` and
  re-enters `handleSlashCommand` with the original input. On `N` or
  any other key it clears the confirm state and restores the user's
  typed text. The `pendingInputs` queue is preserved in either case.
- **The slash-command policy table lives in `commands.go`, not
  `input.go`.** `input.go` only has the autocomplete list; the busy-
  time policy is a TUI concern, not an input-parsing concern.
- **Don't render queued prompts as ghost messages in the chat.** v1
  uses the status-bar badge only. The chat shows the active turn and
  the just-finished turn; the queue is invisible to the conversation
  until it dispatches.
