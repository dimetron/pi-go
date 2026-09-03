# coder/acp-go-sdk (vendored, patched)

This is a local copy of `github.com/coder/acp-go-sdk@v0.13.5`, wired in via
the `replace` directive in the root `go.mod`. It exists because upstream
(v0.13.5 and `main`) has a **data race**: `NewConnection` spawns the
receive/processNotifications goroutines in its constructor, and `SetLogger`
writes the `logger` field afterwards with no synchronization — so the goroutines
which call `loggerOrDefault()` race against any later `SetLogger` call.

That race aborts `go test -race` (which CI runs) in every package that connects
an ACP subprocess (agy, claudecode, copilot, cursor, gemini, client).

- The `version` file and `VERSION` marker from upstream are intentionally omitted:
  a global `**/VERSION` gitignore rule ignores them, they are not embedded by 
  the Go build, and they carry no runtime functionality.

## The patch

Only `connection.go` differs from upstream. The `logger` field was changed from
a bare `*slog.Logger` to an `atomic.Pointer[slog.Logger]`:

```go
logger atomic.Pointer[slog.Logger]

func (c *Connection) SetLogger(l *slog.Logger) { c.logger.Store(l) }

func (c *Connection) loggerOrDefault() *slog.Logger {
    if l := c.logger.Load(); l != nil {
        return l
    }
    return slog.Default()
}
```

A single lock-free atomic load covers the hot `loggerOrDefault()` read path with
no mutex contention or lock-reentrancy risk against the connection's other
mutexes, and the pointer is independently replaced, so an atomic is the correct
tool.

## Origin

- Upstream: <https://github.com/coder/acp-go-sdk> @ `v0.13.5`
- Upstream issue for this race: [`coder/acp-go-sdk#57`](https://github.com/coder/acp-go-sdk/issues/57)
  (filed alongside this vendored copy)
- Copied from the Go module cache
  (`$GOMODCACHE/github.com/coder/acp-go-sdk@v0.13.5`), `_test.go` files,
  `example/`, `testdata/`, and the upstream `version` release marker removed,
  LICENSE retained.

## How to drop this when upstream fixes it

When a release that carries the atomic logger (or a constructor logger option)
lands:

1. `go get github.com/coder/acp-go-sdk@v<new>`
2. Remove the `replace` line from the root `go.mod`.
3. Delete `third_party/acp-go-sdk/`.
