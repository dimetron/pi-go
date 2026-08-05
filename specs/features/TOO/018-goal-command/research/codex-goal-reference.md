# Research — Codex `/goal` Reference

Codex (OpenAI's CLI coding agent) ships a fully realized `/goal` concept. The
repository keeps a snapshot at `tmp/codex/codex-rs/`. This file compresses the
shape so the implementer does not need to read codex source.

## 1. Slash command definition

`tmp/codex/codex-rs/tui/src/slash_command.rs:122`:

```
SlashCommand::Goal => "set or view the goal for a long-running task",
```

The same file lists `Goal` immediately after `Plan` in the enum.

`supports_inline_args` returns true for `Goal` — `/goal foo bar baz` parses
the trailing text as a possible objective. Sub-control commands take
precedence over the positional objective argument.

`available_during_task` returns true for `Goal` — usable while an agent turn
runs.

## 2. Usage string

`tmp/codex/codex-rs/tui/src/goal_display.rs:7`:

```rust
pub(crate) const GOAL_USAGE: &str =
    "Usage: /goal [<objective>|clear|edit|pause|resume]";
```

Bare `/goal` (no args) when no goal is set → shows this string with hint
`"No goal is currently set."`. When a goal exists → opens a summary card
(see §5).

## 3. Sub-command dispatch

From `tmp/codex/codex-rs/tui/src/app/thread_goal_actions.rs`:

| Sub-command    | Side effect                                                 |
|----------------|-------------------------------------------------------------|
| `/goal <text>` | Set objective (create or replace; confirm prompt if active) |
| `/goal edit`   | Open editor over the current objective                      |
| `/goal pause`  | Status → Paused (system prompt hides the objective)         |
| `/goal resume` | Status → Active                                             |
| `/goal clear`  | Drop the goal entry (no confirmation)                       |

When `/goal <text>` is invoked with `mode == ConfirmIfExists` and an existing
goal status is `Paused | Blocked | Complete`, the UI shows a confirmation
prompt before replacing ("Replace the current goal with the new one?").

## 4. Status enum

`tmp/codex/codex-rs/protocol/src/protocol.rs:4067`:

```rust
pub enum ThreadGoalStatus {
    Active,
    Paused,
    Blocked,
    UsageLimited,    // pi-go v1: drop
    BudgetLimited,   // pi-go v1: drop (folded with token budget)
    Complete,
}
```

pi-go v1 keeps: `Active | Paused | Blocked | Complete`.

`Blocked` is set by codex when the agent decides the goal is unattainable in
its current form (e.g. external dep missing). pi-go does not auto-derive this;
the user marks it manually.

## 5. Summary card render (bare `/goal` when goal exists)

```
Goal
Status: active
Objective: Keep improving the bare goal command until it feels calm and useful.
Time used: 1m
Tokens used: 12.5K
Token budget: 80K

Commands: /goal edit, /goal pause, /goal clear
```

Snapshot source:
`tmp/codex/codex-rs/tui/src/chatwidget/snapshots/codex_tui__chatwidget__tests__goal_menu_active.snap`.

## 6. Footer status indicator

`tmp/codex/codex-rs/tui/src/chatwidget/snapshots/codex_tui__chatwidget__tests__status_line_goal_active_token_budget_footer.snap`:

```
gpt-5.4                                            Pursuing goal (40K / 50K)
```

pi-go v1 simplifies to: when `status == Active`, the footer shows
`Goal: <truncated objective>` (32 chars, ellipsis). When `Paused` shows
`Goal paused`. When `Blocked` shows `Goal blocked`. When `Complete` shows
`Goal complete` (dim). When no goal set → no indicator.

## 7. Time and token display

`format_goal_elapsed_seconds` (same file, lines 9-33):

```
0s            < 60s
59s           < 1 minute
1m            exact
30m
1h 30m
2h
1d 0h 0m      exactly 24h
2d 23h 42m
```

Compact token format from `format_tokens_compact`:
`63.9K / 50K` style.

## 8. Validation

`validate_thread_goal_objective` (`protocol.rs:4083`):

- Must not be empty.
- Must not exceed `MAX_THREAD_GOAL_OBJECTIVE_CHARS` = 4 000.

## 9. SQL storage (excluded from pi-go port)

codex persists in `thread_goals` (`state/migrations/0029_thread_goals.sql`) with
columns:

- `goal_id TEXT PRIMARY KEY`
- `thread_id TEXT NOT NULL`
- `objective TEXT NOT NULL`
- `status TEXT NOT NULL`
- `token_budget INTEGER` (nullable)
- `tokens_used INTEGER NOT NULL DEFAULT 0`
- `time_used_seconds INTEGER NOT NULL DEFAULT 0`
- `created_at INTEGER NOT NULL`
- `updated_at INTEGER NOT NULL`

Indexed on `thread_id` and `updated_at`.

pi-go's storage is JSONL via `internal/session/store.go`. A `*GoalContext` in
`Meta` parallels `*PlanContext` (same file:27-33).

## 10. After-resume hook

`maybe_prompt_resume_paused_goal_after_resume` (`thread_goal_actions.rs:64-93`):
after a session is resumed from disk, if the goal status is
`Paused | Blocked | UsageLimited`, the TUI prompts the user to resume it.
pi-go equivalent: when `--continue` or `--session` loads a session with a
paused goal, a one-line status notice appears (no modal) suggesting
`/goal resume`.

## 11. Files in codex that the pi-go port does not need to mirror

- `goal_files.rs` — codex materializes objectives to a temp dir, lets the agent
  read them via a filesystem entry. pi-go passes the objective through the
  system prompt directly.
- `ThreadGoalSetMode` — codex is a stateful app server protocol; pi-go is a
  CLI, so the mode/replace distinction collapses into a simpler TUI-side
  prompt.
- The `guardian_goal_continuation_drops_stale_reviews` snapshot is codex's
  auto-review machinery — irrelevant in pi-go.
