---
name: code-guidelines-go
description: >
  Go 1.24–1.27 coding guidelines for the dimetron/pi-go AI agent runtime.
  Use this skill whenever writing, reviewing, or refactoring ANY Go code in pi-go.
  This covers idiomatic style, error handling, concurrency, project layout, testing
  (table-driven, fuzz, benchmarks, synctest), new stdlib usage, golangci-lint v2
  configuration, slog logging, and manual dependency injection with functional
  options. Always consult this skill before generating Go code for pi-go, even for
  small snippets, to ensure every output is idiomatic and modern.
---

# Go 1.24–1.27 Coding Guidelines — pi-go

**Project**: `github.com/dimetron/pi-go` — Go-native AI coding agent runtime  
**Layout**: `cmd/pi/` · `internal/` · `.pi-go/` · `.vibe/compiled/` · `scripts/`  
**Min version**: Go 1.24 (tool directive support) | Target: Go 1.27

> For what Go 1.27 changed — generic methods, struct-literal field selectors,
> `encoding/json/v2`, the stdlib `uuid` package, removals that break a build, and the
> `GOEXPERIMENT=none` trap on this machine — see the **`go-127`** skill.

---

## 1. Style & Naming

### Packages
- Short, lowercase, singular, no underscores: `config` ✓ `agentUtils` ✗
- No stutter: package `agent` → `type Runner` (not `AgentRunner`)
- Never `util`, `common`, `types`, `helpers`

### Identifiers
- Receivers: 1–2 chars from type name, consistent across all methods, never `self`/`this`
- Acronyms full caps: `ID`, `URL`, `HTTP`, `LLM` — enforced by staticcheck
- Short names in small scopes (`i`, `ctx`, `err`), descriptive in large scopes

```go
// ✓ Correct
func (r *Runner) Execute(ctx context.Context) error { ... }
func (r *Runner) Stop()                             { ... }

// ✗ Wrong
func (self *Runner) Execute(ctx context.Context) error { ... }
func (runner *Runner) Stop()                           { ... }
```

### Interfaces
- Define at the **consumer**, not producer
- 1–3 methods; single-method uses `-er` suffix (`Reader`, `Handler`, `Closer`)
- Accept interfaces, return concrete structs

```go
// consumer defines what it needs (internal/agent/)
type LLMClient interface {
    Complete(ctx context.Context, prompt string) (string, error)
}

// producer returns concrete struct (internal/llm/)
func NewOpenAIClient(apiKey string) *OpenAIClient { ... }
```

### Godoc
Every exported name starts its comment with the name itself:
```go
// Package agent provides the core AI agent runtime for pi-go.
package agent

// Runner orchestrates tool calls and LLM interactions.
type Runner struct { ... }

// Execute runs the agent loop until completion or context cancellation.
func (r *Runner) Execute(ctx context.Context, task string) error { ... }
```

---

## 2. Idiomatic & Functional Style

### Guard clauses — early return, flat code
Avoid deep nesting. Return errors early, keep the happy path at the left edge:
```go
// ✓ Guard clauses
func (r *Runner) Start(ctx context.Context) error {
    if r.running {
        return ErrAlreadyRunning
    }
    if r.client == nil {
        return errors.New("nil client")
    }
    return r.loop(ctx)
}

// ✗ Nested conditionals
func (r *Runner) Start(ctx context.Context) error {
    if !r.running {
        if r.client != nil {
            return r.loop(ctx)
        }
        return errors.New("nil client")
    }
    return ErrAlreadyRunning
}
```

### Pure functions — isolate logic from I/O
Extract testable logic into pure functions that take inputs and return outputs.
Keep side effects (network, disk, logging) at the edges:
```go
// ✓ Pure — easy to test, no mocks needed
func buildPrompt(system string, history []Message, tools []Tool) string { ... }
func mergeToolResults(existing, incoming []ToolResult) []ToolResult { ... }
func selectModel(budget Budget, task Task) string { ... }

// Wire I/O at the caller
func (r *Runner) Execute(ctx context.Context, task string) error {
    prompt := buildPrompt(r.system, r.history, r.tools)  // pure
    resp, err := r.client.Complete(ctx, prompt)           // side effect
    ...
}
```

### Avoid init() — explicit initialization
`init()` hides execution order and makes testing harder. Wire everything in `main` or constructors:
```go
// ✗ Hidden global state
func init() { registry.Register("search", searchTool) }

// ✓ Explicit wiring
func NewRegistry(tools ...Tool) *Registry { ... }
```

### Value semantics — return values, don't mutate pointers
Prefer returning new values over mutating inputs. Makes data flow visible:
```go
// ✓ Value in, value out
func withDefaults(cfg Config) Config {
    if cfg.Timeout == 0 { cfg.Timeout = 30 * time.Second }
    if cfg.Model == "" { cfg.Model = "claude-sonnet-4-5-20250514" }
    return cfg
}

// ✗ Mutate in place — caller can't see what changed
func applyDefaults(cfg *Config) { ... }
```

### Function types as first-class values
Use named function types to simplify callback and middleware patterns:
```go
// Named function type — implements Handler implicitly
type ToolFunc func(ctx context.Context, input json.RawMessage) (string, error)

// Higher-order function — returns a decorated version
func WithTimeout(d time.Duration, fn ToolFunc) ToolFunc {
    return func(ctx context.Context, input json.RawMessage) (string, error) {
        ctx, cancel := context.WithTimeout(ctx, d)
        defer cancel()
        return fn(ctx, input)
    }
}
```

### Composition over flags — small composable pieces
Prefer combining simple functions over adding boolean parameters:
```go
// ✗ Boolean flags multiply code paths
func Send(msg Message, retry bool, validate bool) error { ... }

// ✓ Compose behaviors
func Send(msg Message) error { ... }
func WithRetry(n int, fn func(Message) error) func(Message) error { ... }
func WithValidation(fn func(Message) error) func(Message) error { ... }

// Usage: send := WithRetry(3, WithValidation(Send))
```

### No naked returns — always name what you return
```go
// ✗ Naked return — unclear what's being returned
func parse(s string) (result int, err error) {
    result, err = strconv.Atoi(s)
    return
}

// ✓ Explicit return values
func parse(s string) (int, error) {
    return strconv.Atoi(s)
}
```

---

## 3. Error Handling

**Rule**: Add context at every boundary. Return OR log an error — never both.

```go
func LoadConfig(path string) (*Config, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("read config %s: %w", path, err)
    }
    var cfg Config
    if err := json.Unmarshal(data, &cfg); err != nil {
        return nil, fmt.Errorf("parse config %s: %w", path, err)
    }
    return &cfg, nil
}
```

### errors.Is / errors.As / errors.AsType (Go 1.26)
```go
var ErrNotFound = errors.New("not found")   // sentinel — package-level var

// errors.Is — value matching through chain
if errors.Is(err, ErrNotFound) { ... }

// errors.AsType — generic, no pre-declaration needed (Go 1.26)
if pathErr, ok := errors.AsType[*os.PathError](err); ok {
    slog.Error("path error", "path", pathErr.Path)
}
```

### Concurrent errors
Use `errgroup` for first-error semantics; use `errors.Join` to collect all:
```go
g, ctx := errgroup.WithContext(ctx)
g.SetLimit(10)
for _, r := range records {
    g.Go(func() error { return process(ctx, r) })
}
return g.Wait()
```

---

## 4. Concurrency

### context.Context — always first parameter, never stored in struct
```go
// ✓ Correct
func (s *Service) Process(ctx context.Context, req Request) error {
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()
    return s.client.Call(ctx, req)
}
```

### Top-level context (main.go)
```go
ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer cancel()
if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
    slog.Error("fatal", "error", err)
    os.Exit(1)
}
```

### WaitGroup.Go replaces Add/Done (Go 1.25)
```go
var wg sync.WaitGroup
for _, item := range items {
    wg.Go(func() { processItem(item) }) // loop var capture safe since 1.22
}
wg.Wait()
```

### sync.OnceValue for lazy singletons (Go 1.21+)
```go
var getConfig = sync.OnceValue(func() *Config {
    cfg, err := loadConfig()
    if err != nil { panic(fmt.Sprintf("load config: %v", err)) }
    return cfg
})
// Usage: cfg := getConfig()
```

### Goroutine leak prevention
- Every goroutine must have an exit path (context cancel, channel close, done signal)
- In tests, use `goleak.VerifyTestMain(m)` in `TestMain`
- `goroutineleak` pprof profile — GA in Go 1.26 → 1.27, no GOEXPERIMENT needed.
  Scrape `/debug/pprof/goroutineleak`, or `pprof.Lookup("goroutineleak")`.
  It misses leaks whose primitive is reachable via a global or a runnable goroutine's
  locals, so keep `goleak` in tests. See `go-127` §3.2

---

## 5. Project Layout

```
pi-go/
├── cmd/pi/main.go           # Thin wiring only — no business logic
├── internal/
│   ├── agent/               # Core runner, tool dispatch
│   ├── config/              # Config loading
│   ├── llm/                 # LLM client implementations
│   └── tools/               # Tool registry
├── .pi-go/                  # Templates & runtime config
├── .vibe/compiled/          # Compiled vibe assets
├── scripts/                 # Build / dev scripts
├── go.mod
├── go.sum
├── Makefile
└── .golangci.yml
```

- No `pkg/` directory — this is a CLI app, not a library
- `main.go` only: parse config, wire deps, call `run(ctx)`

### Tool directives (Go 1.24) — replaces tools.go
```
// go.mod
tool (
    github.com/golangci/golangci-lint/cmd/golangci-lint
)
```
```bash
go tool golangci-lint run   # run tracked tool
go get -u tool              # upgrade all tools
```

### Embedding dot-prefixed directories
```go
//go:embed all:.pi-go          // "all:" required — dot-files excluded otherwise
var PiGoConfig embed.FS

//go:embed all:.vibe/compiled
var VibeCompiled embed.FS
```

### Makefile targets
```makefile
build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/pi ./cmd/pi/
test:
	go test -v -race -buildvcs ./...
lint:
	go tool golangci-lint run
audit: test lint
	go mod tidy -diff && go mod verify
```

---

## 6. Testing

### Table-driven tests with t.Run
```go
func TestParseDuration(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    time.Duration
        wantErr bool
    }{
        {"valid seconds", "30s", 30 * time.Second, false},
        {"empty string", "", 0, true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()                                     // always parallelise subtests
            got, err := ParseDuration(tt.input)
            if (err != nil) != tt.wantErr {
                t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
            }
            if diff := cmp.Diff(tt.want, got); diff != "" {
                t.Errorf("mismatch (-want +got):\n%s", diff)
            }
        })
    }
}
```
- Use `t.Helper()` as first line of every test helper
- `t.Context()` (Go 1.24) — auto-cancelled context, no manual setup needed
- Use `google/go-cmp` for struct comparison over `testify/assert`
- Package convention: `package foo_test` for API tests, `package foo` for white-box

### Fuzz testing (input parsing must be fuzzed)
```go
func FuzzParseCommand(f *testing.F) {
    f.Add("run task --model gpt-4")
    f.Add("")
    f.Fuzz(func(t *testing.T, input string) {
        cmd, err := ParseCommand(input)
        if err != nil { t.Skip() }
        if got := cmd.String(); got != input {
            if _, err2 := ParseCommand(got); err2 != nil {
                t.Errorf("round-trip failed: %v", err2)
            }
        }
    })
}
// Run: go test -fuzz=FuzzParseCommand -fuzztime=30s
// Failures auto-saved to testdata/fuzz/ as regression tests
```

### b.Loop() for benchmarks (Go 1.24) — replaces b.N
```go
func BenchmarkProcess(b *testing.B) {
    data := prepareData()          // setup excluded from timing automatically
    b.ReportAllocs()
    for b.Loop() {                 // no b.ResetTimer(), no sink var needed
        process(data)
    }
}
// Run: go test -bench=. -benchmem
// Compare: benchstat old.txt new.txt
```

### testing/synctest — deterministic concurrency tests (Go 1.24→1.25 stable)
Use `synctest.Test` for ANY test involving timers, tickers, timeouts, or context cancellation. Eliminates `time.Sleep` flakiness entirely:
```go
func TestAgentTimeout(t *testing.T) {
    synctest.Test(t, func(t *testing.T) {
        ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
        defer cancel()

        synctest.Sleep(5*time.Second - time.Nanosecond)  // sleep + Wait (Go 1.27)
        if ctx.Err() != nil {
            t.Fatal("should not have timed out yet")
        }
        synctest.Sleep(time.Nanosecond)
        if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
            t.Fatal("should have timed out")
        }
    })
}
```
> **Note**: `synctest.Run` was deprecated — use `synctest.Test` (Go 1.25+).  
> `synctest.Sleep(d)` (Go 1.27) replaces the `time.Sleep(d); synctest.Wait()` pair —
> prefer it inside a bubble, since a bare `time.Sleep` leaves the wake order between the
> test and the code under test unpredictable when both sleep the same duration.  
> `httptest.NewTestServer(t, h)` (Go 1.27) gives an in-memory HTTP server that runs in
> synthetic time inside a bubble and self-cleans — no `defer srv.Close()`.  
> `t.Attr(key, value)` (Go 1.25) emits structured metadata in test output.

---

## 7. Modern Stdlib — Use These Instead of External Libs

| Task | Old / External | ✓ Use Instead |
|------|---------------|--------------|
| Random numbers | `math/rand` | `math/rand/v2` |
| Slice sort/search | hand-written | `slices.Sort`, `slices.Contains` |
| Map utilities | hand-written | `maps.Keys`, `maps.Clone` |
| Default value | ternary logic | `cmp.Or(a, b, "default")` |
| Custom iteration | returning slices | `iter.Seq[V]`, `iter.Seq2[K,V]` |
| Lazy singleton | `sync.Once` + var | `sync.OnceValue` |
| Directory FS safety | path.Join | `os.Root` (prevents traversal) |
| Zero-value JSON omit | `omitempty` | `omitzero` tag |
| Split on last separator | `strings.LastIndex` + slicing | `strings.CutLast` / `bytes.CutLast` (1.27) |
| UUIDs | `github.com/google/uuid` | stdlib `uuid` (1.27); `uuid.NewV7()` for keys |

```go
// iter.Seq2 for lazy DB rows
func (db *DB) Rows(ctx context.Context) iter.Seq2[Row, error] {
    return func(yield func(Row, error) bool) {
        rows, _ := db.Query(ctx)
        defer rows.Close()
        for rows.Next() {
            var r Row; rows.Scan(&r)
            if !yield(r, nil) { return }
        }
    }
}

// slices + cmp replacing manual loops
slices.SortFunc(users, func(a, b User) int { return cmp.Compare(a.Name, b.Name) })
sortedKeys := slices.Sorted(maps.Keys(m))
port := cmp.Or(envPort, flagPort, "8080")
```

### Go 1.24 new packages (use where relevant)
- `weak.Make(obj)` — weak pointer for memory-efficient caches
- `unique.Make(v)` — value interning for fast equality
- `crypto/mlkem`, `crypto/hkdf`, `crypto/pbkdf2`, `crypto/sha3` — prefer over external
- `os.Root` — scoped filesystem access, prevents path traversal

### Go 1.26 syntax
```go
// new(expr) — eliminates ptr() helper boilerplate
timeout := new(30 * time.Second)   // *time.Duration pointing to 30s
enabled := new(true)               // *bool

// errors.AsType — already shown in §2
```

### Go 1.27 syntax
```go
// Generic methods — a generic operation can now live on the type it belongs to.
// Restriction: interfaces cannot declare them, and they cannot satisfy an interface
// method — so keep the package-level generic func wherever the call site is an
// interface (which, per §1, is most of pi-go).
func (b Box[T]) Map[U any](f func(T) U) Box[U] { return Box[U]{v: f(b.v)} }

// Struct literal keys may be any valid field selector — promoted fields included
u := User{ID: 7, Name: "Mittens"}          // was User{Base: Base{ID: 7}, Name: ...}

// Function type inference now applies in conversions and composite literals too
ops := []func([]int) int{first, last}      // was {first[int], last[int]}
```
Full detail, including removals that break a build: **`go-127`** skill.

---

## 8. golangci-lint v2 — Mandatory Gate

**All Go code MUST pass `golangci-lint run` before commit.** Run it after every change:
```bash
golangci-lint run ./...          # check all packages
golangci-lint run ./internal/... # check specific subtree
```

If a linter fires, fix the code — do not add `//nolint` without a comment explaining why the suppression is necessary.

### Active linters (from `.golangci.yml`)

| Category | Linters |
|----------|---------|
| **Correctness** | `errcheck`, `govet`, `staticcheck`, `unused`, `ineffassign` |
| **Style & bugs** | `bodyclose`, `copyloopvar`, `durationcheck`, `errname`, `errorlint`, `fatcontext`, `misspell`, `nilerr`, `revive`, `unconvert`, `wastedassign` |
| **Formatters** | `gofmt`, `goimports` (local prefix: `github.com/dimetron/pi-go`) |

### Key settings to be aware of

- **errcheck**: `check-type-assertions: true` — always handle type assertion ok values
- **govet**: all analyzers enabled except `fieldalignment` and `shadow`
- **revive**: enforces `indent-error-flow` (guard clauses), `receiver-naming`, `error-strings`, `superfluous-else`, `empty-block`
- **staticcheck**: all checks enabled (ST1000 package comments excluded for internal)
- **misspell**: US locale

### Exclusions

- **Test files** (`_test.go`): `errcheck`, `bodyclose`, `nilerr` relaxed
- **`internal/tools/`**: `nilerr` relaxed (errors returned inside result structs)
- **`internal/(lsp|cli|memory|tui)/`**: `nilerr` relaxed (callback wrappers)
- **`research/`**: most linters disabled (experimental code)

### Import ordering (enforced by goimports)
```go
import (
    // 1. stdlib
    "context"
    "fmt"

    // 2. third-party
    "github.com/charmbracelet/bubbletea"

    // 3. local (auto-grouped by goimports local-prefixes)
    "github.com/dimetron/pi-go/internal/config"
)
```

### CI (GitHub Actions)
```yaml
lint:
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v5
    - uses: actions/setup-go@v6
      with: { go-version: '1.27' }
    - uses: golangci/golangci-lint-action@v9
      with: { version: v2 }

test:
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v5
    - uses: actions/setup-go@v6
      with: { go-version: '1.27' }
    - run: go test -v -race -coverprofile=coverage.out ./...
    - run: go mod tidy -diff && go mod verify
```

---

## 9. Logging — log/slog (stdlib only)

**Decision**: `log/slog` for pi-go. Zero deps, swappable handler, context-aware.  
If throughput ever matters: swap backend to `zapslog.NewHandler()` — no call-site changes.

```go
// internal/logging/logging.go
func NewLogger(level slog.Level, jsonOutput bool) *slog.Logger {
    opts := &slog.HandlerOptions{Level: level, AddSource: level <= slog.LevelDebug}
    var h slog.Handler
    if jsonOutput {
        h = slog.NewJSONHandler(os.Stderr, opts)  // production
    } else {
        h = slog.NewTextHandler(os.Stderr, opts)  // development
    }
    return slog.New(h)
}
```

- **Never** use `log` (stdlib old), `fmt.Println`, or init a global `zap.Logger` — `depguard` will catch it
- Pass logger via constructor, not context (context logging = middleware only)
- Use `.With()` for component-scoped fields
- **Go 1.26**: `slog.NewMultiHandler(h1, h2)` for fan-out

```go
type Runner struct { logger *slog.Logger }

func NewRunner(client LLMClient, logger *slog.Logger) *Runner {
    return &Runner{logger: logger.With("component", "runner")}
}

// In methods
r.logger.Info("executing task", slog.String("task", task), slog.Int("iter", n))
r.logger.Error("tool failed", slog.Any("error", err), slog.String("tool", name))
```

---

## 10. Dependency Injection — Manual + Functional Options

No Wire, no fx. Manual constructor injection wired in `cmd/pi/main.go`.

### Functional options for configurable constructors
```go
type clientConfig struct {
    model   string
    timeout time.Duration
}

type Option func(*clientConfig)

func WithModel(m string) Option        { return func(c *clientConfig) { c.model = m } }
func WithTimeout(d time.Duration) Option { return func(c *clientConfig) { c.timeout = d } }

func NewOpenAIClient(apiKey string, opts ...Option) *OpenAIClient {
    cfg := &clientConfig{model: "gpt-4o", timeout: 30 * time.Second}
    for _, o := range opts { o(cfg) }
    return &OpenAIClient{apiKey: apiKey, cfg: cfg}
}
```

### Composition root (cmd/pi/main.go)
```go
func run(ctx context.Context) error {
    cfg := config.Load()
    logger := logging.NewLogger(cfg.LogLevel, cfg.JSONOutput)
    slog.SetDefault(logger)

    llmClient := llm.NewOpenAIClient(cfg.APIKey,
        llm.WithModel(cfg.Model),
        llm.WithTimeout(cfg.Timeout),
    )
    registry := tools.NewRegistry(logger, tools.WithBuiltins())
    runner := agent.NewRunner(llmClient, registry, logger,
        agent.WithMaxIterations(cfg.MaxIterations),
    )
    return runner.Run(ctx, os.Args[1:])
}
```

### Interface-based test mocks — no mock framework needed
```go
type mockLLM struct{ response string; err error }
func (m *mockLLM) Complete(_ context.Context, _ string) (string, error) {
    return m.response, m.err
}

func TestRunner_Execute(t *testing.T) {
    r := agent.NewRunner(&mockLLM{response: "ok"}, nil, slog.Default())
    if err := r.Execute(t.Context(), "task"); err != nil {
        t.Fatalf("unexpected: %v", err)
    }
}
```

---

## Quick Reference

| Concern | Use | Avoid |
|---------|-----|-------|
| Control flow | Guard clauses, early return | Deep nesting |
| Logic | Pure functions, value in/out | Mutating pointer args |
| Composition | Small functions + higher-order combinators | Boolean flag parameters |
| Init | Explicit constructors | `init()` functions |
| Returns | Explicit return values | Naked returns |
| Lint gate | `golangci-lint run` before commit | `//nolint` without justification |
| Imports | stdlib / third-party / local (3 groups) | Mixed or unsorted imports |
| Logging | `log/slog` | `log`, `zap`, `zerolog` |
| DI | manual constructors + functional options | Wire, fx |
| Error wrapping | `fmt.Errorf("...: %w", err)` | `%v` when caller needs to unwrap |
| Error type check | `errors.AsType[T]` (1.26) / `errors.As` | type assertions |
| Benchmarks | `b.Loop()` | `for i := 0; i < b.N; i++` |
| Concurrent tests | `synctest.Test` + `synctest.Sleep` (1.27) | `time.Sleep` in tests |
| HTTP test server | `httptest.NewTestServer(t, h)` (1.27) | `httptest.NewServer` + `defer Close()` |
| Split on last separator | `strings.CutLast` (1.27) | `strings.LastIndex` + slicing |
| UUIDs | stdlib `uuid` (1.27) | `github.com/google/uuid` |
| WaitGroup | `wg.Go(func(){...})` (1.25) | `wg.Add(1); defer wg.Done()` |
| Slice ops | `slices.*`, `maps.*` | hand-written loops |
| Randomness | `math/rand/v2` | `math/rand` |
| Tool deps | `go.mod tool directive` (1.24) | `tools.go` blank imports |
