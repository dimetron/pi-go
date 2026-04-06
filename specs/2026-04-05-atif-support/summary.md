# Project Summary: ATIF Export Support for pi-go

## Artifacts Created

| File | Description |
|---|---|
| `specs/2026-04-05-atif-support/rough-idea.md` | Initial concept |
| `specs/2026-04-05-atif-support/idea-honing.md` | Requirements Q&A (10 questions) |
| `specs/2026-04-05-atif-support/research/atif-specification.md` | ATIF v1.6 spec research |
| `specs/2026-04-05-atif-support/research/pi-go-architecture.md` | Pi-go codebase analysis |
| `specs/2026-04-05-atif-support/design/detailed-design.md` | Comprehensive design document |
| `specs/2026-04-05-atif-support/implementation/plan.md` | 7-step implementation plan with checklist |
| `specs/2026-04-05-atif-support/summary.md` | This document |

## Key Design Decisions

- **Export only**, automatic, always-on — every session produces `trajectory.atif.json`
- **Incremental writes** via atomic temp-file-rename after each event
- **New `internal/atif/` package** with types, converter, and writer (no external dependencies)
- **Integration point**: `FileService.AppendEvent` — after JSONL persist, feed ATIF writer
- **Subagent support**: Separate ATIF files linked via `subagent_trajectory_ref`
- **Non-fatal**: ATIF errors logged but never break sessions
- **ATIF v1.6** compliance, core fields only (no metrics/token IDs initially)
- **First Go implementation** of ATIF

## Implementation Overview

7 incremental steps, each building on the previous:

1. **Data model** — Go structs mirroring ATIF v1.6 schema
2. **Converter** — `session.Event` → ATIF `Step` mapping logic
3. **Writer** — Incremental writer with atomic flush
4. **Integration** — Wire into `FileService.AppendEvent`
5. **Resume** — Rebuild ATIF on session load from existing events
6. **Subagents** — Link subagent trajectories via `subagent_trajectory_ref`
7. **Validation** — End-to-end test with schema compliance checks

## Next Steps

1. Review the detailed design at `specs/2026-04-05-atif-support/design/detailed-design.md`
2. Review the implementation plan at `specs/2026-04-05-atif-support/implementation/plan.md`
3. Begin implementation following the checklist

## Areas for Future Refinement

- **Metrics support**: Add token counts, cost tracking per step (requires provider-level instrumentation)
- **Reasoning content**: Capture model thinking/reasoning blocks when available
- **Tool definitions**: Populate `agent.tool_definitions` from registered tool schemas
- **ATIF import**: Ingest external ATIF files for replay or analysis
- **Schema validation**: Integrate formal JSON schema validation if Harbor publishes one
