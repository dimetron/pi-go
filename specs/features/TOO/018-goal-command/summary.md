# Summary — `/goal` Slash Command Spec

## Outcome

Spec authored for `/goal` slash command — a port of Codex CLI's
`/goal` concept into pi-go's interactive TUI. All seven planning
artifacts produced; no code written. Ready for an executor agent
running `PROMPT.md`.

## Artifacts

| Phase           | File                                   | Lines | Status                        |
|-----------------|----------------------------------------|-------|-------------------------------|
| 1. Idea         | `rough-idea.md`                        | 50    | Rewritten with codex context  |
| 2. Requirements | `requirements.md`                      | 142   | 8 Q&A, full AC                |
| 3a. Research    | `research/codex-goal-reference.md`     | 117   | Compression of codex source   |
| 3b. Research    | `research/pi-go-integration-points.md` | 134   | Where it lands in pi-go       |
| 4. Design       | `design.md`                            | 393   | Architecture, schema, wire-up |
| 5. Outline      | `outline.md`                           | 79    | 8-slice index                 |
| 6. Plan         | `plan.md`                              | 294   | Per-slice files + verify      |
| 7. Prompt       | `PROMPT.md`                            | 184   | Executable briefing           |

## Resolved decisions

1. **Shape** — Full Codex port: set / view / edit / pause / resume / clear.
2. **Prompt injection** — Appended after `Base + Rules + Skills`, parallel to `# Active Skill`.
3. **Cap** — 4 000 bytes (matches codex's `MAX_THREAD_GOAL_OBJECTIVE_CHARS`).
4. **Replace on set** — Always silently overwrite any existing goal.
5. **Footer placement** — Same line as model + token usage (single-line input).
6. **Modes** — Interactive TUI only. `print` / `json` / `rpc` load the goal but expose no command.
7. **Status surface** — One-line summary card with status-aware action hint (Q7 specific text in design).
8. **Block wording** — Full `# Current Goal` block with "stay focused + suggest /goal edit" guidance.

## Non-goals (deferred)

- Token-budget tracking (`token_budget`, `tokens_used`) — out of v1.
- `UsageLimited` / `BudgetLimited` statuses — folded away in v1.
- `--goal <text>` CLI flag, `pi-go-rpc.SetGoal` — follow-up spec.
- Composer-button shortcut — N/A in single-line TUI.
- Cross-session memory palace persistence — single-session scope.
- Multi-objective / sub-goals — one objective per session.

## Risks noted in design

- The `default:` branch in `handleGoalCommand` swallows the first
  argument so `/goal keep tests green` parses as a single objective.
  Tested explicitly.
- `applyGoalToPrompt` calls `RebuildWithInstruction` which is also used
  by skill activation; ensure both call-sites coexist.
- `session.Meta.GoalContext` adds a JSON field; existing sessions
  without it must continue to work.
- `/goal clear` and a fresh load both yield `nil`; both must round-trip
  identically.

## Next action

`PROMPT.md` is the briefing for an executor agent. It enumerates the
eight slices with their verification commands and gates. Pass it to a
fresh agent along with the `design.md` reference.
