# Design — `/goal` Slash Command

## Overview

A `/goal` slash command in pi-go's interactive TUI, modeled on Codex CLI's
`/goal`. Users declare a persistent per-session objective that the agent
pursues across turns, with a status lifecycle (`Active | Paused | Blocked |
Complete`) and a footer indicator. Ported shape, not a port of codex's
implementation — pi-go uses JSONL session metadata, not SQLite.

## End state

Typing `/goal keep tests green after every refactor`:

1. Chat shows `Goal set: keep tests green after every refactor`.
2. Footer reads `… gpt-5.4  Goal: keep tests green after every refactor  …`.
3. Next turn's system prompt includes a `# Current Goal` block (per Q8).
4. Goal persists in `meta.json`. Re-applied on every turn of the session.
5. `/goal pause` → footer becomes `Goal paused: …`, prompt no longer injects.
6. `/goal resume` → restores.
7. `/goal clear` → wipes the entry.
8. `--continue` rehydrates from disk; if `Paused | Blocked`, prepends a
   one-line chat hint suggesting `/goal resume`.

## Architecture

```
internal/tui/commands.go      handleSlashCommand dispatches /goal
                               -> handleGoalCommand(args)
                                  -> printGoalSummary / setGoalFromInput /
                                     beginGoalEdit / transitionGoal / clearGoal

internal/tui/goal.go          NEW — owns the goal-command state machine,
                               summary-card renderer, edit overlay, and the
                               footer status fragment.

internal/tui/status.go        StatusRenderInput.GoalIndicator field.
internal/tui/tui.go           statusRenderInput populates GoalIndicator
                               via goalIndicatorText().

internal/session/store.go     NEW: GoalContext, GoalStatus,
                               Meta.GoalContext, UpdateGoalContext,
                               GetGoalContext. (Mirror PlanContext; the
                               schemas are disjoint.)

internal/agent/agent.go       NEW: AppendActiveGoal, ValidateGoalObjective,
                               MaxGoalObjectiveChars. Sit next to
                               AppendActiveSkill (line 727).

internal/cli/interactive.go   After instruction = instructionParts.String()
                               (line 297), call AppendActiveGoal when the
                               loaded session has Status=Active.
```

### State machine

```
                  /goal <text>          /goal edit (commit)
                  /goal <text-resend>   /goal resume
     (none) ─── /goal ──► Active ──/goal pause──► Paused
                                              ▲   │
                                              │   ▼
                                   /goal resume│
                                              ▼
                                          Blocked ── /goal clear ──► (none)
                                              │
                                              ▼
                                          Complete ── /goal clear ──► (none)
```

`Paused | Blocked | Complete` are terminal "off" states for prompt-injection
(no `# Current Goal` block). `Active` is the only "on" state. `/goal
resume` moves `Paused | Blocked` → `Active`; emits `Goal already active.`
if already `Active`. `Complete` is one-way in v1.

## Session metadata schema (`internal/session/store.go`)

```go
type GoalStatus string

const (
    GoalStatusActive   GoalStatus = "active"
    GoalStatusPaused   GoalStatus = "paused"
    GoalStatusBlocked  GoalStatus = "blocked"
    GoalStatusComplete GoalStatus = "complete"
)

type GoalContext struct {
    Objective       string     `json:"objective,omitempty"`
    Status          GoalStatus `json:"status,omitempty"`
    CreatedAt       int64      `json:"createdAt,omitempty"`        // unix seconds
    UpdatedAt       int64      `json:"updatedAt,omitempty"`
    TimeUsedSeconds int64      `json:"timeUsedSeconds,omitempty"`
}

// Meta (line 47):
//   GoalContext *GoalContext `json:"goalContext,omitempty"`
```

`UpdateGoalContext(sessionID, *GoalContext)` and `GetGoalContext(sessionID)`
mirror `UpdatePlanContext` / `GetPlanContext` (store.go:620, 638) byte-for-
byte; only the field name and struct type differ.

## Agent prompt injection (`internal/agent/agent.go`)

```go
const MaxGoalObjectiveChars = 4_000

func ValidateGoalObjective(objective string) error {
    if objective == "" {
        return errors.New("goal objective must not be empty")
    }
    if len(objective) > MaxGoalObjectiveChars {
        return fmt.Errorf(
            "goal objective must be at most %d characters (got %d)",
            MaxGoalObjectiveChars, len(objective),
        )
    }
    return nil
}

func AppendActiveGoal(prompt, objective string) string {
    return prompt + fmt.Sprintf(
        "\n\n# Current Goal\n\nThe user's active session goal is:\n\n> %s\n\n"+
            "Stay focused on this objective across turns. "+
            "If it is no longer the user's intent, surface that "+
            "and suggest /goal edit before pursuing other work.\n",
        objective,
    )
}
```

`len` is **byte length**, not rune count. Codex's purpose is to bound the
*cost* of the objective in the prompt; bytes are the right unit for a cost
bound. Rune-counting would let 4 000 Han characters consume four times the
budget.

### Injection points

1. **At session start** — `internal/cli/interactive.go:289-300`. After
   `instruction = instructionParts.String()`, if the loaded session has
   `Status=Active`, call `AppendActiveGoal`.
2. **On user-driven updates** — from `/goal edit` and
   `/goal pause → resume`. Same call:
   `m.cfg.Agent.RebuildWithInstruction(instructionWithGoal)`. The pattern
   exists at `create_skill.go:69` (skill rebuild) and `plan.go:245` (plan
   rebuild).

Both share one helper:

```go
// in internal/tui/goal.go
func (m *model) applyGoalToPrompt() {
    if m.cfg.Agent == nil || m.cfg.SessionService == nil { return }
    if m.goal == nil || m.goal.Status != session.GoalStatusActive { return }
    base := agent.LoadInstruction(agent.SystemInstruction)
    full := agent.AppendActiveGoal(base, m.goal.Objective)
    if err := m.cfg.Agent.RebuildWithInstruction(full); err != nil {
        m.chatModel.Messages = append(m.chatModel.Messages, message{
            role:    "assistant",
            content: fmt.Sprintf("Error reapplying goal to prompt: %v", err),
        })
    }
}
```

## TUI model fields

```go
// in internal/tui/types.go
type model struct {
    // ...existing fields...
    goal *session.GoalContext  // nil when no goal
    goalEditing bool           // /goal edit overlay
    goalDraft   []rune         // /goal edit buffer
}
```

A pointer so a fresh-from-disk load and a freshly-set goal share one code
path. `m.goal == nil` ⇒ no indicator, no `# Current Goal` injection.

## Slash-command surface (`internal/tui/commands.go`)

```go
// in handleSlashCommand, between /run and /login
case "/goal":
    return m.handleGoalCommand(parts[1:])

// one entry point; sub-commands route through it
func (m *model) handleGoalCommand(args []string) (tea.Model, tea.Cmd) {
    if len(args) == 0 {
        m.printGoalSummary()
        return m, nil
    }
    sub := strings.ToLower(args[0])
    switch sub {
    case "edit":       m.beginGoalEdit()
    case "pause":      m.transitionGoal(session.GoalStatusPaused, "Goal paused")
    case "resume":     m.transitionGoal(session.GoalStatusActive, "Goal resumed")
    case "clear":      m.clearGoal()
    case "":           m.printGoalSummary()
    default:
        text := strings.TrimSpace(strings.Join(args, " "))
        m.setGoalFromInput(text)
    }
    return m, nil
}
```

The `default` branch joins all of `args` (including the first non-keyword
token) as the objective — same convention as `/skills create` and
`/rename`. So `/goal keep tests green` sets the objective to
`keep tests green`.

### Summary card (`printGoalSummary`)

```go
func (m *model) printGoalSummary() {
    if m.goal == nil {
        m.chatModel.Messages = append(m.chatModel.Messages, message{
            role: "assistant",
            content: "No goal is currently set. `/goal <objective>` to set.",
        })
        return
    }
    obj := truncate(m.goal.Objective, 200)
    var line string
    switch m.goal.Status {
    case session.GoalStatusActive:
        line = fmt.Sprintf("Goal: %s   [/goal edit | /goal pause | /goal clear]", obj)
    case session.GoalStatusPaused:
        line = fmt.Sprintf("Goal paused: %s   [/goal resume | /goal edit | /goal clear]", obj)
    case session.GoalStatusBlocked:
        line = fmt.Sprintf("Goal blocked: %s   [/goal resume | /goal edit | /goal clear]", obj)
    case session.GoalStatusComplete:
        line = fmt.Sprintf("Goal complete: %s   [/goal clear]", obj)
    }
    m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: line})
}
```

`truncate(s, n)` is a tiny local helper. No need for a package.

### Set / edit / pause / resume / clear

```go
func (m *model) setGoalFromInput(text string) {
    if err := agent.ValidateGoalObjective(text); err != nil {
        m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: err.Error()})
        return
    }
    now := time.Now().Unix()
    m.goal = &session.GoalContext{Objective: text, Status: session.GoalStatusActive, CreatedAt: now, UpdatedAt: now}
    if err := m.persistGoal(); err != nil {
        m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: "Failed to persist goal: " + err.Error()})
        return
    }
    m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: "Goal set: " + text})
    m.applyGoalToPrompt()
}
```

`transitionGoal`, `clearGoal`, `beginGoalEdit`, `commitGoalEdit` are
analogous. The *edit* path uses the existing `create_skill.go` overlay
pattern — captures key events into `m.goalDraft`; Enter commits via
`setGoalFromInput`; Esc drops the overlay and clears the draft.

## Footer indicator (`internal/tui/status.go`)

```go
type StatusRenderInput struct {
    // ...existing fields...
    GoalIndicator string  // empty when no goal
}

func (m *model) goalIndicatorText() string {
    if m.goal == nil { return "" }
    obj := truncate(m.goal.Objective, 32)
    switch m.goal.Status {
    case session.GoalStatusActive:    return "Goal: " + obj
    case session.GoalStatusPaused:    return "Goal paused: " + obj
    case session.GoalStatusBlocked:   return "Goal blocked: " + obj
    case session.GoalStatusComplete:  return "Goal complete: " + obj
    }
    return ""
}
```

`status.go` renders it via a new `(s Status).renderGoal()` call inside the
existing inline-rendering loop, placed next to (left of) the token counter.
Empty input renders nothing.

## Autocomplete + help text

`slashCommands` (input.go:272) gains `"/goal"` and `slashCommandDesc`
(line 297) gains `case "/goal": return "Set or view the current session goal"`.

`formatHelp` (commands.go:794) gains a new section between `**Git &
Planning:**` and `**Display:**`:

```
**Long-running tasks:**

| Command | Description |
|---------|-------------|
| `/goal` | Set or view the session goal |
| `/goal <text>` | Set the goal |
| `/goal edit` | Edit the current goal |
| `/goal pause` | Pause pursuing the goal |
| `/goal resume` | Resume pursuing the goal |
| `/goal clear` | Clear the goal |
```

## Resume across `--continue` / `--session`

```go
// internal/cli/interactive.go:~298 — after instructionParts.String()
ctx, _ := sessionSvc.GetGoalContext(loadedSessionID)
if ctx != nil && ctx.Status == session.GoalStatusActive {
    instruction = agent.AppendActiveGoal(instruction, ctx.Objective)
}
```

For the post-resume hint, prepend one assistant message if
`m.goal != nil && m.goal.Status ∈ {paused, blocked}` after load:
`"Loaded a previously-set goal in `<status>` state. Type `/goal resume`
to keep pursuing it."` This uses the same program-prepend route used by
`/plan resume` (plan.go:200ish).

## Patterns to follow (existing codebase)

- Slash-command dispatch shape — `internal/tui/commands.go:31-104`.
- Sub-command `strings.ToLower(args[0])` — `internal/tui/plan.go`.
- `m.cfg.SessionService.UpdatePlanContext(...)` — `plan.go:282`.
- `agent.AppendActiveSkill` — `internal/agent/agent.go:727`.
- `m.cfg.Agent.RebuildWithInstruction(full)` — `create_skill.go:69`,
  `plan.go:245`.
- `m.chatModel.Messages = append(...)` for assistant messages — used by
  every existing command.

## Error handling

| Case                                                | Action                                                                                                                                                      |
|-----------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `validateGoalObjective` fails                       | Error message appended to chat. Model state unchanged.                                                                                                      |
| `UpdateGoalContext` returns error                   | Goal stays in memory; "Error persisting goal: <err>" appended. Model and prompt not rebuilt. meta.json write is the source of truth for the *next* session. |
| `RebuildWithInstruction` fails                      | Error appended to chat. Stored goal is intact but prompt does not include it. Next `/goal <text>` retries.                                                  |
| `m.cfg.SessionService == nil` (e.g. `--memory-off`) | Append "Goal tracking requires the session service." Goal is not stored across sessions; lives only in `m.goal` for this run.                               |
| `m.cfg.Agent == nil`                                | Skip `applyGoalToPrompt`; goal is persisted but no prompt injection. Footer indicator still renders.                                                        |

## Acceptance Criteria

```
Given no goal set, when "/goal keep tests green" → chat "Goal set: …",
    footer "Goal: …", next turn sees "# Current Goal", meta.json correct.

Given goal {status: paused}, when "/goal resume" → chat "Goal resumed",
    footer "Goal: …", next turn sees "# Current Goal" again.

Given no goal, when "/goal <4100-char>" → chat "…at most 4000 characters
    (got 4100)", state unchanged.

Given goal {status: active}, when "/goal clear" → chat "Goal cleared.",
    footer disappears, meta.json GoalContext nil, no "# Current Goal".

Given meta.json with paused goal, when "pi --continue" → chat hint
    "Loaded … paused state … `/goal resume`", footer "Goal paused: …",
    no "# Current Goal" in prompt.
```

## Testing strategy

| Layer        | Location                                    | Coverage                                                                                               |
|--------------|---------------------------------------------|--------------------------------------------------------------------------------------------------------|
| Schema       | `internal/session/store_test.go`            | `TestGoalContext_RoundTrip` — all four statuses + 4 000-char boundary.                                 |
| Validation   | `internal/agent/agent_test.go`              | `TestValidateGoalObjective` — empty/4001/4000 cases.                                                   |
| Injection    | `internal/agent/agent_test.go`              | `TestAppendActiveGoal` — block matches Q8 wording; empty input returns input unchanged.                |
| Dispatch     | `internal/tui/commands_test.go`             | One test per sub-command: bare, set, edit, pause, resume, clear. Mirrors `TestHandleSlashCommandPlan`. |
| Edit overlay | `internal/tui/goal_test.go`                 | Pre-fills; Enter commits; Esc cancels.                                                                 |
| Footer       | `internal/tui/tui_test.go`                  | `goalIndicatorText()` truth table over five inputs (no-goal + four statuses).                          |
| Resume       | `internal/cli/interactive_test.go` (or new) | `--continue` with paused goal injects the resume hint.                                                 |

## Out of scope (v1)

- `token_budget` / `tokens_used` — codex's accounting machinery; pi-go's
  token tracker is per-turn.
- `UsageLimited` / `BudgetLimited` statuses — pi-go has no quota concept yet.
- `--goal <text>` CLI flag, `pi-go-rpc.SetGoal` — follow-up spec.
- Composer-button shortcut — pi-go's TUI is a single-line input.
- Cross-session memory palace persistence — single-session scope.
- Multi-objective / sub-goals — one objective per session.

These match the `out of scope` block in `requirements.md`.
