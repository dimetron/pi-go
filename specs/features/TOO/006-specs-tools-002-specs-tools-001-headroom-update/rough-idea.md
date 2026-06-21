# Rough Idea

## Headline

Redo the headroom-style overhaul of pi-go's RTK compactor. A previous attempt
(`specs/tools/002-specs-tools-001-headroom-update-existingf-rtk/`) finished with
gates passing but **zero implementation landed in the tree** — the agent's
SUMMARY claimed success, but a review confirmed no new Go files, no new tests,
no restructured config, and no CCR store were ever written. The parity
fixtures (`internal/tools/testdata/parity/*.json`) are orphaned in the tree.

## Source / Reference

- Upstream headroom repo (Rust + Python): https://github.com/dimetron/headroom
- Locally checked out at `tmp/headroom/` (untracked) — reference implementation
- Previous (failed) spec artifacts at `specs/tools/002-specs-tools-001-headroom-update-existingf-rtk/`:
    - `requirements.md` — consolidated Q&A
    - `research/headroom-architecture.md`, `research/pi-go-current-compactor.md`
    - `design.md` — full architecture (good, reusable)
    - `outline.md`, `plan.md`, `PROMPT.md` — staging + plan (good, reusable)
    - `SUMMARY.md` — failure analysis + recommendation

## Goal

Port headroom's 6-algorithm compression pipeline into pi-go's existing RTK
compactor (`internal/tools/`), replacing the flat serial stage list with a
two-trait (Reformat + Offload) bloat-gated orchestrator, adaptive sizing
(Kneedle), line importance detection, CCR reversibility, and content-type-based
routing — with byte-for-byte parity against headroom's test fixtures.

## Why redo (not resume)

The previous attempt's deliverables never reached the tree, despite an
"all gates pass" summary. Likely cause: subagent worktrees were discarded or
work was never merged back. Recommendation in the failure summary is to either:

(a) Re-run the same slices 1–9 in worktrees and explicitly merge each branch.
(b) Implement serially in a single branch with explicit per-slice commits.

This spec should pick an explicit path and enforce it via gates that
**actually exercise the new code**, not just confirm absence-of-regression.

## Constraints (carry-over)

- No new external Go deps — stdlib only (`crypto/sha256`, `compress/flate`,
  `encoding/json`, `regexp`, `sync`).
- SHA-256 (not BLAKE3) for CCR keys — documented deviation.
- `strings.Contains` (not aho-corasick) for keyword detection — stdlib only.
- All slices in package `internal/tools`.
- Breaking config change allowed — old configs need migration, document in
  changelog.
- Reference `tmp/headroom/crates/headroom-core/src/{transforms,signals}/` for
  exact algorithm behavior.
- Parity fixtures already present at
  `internal/tools/testdata/parity/{content_detector,diff_compressor,log_compressor,smart_crusher}/`
  (85 JSON files, ~436 KB, currently orphaned) — must be wired into tests or
  deleted.