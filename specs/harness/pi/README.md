# Pi Harness (`earendil-works/pi`) vs pi-go — Comparative Review

> **Reviewed**: `tmp/harness/pi` @ working tree (pi.dev, `pi-monorepo` v0.0.3, 13 packages)
> **Against**: `pi-go` @ `24d1f2b` (`main`)
> **Date**: 2026-10-02
> **Method**: package-by-package read of both trees. Line counts measured with
> `find | wc -l`, not estimated. Provider/tool/command counts extracted from source
> (`NewLLM` switch, `BUILTIN_SLASH_COMMANDS`, `builtinProviders()`), not from docs.
> **Verification commands**: [`99-verification.md`](99-verification.md)

These are **not the same project**. `tmp/harness/pi` is the upstream TypeScript
monorepo behind pi.dev (`@earendil-works/*`). pi-go is an independent Go
implementation inspired by the same concept, which uses Google ADK Go for its
agent loop. Neither is a fork of the other; pi-go's `openai_codex.go` cites
*"pi-mono's openai-codex-responses provider"* as a reference, so the lineage runs
one way.

## Documents

| File | Covers |
|---|---|
| [00-summary.md](00-summary.md) | Executive summary, at-a-glance, the master feature table |
| [01-agent-runtime-and-sessions.md](01-agent-runtime-and-sessions.md) | Agent loop, steering, session format, compaction, durability, crash recovery, ATIF |
| [02-providers-catalog-auth.md](02-providers-catalog-auth.md) | 42 vs 10 providers, model catalog, prompt caching, thinking levels, auth, config |
| [03-tools-extensions-security.md](03-tools-extensions-security.md) | Tool inventory, extension API, hooks, skills, MCP, sandbox, permissions |
| [04-tui.md](04-tui.md) | Renderer, markdown, keybindings, editor, images, themes, screen modes |
| [05-subagents-multi-agent.md](05-subagents-multi-agent.md) | Subagent contract, worktrees, concurrency, bundled agents, external CLIs |
| [06-evals-observability-packaging.md](06-evals-observability-packaging.md) | Evals, quality gates, telemetry, CLI surface, release, docs |
| [07-gaps-and-recommendations.md](07-gaps-and-recommendations.md) | Ranked gap analysis with concrete remediation |
| [99-verification.md](99-verification.md) | Every count in this report, and the command that produced it |

## The structural inversion

```
 pi-go (Go)                                pi (TypeScript)
┌──────────────────────────────┐      ┌────────────────────────────────────┐
│ cmd/pi → internal/cli        │      │ coding-agent (84.7k LOC, 43%)       │
│   tui (bubbletea v2, 19.9k)  │      │   interactive · rpc · print        │
│     chat·sidebar·status      │      │   core/{tools,compaction,ext}      │
│   agent (ADK v2 loop, 1.8k) │─────▶│ agent-core (2.5k, own loop)        │
│     tools (registry, 11.5k)  │      │   extensions/{mcp,codemode,llama}  │
│     extension (hooks, 2.4k) │      │ tui (19.2k, differential rend.)    │
│   provider (10 keys, 8.8k)  │      │ ai (26.3k, 42 providers)           │
│   session (JSONL, 3.4k)     │      │   durable (17.7k, crash-safe)      │
│   subagent (3.5k, 18 agents)│      │   chord (8.8k, CRDT composition)    │
│ piagent/ + pimodels/ (SDK)  │      │   protocol+client+server (CBOR)     │
└──────────────────────────────┘      └────────────────────────────────────┘
```

**pi-go is TUI-first with a thin runtime.** `internal/tui` (19.9k LOC) is the
largest subsystem; the agent loop is 1.8k lines of configuration over ADK.

**pi is runtime-first with a thin TUI.** `packages/agent` is 2,513 LOC and
`packages/tui` is 19,182 LOC, but `coding-agent` at 84.7k LOC is 43% of the
monorepo — mostly `modes/interactive` (21.7k) and 69.5k of tests.

The consequence: pi-go's extension surface is two shell hook events; pi's is 36
typed events with an in-process registration API. pi-go's session log is a plain
append-only JSONL with no recovery; pi's `durable` package commits tool intent
before execution and resumes mid-turn.