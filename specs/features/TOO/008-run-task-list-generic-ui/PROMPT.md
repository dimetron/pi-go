# Generic Task List UI via ACP Plan Protocol

## Objective

Implement a `/run` slash command in pi-acp that loads a PLAN.md task list alongside a PROMPT.md, surfaces it as an ACP
`plan` UI widget, and tracks progress via HTML comment markers emitted by the LLM in its streamed output. The mechanism
is generic and reusable for any checklist-driven workflow.

## Key Requirements

1. **Plan parser** — Parse markdown checklists (`- [ ]`, `- [x]`) with priority prefixes (`!!` high, `~` low) into ACP
   `PlanEntry[]`
2. **Plan tracker** — Accumulate streamed text deltas, extract `<!-- plan:INDEX:STATUS -->` markers (handling
   cross-chunk spanning), strip markers from visible text, track entry statuses, detect all-completed
3. **`/run` command** — Built-in slash command that reads PROMPT.md + sibling PLAN.md, emits initial plan, injects
   marker instructions into prompt, delegates to model
4. **Session integration** — Marker parsing in `text_delta` event handler, emit `plan` updates on status changes, emit
   empty plan on all-completed, clear tracker on new turn/cancel
5. **Stable ACP plan** — Use `sessionUpdate: "plan"` with full `PlanEntry[]` on every update. No PlanId. "Removal" =
   `{ entries: [] }`.

## Acceptance Criteria

### /run command

- Given a valid PROMPT.md path with a sibling PLAN.md, when user sends `/run <path>`, then pi-acp emits a `plan` session
  update with all entries from PLAN.md
- Given a PLAN.md with `!!` prefix, when parsed, then the corresponding PlanEntry has `priority: "high"`
- Given a PLAN.md with `~` prefix, when parsed, then the corresponding PlanEntry has `priority: "low"`
- Given a PLAN.md with `[x]` checkbox, when parsed, then the corresponding PlanEntry has `status: "completed"`
- Given a PLAN.md path that doesn't exist, when `/run` is called, then an error message is emitted and the model is not
  invoked

### Marker parsing

- Given the LLM emits `<!-- plan:0:in_progress -->`, when pi-acp processes the delta, then entry 0's status becomes
  `in_progress` and a `plan` update is emitted
- Given the LLM emits `<!-- plan:0:completed -->`, when pi-acp processes the delta, then entry 0's status becomes
  `completed` and a `plan` update is emitted
- Given a marker in the streamed text, when pi-acp forwards the text to the ACP client, then the marker is stripped from
  the visible text
- Given a marker that spans two text deltas (split across chunks), when pi-acp processes the second delta, then the
  complete marker is detected and processed

### Plan lifecycle

- Given all plan entries have status `completed`, when pi-acp detects this, then an empty plan `{ entries: [] }` is
  emitted
- Given a new prompt is sent (not `/run`), when the turn starts, then any existing plan tracker is cleared

### Prompt injection

- Given `/run` is called with a valid PLAN.md, when the prompt is sent to pi, then marker instructions are appended
  telling the LLM to emit `<!-- plan:INDEX:STATUS -->` markers

## Implementation Slices

1. **Plan parser module** — Create `src/acp/plan-parser.ts` with `parsePlanFile()` and `resolvePlanPath()`, verify:
   `npm run build`
2. **Plan tracker module** — Create `src/acp/plan-tracker.ts` with `PlanTracker` class, `extractMarkers()`,
   `buildMarkerInstructions()`, verify: `npm run build`
3. **Unit tests for parser + tracker** — Create `test/unit/plan-parser.test.ts` and `test/unit/plan-tracker.test.ts`,
   verify: `npm test`
4. **Session integration** — Modify `src/acp/session.ts`: add `planTracker` field, `setPlanTracker()`, integrate marker
   parsing in `text_delta` branch, emit plan updates, strip markers, clear on new turn/cancel, verify:
   `npm run build && npm test`
5. **`/run` command** — Modify `src/acp/agent.ts`: add `/run` to `builtinAvailableCommands()`, handle `/run` in
   `prompt()` (read files, emit plan, inject instructions, create tracker, call model), verify:
   `npm run build && npm test`
6. **`/run` command tests** — Create `test/unit/run-command.test.ts` with end-to-end tests using fakes, verify:
   `npm test`
7. **Lint + format + typecheck** — Run `npm run lint && npm run typecheck && npm run format && npm test`, verify: all
   pass

## Gates

- **build**: `npm run build`
- **test**: `npm test`
- **typecheck**: `npm run typecheck`
- **lint**: `npm run lint`
- **format**: `npm run format`

## Reference

- Design: `specs/features/TOO/008-run-task-list-generic-ui/design.md`
- Outline: `specs/features/TOO/008-run-task-list-generic-ui/outline.md`
- Plan: `specs/features/TOO/008-run-task-list-generic-ui/plan.md`
- Requirements: `specs/features/TOO/008-run-task-list-generic-ui/requirements.md`
- Research: `specs/features/TOO/008-run-task-list-generic-ui/research/`

## Constraints

- Use stable `sessionUpdate: "plan"` type only (not unstable `plan_update`/`plan_removed`)
- `/run` is a built-in command handled in `agent.ts` — but unlike other built-ins, it DOES invoke the model via
  `session.prompt()`
- PLAN.md is read-only — pi-acp never modifies it on disk
- Markers must be stripped from visible `agent_message_chunk` text before forwarding to ACP client
- Marker parsing must handle markers split across streaming text deltas (use a text buffer)
- Priority syntax: `!!` = high, `~` = low, no prefix = medium
- PLAN.md format: markdown checklist with `- [ ]` / `- [x]` (also `* [ ]` / `* [x]`)
- Marker format: `<!-- plan:INDEX:STATUS -->` where INDEX is 0-based, STATUS is `in_progress` or `completed`
- All updates go through `this.emit()` promise chain for serialized delivery
- Do NOT commit unless explicitly asked
- Run `npm run format` after code edits
- Avoid `any` in TypeScript; prefer explicit types
- Avoid unnecessary comments