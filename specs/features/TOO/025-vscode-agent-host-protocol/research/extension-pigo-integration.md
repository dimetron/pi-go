# pi-go `vscode/` Extension — Integration Point Map (facts only)

## 1. Activation flow — `vscode/src/extension.ts`
**`activate(context)`** (extension.ts:42): creates one `PiGoAcpClient` (acp.ts), one `TranscriptStore`, a shared `refresh` EventEmitter, and `active: Set<string>` of ACP session ids with in-flight prompts (extension.ts:43-46). All into `context.subscriptions`.
- Global `session/update` fan-out → `recordUpdate(store, update)` → `refresh.fire()` (extension.ts:52-55).
- `client.onSpawnError` → error dialog offering to open `pi-go.command` settings (extension.ts:59-70).
- **Feature-detects proposed API**: `typeof chat.registerChatSessionItemProvider === "function" && typeof chat.registerChatSessionContentProvider === "function"` → `activateNative(...)`; else logs error telling user to launch with `--enable-proposed-api pi-go.pi-go-vscode` (extension.ts:73-85).
- Always registers `ChatPanelProvider` (one instance for view ids `pi-go.chat` + `pi-go.chatSecondary`, `retainContextWhenHidden: true`) and `pi-go.attachFile` (extension.ts:90-100).
- `registerSessionsTree(...)` (extension.ts:104); `pi-go.start` quick-prompt command via OutputChannel (one-shot `newSession` + `prompt`, streaming `agent_message_chunk` text) (extension.ts:107-139).
- `deactivate()` empty — subscription disposal kills the acp-server child process (extension.ts:472-475).

**Spawn config** (acp.ts:60-101): `vscode.workspace.getConfiguration("pi-go")` → `command` (default `"pi"`), `args` (default `["acp-server"]`), `cwd = workspaceFolders[0].fsPath`. `spawn(command, args, { cwd, stdio: ["pipe","pipe","pipe"] })` — **stdio transport**; stderr piped to a log OutputChannel. **No env vars set.** No flags besides args. 250 ms spawn-failure backoff (acp.ts:69-71). On `process exit` → connection dropped, `initialized = false`; next request respawns (acp.ts:79-83, 123-137: one respawn + 250 ms wait + retry on transport failure).
**Lifecycle**: workspace folder change → `client.reconnect()` (kill + respawn in new cwd) + refresh (extension.ts:157-162).

## 2. ACP client — `vscode/src/acp.ts` (class `PiGoAcpClient`)
Uses `@agentclientprotocol/sdk` `^1.4.0` (package.json:188). Imports: `acp.ndJsonStream(Writable.toWeb(stdin), Readable.toWeb(stdout))`, `acp.client({ name: "pi-go-vscode" })`, `acp.methods.client.session.update` notification hook, `acp.PROTOCOL_VERSION` (= 1).

**ACP agent methods called** (via `conn.agent.request(...)` / `conn.agent.notify(...)`):
- `initialize` (acp.ts:107-111) — sends `protocolVersion: acp.PROTOCOL_VERSION, clientCapabilities: {}, clientInfo: {name:"pi-go-vscode", version:"0.1.0"}`; **rejects any protocolVersion mismatch**; caches caps: `supportsList = agentCapabilities.sessionCapabilities.list !== undefined`, `embeddedContext = promptCapabilities.embeddedContext === true` (acp.ts:115-119).
- `session/new` `{cwd, mcpServers: []}` (acp.ts:139-147)
- `session/list` `{cwd}` — only if `caps.supportsList`, else `[]` (acp.ts:150-170)
- `session/load` `{sessionId, cwd, mcpServers: []}` (acp.ts:173-179)
- `session/prompt` `{sessionId, prompt: blocks}` with cancellation → `session/cancel` on token cancel (acp.ts:183-199)
- `session/cancel` — `notify`, best-effort (acp.ts:201-208)

**NOT called by the extension**: `session/set_mode`, `session/set_config_option`, `session/fork`, `session/resume`, `session/delete`, `session/close` (verified: no `set_mode|setMode|set_config` matches under `vscode/src/`). **`available_commands_update` is not a client call** — it's an inbound `session/update` variant captured per-session into `Map<string, AvailableCommand[]>`, exposed via `availableCommands(sessionId)` (acp.ts:55-58, 92-97).

**Session state the extension keeps** (all in-memory, per window):
- `PiGoAcpClient`: `known: Map<sessionId, SessionEntry>` (`{sessionId, title?, updatedAt?, cwd}`), `caps`, per-session `commands`, `initialized`, process/connection refs (acp.ts:32-41).
- `TranscriptStore` (transcript.ts:39-156): `Map<acpId, Uri>` (`pi-go-session://local/<ts36>-<seq>`), reverse map, `Map<acpId, Turn[]>` where `Turn = {role:"user";prompt} | {role:"agent";parts: TurnPart[]}`, `TurnPart = text | thought | tool(ToolCallState)`. Methods: `register/reset/acpIdFor/uriFor/rebind/snapshot/title/appendUserTurn/appendMessageChunk/appendThought/upsertToolCall`. `recordUpdate` (transcript.ts:166-196) folds `user_message_chunk`/`agent_message_chunk`/`agent_thought_chunk`/`tool_call`/`tool_call_update` into the store; other update kinds ignored.
- Module-level in extension.ts: `active` set, `lastActive` session id for autocomplete.

## 3. VS Code UI integration
**Proposed API usage** (`enabledApiProposals`: `chatSessionsProvider`, `chatParticipantAdditions`, `chatParticipantPrivate` — package.json:21-25; engines `^1.137.0`):
- **No `provideChatSessions`/`onDidChangeChatSessions`** — the implemented names are `ChatSessionItemProviderDto { onDidChangeChatSessionItems: Event<void>; provideChatSessionItems(token): ChatSessionItemDto[]; resolveChatSessionItem? }` and `ChatSessionContentProviderDto { provideChatSessionContent(resource, token, context): ChatSessionDto }` (types/chatApi.ts:46-63). Registered: `chat.registerChatSessionItemProvider(SESSION_TYPE /* "pi-go" */, itemProvider)` and `chat.registerChatSessionContentProvider(SESSION_SCHEME /* "pi-go-session" */, contentProvider, participant)` (extension.ts:263-266). `provideChatSessionContent` calls `store.reset` + `client.load(entry)` then returns `{title, history: historyFromTurns(store.snapshot), activeResponseCallback: undefined, requestHandler, options: undefined}` (extension.ts:236-259).
- **`@pi-go` participant**: `vscode.chat.createChatParticipant(`${context.extension.id}.agent`, () => {})` — empty handler, exists only as the required 3rd arg of `registerChatSessionContentProvider`; id `pi-go.pi-go-vscode.agent` (extension.ts:196-200; package.json chatParticipants). Also carries a loosely-typed `participantVariableProvider` with `triggerCharacters: ["/"]` providing `ChatCompletionItem`s from `client.availableCommands(lastActive)` (extension.ts:207-233).
- **Language model stub**: `vscode.lm.registerLanguageModelChatProvider("pi-go", { provideLanguageModelChatInformation → [modelInfo], provideLanguageModelChatResponse → throws, provideTokenCount → 0 })`; modelInfo id `"agent"`, name `"pi-go agent"`, 1M token caps, `targetChatSessionType: "pi-go"` via loose cast (extension.ts:281-301).
- **Sessions tree**: `PiGoSessionsProvider implements vscode.TreeDataProvider<SessionNode>` + `registerSessionsTree(...)` returning `{treeView, provider}` — `getChildren` → `client.listSessions()` newest-first; badge = `active.size`; item command `pi-go.openSession`; context values `piGoSession`/`piGoSessionActive` (sessionsTree.ts:9-135).
- **Chat panel**: `ChatPanelProvider implements vscode.WebviewViewProvider, vscode.Disposable` (chatPanel.ts:42) — `resolveWebviewView`, message types `ready|prompt|newSession|openSession|cancel|revealFile|requestFileAttachment|draft`; posts `sessionLoaded/replayStarted/userTurn/agentChunk/thoughtChunk/toolUpdate/turnEnd/commandsUpdated/state/notice/error/attachmentsAdded` (shared/protocol.ts). `openSession(entry)` = register + reset + `client.load` replay; `runPrompt` guards one in-flight `CancellationTokenSource`, handles `/help` (local markdown) and `/clear` (newSession + `store.rebind`), sends attachments via `pathsToBlocks` when `caps.embeddedContext`, then `client.prompt(sessionId, blocks, token)` (chatPanel.ts:174-266). CSP allows `https://cdnjs.cloudflare.com` for lazy mermaid (chatPanel.ts:~355).

## 4. Session persistence
**The extension persists nothing.** `TranscriptStore` is explicitly in-memory per window (transcript.ts:34-38; README "Known limits": *"The transcript store is in-memory per window; the persisted-session list comes from pi-go's own on-disk store via `session/list`, so reopening a session replays through `session/load`"*). No `context.workspaceState`/`globalState` usage anywhere in `vscode/src/`. Resume = `session/load` replay only; no `session/resume`/`fork` calls.

## 5. README / DESIGN notes on limitations & follow-ups
`vscode/README.md`:
- **Slash command dispatch**: "pi-go dispatches slash commands only in its TUI today" — in VS Code, `/clear` → fresh session same editor; `/help` lists advertised commands; any other `/command` forwarded as plain text with a notice. Follow-up stated at README.md:46-47: *"Follow-up (pi-go side): dispatch slash commands in the ACP prompt handler; the advertisement already exists."*
- **set_mode / session config / thinking level**: **zero references** in `vscode/README.md` or `vscode/src/` (grep for `set_mode|setMode|set_config|thinking` in src only hits thinking-chunk rendering). SDK 1.4.0 schema does include `session/set_mode`, `session/set_config_option`, `current_mode_update` — the extension just never uses them.
- Other known limits (README.md:144-154): in-memory transcript store; sub-agent nested tool cards flattened into parent output; `registerChatSessionItemProvider` deprecated upstream in favor of `createChatSessionItemController` (migration = follow-up); extension exposes **no** fs/terminal ACP callbacks and pi-go never sends `session/request_permission` (auto-approve).
- Mention caps: 256 KiB/file, 10 files, 1 MiB/prompt, text files only (README.md:26-28).
- Mock agent for testing: `cmd/pi-acp-mock` with `PI_MOCK_TOOLS=1 PI_MOCK_THOUGHTS=1 PI_MOCK_COMMANDS=1 ...` (README.md:139-142).

## 6. `@agentclientprotocol/sdk` dist contents — AHP/agent-host check
**Negative.** No `AHP`, `agent-host`, or `AgentHost` types anywhere in `node_modules/@agentclientprotocol/` (grep over `*.d.ts` + `*.js`, excluding maps = 0 hits; single case-insensitive hit is base64 sourcemap offset data in `dist/v2/acp.js.map` — false positive). The package (`sdk@1.4.0`, Zed Industries) ships:
- Stable v1 entry (`dist/acp.js|d.ts`): `AGENT_METHODS` = initialize, authenticate, providers/list|set|disable, session/new, load, **set_mode**, **set_config_option**, prompt, cancel, list, delete, fork, resume, close, logout, nes/*, document/did*; `CLIENT_METHODS` = session/request_permission, session/update, fs/*, terminal/*, mcp/*, elicitation/*; `PROTOCOL_VERSION = 1`.
- Experimental v2 draft at subpath `dist/v2/` (opt-in `@agentclientprotocol/sdk/experimental/v2`), adding `session/set_model`, batch messages, `nes/*`, `document/did*` — not imported by the extension (imports only root `@agentclientprotocol/sdk`).

## Key signatures summary
| File | Export | Signature |
|---|---|---|
| acp.ts | `PiGoAcpClient` | `newSession(): Promise<SessionEntry>`; `listSessions(): Promise<SessionEntry[]>`; `load(entry): Promise<void>`; `prompt(sessionId, blocks: acp.ContentBlock[], token): Promise<void>`; `cancel(sessionId)`; `reconnect()`; `dispose()`; `onSessionUpdate(l): vscode.Disposable`; `onSpawnError: Event<string>`; `get capabilities(): AgentCaps | undefined`; `availableCommands(sessionId): acp.AvailableCommand[]` |
| acp.ts | consts | `SESSION_SCHEME = "pi-go-session"`, `SESSION_TYPE = "pi-go"`; helpers `chunkText`, `thoughtText`, `isToolUpdate`, `toolPayload`, `workspaceCwd` |
| transcript.ts | `TranscriptStore` | `register/reset/acpIdFor/uriFor/rebind/snapshot/title/appendUserTurn/appendMessageChunk/appendThought/upsertToolCall`; `recordUpdate(store, update): boolean`; `toolStateOf(u): ToolCallState` |
| types/chatApi.ts | `chatParts()` | `(): Partial<ChatTurnConstructors>` — runtime-resolves `ChatRequestTurn`, `ChatResponseTurn2`, `ChatResponseMarkdownPart`, `ChatToolInvocationPart`, `ChatResponseThinkingProgressPart`, `ChatCompletionItem`, etc. off the `vscode` module |
| chatParts.ts | mappers | `toolPartFor`, `thoughtPartFor`, `noticePartFor(text, ctors, warning?)`, `historyFromTurns(turns, ctors)`, `availableCommandsMarkdown(commands)` |
| mentions.ts | converters | `pathsToBlocks(paths, token)`, `referencesToBlocks(references, token)` → `MentionsResult {blocks, skipped}` |
| sessionsTree.ts | `registerSessionsTree` | `(context, client, store, refresh, active, chatPanel) → {treeView, provider}` |
| chatPanel.ts | `ChatPanelProvider` | `implements WebviewViewProvider`; `openSession(entry)`, `startNewSession()`, `attachFiles()` |

## Comparison table (extension precedents)
| Aspect | Codex ext (`openai.chatgpt`) | Claude Code ext (`anthropic.claude-code`) | pi-go ext (`pi-go.pi-go-vscode`) |
|---|---|---|---|
| UI surface | own webview panel | own webview + sessions sidebar | own webview (`pi-go.chat`) + tree + proposed Chat-sessions API |
| Proposed APIs | `chatSessionsProvider`, `languageModelProxy` | none (`no enabledApiProposals`) | `chatSessionsProvider`, `chatParticipantAdditions`, `chatParticipantPrivate` |
| Backend | spawned bundled `codex app-server` child, stdio JSON-RPC 2.0, pinned version | spawned bundled `claude` CLI child, stdio + `ide` MCP server | spawned `pi acp-server` child, stdio ACP (SDK 1.4.0) |
| Own host/protocol | OpenAI "App Server" (proprietary JSON-RPC, experimental SDKs) | Claude Agent SDK / CLI (Anthropic proprietary) | ACP server (`internal/acp/server`) + legacy `pirpc` |
| Session persistence | app-server threads (`~/.codex/sessions`, SQLite) + `thread/list`/`thread/resume` | extension's own store, per-workspace | pi-go store via ACP `session/list`+`session/load`; extension in-memory transcript only |
| In VS Code Agent Host | experimental MS-side harness (`chat.agentHost.codexAgent.enabled`) | MS-side harness, `chat.agentHost.claudeAgent.enabled` (default on), SDK in-process, AHP adapter | none (pi-go would ship its own standalone host) |
| AHP spoken by extension | no | no | no (today) |
| Slash commands | n/a (own UI) | own UI | forwarded as text; dispatch follow-up noted in README |
| Permission flow | own webview | own (Auto/Manual/Plan/acceptEdits) | none — pi-go auto-approves; no `session/request_permission` |

Both vendor extensions keep their durable agent process as the "host" and the extension as a thin UI — the same layering pi-go targets with its standalone AHP host.