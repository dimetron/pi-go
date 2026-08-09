# Topic 4 — Three bugs silently disable compaction and caching

## Research

### 4a. The compactor never sees git tool output (one-line fix)

`internal/tools/compactor.go:108-113` routes on underscores:

```go
case "git_file_diff": return compactGitFileDiff(result, cfg)
case "git_overview":  return compactGitOverview(result, cfg)
case "git_hunk":      return compactGitHunk(result, cfg)
```

The tools register with hyphens (confirmed):

- `internal/tools/git_overview.go:52` — `newTool("git-overview", …)`
- `internal/tools/git_diff.go:36` — `newTool("git-file-diff", …)`
- `internal/tools/git_hunk.go:42` — `newTool("git-hunk", …)`

No case ever matches, so `compactGitFileDiff`, `compactGitOverview` and
`compactGitHunk` are unreachable. `internal/tools/dedup.go:41-43` uses the
hyphenated names and works correctly — which is why this went unnoticed.

### 4b. `read` returns whole source files uncapped

`internal/tools/read.go:136-144`:

```go
limit := input.Limit
if limit <= 0 {
    ext := strings.ToLower(filepath.Ext(input.FilePath))
    if sourceCodeExts[ext] {
        limit = totalLines            // no line truncation for source code
    } else {
        limit = defaultReadLimit      // 2000
    }
}
```

`sourceCodeExts` (`read.go:18-40`) covers `.go`, `.ts`, `.py`, `.rs`, `.java`
and 15 more. Only the 256 KB byte net (`truncate.go:4`) applies. This is why
`read` averages 6,974 bytes per result and accounts for 45.7% of all resend
debt.

### 4c. `FileContentCache` is dead code

`internal/tools/cache.go:10` implements exactly the mtime-invalidated,
TTL-bounded, LRU file cache this problem calls for. `read.go:86`
`readHandlerWithCache` uses it. But `read.go:83` passes `nil`:

```go
func readHandler(sb *Sandbox, input ReadInput) (ReadOutput, error) {
	return readHandlerWithCache(sb, input, nil)
}
```

and `registry.go:45` registers `newReadTool`, which routes to `readHandler`.
`NewFileContentCache` has no production caller (only tests). `edit.go:38→43`
has the same shape.

Note this is an **I/O cache, not a context-level one** — wiring it alone would
not keep bytes out of the prompt. It needs to be paired with a "this file is
unchanged since call #N" pointer.

### Also worth flagging: `smartTruncate` reorders `read` output

`compactor_bash.go:382-406` is applied to `read` results too. Above 440 lines it
keeps head 10% / tail 10% and fills the middle with **priority-scored,
reordered** lines (`func`/`type`/`import` scored 5, error/fail scored 10). For a
numbered source listing this yields non-contiguous, out-of-order line numbers,
and the model has no way to detect it. Cheap in bytes, expensive in correctness.

## Recommendations

1. **Fix the git tool names in `compactor.go:108-113`** to use hyphens
   (`git-file-diff`, `git-overview`, `git-hunk`). Trivial, unblocks three dead
   compaction stages. Add a test that asserts the compactor routes on the same
   names the tools register with, so this class of bug cannot recur.
2. **Cap `read` on source files at 2,000 lines** (the `defaultReadLimit`), so
   source files get the same line truncation as everything else. This attacks
   the 45.7% of resend debt directly. Keep the 256 KB byte net as a backstop.
3. **Wire `FileContentCache` into the production `read` path** (pass a real
   cache instead of `nil` at `read.go:83`), and pair it with an unchanged-file
   pointer so a re-read of an unchanged file is elided from the prompt rather
   than re-sent. The cache alone is I/O-only; the pointer is what keeps bytes
   out of the context.
4. **Replace `smartTruncate` for `read` with contiguous head/tail.** For a
   numbered source listing, keep the first N lines and the last M lines in
   order, with a clear "… N lines omitted …" marker. Do not reorder lines for
   source output.

## Expected impact

Unblocks three dead compaction stages (4a), ~45% of resend debt (4b/4c), and
removes a correctness hazard in `read` output (smartTruncate).

## Risk

Low. 4a and 4b are small, well-scoped fixes. 4c is additive (wire an existing,
tested cache). The smartTruncate change is a correctness improvement.
