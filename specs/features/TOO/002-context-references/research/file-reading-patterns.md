# File/Folder Reading Patterns Research

## Core Sandbox Abstraction (`internal/tools/sandbox.go`)

The **Sandbox** is the central security mechanism for all file operations:

### Key Security Features:

- Uses `os.Root` (Go 1.24+) to restrict all file operations to a specific directory tree
- **Path validation** via `filepath.Clean()` and prefix checking (`../` escape detection)
- **Extra directories** can be added via `AddExtraDir()` with separate `os.Root` instances
- **Worktree-relative path resolution** for subagent contexts

### File Operations:

```go
// sandbox.go:249-272 - ReadFile with retry logic
func (s *Sandbox) ReadFile(name string) ([]byte, error)
func (s *Sandbox) WriteFile(name string, data []byte, perm os.FileMode) error
func (s *Sandbox) Open(name string) (*os.File, error)  // line 300
func (s *Sandbox) Stat(name string) (os.FileInfo, error)  // line 309
func (s *Sandbox) ReadDir(name string) ([]os.DirEntry, error)  // line 318
func (s *Sandbox) MkdirAll(name string, perm os.FileMode) error  // line 332
```

### Path Resolution (`sandbox.go:177-226`):

- `Resolve(name string)` - converts absolute/relative to relative path, blocks escape
- `resolveToRoot(name)` - checks extra roots first, then sandbox root
- `matchExtraRoot(absPath)` - finds matching extra directory

## Directory Listing (`internal/tools/ls.go`)

```go
// ls.go:41-74
func lsHandler(sb *Sandbox, input LsInput) (LsOutput, error)
// Returns: Name, IsDir, Size for each entry
// Limit: maxLsEntries = 1000 (line 10)
```

## Tree Listing (`internal/tools/tree.go`)

```go
// tree.go:50-88
func treeHandler(sb *Sandbox, input TreeInput) (TreeOutput, error)
// Defaults: depth=3, max=10, maxEntries=500
// Skips: .hidden, node_modules, vendor, __pycache__, .git, dist, build, etc.
```

## File Reading (`internal/tools/read.go`)

```go
// read.go:85-172
func readHandlerWithCache(sb *Sandbox, input ReadInput, cache *FileContentCache) (ReadOutput, error)
// Features:
// - Offset/limit support (1-based offset)
// - Source code files bypass default 2000-line limit (line 136-143)
// - UTF-8 via strings.Split (no explicit encoding check)
// - Base64 image stripping for markdown files (line 158-159)
// - 256KB byte safety truncation via truncateOutput()
// - File caching with mtime-based invalidation
```

## Directory Traversal (Find/Grep)

```go
// find.go:38-119 - Glob-based file finding
// Uses: fs.WalkDir(), shouldSkipDir(), shouldSkipPath()

// grep.go:136-243 - Content search
// Uses: fs.WalkDir() for directories, bufio.Scanner for files
// Falls back to ripgrep if available (rg binary)
```

## Binary/File Type Detection

**Audit Scanner (`internal/audit/scanner.go:147-169`):**

```go
// Detects binary via utf8.Valid() check
if !utf8.Valid(data) {
    // Marked as "possible binary"
}
```

**Git Diff (`internal/tools/git_diff.go:70-75`):**

```go
// Detects binary via git output parsing
if strings.Contains(diff, "Binary files") {
    out.Binary = true
}
```

## Path Validation

**Hardcoded Skips (`sandbox.go:459-492`, `find.go:121-129`):**

```go
skipDirs := map[string]bool{
    "node_modules": true, "vendor": true, "__pycache__": true,
    ".git": true, ".hg": true, ".svn": true,
    ".idea": true, ".vscode": true,
    "dist": true, "build": true, ".next": true, ".cache": true,
}
// EXCEPTION: .pi-go, .cursor, .claude are NOT skipped (agent/skill files)
```

**Path Escape Prevention (`sandbox.go:194-200`):**

```go
cleaned := filepath.Clean(rel)
if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
    return "", fmt.Errorf("path %q escapes sandbox root...")
}
```

## Output Limits & Safety

```go
// truncate.go:3-13
const (
    maxOutputBytes = 256 * 1024  // 256KB safety limit
    maxLineLength  = 500         // max chars per line
)
```

## Content Caching (`internal/tools/cache.go`)

```go
// FileContentCache - mtime-based LRU cache
// Invalidated on: file modification, TTL expiry, manual Invalidate()
```

## Key Files Summary

| File                         | Purpose                                    |
|------------------------------|--------------------------------------------|
| `internal/tools/sandbox.go`  | Core file system abstraction with security |
| `internal/tools/read.go`     | File reading with offset/limit             |
| `internal/tools/ls.go`       | Directory listing                          |
| `internal/tools/tree.go`     | Recursive tree display                     |
| `internal/tools/find.go`     | Glob-based file finding                    |
| `internal/tools/grep.go`     | Content search                             |
| `internal/tools/edit.go`     | String-based editing                       |
| `internal/tools/cache.go`    | File content caching                       |
| `internal/tools/truncate.go` | Output size limits                         |
| `internal/audit/scanner.go`  | UTF-8/binary detection                     |
