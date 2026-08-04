# Rough Idea

rtk-phase-2-command-rewriting

Phase 2 of the existing RTK compactor spec at `specs/research/000-rtk-hooks-optimizer/`. The current compactor only
compacts output of our own tools (`bash`, `read`, `grep`, `find`, `tree`, `git_*`). This spec closes the highest-value
remaining gaps: language-aware source filtering, bash command detection for the top 5 missing categories (
ls/cat/find/env/docker), tee overflow for lossy compaction, a `never_worse` token-aware guard, and the missing TUI
per-message compaction indicator. Out of scope: command rewriting before tool execution (would need a separate Phase 3
spec — the original 000 deferred this and we keep it deferred).
