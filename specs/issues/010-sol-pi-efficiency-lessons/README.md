# Issue 010: SoL-Pi Efficiency Lessons — Archive-Before-Reduce and Unblocked Fixes

**Status:** design + plan, awaiting review. No `PROMPT.md` yet — that is written
after the plan is approved.

## Why this spec exists

`tmp/SoL-Pi` is an NVIDIA standalone extension for the Pi (TypeScript) agent
harness that packages four token-efficiency mechanisms found by scaled
auto-research. We read its source to ask one question: **which of its design
*commitments* does pi-go not already have?**

pi-go is not behind on capability. It already ships mechanisms SoL-Pi does not
have at all:

| Capability | pi-go | SoL-Pi |
|---|---|---|
| Deterministic log compactor (no model call) | ✅ `internal/tools/compactor*.go` | ❌ |
| Byte-identical result deduper | ✅ `internal/tools/dedup.go` | ❌ |
| Superseded-result shedding | ✅ `internal/session/compaction_shed.go` | ❌ |
| Window resolution from model catalog | ✅ `internal/ctxwindow` | ❌ |

What pi-go lacks is two **architectural commitments**:

1. **A separation between durable history and the model-visible projection.** pi-go
   collapses them and then destroys the record to shrink the view
   (`internal/session/store.go:1291` `rewriteEvents` truncates `events.jsonl` via
   temp-file + rename). SoL-Pi never edits history; it rewrites only at
   `pi.on("context")` and keeps the original bytes recallable.
2. **A reduction that can be rejected rather than a mutation already applied.**
   SoL-Pi's reducer returns `undefined` on every failure path and the original
   tool result proceeds untouched. pi-go's compactor mutates the result map in
   place (`internal/tools/compactor.go:119` `applyCompaction`), with no
   rejection point and no retained original.

This spec covers the five items that are **unblocked, independently testable,
and mostly additive**. It deliberately excludes the three that are blocked or
belong to other efforts — see "Out of scope".

## Scope

| # | Item | Blocked by | Risk | Ticket |
|---|---|---|---|---|
| 1 | Archive-before-reduce + readback pointer | nothing | medium | `plan.md` §T1 |
| 2 | **7 of 9 compactor pipelines are dead** | nothing | low | `plan.md` §T2 |
| 3 | Make reduction rejectable, not mutating | nothing | low | `plan.md` §T3 |
| 4 | Wire the dead measurement surfaces | nothing | low | `plan.md` §T4 |
| 5 | Cache reporting on the Ollama path | upstream | medium | `plan.md` §T5 |

Item 1 is the only genuinely *architectural* change; items 2–4 are prerequisite
bug fixes and wiring that make item 1 (and everything later) verifiable.

**Item 2 grew during authoring.** It began as "three line-level fixes" for
grep/git tool-name mismatches. Empirical verification (R2.4 in `research.md`)
showed **seven of nine registered compaction pipelines are no-ops in
production** — they read a `result["output"]` key that no tool's output struct
produces. This is a latent subsystem failure, not a typo, and it means the
compactor's real contribution today is `bash` and `read` only.

## Out of scope

| Excluded | Why |
|---|---|
| Compaction **economics / breakeven gate** | Blocked on item 5. `AutoCompactConfig.Decide` (`internal/session/compaction.go:123-137`) reads only `bodyTokens` and `windowSize`; adding a price gate before cache numbers exist would price on wrong inputs for 73% of traffic. |
| **Semantic** (plan-phase) compaction trigger | Needs item 3 (rejectable reduction) and item 5 first. Also: pi-go's turn-level constraint is legitimate — `PreTurnHook`'s comment (`internal/agent/agent.go:377-381`) explains mid-turn rewriting "would orphan a tool call from its result." |
| Tool-call **batching** | Already owned by `specs/issues/token-cost/01-batching.md`; measured at 48–65% and a much larger win than any fusion approach. |
| Cap `read` on source files; wire `FileContentCache` | Already owned by `specs/issues/token-cost/04-bugs.md` §4b/4c. |
| Action Fusion (fused `edit`+`then_run`) | Rejected on the merits, not deferred. pi-go's own measurement: 81.8% of tool-issuing turns make exactly one call; batching beats per-call fusion, and fusion carries a TOCTOU guard pi-go avoids by not having the feature. |
| SoL-Pi's TUI savings display | Cosmetic; creates pressure to over-report. |

## Relationship to existing specs

`specs/issues/token-cost/` is the authoritative measurement effort (1,404
sessions, 2.5B prompt tokens, 201:1 ratio). This spec does **not** re-derive
those numbers; it references them. Two overlaps are handled by reference rather
than duplication:

- **Item 5** expands on `token-cost/02-cache-reporting.md` with the exact code
  anchors needed to implement it. Sequencing follows `token-cost/08-sequencing.md`.
- **Item 4** overlaps `token-cost/07-measurement.md` recommendation 3; this spec
  scopes it to the two surfaces that are wired-but-dead, not the `pi tokens`
  command.

## Documents

| File | Contents |
|---|---|
| `research.md` | Verified gap analysis, every claim anchored to `file:line` |
| `design.md` | The two-layer architecture; per-item design and the decisions required |
| `plan.md` | Implementation tickets, in dependency order |

## Verification status of this spec

All `file:line` anchors in `research.md` were read directly from the tree during
authoring. The R2 finding was additionally **verified by executing the real
`BuildCompactorCallback` against real production result shapes** — the probe and
its output are reproduced in `research.md` §R2.4. That probe was temporary and
has been removed; no code change was left behind, and no build or test of the
repository was run.
