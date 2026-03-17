# Rough Idea: Enhance pi-go with Features from oh-my-pi

## Source
https://github.com/can1357/oh-my-pi

## Concept
Enhance the pi-go coding agent with select high-value features from the oh-my-pi project. oh-my-pi is a TypeScript/Rust terminal AI coding agent with extensive capabilities including:

### oh-my-pi Feature Set (Complete)

1. **Hashline Edits** — Content-hash anchored edit system preventing whitespace reproduction errors
2. **AI-Powered Git Commits** — Tool-based git inspection, split commits, hunk-level staging, changelog generation
3. **Python Tool (IPython Kernel)** — Persistent kernel with streaming output, rich helpers, Mermaid support
4. **LSP Integration** — 11 LSP operations, format-on-write, diagnostics on edit, 40+ language configs
5. **Time Traveling Streamed Rules (TTSR)** — Zero-context-use rules injected via pattern matching
6. **Interactive Code Review** — /review command with structured findings and priority levels
7. **Task Tool (Subagent System)** — 6 bundled agents, parallel exploration, worktree isolation
8. **Model Roles** — Configurable role-based routing (default, smol, slow, plan, commit)
9. **Todo Tool** — Phased task lists with ordered tasks and persistent panel
10. **Ask Tool** — Multiple choice and multi-select interactive questioning
11. **Custom Slash Commands** — TypeScript-based extensible commands
12. **Universal Config Discovery** — Multi-tool unified config loading from 8 editors/tools
13. **MCP & Plugin System** — Stdio/HTTP transports, OAuth, hot-loadable plugins
14. **Web Search & Fetch** — Multi-provider search with specialized handlers
15. **SSH Tool** — Remote host management with persistent connections
16. **Browser Tool** — Puppeteer with 14 stealth plugins, accessibility snapshots
17. **Multi-Credential Support** — Round-robin API key distribution with fallback
18. **Image Generation** — Gemini integration with inline terminal display
19. **TUI Overhaul** — Powerline footer, SQLite prompt history, welcome screen, themes
20. **Native Rust Engine** — grep, shell, text, keys, highlight, glob, task, ps, prof, image, clipboard, html modules
21. **AST Tools** — ast_grep for syntax-aware code search
22. **Background Job Management** — Async execution with configurable concurrency
23. **@file Auto-Read** — Inject file contents inline in prompts

## Goal
Select and adapt the most impactful features for pi-go's Go-based architecture, prioritizing features that:
- Improve the agent's coding capabilities
- Are feasible to implement in Go
- Complement pi-go's existing Google ADK-based agent framework
- Provide the highest value-to-effort ratio
