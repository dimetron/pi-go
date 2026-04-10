# Research: pi-go Session Management

## Session ID

- Generated at `internal/session/store.go:61` via `uuid.NewString()` (UUID v4)
- Caller may supply own ID for resume/continue flows
- CLI resolution (`internal/cli/cli.go:345-360`):
  - `--session <id>`: resume specific session
  - `--continue`: find last session via `LastSessionID(appName, userID)`
  - Neither: create fresh UUID

## Session Lifecycle (Implicit)

No explicit state machine. Lifecycle is implicit:

| Phase | Mechanism |
|---|---|
| Start | `FileService.Create()` — creates meta.json + empty events.jsonl |
| Running | `FileService.AppendEvent()` — appends to events.jsonl, updates meta.json |
| End | Implicit — no close call. Logger file closed with `defer sessionLog.Close()` |

## Storage Layout
```
~/.pi-go/sessions/<uuid>/
  meta.json        — {id, appName, userID, workDir, model, createdAt, updatedAt}
  events.jsonl     — one JSON event per line (append-only)
  branches.json    — branch state
```

## TUI Session Tracking

- `tui.Config.SessionID` — set at construction, immutable for entire session
- Used for: `/session` display, branch ops, `/compact`, agent turns
- Every turn: `m.cfg.Agent.RunStreaming(m.ctx, m.cfg.SessionID, prompt)`

## Mapping to claude-mem Concepts

| claude-mem | pi-go |
|---|---|
| content_session_id | `sessionID` (UUID from FileService) |
| memory_session_id | New concept — could be same UUID or derived |
| project | `meta.WorkDir` (cwd at session start) |
| user_prompt | First user message in the turn |
| started_at | `meta.CreatedAt` |
| completed_at | `meta.UpdatedAt` (or explicit end event) |
| status | Derive from session existence + events |

## Key Decision
We can reuse pi-go's existing session UUID as `content_session_id`. A separate `memory_session_id` may not be needed — the original used it because the TypeScript version had a separate SDK session for AI compression. In Go, if compression runs in-process, one ID suffices.
