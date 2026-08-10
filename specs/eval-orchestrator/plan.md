# Plan — eval-orchestrator: standalone Go module

Three short steps, each producing a file under `specs/eval-orchestrator/artifacts/`.

## Progress

- [ ] Step 1: Create `specs/eval-orchestrator/artifacts/go.mod`
- [ ] Step 2: Create `specs/eval-orchestrator/artifacts/add.go` with `Add`
- [ ] Step 3: Create `specs/eval-orchestrator/artifacts/add_test.go` and make `go test ./...` pass

## Gates

- **test**: `cd specs/eval-orchestrator/artifacts && go test ./...`
- **vet**: `cd specs/eval-orchestrator/artifacts && go vet ./...`
