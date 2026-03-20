# Research: pi-go SQLite & Storage

## Current State: No SQLite

- Zero SQLite usage in the active codebase
- No SQLite library in go.mod
- Only database dependency: `jackc/pgx/v5` (PostgreSQL) in research/fractal-rag prototype

## Current Storage: File-Based JSONL

`internal/session/store.go` — `FileService`:

```
~/.pi-go/sessions/<session-uuid>/
  meta.json          — ID, AppName, UserID, WorkDir, Model, CreatedAt, UpdatedAt
  events.jsonl       — append-only event log
  branches.json      — branch head pointers
  branches/<name>/events.jsonl  — per-branch snapshots
```

- In-memory cache (`map[string]*fileSession`) with `sync.RWMutex`
- Compaction: rewrites events.jsonl atomically (write to .tmp, then os.Rename)
- No key-value stores (bbolt, badger, etc.)

## Recommended SQLite Library

`modernc.org/sqlite` — pure Go, no CGO, fits pi-go's dependency profile (no existing CGO deps).

## Key Design Decisions for claude-mem

1. New SQLite database at `~/.pi-go/memory/claude-mem.db`
2. WAL mode for concurrent read/write
3. FTS5 virtual tables for full-text search
4. Migration system (simple version table + sequential migrations)
5. Separate from session storage — memory DB is cross-session
