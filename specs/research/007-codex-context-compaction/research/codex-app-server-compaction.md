# Codex App-Server Context Compaction — Upstream Reference

**Scope:** Reference map of the four compaction execution paths in upstream
`tmp/codex/codex-rs/`. Used by `design.md`, `plan.md`, and `gap-analysis.md`
in this spec.

## Why this matters

pi-go bundles two codex-backed subagents (`codex`, `codex-review` per
`internal/subagent/agents.go`). Both can run long turns that hit the
model's context window limit. The only recourse today is "let it fail
and retry." Upstream Codex has solved this end-to-end — manual,
automatic on limit, automatic on comp-hash drift, automatic on model
downshift, and a `Feature::TokenBudget` no-summarize mode. This spec
exposes the protocol plumbing; the orchestrator-level use comes in a
follow-up ADR.

## The four execution paths

### 1. Manual — `thread/compact/start`

- Declaration: `tmp/codex/codex-rs/app-server-protocol/src/protocol/common.rs:590-595`.
- Handler: `tmp/codex/codex-rs/app-server/src/request_processors/thread_processor.rs:670`.
- User-facing example: `tmp/codex/codex-rs/app-server/README.md:764-778`.

Client calls `thread/compact/start` with `{ threadId }`. Server replies
`{}` immediately. Progress streams as `item/started` then `item/completed`
with `item.type = "contextCompaction"`. The operator is responsible for
ensuring the conversation is in a sane state — this is a manual trigger,
not transactional.

### 2. Auto (pre-turn) — token limit reached before sampling

- Source: `tmp/codex/codex-rs/core/src/session/turn.rs:980-1010` —
  `run_pre_sampling_compact`.
- Fires when `token_status.token_limit_reached` is true before sampling
  begins a turn.
- Tagged `CompactionReason::ContextLimit`, `CompactionPhase::PreTurn`.

### 3. Auto (mid-turn) — needs follow-up after token rollover

- Source: `tmp/codex/codex-rs/core/src/session/turn.rs:418-457`.
- Decision: `let should_roll_over = needs_follow_up &&
  (sess.take_new_context_window_request().await || token_limit_reached);`.
- Tagged `CompactionPhase::MidTurn`.
- Summary is injected via `InitialContextInjection::BeforeLastUserMessage`
  so the model sees the summary as the last item in its history.

### 4. Auto — comp-hash changed / model downshift

- Source: `tmp/codex/codex-rs/core/src/session/turn.rs:1049-1109`.
- Runs against the *previous* model when its `comp_hash` differs from
  the active model, or when the new model has a smaller context
  window.
- Tagged `CompactionReason::CompHashChanged` or
  `CompactionReason::ModelDownshift`.

All four paths emit the same `item.type = "contextCompaction"` lifecycle.

## The token-budget mode

- Source: `tmp/codex/codex-rs/core/src/compact_token_budget.rs:26-93`.
- Skips model and server summarization. Installs a fresh context
  window directly. Same `ContextCompaction` item type, same
  `preCompact` / `postCompact` hooks, no `/v1/responses/compact`
  call.
- **Out of scope for v1** — gated by `Feature::TokenBudget`,
  requires model-side config that pi-go's `internal/codex/` does not
  negotiate.

## Remote compaction (two flavors)

Two server-side summarization paths:

- **`responses_compact`** — `tmp/codex/codex-rs/core/src/compact_remote.rs:1-466`.
  Calls `/v1/responses/compact`; server returns encrypted summary as
  `ResponseItem::Compaction { encrypted_content }`.
- **`RemoteCompactionV2`** — `tmp/codex/codex-rs/core/src/compact_remote_v2.rs:1-878`.
  Gated by `Feature::RemoteCompactionV2`. Parity tests at
  `tmp/codex/codex-rs/core/src/compact_remote_parity.rs:529`.

**Fallback chain.** Server picks `responses_compact` only when
`supports_remote_compaction()` returns true. If the previous-model
compaction fails with one of `ContextWindowExceeded`,
`UsageLimitReached`, `ServerOverloaded`, `InternalServerError`,
`RetryLimit`, `InvalidRequest`, `UnexpectedStatus` (per
`tmp/codex/codex-rs/core/src/compact_model_fallback.rs:9-20`), retries
against the current model.

**Out of scope for v1** — both paths require OAuth/account scope
negotiation against the codex app-server's own outbound calls to
OpenAI, which `internal/codex/` does not mediate. `internal/codex/`
does not import `internal/auth/`; the codex app-server authenticates
to OpenAI via the user's `CODEX_HOME` directly. `internal/auth/`
handles ChatGPT OAuth for pi-go's own `codex` provider used by the
main agent (`auth.go:118-141`, `CodexOAuth: true`), but it does not
serve the codex app-server's internal endpoints.

## Window accounting

- Source: `tmp/codex/codex-rs/core/src/state/auto_compact_window.rs:1-237`.
- `AutoCompactWindow` tracks `window_number`, `window_id` (UUID v7),
  `prefill_input_tokens` (either `ServerObserved` from server usage or
  `Estimated` from local approximation).
- One-shot claimers:
  - `claim_token_budget_reminder()` — `auto_compact_window.rs:87-89`.
  - `claim_auto_compact_fallback()` — `auto_compact_window.rs:91-93`.
  - `request_new_context_window()` + `take_new_context_window_request()`
    — `auto_compact_window.rs:95-103`.
- `advance()` at `auto_compact_window.rs:77-85` rotates the window on
  each successful compaction.
- `body_after_prefix` scope — `session/context_window.rs:37-50` —
  subtracts this baseline from later active-context usage so the
  *body* (not the prefix) is what trips the limit.

## Token-status decision logic

- Source: `tmp/codex/codex-rs/core/src/session/context_window.rs:1-92`.
- `token_limit_reached := buffered_auto_compact_limit reached
  || full_context_window_limit_reached`. `buffered_auto_compact_limit`
  is the configured limit plus the fallback buffer.
- `AutoCompactTokenLimitScope::{Total, BodyAfterPrefix}` (config_types)
  selects what to count against the auto-compact limit.

## Hooks

- Matcher groups:
  `tmp/codex/codex-rs/app-server/src/request_processors/config_processor.rs:463-480`.
  `preCompact` fires before each compaction; `postCompact` fires after.
- Hooks can `Stop` to abort — see `core/src/compact.rs:73-77` and
  `core/src/compact_token_budget.rs:73-77`. Abort returns
  `CodexErr::TurnAborted`.
- Forward-looking: pi-go's existing extension system
  (`internal/extension/`) could surface these as `preCompact` /
  `postCompact` hook events. Out of scope for v1.

## Deprecated alias

- `tmp/codex/codex-rs/app-server/README.md:1530` — `compacted` event is
  deprecated, use `contextCompaction` item type.
- **pi-go should not model the alias.** If older codex versions still
  emit it, it falls through the spawner's `default` branch and is
  logged.

## App-server protocol types

- Source:
  `tmp/codex/codex-rs/app-server-protocol/src/protocol/v2/thread.rs:991-998`.

  ~~~rust
  pub struct ThreadCompactStartParams {
      pub thread_id: ThreadId,
  }

  pub struct ThreadCompactStartResponse {}
  ~~~

- Generated JSON schema:
  `tmp/codex/codex-rs/app-server-protocol/schema/json/v2/ThreadCompactStartParams.json`.
