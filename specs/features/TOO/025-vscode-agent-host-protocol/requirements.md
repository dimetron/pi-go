# Requirements

## Questions & Answers

### Q1: Which direction is "vscode-agent-host-protocol"?
**A:** Option 1 — pi-go integrates with VS Code's new Agent Host as a third-party agent harness, "same as Claude Code, Codex, Copilot." Option 3 (extension-side changes so the extension supports the Agents view) is in scope only if it is required to achieve that.

### Research notes (from AHP/VS Code docs, 2026-08-26 announcement)
- VS Code moved agent sessions out of the extension host into a dedicated **Agent Host** process that owns sessions; clients (windows, browser, Agents window) attach/detach freely.
- **AHP** (open spec, `microsoft/agent-host-protocol`): JSON-RPC 2.0, transport-agnostic (WebSocket/MessagePort/stdio), URI-addressed channels (`ahp-root://`, `ahp-session://`, `ahp-chat://`), immutable state + pure reducers, ordered `ActionEnvelope`s, write-ahead reconciliation, reconnect/replay via monotonic sequence numbers.
- **Layering (official "AHP and ACP" doc):** AHP = coordination layer (multi-client sync); ACP = communication layer (1:1 agent conversation). "The host speaks AHP to its clients and ACP to its agents." AHP's reference implementation is a standalone server that "delegates agent work to ACP-compatible backends."
- First-party harnesses (Copilot, Claude, Codex) are adapters compiled INTO VS Code's Agent Host process — not yet a public extension API for third-party harness providers.
- AHP ships an official **Go client** library: `github.com/microsoft/agent-host-protocol/clients/go`.
- Standalone host: `code agent host` starts one on localhost with a connection token; `--tunnel` for remote.