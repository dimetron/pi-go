# Design — Codex Context Compaction Plumbing

**Status:** proposed v1
**Companions:** `brief.md`, `gap-analysis.md`, `plan.md`,
`research/codex-app-server-compaction.md`

## Decision summary

v1 ships **additive protocol plumbing on `internal/codex/` only.** No
changes to `internal/subagent/`, no TUI changes, no extension changes.

The reasoning:

- Compaction surfaces in upstream Codex as a unified lifecycle
  (`item.type = "contextCompaction"`); pi-go's existing notification
  channel is exactly the right delivery mechanism.
- The orchestrator pass-through at
  `internal/subagent/spawner_codex.go:147-148` already forwards unknown
  codex event types, so session-level routing is purely additive.
- Spawner architecture is out of scope — see ADR #1 below.

## Protocol additions

All edits land in `internal/codex/protocol.go`.

**Method constant** — extend the const block at `protocol.go:13-21`:

~~~go
// added after line 20
const MethodThreadCompactStart = "thread/compact/start"
~~~

**Item constant** — extend the const block at `protocol.go:56-66`:

~~~go
// added after line 65
const ItemContextCompaction = "contextCompaction"
~~~

**Request/response types** — add parallel to `ReviewStartResponse`
(around `protocol.go:211`):

~~~go
type ThreadCompactStartParams struct {
    ThreadID string `json:"threadId"`
}

// server returns {}; preserved as a typed empty struct for future fields
type ThreadCompactStartResponse struct{}
~~~

**Compaction item** — add as a **separate struct**, parallel to `Item`
at `protocol.go:248-262`, per architect's note (keeps parse-time
discrimination clean):

~~~go
type CompactionItem struct {
    ID string `json:"id"`
    // upstream has no payload today; reserved for future fields
}
~~~

## Client method

New method on `*Client`, after `Client.request()` at
`internal/codex/client.go:213-249`:

~~~go
func (c *Client) ThreadCompactStart(ctx context.Context, threadID string) error {
    _, err := c.request(ctx, MethodThreadCompactStart,
        ThreadCompactStartParams{ThreadID: threadID})
    return err
}
~~~

Returns nil on success. Progress comes through the existing buffered
notification channel (`client.go:40-49`) as `item/started` then
`item/completed`. No new channel, no new goroutine.

## Session event type

In `internal/codex/session.go`, add a constant alongside existing event
types:

~~~go
const EventTypeCompaction = "compaction"
~~~

In the item-type switch where `ItemAgentMessage`, `ItemReasoning`, etc.
are translated, add:

~~~go
case ItemContextCompaction:
    s.emit(Event{
        Type:      EventTypeCompaction,
        SessionID: s.sessionID,
        // carry the item's ID and any other relevant fields
    })
~~~

No other session changes required.

## Spawner pass-through

**Zero code change** in `internal/subagent/spawner_codex.go`. The
`default` branch at `spawner_codex.go:147-148` already forwards unknown
codex event types as orchestrator events, so
`codex.EventTypeCompaction` arrives as
`Event{Type: "compaction", ...}` and the orchestrator already routes
unknown types.

The `codexSession` interface (`spawner_codex.go:30-35`) is unchanged.

## Open question (ADR #1 — codex thread lifetime)

pi-go's `internal/subagent/spawner_codex.go:102-122` (`startCodexSession`)
creates a fresh `codex.NewSession` per subagent invocation. There is no
thread persistence, no `thread/resume`, no transcript continuity across
turns.

**Consequence:** v1's `Client.ThreadCompactStart` has **no caller** in
pi-go's current architecture. The only end-to-end tests possible today
are direct-binary smoke tests or a future orchestrator that reuses
threads.

**Recommendation:** ship the plumbing anyway. Add a unit test for the
protocol method only. File a separate spec for thread lifetime. The
plumbing is forward-compatible and small (~50 LoC). Deferring it would
mean revisiting this code the moment we need thread reuse.

## Open question (ADR #2 — remote compaction auth)

`thread/compact/start` itself requires no auth beyond what
`internal/codex/` already has (the app-server is a subprocess of pi-go
with the user's `CODEX_HOME`). But auto-compaction that delegates to
`/v1/responses/compact` requires OAuth scopes that `internal/codex/` does
not negotiate — it does not import `internal/auth/` at all (grep on
`internal/codex/` for `auth|oauth|token|api.?key` returns zero hits in
production code, only a test stub). `internal/auth/` handles ChatGPT
OAuth for the TUI's own `codex` provider used by pi-go's main agent
(`internal/auth/auth.go:118-141`, `CodexOAuth: true`); it does not
mediate the codex *app-server's* own outbound calls to OpenAI.

**Recommendation:** document and defer. Out of scope for v1.

## Out of scope (v1)

- `Feature::TokenBudget` mode (`research/codex-app-server-compaction.md`).
- `/v1/responses/compact` remote path (auth scope negotiation).
- `RemoteCompactionV2`.
- Model-fallback retry chain on compaction errors.
- `preCompact` / `postCompact` hook surfacing to `internal/extension/`.
- Cross-turn thread lifetime (ADR #1).
- Deprecated `compacted` event alias.
