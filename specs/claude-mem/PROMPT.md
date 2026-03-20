# Implement native claude-mem memory system in pi-go

## Objective

Implement a persistent memory compression system natively in Go within the pi-go codebase. The system captures tool usage observations during coding sessions, compresses them with AI into structured observations, stores them in SQLite with FTS5 full-text search, and injects relevant context into future sessions.

## Key Requirements

1. **New package `internal/memory/`** with: `db.go`, `types.go`, `store.go`, `search.go`, `privacy.go`, `worker.go`, `compress.go`, `context.go`
2. **SQLite database** at `~/.pi-go/memory/claude-mem.db` using `modernc.org/sqlite` (pure Go, no CGO). WAL mode, FTS5 virtual tables, migration system.
3. **Three tables**: `sessions`, `observations` (+ FTS5), `session_summaries` (+ FTS5) with sync triggers
4. **AfterToolCallback** in `cli.go` that enqueues `RawObservation` to a buffered channel (cap 100). Must be non-blocking — never add latency to the main agent loop.
5. **Background worker goroutine** drains channel, applies `<private>` tag stripping, calls compression subagent, stores structured observations
6. **Bundled subagent** `internal/subagent/bundled/memory-compressor.md` (role: smol, no tools, timeout: 30s) that accepts raw tool data as JSON and returns structured `{title, type, text, source_files}`
7. **Context injection** at session start: `ContextGenerator.Generate()` builds markdown from recent observations (72h lookback, 8K token budget), appended to system instruction
8. **Three native tools** registered in `CoreTools()`:
   - `mem-search(query, project, type, limit)` — FTS5 search returning compact index with IDs
   - `mem-timeline(anchor, depth_before, depth_after)` — chronological context window
   - `mem-get(ids)` — batch fetch full observation details
9. **Config** in `internal/config/config.go`: `MemoryConfig` struct with enabled, db_path, token_budget, compression_model_role, max_pending, lookback_hours, excluded_tools, excluded_projects
10. **Graceful degradation**: memory system failure never blocks the main agent. Channel full → drop + log. Compression fails → store raw fallback. SQLite unavailable → disable memory for session.

## Acceptance Criteria

Given the memory system is enabled and the agent executes a tool
When the AfterToolCallback fires
Then a RawObservation is enqueued non-blocking within 1ms

Given observations exist for the current project
When a new session starts
Then recent observations are formatted as markdown and injected into the system instruction

Given observations with text matching a query exist
When mem-search is called with that query
Then matching observations are returned as a compact table with IDs, titles, types

Given tool output contains `<private>...</private>` tags
When the observation is processed
Then private content is replaced with `[PRIVATE]` before storage

Given a session with observations is ending
When shutdown fires
Then pending observations are drained (5s timeout) and a session summary is generated

## Reference

- Design: `specs/claude-mem/design.md`
- Plan: `specs/claude-mem/plan.md` (10 incremental steps)
- Research: `specs/claude-mem/research/` (5 files)
- Architecture: `ARCHITECTURE.md` (Memory System section)
- Source inspiration: https://github.com/thedotmack/claude-mem
