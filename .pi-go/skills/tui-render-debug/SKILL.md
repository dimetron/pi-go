---
name: tui-render-debug
description: Diagnose and fix pi-go TUI rendering corruption, especially displaced panels, stale rows, cursor drift, and terminal-specific ANSI incompatibilities.
---

# TUI Render Debugging

Use this skill when pi-go's Bubble Tea interface is broken in a terminal: text
appears in the wrong row or column, the sidebar moves, old frames remain after a
resize, or a redraw repairs the display.

## Establish the symptom

Capture the exact terminal, `TERM`, `COLORTERM`, `TERMINAL_EMULATOR`, terminal
size, and whether a full redraw or switching terminal modes repairs the frame.
Compare the broken frame with the next clean frame. A clean redraw after a bad
incremental frame points to renderer cursor state, not necessarily to bad
`View()` geometry.

For JetBrains IDE terminals, record `TERMINAL_EMULATOR=JetBrains-JediTerm`.
JediTerm can report `TERM=xterm-256color` while mishandling cursor-backward-tab
(`CSI Z`, CBT), which makes Bubble Tea's differential renderer believe the
cursor is in a different column than the emulator.

## Separate output leaks from renderer drift

The interactive TUI owns the terminal. Search the active path for direct output:

```bash
rg -n 'fmt\.Print|log\.Print|os\.(Stdout|Stderr)|slog\.' internal cmd
```

Routine diagnostics must use the session logger. User-facing notices must use
the TUI message or notice channel. A subprocess's stdout and stderr must be
captured and surfaced as a tool result. Direct writes can move the cursor, but
the absence of leaked stderr does not rule out a renderer incompatibility.

## Build a red-capable repro

Use a PTY with the affected environment and capture stdout and stderr separately.
Count and inspect control sequences in stdout; stderr should contain only
diagnostics expected outside the frame.

For renderer-level tests, create an `ultraviolet.TerminalRenderer` with the
affected environment, move from a later column to an earlier column, and assert
that unsupported CBT is not emitted. Replay the captured bytes through a
JediTerm-compatible emulator when available. Also verify ordinary xterm and
tmux/screen environments remain unchanged.

## Fix decisions

Keep frame geometry tests independent: every rendered row must match the
terminal width, the rail must stay in one display column, and no ANSI escape may
leak into visible content. Do not solve a terminal capability bug by changing
layout constants.

When the Bubble Tea dependency has no per-capability switch, isolate a
compatibility environment to Bubble Tea's renderer. Preserve the original
environment for subprocesses and use the original environment for color-profile
detection. Prefer disabling only the broken capability when the dependency
supports it; otherwise use the narrowest compatible renderer profile and add a
comment explaining why.

## Verification

Run the focused regression test first, then the full TUI package, race tests for
the affected seam, `golangci-lint run ./internal/tui/...`, and a build. Re-run
the PTY capture and confirm the affected terminal no longer drifts while normal
terminal output and colors remain intact.
