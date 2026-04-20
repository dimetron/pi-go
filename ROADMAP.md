
## Roadmap

### Issues

- [x] add --header name=val --insecure --url string --model string in pi serve
- [x] In serve mode - use single place for Connected, Disconnected, Reconnecting messages in right top widget

### Features

#### Short term
- [x] OAuth with Codex, ~~Claude~~
- [x] ACP Coding Agent protocol https://agentclientprotocol.com/protocol/overview
- [ ] A2A Agent as a tool to work with other agents like KAgent
- [ ] Agent Sandbox via ssh
- [ ] Kanban board for agent tasks
- [ ] OAuth with Google/Microsoft for web UI

#### Long term
- [ ] Submit pi-go to ACP registry https://agentclientprotocol.com/get-started/agents
- [ ] Evaluations using terminal bench harbor https://harborframework.com/registry
- [ ] Temporal remote distributed workflows for long-running tasks
- [ ] Distributed tracing

### Agent Harness
- [ ] ignore specific .folders .cursorignore https://code.claude.com/docs/en/settings#permissions
- [ ] connect to different sandboxes (ssh, docker, k8s)

### Sub Agents
- [ ] research agent with cli gh, tavily, exa, perplexity plugins
- [ ] excalidraw agent for visual planning and diagramming

### TUI
- [ ] Tabs for sessions
- [ ] TASK Workflows dashboard with task list, agent status, and logs
- [ ] Show session /tree

### Tools
- [x] Browser mcp support
- [ ] WhatsApp/Telegrtam gateway
- [ ] Agentic wiki with vector search