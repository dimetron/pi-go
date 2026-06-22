# Plan: `pi plan resume`

## Objective

Enable interrupted `/plan` sessions to be resumed from the exact PDD phase where they left off, with full context of what was already discussed and written to the spec directory.

## Motivation

When a `/plan` session is interrupted (Ctrl+C, network error, context limit), the user must re-type the full rough idea and the LLM has no memory of previous phases. This wastes context and creates friction. The fix: persist just enough context in the session metadata to reconstruct the plan state on resume.

## Scope

- TUI `/plan` command only (not CLI modes, not `/run`)
- Resumable from within the same TUI process
- Cross-session resume (user quits and restarts TUI) via the existing `--session` mechanism

## Out of Scope

- Auto-resume on crash/reconnect (future work)
- Plan session conflict detection (e.g., spec dir was modified externally)
- `/run` resume (separate tracking mechanism)

---

## Checklist

- [ ] Step 1: Extend `session.Meta` with `PlanContext`
- [ ] Step 2: Add `UpdatePlanContext` to session store
- [ ] Step 3: Update `startPlanSession` to persist context
- [ ] Step 4: Add `/plan resume` command handler
- [ ] Step 5: Wire up `Ctrl+C` → update context before interrupting
- [ ] Step 6: Write unit tests
- [ ] Step 7: Integration verification

---

## Step 1: Extend `session.Meta` with `PlanContext`

**Objective:** Store the plan session key in session metadata.

**Implementation:**
- Add `PlanContext` struct to `internal/session/store.go`:

```go
// PlanContext holds the /plan session context for resume.
type PlanContext struct {
    TaskName  string `json:"taskName,omitempty"`  // e.g. "features/TOO/001-add-logging"
    RoughIdea string `json:"roughIdea,omitempty"`  // original rough idea
    SpecDir   string `json:"specDir,omitempty"`   // absolute path to spec directory
    Phase     string `json:"phase,omitempty"`     // "plan" (same mode for now)
}
```

- Add `PlanContext *PlanContext` field to `Meta` struct (line ~30 in store.go)

**Test requirements:**
- `TestMetaPlanContext` — Meta with PlanContext marshals/unmarshals correctly
- Existing meta tests still pass

---

## Step 2: Add `UpdatePlanContext` to session store

**Objective:** Expose a way to update plan context in the current session.

**Implementation:**
- Add to `FileService` in `internal/session/store.go`:

```go
// UpdatePlanContext updates the plan session context in the session metadata.
// Pass nil to clear the context.
func (s *FileService) UpdatePlanContext(sessionID string, ctx *PlanContext) error {
    sess, ok := s.sessions[sessionID]
    if !ok {
        return fmt.Errorf("session not found: %s", sessionID)
    }
    sess.mu.Lock()
    defer sess.mu.Unlock()
    sess.meta.PlanContext = ctx
    return sess.writeMetaLocked()
}
```

**Test requirements:**
- `TestUpdatePlanContext_Set` — sets context on existing session
- `TestUpdatePlanContext_Clear` — clears context when ctx is nil
- `TestUpdatePlanContext_NotFound` — returns error for unknown session

---

## Step 3: Update `startPlanSession` to persist context

**Objective:** When a `/plan` session starts, persist the context to session metadata.

**Implementation:**
- In `internal/tui/plan.go`, after `startPlanSession` successfully creates session and sets mode, add:

```go
// Persist plan context for resume.
if m.cfg.SessionService != nil {
    if fs, ok := m.cfg.SessionService.(*session.FileService); ok {
        _ = fs.UpdatePlanContext(m.cfg.SessionID, &session.PlanContext{
            TaskName:  taskName,
            RoughIdea: roughIdea,
            SpecDir:   specDir,
            Phase:     "plan",
        })
    }
}
```

Note: Errors are non-fatal — session persists even if metadata update fails.

**Test requirements:**
- Integration test verifies context is persisted after startPlanSession

---

## Step 4: Add `/plan resume` command handler

**Objective:** Allow users to resume a plan session with a single command.

**Implementation:**
- Extend `planState` in `internal/tui/plan.go` to include `sessionID string`

- Add `handlePlanResumeCommand()` function that:
  1. Gets plan context from session metadata via `fs.GetPlanContext(sessionID)`
  2. If no context found, shows error: "No active plan session to resume"
  3. Validates the spec directory still exists
  4. Reloads the PDD SOP
  5. Rebuilds agent with augmented instruction (same as `startPlanSession`)
  6. Uses the **existing** session ID (no new session created — this is the key resume behavior)
  7. Clears conversation and injects a "Resume" prompt with the taskName and specDir
  8. Sets `mode = "plan"`, `m.running = true`

- Add to `slashCommands` in `tui.go`: `/plan resume`
- Add case in `handleSlashCommand` in `commands.go`: `case "/plan resume":`

- Add to `/help` output: `| \`/plan resume\` | Resume an interrupted plan session |`

**Test requirements:**
- `TestHandlePlanResume_NoContext` — shows error when no context
- `TestHandlePlanResume_ValidContext` — resumes with correct taskName/specDir
- `TestHandlePlanResume_MissingSpecDir` — shows error when spec dir was deleted
- `TestHandlePlanResume_UsesExistingSession` — same session ID preserved

---

## Step 5: Wire up Ctrl+C → update context before interrupting

**Objective:** When agent is interrupted, persist the context so `/plan resume` works immediately after.

**Implementation:**
- In `handleInterrupt()` in `tui.go`, after the `m.running = false` assignment but before canceling the context:
  1. If in plan mode (`m.mode == "plan"`), check if `m.plan` is nil (shouldn't be, but guard)
  2. If session service is available, update plan context with taskName/roughIdea/specDir from current state

Actually, the plan context was already saved at session start in Step 3. The key insight: **the context is saved at session start**, not on interrupt. So Ctrl+C just interrupts the agent loop — the session already has the context.

The only additional change: when user does `Ctrl+C` during a plan session, the `planState` is cleared but the session metadata still has the context. On next `/plan resume`, it reads the context and resumes.

**What about the `planState` in the TUI model?** It's separate from session metadata. The `planState` tracks the TUI-level flow (override confirmation). The `PlanContext` in session metadata tracks what's needed for cross-session resume. They serve different purposes.

**Edge case:** User starts `/plan`, exits TUI, then re-opens. The session metadata has the plan context. User types `/plan resume`. The handler reads context, resumes in the existing session.

**Implementation:**
- In `handleInterrupt()` in `tui.go`, if `m.running && m.mode == "plan"`:
  - Save the current plan context info to `planState` fields if needed, OR
  - Just rely on the context saved at start (Step 3)

Actually, let me reconsider. The issue is that when we `handleInterrupt()` and the agent stops:
- `m.running = false`
- `m.plan = nil` (cleared implicitly when `m.running` becomes false? Or we need to save it?)

Looking at the code, `m.plan` is only set during override confirmation (`confirming_override`). When the plan session is running, `m.plan` is `nil` (because `startPlanSession` never sets `m.plan` — it only sets `m.mode = "plan"`).

So the session metadata approach is the right one. The context is saved at `startPlanSession` time. The user can always resume from that point.

The only question: what if the plan session was partially through (e.g., LLM wrote `outline.md`)? The `/plan resume` will reload the SOP, rebuild the agent with the same instruction, and the LLM will have the same conversation history (because it's the same session). So it should naturally pick up where it left off — the spec directory already has the artifacts.

**Test requirements:**
- E2E: Start `/plan`, interrupt with Ctrl+C, type `/plan resume`, verify agent resumes with same session

---

## Step 6: Write unit tests

**Objective:** Comprehensive test coverage for the new functionality.

**Implementation:**
- Create `internal/tui/plan_resume_test.go`:
  - `TestHandlePlanResumeCommand_NoContext` — no plan context in session
  - `TestHandlePlanResumeCommand_ValidContext` — resumes with correct state
  - `TestHandlePlanResumeCommand_MissingSpecDir` — spec dir deleted externally
  - `TestHandlePlanResumeCommand_CreatesNewSession` — uses existing session ID
  - `TestHandlePlanResumeCommand_PDDSOPReloaded` — SOP is loaded on resume
  - `TestHandlePlanResumeCommand_ClearsConversation` — TUI chat cleared on resume

- Add to `internal/session/store_test.go`:
  - `TestUpdatePlanContext_Set`
  - `TestUpdatePlanContext_Clear`
  - `TestUpdatePlanContext_NotFound`

**Test requirements:**
- All tests pass: `go test ./internal/tui/... ./internal/session/...`
- `go build ./...` succeeds

---

## Step 7: Integration verification

**Objective:** Verify the full plan resume flow works end-to-end.

**Implementation:**
Manual verification steps:
1. Start TUI: `go run ./cmd/pi`
2. Type `/plan add rate limiting to API`
3. Wait for LLM to respond with initial PDD question
4. Type Ctrl+C to interrupt
5. Type `/plan resume`
6. Verify: same session, SOP loaded, conversation continues
7. Exit TUI, restart TUI with `--session <previous-session-id>`
8. Type `/plan resume`
9. Verify: cross-session resume works

**Test requirements:**
- All gates pass: `go build ./... && go test ./internal/tui/... ./internal/session/...`
- Manual E2E verified as described above

---

## Design Notes

### Why persist at session start?

The session is created fresh in `startPlanSession` (line 315). At that point we have all the context needed. Persisting at start means:
- If the process crashes after start but before any progress, resume works
- Simpler logic: save once, read on demand
- The session JSONL already has the conversation — resume just needs the metadata

### Why not save on every interrupt?

Because the agent loop doesn't have a "before interrupt" hook in the right place. Adding one would require changing the agent loop architecture. Saving at start is sufficient for the use case.

### What about the planState?

`planState` tracks TUI-level flow (override confirmation dialogs). It's not persisted — it's ephemeral TUI state. The `PlanContext` in session metadata is what's needed for cross-session resume.

### What about `/run resume`?

Separate concern. `/run` tracks `specName`, `checklist`, gate results. It's a different kind of state. Future work to add `/run resume` would need its own context structure.
