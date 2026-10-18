# 00 — Executive Summary

## Verdict in one paragraph

Both are competent coding agents. They make opposite bets. **pi-go bets on
reach and operator ergonomics** — 29 tools against pi's 8, 10 providers plus
first-class Ollama, a subagent orchestrator with 18 bundled agents and git
worktree isolation, an OS-level sandbox profile, an LLM-robustness layer, and
production observability. **pi bets on a durable, extensible runtime** — 42
providers, a 36-event extension API with in-process registration, a crash-resume
harness that commits tool intent before executing it, a CRDT composition
runtime, and a QuickJS sandbox where the model writes code instead of chaining
tool calls. If you want pi-go to close the gap to pi, the work is not
"add more providers" — it is **crash recovery** and **an extension API**.

## At a glance

| | pi-go | pi (earendil-works) |
|---|---|---|
| Language / runtime | Go 1.27, static binary | TypeScript 7.0.2, Node ≥22.19 |
| Module / package count | 1 module, **41** `internal/` packages | **13** workspace packages |
| Non-test LOC | ~92k Go | ~149k TS |
| Test LOC | ~72k Go | ~110k TS |
| Agent loop | ADK v2 `runner.Run` (delegated) | own 940-line two-level loop |
| Agent loop LOC | 1,761 (config only) | 2,513 |
| TUI LOC | 19,923 (bubbletea v2) | 19,182 (own differential renderer) |
| Model-facing tools | **~29** | **8** (+ codemode, tool-search, llama, MCP) |
| LLM providers | **10** | **42** |
| OAuth flows | **1** (codex) | **9** |
| Slash commands | **24** (27 registered, 3 hidden) | **24** |
| Subagent status | built-in tool, **18** bundled agents | example extension, 4 samples |
| Worktree isolation | **yes**, with `base:` diff trick | none |
| Crash recovery | **none** | commit-before-execute, replay-aware |
| Session schema version | **none** | `CURRENT_SESSION_VERSION = 3` + migrations |
| Extension events | **2** (shell hooks) | **36** (in-process) |
| Per-tool approval | none | none (documented as by design) |
| OS sandbox | **yes** (`sandbox-exec` Seatbelt profile) | delegated to docs + examples |
| MCP | client + server (official Go SDK) | hand-rolled **client only** |
| MCP conformance | none | official suite, 3 protocol versions, baselined |
| Prompt-injection scanner | **yes** (`internal/audit`) | none |
| Container orchestration | **yes** (kagent, GHCR) | no |
| Browser TUI | **yes** (xterm.js + pairing auth) | no |
| Voice | **yes** (Gemini Live) | no |
| Telemetry | OTel SDK + Prometheus + pprof | vendor-neutral **port** + conformance tests |
| Docs | ~40 md + code comments | **40** user docs shipped in the npm tarball |

## Master feature table

Grouped by domain. **✓** = present and comparable, **△** = present but
materially weaker, **✗** = absent, **＋** = present on this side only.

### Agent runtime & sessions

| Feature | pi-go | pi |
|---|---|---|
| Own agent loop | ✗ (ADK) | ✓ |
| Steering (inject mid-turn) | ✗ | ✓ |
| Follow-up queue | ✗ | ✓ |
| Continue-on-stop decision hook | ✗ | ✓ |
| Truncated-tool-call guard | ✗ | ✓ |
| Tool-declaration delta reconciliation | ✗ | ✓ |
| Session persistence | ✓ JSONL | ✓ JSONL |
| Session schema version + migrations | ✗ | ✓ v3 |
| Tree structure in the log | ✗ (integer indices) | ✓ (`parentId`) |
| Session fork / clone | ✗ | ✓ |
| Compaction | ✓ two-stage | ✓ two-stage |
| Compaction is undoable / logged | ✗ destructive | ✓ append-only entry |
| `context_edit` (rewrite projection, not log) | ✗ | ✓ |
| Branch summary on abandon | ✗ | ✓ |
| Crash recovery mid-turn | ✗ | ✓ |
| Tool replay policy (`replay: safe`) | ✗ | ✓ |
| Read-before-write rule | ✗ | ✓ |
| Concurrency contract (`ConversationBusy`) | ✗ | ✓ |
| Versioned documents w/ migrate hooks | ✗ | ✓ |
| Durable tasks w/ checkpoint phases | ✗ | ✓ |
| Trajectory interchange format | ✓ ATIF v1.6 | ✗ |

### Providers & LLM layer

| Feature | pi-go | pi |
|---|---|---|
| Provider count | 10 | **42** |
| Native Ollama provider | ✓ | ✗ (OpenAI-compat only) |
| Wire-API-per-model routing | △ (per route) | ✓ (`model.api`) |
| Bedrock / Vertex | ✗ | ✓ |
| Vision (image input) | ✓ (`read_image`) | ✓ (native in `read`) |
| Image generation | ✗ | ✓ |
| Classifier models | ✗ | ✓ (3 APIs) |
| Model catalog | △ union of 3, no schema | ✓ generated + sha256 drift gate |
| Catalog drift detection | △ (test asserts file exists) | ✓ (fails the build) |
| Prompt caching | ✓ Anthropic only | ✓ Anthropic + emulated + Mistral |
| 1h cache-write accounting | ✗ | ✓ |
| Per-model thinking level defaults | ✗ | ✓ |
| Thinking-level clamping | ✓ measured matrix | ✓ generated map |
| OAuth flows | 1 | **9** |
| Credential refresh / rotation | ✗ | ✓ double-checked |
| Proactive rate limiting | ✓ per provider\|model\|baseURL | ✗ |
| Daily token budget guard | ✓ | ✗ |
| Retry classification (terminal/transient) | ✓ shared, 2 layers | △ ad hoc per API |
| Virtual models / routing | ✗ | ✓ |
| Cost always materialized | △ on-demand | ✓ |

### Tools & extensibility

| Feature | pi-go | pi |
|---|---|---|
| Core tools | **16** | 8 |
| LSP tools | ✓ 7 | ✗ |
| Memory tools | ✓ 3 | ✗ |
| Git tools | ✓ 3 | ✗ |
| MCP tool exposure | ✓ | ✓ |
| Tool param coercion / aliases | ✓ (read has 12) | ✗ |
| Strict declaration / lenient runtime split | ✓ | ✗ |
| Output compaction pipelines | ✓ 9 | ✗ |
| Result dedup | ✓ | ✗ |
| Read-before-write ledger | ✓ | ✗ |
| Secret redaction in output | ✓ regex | △ 0o600 temp files |
| Head/tail truncation choice | ✗ head only | ✓ head/tail/middle |
| Temp-file spill for large output | ✗ | ✓ |
| Bash as an injectable interface | ✗ | ✓ `BashOperations` |
| In-context code execution | ✗ | ✓ QuickJS codemode |
| BM25 tool discovery | ✗ | ✓ |
| Tool exposure tiers | ✗ | ✓ 5 levels |
| Extension registration API | ✗ | ✓ ~28 methods |
| Extension events | 2 (shell) | **36** (in-process) |
| Hook can block a tool | ✗ (logs and continues) | ✓ |
| Hook can rewrite tool args | ✗ | ✓ |
| Hook can rewrite tool results | ✗ | ✓ |
| UI extension hooks | ✗ | ✓ 30 |
| Skills | ✓ progressive disclosure | ✓ progressive disclosure |
| Skill prompt-injection scanner | ✓ blocking | ✗ |
| Plugin marketplace | ✓ git-based | ✓ npm/git/local |
| Prompt-injection scanner | ✓ | ✗ |
| Tool-call approval gate | ✗ | ✗ |

### Terminal UI

| Feature | pi-go | pi |
|---|---|---|
| Differential rendering | ✗ (bubbletea) | ✓ |
| Synchronized output (CSI 2026) | ✗ | ✓ |
| Bounded writes / surrogate-safe chunking | ✗ | ✓ |
| Alt-screen / fullscreen mode | ✗ | ✓ hot-swappable |
| Named keybinding registry | ✗ (~10 hard-coded) | ✓ 45 IDs + conflicts |
| Multiline editor | ✗ (single-line) | ✓ |
| Kill ring / undo | ✗ | ✓ |
| Grapheme / CJK aware input | ✗ | ✓ `Intl.Segmenter` |
| Inline images (Kitty/iTerm2) | ✗ | ✓ |
| Clipboard image + modifier keys (native) | ✗ | ✓ N-API |
| LaTeX rendering | ✗ | ✓ |
| Markdown | ✓ glamour | ✓ marked + custom renderer |
| Syntax highlighting | ✓ chroma (in glamour) | ✓ highlight.js (injectable) |
| Click-to-open links | ✗ (OSC 8 emit only) | ✓ |
| Render cache | ✓ FNV-1a allocation-free | △ per-component |
| Perceptual color (OKLCH) | ✗ | ✓ |
| Sidebar / minimap | ✓ | ✗ |
| Mascot / easter eggs | ✓ 7 moods | ✓ 3D ray-caster |
| Mermaid | ✓ own engine (~17.5k) | △ npm dep |
| Context gauge | ✓ | ✓ |

### Multi-agent

| Feature | pi-go | pi |
|---|---|---|
| Single / parallel / chain modes | ✓ | ✓ |
| Bundled agents | **18** | 4 (samples) |
| Git worktree isolation | ✓ | ✗ |
| `base:` so merged work shows as diff | ✓ | ✗ |
| Stash by message + oid re-verify | ✓ | ✗ |
| Concurrency pool (process-wide) | ✓ default 3 | ✗ (per-call only) |
| Per-nesting-depth budget | ✓ halved | ✗ |
| Env allowlist for children | ✓ | ✗ |
| Spawn retry | ✓ 3 attempts, classified | ✗ |
| Timeout + typed timeout error | ✓ | ✗ |
| External CLI agents (ACP/Codex) | ✓ 7 agents, 3 transports | ✗ |
| Project-agent trust gate | ✗ | ✓ |
| Trajectory linkage | ✓ ATIF ref | ✗ |
| Workflow engines (`/run`, `/plan`) | ✓ SOP DSL | ✗ prompt presets |

### Infrastructure

| Feature | pi-go | pi |
|---|---|---|
| MCP client / server | ✓ / ✓ | ✓ / ✗ |
| ACP client / server | ✓ / ✓ | ✗ |
| A2A server | ✓ | ✗ |
| Remote session protocol | ✗ | ✓ CBOR + client/server |
| Browser TUI | ✓ | ✗ |
| Voice | ✓ Gemini Live | ✗ |
| Local semantic memory | ✓ SQLite FTS | ✗ |
| Semantic palace + embeddings + KG | ✓ | ✗ |
| Tool-output compaction | ✓ | ✗ |
| Eval harness | ✓ ATIF + coverage matrix | ✓ paired lift |
| Eval in CI | ✗ | ✗ |
| Vulnerability gate | ✓ vulngate (fail on fix) | △ npm audit daily |
| SBOM + provenance attestation | ✓ Sigstore | ✗ |
| PGO | ✓ recorded | ✗ |
| pprof | ✓ | ✗ |
| Windows test CI | ✓ | ✗ |
| Cross-arch release | ✓ GoReleaser | ✓ 6 bun binaries |
| Public embeddable SDK | ✓ piagent+pimodels | ✓ packages |

## Where each side leads

**pi-go leads on** tool breadth (29 vs 11), subagents (orchestrator + 18
bundled + worktree isolation + concurrency budget vs an example extension),
LLM robustness (param aliases, coercion, strict/lenient split), security posture
(Seatbelt profile, injection scanner, prompt-injection defense), and operational
depth (kagent, browser TUI, voice, memory palace, pprof, PGO, SBOM, attestation).

**pi leads on** provider breadth (42 vs 10), extension surface (36 events + ~28
registration methods + 30 UI hooks vs 2 shell hooks), crash recovery
(commit-before-execute with replay policy), session durability (versioned +
migrations + `context_edit` + tree), provider engineering (per-model routing,
constrained sampling, cache TTL, credential refresh), terminal primitives
(differential rendering, sync output, native clipboard, keybinding registry,
multiline editor), and architecture for scale (chord's CRDT composition,
CBOR remote sessions, entry-graph cost contracts).

## The three gaps that matter most

1. **Crash recovery.** pi-go's `readEvents` hard-fails on a torn final line
   (`internal/session/store.go:1136`) and a crashed tool call leaves a dangling
   `FunctionCall` with no marker. pi commits `{phase:"execute", arguments,
   replay}` before the tool runs and either re-runs or records
   "interrupted and may have partially run". One ~5-line port (truncate torn
   tail) captures a third of the value immediately.
2. **Extension surface.** pi-go's `HookConfig` has two events and both are shell
   subprocesses that cannot block. pi's `tool_call` event can `block`, mutate
   `event.input` in place, and `terminate` the batch; `tool_result` can replace
   content; `context` can rewrite history; `before_provider_headers` can inject
   headers. pi-go can do none of it.
3. **Provider breadth.** 10 vs 42. pi-go's `NewLLM` is a 10-arm switch; pi's
   per-model `api` field means one provider can serve several wire protocols.
   This is a data-model difference, not a switch statement.

## Recommended order of work

Detailed per-gap remediation is in
[07-gaps-and-recommendations.md](07-gaps-and-recommendations.md).

1. Truncate torn JSONL tail on read (session store) — hours, no design needed
2. Add `schemaVersion` to `meta.json` and a migration hook — hours
3. Make `before_tool` hooks able to deny — days, needs ADK contract change
4. Introduce a typed extension event bus in Go — weeks
5. Add `context_edit`-style compaction records — weeks
6. Convert provider dispatch from a switch to a per-model `api` table — weeks
7. Add remaining providers — days each, but only worth it after (6)