# Design: `pi plan resume`

## Context

The `/plan` command initiates a PDD (Purpose-Directed Planning) session with a subagent. The session is created fresh each time, with no memory of previous runs. When interrupted, the user must re-type the rough idea and start over.

This design extends the existing session metadata system to support plan context persistence, enabling both in-process and cross-session resume of interrupted `/plan` sessions.

## Background

### Existing Session System

pi-go sessions are stored in `~/.pi-go/sessions/{sessionID}/`:
- `meta.json` — session metadata (ID, AppName, UserID, WorkDir, Model, timestamps)
- `events.jsonl` — all conversation events (text_delta, tool_call, etc.)

The `FileService` manages session creation, event appending, and metadata updates. Sessions are identified by UUID and persist across process restarts.

### Current `/plan` Flow

```
User types /plan <rough idea>
  → handlePlanCommand() parses input, derives taskName
  → createSpecSkeleton() creates specs/{taskName}/
  → startPlanSession()
      → sop.LoadPDD() loads PDD SOP
      → agent.RebuildWithInstruction() injects system prompt
      → agent.CreateSession() creates NEW session
      → Clear TUI chat
      → m.mode = "plan"
      → go runAgentLoop(roughIdea)
```

Key issue: a **new session** is created at line 315-323. If the user interrupts and restarts, that session is lost — they have to create a new one.

### Session Metadata Flow

Sessions are created via `FileService.Create()`, which:
1. Creates the session directory
2. Writes `meta.json` with AppName, UserID, WorkDir, Model, timestamps
3. Creates empty `events.jsonl`
4. Caches the `fileSession` in memory

The `Meta` struct is read/written via `writeMetaLocked()` and `loadMeta()`.

## Design

### Goal

Allow `/plan` sessions to be resumed after interruption, with:
- Same conversation context (same session ID)
- Same spec directory (specs/{taskName}/)
- Same PDD SOP loaded

### Approach: Session Metadata Extension

Add `PlanContext` to `session.Meta`. When `startPlanSession` is called, it saves the plan context to the session metadata. On resume, the handler reads the context and continues in the same session.

### New Types

```go
// PlanContext holds the /plan session context for resume.
// Persisted in session meta.json.
type PlanContext struct {
    TaskName  string `json:"taskName,omitempty"`  // e.g. "tools/001-add-logging"
    RoughIdea string `json:"roughIdea,omitempty"` // original rough idea text
    SpecDir   string `json:"specDir,omitempty"`   // absolute path to spec dir
    Phase     string `json:"phase,omitempty"`     // "plan" (mode identifier)
}
```

Added to `Meta` struct:

```go
type Meta struct {
    ID          string    `json:"id"`
    AppName     string    `json:"appName"`
    UserID      string    `json:"userID"`
    WorkDir     string    `json:"workDir,omitempty"`
    Model       string    `json:"model,omitempty"`
    CreatedAt   time.Time `json:"createdAt"`
    UpdatedAt   time.Time `json:"updatedAt"`
    PlanContext *PlanContext `json:"planContext,omitempty"` // NEW
}
```

### New Methods

```go
// UpdatePlanContext updates the plan session context in session metadata.
// Pass nil to clear the context. Non-fatal — returns error but doesn't stop the session.
func (s *FileService) UpdatePlanContext(sessionID string, ctx *PlanContext) error

// GetPlanContext retrieves the plan context from session metadata.
// Returns nil if no context is set or session not found.
func (s *FileService) GetPlanContext(sessionID string) (*PlanContext, error)
```

### Changes to `startPlanSession`

After the new session is created (line 315-324 in plan.go), persist the context:

```go
// Persist plan context for resume.
if fs, ok := m.cfg.SessionService.(*session.FileService); ok {
    _ = fs.UpdatePlanContext(m.cfg.SessionID, &session.PlanContext{
        TaskName:  taskName,
        RoughIdea: roughIdea,
        SpecDir:   specDir,
        Phase:     "plan",
    })
}
```

Errors are non-fatal — if metadata update fails, the session continues normally.

### New Slash Command: `/plan resume`

**Handler:** `handlePlanResumeCommand()`

**Logic:**
1. Get plan context from session metadata via `fs.GetPlanContext(m.cfg.SessionID)`
2. If no context found, show error: "No active plan session to resume. Use `/plan <idea>` to start a new one."
3. Validate spec directory still exists (`os.Stat`)
4. If not, show error: "Spec directory `specDir` no longer exists. Start a new plan with `/plan <idea>`."
5. Reload PDD SOP: `sop.LoadPDD(m.cfg.WorkDir)`
6. Construct augmented instruction (same as `startPlanSession`)
7. **Key:** Do NOT create a new session — use `m.cfg.SessionID` (the current session continues)
8. Rebuild agent: `agent.RebuildWithInstruction(instruction)`
9. Clear TUI conversation: `m.chatModel.Messages = m.chatModel.Messages[:0]`
10. Show confirmation message
11. Set `m.mode = "plan"`, `m.running = true`
12. Start agent loop with a resume prompt: "Resume plan session for {taskName}. Continue from where you left off."

**Registration:**
- `slashCommands` in `tui.go`: add `"/plan resume"`
- `handleSlashCommand` in `commands.go`: case `"/plan resume":`
- `/help` output: `| \`/plan resume\` | Resume an interrupted plan session |`

### Behavior Summary

| Scenario | Behavior |
|----------|----------|
| User starts `/plan`, interrupts Ctrl+C, types `/plan resume` | Same session resumes, SOP loaded, conversation continues |
| User starts `/plan`, exits TUI, restarts with `--session`, types `/plan resume` | Cross-session resume: same context, SOP loaded |
| User starts `/plan`, exits TUI, restarts without `--session`, types `/plan resume` | Error: "No active plan session" (different session, no context) |
| User starts `/plan`, spec dir deleted externally, types `/plan resume` | Error: "Spec directory no longer exists" |
| User types `/plan resume` without ever starting a plan | Error: "No active plan session" |

## File Map

```
internal/
├── session/
│   └── store.go          # + PlanContext type, UpdatePlanContext, GetPlanContext
├── session/
│   └── store_test.go     # + tests for UpdatePlanContext, GetPlanContext
└── tui/
    ├── plan.go           # + handlePlanResumeCommand, context persistence in startPlanSession
    ├── plan_resume_test.go # + unit tests for resume logic
    ├── commands.go       # + "/plan resume" case in handleSlashCommand
    ├── input.go         # + "/plan resume" to slashCommands
    └── tui.go           # + "/plan resume" registered in slashCommands
```

## Alternatives Considered

### Alternative 1: Persist in separate JSON file

Could store plan context in `specs/{taskName}/.plan-context.json`. Simpler session code, but:
- Tied to spec directory (not session)
- Spec directory might not exist if skeleton was never created
- Duplicate storage of context that should be in session

**Rejected:** Session metadata is the right place — it ties context to the conversation.

### Alternative 2: Save context on every interrupt

Could save context in `handleInterrupt()` on every Ctrl+C. This would capture the latest state but:
- Requires modifying the agent loop architecture to add "before interrupt" hooks
- Complex — the context at start is sufficient for resume
- The session conversation (events.jsonl) already captures what happened

**Rejected:** Saving at session start is simpler and sufficient.

### Alternative 3: Auto-resume on restart

Could auto-detect interrupted plan sessions and offer to resume them. This would require:
- Scanning all sessions for `PlanContext != nil`
- Showing a prompt on TUI startup if interrupted plan session found
- Complex UX decision

**Rejected:** Explicit `/plan resume` is clearer. User opts in to resume.

## Risks & Mitigations

| Risk | Mitigation |
|------|-----------|
| Metadata bloat (PlanContext grows large) | Keep it minimal — just strings. JSON overhead is negligible. |
| Race between metadata update and event append | `writeMetaLocked()` uses same locking as existing meta updates. |
| Spec dir deleted between interrupt and resume | Validate existence before resuming, show clear error. |
| Session corrupted / meta.json unreadable | `GetPlanContext` returns nil on error, user sees "No active plan session" |
