# Context References - Design Document

## Current State

The TUI currently supports `@file` mentions for autocomplete, but only attaches `[Referenced file: path]`
annotations—not actual file content. Git references (`@diff`, `@staged`, `@git:N`) and web fetch (`@url:`) are not
implemented.

## Desired End State

Users can inject content directly into messages using `@` syntax references. When a message is submitted, the references
are expanded inline with the content, and the expanded text is shown to the user and sent to the agent.

## Architecture Overview

```mermaid
flowchart TD
subgraph TUI Input
A[User types message] --> B[@ref syntax detected]
B --> C[Path autocomplete active]
end

subgraph Message Submission
D[InputSubmitMsg] --> E[ExpandReferences]
E --> F[Content validated]
F --> G[Content truncated if needed]
G --> H[Expanded message ready]
end

subgraph Reference Types
I[@file:path] --> J[Sandbox.ReadFile]
I1[@file:path:line-range] --> J1[Sandbox.ReadFile + slice]
K[@folder:path] --> L[Sandbox.ReadDir + tree]
M[@diff] --> N[runGit diff]
O[@staged] --> N
P[@git:N] --> Q[runGit log -N]
R[@url:https://...] --> S[HTTP GET]
end

subgraph Security
T[Sensitive path check] --> U[Path traversal check]
U --> V[Binary detection]
V --> W[Size limit check]
end

H --> X[Show in chat + send to agent]
```

## Components

### 1. Reference Expander (`internal/tui/refs/expander.go`)

Core component that parses and expands `@ref` syntax in user messages.

```go
// Expander processes context references in user input
type Expander struct {
sandbox   *tools.Sandbox
workDir   string
maxLines  int // For truncation
}

type RefType string
const (
RefFile    RefType = "file"
RefFolder  RefType = "folder"
RefDiff    RefType = "diff"
RefStaged  RefType = "staged"
RefGit     RefType = "git"
RefURL     RefType = "url"
)

type ParsedRef struct {
Type      RefType
RawValue  string // Original @ref string
Value     string // Parsed value (path, URL, etc.)
LineRange *struct{ Start, End int }  // For @file:path:start-end
}

type ExpansionResult struct {
Original  string // Original message
Expanded  string // Message with content appended
Warnings  []string  // Warnings for invalid refs
}

// Expand parses references and returns expanded message
func (e *Expander) Expand(input string) (*ExpansionResult, error)

// parseRefs extracts all @ref patterns from input
func parseRefs(input string) []ParsedRef

// expandRef expands a single reference to content
func (e *Expander) expandRef(ref ParsedRef) (string, error)
```

### 2. Security Validator (`internal/tui/refs/validator.go`)

Handles security checks before expanding references.

```go
// Validator enforces security policies on references
type Validator struct {
workDir   string
blockedPaths  []string // e.g., ~/.ssh/, ~/.aws/
blockedFiles  []string // e.g., id_rsa, .env
}

func (v *Validator) IsBlocked(path string) bool
func (v *Validator) IsSensitiveFile(path string) bool
func (v *Validator) IsBinaryFile(path string) bool
func (v *Validator) ValidatePath(path string) error
```

### 3. Truncator (`internal/tui/refs/truncate.go`)

Handles content truncation for large references.

```go
const (
MaxLinesPerRef = 500
MaxFolderEntries = 200
MaxGitCommits = 10
)

func truncateContent(content string, maxLines int) (string, bool) {
// Returns truncated content and whether it was truncated
}
```

### 4. Web Fetcher (`internal/tui/refs/web.go`)

Handles `@url:` references.

```go
// Fetcher retrieves web content
type Fetcher struct {
client    *http.Client
timeout   time.Duration
}

func (f *Fetcher) Fetch(url string) (string, error)
```

### 5. TUI Integration (`internal/tui/input.go`, `internal/tui/agent_loop.go`)

Modified to use the expander during message submission.

```go
// In agent_loop.go - submitPrompt() modification
func (m *model) submitPrompt(text string, mentions []string) {
// Parse and expand references
result, err := m.refExpander.Expand(text)
if err != nil {
// Show error to user
return
}

// Show expanded message in chat
displayText := result.Expanded
if len(result.Warnings) > 0 {
displayText += "\n\n" + formatWarnings(result.Warnings)
}

m.addUserMessage(displayText)
go m.runAgentLoop(displayText)
}
```

## Data Models

### ExpansionResult

```go
type ExpansionResult struct {
Original   string // Original message
Expanded   string // Message with content appended
RefsFound  []ParsedRef         // All references found
RefsExpanded map[string]string // Ref → expanded content
Warnings   []RefWarning // Warnings for invalid refs
Truncated  []string     // Refs that were truncated
}

type RefWarning struct {
Ref   string // The @ref that had issues
Type  string // "file_not_found", "binary", "too_large", etc.
Msg   string // Human-readable message
}
```

### Formatted Output

```
--- Attached Context ---

[Referenced file: src/main.go]
```go
func main() {
    fmt.Println("hello")
}
```

---

[Referenced folder: src/utils]

```
src/utils/
├── formatter.go (1.2KB)
├── parser.go (2.4KB)
└── helpers.go (0.8KB)
```

---

[git diff]

```diff
diff --git a/src/main.go b/src/main.go
index 1234567..abcdefg 100644
--- a/src/main.go
+++ b/src/main.go
@@ -1,3 +1,4 @@
 func main() {
+    fmt.Println("hello")
 }
```

---

[Truncated: file exceeds 500 line limit showing first 500 lines]

```

## Patterns to Follow

1. **Sandbox integration**: Use existing `*tools.Sandbox` for all file operations (follows `read.go`, `ls.go`, `tree.go` patterns)

2. **Git execution**: Use `runGit()` from `git_overview.go:143-162` with 10s timeout

3. **Error handling**: Return warnings (not failures) for invalid refs—Hermes pattern: inline warnings rather than failures

4. **Path resolution**: Use `sandbox.Resolve()` for path canonicalization

5. **Completion integration**: Extend existing `@` completion in `completion.go` to support new ref types

## Error Handling Strategy

| Condition | Behavior |
|-----------|----------|
| File not found | Warning: "file not found: {path}" |
| Binary file | Warning: "binary files are not supported: {path}" |
| Folder not found | Warning: "folder not found: {path}" |
| Git command fails | Warning with git stderr |
| URL returns no content | Warning: "no content extracted from {url}" |
| Sensitive path | Warning: "path is a sensitive credential file" |
| Path outside workspace | Warning: "path is outside the allowed workspace" |
| Content too large | Truncate with indicator |

## Security Implementation

### Sensitive Path Blocking

```go
var sensitivePaths = []string{
    // SSH keys
    ".ssh/id_rsa", ".ssh/id_ed25519", ".ssh/authorized_keys", ".ssh/config",
    // Shell profiles
    ".bashrc", ".zshrc", ".profile", ".bash_profile", ".zprofile",
    // Credential files
    ".netrc", ".pgpass", ".npmrc", ".pypirc",
}

var blockedDirs = []string{
    ".ssh", ".aws", ".gnupg", ".kube",
    // Hermes-specific
    "skills/.hub",
}
```

### Path Traversal Protection

```go
func (v *Validator) validatePath(path string) error {
cleaned := filepath.Clean(path)
if strings.HasPrefix(cleaned, "..") {
return errors.New("path escapes sandbox")
}
// Check against blocked directories
for _, blocked := range v.blockedDirs {
if strings.HasPrefix(cleaned, blocked) {
return errors.New("path is in blocked directory")
}
}
return nil
}
```

### Binary Detection

```go
func isBinaryFile(data []byte) bool {
// Check for null bytes
for _, b := range data[:min(512, len(data))] {
if b == 0 {
return true
}
}
// UTF-8 validity check
if !utf8.Valid(data) {
return true
}
return false
}
```

## Acceptance Criteria

### @file References

- **Given** user types `@file:src/main.go`, **when** they press Enter, **then** file contents appear in the message
  under `--- Attached Context ---`
- **Given** user types `@file:src/main.go:10-25`, **when** they press Enter, **then** only lines 10-25 appear
- **Given** user references a binary file, **when** they press Enter, **then** a warning appears and file is not
  attached
- **Given** user references a sensitive path like `~/.ssh/id_rsa`, **when** they press Enter, **then** a security
  warning appears

### @folder References

- **Given** user types `@folder:src/components`, **when** they press Enter, **then** directory tree with file sizes
  appears
- **Given** folder has >200 files, **when** expanded, **then** first 200 shown with `- ...` indicator

### Git References

- **Given** user types `@diff`, **when** they press Enter, **then** `git diff` output appears
- **Given** user types `@staged`, **when** they press Enter, **then** `git diff --staged` output appears
- **Given** user types `@git:5`, **when** they press Enter, **then** last 5 commits with patches appear
- **Given** user types `@git:15`, **when** expanded, **then** clamped to 10 commits with warning

### URL References

- **Given** user types `@url:https://example.com`, **when** they press Enter, **then** web page content is fetched and
  attached
- **Given** URL fetch fails, **when** expanded, **then** error warning appears

### Truncation

- **Given** reference content exceeds 500 lines, **when** expanded, **then** first 500 lines shown with "... truncated"
  indicator

### Autocomplete

- **Given** user types `@file:` in TUI, **when** they press Tab, **then** filesystem path completion appears
- **Given** user types `@folder:` in TUI, **when** they press Tab, **then** folder path completion appears

### Security

- **Given** user tries to reference `../etc/passwd`, **when** expanded, **then** path traversal warning appears
- **Given** user tries to reference `.bashrc`, **when** expanded, **then** sensitive file warning appears

## Testing Strategy

1. **Unit tests** (`refs/expander_test.go`, `refs/validator_test.go`):
    - Parse valid/invalid ref syntax
    - Validate security checks
    - Test truncation logic
    - Test line range parsing

2. **Integration tests** (`refs/integration_test.go`):
    - Full expansion flow with real sandbox
    - Git refs with test git repo
    - URL refs with mock HTTP server

3. **Manual testing**:
    - TUI input flow with various ref types
    - Autocomplete behavior
    - Large file truncation
