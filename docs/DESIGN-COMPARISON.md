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
| Language / runtime | TypeScript on Node.js (Bun binary builds supported) | Go 1.27, single static binary |
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
| Compaction (auto + manual) | ✅ LLM summary, token thresholds, file tracking | 🟡 auto (percent-of-window) + manual `/compact`; ANSI-strip + tool-result aggregation compactor | Different strategies, §6 |
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

**Same:** append-only JSONL event logs, resume (`--continue` / `--session`
— Go has no `--resume` flag), session pickers in the TUI, manual `/compact`.

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
- **Auto-compaction:** TS triggers on an absolute budget
  (`contextWindow − reserveTokens`); Go triggers on **percentages of the
  window** — `ShedPercent` (default 60) sheds superseded tool results,
  `SummarizePercent` (default 90) runs a summarizing LLM rebuild, enabled by
  default (`Enabled: true`) and configured by a global `autoCompact` block
  ([internal/session/compaction.go](../internal/session/compaction.go),
  [internal/config/config.go](../internal/config/config.go)). Both are
  automatic; the budget model and the configurability differ — Go's thresholds
  are neither absolute nor per-model, and they are configured as a single
  global `autoCompact` block even though the context window itself resolves
  per-model from the embedded catalog (`provider.ContextWindowSizeFor`).
  Auto-compaction also disables itself when the window is unknown — `Decide`
  returns no action for `windowSize <= 0`, so a model with no catalog entry and
  no `contextWindow` override never compacts. A per-model window override on
  `/model` switch is a further reason to key these thresholds by model.

**Missing in Go:** session DAG/tree navigation, branch summarization,
labels, read/modified-file preservation in compaction entries, per-model
compaction budgets, `session_info`/naming entries, HTML export of sessions.
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
2. **Compaction parity details** — Go's auto-compaction trigger exists and is
   on by default (percent-of-window thresholds in
   `internal/session/compaction.go`), but it lacks TS's absolute
   `contextWindow − reserve` budget, per-model overrides, and
   read/modified-file preservation in compaction entries. See §16.5.
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

---

## 16. Gap Analysis — TS pi 0.86.0 (2026-09-19)

Upstream `pi` publishes a per-release changelog and a news page
([`pi.dev/news/releases/0.86.0`](https://pi.dev/news/releases/0.86.0)). The
package lives at `earendil-works/pi` (`packages/coding-agent/CHANGELOG.md`);
the repo moved from `badlogic/pi-mono`, so older issue links point there.
0.86.0 carries **66 changelog items** (5 new features, 3 breaking changes,
10 added, 7 changed, 40 fixed, 1 removed).

Two things make this release different from earlier ones worth diffing. First,
its own headline is *transcript-aware prompt and tool changes* — pi grew a
mechanism for mutating the system prompt and toolset **mid-conversation while
keeping the prompt cache intact**. That is a design axis pi-go has not
addressed at all (not a missing feature, a missing model). Second, the release
is dominated by inherited `pi-ai` provider fixes, so several items are already
present by accident or by earlier independent work.

The verdict in one line: **0.86.0's five headline features are all absent from
pi-go; roughly ten of its provider-level fixes are already done here; and its
two most directly useful features (cache warming, per-model compaction
budgets) are cheap precisely because pi-go already tracks cache reads and
already has a percent-based auto-compaction trigger.**

### 16.1 Headline features — all absent

| 0.86.0 feature | pi-go status | Nearest existing thing |
|---|---|---|
| Prompt cache warming (cost-aware, during long tool runs and while idle) | ❌ absent | None. No keep-alive, no pre-warm, no cache-refresh timer, no provider call of any kind during a tool run |
| `/bug` reporting (redacted diagnostics, optional transcript, ZIP export, `pi.bug-report` entry, crash log) | ❌ absent | Session logger + `internal/audit`; no user-facing reporting path, no `/bug` in the slash-command table |
| Transcript-aware mid-conversation prompt **and tool** updates surviving resume/branch nav | ❌ absent | `Agent.RebuildWithInstruction` (`internal/agent/agent.go:531`) — see 16.4 for why this is not equivalent |
| Offline Radius model catalog (cached + live overlaid) | ❌ absent (no Radius provider) | Embedded-catalog floor + XDG runtime cache (`internal/provider/catalog.go`) covers the *offline-selectable* property for the six embedded providers |
| Per-model compaction budgets (`compaction.modelOverrides`) | ❌ absent | Global block only (`internal/config/config.go:123`); two distinct systems, percent-based auto-compaction and absolute-token manual `/compact` |

### 16.2 Breaking changes — none apply directly

pi-go does not implement the `pi-ai` provider-extension surface, so the
`Context` → `TranscriptContext` change and the `ToolResultMessage` /
`JsonValue` typing changes have no Go counterpart. `user_bash` fails-closed
semantics apply to TS extension hooks; pi-go's shell hooks are config-driven
and have no `user_bash` equivalent.

The one **indirect** consequence worth tracking: upstream's own
mid-conversation prompt fix is a bug fix for the mechanism pi-go lacks
entirely, and it is the strongest signal in this release about where upstream
considers the hard part to be.

### 16.3 Already handled in pi-go (do not port)

These read as gaps against the changelog but are already correct here. Listed
so a future reader does not "fix" them twice.

| 0.86.0 fix | pi-go evidence |
|---|---|
| Mistral `prompt_mode` → `reasoning_effort` for `mistral-medium-*` | Already correct: `internal/provider/mistral.go:165` documents the same finding ("magistral takes reasoning_effort, NOT prompt_mode"), with a verified prefix rule at `:145` |
| Mistral-hosted GLM-5.2 `reasoning_effort` | Same mechanism (`mistral.go:133`) |
| Agent-level retry backoff capped so long runs stay responsive | Already capped: `retry.DefaultConfig().MaxDelay = 60s` (`internal/retry/retry.go:52`), and agent retry reuses `retry.Delay` (`internal/agent/retry.go:77`). Upstream's fix *adds* the cap pi-go already had |
| Quadratic CPU draining buffered `EventStream` events | Not applicable — Go channel/iterator semantics in ADK differ from the TS drain path |
| Anthropic-compatible relays breaking signed thinking replay | pi-go's thinking-replay handling is separate; no equivalent defect identified |
| Extension tools without parameter schemas rejected at registration | N/A by construction: pi-go tools declare schemas from Go struct types (`newTool[TArgs,TResults]`, `internal/tools/registry.go:228`) |
| Fullscreen custom footers reserving a blank row | Different TUI (Bubble Tea), no equivalent layout path |
| Skill slash-command autocomplete ranking the `skill:` prefix | pi-go uses explicit `/skill-load` / `/skill-list` commands with no prefix ranking |
| Session ID lookup scanning full transcripts | Already avoided: `SessionModel`/`SessionBackend` are deliberately package functions that read `meta.json` only, specifically to dodge the full `events.jsonl` parse (`internal/session/store.go:1026-1041`) |
| `--resume` / `--continue` slowness | pi-go's `--continue` reads `meta.json` per session dir and compares `updatedAt` (`internal/session/store.go:835`) — header-only, never parses events |

### 16.4 The structural gap: mid-conversation prompt and tool mutation

This is the release's real subject, and pi-go's divergence is architectural
rather than a missing flag.

**Prompt.** pi-go's instruction is a fixed string captured by the
`InstructionProvider` closure at agent-create time
(`internal/agent/agent.go:466-486`). The only mutation path is
`Agent.RebuildWithInstruction` (`:531`), which rebuilds the runner. Upstream
0.86.0 instead records prompt and tool changes **into the transcript** so they
survive resume and branch navigation while preserving cached prefixes
(`#9548`), with a `before_agent_start` hook and `getCurrentSystemPrompt()` /
`getCurrentTools()` accessors.

Three concrete consequences in pi-go today, all verified:

1. **Instruction changes are retroactive, not scoped.** `RebuildWithInstruction`
   applies the new instruction to the *whole* conversation on the next turn —
   there is no "from here on" semantics. Upstream's model is specifically
   designed to avoid this.
2. **Prompt state is not versioned, so it leaks.** `/plan`
   (`internal/tui/plan.go:428`) and skill activation
   (`internal/tui/create_skill.go:79`) rebuild the instruction and persist
   nothing; `/clear` (`internal/tui/commands.go:290` `clearConversation`) clears
   events and the token gauge but **does not reset the instruction**, and
   neither does `/model` — `RebuildWithModel` preserves `cfg.Instruction`
   (`internal/agent/agent.go:552-573`), explicitly only swapping the LLM. So an
   injected skill/plan prompt survives until a restart or an explicit
   `--system`/`RebuildWithInstruction` call.
3. **Resume reconstructs the prompt from current disk state.** Instruction is
   rebuilt from the current `SystemInstruction` plus the AGENTS.md/skills on
   disk at resume time (`internal/cli/interactive.go:539`
   `buildDeferredInstructionParts`), not from what the
   session actually ran with. A session recorded under one prompt resumes under
   another, silently.

**Tools.** `internal/tools/registry.go` has no add/remove API; tools are
supplied once via `agent.Config.Tools` (`internal/agent/agent.go:318-319`), and
both `RebuildWithInstruction` and `RebuildWithModel` deliberately **preserve
`Tools`** (`:531-573`). Even the MCP toolset does not refresh: ADK re-queries
`Toolset.Tools(ctx)`, but `resilientToolset.Tools` guards its body with
`sync.Once` and returns the cached slice thereafter
(`internal/extension/mcp.go:161-187`), so a reconnecting server cannot surface
changed tools mid-session either. There is no
`tool_search` / `additional_tools` / deferred-tool mechanism, and no prompt
caching is involved either way.

**No persistence either way.** Session `meta.json` carries no instruction or
tool fields (`internal/session/store.go:48`; no instruction or tool fields), `CreateBranch` copies only
events (`internal/session/branch.go:28`), and no event author records a
prompt/tool change. (One related dead end: `/plan` *writes*
`PlanContext` to `meta.json` and `GetPlanContext` has no non-test caller, so
the persisted plan context is currently write-only, and `/plan resume` exists
only as help text.)

**Assessment.** Porting the *outcome* does not require porting the mechanism.
A transcript entry type that snapshots `{instruction, toolNames}` plus a
resume path that replays it would close (1)–(3) without touching `pi-ai`
typing. That is a medium change and it is the highest-value one in this
release. pi-go has request-side prompt caching (§16.5) but no way to mutate the
prompt or toolset *while keeping a cached prefix intact*, so the
cache-preserving half of upstream's motivation does not apply — see 16.5.

### 16.5 Cheap wins, and why they are cheap

**Per-model compaction budgets — smallest change with real payoff.** pi-go has
two *independent* compaction systems: percent-based auto-compaction
(`internal/session/compaction.go:81`, `ShedPercent: 60` / `SummarizePercent: 90`)
and an absolute-token manual `/compact` (`internal/session/store.go:1128`,
`MaxTokens: 100000` / `KeepRecent: 10`). Settings are a single global block
(`internal/config/config.go:123`); `ContextWindow` is likewise a single `int64`
(`:118`) even though the embedded catalog *is* per-model
(`provider.ContextWindowSizeFor`). Upstream added `compaction.modelOverrides`
with `reserveTokens` / `keepRecentTokens` (#8133).

Why cheap: `autocompact.ConfigFrom` (`internal/autocompact/config.go:12`) is
already the single choke point and is called identically from CLI, TUI, ACP
and piagent — it just never receives a model name. Adding a
`map[string]AutoCompactConfig` keyed by model and resolving in that one
function is a contained change. Two caveats: the budget model differs
(percentages of the window vs `window − reserve`), so a faithful port means
introducing absolute budgets alongside the percentages rather than replacing
them; and upstream's own release carried a follow-up fix for mid-run threshold
compaction skipping oversized trailing tool results (#9740), which suggests
this area is easy to get subtly wrong.

**Cache warming — cheap in mechanism, questionable in value.** pi-go already
tracks cache reads end-to-end: providers populate
`CachedContentTokenCount` (`internal/provider/anthropic.go:557,712,842`;
`openai_completions.go:368`; `openai_responses.go:639`), the guardrail tracker
exposes `CacheHitRateToday` / `CachePrefixTokens` / `BodyTokens`
(`internal/guardrail/guardrail.go:215,354,363`), and the TUI renders cache
state in the per-turn usage line and in `/context`. Anthropic request-side
caching is already implemented in full — `internal/provider/anthropic_caching.go`
stamps exactly three ephemeral breakpoints (last tool, last system block, last
cacheable block), opt-out via `LLMOptions.DisablePromptCaching`.

What is missing is any *active* component: nothing refreshes or pings a cache,
and nothing happens during a long tool run except TUI heartbeats
(`internal/tools/bash_supervisor.go:290`) — no provider traffic at all. So
warming could be built on existing accounting.

But three things argue against rushing it. Warming is inherently
**Anthropic-only** here, because Anthropic is the only provider that sets
`cache_control` — `internal/provider/cache_apply.go` states outright that
OpenAI, Gemini, Mistral, xAI, Ollama and Azure ignore `DisablePromptCaching`,
and only Mistral (`prompt_cache_key`) and xAI (`x-grok-conv-id`) do anything
cache-affinity-shaped. It spends real tokens to save cache-write cost, so it
needs cost-aware gating to be safe. And pi-go has **no prompt-cache lifetime
metadata**, because cache-write tokens are not broken out at all —
`internal/guardrail/guardrail.go:27-31` records that the `genai` usage metadata
has no field for them, so they stay folded into the non-cached remainder. That
metadata is upstream's
input for deciding *when* to warm — so it is a prerequisite, not a detail.

**Constrained sampling — the same feature with opposite intent.** Upstream
0.86.0 turned strict-prefer JSON-schema sampling **on by default** for
`read`/`bash`/`powershell`/`edit`/`write`, no longer behind `PI_EXPERIMENTAL`.
pi-go does the reverse deliberately: schemas are relaxed and models are
*coerced*, not constrained — `relaxSchema` opens `AdditionalProperties` and can
strip `Required` (`internal/tools/registry.go:126-152`), `lenientSchema` strips
required fields for the runtime schema, and the registry coerces
string→int/bool/array (`:354-505`) and aliases wrong param names
(`internal/tools/read.go:87-102`). OpenAI Responses explicitly pins
`Strict: false` (`internal/provider/openai_responses.go:373`, asserted in test
at `openai_test.go:1676`).

This is a genuine philosophy fork, not an oversight, and it is worth *naming*
rather than porting: upstream trusts the sampler to make malformed args
impossible; pi-go trusts the handler to make them harmless. Adopting upstream's
default would fight the existing coercion layer. If pi-go ever wants it, the
consistent form is opt-in per tool that *keeps* the coercion fallback — the
inverse of upstream's `constrainedSampling: false` escape hatch. Note also that
pi-go has no jitter in its backoff — a separate, smaller robustness gap.

**Resume ergonomics (`-r`/`-c`).** The tweet advertised faster `-r`/`-c`. pi-go
has **no shorthand for resume** — `--session` (`internal/cli/cli.go:211`) and
`--continue` (`:213`) are registered long-form with no single-letter alias.
(Shorthands are not unused repo-wide: `-v` on audit and `-o` on the model-list
subcommand exist — `internal/cli/audit.go:45`, `internal/cli/model.go:69` — but
the root command's resume paths have none, and upstream's `-r`/`-c` are the
muscle-memory flags being compared here.) The underlying *performance* property is already met
(header-only reads, see 16.3), so this is pure flag ergonomics: small, but a
real daily-use difference for anyone typing `-c`. Cheap and worth doing on its
own.

### 16.6 Fixes worth attention (pi-go has the defect; upstream fixed it)

Kept short — each needs its own spec before anyone acts.

- **Local shell commands terminated by a signal.** Upstream #9577: a
  signal-terminated command was reported as *successful* with partial output.
  pi-go's `finish()` special-cases SIGPIPE for good reason (exit 141 → 0,
  `internal/tools/bash_supervisor.go:341`), but derives status from
  `p.exitCode` generally (`:340`), so the same class of misreporting looks
  reachable. `exitStatus()` (`:190`) guards the reap-ordering race, not the
  signal interpretation. **Investigate before trusting bash exit status in a
  gate.**
- **Clipboard reporting success when the fallback was ignored.** Upstream
  #9618. pi-go's `writeSystemClipboard` is explicitly best-effort — every error
  path returns silently (`internal/tui/selection.go:170-185`) — and
  `copySelection` also returns `tea.SetClipboard(text)` (`:147`), which is
  Bubble Tea's OSC 52 path (bubbletea `clipboard.go:30`). So the fallback does
  exist and a terminal that supports OSC 52 can succeed even with no
  `pbcopy`/`clip`/`wl-copy`/`xclip` present. The real gap is narrower: neither
  path reports success or failure, and OSC 52 is silently ignored by terminals
  that do not support it, so a copy can fail with no signal to the user —
  exactly the case upstream #9618 added guidance for. Low severity; the
  mechanism is present, only the confirmation is missing.
- **Cloudflare 520 and Azure peak-load retry classification.** Upstream #9627,
  #9669. pi-go classifies on message substrings
  (`internal/retry/retry.go:65-147`); `"overloaded"` is matched (`:114`) but
  neither `520` nor an Azure peak-load phrase appears. Transient errors would
  be treated as terminal. Worth a look.
- **Bedrock one-hour cache writes priced at the five-minute rate.** Upstream
  #9457. **Not applicable** — pi-go has no Bedrock provider — but pi-go's
  pricing model has a single `CacheWrite` rate
  (`internal/provider/pricing.go:44-45`) with no cache-duration dimension, so
  the same conflation is latent if Bedrock or another tiered-cache provider is
  ever added.

### 16.7 Recommended order

1. **Per-model compaction budgets** — one choke point, real payoff, and it
   fixes a genuine inconsistency (per-model context windows already exist).
2. **`-r` / `-c` shorthands** — trivial, daily ergonomics, no design debate.
3. **Transcript-recorded prompt/tool state** — the release's actual thesis;
   medium cost, closes three verified correctness leaks (16.4).
4. **Signal-terminated bash status** — correctness, investigate first.
5. **Cache warming** — build the cache-lifetime metadata first; revisit
   warming only if measurement shows it pays on Anthropic.
6. **Constrained sampling** — do not port the default; decide the philosophy
   explicitly if at all.
