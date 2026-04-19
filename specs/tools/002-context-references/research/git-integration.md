# Git Integration Research

## 1. Git Command Execution

The core git execution happens in **`internal/tools/git_overview.go`** (lines 143-162):

```go
// runGit executes a git command in the given directory and returns stdout.
func runGit(ctx tool.Context, dir string, args ...string) (string, error) {
    var parentCtx = context.Background()
    if ctx != nil {
        parentCtx = ctx
    }
    cmdCtx, cancel := context.WithTimeout(parentCtx, defaultGitTimeout)
    defer cancel()

    cmd := exec.CommandContext(cmdCtx, "git", args...)
    cmd.Dir = dir

    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    if err := cmd.Run(); err != nil {
        return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(stderr.String()), err)
    }
    return stdout.String(), nil
}
```

### Key patterns:

- **Timeout**: 10 seconds (`defaultGitTimeout`)
- **Context propagation**: Uses tool context when available
- **Output handling**: Captures stdout and stderr separately
- **Error wrapping**: Includes git args and stderr in error messages

## 2. Git Diff Retrieval

**`internal/tools/git_diff.go`** provides unified diff for files:

| Function             | Description                                                   |
|----------------------|---------------------------------------------------------------|
| `gitFileDiffHandler` | Gets unified diff for a specific file                         |
| `GitFileDiffInput`   | `{file: string, staged?: bool}`                               |
| `GitFileDiffOutput`  | `{file, diff, lines_added, lines_removed, binary, truncated}` |

**`internal/tools/git_hunk.go`** provides parsed hunk-level diffs:

| Function         | Description                                        |
|------------------|----------------------------------------------------|
| `gitHunkHandler` | Gets parsed diff hunks with line counts            |
| `GitHunkInput`   | `{file: string, staged?: bool}`                    |
| `parseHunks()`   | Splits unified diff into individual `Hunk` structs |

## 3. Git Overview

**`internal/tools/git_overview.go`** provides repo status:

```go
GitOverviewOutput {
    Branch           string   // Current branch
    RecentCommits    []string // Last 10 commits (oneline)
    StagedFiles      []string // Porcelain staged files
    UnstagedFiles    []string // Modified but unstaged
    UntrackedFiles   []string // Untracked files
    Upstream         string   // Tracking branch
    Ahead, Behind    int      // Commits ahead/behind
}
```

Uses `git status --porcelain` for parsing file status (handles renames via `"R  old -> new"` format).

## 4. Git Compaction

**`internal/tools/compactor_git.go`** handles output size reduction:

| Function               | Purpose                                        |
|------------------------|------------------------------------------------|
| `compactGitDiffText`   | Summarizes diffs to file-level + limited hunks |
| `compactGitLogText`    | Limits git log to `MaxLogEntries`              |
| `compactGitStatusText` | Limits status to `MaxStatusFiles`              |

Default limits (`internal/tools/compactor.go`):

- `MaxDiffLines`: 100
- `MaxDiffHunkLines`: 20
- `MaxStatusFiles`: 10
- `MaxLogEntries`: 40

## 5. Tool Registration

Git tools are registered in **`internal/tools/registry.go`** (`CoreTools` function, lines 28-30):

```go
builders := []func(*Sandbox) (tool.Tool, error){
    // ... other tools ...
    newGitOverviewTool,
    newGitFileDiffTool,
    newGitHunkTool,
}
```

## 6. Test Helpers

**`internal/tools/git_diff_test.go`** provides test utilities:

```go
// initGitRepo: Creates temp dir, runs git init, configures user.email/name
// gitCommit: Commits with proper GIT_AUTHOR_* env vars
```

## 7. Sandbox Integration

All git tools receive a `*Sandbox` parameter which provides:

- `sb.Dir()`: The sandbox root directory for `cmd.Dir`
- Git repos are validated via `git rev-parse --git-dir` before operations

## Summary Table

| File                                  | Lines | Purpose                                |
|---------------------------------------|-------|----------------------------------------|
| `internal/tools/git_overview.go`      | 163   | Core `runGit()` + overview tool        |
| `internal/tools/git_diff.go`          | 95    | File diff tool                         |
| `internal/tools/git_hunk.go`          | 126   | Hunk parsing + tool                    |
| `internal/tools/compactor_git.go`     | 293   | Git output compaction                  |
| `internal/tools/git_diff_test.go`     | 292   | Tests for diff + hunk                  |
| `internal/tools/git_overview_test.go` | 227   | Tests for overview + porcelain parsing |
