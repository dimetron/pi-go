# VS Code Agent Host Visibility for Third-Party Harnesses — Research Findings
(as of the AHP announcement, Aug 26 2026)

## Q1: Can an EXTERNAL standalone AHP server be connected to from the Agents window?

**Yes at the protocol level; no documented product-level path.** Details:

- **The architecture explicitly allows non-Microsoft hosts.** Aug 26 2026 blog: "VS Code bundles our own implementation of an Agent Host and our UI is a client, but **other applications can implement either side of the same agnostic protocol**." (https://code.visualstudio.com/blogs/2026/08/26/agent-host-architecture)
- **Remote host flow**: "For remote sessions, the Agent Host runs as a standalone process and exposes AHP over WebSocket. The Agents window reaches it through SSH or a dev tunnel." (https://code.visualstudio.com/docs/agents/concepts/agent-host)
- **Official way to run a host outside VS Code**: `code agent host` — "starts a server on localhost and protects it with a connection token. Use the `--tunnel` option to expose it through a dev tunnel." (same concepts page)
- **Connecting mechanism**: per https://code.visualstudio.com/docs/agents/run/remote-agent-sessions — "The Agents window connects to the remote machine by using AHP over SSH or a dev tunnel. **When you connect, the Agents window automatically installs and starts the VS Code CLI on the remote machine.**" So the SSH/tunnel flow assumes the **VS Code CLI host**, not an arbitrary third-party server.
- **No setting for registering an arbitrary external AHP WebSocket URI** in the settings schema (`agentHostSchema.ts`, `agentHostCustomizationConfig.ts`): settings found are `chat.agentHost.allowSignedOutWhenUsable`, `chat.agentHost.wsl.autoStart`, `chat.agentHost.codexAgent.enabled`, `chat.agentHost.copilotAgent.multiRootEnabled`, `chat.agentHost.sessionCatalog.enabled`, HTTP-proxy settings — all host-behavior settings, none an endpoint registry.
- **Local third-party clients ARE documented — opposite direction**: `src/vs/platform/agentHost/LOCAL_ENDPOINT.md` (https://github.com/microsoft/vscode/blob/main/src/vs/platform/agentHost/LOCAL_ENDPOINT.md) documents a discoverable registry at `<userDataPath>/agent-host/local-endpoint/entries/<sha256>.json` with `type: "editor" | "standalone"`, socket/tcp endpoint, and a `connectionToken` (`?tkn=`) — **any local third-party AHP client can discover and connect to a VS Code agent host**. Confirms third parties as *clients* of VS Code's host; does not document VS Code connecting to a *foreign* host.
- `common/agentHostEndpointRegistry.ts` distinguishes `AgentHostServerType = 'editor' | 'standalone'` — no third-party type exists.

**⚠ Flag (not yet publicly supported)**: no public mechanism (setting, URI handler, or UI command) to add an arbitrary third-party AHP server to the Agents window's host list. The remote flow auto-installs VS Code CLI; "other applications can implement either side" is a protocol-level claim, not a documented Agents-window feature. Third-party host visibility would currently require masquerading as a VS Code CLI host (same registry/SSH/tunnel contract) — plausible but undocumented.

## Q2: Does the AHP spec repo ship a reference HOST server?

**No. Only one server listed, and it lives in the vscode repo.** The Implementations page (https://microsoft.github.io/agent-host-protocol/guide/implementations.html) lists under "Servers": **"VS Code agent host — The reference AHP server implementation (`src/vs/platform/agentHost/node/`)"** — nothing else. Repo README repeats this. **No standalone open-source host in the spec repo** to run/embed besides VS Code itself (its source is MIT — compilable but not packaged/documented as an independent server; the shippable standalone form is VS Code's `code agent host` CLI).

## Q3: Does the vscode Agent Host have a third-party provider registration point?

**No public one — providers are hardcoded.** Verified in source:
- `src/vs/platform/agentHost/node/agentHostMain.ts` (https://github.com/microsoft/vscode/blob/main/src/vs/platform/agentHost/node/agentHostMain.ts) — comment "registers agent providers (Copilot)"; code does `providerService.registerProvider(...CopilotAgent)`, env/SDK-gated `...ClaudeAgent`, register-on-enable `...CodexAgent` (reacting to `chat.agentHost.codexAgent.enabled`). **No extension- or config-driven provider registration** in host bootstrap.
- Internal extension point exists (`IAgentHostProviderService.registerProvider(provider: IAgent)` in `node/agentHostProviderService.ts`) but is platform-internal — not exposed via Extension API or IPC.
- **Corroborating open feature request**: https://github.com/microsoft/vscode/issues/325827 — "Support registration of external agents via Extension API in the agents view" (filed 2026-07-14, **open**, 1 comment): "Agent Host providers require modifying VS Code's core code in `agentHostMain.ts`; Extensions have no way to register full `IAgent` implementations with custom SDKs; the agent picker only shows agents registered through hardcoded provider registration," proposing `vscode.agentHost.registerAgentProvider()`.
- Extensions today contribute *chat-level* customizations (tools, MCP servers, prompt-based custom agents via `vscode.chat.registerCustomAgentProvider()` / `.agent.md`), but "the agent runtime itself runs in the Agent Host process"; harness-level providers remain first-party (https://code.visualstudio.com/docs/agents/concepts/agent-host; https://code.visualstudio.com/learn/agents/4-using-third-party-agents-in-vs-code — third-party support = VS Code embedding the provider's SDK: Copilot/Claude/Codex, wired by Microsoft).

## Q4: Client libraries for connecting to non-VS Code hosts

**Yes — six official client libraries, host-agnostic, with multi-host support.** (https://github.com/microsoft/agent-host-protocol, https://microsoft.github.io/agent-host-protocol/guide/clients.html)

| Language | Package |
|---|---|
| Rust | `ahp` · `ahp-types` · `ahp-ws` |
| TypeScript | `@microsoft/agent-host-protocol` |
| Kotlin (JVM 8) | `com.microsoft.agenthostprotocol:agent-host-protocol` (0.2.0) |
| Go | `github.com/microsoft/agent-host-protocol/clients/go` |
| Swift | SwiftPM `microsoft/agent-host-protocol` |
| .NET | `Microsoft.VisualStudioCode.AgentHostProtocol(.Abstractions)` |

Wire types generated from canonical TS in `types/`; each ships `MultiHostClient` ("for talking to two or more hosts at once"). Remote transport = **JSON-RPC over WebSocket** (concepts/agent-host); local same-user discovery in `LOCAL_ENDPOINT.md`. Client docs don't restrict connections to VS Code hosts ("Connecting to Multiple Hosts" implies arbitrary hosts). **Caveat:** docs describe connecting to *VS Code's* host (token, `initialize`); no catalog of third-party host implementations documented — the Implementations page lists none.

## Bottom line (flags)

1. ✅ **Supported:** third parties build AHP **clients** (6 SDKs) and connect to VS Code's host — locally via on-disk endpoint registry + `?tkn=` token, or remotely via SSH/tunnel WebSocket. Third parties can also run VS Code's own standalone host via `code agent host` (+ `--tunnel`).
2. ⚠ **Protocol-open but not product-supported:** an **external, non-VS-Code AHP server** appearing in the Agents window is architecturally possible but **no documented setting/URI/`chat.agentHost.*` option registers an arbitrary external host**; the SSH/tunnel flow auto-installs the VS Code CLI.
3. ❌ **Not yet supported:** no public Extension API for third-party **in-process providers** — `IAgent` providers (Copilot/Claude/Codex) are hardcoded in `agentHostMain.ts`; tracked in open issue microsoft/vscode#325827 proposing `vscode.agentHost.registerAgentProvider()`.
4. ⚠ **No standalone reference host shipped** outside VS Code: the spec repo's Implementations page lists exactly one server — vscode's `src/vs/platform/agentHost/node/` (standalone entry `agentHostServerMain.ts`).

## Implication for pi-go (fact, not decision)
pi-go's AHP host will be protocol-correct and connectable by any AHP client (incl. the official Go client against any host). Whether **VS Code's Agents window** can natively attach to it is **not documented as supported** today; the documented paths connect to VS Code's own `code agent host`. Extension-host sessions (the existing `vscode/` extension using proposed `chatSessionsProvider`) continue to be supported by VS Code ("Agent sessions that don't run on the Agent Host run in the extension host. Existing extension-host sessions continue to run there." — concepts/agent-host).