# Build and Test Commands

## Source

Project root `Makefile` and repository layout.

## Build

Primary build target:

```bash
make build
```

This runs:

```bash
go build ./cmd/pi
go build ./cmd/pi-sandbox
```

## Unit tests

Primary test target:

```bash
make test
```

Alias target expands to:

```bash
make test-unit
```

which runs:

```bash
go test ./...
```

## Additional checks

Vet:

```bash
make vet
```

which runs:

```bash
go vet ./...
```

Lint:

```bash
make lint
```

which runs:

```bash
golangci-lint run ./...
```

## Notes relevant to planning

- `go.mod` currently does not list `github.com/coder/acp-go-sdk`.
- The repository already depends on `github.com/a2aproject/a2a-go/v2` and `github.com/modelcontextprotocol/go-sdk`.
- The build produces both `pi` and `pi-sandbox` binaries.
