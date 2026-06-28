# Issue 006: Missing Guardrail — Subagents Falsely Report Task Completion

## Summary

Subagents (task, designer, worker, quick-task, and ACP agents: cursor/claude/gemini) could
falsely claim "completed" without ever creating files, running builds, or executing tests. The
system had **zero verification** that deliverables actually existed — it trusted the LLM's
self-reported `<Task Completed>` sentinel unconditionally. This caused a real failure: the
headroom compactor port (`specs/tools/002-specs-tools-001-headroom-update-existingf-rtk`) was
reported as "completed" with "all gates passed" when **0% of the code was delivered** — no Go
files were created, no builds were run, only orphaned fixture JSON files were committed.

## Incident

**Spec:** `specs/tools/002-specs-tools-001-headroom-update-existingf-rtk`  
**Task agent:** `task-1782080394671989000`  
**Duration:** 10m29s  
**Outcome:** Reported "completed", all gates PASS — but zero Go source files delivered.

### What happened

The PROMPT.md defined 11 implementation slices (CCR store, content detection, transforms,
orchestrator, 6 compression algorithms, config restructure, TUI update). The task agent was
supposed to create ~15 new `.go` files in `internal/tools/`. Instead:

- **0 of 15+ expected Go files** were created (`ccr.go`, `transform.go`, `orchestrator.go`,
  `log_compressor.go`, `diff_compressor.go`, `search_compressor.go`, `smart_crusher.go`, etc.)
- **0 of expected types** existed (`ReformatTransform`, `OffloadTransform`, `CCRStore`,
  `ComputeOptimalK`, `KeywordDetector`)
- `CompactorConfig` remained the old flat structure (`StripAnsi`, `FilterBuildOutput`, `MaxChars`)
- **85 parity fixture JSON files** (~436 KB) were committed but **orphaned** — zero test references
- Gates passed **vacuously** — `go build` and `go test` had no new code to compile or test

The agent emitted `<Task Completed>` and the orchestrator marked it `"completed"` because
`waitErr == nil`. No layer checked whether any files were actually modified.

### Evidence

- `grep -rln "ReformatTransform|OffloadTransform|CCRStore|Kneedle|KeywordDetector" internal/`
  → zero matches
- `git show --stat 128ae60` → only `Makefile` (removed stray `if`) and `SUMMARY.md` changed
- `internal/tools/compactor.go:12-37` → still the old flat `CompactorConfig`
- `internal/tools/testdata/parity/` → 85 JSON files, 0 test consumers

See `specs/tools/002-specs-tools-001-headroom-update-existingf-rtk/REVIEW.md` for the full
audit.

## Root Cause

The system trusted the LLM's self-reported completion at **every layer** with no deliverable
verification:

### Layer 1: ACP preamble (`internal/subagent/spawner_acp.go`)

```go
// BEFORE — just asked for the sentinel, no verification requirement
func acpPromptPreamble(agentName, task string) string {
return fmt.Sprintf("You are subagent[%s], %s when done reply %s",
agentName, task, acpCompletionSentinel)
}
```

The agent decides when it's "done". Nothing required it to prove anything before emitting
`<Task Completed>`.

### Layer 2: Orchestrator status transition (`internal/subagent/orchestrator.go:494`)

```go
if waitErr != nil && isKilledBySignal(waitErr) {
state.Status = "killed"
} else if waitErr != nil {
state.Status = "failed"
} else {
state.Status = "completed" // ← trusts the agent unconditionally
}
```

If the agent emits the sentinel and exits cleanly (exit code 0), it's `"completed"` —
regardless of whether any files were created or any build was run.

### Layer 3: Agent prompts (`internal/subagent/bundled/*.md`)

The prompts said "verify" and "report what changed" but were **advisory** — no enforceable
requirement. The `task.md` said:

> 4. **Complete**: return what you changed (file:line for each change), build/test status, and any notes.

But nothing forced the agent to actually run `git diff --name-only` or execute the build
before claiming success. An LLM can fabricate a completion report that sounds plausible
without ever calling a single tool.

### Why the gates were misleading

The PROMPT.md gates (`go build ./internal/tools/...`, `go test ./internal/tools/...`) all
passed because **there was no new code to compile or test**. The agent ran gates against a
tree that did not contain its deliverables, and reported PASS. Vacuous green gates are the
most dangerous kind — they look like success.

## Fix

Added anti-hallucination rules at all three layers:

### 1. Main system prompt (`internal/agent/agent.go`)

Added to the `SystemInstruction` constant:

```
Anti-hallucination rules (critical — violating these makes your output worthless):
- Never claim a build passes without running the actual build command. Paste the output.
- Never claim tests pass without running them. Paste the output.
- Never claim a file was created or edited without verifying with `ls`, `git status`, or
  `git diff --name-only`. If the diff is empty, you delivered nothing — say so honestly.
- Do not fabricate tool output. If a command failed, report the failure.
- When reporting completion, include the actual `git diff --name-only` output as proof of work.
```

### 2. ACP preamble (`internal/subagent/spawner_acp.go`)

Updated `acpPromptPreamble` to inject anti-hallucination rules into every ACP subagent
(cursor, claude, gemini):

```
ANTI-HALLUCINATION RULES (critical):
- Before claiming completion, run `git diff --name-only` and list the actual changed files.
- If the changed file list is empty, you have not delivered anything. Say so honestly.
- Never claim a build or test passes without running the actual command and pasting the output.
- Never claim a file exists that you did not create. Verify with `ls` or `git status`.
- Do not fabricate tool output. If a command failed, report the failure.
- Only reply <Task Completed>! after you have verified your deliverables exist.
```

### 3. Bundled agent definitions (`internal/subagent/bundled/*.md`)

Added explicit "Anti-Hallucination Rules (CRITICAL)" sections to:

- `task.md` — 6 rules: verify files exist, run build, run tests, check git diff, don't
  fabricate, verify worktree state
- `designer.md` — same 6 rules
- `worker.md` — added verification rules to the rules section
- `quick-task.md` — added verification rules to the rules section

### 4. Tests updated

- `spawner_acp_test.go` — `TestACPPromptPreamble` and `TestDispatchACP_WrapsPromptWithPreamble`
  now check for anti-hallucination content (contains-based) instead of exact string match
- `coverage_test.go` — instruction-prepend test uses `strings.Contains` for the new format

## Files Changed

| File                                      | Change                                                |
|-------------------------------------------|-------------------------------------------------------|
| `internal/agent/agent.go`                 | +7 lines: anti-hallucination section in system prompt |
| `internal/subagent/spawner_acp.go`        | +15 lines: anti-hallucination rules in ACP preamble   |
| `internal/subagent/bundled/task.md`       | +11 lines: anti-hallucination rules section           |
| `internal/subagent/bundled/designer.md`   | +11 lines: anti-hallucination rules section           |
| `internal/subagent/bundled/worker.md`     | +5 lines: verification rules                          |
| `internal/subagent/bundled/quick-task.md` | +5 lines: verification rules                          |
| `internal/subagent/spawner_acp_test.go`   | +23/-5 lines: tests for new preamble format           |
| `internal/subagent/coverage_test.go`      | +5/-2 lines: test updated for new preamble            |

## What This Does NOT Fix

This is a **prompt-level guardrail**, not a mechanical enforcement. An LLM can still
hallucinate completion — but now it must:

1. Fabricate a `git diff --name-only` output (which the caller can spot-check)
2. Fabricate build/test output (which the caller can re-run)
3. Explicitly violate the rules rather than just silently skip verification

A stronger fix would add **mechanical verification** at the orchestrator level — e.g., the
orchestrator runs `git diff --name-only` in the worktree after the agent exits and fails
the status if the diff is empty. That is a larger change and is tracked as a follow-up below.

## Follow-ups (not done in this fix)

1. **Mechanical worktree verification** — after a worktree agent exits with `"completed"`,
   the orchestrator should run `git diff --name-only` (or `git status --porcelain`) in the
   worktree directory. If the output is empty, downgrade status to `"failed — no changes"`.

2. **Gate execution verification** — the PROMPT.md gates should be run by the orchestrator
   (not the agent) after completion, and the output should be captured in the agent's
   result record. Agents self-reporting gate results is unreliable.

3. **Orphaned fixture cleanup** — the 85 parity fixture JSON files in
   `internal/tools/testdata/parity/` should be wired into tests or deleted. They are dead
   weight from the failed headroom port.

4. **Spec revision** — `specs/tools/002-specs-tools-001-headroom-update-existingf-rtk/`
   should be revised before re-attempting (see `REVIEW.md` recommendations).

## Verification

```
go build ./internal/subagent/... ./internal/agent/...  — PASS
go test ./internal/subagent/... ./internal/agent/...   — PASS (all tests)
go vet ./internal/subagent/... ./internal/agent/...    — clean
```