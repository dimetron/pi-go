# Outline: Generic Task List UI via ACP Plan Protocol

## Slices (ordered)

1. **Plan parser module** — `src/acp/plan-parser.ts` with `parsePlanFile()` and `resolvePlanPath()`. Pure parsing, no
   side effects. Verify: `npm run build && npm test`.

2. **Plan tracker module** — `src/acp/plan-tracker.ts` with `PlanTracker` class, `extractMarkers()`,
   `buildMarkerInstructions()`. Pure logic, no ACP dependencies. Verify: `npm run build && npm test`.

3. **Unit tests for parser + tracker** — `test/unit/plan-parser.test.ts` and `test/unit/plan-tracker.test.ts`. Covers
   checklist parsing, priorities, completed items, marker extraction (single + cross-chunk), marker stripping,
   all-completed detection, out-of-bounds, invalid status. Verify: `npm test`.

4. **Session integration** — Modify `src/acp/session.ts`: add `planTracker` field, `setPlanTracker()` method, integrate
   marker parsing in `text_delta` branch, emit `plan` updates, strip markers from `agent_message_chunk`, clear tracker
   on new turn/cancel. Verify: `npm run build && npm test`.

5. **`/run` command** — Modify `src/acp/agent.ts`: add `/run` to `builtinAvailableCommands()`, handle `/run` in
   `prompt()` (read PROMPT.md + PLAN.md, emit plan, inject marker instructions, create PlanTracker, call
   `session.prompt()`). Verify: `npm run build && npm test`.

6. **`/run` command tests** — `test/unit/run-command.test.ts`: valid paths, missing PLAN.md, missing PROMPT.md, empty
   plan, plan emission, prompt injection, marker processing via simulated events. Verify: `npm test`.

7. **Lint + format + typecheck** — Run full validation suite. Verify:
   `npm run lint && npm run typecheck && npm run format`.

## Key Type Signatures

```typescript
// plan-parser.ts
export function parsePlanFile(content: string): PlanEntry[]
export function resolvePlanPath(promptPath: string): string

// plan-tracker.ts
export type PlanMarker = { index: number; status: 'in_progress' | 'completed' }
export type ProcessDeltaResult = { cleanedText: string; markers: PlanMarker[]; allCompleted: boolean }

export class PlanTracker {
  constructor(entries: PlanEntry[])
  processDelta(delta: string): ProcessDeltaResult
  flush(): string  // flush partial buffer at turn end
  getEntries(): PlanEntry[]
  isAllCompleted(): boolean
}

export function extractMarkers(text: string): { cleanedText: string; markers: PlanMarker[] }
export function buildMarkerInstructions(entries: PlanEntry[]): string

// session.ts (modifications)
export class PiAcpSession {
  private planTracker: PlanTracker | null  // new field
  setPlanTracker(tracker: PlanTracker | null): void  // new method
}

// agent.ts (modifications)
// builtinAvailableCommands() gets:
//   { name: 'run', description: 'Run a plan-driven task from a PROMPT.md with sibling PLAN.md', input: { hint: '<path-to-PROMPT.md>' } }
// prompt() gets new if (cmd === 'run') { ... } block
```

## Order Rationale

- Slices 1-3 build and test the pure logic modules first (no ACP dependencies)
- Slice 4 integrates the tracker into the session event loop
- Slice 5 adds the user-facing `/run` command that wires everything together
- Slice 6 tests the full command end-to-end
- Slice 7 is final validation