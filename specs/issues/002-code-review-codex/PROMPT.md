The patch currently breaks the test package: `internal/agent/e2e_test.go` both contains leftover two-value `CreateSession` calls and ends with stray tokens that make the file unparsable. There is also a functional inconsistency in the new `/title` path, where the live TUI title can diverge from the persisted session title.

Full review comments:

- [P1] Update the remaining `CreateSession` callers for the new arity — /Users/dimetron/p6s/pi-dev/pi-go/internal/agent/agent.go:401-406
  After widening `CreateSession` to return `(sessionID, defaultTitle, error)`, `internal/agent/e2e_test.go` still has several two-value assignments at lines 138, 227, 364, 494, and 552. As soon as this signature change lands, `go test ./internal/agent` stops compiling with `assignment mismatch` errors, so the test package is broken until those callers are updated too.

- [P1] Remove the stray trailer appended to `e2e_test.go` — /Users/dimetron/p6s/pi-dev/pi-go/internal/agent/e2e_test.go:599-600
  The extra `n")` and closing braces appended after `TestE2ESessionBranchingWorkflow` make `internal/agent/e2e_test.go` syntactically invalid. Any build that includes tests will fail to parse this file before it can run the updated session-title logic.

- [P2] Normalize `/title` input before storing it in TUI state — /Users/dimetron/p6s/pi-dev/pi-go/internal/tui/agent_loop.go:372-380
  `setSessionTitle` now saves the raw `/title` text directly into `m.sessionTitle`, but `FileService.SetSessionTitle` still sanitizes control characters and truncates to 200 bytes. In an interactive session, `/title` values that are very long or contain C0 controls will therefore show one title in the current tab and a different one in `meta.json`/future resumes, because the model state is never normalized the same way as the persisted value.