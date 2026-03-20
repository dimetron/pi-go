# Nanocoder TUI

Build a terminal-based coding agent TUI inspired by [Nanocoder](https://github.com/Nano-Collective/nanocoder) — a community-built, local-first CLI coding agent.

## Source of Inspiration

Nanocoder is a TypeScript-based CLI coding agent by the Nano Collective that:
- Supports multiple AI providers (OpenRouter, Ollama, local models)
- Offers interactive and non-interactive modes
- Provides tool capabilities (file operations, command execution)
- Integrates MCP (Model Context Protocol) servers
- Features checkpointing, task management, custom commands, keyboard shortcuts
- Distributed via npm, Homebrew, Nix Flakes
- MIT licensed, 1.5k+ stars

## Initial Concept

"nanocoder-tui" — a Go-based TUI implementation leveraging the existing pi-go codebase (Bubble Tea v2, agent infrastructure, MCP integration, tool registry) to provide a nanocoder-like experience with:
- Local-first AI coding agent in the terminal
- Multi-provider support (OpenRouter, Ollama, local models)
- Rich TUI with Bubble Tea
- Agentic tool use (file ops, shell, code search)
- MCP server integration
