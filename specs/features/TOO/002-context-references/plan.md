# Context References - Implementation Plan

## Objective

Implement a Context References feature for pi-go that allows users to inject file, folder, git, and web content into
messages using `@ref` syntax in TUI mode.

---

## Slices

### Slice 1: Reference Parser

- [x] **Create `internal/tui/refs/parser.go`**
    - Define `RefType` constants (`RefFile`, `RefFolder`, `RefDiff`, `RefStaged`, `RefGit`, `RefURL`)
    - Define `ParsedRef` struct with Type, RawValue, Value, LineRange
    - Implement `parseRefs(input string) []ParsedRef` — regex-based parsing of `@type:value` patterns
    - Handle trailing punctuation stripping (`,`, `.`, `;`, `!`, `?`)
    - Handle line range parsing for `@file:path:start-end`
- [x] **Create `internal/tui/refs/parser_test.go`**
    - Test all ref type parsing
    - Test line range extraction
    - Test punctuation stripping
- [x] **Verify**: `go test ./internal/tui/refs/...`

### Slice 2: Security Validator

- [x] **Create `internal/tui/refs/validator.go`**
    - Define blocked paths and directories
    - Implement `Validate(path string) error` — path traversal check
    - Implement `IsBlocked(path string) bool`
    - Implement `IsSensitiveFile(path string) bool`
    - Implement `isBinaryFile(data []byte) bool` — null byte + UTF-8 validity
    - Implement `IsBinaryPath(path string) bool`
- [x] **Create `internal/tui/refs/validator_test.go`**
    - Test path traversal blocking
    - Test sensitive path detection
    - Test binary detection
- [x] **Verify**: `go test ./internal/tui/refs/...`

### Slice 3: Base Expander + Truncation

- [ ] **Create `internal/tui/refs/expander.go`**
    - Define `Validator`, `Truncator` fields
    - Implement `Expand(input string) (*ExpansionResult, error)`
    - Implement `expandFile(ref ParsedRef) (string, error)`
    - Implement `expandFolder(ref ParsedRef) (string, error)`
    - Use Sandbox for file operations
- [ ] **Create `internal/tui/refs/expander_test.go`**
    - Test file expansion with temp files
    - Test folder expansion with temp directories
    - Test truncation (files >500 lines)
- [ ] **Verify**: `go test ./internal/tui/refs/...`

### Slice 4: Git References (@diff, @staged, @git:N)

- [ ] **Add git expansion to `expander.go`**
    - Implement `expandDiff(ref ParsedRef) (string, error)` — calls `git diff`
    - Implement `expandStaged(ref ParsedRef) (string, error)` — calls `git diff --staged`
    - Implement `expandGitLog(ref ParsedRef) (string, error)` — calls `git log -N --patch`
    - Reuse `runGit()` from `git_overview.go`
- [ ] **Add git tests to `expander_test.go`**
    - Test diff expansion with test repo
    - Test staged expansion
    - Test log expansion with max 10 commits clamp
- [ ] **Verify**: `go test ./internal/tui/refs/...`

### Slice 5: URL References (@url:)

- [ ] **Create `internal/tui/refs/web.go`**
    - Define `Fetcher` struct with http.Client
    - Implement `Fetch(url string) (string, error)` with timeout
    - Implement content size limit (100KB max)
- [ ] **Add web expansion to `expander.go`**
    - Implement `expandURL(ref ParsedRef) (string, error)`
- [ ] **Add web tests to `expander_test.go`**
    - Test URL fetch with mock server
    - Test size limit truncation
- [ ] **Verify**: `go test ./internal/tui/refs/...`

### Slice 6: TUI Integration

- [ ] **Modify `internal/tui/agent_loop.go`**
    - Add `refExpander *refs.Expander` field to model
    - Modify `submitPrompt()` to use expander
    - Show expanded content in chat (with warnings)
    - Handle truncation warnings display
- [ ] **Add expander initialization in `tui.go`**
    - Initialize expander with sandbox in `Run()`
    - Pass through config
- [ ] **Verify**: `go build ./... && go test ./internal/tui/...`

### Slice 7: Autocomplete Enhancement

- [ ] **Modify `internal/tui/completion.go`**
    - Extend `CompleteMention()` to handle `@file:`, `@folder:` prefixes
    - Add `completeGit()` for `@git:` prefix
    - Add `completeURL()` for `@url:` prefix
- [ ] **Modify `internal/tui/input.go`**
    - Update `findMentionAtCursor()` to detect ref type prefixes
- [ ] **Verify**: `go build ./... && manual TUI test`

---

## Key Files to Create

```
internal/tui/refs/
├── expander.go       # Main expander component
├── expander_test.go  # Tests
├── parser.go         # @ref syntax parsing
├── parser_test.go    # Tests
├── validator.go      # Security checks
├── validator_test.go # Tests
├── web.go            # URL fetching
└── doc.go            # Package documentation
```

## Key Files to Modify

- `internal/tui/tui.go` — Initialize expander
- `internal/tui/agent_loop.go` — Use expander in submitPrompt
- `internal/tui/completion.go` — Extend autocomplete
- `internal/tui/input.go` — Update mention detection

---

## Dependencies

- Slice 3 depends on Slices 1 and 2
- Slice 4 depends on Slice 3
- Slice 5 depends on Slice 3
- Slice 6 depends on Slices 3, 4, 5
- Slice 7 depends on Slice 6

---

## Acceptance Criteria

### Slice 1 (Parser)

- [ ] `@file:path`, `@folder:path`, `@diff`, `@staged`, `@git:N`, `@url:` all parsed correctly
- [ ] Line ranges extracted for `@file:path:start-end`
- [ ] Trailing punctuation stripped

### Slice 2 (Validator)

- [ ] `../` paths blocked
- [ ] `~/.ssh/id_rsa` blocked
- [ ] Binary files detected

### Slice 3 (File/Folder Expander)

- [ ] File contents attached under `--- Attached Context ---`
- [ ] Folder tree shown with sizes
- [ ] Files >500 lines truncated with indicator

### Slice 4 (Git Expander)

- [ ] `@diff` shows git diff output
- [ ] `@staged` shows staged diff
- [ ] `@git:5` shows 5 commits with patches
- [ ] `@git:15` clamped to 10 with warning

### Slice 5 (URL Expander)

- [ ] `@url:https://...` fetches page content
- [ ] Large content truncated

### Slice 6 (TUI Integration)

- [ ] Expanded message shown in chat
- [ ] Warnings displayed inline

### Slice 7 (Autocomplete)

- [ ] `@file:` triggers path completion
- [ ] `@folder:` triggers folder completion
