# Design Comparison: pi-go (Go) vs pi coding-agent (TypeScript)

> Last validated: 2026-07-07
> Scope: `pi-go` (this repository) compared against `pi-mono/packages/coding-agent`
> (the original TypeScript implementation by Mario Zechner, npm
> `@mariozechner/pi-coding-agent`), including its dependencies `pi-agent-core`,
> `pi-ai`, and `pi-tui`.
> Method: parallel source-level inventory of both codebases with claims
> verified against source files. TS paths are relative to
> `pi-mono/packages/coding-agent`, Go paths relative to this repository.

---

## 1. Executive Summary

The two projects share a name, a philosophy ("minimal terminal coding
harness"), and a core loop (LLM + read/write/edit/bash in a REPL with sessions
persisted as JSONL). Beyond that, they have diverged into **two different
products**:

- **TypeScript pi** is a *minimal, radically extensible harness*. The core
  ships almost nothing beyond 7 tools and 4 operating modes, but exposes a
  deep in-process **extension API** (20+ event hooks, custom tools, custom
  slash commands, custom UI components, custom editors) plus distributable
  **pi packages** (extensions + skills + prompt templates + themes over
  npm/git). Sub-agents, MCP, and web search are deliberately *not* built in.

- **Go pi** is a *batteries-included agent runtime*. It builds in what TS
  leaves to extensions — **MCP**, **sub-agent orchestration with git-worktree
  isolation**, **LSP tools**, **two persistent memory systems**, **token
  guardrails**, **supply-chain skill auditing**, **OTEL tracing**, **ACP
  server/client**, **ATIF trajectory export**, a **macOS sandbox wrapper**,
  and a **remote-terminal web server** — but has a far thinner in-process
  extension story (shell hooks + config only, no code-level plugin API).

**Rough positioning:** TS pi optimizes for *user-programmable behavior*;
Go pi optimizes for *out-of-the-box capability, safety, and observability*.

The most significant functional gaps in pi-go relative to TS pi are the
**extension system, session tree/branch navigation UX, message
queueing/steering, prompt templates, pi packages, HTML export, and breadth of
provider/subscription auth**. The most significant TS gaps relative to pi-go
are **MCP, sub-agents, LSP, memory, sandboxing, and telemetry** — all of which
are intentional omissions in TS ("build it as an extension").

---

## 2. Stack & Architecture at a Glance

| Dimension | TypeScript pi | Go pi |
|---|---|---|
| Language / runtime | TypeScript on Node.js (Bun binary builds supported) | Go 1.26, single static binary |
| Agent core | Custom (`@mariozechner/pi-agent-core`) | Google ADK for Go (`google.golang.org/adk`) — agent, runner, `model.LLM`, `tool.Tool`, `session.Service` |
| LLM abstraction | `@mariozechner/pi-ai` (20+ providers, unified streaming API) | `internal/provider` adapters implementing ADK `model.LLM` (5 providers) |
| TUI | `@mariozechner/pi-tui` (custom differential renderer) | Bubble Tea v2 + Glamour (`internal/tui`) |
| Tool schemas | TypeBox | `jsonschema-go` reflection with lenient/declaration split (`internal/tools/registry.go`) |
| Config dir | `~/.pi/agent/` + project `.pi/` | `~/.pi-go/` + project `.pi-go/` |
| Session store | `~/.pi/agent/sessions/*.jsonl` (flat files) | `~/.pi-go/sessions/<id>/{meta.json,events.jsonl}` (dir per session) |
| Distribution | npm (`npm i -g`), Bun-compiled binaries | GitHub Releases binary, `pi upgrade` self-update |
| Test strategy | vitest | `go test`, table-driven + e2e build tags, ~180 Go files |

### Layout

- **TS:** `src/core/` (agent-session, session-manager, tools, extensions,
  compaction, model-registry, settings, skills, prompt-templates,
  package-manager), `src/modes/` (interactive, print, rpc), `src/cli/`,
  `src/main.ts`, SDK surface in `src/core/sdk.ts` / `src/index.ts`.
- **Go:** thin `cmd/` (pi, pi-sandbox, pi-acp-mock) + 22 `internal/` packages
  (agent, acp, atif, audit, auth, cli, config, extension, guardrail, jsonrpc,
  logger, lsp, memory, otel, palace, provider, session, sop, subagent, tools,
  tui, webserver).

---

## 3. Feature Matrix

Legend: ✅ present · 🟡 partial/divergent · ❌ absent · 🧩 "via extension" (TS's intended path)

| Feature | TS pi | Go pi | Notes |
|---|---|---|---|
| Interactive TUI mode | ✅ | ✅ | Different frameworks, comparable core UX |
| Print / one-shot mode | ✅ | ✅ | Both text output for scripting |
| JSON event stream mode | ✅ (`--mode json`) | ✅ (`--mode json`) | Different event schemas |
| RPC headless mode | ✅ JSON-lines stdin/stdout, ~25 commands | 🟡 Unix-socket JSON-RPC 2.0, few methods | Go protocol much narrower |
| SDK / embedding | ✅ `createAgentSession()` public npm API | 🟡 `internal/` only — not importable | Go packages are unexported by design |
| Core file tools (read/write/edit/bash) | ✅ | ✅ | Schema/limit differences, §5 |
| grep / find / ls | ✅ | ✅ | Go adds `tree` |
| Git tools | ❌ 🧩 | ✅ git-overview, git-file-diff, git-hunk + `/commit` | |
| LSP tools | ❌ 🧩 | ✅ diagnostics/definition/references/hover/symbols (Go/TS/Py/Rust) | |
| Filesystem sandboxing | ❌ (plain path resolution) | ✅ `os.Root` sandbox on all file tools + macOS `sandbox-exec` wrapper | |
| Secret redaction in tool output | ❌ | ✅ `internal/tools/redact.go` | |
| Sessions (JSONL, resume) | ✅ | ✅ | |
| Session branching | ✅ fork + full **tree navigation UI** + branch summarization | 🟡 named branches (`/branch`), no tree UI, no branch summaries | |
| Compaction (auto + manual) | ✅ LLM summary, token thresholds, file tracking | 🟡 manual `/compact`; ANSI-strip + tool-result aggregation compactor | Different strategies, §6 |
| Message queue (steering / follow-up) | ✅ two queues, modes, dequeue | ❌ | |
| Extensions (in-process code plugins) | ✅ TypeScript via jiti, 20+ hooks, custom tools/commands/UI | ❌ (shell hooks only) | Largest gap, §7 |
| Lifecycle hooks | ✅ in-process events | 🟡 shell-command hooks on tool events (`internal/extension/hooks.go`) | |
| Skills (SKILL.md) | ✅ + `/skill:name` commands | ✅ + bundled embedded skills + audit | Near parity |
| Skill security audit | ❌ | ✅ Unicode/BiDi/homoglyph scanning (`internal/audit`) | |
| Prompt templates (`$1`, `$@`) | ✅ | ❌ | |
| Themes (JSON, hot-reload) | ✅ 43 color slots, project/global/package | 🟡 fixed built-in TUI color profiles | |
| Pi packages (npm/git distribution) | ✅ install/update/remove/list + filtering | ❌ | |
| Keybinding customization | ✅ `keybindings.json` | ❌ hardcoded | |
| Providers | ✅ 20+ (pi-ai) | 🟡 5 (Anthropic, OpenAI/Azure, Gemini, Mistral, Ollama) | |
| Subscription OAuth (Claude Pro/Max, ChatGPT, Copilot, Gemini CLI, Antigravity) | ✅ | 🟡 OAuth PKCE/device-code for Anthropic/OpenAI/Gemini | No Copilot/Antigravity |
| Custom models registry | ✅ `models.json` | 🟡 hardcoded `KnownModels` + `--url`/`--header` overrides | |
| Thinking levels | ✅ 6 levels, per-model budgets, cycling UI | 🟡 none/low/medium/high, config-level | |
| Roles / model aliases | ❌ | ✅ `roles` (default/smol/slow/plan/commit) | Go-unique concept |
| Context files | ✅ AGENTS.md/CLAUDE.md, cwd→root walk + global | 🟡 only `./AGENT.md` or `.pi-go/AGENTS.md` | §9 |
| MCP | ❌ 🧩 | ✅ stdio + streamable HTTP, resilient wrapper | |
| Sub-agents | ❌ 🧩 | ✅ typed agents, pool, git worktrees, parallel `/run` with gates + merge | |
| A2A agent network | ❌ | ✅ `internal/tools/a2a.go` (config-driven) | |
| ACP protocol (Zed/Cursor) | ❌ | ✅ client + server (`internal/acp`) | |
| Memory (persistent, cross-session) | ❌ 🧩 | ✅ two systems: observations (SQLite+FTS5) + Memory Palace (4-layer, embeddings, KG) | |
| Token usage guardrails | ❌ | ✅ daily limits (`internal/guardrail`, default 50M/day) | |
| Retry with backoff | ✅ settings-driven (3 retries, 2s→60s) | ✅ (3 retries, 1s→30s, transient-only) | Parity |
| OTEL tracing | ❌ | ✅ per-tool spans, OTLP gRPC/HTTP | |
| Session logging (structured) | 🟡 session JSONL only | ✅ separate JSON logs `~/.pi-go/log/…` | |
| HTML session export / gist share | ✅ `/export`, `/share` | ❌ | |
| ATIF trajectory export | ❌ | ✅ v1.6 (`pi atif export`) | |
| Image support (paste, vision, auto-resize) | ✅ Ctrl+V paste, 2000×2000 resize, terminal render | 🟡 vision via genai; no paste/resize UX | |
| Bash escape hatch in editor (`!cmd`, `!!cmd`) | ✅ | ❌ | |
| External editor (Ctrl+G `$EDITOR`) | ✅ | ❌ | |
| Web search | ❌ 🧩 | 🟡 via MCP server config | |
| Remote terminal pairing (web+QR) | ❌ | ✅ `internal/webserver` | |
| PDD / plan-driven workflow | ❌ | ✅ `/plan`, `/run`, SOPs (`internal/sop`) | |
| Self-update | 🟡 changelog detection + npm instructions | ✅ `pi upgrade` binary replace | |
| Install telemetry | ✅ opt-out ping | ❌ (OTEL is local/user-configured) | |

---

## 4. Operating Modes

**Same:** Both offer interactive, print, JSON-stream, and a headless
programmatic mode; both auto-detect TTY vs pipe.

**Different:**

- **RPC.** TS runs JSON lines over **stdin/stdout** with a rich command set
  (`prompt`, `steer`, `follow_up`, `abort`, `fork`, `switch_session`,
  `set_model`, `cycle_model`, `compact`, `bash`, `export_html`,
  `get_session_stats`, `get_messages`, extension-UI request forwarding …) —
  designed so a GUI can fully drive pi (`src/modes/rpc/rpc-types.ts`). Go runs
  JSON-RPC 2.0 over a **Unix socket** ([internal/jsonrpc](../internal/jsonrpc))
  with essentially `prompt` + session methods. Go additionally speaks **ACP**
  ([internal/acp/server](../internal/acp/server)), which TS doesn't; ACP covers
  part of the same integration need (Zed, Coder) via a standard protocol
  rather than a bespoke one.
- **SDK.** TS exports a real embedding API (`createAgentSession()` in
  `src/core/sdk.ts`, consumed by e.g. openclaw). Go keeps everything under
  `internal/`, so third parties cannot embed pi-go as a library at all.

**Missing in Go:** stdin/stdout RPC parity (steering, queue control, model
cycling, HTML export, stats), embeddable public API.
**Missing in TS:** ACP, Unix-socket transport.

---

## 5. Built-in Tools

**Same:** read, write, edit, bash, grep, find, ls with JSON-schema'd inputs
and output truncation.

**Different (details matter here):**

| Aspect | TS | Go |
|---|---|---|
| edit schema | `{path, edits: [{oldText, newText}]}` — multiple atomic edits per call (`src/core/tools/edit.ts`) | `{file_path, old_string, new_string?, replace_all?}` — single replacement, `replace_all` flag ([internal/tools/edit.go](../internal/tools/edit.go)) |
| read limits | 10,000 lines / 512 KB; images auto-resized | 2,000-line default but **full file for source code extensions**; 256 KB; strips base64 images ([internal/tools/read.go](../internal/tools/read.go)) |
| bash | timeout param, process-tree kill, full output to temp file when truncated, custom shell + command prefix | default 120 s / max 10 m, SIGPIPE-tolerated, output redacted + truncated ([internal/tools/bash.go](../internal/tools/bash.go)) |
| Schema philosophy | Strict TypeBox validation | Lenient runtime schema (extra props allowed, required relaxed) + strict declaration schema, plus string→int/bool coercion for sloppy models ([internal/tools/registry.go](../internal/tools/registry.go)) |
| Remote/pluggable ops | `ReadOperations`/`WriteOperations`/etc. interfaces for SSH/remote delegation | Not pluggable; sandbox-bound |
| Safety | `.gitignore` respected in grep/find; no sandbox | `os.Root` sandbox (no `..`/symlink escape), secret redaction |

**Go-only tools:** tree, git-overview, git-file-diff, git-hunk, 5× lsp-*,
agent/subagent, mem-search, palace-*, a2a, plus dynamic MCP tools.
**TS-only tool capability:** temp-file spillover of truncated bash output;
custom renderers per tool call/result in the TUI.

**Missing in Go:** multi-edit-per-call edit tool, pluggable tool operations,
tool-call custom rendering.
**Missing in TS:** everything in the "Go-only tools" list (by design — 🧩).

---

## 6. Sessions, Branching, Compaction

**Same:** append-only JSONL event logs, resume (`--continue` /
`--resume`), session pickers in the TUI, manual `/compact`.

**Different:**

- **File model.** TS: one flat `sessions/{uuid}.jsonl` with typed entries
  (`message`, `model_change`, `compaction`, `branch_summary`, `label`,
  `custom`, …) forming a **DAG** (`src/core/session-manager.ts`). Go: a
  directory per time-sortable ID with `meta.json` + `events.jsonl` + branch
  metadata ([internal/session/store.go](../internal/session/store.go),
  [internal/session/branch.go](../internal/session/branch.go)); events are ADK
  session events.
- **Branching UX.** TS has `/fork`, `/tree` with an interactive tree
  navigator, entry labels, filter modes, and **LLM branch summaries** injected
  when you switch branches (`src/core/compaction/branch-summarization.ts`). Go
  has named branches with head pointers — functional but flat; no tree
  navigation or summarization.
- **Compaction strategy.** TS compaction is **conversation
  summarization**: automatic when estimated tokens exceed
  `contextWindow − reserveTokens(16 384)`, always preserving the last
  20 000 tokens, and recording read/modified file lists in the compaction
  entry (`src/core/compaction/compaction.ts`). Go compaction is primarily
  **tool-output reduction**: ANSI stripping, aggregation of repeated
  bash/grep/read results, and source-code filtering
  (none/minimal/aggressive) applied via compactor callbacks
  ([internal/tools/compactor.go](../internal/tools/compactor.go)), plus a
  session-level `Compact` with a summarizer hook
  ([internal/session/store.go](../internal/session/store.go)). Go's approach
  reduces tokens *as they are produced*; TS reduces *retrospectively with an
  LLM summary*. These are complementary, not equivalent.
- **Auto-compaction:** TS on by default with thresholds; Go's LLM-style
  summarization is not automatically threshold-triggered in the same way.

**Missing in Go:** session DAG/tree navigation, branch summarization,
labels, automatic token-threshold LLM compaction, `session_info`/naming
entries, HTML export of sessions.
**Missing in TS:** streaming tool-output compaction, plan context persisted
in session metadata (`meta.json.planContext` for PDD resume).

---

## 7. Extensibility (the biggest divergence)

**TS pi's defining feature** is its extension system
(`src/core/extensions/loader.ts`, `runner.ts`):

- Extensions are **TypeScript modules** loaded at runtime via jiti from
  `~/.pi/agent/extensions/`, `.pi/extensions/`, or npm packages.
- API surface: ~25 lifecycle events (`session_start`, `context`,
  `before_provider_request`, `tool_call`, `tool_result`, `turn_start/end`,
  `session_before_compact`, `resources_discover`, …), `registerTool()`,
  `registerCommand()` (slash commands with flags/shortcuts),
  `registerProvider()`, and a full **UI context** (dialogs, widgets, footer,
  custom editor replacement, theming).
- **Pi packages** bundle extensions + skills + prompts + themes and are
  installed/updated via `pi install npm:…` / `git:…` with per-source resource
  filtering (`src/core/package-manager.ts`).

**Go pi's extension surface** ([internal/extension](../internal/extension)) is
configuration-driven:

- **Hooks:** shell commands bound to tool lifecycle events with optional tool
  filters and timeouts — good for CI-style guards, but they can't add tools,
  commands, or UI.
- **MCP servers:** the primary way to add tools (which TS lacks natively).
- **Skills:** markdown instructions (both have this).
- **Sub-agent definitions:** frontmattered `.md` agents (Go-unique).

**Assessment:** Go trades arbitrary in-process programmability for a fixed
but large built-in feature set plus MCP as the tool-extension mechanism. What
Go cannot express today: custom slash commands, custom TUI components/editors,
request/response middleware (`before_provider_request`), custom providers at
runtime, and shareable packages. If pi-go wants ecosystem growth, MCP covers
tools but nothing covers *commands/UI/middleware*.

---

## 8. Skills, Prompt Templates, Themes

**Skills — near parity, different guarantees:**

- Both: `SKILL.md` + YAML frontmatter (name/description), global + project
  discovery, injection into the system prompt.
- TS extras: `disable-model-invocation` flag, validation limits (name ≤64,
  description ≤1024, dir-name match), auto-registered `/skill:name` commands,
  skills listed only when the read tool exists, npm-distributed skills.
- Go extras: **bundled skills embedded in the binary**
  ([internal/extension/bundled_skills](../internal/extension/bundled_skills)),
  per-skill **tool whitelists** (`tools:` frontmatter), and **security
  auditing** with block/warn/skip modes ([internal/audit](../internal/audit)).

**Prompt templates — missing in Go.** TS templates
(`src/core/prompt-templates.ts`) are markdown files with `$1`, `$@`,
`${@:N:L}` bash-style argument substitution invoked as `/name args`. Go has no
equivalent; nearest analogs are skills and SOPs.

**Themes — divergent.** TS themes are user-editable JSON (43 color slots,
variables, hot reload, project/global/package scopes,
`src/modes/interactive/theme/theme.ts`). Go has built-in color profiles chosen
via `config.json` `theme` — not user-definable files.

**SOPs — missing in TS.** Go's PDD system prompt with override files
(`~/.pi-go/sops/pdd.md`, [internal/sop](../internal/sop)) has no TS
counterpart; TS would model this as a prompt template or extension.

---

## 9. System Prompt & Context Files

- **TS:** discovers `AGENTS.md` **or** `CLAUDE.md` from cwd **up to
  filesystem root**, plus global `~/.pi/agent/AGENTS.md`, merges all into a
  "# Project Context" section (`src/core/resource-loader.ts`,
  `src/core/system-prompt.ts`).
- **Go:** loads only `./AGENT.md` or `./.pi-go/AGENTS.md` (first hit wins,
  size-capped) and appends as "# Project Rules"; also appends a skill index
  and (optionally) Memory Palace wake-up context
  ([internal/agent/agent.go](../internal/agent/agent.go) `LoadInstruction`).

**Gap (Go):** no parent-directory walk, no `CLAUDE.md`/`AGENTS.md` at repo
root recognized directly (note: root `AGENTS.md` — the common convention — is
**not** read; only `AGENT.md` singular or `.pi-go/AGENTS.md`), no global
context file, no multi-file merge. This is a cheap, high-value fix.
**Gap (TS):** no memory-derived context injection.

---

## 10. Providers, Models, Auth

- **Breadth:** TS ships 20+ providers through pi-ai (incl. Azure, Vertex,
  Bedrock, Groq, Cerebras, xAI, OpenRouter, Vercel Gateway, ZAI…) and five
  **subscription OAuth** flows (Claude Pro/Max, ChatGPT Codex, GitHub Copilot,
  Gemini CLI, Antigravity). Go implements 5 providers as ADK `model.LLM`
  adapters ([internal/provider](../internal/provider)) with OAuth
  (PKCE/device-code/manual) for Anthropic/OpenAI/Gemini
  ([internal/auth](../internal/auth)).
- **Registry:** TS has a dynamic registry + user `models.json` for custom
  models and fuzzy resolution/cycling (Ctrl+P). Go has a hardcoded
  `KnownModels` snapshot plus `--url`/`--header`/`--insecure` escape hatches
  and prefix-based resolution.
- **Go-unique:** **roles** (named model aliases like `smol`/`slow`/`plan`
  with per-role advisor models) — an ergonomic layer TS lacks; Ollama
  local-model support; Azure managed identity.
- **TS-unique:** thinking-level extraction from model strings
  (`model:high`), per-level token budgets, enabled-models scoping,
  subscription-token billing warnings.

**Missing in Go:** provider breadth, Copilot/Antigravity subscriptions,
user-defined custom models file, model cycling UX.
**Missing in TS:** roles, Ollama-first local story (it does have generic
OpenAI-compatible endpoints via pi-ai, but not as a first-class local flow).

---

## 11. TUI

**Same:** streaming markdown chat, slash commands, autocomplete, model
switching, session pickers.

**TS-only UX:** message steering/follow-up **queues** (Alt+Enter, modes,
dequeue), tool-output & thinking-block collapse toggles (Ctrl+O/Ctrl+T),
image paste, `!`/`!!` inline bash, Ctrl+G external editor, customizable
keybindings with conflict detection, hot-reloadable themes, session tree
navigator, extension-provided widgets/footers/custom editors, IME/hardware
cursor support.

**Go-only UX:** persistent **sidebar** (model, tokens, session, agents,
memory, MCP status), parallel sub-agent orchestration view with per-agent
progress, gate results and worktree merge flow (`/run`,
[internal/tui/run.go](../internal/tui/run.go)), `/commit` LLM-assisted
conventional commits, `/plan` PDD flow, `/memory` and `/audit` commands,
remote pairing via web server + QR.

---

## 12. Unique Go Subsystems (no TS counterpart)

| Subsystem | What it does |
|---|---|
| [internal/subagent](../internal/subagent) | Typed agents (explore/plan/designer/reviewer/task/quick_task) from frontmattered `.md`, concurrency pool, **git worktree isolation per agent**, parallel plan execution with gate commands and branch merging; can also drive external Claude/Gemini agents over ACP |
| [internal/memory](../internal/memory) | Observational memory: tool calls captured in callbacks → SQLite+FTS5, background compression via a `smol` subagent, keyword/semantic search |
| [internal/palace](../internal/palace) | Memory Palace: 4-layer memory (identity / essential story / recall / search), wings+rooms drawers from `mempalace.yaml`, MiniLM embeddings with FTS5 fallback, knowledge-graph triples, miners, `pi memory` CLI |
| [internal/guardrail](../internal/guardrail) | Daily token budget enforcement (`usage.json`, default 50M/day) |
| [internal/audit](../internal/audit) | Skill supply-chain scanner: zero-width/BiDi/homoglyph detection, text/JSON/Markdown reports |
| [internal/lsp](../internal/lsp) | Managed LSP servers (Go/TS/Py/Rust) + 5 tools + auto-format/diagnostic callbacks |
| [internal/acp](../internal/acp) | ACP client (spawn external agents) and server (be an agent for Zed/Coder), with per-request tool permissions |
| [internal/atif](../internal/atif) | ATIF v1.6 trajectory export for eval interop |
| [internal/otel](../internal/otel) + [internal/logger](../internal/logger) | OTLP tracing per tool span; structured JSON session logs |
| [cmd/pi-sandbox](../cmd/pi-sandbox) | macOS `sandbox-exec` wrapper with embedded profile + denial-log tailing |
| [internal/webserver](../internal/webserver) | Remote terminal pairing (HTTP+WS, PTY, QR codes) |
| [internal/sop](../internal/sop) | Overridable PDD planning prompt powering `/plan` & `/run` |

TS's stance on all of the above is explicit: keep the core minimal, implement
via extensions/packages. Go's stance: these are core capabilities of an agent
runtime.

---

## 13. Gap Analysis — Missing from pi-go (prioritized)

### P0 — high user value, moderate cost
1. **Context-file discovery parity** — read root `AGENTS.md`/`CLAUDE.md`,
   walk parent dirs, support a global file. (Small change in
   `agent.LoadInstruction`; today the common `AGENTS.md`-at-repo-root
   convention is silently ignored.)
2. **Automatic token-threshold compaction with LLM summary** — Go has the
   summarizer hook in `session.Compact` but lacks TS's auto-trigger
   (`contextWindow − reserve`, keep-recent-tokens) and read/modified-file
   preservation.
3. **Message queueing / steering** — TS's follow-up & steering queues are
   core interactive ergonomics with no Go equivalent.
4. **Prompt templates** — cheap to implement (markdown + `$1`/`$@`
   substitution registered as slash commands) and highly requested in TS land.

### P1 — ecosystem & integration
5. **Richer RPC surface** — parity commands (steer, fork, set_model,
   get_state, stats) and/or a stdio JSON-lines transport so GUIs can embed
   pi-go the way openclaw embeds TS pi.
6. **Session tree navigation + branch summaries** — Go's branch model stores
   the data; the UX and summarization are missing.
7. **Pi-package-style distribution** — Go can't load third-party code, but a
   `pi install git:…` fetching *skills + agents + SOPs + MCP configs* would
   replicate most of the value without a plugin runtime.
8. **User-definable themes + keybindings** files.
9. **HTML session export / share.**

### P2 — breadth
10. **Provider breadth** (OpenRouter, Groq, xAI, Bedrock, Vertex; GitHub
    Copilot subscription auth) and a user `models.json`.
11. **Image paste + auto-resize UX** in the TUI.
12. **`!`/`!!` inline bash and external `$EDITOR`** in the editor.
13. **Public Go SDK** — promote a stable subset of `internal/` to `pkg/` (or
    a versioned module) if embedding is ever a goal; note this conflicts with
    the current single-module `internal/`-only design rule and needs an
    explicit decision.

### Not recommended to port
- **In-process TypeScript-style extension runtime.** Go can't hot-load code
  safely/portably (plugins are fragile). The pragmatic equivalents are
  already present (MCP for tools, shell hooks for events) or cheap (declarative
  slash commands via prompt templates). If middleware hooks are needed,
  consider a *stdio hook protocol* (JSON in/out shell hooks that can mutate
  requests) rather than a code plugin system.

## 14. Gap Analysis — Missing from TS pi (for context)

MCP, sub-agents, LSP, persistent memory, token guardrails, skill auditing,
sandboxing, OTEL, ACP, ATIF, git tools, remote pairing, roles, PDD. All are
deliberate ("extensions can do this"), but for pi-go these constitute its
differentiation — they should be treated as the product's moat rather than
debt, and hardened accordingly (several already have dedicated tests and
specs under [specs/](../specs/)).

---

## 15. Philosophy Delta (summary judgment)

| | TS pi | Go pi |
|---|---|---|
| Core credo | "Adapt pi to your workflow **without forking**" — minimal core, everything else user-space | "The runtime should already do it" — capable, safe, observable by default |
| Extension unit | In-process TS module / npm package | Config: MCP server, shell hook, skill, agent.md, SOP |
| Safety model | Trust the user & extensions; no sandbox | Defense-in-depth: os.Root, sandbox-exec, redaction, audit, guardrails, permissions |
| Sub-agents | Explicit non-goal | First-class, worktree-isolated, parallel |
| Observability | Session JSONL + optional install ping | OTEL spans, structured logs, ATIF, usage accounting |
| Ecosystem | Pi packages on npm/git | Bundled + config; no third-party distribution yet |

The implementations are best understood not as a port and its original, but
as two answers to "what belongs in the core of a coding agent?" pi-go's
highest-leverage next steps are the P0 items above — they close everyday
ergonomic gaps without compromising its batteries-included identity.
