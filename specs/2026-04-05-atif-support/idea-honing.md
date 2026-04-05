# Idea Honing: ATIF Support

Requirements clarification through interactive Q&A.

---

## Q1: What is the primary goal for ATIF support?

Pi-go currently stores sessions as proprietary JSONL events. ATIF support could serve several purposes:

- **Export only**: Convert existing sessions to ATIF for analysis/training pipelines
- **Import only**: Ingest ATIF from other agents for replay/analysis in pi-go
- **Both import and export**: Full bidirectional ATIF support
- **Native format**: Use ATIF as the primary session storage format (replacing JSONL)

Which direction are you thinking?

**A1:** Export only — convert existing pi-go sessions to ATIF format for external consumption (analysis, training pipelines, visualization).

---

## Q2: How should the export be triggered?

There are several ways users could invoke ATIF export:

- **CLI command**: e.g. `pi-go export --format atif --session <id>` (explicit, on-demand)
- **Automatic**: Every session automatically writes an ATIF file alongside the JSONL events
- **TUI slash command**: e.g. `/export atif` from within the interactive TUI
- **Combination**: CLI command + slash command, but not automatic

Which approach fits your workflow?

**A2:** Automatic — every session automatically writes an ATIF file alongside the JSONL events, no manual export step needed.

---

## Q3: Where should the ATIF file be written?

Since it's automatic, the ATIF output needs a predictable location. Options:

- **Alongside session files**: `~/.pi-go/sessions/<session-id>/trajectory.atif.json` (co-located with existing JSONL)
- **Dedicated directory**: `~/.pi-go/trajectories/<session-id>.atif.json` (separate from session internals)
- **Project-local**: `.pi-go/trajectories/<session-id>.atif.json` in the working directory (close to the code being worked on)

Which location makes the most sense?

**A3:** Alongside session files — `~/.pi-go/sessions/<session-id>/trajectory.atif.json`, co-located with the existing JSONL events.

---

## Q4: When should the ATIF file be written — incrementally or at session end?

Two approaches for automatic writing:

- **Incremental (append)**: Write/update the ATIF file after each step (tool call, response, etc.). Ensures data is captured even if the session crashes, but more I/O and the file may be in a partial state during the session.
- **At session end**: Write the complete ATIF file once when the session finishes/closes. Simpler, always a valid complete document, but data is lost if the process crashes.
- **Hybrid**: Write incrementally to a temp file, then finalize at session end. Best of both worlds but more complex.

Which approach do you prefer?

**A4:** Incremental — write/update the ATIF file after each step so data is preserved even if the session crashes.

---

## Q5: Which ATIF fields should pi-go populate?

ATIF has many optional fields. Given pi-go's current data model, here's what's feasible:

**Definitely mappable now:**
- `schema_version`, `session_id`, `agent` (name, model_name)
- `steps` with `step_id`, `timestamp`, `source`, `message`
- `tool_calls` (function_name, arguments)
- `observation` (tool results)

**Possible with some work:**
- `metrics` (prompt_tokens, completion_tokens) — requires capturing from LLM provider responses
- `reasoning_content` — if the model returns thinking/reasoning blocks
- `tool_definitions` — from registered tool schemas
- `final_metrics` — aggregate token/cost totals

**Not feasible without significant effort:**
- `token_ids`, `logprobs` — requires raw API response data most providers don't expose
- `cost_usd` — requires per-model pricing tables

Should we aim for the "definitely mappable" fields first and treat metrics/reasoning as stretch goals, or is token metrics tracking a hard requirement?

**A5:** Start with the definitely mappable fields only. Metrics, reasoning, and token IDs are out of scope for the initial implementation.

---

## Q6: How should subagent trajectories be handled?

Pi-go has a subagent system (explore, plan, reviewer, etc.). ATIF supports multi-agent via `subagent_trajectory_ref` in observations. Options:

- **Ignore subagents**: Only export the main agent's trajectory, subagent interactions appear as opaque tool call/results
- **Separate files**: Each subagent gets its own ATIF file, linked via `subagent_trajectory_ref` from the parent
- **Inline**: Embed subagent steps directly in the parent trajectory (not standard ATIF, but simpler)

Which approach?

**A6:** Separate files — each subagent gets its own ATIF trajectory file, linked from the parent trajectory via `subagent_trajectory_ref`.

---

## Q7: Should the ATIF export be configurable (enable/disable)?

Since it's automatic, there's a question of overhead and user preference:

- **Always on**: Every session produces ATIF, no config needed. Simple, predictable.
- **Config toggle**: A setting in pi-go config (e.g. `atif.enabled: true`) to enable/disable. Default on or off.
- **Per-session flag**: A CLI flag like `--atif` to opt in per session.

What's your preference?

**A7:** Always on — every session automatically produces an ATIF trajectory file, no configuration needed.

---

## Q8: Which ATIF schema version should pi-go target?

The spec is at v1.6 with multimodal content support. Options:

- **v1.6 (latest)**: Full spec compliance including multimodal ContentPart arrays. Future-proof but pi-go may not have multimodal content to populate.
- **v1.5**: Includes `tool_definitions` and `is_copied_context` but without multimodal complexity. Practical since pi-go is text-only.
- **v1.0 (minimal)**: Core fields only. Simplest to implement but may limit interoperability with newer tooling.

Which version to target?

**A8:** Target v1.6 (latest) for full spec compliance and future-proofing. Multimodal ContentPart arrays will be supported in the schema even if pi-go currently only produces text content.

---

## Q9: Should the ATIF output include session branching?

Pi-go supports session branching — users can explore alternative paths from a point in the conversation. ATIF doesn't have a native branching concept. Options:

- **Main branch only**: Export only the active/final branch as a single linear trajectory
- **Branch per file**: Each branch gets its own ATIF file, linked via `continued_trajectory_ref`
- **Flatten**: Include all branches sequentially with branch metadata in `extra` fields

Which approach?

**A9:** Main branch only — export the active/final branch as a single linear trajectory. Alternative branches are not included.

---

## Q10: Are there any specific consumers or downstream systems you have in mind for the ATIF output?

Understanding who will read these files helps prioritize correctness. For example:

- Harbor SFT/RL training pipelines
- AgentLens visualization
- Custom analysis scripts
- General interoperability / future-proofing

Or is this more about standardization for its own sake?

**A10:** General interoperability — the goal is to produce standard-compliant ATIF so pi-go trajectories can be consumed by any ATIF-compatible tool or pipeline.

