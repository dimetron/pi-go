# Requirements

## Context

pi-go is a Go coding agent built on `google.golang.org/adk/v2` with multi-provider LLM support.
Google ADK Go v2 ships a built-in `geminitool.GoogleSearch` server-side tool that, when added to a
Gemini request, lets Gemini 2+ automatically invoke Google Search to ground answers with citations.

Today (HEAD of `feature/gemini-adk-grounding`) pi-go does **not** use `geminitool` at all. The
Gemini provider (`internal/provider/gemini.go`) only constructs a `gemini.NewModel` and exposes
no way to enable search grounding. This spec adds that capability.

## Questions & Answers

### Q1. When should Gemini search grounding be enabled?

This is the core scope decision. The grounded tool is provided by Gemini (Google's backend), so
it is essentially free per-call (no per-search fee on Gemini side) but increases latency and
sometimes changes model behavior. There are four reasonable opt-in shapes:

- **A. Always on for Gemini** (recommended) — Whenever the active model is a Gemini model, the
  `google_search` tool is registered automatically. No flag, no config. Simple, predictable,
  matches Gemini's "grounded generation" default story.
- **B. CLI flag only** — Add a `--google-search` / `--grounding` boolean flag (and a config
  counterpart) that is `false` by default. User opts in per-invocation.
- **C. Config-only default** — Add a config key like `providers.gemini.grounding: true|false`
  in `.pi-go/config.json`, defaulting to off. CLI flag overrides config.
- **D. Hybrid** — Config default + CLI flag override. Most flexible but most surface area.

**A. Always on for Gemini** — simplest, smallest diff, matches the way `geminitool.GoogleSearch`
is documented ("automatically invoked by Gemini 2 models to retrieve search results").
Users who want to disable it can switch provider.

**B. CLI flag only** — gives users explicit control but adds two flags and forces every user
to discover and pass them; first-time UX is worse.

**C. Config-only** — discoverable for power users, invisible to others, no per-command friction.

**D. Hybrid** — overkill for a tool with no usage cost beyond latency.

**Chosen: A** — always on for Gemini. No flag, no config. If the active model is a
Gemini model (detected via `provider.Info.Provider == "gemini"`), the
`geminitool.GoogleSearch` tool is added to the agent's `Tools` slice.

### Q2. Should the tool be available to subagents too, or only the top-level agent?

The Gemini provider is shared by the main agent and the spawned subagent processes. The grounding
tool is a model-side feature, so it can be enabled per-LLM-call. Two options:

- **A. Main agent only** — Grounding only registered for the top-level `agent.New(agent.Config)`
  call. Subagents stay lean.
- **B. Both** — Every LLM call in the process gets the grounding tool. More consistent, but
  increases per-subagent latency.

**Chosen: B (revised)** — both main agent and subagents. Every `agent.New(agent.Config{...})`
call in the process receives the grounding tool when the LLM is Gemini-backed, so the
top-level loop **and** every spawned subagent loop can use grounded answers.

### Q3. Should there be a config/CLI escape hatch to disable it for Gemini?

If Q1 = A, this is essentially asking "do we need the negative opt-out?". Two options:

- **A. No escape hatch** — Simplest. If you want no grounding, pick a different provider.
- **B. Provide a disable** — `providers.gemini.grounding: false` config key or `--no-grounding`
  flag. Safer for users on metered/flaky networks.

**Chosen: B (revised), narrowed to env var only.** No config key, no CLI flag. A single
environment variable disables the entire feature. The exact semantics of that env var are
decided in Q4 below.

### Q4. What is the env var and its semantics?

- **A. `PI_NO_GROUNDING=1` disables for all agents (recommended)** — one var, off-on switch.
  Empty/unset = enabled (current default). `=1` / `=true` = disabled. Applied to **every**
  Gemini-backed `agent.New` call (main + subagents).
- **B. `PI_NO_GROUNDING=1` disables for subagents only** — main loop keeps grounding on, only
  subagents skip it. Saves per-subagent latency, slightly inconsistent.
- **C. `PI_GROUNDING=off` opt-out with explicit on/off** — three-state (`on`/`off`/unset).
  More expressive but `unset=on` is surprising for an "off" var.
- **D. `PI_GEMINI_GROUNDING=0` zero/one style** — alternative naming convention.

**Chosen: A** — `PI_NO_GROUNDING=1` disables for all agents (main + subagents). Unset / empty
= enabled. This is the conventional "PI_*" env-var pattern used elsewhere in the project
(e.g. `PI_*` for pi-go control vars; the same pattern as `GOOGLE_API_KEY` / `GEMINI_API_KEY`
for credentials but inverted naming for an opt-out).

## Summary

- When the active LLM is a Gemini model **and** `PI_NO_GROUNDING` is not set to a truthy value
  (`1`, `true`, `yes`, `on` — case-insensitive), the `geminitool.GoogleSearch` tool is appended
  to the `Tools` slice of every `agent.New(agent.Config{...})` call in the process — the main
  agent and every spawned subagent.
- The check is `info.Provider == "gemini"` (or equivalent model-name prefix detection for
  subagents that resolve their own model). Non-Gemini LLMs are unaffected.
- The `PI_NO_GROUNDING` env var is a process-wide kill switch. Empty / unset = enabled.
  Set to a truthy value to disable for all Gemini-backed agents in the process.
- No new CLI flag, no new config key, no new `agent.Config` field.



