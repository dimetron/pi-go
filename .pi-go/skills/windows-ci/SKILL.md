---
name: windows-ci
description: Diagnose pi-go failures that only appear in Windows CI - t.TempDir cleanup with an open sandbox handle, tests that hard-code the ripgrep tool name, and reading a Windows job log with gh. Use when Test (Windows) or the windows build fails.
---

# Windows CI failures

macOS cannot reproduce these, so a green local run says nothing about them.

### Two failures macOS cannot catch, but Windows CI will

CI runs `Test (Windows)` and `Build (windows, amd64)`. Both classes below pass on
macOS and fail only there, so a green local run is not evidence they are fixed.

- **An open directory handle breaks `t.TempDir()` cleanup.** Windows refuses to
  remove a directory whose entries are still open: `TempDir RemoveAll cleanup:
  unlinkat ...: The process cannot access the file because it is being used by
  another process`. `tools.NewSandbox` holds an open root, so a test that builds
  one must release it:

  ```go
  sb, err := NewSandbox(t.TempDir())
  if err != nil {
      t.Fatal(err)
  }
  t.Cleanup(func() { _ = sb.Close() })
  ```

  The failure surfaces during *cleanup*, so the test's own assertions pass and the
  report points at `testing.go` rather than at your test. `internal/agent` wraps
  this in `testSandbox`; use that helper, or close explicitly.

- **`rg` is not installed on the Windows runner.** `newGrepTool` self-names by
  capability — `"ripgrep"` when `rg` is present, `"grep"` when it is not
  (`grep.go:128-133`) — so a test that requires `"ripgrep"` by name fails on any
  host without `rg`. Assert *exactly one* of the two is registered and that both
  spellings route, and cover both branches by toggling the `rgAvailable` package
  variable (`TestCompactorRouting_WithRipgrep` / `_WithoutRipgrep` show the
  pattern). Do not hard-code either name.

When CI reports a Windows failure, read the job log directly rather than guessing:

```bash
gh run list --branch <branch> --limit 5 \
  --json databaseId,conclusion,headSha --jq '.[].databaseId'
gh api repos/dimetron/pi-go/actions/runs/<run>/jobs \
  --jq '.jobs[] | select(.name|test("Windows")) | .id'
gh api --allow-escape-sequences repos/dimetron/pi-go/actions/jobs/<job>/logs \
  | tr -d '\r' | grep -E 'FAIL|panic'
```

`--allow-escape-sequences` is required; without it the log API refuses to print.
