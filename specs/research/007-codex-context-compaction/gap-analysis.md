# Gap Analysis — Codex Context Compaction

**Reference:** `research/codex-app-server-compaction.md`, `design.md`,
`brief.md`.

## Executive table

| Capability | Upstream Codex (reference) | pi-go v1 (proposal) | pi-go today | Gap |
|------------|----------------------------|---------------------|-------------|-----|
| Manual compaction entry point | `thread/compact/start` (returns `{}`) | Add `MethodThreadCompactStart`, `Client.ThreadCompactStart()` | Absent | **Closed by v1** |
| Compaction item type | `item.type = "contextCompaction"` (`app-server-protocol/src/protocol/common.rs:590-595`) | Add `ItemContextCompaction`, `CompactionItem`, `EventTypeCompaction` | Absent | **Closed by v1** |
| Auto-compaction events surface | Server emits item lifecycle on auto-trigger (`core/src/session/turn.rs:980-1010`, `:418-457`, `:1049-1109`) | Pass-through via `pumpCodexSession:147-148` default branch | Lost (no handler) | **Closed by v1** |
| Cross-turn codex thread lifetime | N/A (server-side) | Out of scope | One-shot per subagent call (`spawner_codex.go:102-122`) | **Remains open (ADR #1)** |
| Remote `/v1/responses/compact` | `tmp/codex/codex-rs/core/src/compact_remote.rs` | Out of scope v1 | Absent | **Remains open (ADR #2)** |
| `RemoteCompactionV2` | `compact_remote_v2.rs`, parity at `compact_remote_parity.rs:529` | Out of scope v1 | Absent | **Deferred** |
| `Feature::TokenBudget` mode | `compact_token_budget.rs:26-93` | Out of scope v1 | Absent | **Deferred** |
| `preCompact` / `postCompact` hooks | `config_processor.rs:463-480` | Out of scope v1 | Absent | **Deferred** |
| Model-fallback retry on compaction errors | `compact_model_fallback.rs:9-20` | Out of scope v1 | Absent | **Deferred** |
| Deprecated `compacted` event | `app-server/README.md:1530` | Do not model | N/A | N/A |

## Current state (pi-go today)

- `internal/codex/protocol.go:13-21` enumerates six JSON-RPC methods
  (`initialize`, `initialized`, `thread/start`, `turn/start`,
  `review/start`, `turn/interrupt`). No `thread/compact/start`.
- `internal/codex/protocol.go:56-66` enumerates eight item types
  (`ItemAgentMessage`, `ItemReasoning`, `ItemCommandExecution`,
  `ItemFileChange`, `ItemMCPToolCall`, `ItemDynamicToolCall`,
  `ItemWebSearch`, `ItemExitedReviewMode`). No `ItemContextCompaction`.
- `internal/codex/client.go:213-272` defines `Client.request()` and
  `Client.notify()`. No `Client.ThreadCompactStart()`.
- `internal/codex/session.go` has no `EventTypeCompaction` constant
  and no translation for `contextCompaction` items.
- `internal/subagent/spawner_codex.go:102-122` (`startCodexSession`)
  creates a fresh `codex.NewSession` per call. No thread reuse, no
  caller for any compaction method.

## v1 state (this proposal)

- New protocol constants `MethodThreadCompactStart` and
  `ItemContextCompaction` in `internal/codex/protocol.go`.
- New typed request/response structs `ThreadCompactStartParams`,
  `ThreadCompactStartResponse`, and parallel `CompactionItem`.
- New `Client.ThreadCompactStart(ctx, threadID)` method in
  `internal/codex/client.go`.
- New `EventTypeCompaction` constant and item-translation case in
  `internal/codex/session.go`.
- Spawner pass-through via the existing `default` branch at
  `spawner_codex.go:147-148` (no code change).

## Future state

- **ADR #1 — codex thread lifetime.** A separate spec must define how
  pi-go's spawner architecture reuses threads across turns so that
  `Client.ThreadCompactStart()` has a caller. Until that lands, v1's
  plumbing is exercised only by unit tests.
- **ADR #2 — remote compaction auth.** A separate spec must define
  how `internal/codex/` negotiates OAuth scopes for
  `/v1/responses/compact`, if we ever want server-side summarization
  from auto-compaction. `internal/codex/` does not import
  `internal/auth/` today (the codex app-server authenticates to OpenAI
  via the user's `CODEX_HOME` directly). `internal/auth/`
  (`auth.go:118-141`) handles ChatGPT OAuth for pi-go's own `codex`
  provider used by the main agent — it does not mediate codex
  app-server-internal endpoints.

## Prior art in pi-go

- `internal/tools/compactor.go` (and its `compactor_*.go` siblings) is the
  tool-output compactor — a *different* concern that trims tool output
  to reclaim token budget without changing the conversation thread. No
  overlap with context compaction at the protocol level. It used to be
  described in the `TOO/006-…-headroom-update` spec, which was removed
  when headroom was descoped; read the code instead.
