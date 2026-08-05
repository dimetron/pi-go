# Research — pi-go Integration Points for `/goal`

## Existing mechanisms to imitate (NOT invent)

### 1. Slash-command dispatch layer

`internal/tui/commands.go:31-104` — the `handleSlashCommand` switch is the only
place to register a new command. Pattern from `/plan`:

```go
case "/plan":
    return m.handlePlanCommand(parts[1:])
```

For `/goal`, every sub-command is processed in one entry point because the
arg-less variants share the "show summary" code path:

```go
case "/goal":
    return m.handleGoalCommand(parts[1:])
```

### 2. Autocomplete list and help table

`internal/tui/input.go:272-295` — `slashCommands` slice.
`internal/tui/input.go:297-351` — `slashCommandDesc` lookup table.
`internal/tui/commands.go:794-820` — `formatHelp` table (group: "**Git & Planning:**").

### 3. The plan command itself — closest existing analogue

`internal/tui/plan.go` contains the entire `handlePlanCommand` implementation
and is the file we pattern after for stateful TUI side-effects.

Key patterns from `plan.go`:

- Multi-step command (resume / arg-less / subarg) — uses `strings.ToLower(subcmd)`.
- Writing to session state via `m.cfg.SessionService.UpdatePlanContext(...)`
  (`plan.go:282`).
- Sending an info message back to chat: appending to `m.chatModel.Messages`
  with `role: "assistant"`.

### 4. Session metadata schema

`internal/session/store.go:27-33`:

```go
type PlanContext struct {
    TaskName   string `json:"taskName,omitempty"`
    RoughIdea  string `json:"roughIdea,omitempty"`
    Phase      string `json:"phase,omitempty"`
}
```

And `Meta` (lines ~50-60) embeds `*PlanContext` with the JSON tag
`"planContext,omitempty"`.

For `/goal`, mirror with:

```go
type GoalContext struct {
    Objective      string `json:"objective,omitempty"`
    Status         string `json:"status,omitempty"`            // active | paused | blocked | complete
    CreatedAt      int64  `json:"createdAt,omitempty"`         // unix seconds
    UpdatedAt      int64  `json:"updatedAt,omitempty"`
    TimeUsedSeconds int64 `json:"timeUsedSeconds,omitempty"`    // for v1 display
}
```

`UpdatePlanContext` / `GetPlanContext` (store.go:~620-650) provide the read/write
API to copy.

### 5. System-prompt injection

`internal/agent/agent.go:687-695` shows `LoadInstructionParts(base)` returns
`{Base, Rules, Skills}`. `agent.go:758-765` shows `AppendActiveSkill(prompt,
skill, body)` as the precedent for "append a context block for one turn".

For `/goal`, add a thin `AppendActiveGoal(prompt, objective string) string` next
to `AppendActiveSkill`. Call it from `interactive.go:289-300` (the runRoot /
interactive build path) after `LoadInstructionParts(agent.SystemInstruction)`
when `m.cfg.SessionService` has an active goal.

### 6. Status / footer

The status bar lives in `m.statusModel` (status.go / tui.go:1670-1700). The
goal indicator slots into the same line as the model name and the token
usage counter. The simplest extension is to give `statusRenderInput` an extra
field on the `model` struct — `goalIndicator string` — populated by a new
`buildGoalIndicator()` function called once per render tick from the
existing render-input builder.

The exact existing function to extend during design is found via:

```
grep -n "statusRenderInput\|statusModel" internal/tui/*.go
```

### 7. Tests

Pattern from `internal/tui/commands_test.go` (TestHandleSlashCommandHelp,
TestHandleSlashCommandClear, TestHandleSlashCommandModel — lines 37-220). Each
test constructs a `model`, calls `m.handleSlashCommand("/foo <args>")`, and
asserts on the resulting messages / cmds. The `/goal` tests follow this shape.

`internal/tui/teatest_test.go:1132-1187` is the secondary e2e harness
(`handleSlashCommand("/session")`, `/branch`, `/compact`, `/nonexistent`). A
matching `handleSlashCommand("/goal <variants>")` block sits in the same file.

## Things this work will touch but should NOT redesign

- `session.Meta` JSON keys: add `goalContext,omitempty`; do not change
  `planContext` or any other key.
- The agent system prompt lives at `internal/agent/agent.go:46`. Do not edit
  it as part of this spec — prompt injection goes through `AppendActiveGoal`
  in `agent.go` next to `AppendActiveSkill` (line 758), preserving the
  assembly rule "Base + Rules + Skills" set by `InstructionParts.String()`
  (line 677).
- The CLI's `runRoot` and `runPrint` paths: add wiring via the same
  `setupSessionService` already used by `/plan`, do not introduce a new
  constructor.

## Confirmed non-goals

- No new persistence layer (no SQLite). JSONL via `session.Meta` is enough.
- No new dependency in `go.mod`.
- No changes to the memory palace.
- No MCP-server integration of `/goal`.
- No changes to the `/run` task agent.

## Build / test commands discovered

Gates that the slice verification must pass at the end of every implemented
slice:

```bash
go build ./...                # compiles
go vet ./...                  # static checks
go test ./internal/tui/...    # unit + commands_test + teatest_test
go test ./internal/session/... # store round-trip
go test ./internal/agent/...  # AppendActiveGoal surfaces objective
```

Full repo test (run after plan completion, optional per-slice):

```bash
go test ./...
```

No race detector is enforced today, but the work must be safe under
`go test -race ./internal/tui/... ./internal/session/...` (no new
goroutines, no shared mutation).
