# Requirements

## Questions & Answers

### Q1: What is being built?

**A:** Add "codex" as a new subagent type (like claude/gemini/cursor/copilot) that the orchestrator can spawn. It uses
the Codex app-server JSON-RPC protocol (not ACP).

### Q2: How should codex integrate since it's not ACP?

**A:** Option A — New dispatch path. Add a `dispatchCodex()` function alongside `dispatchACP()` in a new
`spawner_codex.go`. Add `"codex"` to a new `codexAgentNames` map (separate from `acpAgentNames`). The orchestrator's
`Spawn()` gets a third branch: `isACPAgent` → `isCodexAgent` → else `Spawner.Spawn`.

### Q3: What scope of the codex protocol?

**A:** Minimal subset for now: initialize/initialized, thread/start, turn/start, turn/interrupt, and notifications (
turn/started, item/started, item/completed, turn/completed, error). Add TODO comments for future extended support (
thread/resume, thread/list, externalAgentConfig/import, config/read, account/read).

### Q4: What sandbox mode should codex default to?

**A:** workspace-write (can modify files) by default, plus review/start for code reviews.

### Q5: How should review/start be exposed?

**A:** Two bundled agent definitions: `codex` (task mode, workspace-write, turn/start) and `codex-review` (review mode,
read-only, review/start). Both accessible via existing subagent spawning mechanisms — LLM can spawn
`{"agent": "codex", ...}` or `{"agent": "codex-review", ...}`. No new slash commands.

### Q6: Binary availability and env override?

**A:** Yes — include binary availability check using the existing `findBinary` pattern (check PATH + default locations).
Return a clear error if `codex` is not installed. Support a `PI_CODEX_CMD` env var override matching the
`PI_ACP_CLAUDE_CMD` / `PI_ACP_COPILOT_CMD` pattern.
