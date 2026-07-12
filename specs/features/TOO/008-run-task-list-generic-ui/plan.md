# Implementation Plan: Generic Task List UI via ACP Plan Protocol

## Slice 1: Plan Parser Module

- [ ] Slice 1: Plan parser module
    - Create `src/acp/plan-parser.ts`
    - Implement `parsePlanFile(content: string): PlanEntry[]`:
        - Split content by lines
        - Match lines starting with `- [ ]` or `- [x]` (also support `* [ ]` / `* [x]`)
        - Extract checkbox state: `[ ]` → `status: "pending"`, `[x]` → `status: "completed"`
        - Extract priority from prefix after checkbox: `!!` → `"high"`, `~` → `"low"`, otherwise `"medium"`
        - Strip checkbox and priority prefix to get `content` (trimmed)
        - Skip empty lines, headers, and non-checklist lines
        - Return `PlanEntry[]` in order
    - Implement `resolvePlanPath(promptPath: string): string`:
        - Take the directory of `promptPath`, replace filename with `PLAN.md`
        - Use `node:path` `dirname` + `join`
    - Import `PlanEntry` type from `@agentclientprotocol/sdk`
    - Verify: `npm run build`

## Slice 2: Plan Tracker Module

- [ ] Slice 2: Plan tracker module
    - Create `src/acp/plan-tracker.ts`
    - Define `PlanMarker` type: `{ index: number; status: 'in_progress' | 'completed' }`
    - Define `ProcessDeltaResult` type: `{ cleanedText: string; markers: PlanMarker[]; allCompleted: boolean }`
    - Implement `extractMarkers(text: string): { cleanedText: string; markers: PlanMarker[] }`:
        - Regex: `/<!--\s*plan:(\d+):(in_progress|completed)\s*-->/g`
        - Extract all matches, parse index (number) and status
        - Remove markers from text, return cleaned text
    - Implement `buildMarkerInstructions(entries: PlanEntry[]): string`:
        - Build a `<plan-marker-instructions>` block listing each task with its index and priority
        - Include instructions for `<!-- plan:INDEX:in_progress -->` and `<!-- plan:INDEX:completed -->`
    - Implement `PlanTracker` class:
        - Constructor takes `entries: PlanEntry[]`, stores deep copy
        - `private buffer: string` — accumulates text for cross-chunk marker detection
        - `processDelta(delta: string): ProcessDeltaResult`:
            - Append `delta` to `buffer`
            - Run `extractMarkers(buffer)` to find complete markers
            - If markers found: update internal `entries[index].status`, update buffer to cleanedText
            - If no markers found but buffer has partial marker prefix (`<!--` without closing `-->`): hold back the
              partial text, return only the safe prefix
            - If no partial marker detected: clear buffer, return full delta as cleanedText
            - Return `{ cleanedText, markers, allCompleted: this.isAllCompleted() }`
        - `flush(): string` — return any remaining buffer content (partial markers that never completed) and clear the
          buffer. Called at turn end to avoid silently dropping text.
        - `getEntries(): PlanEntry[]` — return deep copy of current entries
        - `isAllCompleted(): boolean` — true if every entry has `status: "completed"` (and entries is non-empty)
    - Import `PlanEntry` type from `@agentclientprotocol/sdk`
    - Verify: `npm run build`

## Slice 3: Unit Tests for Parser + Tracker

- [ ] Slice 3: Unit tests for parser + tracker
    - Create `test/unit/plan-parser.test.ts`:
        - Test: parse basic checklist `- [ ] Task A\n- [ ] Task B` → 2 entries, both pending, medium priority
        - Test: parse completed item `- [x] Done task` → status "completed"
        - Test: parse high priority `- [ ] !! Critical task` → priority "high"
        - Test: parse low priority `- [ ] ~ Minor task` → priority "low"
        - Test: parse mixed priorities and statuses
        - Test: skip non-checklist lines (headers, paragraphs, blank lines)
        - Test: support `* [ ]` as alternative to `- [ ]`
        - Test: empty content → empty array
        - Test: content with no checkboxes → empty array
        - Test: `resolvePlanPath("/foo/bar/PROMPT.md")` → "/foo/bar/PLAN.md"
        - Test: `resolvePlanPath("PROMPT.md")` → "PLAN.md"
    - Create `test/unit/plan-tracker.test.ts`:
        - Test: `extractMarkers` with single marker → cleaned text + 1 marker
        - Test: `extractMarkers` with no markers → original text, empty markers
        - Test: `extractMarkers` with multiple markers in one string
        - Test: `extractMarkers` with invalid index (non-numeric) → ignored
        - Test: `extractMarkers` with invalid status → ignored
        - Test: `PlanTracker.processDelta` with out-of-bounds index (e.g., `<!-- plan:99:completed -->` when only 3
          entries exist) → marker ignored, no crash, no status change
        - Test: `PlanTracker.processDelta` with marker in single delta
        - Test: `PlanTracker.processDelta` with marker split across two deltas (e.g.,
          `<!-- plan:0:` then `in_progress -->`)
        - Test: `PlanTracker.processDelta` with no marker → cleanedText equals delta
        - Test: `PlanTracker.processDelta` updates entry status to in_progress
        - Test: `PlanTracker.processDelta` updates entry status to completed
        - Test: `PlanTracker.isAllCompleted` → false when some pending
        - Test: `PlanTracker.isAllCompleted` → true when all completed
        - Test: `PlanTracker.isAllCompleted` → false when entries is empty
        - Test: `PlanTracker.getEntries` returns deep copy (mutation doesn't affect internal state)
        - Test: `PlanTracker.flush` returns remaining buffer text and clears it (partial `<!--` without `-->` held back
          by processDelta)
        - Test: `PlanTracker.flush` returns empty string when buffer is empty
        - Test: `buildMarkerInstructions` output contains task descriptions and marker format examples
        - Test: `buildMarkerInstructions` includes index numbers
    - Verify: `npm test`

## Slice 4: Session Integration

- [ ] Slice 4: Session integration
    - Modify `src/acp/session.ts`:
        - Import `PlanTracker` and `ProcessDeltaResult` from `./plan-tracker.js`
        - Add `private planTracker: PlanTracker | null = null` field to `PiAcpSession`
        - Add `setPlanTracker(tracker: PlanTracker | null): void` method:
            - Set `this.planTracker = tracker`
        - In `handlePiEvent()` → `case 'message_update'` → `text_delta` branch:
            - Before the existing `this.emit(...)` call, check if `this.planTracker` is set
            - If tracker is set: call `this.planTracker.processDelta(ame.delta)`
            - If `result.markers.length > 0`: emit `sessionUpdate: "plan"` with `this.planTracker.getEntries()`
            - If `result.allCompleted` and markers were found: emit `sessionUpdate: "plan"` with `{ entries: [] }`, call
              `this.setPlanTracker(null)`
            - Emit `agent_message_chunk` with `result.cleanedText` instead of raw `ame.delta`
            - If no tracker: emit `agent_message_chunk` with raw `ame.delta` (existing behavior)
        - In `handlePiEvent()` → `case 'agent_end'` branch:
            - If `this.planTracker` is set: call `this.planTracker.flush()` and emit any returned text as
              `agent_message_chunk` (flushes partial marker buffer so text is not silently dropped)
        - In `startTurn()`:
            - Clear plan tracker if the queued turn does not carry its own tracker (tracker is set by agent.ts before
              calling session.prompt(), and cleared on next turn)
            - Add `this.setPlanTracker(null)` at the start of `startTurn()` to reset state for queued prompts that don't
              have a plan
        - In `cancel()`:
            - Add `this.setPlanTracker(null)` to clear tracker on cancel
    - Verify: `npm run build && npm test`

## Slice 5: /run Command

- [ ] Slice 5: /run command in agent.ts
    - Modify `src/acp/agent.ts`:
        - Import `parsePlanFile`, `resolvePlanPath` from `./plan-parser.js`
        - Import `PlanTracker`, `buildMarkerInstructions` from `./plan-tracker.js`
        - Import `readFileSync`, `existsSync` from `node:fs` (already imported)
        - Import `isAbsolute`, `resolve` from `node:path` (already imported)
        - Add `/run` to `builtinAvailableCommands()`:
          ```typescript
          { name: 'run', description: 'Run a plan-driven task from a PROMPT.md with sibling PLAN.md', input: { hint: '<path-to-PROMPT.md>' } }
          ```
        - Add `if (cmd === 'run')` block in `prompt()`, after existing built-in command blocks:
            1. Get the path argument: `const promptPath = args[0]`
            2. If no path arg: emit usage error `agent_message_chunk`, return `{ stopReason: 'end_turn' }`
            3. Resolve path: if not absolute, resolve against `session.cwd`
            4. Check PROMPT.md exists; if not: emit error, return `end_turn`
            5. Read PROMPT.md content
            6. Resolve PLAN.md path via `resolvePlanPath(promptPath)`
            7. Check PLAN.md exists; if not: emit error, return `end_turn`
            8. Read PLAN.md content
            9. Parse plan: `const entries = parsePlanFile(planContent)`
            10. If entries is empty: emit warning `agent_message_chunk`, proceed without plan (still send prompt to
                model without marker instructions or tracker)
            11. If entries non-empty: emit `sessionUpdate: "plan"` with entries via `this.conn.sessionUpdate()`
            12. Build marker instructions: `const instructions = buildMarkerInstructions(entries)` (only if entries
                non-empty)
            13. Create `PlanTracker` instance (only if entries non-empty)
            14. Set tracker on session: `session.setPlanTracker(tracker)` (only if entries non-empty; otherwise ensure
                `session.setPlanTracker(null)`)
            15. Build enhanced prompt: entries non-empty ? `promptContent + "\n\n" + instructions` : `promptContent`
            16. Call `session.prompt(enhancedPrompt, images)` — this invokes the model
            17. Map result to stopReason (same as normal prompt flow)
            18. Return `{ stopReason }`
    - Verify: `npm run build && npm test`

## Slice 6: /run Command Tests

- [ ] Slice 6: /run command tests
    - Create `test/unit/run-command.test.ts`:
        - Test: `/run` with no path arg → error message, `stopReason: 'end_turn'`, model not called
        - Test: `/run /nonexistent/PROMPT.md` → error message about missing file, `stopReason: 'end_turn'`, model not
          called
        - Test: `/run <valid-PROMPT.md>` with sibling PLAN.md → `plan` session update emitted with correct entries,
          `stopReason` from model, prompt sent to model includes marker instructions
        - Test: `/run <valid-PROMPT.md>` with no PLAN.md → error message, model not called
        - Test: `/run <valid-PROMPT.md>` with empty PLAN.md (no checkboxes) → warning emitted, prompt still sent to
          model, no plan update, no tracker set
        - Test: verify `session.setPlanTracker` is called with a `PlanTracker` instance when plan is non-empty
        - Test: verify enhanced prompt contains marker instructions text
        - Test: `/run` with valid PLAN.md → simulate `message_update` text_delta with `<!-- plan:0:in_progress -->`
          marker → verify `plan` session update emitted with updated entry status, marker stripped from
          `agent_message_chunk`
        - Test: `/run` with valid PLAN.md → simulate markers completing all entries → verify `plan` session update with
          `{ entries: [] }` emitted (all-completed → empty plan)
        - Test: `/run` with valid PLAN.md → simulate `agent_end` event with partial marker in buffer → verify `flush()`
          emits remaining text as `agent_message_chunk` (no text dropped)
        - Use `FakeAgentSideConnection`, `FakePiRpcProcess`, `FakeSessions` pattern
        - May need to use `fs.writeFileSync` in a temp dir or mock `readFileSync` / `existsSync`
        - Use `node:os tmpdir` for temp test files, clean up after tests
    - Verify: `npm test`

## Slice 7: Lint + Format + Typecheck

- [ ] Slice 7: Lint + format + typecheck
    - Run `npm run lint` — fix any lint errors in new/modified files
    - Run `npm run typecheck` — fix any type errors
    - Run `npm run format` — format all files
    - Run `npm test` — confirm all tests still pass
    - Verify: `npm run lint && npm run typecheck && npm test`