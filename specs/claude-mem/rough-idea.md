# Rough Idea: Implement claude-mem natively in pi-go

## Source
https://github.com/thedotmack/claude-mem

## What is claude-mem?
A persistent memory compression system for Claude Code that:
1. Automatically captures tool usage observations during coding sessions
2. Compresses them with AI into structured observations (title, type, source files, narrative)
3. Stores in SQLite with FTS5 full-text search
4. Injects relevant context back into future sessions via SessionStart hook
5. Provides MCP search tools (search, timeline, get_observations) with progressive disclosure

## Core Architecture (TypeScript original)
- **5 Lifecycle Hooks**: SessionStart (context), UserPromptSubmit (session-init), PostToolUse (observation), Stop (summarize), SessionEnd (session-complete)
- **Worker Service**: HTTP API on port 37777, manages SQLite DB, serves web viewer
- **MCP Server**: 7 tools (search, timeline, get_observations, smart_search, smart_unfold, smart_outline, __IMPORTANT)
- **SQLite Database**: sdk_sessions, observations, session_summaries tables with FTS5 virtual tables
- **AI Compression**: Tool outputs compressed into structured observations via Claude Agent SDK

## Goal
Implement equivalent functionality natively in Go within the pi-go codebase, leveraging existing:
- Agent infrastructure (internal/agent/)
- Tool system (internal/tools/)
- TUI (internal/tui/)
- Subagent system (internal/subagent/)
- Hook/plugin architecture

## Key Data Model
- **Observation**: id, memory_session_id, project, text, type (decision|bugfix|feature|refactor|discovery|change), title, concept, source_files, prompt_number, discovery_tokens, created_at
- **Session Summary**: id, memory_session_id, project, request, investigated, learned, completed, next_steps, created_at
- **SDK Session**: id, content_session_id, memory_session_id, project, user_prompt, started_at, completed_at, status

## 3-Layer Search Workflow
1. `search(query)` - Returns compact index with IDs (~50-100 tokens/result)
2. `timeline(anchor=ID)` - Get chronological context around results
3. `get_observations([IDs])` - Fetch full details for filtered IDs (~500-1000 tokens/result)
