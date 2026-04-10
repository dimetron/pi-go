The patch introduces two user-facing regressions in interactive mode: the printed resume command does not actually target the shown session, and the post-init input seed breaks slash-command completion on startup. Those behavior changes make the patch incorrect despite the added tests and documentation.

Full review comments:

- [P2] Use `--session` in the printed resume command — /Users/dimetron/p6s/pi-dev/pi-go/internal/cli/interactive.go:105-105
  The exit hint now prints `pi --continue <session-id>`, but `--continue` is a boolean flag that always resolves to `LastSessionID` and ignores the supplied ID. In the common case where a user opens another session before copying this hint, the advertised command resumes the newer conversation instead of the session shown here. Emitting `--session <id>` (or omitting the ID entirely) would keep the hint aligned with actual CLI behavior.

- [P3] Leave the input buffer empty after TUI initialization — /Users/dimetron/p6s/pi-dev/pi-go/internal/tui/tui.go:924-925
  Seeding the input with a literal space makes every fresh TUI session start with a non-empty buffer. `InputModel.HandleKey` only enters slash-command completion/cycling when the first `/` is typed into an empty buffer, so after this change the command menu and `/` cycling no longer activate until the user manually deletes the seeded space. That regresses the main slash-command entry path for every interactive startup.