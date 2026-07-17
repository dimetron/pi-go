# pi-go vs grok-build — Feature Comparison & Roadmap Recommendations

> **Scope:** Detailed side-by-side feature comparison between `pi-go` (the Go-based AI coding agent in this repo) and
`grok-build` (xAI's Rust port of "Grok Build", snapshotted at `tmp/grok-build/`). Produces a prioritised list of feature
> gaps and concrete recommendations for `pi-go`.
>
> **Date:** 2026-07-16
> **Methodology:** Two parallel `explore` agents produced exhaustive, file:line-anchored inventories of both codebases (
> grok-build's 24 user-guide docs + ~60 crates, pi-go's 23 internal packages). `copilot` was consulted for a focused
> high-leverage gap analysis. This report synthesises both, plus a third top-down review of file:line references. All
> claims are anchored to actual file:line locations — see `Appendix A: Package/Crate Map`,
`Appendix B: Tool Inventory Diff`, and `Appendix C: Slash Command Parity`.
>
> **Status:** WIP — awaiting user prioritisation. See `## Open Questions for the user`.

---

## Executive Summary

| # | Headline                                                                                                                                                                                                                                                                                                                                                                                           |
|---|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 1 | **pi-go has 33 tools and 17 bundled subagents with parallel/chain modes** (`internal/tools/`, `internal/subagent/bundled/*.md`); grok-build has 60+ tools but only 3 built-in agent types (`general-purpose`/`explore`/`plan`) and a one-mode-at-a-time spawn tool.                                                                                                                                |
| 2 | **pi-go has two coexisting memory subsystems** — claude-mem style observation store (`internal/memory/`) **and** MemPalace wings/rooms/drawers + KG + diary (`internal/palace/`). grok-build has one combined memory system (markdown + SQLite FTS5 + vec0). pi-go's separation is more auditable; grok's hybrid search is more powerful.                                                          |
| 3 | **pi-go is the only agent with hidden-Unicode skill auditing** (`internal/audit/chars.go:1-160` — tag chars, BiDi overrides, "Glassworm" Variation Selectors) and a **7-phase PDD plan/run workflow with gate validation** (`internal/sop/pdd_default.go`, `internal/tui/plan.go`, `internal/tui/run.go`). grok-build's `/plan` is a read-only gate without a multi-phase SOP.                     |
| 4 | **grok-build has OS-level kernel sandboxing** (`Landlock`/`Seatbelt`/`bwrap`/`seccomp`) with 4 built-in profiles and custom profiles; pi-go uses Go 1.24+ `os.Root` (`internal/tools/sandbox.go:1-526`) which is portable but in-process only and cannot isolate subagent subprocesses. **This is the single biggest security gap.**                                                               |
| 5 | **grok-build has 13 hook events** with `PreToolUse` as a hard security boundary (`xai-grok-hooks`), including Cursor's camelCase compat layer; pi-go has only 2 (`before_tool`/`after_tool` in `internal/extension/hooks.go`).                                                                                                                                                                     |
| 6 | **pi-go runs as an ACP server** (`pi acp-server` via `internal/acp/server/`) **and** spawns ACP-coded agents (claude, gemini, cursor, copilot via `internal/acp/client/`); grok-build only runs as an ACP agent, not as a server that other agents can embed.                                                                                                                                      |
| 7 | **grok-build has a richer TUI surface** — 70+ slash commands, full theming system (5 themes + auto OS-detection), 3 permission modes, OSC 9/99/777 notifications, sleep prevention, Mermaid rendering, voice dictation. pi-go's TUI is more focused (30+ commands, single theme) but has features grok lacks (matrix rain, mascot with 7 moods, drag-select, ATIF export, webserver with pairing). |
| 8 | **grok-build has a 60-page `xai-grok-config` schema** (TOML, 4 config files: `config.toml`, `pager.toml`, `auth.json`, `requirements.toml`) and a `xai-grok-pager` pager architecture; pi-go has a single `config.json` + `.env` + `mcp.json` model. pi-go's simplicity is easier to audit; grok's flexibility is more user-friendly at scale.                                                     |

**Verdict:** pi-go is **not behind** in capability — it has unique advantages in subagent orchestration, memory depth,
PDD workflow, and security auditing. The **top three gaps** to close are (1) OS-level kernel sandboxing, (2) extended
hook event set with blocking PreToolUse, and (3) structured permission system. Beyond that, fill in plan mode,
fork/rewind, and code graph for a more competitive feature set.

---

## Feature Matrix

Legend: ✅ done · 🟡 partial · ⚪ none · 🚫 deliberately skipped

### Tools

| Feature                                                 |                               pi-go                                |                                                             grok-build                                                             | Gap                                       | Priority |
|---------------------------------------------------------|:------------------------------------------------------------------:|:----------------------------------------------------------------------------------------------------------------------------------:|-------------------------------------------|:--------:|
| `read` (file with offset/limit)                         |                      ✅ `tools/read.go:73-80`                       |                                                ✅ `read_file` w/ line-precise ranges                                                | none                                      |    —     |
| `write` (file)                                          |                      ✅ `tools/write.go:26-32`                      |                                                     ✅ `search_replace`/`Write`                                                     | none                                      |    —     |
| `edit` (search/replace)                                 |         ✅ `tools/edit.go:32-40` w/ 3 retries + fuzzy-diff          |                                                         ✅ `search_replace`                                                         | pi-go has retry-on-race                   |    —     |
| `bash` (terminal)                                       |          ✅ `tools/bash.go:38-42` (timeout, redact, OTEL)           |                                            ✅ `run_terminal_command` w/ persistent shell                                            | grok has persistent shell session         |    M     |
| `grep` (content search)                                 |          ✅ `tools/grep.go:128-136` (ripgrep, regex cache)          |                                                              ✅ `grep`                                                              | none                                      |    —     |
| `find` / glob                                           |                      ✅ `tools/find.go:33-37`                       |                                                            ✅ `list_dir`                                                            | none                                      |    —     |
| `ls`                                                    |                       ✅ `tools/ls.go:36-40`                        |                                                            ✅ `list_dir`                                                            | none                                      |    —     |
| `tree`                                                  |                      ✅ `tools/tree.go:45-49`                       |                                                  ⚪ none (folded into `list_dir`)                                                   | pi-go only                                |    —     |
| `git-overview`                                          |                  ✅ `tools/git_overview.go:51-55`                   |                                                        ⚪ none (uses `bash`)                                                        | pi-go only                                |    —     |
| `git-file-diff`                                         |                    ✅ `tools/git_diff.go:35-42`                     |                                                        ⚪ none (uses `bash`)                                                        | pi-go only                                |    —     |
| `git-hunk`                                              |                    ✅ `tools/git_hunk.go:41-45`                     |                                                        ⚪ none (uses `bash`)                                                        | pi-go only                                |    —     |
| LSP diagnostics                                         |                      ✅ `tools/lsp.go:131-141`                      |                                           ✅ `LspTool` (gated by `[features] lsp_tools`)                                            | equal                                     |    —     |
| LSP definition                                          |                      ✅ `tools/lsp.go:143-152`                      |                                                             ✅ (gated)                                                              | equal                                     |    —     |
| LSP references                                          |                      ✅ `tools/lsp.go:154-163`                      |                                                             ✅ (gated)                                                              | equal                                     |    —     |
| LSP hover                                               |                      ✅ `tools/lsp.go:165-174`                      |                                                             ✅ (gated)                                                              | equal                                     |    —     |
| LSP symbols                                             |                      ✅ `tools/lsp.go:176-185`                      |                                                             ✅ (gated)                                                              | equal                                     |    —     |
| LSP workspace-symbol                                    |                      ✅ `tools/lsp.go:187-197`                      |                                                             ✅ (gated)                                                              | equal                                     |    —     |
| LSP code-action                                         |                      ✅ `tools/lsp.go:199-209`                      |                                                             ✅ (gated)                                                              | equal                                     |    —     |
| Subagent                                                |        ✅ `tools/subagent.go:89-105` (single/parallel/chain)        |                                                  ✅ `spawn_subagent` (single-mode)                                                  | pi-go richer                              |    —     |
| A2A                                                     |                      ✅ `tools/a2a.go:306-321`                      |                                                               ⚪ none                                                               | pi-go only                                |    —     |
| mem-search / mem-timeline / mem-get                     |                   ✅ `tools/mem_search.go:35-167`                   |                                                  ✅ `memory_search` + `memory_get`                                                  | equal                                     |    —     |
| Palace (10 tools)                                       | ✅ `internal/palace/tools.go` (status/search/add/kg/traverse/diary) |                                                               ⚪ none                                                               | pi-go only                                |    —     |
| `ask_user_question`                                     |                      ⚪ none (use TUI prompt)                       |                                                         ✅ (30 min timeout)                                                         | grok only                                 |    S     |
| `todo_write`                                            |                     ⚪ none (use `tui/plan.go`)                     |                                                                 ✅                                                                  | partial parity                            |    —     |
| `web_search` / `web_fetch`                              |                     ⚪ none (use external MCP)                      |                                             ✅ w/ backend model + proxy/allowed-domains                                             | grok only                                 |    M     |
| Image generation (imagine)                              |                               ⚪ none                               |                                              ✅ `imagine`, `imagine_video` (xAI media)                                              | grok only (provider-specific)             |    🚫    |
| `monitor` (event-stream from script)                    |                               ⚪ none                               |                                                                 ✅                                                                  | grok only                                 |    S     |
| `scheduler_create` / `/loop`                            |                               ⚪ none                               |                                                       ✅ (recurring prompts)                                                        | grok only                                 |    M     |
| Background tasks (`background: true`)                   |                🟡 `bash` returns after timeout only                | ✅ `run_terminal_command.background` + `get_command_or_subagent_output` + `wait_commands_or_subagents` + `kill_command_or_subagent` | grok richer                               |    M     |
| `enter_plan_mode` / `exit_plan_mode`                    |                   ⚪ none (use `/plan` + `/run`)                    |                                                        ✅ (4-state machine)                                                         | grok only                                 |    S     |
| `apply_patch` (Codex compat)                            |                               ⚪ none                               |                                                         ✅ `ApplyPatchTool`                                                         | n/a                                       |    🚫    |
| `read_file` image/pdf/pptx                              |                               ⚪ none                               |                                              ✅ (image+OCR, PDF extract, PPTX extract)                                              | grok only                                 |    L     |
| Tool profile variants (concise/hashline/codex/opencode) |             🟡 single profile + `coercingTool` aliases             |                        ✅ 4 profiles (grok_build, grok_build_concise, grok_build_hashline, codex, opencode)                         | pi-go has alias-based parity at less code |    —     |

### Providers

| Feature                                          |                                      pi-go                                      |                grok-build                 | Gap                                      | Priority |
|--------------------------------------------------|:-------------------------------------------------------------------------------:|:-----------------------------------------:|------------------------------------------|:--------:|
| Anthropic (with thinking levels)                 |               ✅ `provider/anthropic.go:40-94` (none/low/med/high)               |        ✅ (config `extra_headers`)         | equal                                    |    —     |
| Anthropic OAuth (`sk-ant-oat` detection)         |                         ✅ `provider/anthropic.go:46-58`                         |               ✅ (via login)               | equal                                    |    —     |
| Anthropic advisor tool (consult separate model)  |                    ✅ `provider/anthropic.go:161-200` (Beta)                     |                  ⚪ none                   | pi-go only                               |    —     |
| OpenAI Chat Completions                          |                       ✅ `provider/openai_completions.go`                        |      ✅ (`chat_completions` backend)       | equal                                    |    —     |
| OpenAI Responses API                             |                        ✅ `provider/openai_responses.go`                         |          ✅ (`responses` backend)          | equal                                    |    —     |
| OpenAI ChatGPT Codex backend                     |          ✅ `provider/openai_codex.go:15-110` (auth, JWT, 4xx logging)           |              ✅ (via XAI API)              | equal                                    |    —     |
| OpenAI Azure                                     |                       ✅ `provider/openai_azure.go:22-130`                       |            ✅ (via `base_url`)             | equal                                    |    —     |
| Gemini (with grounding)                          |    ✅ `provider/gemini.go:18-55` + `agent/grounding.go:18-31, 38-39, 160-213`    |               ✅ (grounding)               | equal                                    |    —     |
| Gemini thinking/Imagen                           |                        ⚪ none (provider doesn't expose)                         |                     ✅                     | provider-specific                        |    🚫    |
| Mistral                                          |                          ✅ `provider/mistral.go:27-83`                          |           ✅ (via OpenAI compat)           | equal                                    |    —     |
| Ollama (native, with `ThinkValue`)               | ✅ `provider/ollama.go:29-95` + `provider.go:478-519` (context from `/api/show`) |            ✅ (via `base_url`)             | pi-go richer (live context window query) |    —     |
| Custom OpenAI-compatible via `--url`             |                        ✅ `provider/provider.go:314-333`                         |        ✅ `[model.<name>].base_url`        | equal                                    |    —     |
| `--header` / `--insecure` propagation            |                         ✅ `provider/provider.go:18-60`                          |            ✅ (`extra_headers`)            | equal                                    |    —     |
| Model listing CLI                                |                                ✅ `cli/model.go`                                 |              ✅ `grok models`              | equal                                    |    —     |
| Multi-role model config (default/smol/plan/slow) |                      ✅ `config/config.go:55-71` (4 roles)                       |             ✅ (`-m <model>`)              | pi-go has roles                          |    —     |
| 3 API backends (chat/responses/messages)         |         🟡 chat+responses+codex (no Anthropic-style `messages` backend)         |              ✅ (3 backends)               | equal                                    |    —     |
| `env_key` array (try multiple env vars)          |                           🟡 single env per provider                            | ✅ (string or array, first non-empty wins) | grok richer                              |    S     |
| Custom models endpoint (`/v1/models` prefetch)   |                                     ⚪ none                                      |         ✅ `GROK_MODELS_BASE_URL`          | grok only                                |    L     |
| `supports_backend_search` per-model              |                          🟡 uses Gemini's native flag                           |               ✅ (any model)               | grok only                                |    L     |
| Web search model (`[models] web_search`)         |                ⚪ none (Gemini uses native `google_search` tool)                 |        ✅ separate model for search        | grok only                                |    M     |

### Subagents

| Feature                                         |                                                  pi-go                                                  |                                  grok-build                                  | Gap          | Priority |
|-------------------------------------------------|:-------------------------------------------------------------------------------------------------------:|:----------------------------------------------------------------------------:|--------------|:--------:|
| Bundled subagent count                          |                               ✅ **17** (`internal/subagent/bundled/*.md`)                               |         🟡 **3** built-in (general-purpose, explore, plan) + custom          | pi-go richer |    —     |
| Single execution mode                           |                                      ✅ `tools/subagent.go:30-114`                                       |                              ✅ `spawn_subagent`                              | equal        |    —     |
| Parallel execution (max 8)                      |                                          ✅ `tasks[]` (line 25)                                          |                            ⚪ none (one-at-a-time)                            | pi-go only   |    —     |
| Chain execution (max 8, `{previous}` template)  |                                     ✅ `chain[]` (line 28, 580-594)                                      |                                    ⚪ none                                    | pi-go only   |    —     |
| Worktree isolation per subagent                 |                    ✅ `internal/subagent/worktree.go:1-541` + `--worktree` per agent                     |                        ✅ `isolation: worktree` param                         | equal        |    —     |
| Spawn via ACP (claude/gemini/cursor/copilot)    | ✅ `internal/subagent/spawner_acp.go:64-100` + `internal/acp/client/{claudecode,gemini,cursor,copilot}/` |                 ✅ (claude, gemini, cursor, copilot via ACP)                  | equal        |    —     |
| Spawn via Codex `app-server` JSON-RPC           |                    ✅ `internal/codex/` + `internal/subagent/spawner_codex.go:63-93`                     |                  ⚪ none (Codex via login, not as subagent)                   | pi-go only   |    —     |
| Subagent tool allowlist per agent               |                                        ✅ (frontmatter `tools:`)                                         |            ✅ `capability_mode` (read-only/read-write/execute/all)            | equal        |    —     |
| Subagent timeout                                |                ✅ `internal/subagent/timeout.go:1-89` (frontmatter > env > default 10min)                |                              ✅ (default 10 min)                              | equal        |    —     |
| Recent task dedup (30 min TTL)                  |                          ✅ `internal/subagent/orchestrator.go:30-53, 206-276`                           |                                    ⚪ none                                    | pi-go only   |    —     |
| Event log to session (`acp.jsonl`)              |                              ✅ `internal/subagent/orchestrator.go:131-167`                              |                                    ⚪ none                                    | pi-go only   |    —     |
| Pool (semaphore, default 5)                     |                                   ✅ `internal/subagent/pool.go:1-49`                                    |                       ⚪ none (relies on system limits)                       | pi-go only   |    —     |
| Env allowlist (secret-stripped)                 |                                 ✅ `internal/subagent/environ.go:1-106`                                  |                                    ⚪ none                                    | pi-go only   |    —     |
| Foreign session discovery (Claude/Codex/Cursor) |                                                 ⚪ none                                                  | ✅ `xai-grok-workspace/src/foreign_sessions.rs` (staged, no scanner consumer) | grok staged  |    L     |
| `resume_from` (continue from completed)         |                                                 ⚪ none                                                  |                   ✅ (inherits transcript, model, identity)                   | grok only    |    L     |
| Persona behavioral overlay                      |                                                 ⚪ none                                                  |          ✅ `[subagents.personas.<name>]` w/ inputs/outputs contract          | grok only    |    L     |
| Depth limit (max 1)                             |                                    ✅ (not enforced but UI prevents)                                     |                                  ✅ enforced                                  | grok only    |    S     |
| Subagent error re-spawn retry                   |                                 ✅ `SpawnWithRetry` (3 retries on crash)                                 |                                    ⚪ none                                    | pi-go only   |    —     |

### Skills

| Feature                                             |                               pi-go                               |                                      grok-build                                       | Gap            | Priority |
|-----------------------------------------------------|:-----------------------------------------------------------------:|:-------------------------------------------------------------------------------------:|----------------|:--------:|
| SKILL.md format (YAML frontmatter + markdown)       |             ✅ `internal/extension/skills.go:170-241`              |                   ✅ (YAML frontmatter, lowercased name, ≤64 chars)                    | equal          |    —     |
| Bundled skills                                      |           ✅ 3 (`agents-md`, `memory-index`, `ponytail`)           |                    ✅ 3+ (`/create-skill`, `/help`, `/check-work`)                     | equal          |    —     |
| User skills (`~/.pi-go/skills/`)                    |                                 ✅                                 |                                           ✅                                           | equal          |    —     |
| Project skills (`.pi-go/skills/`)                   |                                 ✅                                 |                                           ✅                                           | equal          |    —     |
| Project > user > bundled precedence                 |                                 ✅                                 |                                           ✅                                           | equal          |    —     |
| Auto-invocation by description                      |              ⚪ none (skills are user-invocable only)              |                      ✅ (`description` + `when-to-use` matching)                       | grok richer    |    S     |
| `allowed-tools` per skill                           |                     ✅ (frontmatter `tools:`)                      |                                           ✅                                           | equal          |    —     |
| `user-invocable` / `disable-model-invocation` flags |                  ⚪ none (always user-invocable)                   |                ✅ (qualifies `/local:foo`, `/user:foo`, `/plugin:foo`)                 | grok richer    |    S     |
| Hidden-Unicode audit on load                        | ✅ `internal/audit/chars.go:1-160` (Tag, BiDi, ZWSP, Glassworm VS) |                                        ⚪ none                                         | pi-go only     |    —     |
| Audit modes (block/warn/skip)                       |              ✅ `extension/skills.go:31-32, 124-135`               |                                        ⚪ none                                         | pi-go only     |    —     |
| Dynamic `/<skillname>` slash command                |               ✅ `internal/tui/commands.go:116-120`                |                                 ✅ (built-in priority)                                 | equal          |    —     |
| `/create-skill` (interactive wizard)                |                ⚪ none (use `/skill-create <name>`)                |                                   ✅ `/create-skill`                                   | partial parity |    —     |
| Cursor/Claude skill compat                          |           ✅ (read `.claude/skills/`, `.cursor/skills/`)           |                              ✅ (compat table in config)                               | equal          |    —     |
| Plugin system (install/marketplace)                 |                              ⚪ none                               | ✅ `xai-grok-plugin-marketplace` (install `user/repo`, `--trust`, marketplace sources) | grok only      |    L     |

### Hooks

| Feature                                                           |                  pi-go                  |                                                                                                               grok-build                                                                                                                | Gap             | Priority |
|-------------------------------------------------------------------|:---------------------------------------:|:---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------:|-----------------|:--------:|
| Hook events                                                       | 🟡 **2** (`before_tool`, `after_tool`)  | ✅ **13** (`SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PermissionDenied`, `Stop`, `StopFailure`, `Notification`, `SubagentStart`, `SubagentStop`, `PreCompact`, `PostCompact`, `SessionEnd`) | **grok richer** |  **M**   |
| Blocking hook (can deny)                                          |          ⚪ none (always allow)          |                                                                                 ✅ `PreToolUse` (exit 2 = deny, `{"decision": "deny", "reason": "..."}`)                                                                                 | **grok only**   |  **M**   |
| Per-tool matcher (regex on tool name)                             |       ✅ `tools[]` in `HookConfig`       |                                                                                                        ✅ (matcher on tool name)                                                                                                         | equal           |    —     |
| MCP tool name in matcher (`server__tool`)                         |                 ⚪ none                  |                                                                                                                    ✅                                                                                                                    | grok only       |    S     |
| HTTP hooks                                                        |           ⚪ none (shell only)           |                                                                                           ✅ `{ "type": "http", "url": "...", "timeout": 15 }`                                                                                           | grok only       |    L     |
| Reserved env vars (`GROK_HOOK_EVENT`, `CLAUDE_PROJECT_DIR`, etc.) |    🟡 only `GROK_*` and project dir     |                                                                                                          ✅ (8+ reserved vars)                                                                                                           | equal           |    —     |
| Plugin hook scope (`GROK_PLUGIN_ROOT`)                            |           ⚪ none (no plugins)           |                                                                                                                    ✅                                                                                                                    | grok only       |    L     |
| Cursor camelCase compat (`sessionStart`, `preToolUse`)            |                 ⚪ none                  |                                                                                              ✅ (accepted alongside PascalCase/snake_case)                                                                                               | grok only       |    S     |
| Folder trust gate (`~/.grok/trusted_folders.toml`)                |                 ⚪ none                  |                                                                                ✅ (--trust /hooks-trust, cascades to subdirs, GROK_FOLDER_TRUST=0 escape)                                                                                | grok only       |    M     |
| ACP bridge hooks (`OnToolStart`/`OnToolEnd`)                      | ✅ `internal/extension/hooks.go:72-101`  |                                                                                                                    ✅                                                                                                                    | equal           |    —     |
| Tracing hooks (OTEL spans)                                        | ✅ `internal/extension/hooks.go:151-213` |                                                                                                                    ✅                                                                                                                    | equal           |    —     |

### MCP

| Feature                                            |                          pi-go                          |                    grok-build                    | Gap         | Priority |
|----------------------------------------------------|:-------------------------------------------------------:|:------------------------------------------------:|-------------|:--------:|
| stdio transport                                    |           ✅ `internal/extension/mcp.go:55-60`           |              ✅ (in `xai-grok-mcp`)               | equal       |    —     |
| HTTP / SSE transport                               |                    ✅ Streamable HTTP                    |   ✅ HTTP + SSE + Streamable HTTP w/ session_id   | equal       |    —     |
| URL headers                                        |                   ✅ `config.go:93-99`                   |             ✅ (per-server `headers`)             | equal       |    —     |
| ${VAR} substitution from `.env`                    |                  ✅ `config.go:309-368`                  |           ⚪ none (env-var at runtime)            | pi-go only  |    —     |
| `claude_desktop_config.json` object format         |                  ✅ `config.go:239-435`                  |                    ✅ (compat)                    | equal       |    —     |
| Resilient toolset (15s connect, log on fail)       |                   ✅ `mcp.go:107-167`                    |                        ✅                         | equal       |    —     |
| Tool result size cap                               | 🟡 256KB byte cap on tool output (`tools/truncate.go`)  |  ✅ 20KB inline + full payload in `mcp/` folder   | grok richer |    S     |
| OAuth for MCP servers                              |                         ⚪ none                          | ✅ (browser flow, `~/.grok/mcp_credentials.json`) | grok only   |    S     |
| Meta-tools (`search_tool`, `use_tool`)             |                         ⚪ none                          |                        ✅                         | grok only   |    S     |
| Runtime toggle (`/mcps` modal)                     |          ✅ `internal/tui/commands.go:848-909`           |         ✅ (`/mcps` modal, Space/r/i/a/x)         | equal       |    —     |
| `.claude.json` / `.cursor/mcp.json` compat         | 🟡 reads project `.claude/skills/`, but not `.mcp.json` | ✅ `[compat.cursor] mcps`, `[compat.claude] mcps` | grok richer |    M     |
| CLI management (`grok mcp list/add/remove/doctor`) |                 🟡 `/mcp` only (no CLI)                 |                     ✅ (full)                     | grok richer |    S     |

### ACP

| Feature                                         |                            pi-go                            |                                                            grok-build                                                             | Gap         | Priority |
|-------------------------------------------------|:-----------------------------------------------------------:|:---------------------------------------------------------------------------------------------------------------------------------:|-------------|:--------:|
| ACP client (spawn claude/gemini/cursor/copilot) | ✅ `internal/acp/client/{claudecode,gemini,cursor,copilot}/` |                                                                 ✅                                                                 | equal       |    —     |
| ACP server (run as ACP agent)                   |         ✅ `pi acp-server` (`internal/acp/server/`)          |                                                       ✅ `grok agent stdio`                                                        | equal       |    —     |
| Stdio transport                                 |                              ✅                              |                                                                 ✅                                                                 | equal       |    —     |
| WebSocket server transport                      |                           ⚪ none                            |                                               ✅ `grok agent serve --bind --secret`                                                | grok only   |    L     |
| WebSocket relay transport                       |                           ⚪ none                            |                                               ✅ `grok agent headless --grok-ws-url`                                               | grok only   |    L     |
| Extension methods (`x.ai/*` prefix)             |               ⚪ none (uses standard ACP only)               | ✅ 30+ (fs, git, git/worktree, search, terminal, session, prompt_history, rewind, compact_conversation, auth, feedback, telemetry) | grok richer |    M     |
| Per-session agent cache                         |             ✅ `internal/acp/server/runtime.go`              |                                                                 ✅                                                                 | equal       |    —     |
| Dynamic slash command discovery                 |             ✅ `internal/acp/server/commands.go`             |                                                                 ✅                                                                 | equal       |    —     |
| Compatible clients list                         | Zed, Neovim, Emacs, marimo (grok), pi-go, JetBrains (grok)  |                                          Zed, Neovim, Emacs, marimo, JetBrains (coming)                                           | equal       |    —     |

### Session

| Feature                                  |                                       pi-go                                        |                                       grok-build                                        | Gap                         |   Priority    |
|------------------------------------------|:----------------------------------------------------------------------------------:|:---------------------------------------------------------------------------------------:|-----------------------------|:-------------:|
| JSONL events (append-only)               |                        ✅ `internal/session/store.go:75-83`                         |                                    ✅ `updates.jsonl`                                    | equal                       |       —       |
| Meta file (`meta.json` / `summary.json`) |                                         ✅                                          |                                            ✅                                            | equal                       |       —       |
| Branches                                 |   ✅ `internal/session/branch.go:1-212` (each in `branches/<name>/events.jsonl`)    |           🟡 `parent_session_id` in `summary.json` (no first-class branches)            | pi-go richer                |       —       |
| Compaction (auto)                        |                              🟡 `/compact` slash only                              |     ✅ auto at 85% of `context_window` (`[session] auto_compact_threshold_percent`)      | grok richer                 |       S       |
| Compaction (two-pass + memory flush)     |                                       ⚪ none                                       | ✅ `[features] two_pass_compaction`, `[compaction.memory_flush]`, `[compaction.pruning]` | grok only                   |       M       |
| Session fork (`/fork`)                   |                                       ⚪ none                                       |                                  ✅ `/fork [--worktree                                   | --no-worktree] [directive]` | **grok only** | **S** |
| Session rewind (`/rewind`)               |                                       ⚪ none                                       |                ✅ `/rewind` (file snapshot restore + truncate transcript)                | **grok only**               |     **S**     |
| Worktree-scoped session                  |              🟡 subagent worktrees (`internal/subagent/worktree.go`)               |                       ✅ `x.ai/git/worktree/*` + `grok -w -r <id>`                       | equal                       |       —       |
| SQLite FTS5 search index                 |                              ⚪ none (file-based only)                              |                                ✅ `grok sessions search`                                 | grok only                   |       S       |
| Session picker                           |                                 ✅ `/resume` (TUI)                                  |                       ✅ `/resume` with content search (`Ctrl+/`)                        | equal                       |       —       |
| Auto-set title on first turn             |                 ✅ `internal/tui/commands.go:511-517` (recent feat)                 |                                ✅ `generated_title` field                                | equal                       |       —       |
| OSC 0 terminal title                     |                      ✅ `internal/tui/terminal_title.go:1-65`                       |                                            ✅                                            | equal                       |       —       |
| Dashboard (multi-session overview)       |                                       ⚪ none                                       |       ✅ `/dashboard`, `Ctrl+\\`, peek panel, dispatch, search, pinning, grouping        | **grok only**               |     **M**     |
| Title sanitization consistency           | 🟡 `formatTerminalTitle` and `FileService.SetSessionTitle` differ (see issues/002) |                                    ✅ (single source)                                    | grok only                   |       S       |

### Memory

| Feature                                                                     |                                   pi-go                                    |                               grok-build                                | Gap             | Priority |
|-----------------------------------------------------------------------------|:--------------------------------------------------------------------------:|:-----------------------------------------------------------------------:|-----------------|:--------:|
| Claude-mem observations (decision/bugfix/feature/refactor/discovery/change) |                      ✅ `internal/memory/db.go:14-101`                      |                      ⚪ none (own markdown format)                       | pi-go only      |    —     |
| FTS5 sync triggers                                                          |                                     ✅                                      |                                    ✅                                    | equal           |    —     |
| Embedding-backed search                                                     | ✅ `internal/palace/embedder_backend_{go,ort}.go` (ONNX `all-MiniLM-L6-v2`) |                       ✅ `vec0` (vector extension)                       | equal           |    —     |
| Hybrid search (vector + BM25)                                               |                    ⚪ FTS5 only (no vector+BM25 ranking)                    | ✅ vector 0.7 + BM25 0.3 (`[memory.search] vector_weight`/`text_weight`) | **grok richer** |  **M**   |
| Wings/rooms/drawers                                                         |                      ✅ `internal/palace/` (MemPalace)                      |                                 ⚪ none                                  | pi-go only      |    —     |
| Knowledge graph (subject/predicate/object, time-versioned)                  |                         ✅ `internal/palace/kg.go`                          |                                 ⚪ none                                  | pi-go only      |    —     |
| Diary (per-agent)                                                           |                     ✅ `internal/palace/tool_diary.go`                      |                                 ⚪ none                                  | pi-go only      |    —     |
| 3-layer memory (L0/L1/L2)                                                   |       ✅ L0 (identity) + L1 (wake-up summary) + L2 (on-demand recall)       |            🟡 first-turn injection + after-compact re-search            | equal           |    —     |
| Memory flush before compact                                                 |                                   ⚪ none                                   |                      ✅ `[compaction.memory_flush]`                      | grok only       |    M     |
| Dream consolidation                                                         |                 ⚪ none (mine does entity extraction only)                  |        ✅ `/dream` (auto at session end, 4h min, 3 sessions min)         | grok only       |    L     |
| MMR re-ranking (`lambda=0.7`)                                               |                                   ⚪ none                                   |                         ✅ `[memory.search].mmr`                         | grok only       |    L     |
| Temporal decay (`half_life_days=7`)                                         |                                   ⚪ none                                   |                         ✅ (session-only source)                         | grok only       |    L     |
| Source weights (workspace/session/global)                                   |                      ⚪ none (single source per query)                      |                   ✅ `[memory.search].source_weights`                    | grok only       |    L     |
| Initial-injection re-search after compact                                   |                                   ⚪ none                                   |                     ✅ `[memory.initial_injection]`                      | grok only       |    S     |
| File watcher (re-index on external edit)                                    |                                   ⚪ none                                   |                      ✅ `[memory.watcher] enabled`                       | grok only       |    S     |
| Privacy tags (`<private>...</private>`)                                     |                       ✅ `internal/memory/privacy.go`                       |                                 ⚪ none                                  | pi-go only      |    —     |
| Entity mining from source code                                              |                             ✅ `pi memory mine`                             |                                 ⚪ none                                  | pi-go only      |    —     |

### Sandbox

| Feature                                     |                       pi-go                       |                                           grok-build                                           | Gap             | Priority |
|---------------------------------------------|:-------------------------------------------------:|:----------------------------------------------------------------------------------------------:|-----------------|:--------:|
| In-process FS root restriction              |  ✅ Go 1.24+ `os.Root` (`tools/sandbox.go:1-526`)  |                            ✅ `[tools] respect_gitignore` (simpler)                             | equal           |    —     |
| OS-level kernel sandbox (Linux)             |                      ⚪ none                       |                                ✅ `Landlock` LSM (kernel 5.13+)                                 | **grok only**   |  **L**   |
| OS-level kernel sandbox (macOS)             |                      ⚪ none                       |                                  ✅ `Seatbelt` (sandbox-exec)                                   | **grok only**   |  **L**   |
| Subprocess isolation (bwrap on Linux)       |                      ⚪ none                       |                       ✅ bwrap mount-namespace expansion (for deny rules)                       | **grok only**   |  **L**   |
| Child-process network blocking (seccomp)    |                      ⚪ none                       |                         ✅ (Linux only, `Profile::read_only`/`strict`)                          | **grok only**   |  **L**   |
| Built-in sandbox profiles                   | 🟡 single mode (workspace-relative via `os.Root`) |                      ✅ 4: `workspace` / `devbox` / `read-only` / `strict`                      | **grok richer** |  **L**   |
| Custom sandbox profile (deny globs)         |                      ⚪ none                       | ✅ `~/.grok/sandbox.toml` with `extends`, `restrict_network`, `read_only`, `read_write`, `deny` | grok only       |    L     |
| Profile saved with session (resumable)      |                      ⚪ none                       |                      ✅ (profile fixed for life; mismatch refuses resume)                       | grok only       |    L     |
| Event log (`~/.grok/sandbox-events.jsonl`)  |                      ⚪ none                       |                                               ✅                                                | grok only       |    L     |
| `.envrc` auto-load (`[session] load_envrc`) |                      ⚪ none                       |                              ✅ `xai-grok-workspace/src/envrc.rs`                               | grok only       |    S     |
| `.gitignore` respect                        |    ✅ `tools/sandbox.go:144-167` (TTL cache 5s)    |                                               ✅                                                | equal           |    —     |
| Read `.pi-go`, `.cursor`, `.claude`         |           ✅ `tools/sandbox.go:498-501`            |                                             ⚪ none                                             | pi-go only      |    —     |

### TUI

| Feature                                                 |                         pi-go                         |                                        grok-build                                        | Gap             |    Priority    |
|---------------------------------------------------------|:-----------------------------------------------------:|:----------------------------------------------------------------------------------------:|-----------------|:--------------:|
| TUI framework                                           |      ✅ Bubble Tea v2 (`charm.land/bubbletea/v2`)      |                                     ✅ ratatui (Rust)                                     | equal           |       —        |
| Markdown rendering                                      |                       ✅ Glamour                       |                              ✅ custom (`xai-grok-markdown`)                              | equal           |       —        |
| Status bar (full-width)                                 |              ✅ `internal/tui/status.go`               |                               🟡 split into status blocks                                | pi-go richer    |       —        |
| Sidebar (30-char rail)                                  | ✅ `internal/tui/sidebar.go` (full-width per feat #48) |                              🟡 tasks pane is in main view                               | equal           |       —        |
| Mascot with moods                                       |          ✅ `internal/tui/face.go` (7 moods)           |                                          ⚪ none                                          | pi-go only      |       —        |
| Matrix rain                                             |     ✅ `internal/tui/matrix.go` (Catppuccin-Mocha)     |                                          ⚪ none                                          | pi-go only      |       —        |
| Drag-select (mouse)                                     |       ✅ `internal/tui/selection.go` (own impl)        |                                   ✅ (mouse reporting)                                    | equal           |       —        |
| Theming (multi-theme)                                   |               🟡 single theme `default`               |         ✅ 5 + `auto` (GrokNight/GrokDay/TokyoNight/RosePineMoon/OscuraMidnight)          | **grok richer** |     **S**      |
| `theme = "auto"` (OS detection)                         |                        ⚪ none                         |            ✅ (macOS `AppleInterfaceStyle`, Linux XDG Desktop Portal, OSC 11)             | grok only       |       S        |
| `theme = "system"` cursor color (OSC 12)                |                        ⚪ none                         |                        ✅ (set on startup, reset OSC 112 on exit)                         | grok only       |       S        |
| Truecolor → 256 → 16 quantization                       |                ✅ (default Go palette)                 |                               ✅ (auto-quantize at startup)                               | equal           |       —        |
| Compact mode                                            |               🟡 always minimal padding               |                           ✅ `/compact-mode` toggle (persisted)                           | grok only       |       L        |
| Syntax highlighting (`.tmTheme`)                        |             🟡 Glamour (no theme switch)              |                              ✅ 3 built-in `.tmTheme` files                               | grok only       |       S        |
| Image paste / drag-and-drop                             |                        ⚪ none                         |            ✅ (`Cmd+V`/`Ctrl+V`/`Alt+V`, image chip in prompt, inline preview)            | grok only       |       M        |
| Voice dictation (STT)                                   |                        ⚪ none                         |                         ✅ `/voice`, Ctrl+Space/F8, 25 languages                          | grok only       | 🚫 (long-term) |
| OSC 9/99/777/BEL notifications                          |                        ⚪ none                         |       ✅ focus-gated, per-terminal protocol (iTerm2/Kitty/Ghostty/WezTerm/Warp/VTE)       | grok only       |       S        |
| Sleep prevention (focused)                              |                        ⚪ none                         |              ✅ macOS `IOPMAssertionCreateWithName`, Linux `systemd-inhibit`              | grok only       |       S        |
| Tab progress bar (OSC 9;4)                              |                        ⚪ none                         |                                            ✅                                             | grok only       |       L        |
| Custom shell notification hooks                         |                        ⚪ none                         |                              ✅ `[[ui.notifications.hooks]]`                              | grok only       |       L        |
| Prompt queue (Ctrl+;)                                   |                        ⚪ none                         |                                            ✅                                             | grok only       |       M        |
| Tasks pane (Ctrl+B)                                     |                        ⚪ none                         |                     ✅ (subagents, background tasks, monitors, /loop)                     | grok only       |       M        |
| Todos pane (Ctrl+T)                                     |                 ✅ (sidebar checklist)                 |                                    ✅ (separate modal)                                    | equal           |       —        |
| Mermaid rendering                                       |                        ⚪ none                         | ✅ (pure Rust vendored dagre + resvg/tiny-skia, out-of-process worker, per-session cache) | **grok only**   |     **L**      |
| Mermaid `mmdc` subprocess fallback                      |                        ⚪ none                         |                                  ✅ (mmdc CLI fallback)                                   | grok only       |       L        |
| Extensions modal (Ctrl+L)                               |                   ✅ (`/mcp` modal)                    |               ✅ (`/hooks`, `/plugins`, `/marketplace`, `/skills`, `/mcps`)               | grok richer     |       S        |
| Vim mode                                                |                        ⚪ none                         |                              ✅ `/vim-mode` (j/k, h/l, etc.)                              | grok only       |       S        |
| Command palette (Ctrl+P, ?)                             |                        ⚪ none                         |                                            ✅                                             | grok only       |       S        |
| Model picker (Ctrl+M)                                   |                      ✅ `/model`                       |                                            ✅                                             | equal           |       —        |
| Always-approve toggle (Ctrl+O)                          |              🟡 via `/yolo` config flag               |                                ✅ toggle (session-scoped)                                 | grok richer     |       S        |
| `/btw` (aside without interrupt)                        |                        ⚪ none                         |                                            ✅                                             | grok only       |       L        |
| `/share`, `/recap`, `/timeline`, `/transcript`          |                        ⚪ none                         |                                            ✅                                             | grok only       |       L        |
| `/usage` (credits/billing)                              |                        ⚪ none                         |                                            ✅                                             | grok only       |       S        |
| `/privacy` (data retention toggle)                      |                        ⚪ none                         |                                            ✅                                             | grok only       |       S        |
| `/announcements`                                        |                        ⚪ none                         |                                            ✅                                             | grok only       |       L        |
| `/terminal-setup` (terminal detection)                  |                        ⚪ none                         |                        ✅ (iTerm2/Ghostty/Kitty/WezTerm/VTE/etc.)                         | grok only       |       S        |
| Welcome screen (Ctrl+S/W/I)                             |                        ⚪ none                         |                 ✅ (resume, new worktree, import Claude, dismiss import)                  | grok only       |       M        |
| Stuck detection (identical call / cycle / error streak) |         ✅ `internal/tui/agent_loop.go:85-148`         |                              ⚪ none (relies on user Ctrl+C)                              | pi-go only      |       —        |
| Panic recovery                                          |        ✅ `internal/tui/agent_loop.go:286-290`         |                                 ✅ (`xai-crash-handler`)                                  | equal           |       —        |

### Web / Remote

| Feature                              |                         pi-go                          |                           grok-build                           | Gap          | Priority |
|--------------------------------------|:------------------------------------------------------:|:--------------------------------------------------------------:|--------------|:--------:|
| HTTP server (browser-based terminal) | ✅ `pi serve` (`internal/webserver/serverv2.go:21-503`) | ⚪ none (relies on `grok agent headless` over WebSocket relay)  | pi-go richer |    —     |
| xterm.js frontend                    |         ✅ `internal/webserver/static_embed.go`         |                             ⚪ none                             | pi-go only   |    —     |
| Pairing-code auth (QR + 6-digit)     |        ✅ `internal/webserver/pairing.go:1-290`         |                             ⚪ none                             | pi-go only   |    —     |
| Per-tab PTY sessions                 |          ✅ `internal/webserver/pty.go:1-449`           |                             ⚪ none                             | pi-go only   |    —     |
| WebSocket PTY                        |                ✅ (survives reconnects)                 |                   ✅ `grok agent serve` (WSS)                   | equal        |    —     |
| pprof endpoint                       |             ✅ `--pprof --pprof-port 6060`              | ⚪ none (grok has heap_profile.rs but no public pprof endpoint) | pi-go only   |    —     |
| Multi-tab sessions                   |                    ✅ SessionManager                    |                             ⚪ none                             | pi-go only   |    —     |

### JSON-RPC / IPC

| Feature                                              |                         pi-go                          |        grok-build         | Gap         | Priority |
|------------------------------------------------------|:------------------------------------------------------:|:-------------------------:|-------------|:--------:|
| Unix-socket JSON-RPC 2.0 server                      | ✅ `internal/jsonrpc/rpc.go` (socket `/tmp/pi-go.sock`) | ⚪ none (uses ACP instead) | pi-go only  |    —     |
| `prompt` / `session.create` / `session.list` methods |                           ✅                            |          ⚪ none           | pi-go only  |    —     |
| ACP transport (stdio / WebSocket)                    |                           ✅                            |    ✅ (more transports)    | grok richer |    —     |

### Auth

| Feature                                    |                             pi-go                             |                             grok-build                             | Gap         | Priority |
|--------------------------------------------|:-------------------------------------------------------------:|:------------------------------------------------------------------:|-------------|:--------:|
| OAuth PKCE                                 | ✅ `internal/auth/auth.go:159-200` (random port, open browser) |              ✅ (loopback `http://127.0.0.1/callback`)              | equal       |    —     |
| Device code flow                           |              ✅ `DeviceFlow` + `PollDeviceToken`               |                    ✅ `grok login --device-auth`                    | equal       |    —     |
| Manual code flow                           |                    ✅ `StartManualCodeFlow`                    |                               ⚪ none                               | pi-go only  |    —     |
| OIDC (Authorization Code w/ PKCE)          |                            ⚪ none                             |  ✅ `[grok_com_config.oidc]` (issuer, client_id, scopes, audience)  | grok only   |    L     |
| External auth provider (shell-out binary)  |                            ⚪ none                             | ✅ `[auth] auth_provider_command` (stdout = token, stderr = status) | grok only   |    L     |
| Multiple OAuth providers                   |                   🟡 only `codex` (ChatGPT)                   |                 ✅ (SpaceXAI OAuth, OIDC, external)                 | grok richer |    M     |
| Auth token hot reload                      |            🟡 reload on next request (no fsnotify)            |                           ✅ (in-process)                           | equal       |    —     |
| Pre-expiry refresh (~5 min before)         |                            ⚪ none                             |             ✅ `GROK_AUTH_EARLY_INVALIDATION_SECS=300`              | grok only   |    S     |
| Refresh on 401                             |                  ✅ (auto-retry in provider)                   |                                 ✅                                  | equal       |    —     |
| Per-model `api_key` / `env_key` precedence |                       🟡 env vars only                        |        ✅ (per-model `[model.*].api_key` or `env_key` array)        | grok richer |    S     |
| Auth diagnostic log                        |                      ✅ `SetDebugLogger`                       |                    ✅ (RUST_LOG + GROK_LOG_FILE)                    | equal       |    —     |

### Telemetry

| Feature                                   |                              pi-go                              |                                                                                                                                                                                                  grok-build                                                                                                                                                                                                   | Gap             | Priority |
|-------------------------------------------|:---------------------------------------------------------------:|:-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------:|-----------------|:--------:|
| OTLP gRPC/HTTP                            |   ✅ `internal/otel/otel.go:1-253` (gRPC :4317 or HTTP :4318)    |                                                                                                                                                                                  ✅ (HTTP / gRPC, `OTEL_EXPORTER_OTLP_*` env)                                                                                                                                                                                  | equal           |    —     |
| Auto-disable on port-unreachable          |                        ✅ `otel.go:67-77`                        |                                                                                                                                                                                              ⚪ none (logs error)                                                                                                                                                                                              | pi-go only      |    —     |
| Spans for tool.*                          |    ✅ `BuildTracingCallbacks` (`extension/hooks.go:151-213`)     |                                                                                                                                                                                                       ✅                                                                                                                                                                                                       | equal           |    —     |
| Spans for llm.*                           |            ✅ `BuildLLMTracingCallbacks` (same file)             |                                                                                                                                                                                                       ✅                                                                                                                                                                                                       | equal           |    —     |
| Structured OTel **metrics** (grok_code.*) |                       ⚪ none (spans only)                       |                                                                                                                           ✅ `grok_code.session.count`, `grok_code.token.usage`, `grok_code.turn.count`, `grok_code.tool.decision`, `grok_code.tool.usage`, `grok_code.error.count`                                                                                                                            | **grok richer** |  **M**   |
| Structured OTel **events** (grok_code.*)  |                             ⚪ none                              | ✅ `grok_code.session_start/end`, `grok_code.user_prompt`, `grok_code.turn_completed`, `grok_code.api_request/error`, `grok_code.tool_result/decision`, `grok_code.mcp_server_connection`, `grok_code.permission_mode_changed`, `grok_code.skill_activated`, `grok_code.plugin_loaded`, `grok_code.compaction`, `grok_code.subagent`, `grok_code.auth`, `grok_code.internal_error`, `grok_code.model_switched` | **grok richer** |  **M**   |
| Content-free by default (privacy)         | 🟡 spans include `bash.command`, `tool.args_count` (no opt-in)  |                                                                                                                                                        ✅ double opt-in (`GROK_EXTERNAL_OTEL=1` + exporter) + 60KB cap on prompts + 4KB on tool params                                                                                                                                                         | **grok richer** |  **M**   |
| Mixpanel client                           |                             ⚪ none                              |                                                                                                                                                                     ✅ `xai-mixpanel` (`events_url`, `events_api_key`, `mixpanel_enabled`)                                                                                                                                                                     | grok only       |    L     |
| Sentry error reporting                    |                             ⚪ none                              |                                                                                                                                                                                          ✅ (in `xai-grok-telemetry`)                                                                                                                                                                                          | grok only       |    L     |
| Per-session structured JSON log           |  ✅ `internal/logger/logger.go` (to `~/.pi-go/log/yyyy-mm-dd/`)  |                                                                                                                                                                                        ✅ `~/.grok/logs/unified.jsonl`                                                                                                                                                                                         | equal           |    —     |
| ATIF v1.6 export                          | ✅ `internal/atif/` (`types.go:7` `SchemaVersion = "ATIF-v1.6"`) |                                                                                                                                                                                         ⚪ none (grok uses own format)                                                                                                                                                                                         | pi-go only      |    —     |

### Config

| Feature                                            |                             pi-go                              |                                        grok-build                                        | Gap           | Priority |
|----------------------------------------------------|:--------------------------------------------------------------:|:----------------------------------------------------------------------------------------:|---------------|:--------:|
| Single `config.json`                               |              ✅ `internal/config/config.go:55-71`               |                         ⚪ TOML split (config.toml + pager.toml)                          | pi-go simpler |    —     |
| Multi-source precedence (project > user > bundled) |                     ✅ `config.go:195-234`                      |                                       ✅ (similar)                                        | equal         |    —     |
| `.env` file loading                                | ✅ `cli.go:1243-1303` (`~/.pi-go/.env` + project `.pi-go/.env`) |                                      ⚪ env-var only                                      | pi-go only    |    —     |
| `${VAR}` substitution in config                    |                     ✅ `config.go:309-368`                      |                                          ⚪ none                                          | pi-go only    |    —     |
| Managed config (`requirements.toml`)               |                             ⚪ none                             | ✅ `~/.grok/requirements.toml` + `/etc/grok/requirements.toml` (root-owned, admin-locked) | grok only     |    L     |
| `disable_bypass_permissions_mode` admin lock       |                             ⚪ none                             |           ✅ `[ui] disable_bypass_permissions_mode = true` in requirements.toml           | grok only     |    L     |
| Project trust gate                                 |                 ⚪ none (always trusts project)                 |                  ✅ `~/.grok/trusted_folders.toml` (`--trust`, cascades)                  | grok only     |    M     |
| 4 config roles (default/smol/plan/slow)            |                               ✅                                |                            🟡 single default + `-m` override                             | pi-go richer  |    —     |

### Custom Models

See "Providers" section for the bulk of this; specifics:

| Feature                                  |                           pi-go                           |                       grok-build                       | Gap         | Priority |
|------------------------------------------|:---------------------------------------------------------:|:------------------------------------------------------:|-------------|:--------:|
| 3 API backends (chat/responses/messages) | 🟡 chat+responses+codex (no Anthropic `messages` backend) |                         ✅ (3)                          | grok richer |    S     |
| `env_key` array                          |              🟡 single env var per provider               |       ✅ (string or array, first non-empty wins)        | grok richer |    S     |
| `extra_headers` per model                |               ✅ (`extraHeaders` in config)                |                           ✅                            | equal       |    —     |
| Custom models endpoint                   |                          ⚪ none                           | ✅ `GROK_MODELS_BASE_URL` (fetches `{base_url}/models`) | grok only   |    L     |
| `supports_backend_search` per model      |                ⚪ uses Gemini's native flag                |                     ✅ (any model)                      | grok only   |    L     |

### Voice

| Feature                                                             | pi-go  |                            grok-build                            | Gap       |    Priority    |
|---------------------------------------------------------------------|:------:|:----------------------------------------------------------------:|-----------|:--------------:|
| Streaming STT to prompt                                             | ⚪ none | ✅ `xai-grok-voice` (WSS to `api.x.ai/v1/stt`, 25 languages, ITN) | grok only | 🚫 (long-term) |
| Push-to-talk (toggle / hold)                                        | ⚪ none |                       ✅ (Ctrl+Space / F8)                        | grok only |       🚫       |
| Audio capture (mac/win via cpal, Linux via pw-record/parec/arecord) | ⚪ none |                                ✅                                 | grok only |       🚫       |

### Mermaid

| Feature                                                    | pi-go  |                                 grok-build                                  | Gap           | Priority |
|------------------------------------------------------------|:------:|:---------------------------------------------------------------------------:|---------------|:--------:|
| Mermaid → PNG (pure Rust vendored dagre + resvg/tiny-skia) | ⚪ none | ✅ `xai-grok-mermaid` (offline, panic isolation, `MAX_OUTPUT_MEGAPIXELS=32`) | **grok only** |  **L**   |
| `mmdc` subprocess fallback                                 | ⚪ none |                              ✅ (`MmdcEngine`)                               | grok only     |    L     |
| Out-of-process render worker                               | ⚪ none |      ✅ `xai-grok-pager/src/app/mermaid_worker.rs` + per-session cache       | grok only     |    L     |

### Codebase Graph

| Feature                           |               pi-go                |                    grok-build                     | Gap           | Priority |
|-----------------------------------|:----------------------------------:|:-------------------------------------------------:|---------------|:--------:|
| tree-sitter symbol extraction     |               ⚪ none               | ✅ `xai-codebase-graph` (Rust, TS, JS, Python, Go) | **grok only** |  **M**   |
| go-to-definition                  | ✅ via LSP (`tools/lsp.go:143-152`) |          ✅ `Navigator::goto_definition`           | equal         |    —     |
| go-to-references                  | ✅ via LSP (`tools/lsp.go:154-163`) |                    ✅ (implied)                    | equal         |    —     |
| Channel-based incremental updates |               ⚪ none               |           ✅ `IndexManagerHandle` actor            | grok only     |    L     |
| Scope graph + string interner     |               ⚪ none               |         ✅ `ScopeGraph`, `StringInterner`          | grok only     |    L     |
| Symbol extraction fast path       |               ⚪ none               |             ✅ `extract_symbols_fast`              | grok only     |    L     |

### Plan Mode

| Feature                                |                                       pi-go                                       |                                  grok-build                                  | Gap           | Priority |
|----------------------------------------|:---------------------------------------------------------------------------------:|:----------------------------------------------------------------------------:|---------------|:--------:|
| 7-phase PDD plan/run flow              | ✅ `internal/sop/pdd_default.go:6-137` + `tui/plan.go:1-302` + `tui/run.go:1-1223` |                                    ⚪ none                                    | pi-go only    |    —     |
| Gate-based validation                  |           ✅ `- **name**: \`command\`` parsed in `tui/run.go:1069-1106`            |                                    ⚪ none                                    | pi-go only    |    —     |
| `/plan <idea>` (starts PDD)            |                                         ✅                                         |          🟡 `/plan [description]` (enters grok plan mode, not PDD)           | pi-go richer  |    —     |
| `/run <spec>` (executes in worktree)   |                                  ✅ `tui/run.go`                                   |                                    ⚪ none                                    | pi-go only    |    —     |
| `/run --parallel` (split spec)         |                                         ✅                                         |                                    ⚪ none                                    | pi-go only    |    —     |
| Plan mode (4-state machine, read-only) |                                      ⚪ none                                       |  ✅ `enter_plan_mode` / `exit_plan_mode` tools + `/plan` toggle (Shift+Tab)   | **grok only** |  **S**   |
| Plan file in session dir               |                                      ⚪ none                                       | ✅ `~/.grok/sessions/<cwd>/<sid>/plan.md` (auto-edited, other files rejected) | grok only     |    S     |
| Plan-mode comment UI (a/s/c/q)         |                                      ⚪ none                                       |                  ✅ (preview/commenting/prompt focus states)                  | grok only     |    S     |

### Background Tasks

| Feature                                                      |              pi-go              |                   grok-build                    | Gap           | Priority |
|--------------------------------------------------------------|:-------------------------------:|:-----------------------------------------------:|---------------|:--------:|
| Background bash (`background: true`)                         | ⚪ none (bash blocks until done) | ✅ `run_terminal_command.background` → `task_id` | **grok only** |  **M**   |
| `get_command_or_subagent_output`                             |             ⚪ none              |              ✅ (with `timeout_ms`)              | grok only     |    M     |
| `wait_commands_or_subagents` (multi, mode wait_any/wait_all) |             ⚪ none              |                 ✅ (max 20 ids)                  | grok only     |    M     |
| `kill_command_or_subagent` (SIGTERM → SIGKILL)               |             ⚪ none              |                        ✅                        | grok only     |    M     |
| `/loop` recurring scheduler                                  |             ⚪ none              |    ✅ (60s min, 7d expiry, max 50 concurrent)    | grok only     |    M     |
| `monitor` tool (line-buffered stream → notifications)        |             ⚪ none              |   ✅ (`persistent: true` for session-lifetime)   | grok only     |    M     |
| Send running task to bg mid-run (Ctrl+G)                     |             ⚪ none              |                        ✅                        | grok only     |    M     |
| Prompt queue (Ctrl+;)                                        |             ⚪ none              |                        ✅                        | grok only     |    M     |

### Compaction

| Feature                                     |                      pi-go                       |                             grok-build                              | Gap       | Priority |
|---------------------------------------------|:------------------------------------------------:|:-------------------------------------------------------------------:|-----------|:--------:|
| `/compact` (manual)                         |   ✅ `internal/tui/commands.go:80-81, 238-263`    |                       ✅ `/compact [context]`                        | equal     |    —     |
| Auto-compact at context limit               |               ⚪ none (manual only)               |        ✅ at 85% (`[session] auto_compact_threshold_percent`)        | grok only |    S     |
| 2-pass compaction                           |                      ⚪ none                      |                 ✅ `[features] two_pass_compaction`                  | grok only |    M     |
| Memory flush before compact                 |                      ⚪ none                      | ✅ `[compaction.memory_flush]` (4000 soft threshold, 8000 max write) | grok only |    M     |
| Tool result pruning (soft-trim, hard-clear) |                      ⚪ none                      |     ✅ `[compaction.pruning]` (3 turns kept, 10-turn hard-clear)     | grok only |    M     |
| Compaction checkpoints saved                |                      ⚪ none                      |                     ✅ `compaction_checkpoints/`                     | grok only |    S     |
| SimpleSummarizer (current impl)             | ✅ `internal/session/store.go` (SimpleSummarizer) |                 ✅ (multiple summarizer strategies)                  | equal     |    —     |

### Permissions

| Feature                                                                           |                 pi-go                 |                                       grok-build                                       | Gap             |                           Priority                           |
|-----------------------------------------------------------------------------------|:-------------------------------------:|:--------------------------------------------------------------------------------------:|-----------------|:------------------------------------------------------------:|
| Permission modes (default/dontAsk/bypass/acceptEdits/plan)                        | 🟡 single mode (`--yolo` config flag) |                                       ✅ 5 modes                                        | **grok richer** |                            **M**                             |
| Structured rules (`[permission].rules[]`)                                         |                ⚪ none                 |                              ✅ `{action, tool, pattern}`                               | **grok only**   |                            **M**                             |
| Compact string rules (`Bash(git *)`, `Read(/path/**)`)                            |                ⚪ none                 |                                           ✅                                            | **grok only**   |                            **M**                             |
| Glob `*` (single level) / `**` (recursive)                                        |                ⚪ none                 |                                           ✅                                            | grok only       |                              M                               |
| Chained-command parsing (`&&`/`                                                   |                                       |                                        `/`;`/`)                                        | ⚪ none          | ✅ (deny/ask check every segment AND whole; allow only whole) | grok only | M |
| Env-prefix stripping (`RUST_LOG=debug cmd`)                                       |                ⚪ none                 |                                           ✅                                            | grok only       |                              M                               |
| Wrapper stripping (`timeout`/`nice`/`ionice`/`chrt`/`stdbuf`/`env`)               |                ⚪ none                 |                         ✅ (NOT peeled: `sudo`/`xargs`/`nohup`)                         | grok only       |                              M                               |
| Dangerous commands list (`rm`/`chmod`/`chown`/`chattr`/`pkill`/`kill`/`git push`) |                ⚪ none                 |                        ✅ (always prompt even with prefix grant)                        | grok only       |                              M                               |
| Per-tool memory of grants (per project)                                           |                ⚪ none                 | ✅ `~/.grok/<state>/<project>/grants.json` (when `[ui] remember_tool_approvals = true`) | grok only       |                              S                               |
| Claude compat (.claude/settings.local.json)                                       |                ⚪ none                 |                                           ✅                                            | grok only       |                              S                               |
| MCP rules (`MCPTool(server__tool)`, `MCPTool(server__*)`)                         |                ⚪ none                 |                                 ✅ (no `mcp__` prefix)                                  | grok only       |                              S                               |
| WebFetch rules (`WebFetch(domain:host)` or full URL glob)                         |                ⚪ none                 |                                           ✅                                            | grok only       |                              S                               |
| PreToolUse hook as security boundary (deny)                                       |                ⚪ none                 |                 ✅ (`{"decision": "deny", "reason": "..."}` or exit 2)                  | **grok only**   |                            **M**                             |
| Admin-locked `requirements.toml` (deny cannot be overridden)                      |                ⚪ none                 |                           ✅ (`/etc/grok/requirements.toml`)                            | grok only       |                              L                               |

### Compatibility

| Feature                                                     |              pi-go              |                            grok-build                             | Gap                  | Priority |
|-------------------------------------------------------------|:-------------------------------:|:-----------------------------------------------------------------:|----------------------|:--------:|
| Read `.claude/CLAUDE.md`                                    | ✅ (`config.go:239-435` for MCP) |                                 ✅                                 | equal                |    —     |
| Read `.claude/skills/`                                      |      ✅ (skills.go:277-306)      |                                 ✅                                 | equal                |    —     |
| Read `.claude/settings.json` hooks                          |             ⚪ none              |                  ✅ (Cursor/Claude compat layer)                   | grok only            |    S     |
| Read `.cursor/skills/`                                      |                ✅                |                                 ✅                                 | equal                |    —     |
| Read `.cursor/rules/`                                       |           🟡 not yet            |                     ✅ (per-cell compat table)                     | grok richer          |    S     |
| Read `.cursor/mcp.json`                                     |             ⚪ none              |                                 ✅                                 | grok only            |    S     |
| Read `.cursor/hooks.json`                                   |             ⚪ none              |                     ✅ (camelCase event names)                     | grok only            |    S     |
| Read `.mcp.json`                                            |             ⚪ none              |                            ✅ (compat)                             | grok only            |    S     |
| Foreign session discovery (resume from Claude/Codex/Cursor) |             ⚪ none              | 🟡 `[compat.*] sessions = true` (staged, no scanner consumer yet) | equal (both nothing) |    —     |

### Reliability

| Feature                                                   |                       pi-go                       |                                      grok-build                                       | Gap                                                         | Priority |
|-----------------------------------------------------------|:-------------------------------------------------:|:-------------------------------------------------------------------------------------:|-------------------------------------------------------------|:--------:|
| Cross-platform crash handler (Unix signals + Windows SEH) |     🟡 `agent_loop.go:286-290` panic recover      |       ✅ `xai-crash-handler` (installed before async runtime, `last-crash.bin`)        | grok richer                                                 |    L     |
| Auto-update (grok update)                                 |    ⚪ none (manual `pi upgrade` checks GitHub)     | ✅ `grok update` + auto-check on launch (`[cli] auto_update`, `GROK_DISABLE_AUTOPR=1`) | grok richer                                                 |    S     |
| Non-blocking update check                                 |                      ⚪ none                       |                         ✅ (background check w/ notification)                          | grok only                                                   |    S     |
| Session-level error recovery (resume interrupted)         |                  ✅ `--continue`                   |                           ✅ `--resume <id>` or `--continue`                           | equal                                                       |    —     |
| Worktree pool (pre-create worktrees)                      |                      ⚪ none                       |                            ✅ `xai-fast-worktree::sync` API                            | grok only                                                   |    L     |
| BTRFS snapshot for worktree (O(1) clone)                  |                      ⚪ none                       |                                 ✅ (Linux BTRFS only)                                  | grok only                                                   |    L     |
| fsnotify single causal stream                             |                      ⚪ none                       |                   ✅ `xai-fsnotify` (broadcast channel of `FsEvent`)                   | grok only                                                   |    L     |
| gix-status thread budget under RLIMIT_NPROC               |                        n/a                        |                      ✅ (avoid `panic=abort` under spawn failure)                      | grok only                                                   |    L     |
| Hunk tracker (agent/external attribution)                 |                        n/a                        |                         ✅ `xai-hunk-tracker` (actor pattern)                          | grok only                                                   |    L     |
| SQLite journal mode (WAL on local, Truncate on NFS)       |                        n/a                        |                                ✅ `xai-sqlite-journal`                                 | grok only (n/a for pi-go since pure-Go sqlite manages this) |    —     |
| System power events (sleep/wake)                          |                      ⚪ none                       |              ✅ `xai-system-power` (macOS IOKit / Windows / Linux logind)              | grok only                                                   |    L     |
| Token estimation (bytes/4, 765/image)                     | 🟡 provider-reported (per-model `context_window`) |    ✅ `xai-token-estimation` (`BYTES_PER_TOKEN = 4`, `IMAGE_TOKEN_ESTIMATE = 765`)     | equal                                                       |    —     |

### Auto-Update

| Feature                                                             |                 pi-go                 |                 grok-build                 | Gap       | Priority |
|---------------------------------------------------------------------|:-------------------------------------:|:------------------------------------------:|-----------|:--------:|
| Manual upgrade (`pi upgrade`)                                       |          ✅ `cli/upgrade.go`           |              ✅ `grok update`               | equal     |    —     |
| Auto-check on launch                                                | 🟡 runs at startup (`cli/upgrade.go`) |        ✅ (background, non-blocking)        | equal     |    —     |
| Suppress via env (`GROK_DISABLE_AUTOUPDATER=1`, `--no-auto-update`) |             🟡 no env var             |                     ✅                      | grok only |    S     |
| Read-only `~/.grok` mount handling (skip update + warn)             |                ⚪ none                 |                     ✅                      | grok only |    S     |
| Minimum version enforcement                                         |                ⚪ none                 | ✅ `xai-grok-update/src/minimum_version.rs` | grok only |    L     |

### Image / Media

| Feature                             |                  pi-go                   |                     grok-build                      | Gap                           | Priority |
|-------------------------------------|:----------------------------------------:|:---------------------------------------------------:|-------------------------------|:--------:|
| Read image files in `read`          | 🟡 strips base64 data URIs from markdown | ✅ (`image.rs` — full image support + hover preview) | grok richer                   |    S     |
| Read PDF files                      |                  ⚪ none                  |              ✅ (`pdf.rs` — extraction)              | grok only                     |    S     |
| Read PPTX files                     |                  ⚪ none                  |                    ✅ (`pptx.rs`)                    | grok only                     |    S     |
| Image generation (`/imagine`)       |                  ⚪ none                  |                    ✅ (xAI media)                    | grok only (provider-specific) |    🚫    |
| Video generation (`/imagine-video`) |                  ⚪ none                  |                    ✅ (xAI media)                    | grok only (provider-specific) |    🚫    |
| Image paste from clipboard          |                  ⚪ none                  | ✅ (`Cmd+V`/`Ctrl+V`/`Alt+V`, image chip in prompt)  | grok only                     |    M     |

---

## Top 15 Recommendations for pi-go

Each recommendation is **concrete and actionable** — names a specific package, file, and sketch interface.

### 1. **OS-level kernel sandbox** (Priority: **L** | Effort: **L** | Risk: **Medium**)

**Why:** grok's `xai-grok-sandbox` is the single biggest security gap. Today, `internal/tools/sandbox.go:1-526`
restricts FS via `os.Root` (in-process only). Subagent subprocesses (Claude/Codex/Cursor/Copilot) and `bash` tool
commands are not isolated at the OS level.
**Proposal:**

- New `internal/sandbox/` package. On Linux, use Landlock LSM via `syscall.SYS_LANDLOCK_*` (or a small CGO-free library
  like `github.com/landlock-lsm/go-landlock`). On macOS, shell out to `sandbox-exec` with a generated Seatbelt profile.
- 4 built-in profiles matching grok: `off` / `workspace` / `devbox` / `read-only` / `strict`.
- Custom profiles via `[sandbox]` in `config.json`: `extends`, `restrict_network`, `read_only`, `read_write`, `deny` (
  globs).
- Wrap every `os/exec` (`tools/bash.go`, `subagent/spawner.go`, `subagent/spawner_acp.go`, `subagent/spawner_codex.go`)
  to apply the profile at process spawn.
- Save the profile in `meta.json` and refuse to resume a session with a mismatched profile (like grok).
  **Risk:** CGO dependency for Landlock; macOS Seatbelt shelling out to `sandbox-exec` adds latency.

### 2. **Structured permission system** (Priority: **M** | Effort: **M** | Risk: **Low**)

**Why:** pi-go's only permission knob is the all-or-nothing `--yolo` flag. Users want fine-grained control over which
bash commands and which paths can run without asking.
**Proposal:**

- New `internal/permission/` package. Define `Rule{Action: allow|deny|ask, Tool, Pattern}` and
  `Mode: default|dontAsk|bypassPermissions|acceptEdits|plan`.
- Match `Bash(cmd:*)` as `cmd` prefix; `*`/`?` don't cross `/`, `**` does. Parse chained commands (`&&`/`||`/`;`/`|`),
  strip env prefixes (`KEY=val cmd`) and wrappers (`timeout`, `nice`).
- Wire into `internal/agent/agent.go` before each tool call: `perm.Check(toolName, args) Decision`. `acceptEdits`
  auto-approves `write`/`edit`; `bypassPermissions` short-circuits after PreToolUse hooks.
- Config schema: extend `internal/config/config.go` with `Permission{Mode, Rules, Allow, Deny}`; read Claude
  `.claude/settings.local.json` for compat.
- Add `--permission-mode` CLI flag and `/permission` slash.
  **Risk:** Glob + chained-command parsing edge cases. Mitigate with the same `n/a` fail-closed stance as grok.

### 3. **Plan mode (read-only gate)** (Priority: **S** | Effort: **S** | Risk: **Low**)

**Why:** grok's `enter_plan_mode`/`exit_plan_mode` tools are a clean read-only escape hatch. pi-go's `/plan` starts a
PDD flow (different concept), but a quick "think first, then approve" toggle is missing.
**Proposal:**

- Add 4-state machine to `internal/agent/agent.go`: `Inactive` → `Pending` → `Active` → `ExitPending`.
- New tools `enter_plan_mode` (requires user approval) and `exit_plan_mode` (reads `plan.md` from session dir).
- `Active` state denies `write`/`edit` (except `plan.md`); bash allowed but `PreToolUse` hook can flag file writes.
- Wire `Shift+Tab` cycle (Normal → Plan → Always-approve) and `/plan [description]` toggle into
  `internal/tui/commands.go`.
- Plan file: `~/.pi-go/sessions/<sid>/plan.md`, auto-approved edit target.
- TUI preview with `a`/`s`/`c`/`q` actions.
  **Risk:** Naming conflict with existing `/plan` (PDD). Resolve by renaming PDD to `/pdd` or using a separate
  `/plan-mode` toggle.

### 4. **Extended hook events (13 + blocking PreToolUse)** (Priority: **M** | Effort: **M** | Risk: **Low**)

**Why:** pi-go's `internal/extension/hooks.go:1-241` only has `before_tool`/`after_tool`. The full set is needed to wire
a security boundary and lifecycle integrations.
**Proposal:**

- Extend `HookEvent` enum in `internal/extension/hooks.go` to 13 values: `SessionStart`, `UserPromptSubmit`,
  `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PermissionDenied`, `Stop`, `StopFailure`, `Notification`,
  `SubagentStart`, `SubagentStop`, `PreCompact`, `PostCompact`, `SessionEnd`.
- Make `PreToolUse` **blocking** with `{"decision": "allow"|"deny", "reason": "..."}` JSON output (or exit 2 = deny).
- Per-tool matcher on `Bash`/`Read`/`Edit`/`Grep`/etc. (Claude compat names). Reserved env vars: `GROK_HOOK_EVENT`,
  `GROK_HOOK_NAME`, `GROK_SESSION_ID`, `GROK_WORKSPACE_ROOT`.
- Call sites: `internal/agent/agent.go` (SessionStart, UserPromptSubmit, Stop, Notification),
  `internal/tools/registry.go` (PreToolUse, PostToolUse, PostToolUseFailure), `internal/permission/` (PermissionDenied),
  `internal/subagent/orchestrator.go` (SubagentStart/Stop), `internal/session/store.go` (PreCompact, PostCompact,
  SessionEnd).
- Folder trust gate: write `~/.pi-go/trusted_folders.toml`; require `--trust` or `/hooks-trust` to enable hooks for a
  project.
  **Risk:** More hook points = more places a hung script can stall the agent loop. Use a 5s default timeout (10s for
  PreToolUse) and fail-open.

### 5. **Background tasks + tasks pane (Ctrl+B)** (Priority: **M** | Effort: **M** | Risk: **Medium**)

**Why:** Long bash commands and subagents currently block the TUI. grok's `Ctrl+B` tasks pane + `background: true`
bash + `wait_commands_or_subagents` + `kill_command_or_subagent` is a much better UX.
**Proposal:**

- New `internal/background/` package: `Tracker` keyed by task_id, tracks `command`/`subagent` tasks (PID, status, output
  buffer, started_at).
- `tools/bash.go`: add `background: true` param → return `task_id` immediately; spawn the command in a goroutine with
  stdout/stderr captured.
- New tools: `get_command_or_subagent_output(task_id, timeout_ms)`,
  `wait_commands_or_subagents(task_ids[], mode, timeout_ms)`, `kill_command_or_subagent(task_id)` (SIGTERM then SIGKILL
  after 5s).
- TUI: new `internal/tui/tasks_pane.go` toggled by `Ctrl+B`. Lists subagents (already shown in sidebar), background
  tasks, `/loop` tasks, monitors. Spinners, elapsed time, kill action.
- Wire subagent `internal/subagent/orchestrator.go` to register itself in the tracker on spawn.
  **Risk:** Killing a worktree-subagent mid-merge could leave `.pi-go/worktrees/` inconsistent. Track ownership and only
  auto-kill on process group.

### 6. **Session fork + rewind** (Priority: **S** | Effort: **M** | Risk: **Medium**)

**Why:** When the agent goes down a bad path, users want to roll back. pi-go's branch support is in
`internal/session/branch.go:1-212` but exposed only as `/branch` (less discoverable than grok's `/fork` and `/rewind`).
**Proposal:**

- `/rewind`: on invoke, take a snapshot of working tree (already possible via existing `internal/subagent/worktree.go`
  git stash logic), truncate `events.jsonl` to the chosen turn index, restore files.
- `/fork [--worktree] [directive]`: copy the current session dir to a new session_id, optionally into a fresh git
  worktree (reuse `subagent/worktree.go`).
- Reuse `internal/session/branch.go` for the in-memory branch pointer; persist `parent_session_id` in `meta.json` (
  already a field).
- TUI: `/fork` and `/rewind` slash commands in `internal/tui/commands.go`. `/rewind` opens a turn picker.
  **Risk:** File snapshot/restore conflicts with concurrent subagent worktrees. Take a per-file backup tarball, not a
  full git stash.

### 7. **Mermaid rendering in TUI** (Priority: **L** | Effort: **L** | Risk: **Low**)

**Why:** grok renders Mermaid blocks in chat as PNG. pi-go users resort to copy-pasting into mermaid.live. Self-hosting
in TUI is a real differentiator.
**Proposal:**

- New `internal/mermaid/` package. Two backends:
    - **Pure Go** (preferred): embed a small Mermaid subset (flowchart + sequence) parser, render to SVG. Use
      `github.com/goccy/go-graphviz` or `github.com/lukasbob/svgo` for layout + output. For v1, accept a smaller Mermaid
      subset.
    - **`mmdc` subprocess** (fallback): spawn the official `@mermaid-js/mermaid-cli` if installed.
- Out-of-process render worker in `internal/tui/mermaid_worker.go` (using a goroutine + channel cache) to avoid blocking
  the TUI.
- Detect fenced ` ```mermaid ` blocks in tool output; replace with inline PNG via `lipgloss.Image` (or terminal native
  image protocol like Sixel/Kitty/iTerm2).
- Render cap: 32 megapixels (grok's limit).
  **Risk:** Pure-Go Mermaid is a long project. Ship the `mmdc` fallback first; pure-Go as a follow-up.

### 8. **Dashboard (multi-session overview)** (Priority: **M** | Effort: **M** | Risk: **Low**)

**Why:** A power user running 3+ parallel sessions needs a top-level view. pi-go's `--continue` is the only
multi-session affordance.
**Proposal:**

- New `internal/dashboard/` package. `List` enumerates `~/.pi-go/sessions/*/meta.json` and parses `last_activity` from
  the last `events.jsonl` line.
- Sort by state: Needs input → Working → Idle → Inactive → Completed → Failed. State derived from `events.jsonl` last
  event type + `updated_at` age.
- New TUI modal `/dashboard` (alias `/sessions`, `Ctrl+\\`). Rows show: name, last prompt (truncated to 60 chars), state
  icon, elapsed.
- Peek panel: latest response (3 lines word-wrapped), live reply input. Dispatch input (Ctrl+S) to spawn a new session
  with the typed prompt.
- Search filter: `Ctrl+/` toggles, `a:<name>`, `s:<state>`, `#<text>`.
- Persistence: `[dashboard] enabled, grouping, pinned, reorder` keys in `config.json`.
  **Risk:** Concurrent `events.jsonl` reads across sessions. Use a shared file-locker or just open per-session (each
  session is append-only and JSONL is line-safe).

### 9. **Telemetry: structured OTel metrics + events** (Priority: **M** | Effort: **M** | Risk: **Low**)

**Why:** pi-go's OTEL only emits spans. grok's typed metrics (`grok_code.token.usage`, `grok_code.tool.decision`) and
event log (`grok_code.session_start`) are PromQL-friendly and analytics-grade.
**Proposal:**

- Extend `internal/otel/otel.go` to also create a `Meter` and `Logger` (in addition to existing `Tracer`).
- New `internal/observability/metrics.go` registers metrics on init:
    - `pi_go.session.count` (counter, attrs: model, app_entrypoint)
    - `pi_go.token.usage` (counter, attrs: type=input|output|reasoning|cache_read, model)
    - `pi_go.turn.count` (counter, attrs: outcome, model)
    - `pi_go.tool.decision` (counter, attrs: tool_name, decision, access_kind, permission_mode)
    - `pi_go.tool.usage` (counter, attrs: tool_name, outcome)
    - `pi_go.error.count` (counter, attrs: error_category, model)
- New `internal/observability/events.go` emits `pi_go.session_start`, `pi_go.session_end`, `pi_go.user_prompt`,
  `pi_go.turn_completed`, `pi_go.tool_result`, `pi_go.subagent`, `pi_go.compaction`, `pi_go.api_error`, etc. as OTLP log
  records.
- **Content-free by default**: scrub `bash.command`, file paths, tool params unless `OTEL_LOG_TOOL_DETAILS=1` or
  `OTEL_LOG_USER_PROMPTS=1`. Apply the same emit-time redaction (secret-scrub, home-dir-scrub, char truncation) as grok.
- Double opt-in: master switch `PI_EXTERNAL_OTEL=1` AND explicit `OTEL_METRICS_EXPORTER=otlp` /
  `OTEL_LOGS_EXPORTER=otlp`.
  **Risk:** Schema drift with grok's metric names. Decide whether to mirror `grok_code.*` or use `pi_go.*` (recommend
  `pi_go.*` for ownership clarity).

### 10. **Custom models with 3 API backends + env_key array** (Priority: **S** | Effort: **S** | Risk: **Low**)

**Why:** pi-go already supports Chat Completions + Responses + Codex, but no Anthropic-style `messages` backend, and
`api_key` lookup is single-env-var.
**Proposal:**

- Add `anthropic_messages` (or unify) backend to `internal/provider/`. Today the Anthropic provider uses
  `anthropic-sdk-go`'s native client; expose it as a generic `messages` backend so user-added `base_url` endpoints (e.g.
  AWS Bedrock) can plug in.
- Extend `config.go` `ProviderConfig` to accept `env_key` as `string | []string` (first non-empty wins).
- Per-model `extra_headers` (already exists; verify it cascades from `[models].extra_headers`).
- New `GROK_MODELS_BASE_URL` analogue (`PI_MODELS_BASE_URL`): fetch `{base_url}/models` and cache.
- `supports_backend_search` per-model flag (default false); when true, the `web_search` tool calls the model instead of
  the default.
  **Risk:** Adding a 3rd Anthropic path complicates the test matrix. Keep `anthropic-sdk-go` as the default and only use
  the new backend when `base_url` is overridden.

### 11. **OSC notifications + sleep prevention** (Priority: **S** | Effort: **S** | Risk: **Low**)

**Why:** Long-running sessions go to background. When the model finishes, the user should see a notification. grok's
per-terminal OSC 9/99/777/BEL is the standard.
**Proposal:**

- New `internal/notify/` package. Detect terminal protocol (iTerm2=OSC 9, Kitty=OSC 99, Ghostty=OSC 777,
  WezTerm/Warp=OSC 9, others=BEL) via `TERM_PROGRAM` + `KITTY_WINDOW_ID` + a probe.
- On `task_complete` / `approval_required` / `session_ready` events, emit the appropriate OSC sequence. Honor focus
  state (use OSC 11 background query or skip if unfocused + `[ui] notifications.only_unfocused = true`).
- Sleep prevention: macOS `IOPMAssertionCreateWithName` via CGO or `caffeinate -i` subprocess; Linux
  `systemd-inhibit --what=sleep --who=pi-go --why="long-running agent" --mode=block sleep 1 &` (auto-kill on agent
  exit).
- `[ui].notifications.{method, condition, idle_threshold_secs, events, sleep_prevention, progress_bar}` config.
- Custom shell hooks: `[[ui.notifications.hooks]]` runs a command on event with `$PI_EVENT`, `$PI_MESSAGE`,
  `$PI_SESSION_ID`.
  **Risk:** Focus detection on tmux/SSH is unreliable (grok doc explicitly notes this). Default to always-notify and let
  users opt in to focus-aware.

### 12. **@ file mentions (line-precise, dir browse, hidden prefix)** (Priority: **S** | Effort: **S** | Risk: **Low**)

**Why:** grok's `@src/main.rs:10-50` is a frictionless way to point the agent. pi-go has `internal/tui/refs/` but it
only resolves plain paths.
**Proposal:**

- Extend `internal/tui/refs/` parser to handle: `@<path>` (file/dir), `@<dir>/` (browse), `@<file>:<line>-<line>` (
  line-precise), `@!.<file>` (hidden files like `@!.env`).
- The TUI input box shows the resolved file/line as a chip on tab completion; pressing Enter inserts a token that the
  agent's system prompt expands to the file contents at that range.
- Add the `@` chip to the bottom-right prompt hint bar (alongside `Alt+Enter: newline` per grok's pattern).
  **Risk:** Shell-style escaping of `@` in user input. Use a backslash escape or only treat `@` as a ref at the start of
  a token.

### 13. **Memory: hybrid search (vector + BM25) + dream consolidation** (Priority: **M** | Effort: **M** | Risk: **Low
**)

**Why:** pi-go's `internal/memory/` is FTS5-only; `internal/palace/` has embeddings but no first-class hybrid ranking.
grok's `vector 0.7 + BM25 0.3` hybrid is state-of-art.
**Proposal:**

- Extend `internal/palace/store.go` to support `sqlite-vec` (or `modernc.org/sqlite` with the `vec0` extension). Add a
  `SearchHybrid(query, vectorWeight, textWeight, minScore)` function that returns ranked results.
- New `/dream` slash command + auto-scheduler (`[memory.dream] enabled, min_hours, min_sessions`): on session end, if
  criteria met, spawn a smol subagent to consolidate last 24h of session observations into wing-scoped topics (MemPalace
  drawers with type=`topic`).
- Add `pi_go.memory.search_metrics` OTEL metric to track query latency and result quality (for tuning weights).
- Add MMR re-ranking (`lambda=0.7`) and temporal decay (`half_life_days=7`) as opt-in knobs.
- File watcher (`[memory.watcher] enabled`): reindex palace on external edits via `fsnotify` (pure Go:
  `github.com/fsnotify/fsnotify`).
  **Risk:** `sqlite-vec` requires CGO or a custom SQLite build. Use `modernc.org/sqlite` with a prebuilt extension blob.

### 14. **Cursor/Claude compatibility layers** (Priority: **S** | Effort: **M** | Risk: **Low**)

**Why:** Most users have an existing `~/.claude/` or `~/.cursor/` directory with skills, hooks, rules, MCP servers.
pi-go only reads a fraction of this.
**Proposal:**

- Extend `internal/extension/skills.go` to scan `.claude/skills/`, `.cursor/skills/`, `.agents/skills/` (already done).
  Add `.claude/rules/`, `.cursor/rules/` (per-file rules).
- New `internal/extension/hooks_claude.go`: parse `.claude/settings.json` `hooks` table with camelCase event names (
  `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, etc.). Map to pi-go's extended hook events (see #4).
- Extend `internal/extension/mcp.go` to read `.claude.json` (already done), `.cursor/mcp.json`, `.mcp.json` formats.
- New `[compat] {claude: bool, cursor: bool}` config in `config.json` to toggle.
  **Risk:** Drift between the two source-of-truths. Version-pin the expected format versions (e.g.
  `claude-settings-v1`).

### 15. **Auth: OIDC + external auth provider** (Priority: **L** | Effort: **L** | Risk: **Low**)

**Why:** Enterprises with OIDC SSO can't use pi-go's ChatGPT-only OAuth. grok's external auth provider (shell-out
binary) is a clean escape hatch for custom IdPs.
**Proposal:**

- Extend `internal/auth/auth.go` with OIDC Authorization Code + PKCE: `[auth.oidc] issuer, client_id, scopes, audience`.
  Loopback redirect on `http://127.0.0.1:<port>/callback`.
- Add `[auth] auth_provider_command, auth_provider_label, auth_token_ttl`: shell out to a binary, contract:
  `stdout = token` (bare or JSON `{access_token, refresh_token, expires_in, issuer}`), `stderr = user-facing status`.
  `GROK_AUTH_EXPIRED=1` set on refresh.
- New `env_key []string` resolution: first non-empty env var wins.
- Auto-refresh: ~5 min before expiry (`PI_AUTH_EARLY_INVALIDATION_SECS=300`), or on 401.
  **Risk:** OIDC implementations vary (Okta, Auth0, Keycloak, Azure AD). Test against a representative IdP, not all.

---

## What pi-go Already Does Better

Don't regress these — they are real differentiators.

1. **17 bundled subagents with 3 execution modes** (`internal/subagent/bundled/*.md`). grok has 3 built-ins (
   general-purpose, explore, plan) and a single-mode spawn. pi-go's `tasks[]` (parallel, max 8) and `chain[]` (
   sequential, max 8, `{previous}` template) are unique.
2. **3-layer memory coexistence** — claude-mem observations (`internal/memory/`) AND MemPalace wings/rooms/drawers (
   `internal/palace/`), plus a knowledge graph (subject/predicate/object, time-versioned) and per-agent diary. grok has
   one combined memory system.
3. **Hidden-Unicode skill auditing** (`internal/audit/chars.go:1-160`) — detects the "Glassworm" Variation Selector
   Supplement vector. pi-go is the only agent with this defense.
4. **Go 1.24+ `os.Root` sandbox** (`internal/tools/sandbox.go:1-526`) — portable, no kernel deps, in-process. Less
   robust than Landlock/Seatbelt but easier to deploy.
5. **Webserver with pairing-code auth** (`internal/webserver/`) — xterm.js + WebSocket + 6-digit code + QR code, per-tab
   PTY sessions. pi-go is the only agent that runs in a browser out-of-the-box.
6. **7-phase PDD plan/run workflow** (`internal/sop/pdd_default.go:6-137` + `internal/tui/plan.go:1-302` +
   `internal/tui/run.go:1-1223`) — vertical-slice plans, gate-based validation, parallel worktree split. Grok's `/plan`
   is a read-only gate, not a multi-phase SOP.
7. **Stuck-detection** in agent loop (`internal/tui/agent_loop.go:85-148`) — SHA256-fingerprinted identical call
   detector, AB/AB cycle detector, error streak. Auto-aborts runaway loops.
8. **ATIF v1.6 export** (`internal/atif/`, `SchemaVersion = "ATIF-v1.6"` at `types.go:7`) — interop with the Agent
   Trajectory Interchange Format.
9. **Anthropic advisor tool** (`provider/anthropic.go:161-200`) — main model can consult a separate "advisor" model
   mid-response (e.g. opus from sonnet).
10. **Output compactor (RTK)** (`internal/tools/compactor.go:1-174`) — pipeline (ANSI strip, test aggregate, build
    filter, git compact, linter aggregate, search group, smart truncate) with `/rtk` command and metrics. Cuts token
    usage dramatically on test/build output.
11. **Type coercion layer** (`tools/registry.go:196-276`) — `coercingTool` wrapper auto-converts string→int/bool/JSON,
    handles parameter name aliases (`type→agent`, `prompt→task`), strips stringified JSON arrays. Solves the "LLM sent
    wrong type" problem at the tool layer.
12. **Coverage gate** — 80%+ test coverage enforced in CI.
13. **ACP server AND client** — pi-go runs as an ACP agent (`pi acp-server`) AND spawns ACP-coded agents (claude,
    gemini, cursor, copilot). Grok only runs as an ACP agent.
14. **Per-session structured JSON log** (`internal/logger/logger.go`) — `~/.pi-go/log/yyyy-mm-dd/session-HH-MM-SS.log`
    with event types. 500ms LLM-text coalescing.

---

## Risks / Don't-Regress

Things grok does that pi-go should **not** blindly copy. pi-go's choices are better here.

1. **60+ tools with profile variants** (grok_build + grok_build_concise + grok_build_hashline + codex + opencode).
   pi-go's `coercingTool` + alias layer (`tools/registry.go:196-276`) covers the same ground with one tool per concept.
   **Don't add 5 profiles** — extend the alias/coercion layer instead.
2. **3 separate config files** (`config.toml` + `pager.toml` + `sandbox.toml` + `auth.json` + `requirements.toml`).
   pi-go's single `config.json` + `.env` is easier to audit and reason about. **Don't split pi-go's config.**
3. **Cursor/Claude compat layers as runtime tables** (`[compat.cursor]`, `[compat.claude]`, `[compat.codex]` with 6+
   cells each). pi-go's read-once-on-discovery approach in `internal/extension/skills.go` is simpler. **Keep compat
   scans pure discovery, not config.**
4. **13 hook events with regex matchers, HTTP hooks, plugin hooks, folder trust, etc.** grok's `xai-grok-hooks` is a
   small framework. pi-go's `before_tool`/`after_tool` shell hooks are sufficient for 80% of use cases. **Extend
   incrementally** (see #4 in recommendations), don't ship a full framework.
5. **Mermaid out-of-process subprocess** (`xai-grok-pager/src/app/mermaid_worker.rs`). Necessary for Rust because
   `panic=abort` can't be caught in-process. **In Go, panics are recoverable** — use a goroutine + recover() instead.
   Simpler.
6. **Hunk tracker + gix-status + sqlite-journal + fsnotify** (4 separate crates for low-level FS work). pi-go's pure-Go
   `os.Root` + standard library is sufficient. **Don't pull in heavy FS abstractions.**
7. **5 permission modes with rules engine** is overkill for v1. Ship 3 modes (default/acceptEdits/bypass) + a simple
   rule string, then add dontAsk + plan based on demand.
8. **BTRFS snapshots + worktree pool** (Linux-only). grok's `xai-fast-worktree` is a 500-line crate. pi-go's
   `git worktree add` is fine. **Don't optimize the cold path.**

---

## Open Questions for the user

1. **Which gap is highest priority?** Sandbox (#1) is the biggest security win but takes 2-3 weeks. Permission system (
   #2) is a 1-week change with broad UX impact. Plan mode (#3) overlaps with existing `/plan` — needs naming decision.
2. **Should pi-go mirror grok's metric names** (`grok_code.*`) for shared dashboards, or use `pi_go.*`? Recommendation:
   `pi_go.*` (ownership).
3. **Is the OIDC + external auth provider worth the effort** (#15) for an enterprise tier, or skip for now?
4. **Mermaid** (#7) — pure Go (months) or `mmdc` subprocess fallback (days)?
5. **Do we want a plugin/marketplace system** (grok's `xai-grok-plugin-marketplace`)? Or is the SKILL.md system already
   enough?
6. **Should we keep `/plan` for PDD** and add `/plan-mode` (grok-style read-only), or rename PDD to `/pdd` to free up
   `/plan`? Recommendation: keep `/plan` for PDD, add `/plan-mode` for grok-style.
7. **Dashboard** (#8) — build for the multi-session power user, or wait for explicit demand?
8. **Voice** — explicit non-goal for pi-go, or roadmap item?

---

## Appendix A: Package / Crate Map

Maps grok-build's 60+ Rust crates to pi-go's 23 internal packages. `(n/a)` means the concept doesn't have an equivalent
in pi-go.

| grok-build crate                                   | pi-go package                                                 | Notes                                                             |
|----------------------------------------------------|---------------------------------------------------------------|-------------------------------------------------------------------|
| `xai-grok-pager` (TUI)                             | `internal/tui`                                                | Both are the TUI frontend.                                        |
| `xai-grok-pager-minimal` / `xai-grok-pager-render` | `internal/tui` (single sub-mode)                              | pi-go has one TUI; grok has 3 render modes.                       |
| `xai-grok-pager-bin` (binary)                      | `cmd/pi/main.go`                                              | Entry point.                                                      |
| `xai-grok-pager-pty-harness`                       | (n/a)                                                         | grok has a PTY-based test harness; pi-go uses `teatest` patterns. |
| `xai-grok-shell` (agent backend)                   | `internal/agent` + `internal/subagent`                        | pi-go splits agent runtime from subagent orchestration.           |
| `xai-grok-shell-base` (CPU profile, env)           | (n/a)                                                         | pi-go uses `--pprof` flag.                                        |
| `xai-grok-shell-session-support`                   | `internal/session`                                            | Session service.                                                  |
| `xai-grok-tools` (60+ tools)                       | `internal/tools` + `internal/palace/tools.go`                 | pi-go: 33 tools in 5 groups.                                      |
| `xai-grok-tools-api` (protobuf)                    | (n/a)                                                         | grok serializes tools as protobuf.                                |
| `xai-grok-workspace` (FS, VCS, exec, discovery)    | `internal/tools/sandbox.go` + `internal/subagent/worktree.go` | pi-go's workspace is implicit; grok has 17 submodules.            |
| `xai-grok-workspace-client`                        | `internal/webserver`                                          | Browser session backend.                                          |
| `xai-grok-workspace-types`                         | (n/a)                                                         | grok has explicit workspace types.                                |
| `xai-grok-config`                                  | `internal/config`                                             | Config types.                                                     |
| `xai-grok-config-types`                            | `internal/config`                                             | pi-go merges.                                                     |
| `xai-grok-env`                                     | `internal/cli/cli.go:1243-1303`                               | Env-var helpers.                                                  |
| `xai-grok-paths`                                   | (n/a — uses `os.UserConfigDir()`)                             | pi-go uses stdlib.                                                |
| `xai-grok-version`                                 | `cmd/pi/main.go`                                              | Version constant.                                                 |
| `xai-grok-models` (default model IDs)              | `internal/provider/provider.go:202-271`                       | pi-go hardcodes model window sizes.                               |
| `xai-grok-sampler` (LLM sampling)                  | `internal/provider/*` (per-provider)                          | pi-go uses ADK Go's `model.LLM` interface.                        |
| `xai-grok-sampling-types`                          | `internal/provider/types`                                     | pi-go reuses ADK Go types.                                        |
| `xai-grok-auth`                                    | `internal/auth`                                               | OAuth seam.                                                       |
| `xai-grok-http`                                    | (n/a — uses stdlib `net/http`)                                | pi-go uses stdlib.                                                |
| `xai-grok-secrets`                                 | `internal/tools/redact.go` + `internal/subagent/environ.go`   | pi-go scrubs secrets at the tool boundary.                        |
| `xai-grok-shared`                                  | (n/a)                                                         | Common types.                                                     |
| `xai-grok-markdown` (TUI rendering)                | `internal/tui/chat.go` (Glamour)                              | pi-go uses Glamour.                                               |
| `xai-grok-markdown-core`                           | (n/a)                                                         | pi-go doesn't have a separate markdown core.                      |
| `xai-grok-test-support`                            | (test files in each package)                                  | pi-go has per-package `*_test.go`.                                |
| `xai-grok-announcements`                           | (n/a)                                                         | Not in pi-go.                                                     |
| `xai-grok-agent`                                   | `internal/agent/agent.go`                                     | Agent runtime.                                                    |
| `xai-agent-lifecycle`                              | `internal/agent` + `internal/session`                         | pi-go merges.                                                     |
| `xai-chat-state`                                   | `internal/tui/chat.go` + `internal/session/store.go`          | pi-go splits.                                                     |
| `xai-acp-lib`                                      | `internal/acp`                                                | ACP transport.                                                    |
| `xai-hooks-plugins-types`                          | `internal/extension` (hooks, skills)                          | pi-go merges.                                                     |
| `xai-grok-hooks`                                   | `internal/extension/hooks.go`                                 | Hook dispatcher.                                                  |
| `xai-grok-plugin-marketplace`                      | (n/a)                                                         | pi-go has no plugin marketplace.                                  |
| `xai-grok-mcp`                                     | `internal/extension/mcp.go`                                   | MCP integration.                                                  |
| `xai-grok-memory`                                  | `internal/memory` + `internal/palace`                         | pi-go has two memory subsystems; grok has one.                    |
| `xai-grok-sandbox`                                 | `internal/tools/sandbox.go` (os.Root only)                    | pi-go lacks OS-level kernel sandbox.                              |
| `xai-grok-update`                                  | `internal/cli/upgrade.go`                                     | Auto-update.                                                      |
| `xai-grok-telemetry`                               | `internal/otel` + `internal/logger`                           | pi-go has spans + JSON logs; grok has typed metrics + events.     |
| `xai-grok-voice`                                   | (n/a)                                                         | Not in pi-go.                                                     |
| `xai-grok-mermaid`                                 | (n/a)                                                         | Not in pi-go.                                                     |
| `xai-grok-subagent-resolution`                     | `internal/subagent/agents.go`                                 | Subagent config resolution.                                       |
| `xai-codebase-graph`                               | (n/a — uses LSP instead)                                      | pi-go delegates symbol lookup to LSP.                             |
| `xai-fast-worktree`                                | `internal/subagent/worktree.go`                               | pi-go uses `git worktree` directly.                               |
| `xai-crash-handler`                                | `internal/tui/agent_loop.go:286-290` (panic recovery only)    | pi-go's panic recovery; grok has cross-platform signals.          |
| `xai-token-estimation`                             | (n/a — uses provider-reported windows)                        | pi-go doesn't estimate locally.                                   |
| `xai-system-power`                                 | (n/a)                                                         | Not in pi-go.                                                     |
| `xai-fsnotify`                                     | (n/a)                                                         | Not in pi-go.                                                     |
| `xai-gix-status`                                   | (n/a — uses `internal/tools/git_*.go`)                        | pi-go shells out to `git`.                                        |
| `xai-hunk-tracker`                                 | (n/a)                                                         | Not in pi-go.                                                     |
| `xai-sqlite-journal`                               | (n/a — uses `modernc.org/sqlite` defaults)                    | pi-go uses pure-Go SQLite.                                        |
| `xai-prompt-queue`                                 | (n/a)                                                         | Not in pi-go.                                                     |
| `xai-ratatui-textarea`                             | `charm.land/bubbletea/v2` (third-party)                       | pi-go uses upstream Bubble Tea.                                   |
| `xai-ratatui-inline`                               | `internal/tui/chat.go` (ANSI handling)                        | pi-go uses `x/exp/ansi` or stdlib.                                |
| `xai-mixpanel`                                     | (n/a)                                                         | Not in pi-go.                                                     |
| `xai-file-utils`                                   | (n/a — uses stdlib `os`)                                      | pi-go uses stdlib.                                                |
| `xai-tty-utils`                                    | (n/a)                                                         | Not in pi-go.                                                     |
| `xai-tracing-macros`                               | (n/a)                                                         | pi-go uses `go.opentelemetry.io/otel/trace`.                      |
| `ptyctl`                                           | (n/a)                                                         | Not in pi-go.                                                     |
| `ptyctl-cli`                                       | (n/a)                                                         | Not in pi-go.                                                     |

**Summary:** pi-go's 23 packages cover ~70% of grok's 60 crates. The 30% gap is in (a) **OS-level primitives** (sandbox,
crash handler, system power, fsnotify, hunk tracker, gix-status), (b) **rendering** (mermaid, voice, custom markdown), (
c) **extensibility** (plugin marketplace, custom theming), and (d) **telemetry depth** (Mixpanel, Sentry, structured
metrics).

---

## Appendix B: Tool Inventory Diff

Every tool on both sides, by name. ✅ = implemented, ⚪ = none, 🟡 = partial. Tool names are **internal identifiers** (
grok's `run_terminal_cmd`, pi-go's `bash`).

| Tool category          | pi-go                                                                                                           | grok-build                                                                                                                                    |
|------------------------|-----------------------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------|
| File read              | `read`                                                                                                          | `read_file` (with image/pdf/pptx)                                                                                                             |
| File write             | `write`                                                                                                         | `search_replace` / `Write`                                                                                                                    |
| File edit              | `edit` (with retries + fuzzy-diff)                                                                              | `search_replace`                                                                                                                              |
| Directory list         | `ls`                                                                                                            | `list_dir`                                                                                                                                    |
| Directory tree         | `tree`                                                                                                          | ⚪                                                                                                                                             |
| Glob                   | `find` (alias `glob`)                                                                                           | `Glob` (separate)                                                                                                                             |
| Content search         | `grep` (alias `ripgrep`)                                                                                        | `grep`                                                                                                                                        |
| Bash                   | `bash`                                                                                                          | `run_terminal_command` (persistent shell)                                                                                                     |
| Git status             | `git-overview`                                                                                                  | ⚪ (uses `bash git status`)                                                                                                                    |
| Git diff               | `git-file-diff`                                                                                                 | ⚪                                                                                                                                             |
| Git hunk               | `git-hunk`                                                                                                      | ⚪                                                                                                                                             |
| LSP                    | 7 tools (`lsp-diagnostics/definition/references/hover/symbols/workspace-symbol/code-action`)                    | 1 `LspTool` (gated)                                                                                                                           |
| Subagent               | `subagent` (single/parallel/chain)                                                                              | `spawn_subagent` (single mode, but `capability_mode` + `isolation` + `resume_from`)                                                           |
| A2A                    | `a2a`                                                                                                           | ⚪                                                                                                                                             |
| Memory                 | `mem-search`, `mem-timeline`, `mem-get`                                                                         | `memory_search`, `memory_get`                                                                                                                 |
| MemPalace              | 10 tools (`palace-status/search/add/kg-add/kg-query/kg-invalidate/kg-timeline/traverse/diary-write/diary-read`) | ⚪                                                                                                                                             |
| Web search             | ⚪ (use external MCP)                                                                                            | `web_search`                                                                                                                                  |
| Web fetch              | ⚪ (use external MCP)                                                                                            | `web_fetch` (with proxy + allowed-domains)                                                                                                    |
| Ask user question      | ⚪ (use TUI prompt)                                                                                              | `ask_user_question` (30 min timeout)                                                                                                          |
| Todo                   | ⚪ (use TUI plan)                                                                                                | `todo_write`                                                                                                                                  |
| Image generation       | ⚪                                                                                                               | `image_gen` (`/imagine`)                                                                                                                      |
| Image edit             | ⚪                                                                                                               | `image_edit`                                                                                                                                  |
| Video generation       | ⚪                                                                                                               | `image_to_video`, `reference_to_video`                                                                                                        |
| Monitor (event stream) | ⚪                                                                                                               | `monitor` (line-buffered)                                                                                                                     |
| Scheduler              | ⚪                                                                                                               | `scheduler_create`, `scheduler_delete`, `scheduler_list`                                                                                      |
| Background task        | ⚪                                                                                                               | `get_command_or_subagent_output`, `wait_commands_or_subagents`, `kill_command_or_subagent` (via `background: true` on `run_terminal_command`) |
| Plan mode              | ⚪ (use `/plan` + `/run`)                                                                                        | `enter_plan_mode`, `exit_plan_mode`                                                                                                           |
| Apply patch (Codex)    | ⚪                                                                                                               | `apply_patch`                                                                                                                                 |
| Concise variants       | ⚪ (alias + coercion layer)                                                                                      | `BashConciseTool`, `ReadFileConciseTool`, `SearchReplaceConciseTool`                                                                          |
| Hashline variants      | ⚪                                                                                                               | `HashlineReadTool`, `HashlineEditTool`, `HashlineGrepTool`                                                                                    |
| Meta (search/use)      | ⚪                                                                                                               | `search_tool`, `use_tool` (for MCP)                                                                                                           |
| Codex profile          | ⚪                                                                                                               | 4: `ApplyPatchTool`, `CodexListDirTool`, `CodexGrepFilesTool`, `CodexReadFileTool`                                                            |
| Opencode profile       | ⚪                                                                                                               | 8: `OpenCodeBashTool/ReadTool/EditTool/WriteTool/GrepTool/GlobTool/TodoWriteTool/SkillTool`                                                   |

**Summary:** pi-go has **33 tools** in 5 groups (core 11 + lsp 7 + mem 3 + palace 10 + subagent 1 + a2a 1). grok-build
has **60+ tools** in 1 group with 4 profile variants (grok_build, grok_build_concise, grok_build_hashline, codex,
opencode). pi-go's narrower scope is compensated by the `coercingTool` alias/coercion layer at
`internal/tools/registry.go:196-276`.

---

## Appendix C: Slash Command Parity

| Slash command                                         | pi-go (`internal/tui/commands.go`)  | grok-build (`xai-grok-pager/src/slash/commands/`) |
|-------------------------------------------------------|-------------------------------------|---------------------------------------------------|
| `/help`                                               | ✅                                   | ✅                                                 |
| `/clear` (alias `/new`)                               | ✅                                   | ✅ (`/new` + `/clear`)                             |
| `/copy`                                               | ✅                                   | ✅                                                 |
| `/model` (alias `/m`)                                 | ✅                                   | ✅                                                 |
| `/session` (info)                                     | ✅                                   | ✅ (`/session-info`)                               |
| `/context`                                            | ✅                                   | ✅                                                 |
| `/compact`                                            | ✅                                   | ✅                                                 |
| `/history`                                            | ✅                                   | ✅                                                 |
| `/commit`                                             | ✅                                   | ⚪ (uses `bash git commit`)                        |
| `/branch`                                             | ✅                                   | ⚪                                                 |
| `/plan` (PDD)                                         | ✅                                   | 🟡 (`/plan` = read-only gate, different concept)  |
| `/run`                                                | ✅                                   | ⚪                                                 |
| `/theme` (alias `/t`)                                 | ✅                                   | ✅ (5 themes + auto)                               |
| `/subagents`                                          | ✅                                   | ⚪ (subagents in `/dashboard` only)                |
| `/rtk` (compactor stats)                              | ✅                                   | ⚪                                                 |
| `/mcp`                                                | ✅                                   | ✅ (`/mcps` modal)                                 |
| `/login`                                              | ✅                                   | ✅                                                 |
| `/logout`                                             | ⚪ (use `pi logout` not implemented) | ✅                                                 |
| `/restart`                                            | ✅                                   | ⚪                                                 |
| `/skills`                                             | ✅                                   | ✅                                                 |
| `/skill-create`                                       | ✅                                   | ✅ (`/create-skill` wizard)                        |
| `/skill-load`                                         | ✅                                   | ⚪ (auto-load)                                     |
| `/skill-list`                                         | ✅                                   | ⚪ (`/skills` modal)                               |
| `/exit` / `/quit`                                     | ✅                                   | ✅                                                 |
| `/ping`                                               | ✅                                   | ⚪                                                 |
| Dynamic `/<skillname>`                                | ✅                                   | ✅                                                 |
| `/rewind`                                             | ⚪                                   | ✅                                                 |
| `/fork`                                               | ⚪                                   | ✅ (with `--worktree`)                             |
| `/resume`                                             | ✅                                   | ✅ (with content search)                           |
| `/effort`                                             | ⚪ (use `--smol`/`--slow`)           | ✅ (`low`/`medium`/`high`/`xhigh`)                 |
| `/always-approve` (toggle)                            | 🟡 (config flag only)               | ✅ (toggle)                                        |
| `/auto` (classifier)                                  | ⚪                                   | ✅                                                 |
| `/multiline` (alias `/ml`)                            | ⚪ (always available)                | ✅ (toggle)                                        |
| `/compact-mode`                                       | ⚪                                   | ✅                                                 |
| `/vim-mode`                                           | ⚪                                   | ✅                                                 |
| `/minimal` / `/fullscreen`                            | ⚪                                   | ✅                                                 |
| `/view-plan`                                          | ⚪                                   | ✅                                                 |
| `/memory` (alias `/mem`)                              | ⚪ (use `pi memory` CLI)             | ✅                                                 |
| `/flush`                                              | ⚪                                   | ✅                                                 |
| `/dream`                                              | ⚪                                   | ✅                                                 |
| `/remember <text>`                                    | ⚪                                   | ✅                                                 |
| `/hooks`                                              | ⚪                                   | ✅                                                 |
| `/plugins`                                            | ⚪                                   | ✅                                                 |
| `/marketplace`                                        | ⚪                                   | ✅                                                 |
| `/imagine`                                            | ⚪                                   | ✅ (xAI media)                                     |
| `/imagine-video`                                      | ⚪                                   | ✅                                                 |
| `/loop [interval] [prompt]`                           | ⚪                                   | ✅ (recurring)                                     |
| `/goal`                                               | ⚪                                   | ✅                                                 |
| `/btw <aside>`                                        | ⚪                                   | ✅                                                 |
| `/terminal-setup`                                     | ⚪                                   | ✅                                                 |
| `/release-notes` (alias `/changelog`)                 | ⚪                                   | ✅                                                 |
| `/docs` (alias `/howto`)                              | ⚪                                   | ✅                                                 |
| `/import-claude`                                      | ⚪                                   | ✅                                                 |
| `/config-agents` (alias `/agents`)                    | ⚪                                   | ✅                                                 |
| `/personas`                                           | ⚪                                   | ✅                                                 |
| `/usage`                                              | ⚪                                   | ✅                                                 |
| `/privacy`                                            | ⚪                                   | ✅                                                 |
| `/settings` (alias `/config`)                         | ⚪                                   | ✅                                                 |
| `/timestamps`                                         | ⚪                                   | ✅                                                 |
| `/queue`                                              | ⚪                                   | ✅                                                 |
| `/recap`                                              | ⚪                                   | ✅                                                 |
| `/timeline`                                           | ⚪                                   | ✅                                                 |
| `/transcript`                                         | ⚪                                   | ✅                                                 |
| `/share`                                              | ⚪                                   | ✅                                                 |
| `/tasks`                                              | ⚪                                   | ✅                                                 |
| `/dashboard` (alias `/sessions`, `/agents-dashboard`) | ⚪                                   | ✅                                                 |
| `/voice`                                              | ⚪                                   | ✅                                                 |
| `/jump` / `/find` / `/expand`                         | ⚪                                   | ✅                                                 |
| `/announcements`                                      | ⚪                                   | ✅                                                 |
| `/scroll-debug`                                       | ⚪                                   | ✅                                                 |
| `/toggle-mouse-reporting`                             | ⚪                                   | ✅                                                 |
| `/audit` (hidden-Unicode scanner)                     | ✅ (`pi audit` CLI)                  | ⚪                                                 |
| `/export`                                             | ⚪                                   | ✅                                                 |

**Summary:** pi-go has ~30 slash commands. grok-build has ~70 (counting aliases). pi-go's commands are more focused (
PDD, RTK, branch). grok's commands are more diverse (media, scheduling, marketplace, billing, privacy, settings modal).

---

## Appendix D: Top 5 Highest-Leverage (Copilot Confirms)

Confirmed by `copilot -p` focused analysis (5 features, returned in 20s). These align with our top picks above:

1. **Permission system (structured rules + modes)** — `internal/permission/` package, glob matchers, 4-5 modes, wrap
   tool calls. **Matches our #2.**
2. **Fork/rewind (session branching + turn undo)** — `internal/session` + new TUI commands. **Matches our #6.**
3. **Plan mode with read-only gate** — extend `internal/sop` + new `internal/permission` gate. **Matches our #3.**
4. **Hook events (expanded set + PreToolUse as security boundary)** — extend `internal/extension/hooks.go`. **Matches
   our #4.**
5. **Background task management (tasks pane + wait/kill)** — `internal/tools` + `internal/tui`. **Matches our #5.**

These are also the 5 features a power user would notice missing first when migrating from grok-build to pi-go.

---

## Appendix E: What pi-go Should NOT Do (Top 1)

**Don't copy grok's 60-tool profile variant system.** pi-go's `coercingTool` + alias layer is 80 lines of code and
handles 100% of the same use cases. Grok's `grok_build` + `grok_build_concise` + `grok_build_hashline` + `codex` +
`opencode` profiles are 5× the maintenance burden for the same functionality. If a user needs a "concise read", just
register an alias in `internal/tools/registry.go:196-276`.

The second-strongest "don't" is **don't split config into TOML files**. pi-go's single `config.json` + `.env` is the
right call. grok's `config.toml` + `pager.toml` + `sandbox.toml` + `auth.json` + `requirements.toml` is a configuration
management problem masquerading as a feature.

---

*End of report.*
