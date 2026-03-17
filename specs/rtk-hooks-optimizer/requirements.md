# Requirements — RTK Hooks Optimizer

## Q&A Record

_(Questions and answers will be appended as requirements are clarified.)_

## Q1: Scope Priority

Given the two main features from pi-rtk-optimizer:

**A) Output Compaction** — AfterToolCallback that compacts bash output (ANSI stripping, test aggregation, build log filtering, git compaction, linter aggregation, smart truncation). This is where the biggest token savings come from.

**B) Command Rewriting** — BeforeToolCallback that intercepts bash commands and rewrites them. Since we have no external `rtk` binary, this would mean either:
  - Rewriting to more efficient command forms (e.g., `cat file` → suggest read tool)
  - Suggesting pi-go built-in tools instead of bash equivalents
  - Optimizing command flags for less verbose output

Which is higher priority, and do you want both? Or should we focus on output compaction first and defer command rewriting?

**A1:** Focus on Output Compaction (A) first. Command rewriting (B) deferred to a later phase.

---

## Q2: Compaction Stages

The pi-rtk-optimizer has these compaction stages for bash output:

1. **ANSI stripping** — remove escape codes (trivial, universal benefit)
2. **Test output aggregation** — `go test`, `pytest`, `jest` → pass/fail/skip summary + failure details
3. **Build output filtering** — `go build`, `cargo build`, `make` → errors/warnings only
4. **Git output compaction** — diff summarization, status limiting, log condensing
5. **Linter aggregation** — `golangci-lint`, `eslint` → grouped by rule/file
6. **Search result grouping** — grep/find output grouped by file
7. **Smart truncation** — priority-based line selection as final fallback
8. **Hard truncation** — absolute char limit (default 12000)

Plus for the `read` tool:
9. **Source code filtering** — minimal (strip comments) or aggressive (signatures only)

Which stages do you want in the initial implementation? All of them, or a subset?

**A2:** All stages (1-9) in initial implementation.

---

## Q3: Configuration & TUI

The pi-rtk-optimizer has a `/rtk` slash command with:
- TUI modal for toggling individual compaction stages on/off
- `/rtk stats` for session metrics (token savings per tool)
- `/rtk reset` to reset config to defaults
- Per-stage enable/disable in config.json

Do you want the full `/rtk` TUI command with interactive config modal, or is a simpler approach sufficient (e.g., just config.json toggles + `/rtk stats`)?

**A3:** Just `/rtk stats` for viewing session metrics. Configuration via config.json only, no TUI modal.

---

## Q4: Compaction Visibility

When output is compacted, should the user see it? Options:

- **A) Silent** — compaction happens transparently, user only sees compacted result in the conversation
- **B) Banner** — prepend a short note like `[compacted: 12KB → 2KB, techniques: ansi, test-aggregate]` to the tool output
- **C) TUI indicator** — show compaction info in the TUI tool output display (not sent to LLM)

Which approach?

**A4:** TUI indicator only (C). Show compaction info in TUI display, don't pollute LLM context with metadata.

---

## Q5: Default State

Should output compaction be enabled by default for all users, or opt-in via config.json?

- **A) Enabled by default** — all stages active out of the box, users can disable individual stages
- **B) Opt-in** — disabled by default, users enable in config.json

**A5:** Enabled by default (A). All stages active out of the box.

---

## Q6: Compaction Limits

The pi-rtk-optimizer uses these defaults:
- Hard truncation: 12,000 chars
- Smart truncation: 220 lines
- Test failures shown: up to 5, 4 lines each
- Build errors shown: up to 5, 10 lines each
- Git diff: max 50 lines, 10 lines/hunk
- Git status: 5 files per category
- Git log: 20 entries
- Linter: top 10 rules, top 10 files
- Search: 10 matches/file, 50 total

pi-go currently has a 100KB hard limit on tool output. Should we:

- **A) Use rtk-optimizer defaults** as listed above (much more aggressive than current 100KB)
- **B) Start with higher limits** (e.g., 24K chars, 500 lines) and tune down based on `/rtk stats` data
- **C) Make all limits configurable** in config.json with rtk-optimizer defaults

**A6:** Use rtk-optimizer defaults but doubled:
- Hard truncation: 24,000 chars
- Smart truncation: 440 lines
- Test failures: up to 10, 8 lines each
- Build errors: up to 10, 20 lines each
- Git diff: max 100 lines, 20 lines/hunk
- Git status: 10 files per category
- Git log: 40 entries
- Linter: top 20 rules, top 20 files
- Search: 20 matches/file, 100 total

---

## Q7: Package Structure

Where should the compaction code live? Options:

- **A) `internal/extension/compactor/`** — new subpackage under extension, with separate files per technique
- **B) `internal/compactor/`** — top-level internal package, independent of extension
- **C) `internal/tools/compactor.go`** — within existing tools package

**A7:** Within existing tools package (C). Keep it close to the tools that produce the output.

---

## Q8: Interaction with Existing Truncation

pi-go already has `truncateOutput()` (100KB) and `truncateLine()` (500 chars) applied inside tools before returning results. The new compaction runs as an AfterToolCallback — after the tool returns.

Should the compactor:

- **A) Replace existing truncation** — remove `truncateOutput()` from tools, let the compactor handle everything
- **B) Layer on top** — keep existing truncation as safety net, compactor does semantic work on already-truncated output
- **C) Replace for bash only** — remove truncation from bash tool (main compaction target), keep for other tools

**A8:** Replace existing truncation (A). Compactor handles all output processing — remove `truncateOutput()` from tools, centralize in the AfterToolCallback.

---

## Q9: Which Tools Get Compacted?

The compactor AfterToolCallback fires for all tools. Which should it process?

- **bash** — build logs, test output, linter, git, general output (biggest win)
- **read** — source code filtering (strip comments/bodies)
- **grep** — group results by file
- **find** — group/sort results
- **tree** — already fairly compact
- **git-diff** — diff summarization
- **git-overview** — status limiting (currently unbounded file lists)
- **git-hunk** — hunk output

All of them, or a subset?

**A9:** All tools get compacted.

---

## Q10: Metrics Storage

`/rtk stats` needs to track compaction savings. Options:

- **A) In-memory only** — reset on restart, simple
- **B) Persisted per session** — saved alongside session events, survives restart
- **C) Persisted globally** — accumulated across all sessions in a file

**A10:** Persisted per session (B).

---

## Q11: Naming

The pi-rtk-optimizer name comes from the `rtk` binary. Since we're not using `rtk`, should we:

- **A) Keep `/rtk` naming** — familiar to pi-rtk-optimizer users
- **B) Use `/compact`** — already exists as a slash command (session context compaction), could conflict
- **C) Use `/optimizer`** or `/opt`** — new name reflecting purpose
- **D) Something else?

**A11:** Keep `/rtk` naming (A).

---

Requirements clarification complete.
