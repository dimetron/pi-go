# OpenAI Codex VS Code Extension (`openai.chatgpt`) — Integration Findings

Identity: publisher "OpenAI", id `openai.chatgpt`, title "Codex – OpenAI's coding agent", ~14.3M installs, version line 26.x. Closed source — public repo `openai/codex` (Apache-2.0) is the CLI/core; the extension's code is not in it. Docs: https://developers.openai.com/codex/ide · https://marketplace.visualstudio.com/items?itemName=openai.chatgpt

## 1. VS Code APIs used
- **No chat participant** — renders its **own webview panel** (`CodexWebviewProvider`), not the native Chat view. No `@chatgpt`/`@codex` participant; feature request confirms: https://github.com/openai/codex/issues/8742. LM-provider API exposure also requested: https://github.com/openai/codex/issues/26906.
- **DOES use proposed `chatSessionsProvider` API** — from extension v0.5.23 (Oct 2025), package.json declares `enabledApiProposals: ['chatSessionsProvider', 'languageModelProxy']` and calls `vscode.chat.registerChatSessionItemProvider` — this is how Codex sessions appear in the Agent Sessions view. API introduced VS Code 1.103, still proposed; Microsoft already deprecated `registerChatSessionItemProvider` in favor of a `ChatSessionItemController` API (migration tracked in https://github.com/microsoft/vscode/issues/288459). Users hit "using the API proposal 'chatSessionsProvider' that is not compatible" errors (community reports).
- **Agent runs in its own process, not the extension host**: extension spawns the **Codex App Server as a child process**, JSON-RPC over stdio; the agent loop lives in the Rust process. OpenAI blog: clients "bundle or fetch a platform-specific App Server binary, launch it as a long-running child process, and keep a bidirectional stdio channel open… the shipped artifact includes the platform-specific Codex binary and is pinned to a tested version" (https://openai.com/index/unlocking-the-codex-harness). Log confirmations: `[CodexMcpConnection] Spawning codex app-server`; modern invocation `codex -c features.code_mode_host=true app-server --analytics-default-enabled` (https://github.com/openai/codex/issues/45400). (Class name `CodexMcpConnection` is a fossil of the original MCP experiment before OpenAI moved to plain JSON-RPC — same blog.)

## 2. AHP participation
**The OpenAI extension does not speak AHP itself; VS Code added a separate "Codex harness" that does.** Two distinct paths:
- **Extension-native mode (default):** OpenAI's own webview + spawned `codex app-server` (OpenAI's proprietary JSON-RPC protocol, *not* AHP). VS Code docs: "The Local harness and Codex sessions from the OpenAI extension run only in the main VS Code window" (https://code.visualstudio.com/docs/agents/run/agents-window).
- **Agent Host mode (VS Code-side, experimental):** VS Code 1.129 (July 2026) introduced the Agent Host running harnesses "such as Copilot, Claude, and Codex, based on the Agent Host Protocol (AHP)" (https://code.visualstudio.com/updates/v1_129).

**Gating settings** (https://code.visualstudio.com/docs/agents/reference/ai-settings):
- `chat.agentHost.enabled` — master opt-in.
- `chat.agentHost.codexAgent.enabled` — "(Experimental) Register the Codex provider in the Agent Host process." Default false; no host restart needed.
- `chat.editor.codex.preferAgentHost` — "(Experimental) Run Codex sessions opened from the Chat view on the Agent Host instead of the OpenAI extension. Only one Codex implementation appears per window. Requires `chat.agentHost.codexAgent.enabled`; prompts for restart." Default false.
- `chat.agentHost.allowSignedOutWhenUsable` — Agents window without GitHub sign-in when "Codex signed in to ChatGPT" available.

**Chronology** (partly inferred): extension sessions in the Sessions view via `chatSessionsProvider` (Oct 2025); VS Code 1.129 (Jul 2026) added the Agent Host with a first-party Codex harness; 1.133-era docs describe the host-side Codex harness with ChatGPT-subscription auth. Dual-provider test plan: https://github.com/microsoft/vscode/issues/330900.

## 3. Sessions: persistence, list, resume, handoff
- **Own persistence:** Codex threads persisted by the app-server ("Codex creates, resumes, forks, and archives threads, and persists the event history so clients can reconnect" — OpenAI blog). On disk: JSONL "rollout" files under `~/.codex/sessions/YYYY/MM/DD/` + SQLite `threads` table + `codex resume <id>`; extension log shows `thread/list` RPC (`limit:50`) for history and `thread/resume` to reopen (community debugging posts, https://github.com/openai/codex/discussions/2956).
- **Agents window/list**: sessions appear in VS Code's Agent Sessions view through the proposed API (§1). Side chats "not available for Codex sessions" (https://code.visualstudio.com/docs/agents/run/sessions/manage-sessions).
- **External-session discovery**: "VS Code can discover local agent sessions created by supported applications outside VS Code. You can open and continue sessions from Copilot CLI, the GitHub Copilot app, Claude Code, and Codex" (manage-sessions).
- **Handoff**: `@codex` hand-off documented by Microsoft as a pattern but not implemented (openai/codex#8742). Agent-to-agent session handoff exists on the Agent Host, "also available for Codex sessions when Codex runs on the Agent Host". Resume-after-restart has known bugs (openai/codex#20834).

## 4. Backend launch
Bundled platform binary + spawned CLI subcommand over stdio: `codex app-server`, **JSON-RPC 2.0 over stdio (newline-delimited JSON)**; `initialize` handshake advertises `clientInfo: {name: "codex_vscode", title: "Codex VS Code Extension"}`. WS transport exists but is experimental/unsupported (https://gist.github.com/oneryalcin/ee2c27e2d8aa040da8fbe7eebcc2ecea). OpenAI implements its **own host** (`app-server`); VS Code's Agent Host additionally *adapts* it into AHP when the experimental harness is enabled.

## 5. Third-party harness statements
- OpenAI frames the App Server as the third-party integration path (JetBrains, Xcode partners; SDKs Go/Python/TS/Swift/Kotlin; TS defs via `codex app-server generate-ts`) — "Unlocking the Codex harness". Protocol documented as "experimental" for outsiders (promptfoo provider docs).
- Gaps tracked in issues: no Extension API to register full agent providers in the Agents view (microsoft/vscode#325827); Codex chat/LM-API integration requests (openai/codex#26906, #8742); Codex harness Multi-Chat parity (microsoft/vscode#323624). **No OpenAI statement endorsing AHP for the extension** — the extension's interop story is the App Server JSON-RPC, not AHP.

## Bottom line for pi-go comparison
The Codex extension is the closest structural precedent to pi-go's existing shape: **own webview UI + spawned agent backend as child process over stdio JSON-RPC + proposed `chatSessionsProvider` for the sessions list + own persistence layer**. pi-go's `vscode/` extension is the same shape (spawns `pi acp-server` stdio, own webview, `chatSessionsProvider`, pi-go's store). The "first-party harness in the Agent Host" path exists only because Microsoft wrote in-process adapters (Copilot/Claude/Codex) gated behind experimental settings — inaccessible to third parties today (microsoft/vscode#325827).