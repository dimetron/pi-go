# Expose Codex Context Compaction to pi-go

**Status:** proposed v1 — transport-only exposure
**Spec ID:** 007-codex-context-compaction

## Executive summary

pi-go's `internal/codex/` is a thin JSON-RPC client for the Codex app-server.
The upstream server supports thread-level context compaction through three
mechanisms — manual `thread/compact/start`, auto on token-limit, and
`Feature::TokenBudget` — and emits a unified `item.type = "contextCompaction"`
lifecycle. pi-go does not currently expose any of it.

v1 is **transport-only**: add `MethodThreadCompactStart`, `ItemContextCompaction`,
`Client.ThreadCompactStart()`, and `EventTypeCompaction` for session routing.
v1 does **not** make compaction *useful* end-to-end, because
`internal/subagent/spawner_codex.go:102-122` (`startCodexSession`) creates a
fresh `codex.NewSession` per subagent call — there is no thread to compact.
Thread lifetime in the spawner is a separate ADR.

## The gap

- **No protocol plumbing.** `internal/codex/protocol.go:13-21` enumerates
  methods; `internal/codex/protocol.go:56-66` enumerates item types; the
  session has no compaction routing. None of `thread/compact/start`,
  `contextCompaction`, or `EventTypeCompaction` exist.
- **No caller.** `internal/subagent/spawner_codex.go:102-122` spawns a
  fresh `codex.NewSession` per subagent invocation. The proposed method
  has no caller in pi-go's current architecture.
- **No cross-turn codex thread lifetime.** Compaction operates on thread
  lifetime; pi-go's codex path has none.

## Scope

**v1 (this spec)**

- Add `MethodThreadCompactStart` to `internal/codex/protocol.go`.
- Add `ItemContextCompaction` and parallel `CompactionItem` struct.
- Add `Client.ThreadCompactStart(ctx, threadID)` in
  `internal/codex/client.go`.
- Add `EventTypeCompaction` in `internal/codex/session.go` and translate
  `ItemContextCompaction` items to events.
- Verify spawner pass-through (no code change — `spawner_codex.go:147-148`).

**Out of scope**

- Cross-turn thread lifetime in `internal/subagent/spawner_codex.go`.
- `/v1/responses/compact` remote path (auth scope negotiation deferred).
- `Feature::TokenBudget` mode (model-side config not negotiated).
- `preCompact`/`postCompact` hook surfacing to `internal/extension/`.
- `RemoteCompactionV2` and model-fallback retry chain.
- Deprecated `compacted` event alias.

## Open question (ADR)

**ADR #1 — codex thread lifetime.** File as a separate spec. The plumbing
in v1 is forward-compatible and small (~50 LoC). The question of
*what caller* makes `Client.ThreadCompactStart` meaningful is separate
from *how to call it*.
