# Anthropic Claude Code VS Code Extension (`anthropic.claude-code`) — Integration Findings

## ⚠ Premise correction
Commit `98f15b5` in microsoft/vscode is **"Implement agentHost process (#296627)", dated 2026-03-16** — it *added* the Agent Host *with* a Claude agent included; it is not a "Remove Claude agent" commit. No "Remove Claude agent from agent-host process" commit was found in microsoft/vscode history. What changed in Aug–Sep 2026 is the **opposite direction**: Claude's agent-host presence was *expanded* (Claude Agent SDK bumped to 0.3.239 on 2026-08-28; AHP 1.0.0 adoption 2026-08-21; "Multiple chats in a session now supports Claude agent", Sept 2026). The likely source of the "removal" rumor is the removal of the `ChatAgentHostEnabled` admin *policy* in Sept 2026.

Note: the AHP docs page fetched during earlier research still shows "Remove Claude agent from agent-host process" in a commit *highlights* list — the authoritative commit itself (`98f15b5`, #296627) contradicts that title. Treated as resolved in favor of the primary source (the commit).

## 1. VS Code surfaces used
- `anthropic.claude-code` uses **its own webview UI + sessions-list sidebar** (verified in Open VSX manifest v2.1.273: `claudeVSCodeSidebar`, `claudeVSCodeSessionsList`; **no `chatSessions` contribution, no `enabledApiProposals`, no `chatParticipants`**). It does **not** use the Chat view.
- VS Code's *own built-in Claude harness* (separate product path, Microsoft-side) uses the Chat view/Agents window.
- Proposed APIs: `chatSessionsProvider` is real (`registerChatSessionItemProvider`, now deprecated → `createChatSessionItemController`); **no `registerCustomAgentProvider` exists** in `chatParticipantAdditions`; `LanguageModelChatProvider` is real (`chatProvider.d.ts`).

## 2. AHP vs SDK
- **The extension speaks neither**: it spawns its **bundled `claude` CLI as a child process over stdio** + a local `ide` MCP server for editor integration.
- **VS Code's built-in Claude harness** runs the **Claude Agent SDK in-process inside the Agent Host**, adapted to AHP (JSON-RPC; MessagePort IPC locally, WebSocket remotely). Host registration is gated by `chat.agentHost.claudeAgent.enabled` (default on) + SDK reachability via `product.agentSdks` downloader (verified in `agentHostMain.ts`).

## 3. Sessions
- Extension sessions persist in **its own store** (survive reloads, grouped per workspace); they do **not** appear in VS Code's Agents window — and actually break there (https://github.com/microsoft/vscode/issues/320719).
- Handoff across harnesses and cross-window persistence are **Agent Host features of Microsoft's harness**, gated by `chat.agentHost.claudeAgent.enabled` + `chat.agents.claude.preferAgentHost`.

## 4. Timeline
- v2.0 native UI Sep 29 2025 → VS Code Claude agent 1.109 (Feb 2026, extension host) → `98f15b5` AgentHost (Mar 2026) → Agent Host Stable 1.129 (Jul 15 2026) → AHP open announcement Aug 26 2026 → 1.136 Sept 2026.

## 5. Permissions
- Extension: Claude-native modes (Auto/Manual/Plan/acceptEdits/bypass) with its own diff-accept/reject commands.
- Agent Host: Claude's permission requests are **translated by the adapter into the AHP approval model**; enterprise gate via `Claude3PIntegration` policy.

## Relevance for pi-go
Claude Code's extension = **self-contained webview + own CLI child process over stdio + own persistence**, deliberately avoiding proposed APIs (no `enabledApiProposals` in its manifest — works on stable VS Code). Codex extension = same shape but *does* adopt the proposed `chatSessionsProvider` for the Sessions view. pi-go's extension follows the Codex shape. Both vendors keep their "agent host" (app-server / claude CLI) as the durable process and the extension as a thin UI — the same layering pi-go is targeting with its AHP host.