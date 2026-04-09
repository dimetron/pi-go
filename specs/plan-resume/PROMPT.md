# Plan: `pi plan resume`

## Objective

Enable interrupted `/plan` sessions to be resumed from the exact PDD phase where they left off, with full context of what was already discussed and written to the spec directory.

## Motivation

When a `/plan` session is interrupted (Ctrl+C, network error, context limit), the user must re-type the full rough idea and the LLM has no memory of previous phases. This wastes context and creates friction. The fix: persist just enough context in the session metadata to reconstruct the plan state on resume.

## Scope

- TUI `/plan` command only (not CLI modes, not `/run`)
- Resumable from within the same TUI process
- Cross-session resume (user quits and restarts TUI) via the existing `--session` mechanism

## Out of Scope

- Auto-resume on crash/reconnect (future work)
- Plan session conflict detection (e.g., spec dir was modified externally)
- `/run` resume (separate tracking mechanism)

## Gates

- **build**: `go build ./...`
- **test**: `go test ./internal/tui/...`
