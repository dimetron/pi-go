---
name: bubbletea-testing
description: Use this skill whenever writing tests for Bubble Tea (charmbracelet/bubbletea) TUI applications in Go. Triggers include any mention of testing Bubble Tea models, teatest, golden file testing for TUIs, testing tea.Cmd or tea.Msg, snapshot testing terminal output, or writing tests for any Go CLI/TUI that uses the Elm Architecture (Init/Update/View). Also use when the user asks about testing bubbletea components, bubbles, or lipgloss-styled views, or when they need CI-friendly TUI test patterns. Even if they just say "test my TUI" or "add tests to my Bubble Tea app", use this skill.
---

# Bubble Tea Testing

Write robust, CI-friendly tests for Bubble Tea TUI applications using a three-layer strategy: direct model unit tests, golden file view snapshots, and full-program integration tests via `teatest`.

## Architecture overview

Bubble Tea's Elm Architecture (`Init`, `Update`, `View`) makes TUI apps inherently testable. `Update(msg) -> (model, cmd)` is a pure function of state and message — no terminal, program, or event loop needed for most tests.

**Three-layer strategy:**

| Layer | Coverage | Speed | Tool |
|-------|----------|-------|------|
| 1. Direct model tests | State transitions, commands, view content | ~ms | Standard `testing` |
| 2. Golden file snapshots | Visual regression on `View()` output | ~ms | `golden.RequireEqual` |
| 3. Full integration | End-to-end user flows | ~seconds | `teatest.NewTestModel` |

Target ratio: **80% Layer 1 / 15% Layer 2 / 5% Layer 3**.

---

## Layer 1: Direct model unit tests

### Constructing test messages

Build `tea.Msg` values directly — they are plain Go structs:

```go
// v1
qKey   := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}
enter  := tea.KeyMsg{Type: tea.KeyEnter}
ctrlC  := tea.KeyMsg{Type: tea.KeyCtrlC}
down   := tea.KeyMsg{Type: tea.KeyDown}
resize := tea.WindowSizeMsg{Width: 80, Height: 24}

// v2 renames
qKey   := tea.KeyPressMsg{Type: tea.KeyRunes, Runes: []rune("q")}
click  := tea.MouseClickMsg{X: 10, Y: 5, Button: tea.MouseButtonLeft}
```

### Table-driven Update tests

The standard pattern — each case specifies initial state, message, and expected outcome:

```go
func TestUpdate(t *testing.T) {
    tests := []struct {
        name       string
        initial    model
        msg        tea.Msg
        wantCursor int
        wantQuit   bool
    }{
        {
            name:       "down moves cursor",
            initial:    model{cursor: 0, choices: []string{"a", "b", "c"}},
            msg:        tea.KeyMsg{Type: tea.KeyDown},
            wantCursor: 1,
        },
        {
            name:       "cursor stops at bottom",
            initial:    model{cursor: 2, choices: []string{"a", "b", "c"}},
            msg:        tea.KeyMsg{Type: tea.KeyDown},
            wantCursor: 2,
        },
        {
            name:       "q triggers quit",
            initial:    model{},
            msg:        tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")},
            wantQuit:   true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            updated, cmd := tt.initial.Update(tt.msg)
            m := updated.(model)

            if m.cursor != tt.wantCursor {
                t.Errorf("cursor = %d, want %d", m.cursor, tt.wantCursor)
            }
            if tt.wantQuit {
                if cmd == nil {
                    t.Fatal("expected quit command")
                }
                if _, ok := cmd().(tea.QuitMsg); !ok {
                    t.Error("quit command did not return QuitMsg")
                }
            }
        })
    }
}
```

### Testing commands synchronously

`tea.Cmd` is `func() tea.Msg`. Execute it directly and inspect the result:

```go
func TestQuitCommand(t *testing.T) {
    m := model{}
    _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})

    if cmd == nil {
        t.Fatal("expected quit command")
    }
    msg := cmd() // execute synchronously
    if _, ok := msg.(tea.QuitMsg); !ok {
        t.Errorf("expected QuitMsg, got %T", msg)
    }
}
```

### Mocking I/O dependencies

Use interfaces for anything that does real I/O, inject mocks in tests:

```go
type DataFetcher interface {
    FetchItems() ([]Item, error)
}

type model struct {
    fetcher DataFetcher
    items   []Item
}

// Test mock
type mockFetcher struct {
    items []Item
    err   error
}
func (f mockFetcher) FetchItems() ([]Item, error) { return f.items, f.err }

func TestFetchSuccess(t *testing.T) {
    m := model{fetcher: mockFetcher{items: []Item{{Name: "test"}}}}
    cmd := m.fetchCmd()
    msg := cmd()
    result, ok := msg.(itemsMsg)
    if !ok {
        t.Fatalf("expected itemsMsg, got %T", msg)
    }
    if len(result.items) != 1 {
        t.Errorf("expected 1 item, got %d", len(result.items))
    }
}
```

### Chaining update–command–message cycles

Simulate multi-step flows without a running program:

```go
func TestMultiStepFlow(t *testing.T) {
    m := tea.Model(initialModel())
    var cmd tea.Cmd

    // Step 1: user presses enter
    m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

    // Step 2: execute the resulting command, feed msg back
    if cmd != nil {
        m, cmd = m.Update(cmd())
    }

    // Assert final state
    final := m.(myModel)
    if final.state != resultView {
        t.Errorf("expected resultView, got %v", final.state)
    }
}
```

### Testing View output with substring assertions

Prefer substring checks over exact matches — more resilient to styling changes:

```go
func TestViewShowsSelection(t *testing.T) {
    m := model{
        cursor:   1,
        choices:  []string{"carrots", "celery", "kohlrabi"},
        selected: map[int]struct{}{1: {}},
    }
    view := m.View()

    if !strings.Contains(view, "[x] celery") {
        t.Errorf("expected selected celery in view:\n%s", view)
    }
    if !strings.Contains(view, "[ ] carrots") {
        t.Errorf("expected unselected carrots in view:\n%s", view)
    }
}
```

**Principle:** Assert on intent (flags, indices, content), not styling.

### Testing nested/composed models

Test parent routing and child transitions independently:

```go
func TestParentRoutesToActiveChild(t *testing.T) {
    parent := newParentModel()
    updated, _ := parent.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
    p := updated.(parentModel)

    child := p.list.(listModel)
    if child.width != 80 {
        t.Errorf("child width = %d, want 80", child.width)
    }
}
```

---

## Layer 2: Golden file testing

### Packages

| Package | Import | Use case |
|---------|--------|----------|
| `golden` | `github.com/charmbracelet/x/exp/golden` | Component-level View snapshots |
| `teatest` | `github.com/charmbracelet/x/exp/teatest` | Full-program output snapshots |

### Component snapshot with `golden.RequireEqual`

```go
import "github.com/charmbracelet/x/exp/golden"

func TestTableRendering(t *testing.T) {
    tbl := table.New(
        table.WithColumns(columns),
        table.WithRows(rows),
    )
    // Compares against testdata/TestTableRendering.golden
    golden.RequireEqual(t, tbl.View())
}
```

`golden.RequireEqual` auto-escapes ANSI codes and uses `go-udiff` for portable diffs.

### Full-program snapshot with `teatest.RequireEqualOutput`

```go
func TestFullOutput(t *testing.T) {
    tm := teatest.NewTestModel(t, initialModel(),
        teatest.WithInitialTermSize(80, 24),
    )
    tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})

    out, _ := io.ReadAll(tm.FinalOutput(t, teatest.WithFinalTimeout(3*time.Second)))
    // Compares against testdata/TestFullOutput.golden
    teatest.RequireEqualOutput(t, out)
}
```

### Golden file workflow

```bash
# 1. Generate initial golden files
go test ./... -update

# 2. Commit them
git add testdata/*.golden

# 3. Normal test runs — fails if output differs
go test ./...

# 4. After intentional UI changes — regenerate and review diff
go test ./... -update
git diff testdata/
```

---

## Layer 3: Full integration with teatest

### Core API

```go
import (
    "bytes"
    "io"
    "testing"
    "time"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/x/exp/teatest"
)

func TestIntegration(t *testing.T) {
    tm := teatest.NewTestModel(t, initialModel(),
        teatest.WithInitialTermSize(80, 24),
    )

    // Interact
    tm.Send(tea.KeyMsg{Type: tea.KeyDown})
    tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
    tm.Type("hello")

    // Assert intermediate output (polls reader)
    teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
        return bytes.Contains(bts, []byte("hello"))
    }, teatest.WithDuration(2*time.Second),
       teatest.WithCheckInterval(100*time.Millisecond))

    // Quit and assert final state
    tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
    fm := tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second))
    m := fm.(myModel)
    if !m.submitted {
        t.Error("expected submitted")
    }
}
```

### teatest API reference

| Method | Purpose | Blocks? |
|--------|---------|---------|
| `NewTestModel(tb, model, opts...)` | Create & start headless program | No |
| `tm.Send(msg)` | Inject any `tea.Msg` | No |
| `tm.Type(s)` | Type string as key events | No |
| `tm.Output()` | Live output `io.Reader` | No |
| `tm.FinalOutput(tb, opts...)` | Complete output after quit | Yes |
| `tm.FinalModel(tb, opts...)` | Final `tea.Model` after quit | Yes |
| `tm.WaitFinished(tb, opts...)` | Block until program exits | Yes |
| `WaitFor(tb, reader, cond, opts...)` | Poll reader until condition true | Yes |
| `RequireEqualOutput(tb, out)` | Golden file comparison | No |

**Always set timeouts** via `WithFinalTimeout` to prevent hanging tests.

---

## CI determinism — the three critical fixes

### 1. Force a fixed color profile

Without this, golden files from a TrueColor dev terminal will mismatch in CI (no TTY).

```go
// v1 — in test init or TestMain
import (
    "github.com/charmbracelet/lipgloss"
    "github.com/muesli/termenv"
)
func init() {
    lipgloss.SetColorProfile(termenv.Ascii)
}

// v2 — per-program
import "github.com/charmbracelet/colorprofile"
prog := tea.NewProgram(model, tea.WithColorProfile(colorprofile.Ascii))
```

Use `termenv.Ascii` for simplest golden files. Use `termenv.TrueColor` if testing color output.

### 2. Lock terminal dimensions

```go
// teatest
teatest.WithInitialTermSize(80, 24)

// Direct model tests — send resize before View()
m, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
output := m.(myModel).View()

// v2
tea.WithWindowSize(80, 24)
```

### 3. Prevent git from corrupting golden files

Add to `.gitattributes`:

```
*.golden -text
testdata/** -diff linguist-generated=true
```

Prevents CRLF normalization and suppresses golden files from GitHub PR diffs.

### Handle non-deterministic elements

Spinners, timestamps, cursor blink, and animations produce varying output. Strategies:

- Freeze spinner frame index to 0 in test setup
- Inject a clock interface for timestamps, use fixed `time.Time` in tests
- Disable cursor blink before capture
- Seed RNGs with constant values
- For animations, test the final state rather than intermediate frames

---

## Bubble Tea v2 testing options

v2 (`charm.land/bubbletea/v2`) adds first-class `ProgramOption` values for testing:

```go
prog := tea.NewProgram(model,
    tea.WithWindowSize(80, 24),           // Fixed dimensions
    tea.WithInput(nil),                   // Disable input
    tea.WithOutput(&buf),                 // Redirect to buffer
    tea.WithoutRenderer(),                // Headless, no rendering
    tea.WithoutSignals(),                 // Ignore OS signals
    tea.WithColorProfile(colorprofile.Ascii), // Fixed colors
)
```

These replace global `init()` hacks with explicit per-program config.

The v2 teatest package is at `github.com/charmbracelet/x/exp/teatest/v2`.

---

## Community tools

### knz/catwalk — data-driven text-file tests

Test cases as plain text files with input directives:

```
run
type hello
key enter
----
-- view:
You typed: hello
```

Run with `-rewrite` to regenerate expected output. Good for testing individual Bubbles components.

**Repo:** `github.com/knz/catwalk`

### Custom direct-model harness (Noteleaf pattern)

Drive `tea.Model` directly without `tea.NewProgram` for single-threaded, faster tests:

```go
type TestHarness struct {
    model tea.Model
}

func (h *TestHarness) SendKey(key tea.KeyType) {
    var cmd tea.Cmd
    h.model, cmd = h.model.Update(tea.KeyMsg{Type: key})
    // Optionally execute cmd and feed back
}

func (h *TestHarness) WaitForView(contains string, timeout time.Duration) error {
    deadline := time.Now().Add(timeout)
    for time.Now().Before(deadline) {
        if strings.Contains(h.model.View(), contains) {
            return nil
        }
        time.Sleep(10 * time.Millisecond)
    }
    return fmt.Errorf("timed out waiting for %q", contains)
}
```

Lighter than teatest, but doesn't test the terminal rendering pipeline.

---

## Checklist for adding tests to a Bubble Tea app

1. **Set up test infrastructure:**
   - Create `testdata/` directory for golden files
   - Add `.gitattributes` entry: `*.golden -text`
   - Add `init()` or `TestMain` that sets `lipgloss.SetColorProfile(termenv.Ascii)`

2. **Layer 1 — Write table-driven Update tests for:**
   - Every key binding and its effect on model state
   - State machine transitions (view switches, mode changes)
   - Edge cases (empty lists, max cursor, error states)
   - Commands returned by Update (execute synchronously, assert on msg type)
   - Custom message handlers (API responses, timer ticks)

3. **Layer 2 — Add golden file tests for key UI states:**
   - Initial/welcome screen
   - Loading/spinner state
   - Error display
   - Main content with data populated
   - Empty state

4. **Layer 3 — Write integration tests for critical flows:**
   - Startup → input → result → quit
   - Error recovery paths
   - Multi-step wizards or workflows

5. **CI pipeline:**
   - Ensure `go test ./...` passes without `-update`
   - Add `-update` as a manual/explicit step only
   - Consider a CI step that fails if golden files are uncommitted
