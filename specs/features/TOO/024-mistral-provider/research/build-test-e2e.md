# Research: Build/Test/E2E & Caching Conventions

## 1. Makefile targets

`.PHONY`: build install test test-unit test-integration test-e2e test-all
test-coverage test-ollama check-cve sbom lint vet e2e clean sandbox-run sandbox-log
eval-run eval-pin eval-judge eval-tools eval-tools-judge hooks

| Target | Recipe |
|---|---|
| build (:21) | `go build -ldflags ... ./cmd/pi` + `go build ./cmd/pi-sandbox` |
| test (:73) | alias of test-unit |
| test-unit (:75) | `go test ./...` |
| test-integration (:78) | `go test -tags integration ./...` |
| test-e2e (:81) / e2e (:85) | `go test -tags e2e ./...` |
| test-all (:121) | test-unit + test-integration + test-e2e |
| test-coverage (:123) | `go test -coverprofile=coverage.out -coverpkg=./internal/... ./internal/... && go tool cover -func=coverage.out \| tail -1` |
| lint (:143) | `golangci-lint run ./...` |
| vet (:146) | `go vet ./...` |
| deps-accel (:59) | curl fetches prebuilt Rust tokenizers into `$HOME/.pi-go/lib` (make's only external fetch) |

**No `fetch-models`-like target exists.** Catalog update is manual (README-documented).

## 2. E2E gating

- Every e2e test file starts `//go:build e2e`; 15 files incl. 3 provider files.
- Skip pattern: `testGetMistralAPIKey` reads `MISTRAL_API_KEY`, `t.Skip("skipping:
  MISTRAL_API_KEY not set")` if empty (mistral_e2e_test.go:15-21). Same for
  OPENROUTER_API_KEY / XAI_API_KEY (+ skipIfXAIUnprovisioned for 403s).
- `make test-e2e` runs `go test -tags e2e ./...`.

## 3. CI (.github/workflows/ci.yml)

- Lint: golangci-lint v2.13.
- Test (Linux): `go test -count=1 ./...` + `go test -race -count=1 $(go list ./... | grep -v '/internal/acp/server$')`.
- Test (Windows): `go test -count=1 ./...`.
- Coverage: `go test -coverprofile=coverage.out -coverpkg=<all pkgs minus cmd/hack>` + Codecov.
- Build matrix: linux/windows/darwin × amd64/arm64 (no windows/arm64).
- **No `-tags e2e` anywhere in CI** — e2e never runs in CI.
- Release workflow: lint + `go test -race -count=1 ./...` + goreleaser.

## 4. .golangci.yml (version 2, go 1.27)

- Enabled: errcheck, govet (all minus fieldalignment/shadow), ineffassign,
  staticcheck (all), unused, bodyclose, copyloopvar, durationcheck, errname,
  errorlint, fatcontext, misspell, nilerr, revive, unconvert, wastedassign.
- Formatters: gofmt, goimports (local-prefix github.com/dimetron/pi-go).
- Exclusions: `tmp/`, `hack/`; _test.go relaxes errcheck/bodyclose/nilerr/SA5011.

## 5. Cache directory usage

**Neither `os.UserCacheDir` nor `os.UserConfigDir` used anywhere.** Project
convention is `$HOME/.pi-go` via `os.UserHomeDir()`:
- `~/.pi-go/llms-cache` (internal/tools/llms.go:147-153, content-addressed JSON).
- `~/.pi-go/sessions` (session_stats.go:101-105).
- `~/.pi-go/memory/claude-mem.db`, `~/.pi-go/palace.db` (session_sweep.go:293-322).
- `.pi-go/cache/read_image` (project-relative).
- `~/.pi-go/skills` + project `.pi-go/skills` (extension/skills.go:435-448).
- `make deps-accel` writes `$HOME/.pi-go/lib`.

## 6. External catalog fetch

**No Makefile target or script fetches a model catalog.** Embedded at compile time
via `//go:embed modeldata/*.json`. llm-prices files vendored from
github.com/simonw/llm-prices with blob SHAs in modeldata/README.md; update process
is manual (README): refresh → review IDs → update context-windows.json → go test.
