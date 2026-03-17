# Gap Analysis: pi-go vs oh-my-pi

## pi-go Current State (Baseline)
- ~10,356 LOC across 43 Go files
- Built on Google ADK Go v0.6.0
- 8 core tools: read, write, edit, bash, grep, find, ls, tree
- 3 LLM providers: Anthropic, OpenAI, Gemini + Ollama
- 4 output modes: interactive (Bubble Tea), print, json, rpc
- Extensions: hooks, skills (.SKILL.md), MCP servers
- Sessions: file-based JSONL, branching, compaction, resume
- Sandbox: `os.Root`-based filesystem isolation

---

## Feature-by-Feature Gap Mapping

### Legend
- **Has** = pi-go already has equivalent functionality
- **Partial** = pi-go has basic version, oh-my-pi has more
- **Gap** = pi-go lacks this entirely
- **N/A** = not applicable to Go architecture

| # | oh-my-pi Feature | pi-go Status | Notes |
|---|-----------------|--------------|-------|
| 1 | Hashline Edits | **Partial** | pi-go has string-match edit; no hash anchoring |
| 2 | AI Git Commits | **Gap** | No git inspection tools, no split commits |
| 3 | Python/IPython Tool | **Gap** | No REPL/interpreter tool |
| 4 | LSP Integration | **Gap** | No language server support |
| 5 | TTSR (pattern rules) | **Gap** | Has static skills, no pattern-triggered injection |
| 6 | Interactive Code Review | **Gap** | No /review command or findings system |
| 7 | Subagent/Task System | **Gap** | Single agent only, no delegation |
| 8 | Model Roles | **Gap** | Single model per session, no role routing |
| 9 | Todo Tool | **Gap** | No task tracking within sessions |
| 10 | Ask Tool | **Gap** | No structured user questioning |
| 11 | Custom Slash Commands | **Partial** | Has built-in slash commands, not user-extensible |
| 12 | Universal Config Discovery | **Gap** | Only reads own config files |
| 13 | MCP & Plugin System | **Partial** | Has MCP stdio, no HTTP/OAuth/plugins |
| 14 | Web Search & Fetch | **Gap** | No web capabilities |
| 15 | SSH Tool | **Gap** | No remote host support |
| 16 | Browser Tool | **Gap** | No browser automation |
| 17 | Multi-Credential Support | **Gap** | Single API key per provider |
| 18 | Image Generation | **Gap** | No image support |
| 19 | TUI Overhaul | **Partial** | Has Bubble Tea TUI, lacks powerline/themes/history |
| 20 | Native Rust Engine | **N/A** | Go uses stdlib; no need for N-API addon |
| 21 | AST Tools | **Gap** | No syntax-aware search/edit |
| 22 | Background Jobs | **Gap** | Synchronous tool execution only |
| 23 | @file Auto-Read | **Gap** | No inline file injection |

---

## Priority Tiers (Value vs Effort for Go implementation)

### Tier 1 — High Value, Moderate Effort
These features would most significantly improve pi-go as a coding agent:

| Feature | Value | Effort | Rationale |
|---------|-------|--------|-----------|
| **LSP Integration** | Very High | High | Go has `gopls` ecosystem; LSP client via JSON-RPC. Enables diagnostics, go-to-def, format-on-write. Transforms agent from text-based to code-aware. |
| **Subagent/Task System** | Very High | High | ADK Go supports multi-agent; needs orchestration layer. Enables parallel exploration, plan-then-execute patterns. |
| **AI Git Commits** | High | Medium | Go's `go-git` or shell `git` commands. Split commits, hunk staging, conventional commit validation. High daily-use value. |
| **Model Roles** | High | Low | Config + routing logic. Enables smol/slow/plan/commit model selection. Cheap to implement, high flexibility gain. |

### Tier 2 — Medium Value, Low-Medium Effort
Good quality-of-life improvements:

| Feature | Value | Effort | Rationale |
|---------|-------|--------|-----------|
| **Ask Tool** | Medium | Low | Simple structured prompting. Improves agent-user interaction quality. |
| **Todo Tool** | Medium | Low | Task state tracking. Helps manage multi-step work. |
| **@file Auto-Read** | Medium | Low | Input preprocessing. Simple regex + file read. |
| **TUI Enhancements** | Medium | Medium | Powerline footer (model, git, tokens), persistent history (SQLite/file), themes. |
| **Web Search & Fetch** | Medium | Medium | HTTP client + HTML-to-markdown. Go has good HTTP/scraping libs. |
| **Background Jobs** | Medium | Medium | Goroutines + channels. Natural fit for Go's concurrency model. |

### Tier 3 — Specialized, Higher Effort
Valuable but more niche or complex:

| Feature | Value | Effort | Rationale |
|---------|-------|--------|-----------|
| **Hashline Edits** | Medium | High | Requires custom diff algorithm, model prompt engineering. Big improvement for edit reliability. |
| **Custom Slash Commands** | Medium | Medium | Plugin system via Go plugins or subprocess. |
| **Code Review** | Medium | Medium | Tool + prompt engineering. Structured findings report. |
| **Multi-Credential** | Low | Low | Round-robin key selection. Useful for teams. |
| **TTSR** | Low | Medium | Pattern-matching rule injection. Niche but clever. |
| **SSH Tool** | Low | High | Remote execution adds complexity. Go has `x/crypto/ssh`. |
| **Browser Tool** | Low | High | Puppeteer equivalent in Go (chromedp). Heavy dependency. |
| **Image Generation** | Low | Medium | Provider-specific, limited terminal display. |
| **AST Tools** | Medium | High | tree-sitter bindings for Go exist but are immature. |
| **Python/IPython** | Low | High | Running external kernel from Go. Limited use case overlap. |

---

## Architecture Compatibility Notes

### What maps well to Go/ADK:
- **Model Roles** — config + provider routing, straightforward
- **Subagents** — ADK Go has multi-agent support via `agent.Agent` composition
- **Background Jobs** — goroutines + channels are ideal
- **Git Tools** — shell exec or go-git library
- **LSP** — JSON-RPC over stdio, Go has good JSON handling
- **Todo/Ask** — simple tool implementations
- **Web Fetch** — net/http + html-to-markdown libs

### What doesn't map directly:
- **Hashline Edits** — model-specific prompt engineering, needs custom diff logic
- **Native Rust Engine** — Go stdlib covers grep/glob/shell natively; no FFI needed
- **Browser Stealth** — chromedp exists but stealth plugins need custom work
- **Python Kernel** — cross-language IPC, better served by MCP
- **TTSR** — clever but tightly coupled to streaming implementation

---

## Recommended Implementation Order

```mermaid
graph TD
    A[Model Roles] --> B[Ask + Todo Tools]
    B --> C[AI Git Commits]
    C --> D[TUI Enhancements]
    D --> E[Web Search/Fetch]
    A --> F[Subagent System]
    F --> G[Background Jobs]
    G --> H[LSP Integration]
    E --> I[@file Auto-Read]
    H --> J[Code Review]
    J --> K[Hashline Edits]
```

**Phase 1** (Foundation): Model Roles → Ask/Todo Tools → @file Auto-Read
**Phase 2** (Core Power): AI Git Commits → Subagent System → Background Jobs
**Phase 3** (Code Intelligence): LSP Integration → Code Review → TUI Enhancements
**Phase 4** (Advanced): Web Fetch → Hashline Edits → Custom Commands

---

## Key Metrics

| Category | pi-go | oh-my-pi | Gap |
|----------|-------|----------|-----|
| Core tools | 8 | 20+ | 12+ tools |
| LLM providers | 3+Ollama | 5+ (incl. Cursor) | 2+ |
| Slash commands | 8 | 15+ | 7+ |
| LSP languages | 0 | 40+ | 40+ |
| Subagent types | 0 | 6 | 6 |
| Themes | 1 | 65+ | 64+ |
| Config sources | 2 | 8+ | 6+ |
| Lines of code | ~10K | ~30K+ (TS+Rust) | — |
