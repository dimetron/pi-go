# Requirements

## Purpose

Add a **phase checklist** to the right sidebar in Plan mode that shows the progress
of the `/plan` PDD SOP. When the user runs `/plan`, the sidebar should display the
7 PDD phases with the current phase highlighted and completed phases checked off,
so the user can see at a glance how far the planning session has progressed.

## Questions & Answers

### Q1: What should the phase checklist show?
**A:** All 7 PDD phases as a static checklist, with the current phase highlighted
and completed phases checked off.

### Q2: How is the "current phase" determined?
**A:** **Detected from spec artifacts** — scan the spec directory for which files
exist. The current phase is the first one whose artifact is missing. Self-correcting
and resume-friendly.

### Q3: Where should the checklist go?
**A:** **Inside the existing right sidebar** — add a "Plan" section (mirroring the
existing `/run` checklist section). No layout change.

### Q4: What labels for the phases?
**A:** All 7 phases, short labels: Idea, Requirements, Research, Design, Outline,
Plan, Prompt.

### Q5: How is the current phase visually indicated?
**A:** A distinct marker (e.g., `▶`) on the current phase; completed phases as
`[x]`, future phases as `[ ]`.

### Q6: How does the checklist get the spec directory?
**A:** Use the existing `m.planWorktreePath` + `m.planTaskName` (already set in
`startPlanSession`). No new plumbing.

### Q7: How should the checklist refresh?
**A:** **Re-scan on every render** — cheap `os.Stat` calls, mirrors the existing
`/run` checklist behavior.

## Scope

- **In scope:** `internal/tui` package only. Add a "Plan" section to the right
  sidebar that renders the 7-phase checklist when in plan mode. Detect phase
  completion by scanning the spec directory for artifact files.
- **Out of scope:** No layout changes to the sidebar width or panel structure. No
  changes to the `/plan` command flow or the PDD SOP itself. No changes to `/run`.

## Phase → Artifact Mapping

| # | Phase    | Artifact to detect |
|---|----------|--------------------|
| 1 | Idea     | `rough-idea.md`    |
| 2 | Requirements | `requirements.md` |
| 3 | Research | `research/` (dir)  |
| 4 | Design   | `design.md`        |
| 5 | Outline  | `outline.md`       |
| 6 | Plan     | `plan.md`          |
| 7 | Prompt   | `PROMPT.md`        |

## Constraints

- The checklist must only render in plan mode (`m.mode == "plan"`).
- The current phase is the first phase whose artifact is missing; if all artifacts
  exist, all phases are checked (plan complete).
- The sidebar width (23) must not change; labels must fit.
- Re-scan the spec directory on every render (cheap `os.Stat` calls).
- Must not break the existing `/run` checklist rendering.

## Acceptance Criteria

- Given the user is in plan mode, when the sidebar renders, then a "Plan" section
  shows all 7 phases with the current phase marked `▶` and completed phases `[x]`.
- Given a phase's artifact file exists, when the sidebar renders, then that phase
  is shown as completed (`[x]`).
- Given the current phase's artifact is missing, when the sidebar renders, then
  that phase is shown as current (`▶`) and later phases as `[ ]`.
- Given all 7 artifacts exist, when the sidebar renders, then all phases are shown
  as completed.
- Given the user is NOT in plan mode, when the sidebar renders, then no "Plan"
  section is shown.
- Given the spec directory cannot be read, when the sidebar renders, then the
  checklist degrades gracefully (no crash; phases shown as incomplete).
