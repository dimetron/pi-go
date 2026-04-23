
## Roadmap

### Issues

- [x] add --header name=val --insecure --url string --model string in pi serve
- [x] In serve mode - use single place for Connected, Disconnected, Reconnecting messages in right top widget

### Features

#### Short term
- [x] OAuth with Codex, ~~Claude~~
- [x] ACP Coding Agent protocol https://agentclientprotocol.com/protocol/overview
- [x] OTEL tracing https://opentelemetry.io/docs/
- [x] Go LSP server
- [x] Rust LSP server
- [x] Python LSP server
- [ ] Kotlin LSP server `brew install JetBrains/utils/kotlin-lsp`
- [ ] Switch between models for plan/run tasks
- [ ] Kanban board for agent tasks
- [ ] OAuth with Google/Microsoft for web UI
- [ ] A2A Agent as a tool to work with other agents like KAgent

#### Long term

- [ ] Agent Sandbox via ssh
- [ ] Submit pi-go to ACP registry https://agentclientprotocol.com/get-started/agents
- [ ] Evaluations using terminal bench harbor https://harborframework.com/registry
- [ ] Temporal remote distributed workflows for long-running tasks
- [ ] Distributed tracing

### Agent Harness
- [ ] ignore specific .folders .cursorignore https://code.claude.com/docs/en/settings#permissions
- [ ] connect to different sandboxes (ssh, docker, k8s)

### Sub Agents
- [ ] research agent with cli gh, tavily, exa, perplexity plugins
- [ ] excalidraw for visual planning and diagramming https://excalidraw.com
- [ ] Notebook LM https://notebooklm.google.com

### TUI
- [ ] Tabs for sessions
- [ ] TASK Workflows dashboard with task list, agent status, and logs
- [ ] Show session /tree

### Tools
- [x] Browser mcp support
- [ ] WhatsApp/Telegrtam gateway
- [ ] Agentic wiki with vector search

### AI providers

- [x] Ollama (local/cloud)
- [x] Ollama (open ai)
- [ ] Ollama (anthropic)
- [x] OpenAI (API / Codex)
- [x] Anthropic (API)
- [x] Google Gemini
- [ ] Google Vertex AI
- [x] Microsoft Azure OpenAI
- [ ] Amazon
- [ ] Open Router
- [ ] Fireworks AI