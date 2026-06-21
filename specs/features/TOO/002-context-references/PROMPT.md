# Context References

## Objective

Implement a Context References feature for pi-go that allows users to inject file, folder, git, and web content into
messages using `@ref` syntax in TUI mode. Expanded content is shown inline with the message.

## Key Requirements

1. **@file:path** — Inject file contents, optionally with line range `@file:path:10-25`
2. **@folder:path** — Inject directory tree with file metadata
3. **@diff** — Inject `git diff` output
4. **@staged** — Inject `git diff --staged` output
5. **@git:N** — Inject last N commits with patches (max 10)
6. **@url:https://...** — Fetch and inject web page content
7. **Visible expansion** — Content shown inline under `--- Attached Context ---`
8. **Truncation** — Large content truncated with indicator
9. **Path autocomplete** — File/folder path completion in TUI
10. **Security** — Sensitive path blocking, path traversal protection, binary detection

## Acceptance Criteria

### @file References

- Given user types `@file:src/main.go`, when they press Enter, then file contents appear under
  `--- Attached Context ---`
- Given user types `@file:src/main.go:10-25`, when they press Enter, then only lines 10-25 appear
- Given user references a binary file, when they press Enter, then a warning appears

### @folder References

- Given user types `@folder:src/components`, when they press Enter, then directory tree with file sizes appears
- Given folder has >200 files, when expanded, then first 200 shown with `- ...` indicator

### Git References

- Given user types `@diff`, when they press Enter, then `git diff` output appears
- Given user types `@staged`, when they press Enter, then `git diff --staged` output appears
- Given user types `@git:5`, when they press Enter, then last 5 commits with patches appear
- Given user types `@git:15`, when expanded, then clamped to 10 commits with warning

### URL References

- Given user types `@url:https://example.com`, when they press Enter, then web page content is fetched

### Security

- Given user tries to reference `../etc/passwd`, when expanded, then path traversal warning appears
- Given user tries to reference `~/.ssh/id_rsa`, when expanded, then sensitive file warning appears

## Implementation Slices

1. **Reference Parser** — Parse `@ref` syntax, create `internal/tui/refs/parser.go`, verify:
   `go test ./internal/tui/refs/...`
2. **Security Validator** — Path traversal, sensitive paths, binary detection, create `internal/tui/refs/validator.go`,
   verify: `go test ./internal/tui/refs/...`
3. **Base Expander** — Wire parsing + validation for file/folder refs, create `internal/tui/refs/expander.go`, verify:
   `go test ./internal/tui/refs/...`
4. **Git References** — Add `@diff`, `@staged`, `@git:N` expansion using existing runGit, verify:
   `go test ./internal/tui/refs/...`
5. **URL References** — Add `@url:` HTTP fetch support, create `internal/tui/refs/web.go`, verify:
   `go test ./internal/tui/refs/...`
6. **TUI Integration** — Hook expander into message submission in `agent_loop.go`, verify:
   `go build ./... && go test ./internal/tui/...`
7. **Autocomplete Enhancement** — Extend completion for `@file:`, `@folder:` prefixes in `completion.go`, verify:
   `go build ./...`

## Gates

- **build**: `go build ./...`
- **test**: `go test ./...`
- **vet**: `go vet ./...`

## Reference

- Design: `specs/features/TOO/002-context-references/design.md`
- Outline: `specs/features/TOO/002-context-references/outline.md`
- Plan: `specs/features/TOO/002-context-references/plan.md`
- Requirements: `specs/features/TOO/002-context-references/requirements.md`
- Research: `specs/features/TOO/002-context-references/research/`

## Constraints

- Use existing `*tools.Sandbox` for all file operations
- Use existing `runGit()` from `git_overview.go` for git operations
- Follow error handling pattern: return warnings (not failures) for invalid refs
- 500 line max per reference, 200 folder entries max, 10 git commits max
