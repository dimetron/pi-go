# Plan — Codex Context Compaction (v1 Plumbing)

**Scope:** tickets for `design.md`. Out-of-scope items live in their
own specs (see ADR #1, ADR #2).

## Prerequisites

- `go test ./internal/codex/...` is green before starting.
- Verified by `go test ./...` on the current tree — all 36 packages
  pass.
- Working knowledge: `internal/codex/protocol.go`,
  `internal/codex/client.go`, `internal/codex/session.go`,
  `internal/subagent/spawner_codex.go`.

## Tickets

### 1. Add protocol constants and types

**File:** `internal/codex/protocol.go`

- After line 20, in the method constant block, add:
  ~~~go
  const MethodThreadCompactStart = "thread/compact/start"
  ~~~
- Update the TODO comment at `protocol.go:23-25` to note that
  `thread/compact/start` is now implemented.
- After line 65, in the item-type constant block, add:
  ~~~go
  const ItemContextCompaction = "contextCompaction"
  ~~~
- After `ReviewStartResponse` (around `protocol.go:211`), add:
  ~~~go
  type ThreadCompactStartParams struct {
      ThreadID string `json:"threadId"`
  }
  type ThreadCompactStartResponse struct{}
  ~~~
- Add a **new** struct, parallel to `Item` at `protocol.go:248-262`,
  *not* by extending `Item`. Per architect: keeps parse-time
  discrimination clean.
  ~~~go
  type CompactionItem struct {
      ID string `json:"id"`
      // upstream has no payload today; reserved for future fields
  }
  ~~~

**Tests:** JSON encode/decode coverage exercised by ticket #2's
tests.

### 2. Add `Client.ThreadCompactStart`

**File:** `internal/codex/client.go`

- After `Client.request` at line 213-249, add:
  ~~~go
  func (c *Client) ThreadCompactStart(ctx context.Context, threadID string) error {
      _, err := c.request(ctx, MethodThreadCompactStart,
          ThreadCompactStartParams{ThreadID: threadID})
      return err
  }
  ~~~

**Tests:** extend `internal/codex/client_test.go` (422 lines today)
with a fake-server scenario that mounts `MethodThreadCompactStart` and
asserts request/response. Mirror the existing
`startFakeSession`/`newHandshakenServer` helpers at
`session_test.go:13-56` and `client_test.go:28-60`.

### 3. Translate compaction items in Session

**File:** `internal/codex/session.go`

- Add `EventTypeCompaction = "compaction"` to the event-type consts.
- In the item-type switch where `ItemAgentMessage`, `ItemReasoning`,
  etc. are translated, add:
  ~~~go
  case ItemContextCompaction:
      s.emit(Event{Type: EventTypeCompaction, SessionID: s.sessionID,
          // carry ID and any other fields from the item
      })
  ~~~

**Tests:** extend `internal/codex/session_test.go` (323 lines) with a
new `TestSession_TranslatesContextCompactionItem` mirroring
`TestSession_TranslatesItemsAndCompletes` at
`session_test.go:90-156`. Inject `item/started` and `item/completed`
notifications with `Item{Type: ItemContextCompaction, ID: "comp_1"}`
and assert the resulting event.

### 4. Spawner pass-through verification

**File:** `internal/subagent/spawner_codex_test.go` (60 lines)
**Code change:** none in `internal/subagent/spawner_codex.go`.

- Add a test case asserting that an unknown codex event type
  (`codex.EventTypeCompaction`) is forwarded as an orchestrator event
  with the matching type. Mirror `TestCodexAgentsAreNotACPAgents` at
  `spawner_codex_test.go:76-90`.
- Document in the test header that this is a regression guard for
  the `default` branch at `spawner_codex.go:147-148`.

### 5. End-to-end smoke (free — bonus)

**File:** `internal/subagent/spawner_codex_e2e_test.go:12-50`
(already runs against a real `codex app-server`)

- Assert that an injected `contextCompaction` item (via fake
  app-server in test mode) flows through as
  `subagent.Event.Type == "compaction"`.
- **Marked free** because ticket #3 already covers routing; this
  just confirms the spawner doesn't drop it.

### 6. Documentation

**File:** `internal/codex/protocol.go:1-9` (package comment)

- Extend the protocol flow description (currently lists
  `initialize → thread/start → turn/start → notifications → turn/completed`)
  to include `thread/compact/start → contextCompaction`.

## Out of scope (explicit non-tickets)

- **Cross-turn codex thread lifetime** in
  `internal/subagent/spawner_codex.go` → separate ADR/spec. Without
  it, v1's `Client.ThreadCompactStart` has no caller inside pi-go.
- **Remote `/v1/responses/compact`** path → blocked on OAuth scope
  negotiation (ADR #2). Separate spec.
- **`Feature::TokenBudget`** mode → blocked on model-side config.
  Defer.
- **`preCompact` / `postCompact`** hook surfacing to
  `internal/extension/` → separate spec, parallel to the existing
  hook system.
- **Deprecated `compacted`** event alias → do not model.

## Verification

- `go test ./internal/codex/... ./internal/subagent/...` — must pass.
- `go test ./...` — must remain green across the full 36-package
  tree.
- `go vet ./...` — clean.
- All new exported identifiers must carry GoDoc comments per project
  convention. Concretely:
  - `MethodThreadCompactStart`
  - `ItemContextCompaction`
  - `EventTypeCompaction`
  - `ThreadCompactStartParams`
  - `ThreadCompactStartResponse`
  - `CompactionItem`
  - `Client.ThreadCompactStart`

## Risks

- **No end-to-end caller inside pi-go until ADR #1 lands.**
  Mitigation: unit-test the protocol method against a fake server
  (ticket #2) and pass-through behavior at the spawner (ticket #4).
  End-to-end coverage comes when thread lifetime ships.
- **Deprecated `compacted` event may still arrive from older codex
  versions** (`app-server/README.md:1530`). Mitigation: do not handle
  it. If it appears, it falls through the spawner's `default` branch
  (`spawner_codex.go:147-148`) and is logged.
- **`internal/codex/` protocol drift.** If upstream renames or
  restructures the compaction lifecycle, the constants and types
  added in this spec become dead. Mitigation: parking the spec in
  the v1 plumbing tier keeps the surface small enough to fix in one
  PR.
