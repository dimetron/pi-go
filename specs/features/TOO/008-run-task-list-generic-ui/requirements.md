# Requirements

## Questions & Answers

### Q1: What format does the user provide the task list in?

**A:** `/run <path-to-PROMPT.md>` where alongside it is a `PLAN.md` with tasks. The underlying mechanism should be
generic enough to work with TODO.md or any list of items, not just plan mode.

### Q2: How should task progress be tracked and updated?

**A:** The LLM agent (main process) is the only one who can update UI task status. pi-acp does not orchestrate turns.

### Q3: How does the agent communicate task status changes back to pi-acp?

**A:** Structured text markers in the agent's streamed output, parsed by pi-acp from `agent_message_chunk` deltas. Works
for subprocess (pi in RPC mode).

### Q4: How should the generic (non-/run) case work?

**A:** Explicit command only — user must run `/run <path>` to load a task list. Regular prompts don't trigger the plan
UI.

### Q5: What format for PLAN.md and markers?

**A:** Markdown checklist for PLAN.md (`- [ ] Task` / `- [x] Done`). HTML comment markers (
`<!-- plan:0:in_progress -->` / `<!-- plan:0:completed -->`).

### Q6: Should markers be stripped from visible text? Priority syntax? Plan lifecycle? Update PLAN.md on disk?

**A:**

1. Yes — strip markers from visible agent text before emitting `agent_message_chunk`.
2. Priority syntax: `!!` = high, `~` = low, default = medium.
3. Plan cleared when all entries are `completed` → emit `sessionUpdate: "plan"` with `{ entries: [] }`.
4. No — pi-acp does not modify PLAN.md on disk.

## Confirmed Requirements

1. **`/run` slash command** — Built-in command handled adapter-side in pi-acp. Usage: `/run <path-to-PROMPT.md>`. The
   PROMPT.md's sibling `PLAN.md` contains the task list as markdown checkboxes.

2. **Generic task list widget** — The underlying mechanism (parse checklist file → emit ACP plan, parse markers → emit
   plan updates, strip markers, auto-remove on completion) should be reusable beyond `/run`.

3. **ACP Plan protocol** — Uses the **stable** `sessionUpdate: "plan"` type only (not unstable `plan_update`/
   `plan_removed`). Emits `plan` with full `PlanEntry[]` on initial load and on every status change. The client replaces
   the entire plan on each update. No `PlanId` needed. No `clientCapabilities.plan` check needed (stable `plan` does not
   require client advertisement). "Removal" = emit `plan` with `{ entries: [] }`.

4. **PlanEntry fields:**
    - `content` — task description from the checkbox line
    - `priority` — `!!` = high, `~` = low, default = medium
    - `status` — `pending` (unchecked `[ ]`), `completed` (checked `[x]`)

5. **Marker protocol** — LLM emits HTML comments in streamed text: `<!-- plan:INDEX:STATUS -->` where INDEX is 0-based
   entry index and STATUS is `in_progress` or `completed`. pi-acp parses these from `agent_message_chunk` deltas, emits
   `sessionUpdate: "plan"` with updated full `PlanEntry[]`, and strips the markers before forwarding text to the ACP
   client.

6. **Prompt injection** — pi-acp injects marker format instructions into the prompt sent to pi so the LLM knows to emit
   markers as it works through tasks.

7. **Plan lifecycle** — When all entries reach `completed`, pi-acp emits `sessionUpdate: "plan"` with `{ entries: [] }`
   to clear the UI.

8. **PLAN.md is read-only** — pi-acp does not modify PLAN.md on disk.

9. **No file watching** — status flows only through the marker protocol in the agent's text output.