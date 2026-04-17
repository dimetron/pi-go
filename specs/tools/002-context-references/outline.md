# Context References - Implementation Outline

## Slices Overview

### Phase 1: Core Infrastructure

1. **Reference Parser** — Parse `@ref` syntax from user input
2. **Security Validator** — Path traversal, sensitive paths, binary detection
3. **Base Expander** — Wire up parsing + validation for file refs

### Phase 2: File/Folder Support

4. **@file expansion** — Read file contents with line range support
5. **@folder expansion** — Directory listing with file metadata
6. **Truncation** — Content truncation for large files/folders

### Phase 3: Git Support

7. **@diff/@staged** — Git diff expansion using existing runGit
8. **@git:N** — Git log expansion with max 10 commits

### Phase 4: Web Support

9. **@url: support** — HTTP fetch for web content

### Phase 5: TUI Integration

10. **TUI integration** — Hook expander into message submission
11. **Autocomplete enhancement** — Extend completion for new ref types

---

## Key Type Signatures

```go
// Package: internal/tui/refs

// Expander - main component
type Expander struct {
    sandbox    *tools.Sandbox
    workDir    string
    validator  *Validator
    truncator  *Truncator
    fetcher    *Fetcher
}

func (e *Expander) Expand(input string) (*ExpansionResult, error)

// Validator - security checks
type Validator struct {
    workDir       string
    blockedDirs   []string
    blockedFiles  []string
}

func (v *Validator) Validate(path string) error
func (v *Validator) IsBinary(path string) bool

// ExpansionResult - output
type ExpansionResult struct {
    Original   string
    Expanded   string
    Warnings   []RefWarning
    Truncated  []string
}

// Constants
const (
    MaxLinesPerRef = 500
    MaxFolderEntries = 200
    MaxGitCommits = 10
    MaxURLSize = 100 * 1024
)
```

---

## Order of Changes

1. Create `internal/tui/refs/` package
2. Add reference parser
3. Add validator with security checks
4. Add truncation utilities
5. Implement file/folder expansion using Sandbox
6. Implement git expansion using existing runGit
7. Implement web fetcher
8. Modify `agent_loop.go` to use expander
9. Extend completion for new ref types

---

## Testing Order

1. Parser tests (can run without sandbox)
2. Validator tests (with mock paths)
3. File expansion tests (with temp files)
4. Git expansion tests (with test repo)
5. Integration test (full flow in TUI)
