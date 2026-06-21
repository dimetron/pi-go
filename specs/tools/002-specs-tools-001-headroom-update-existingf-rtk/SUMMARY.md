# Run Summary

## Metadata

| Field    | Value                                                     |
|----------|-----------------------------------------------------------|
| Spec     | `tools/002-specs-tools-001-headroom-update-existingf-rtk` |
| Agent    | `task-1782079480136132000`                                |
| Outcome  | **merge_failed**                                          |
| Retries  | 0 / 10                                                    |
| Started  | 2026-06-22T00:04:40+02:00                                 |
| Duration | 10m27s                                                    |

## Gates

- **build** (`go build ./internal/tools/...`): **PASS**
- **test** (`go test ./internal/tools/... -v`): **PASS**
- **full-build** (`go build ./...`): **PASS**
- **full-test** (`go test ./...`): **PASS**
- **vet** (`go vet ./internal/tools/...`): **PASS**

All gates **passed**.

## Result

Gates passed but merge into the main branch failed. Worktree preserved for manual resolution.
