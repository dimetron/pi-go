# Run Summary

## Metadata

| Field    | Value                               |
|----------|-------------------------------------|
| Spec     | `features/TOO/007-adk-2-0-adoption` |
| Agent    | `task-1783468321877811000`          |
| Outcome  | **merge_failed**                    |
| Retries  | 1 / 10                              |
| Started  | 2026-07-08T01:40:06+02:00           |
| Duration | 20m26s                              |

## Gates

Gates were defined but not executed.

- **build**: `go build ./...`
- **test**: `make test`
- **race**: `go test -race ./...`
- **e2e**: `make test-e2e`
- **lint**: `make lint`
- **vet**: `make vet`
- **coverage**: `make test-coverage`
- **mod-tidy**: `go mod tidy`
- **audit**: `pi audit`

## Result

Gates passed but merge into the main branch failed. Worktree preserved for manual resolution.
