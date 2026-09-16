# Requirements

## Questions & Answers

### Q1: Which direction is "vscode-agent-host-protocol"?
**A:** Option 1 — pi-go integrates with VS Code's new Agent Host as a third-party agent harness, "same as Claude Code, Codex, Copilot." Option 3 (extension-side changes so the extension supports the Agents view) is in scope only if it is required to achieve that.

### Q2: Which architecture for pi-go's integration?
**A:** Option 3 (hybrid bridge, per AHP's own layering doc): pi-go ships a standalone **AHP host/server mode** that speaks AHP to clients (VS Code Agents window, any AHP client) and bridges internally to pi-go's **existing ACP server** (`internal/acp/server`) for the agent conversation. Not compiled into VS Code's host process; not primarily extension-side work.

### Q3: Which transports must the AHP host support for v1?
**A:** Option 1 — WebSocket only (matches VS Code Agents window connecting to a standalone host, localhost + connection token, `--tunnel` for remote). stdio/Unix-socket transports deferred.

### Q4: Which slice of the AHP surface is v1?
**A:** Keep v1 minimal ("AHP is broad"). Accept proposed split:
- **In:** `initialize`/`ping`/`reconnect`+replay, `subscribe`/`unsubscribe`; root `listSessions` + `root/session*` notifications; `createSession`/`disposeSession`, `SessionState` (lifecycle, chats catalog, defaultChat, inputNeeded roll-up, workingDirectories), `fetchTurns`; chat channel — `createChat`, turn lifecycle (`chat/turnStarted`, streaming content parts, `turnComplete`), tool-call state machine (`toolCallStart/Delta/Ready/Complete`, confirmation via inputNeeded + `chat/toolCallConfirmed`), `chat/cancel`; `authenticate` (or advertise no protected resources).
- **Out (defer):** terminal channel, automation channels, changesets/review flow, client-contributed tools (`session/activeClientSet` tool routing), customizations/plugins, `completions`, multi-chat per session, multi-client optimistic dispatch/arbitration (single client drives a session in v1).

### Research notes (from AHP/VS Code docs, 2026-08-26 announcement)
- VS Code moved agent sessions out of the extension host into a dedicated **Agent Host** process that owns sessions; clients (windows, browser, Agents window) attach/detach freely.
- **AHP** (open spec, `microsoft/agent-host-protocol`): JSON-RPC 2.0, transport-agnostic (WebSocket/MessagePort/stdio), URI-addressed channels (`ahp-root://`, `ahp-session://`, `ahp-chat://`), immutable state + pure reducers, ordered `ActionEnvelope`s, write-ahead reconciliation, reconnect/replay via monotonic sequence numbers.
- **Layering (official "AHP and ACP" doc):** AHP = coordination layer (multi-client sync); ACP = communication layer (1:1 agent conversation). "The host speaks AHP to its clients and ACP to its agents." AHP's reference implementation is a standalone server that "delegates agent work to ACP-compatible backends."
- First-party harnesses (Copilot, Claude, Codex) are adapters compiled INTO VS Code's Agent Host process — not yet a public extension API for third-party harness providers.
- AHP ships an official **Go client** library: `github.com/microsoft/agent-host-protocol/clients/go`.
- Standalone host: `code agent host` starts one on localhost with a connection token; `--tunnel` for remote.