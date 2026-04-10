# Nanocoder TUI — Design Patterns for pi-go

## Source
https://github.com/Nano-Collective/nanocoder

## Idea
Analyze nanocoder's TUI design patterns and identify features that can be adapted for pi-go's Bubble Tea v2 terminal interface. Nanocoder is a TypeScript/Ink-based coding agent with a polished TUI — we want to cherry-pick the best UX patterns and translate them to Go/Bubble Tea.

## Key Areas of Interest
- Theme system (39 themes, JSON-loaded, runtime switching)
- Responsive terminal layout (narrow/normal/wide breakpoints)
- Tool confirmation with formatter previews
- File autocomplete via @mentions
- Context window usage percentage display
- Compact/expanded tool output toggle
- Bash streaming progress with live output
- Markdown rendering in terminal
- Prompt history with full InputState preservation
- Development mode indicator
