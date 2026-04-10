# Run Summary

## Metadata

| Field | Value |
|-------|-------|
| Spec | `research-coding-agents-session-log-optimizations` |
| Agent | `task-1773963391765741000` |
| Outcome | **merge_failed** |
| Retries | 1 / 10 |
| Started | 2026-03-20T00:26:23+01:00 |
| Duration | 14m12s |

## Gates

Gates were defined but not executed.

- **build**: `go build ./...`
- **test**: `go test ./internal/tools/... ./internal/tui/...`
- **vet**: `go vet ./...`
- **race**: `go test -race ./internal/tools/... ./internal/tui/...`
- **docs**: `go doc ./internal/tools/...`

## Result

Gates passed but merge into the main branch failed. Worktree preserved for manual resolution.
