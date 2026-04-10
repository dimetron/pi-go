# Requirements — Enhance pi-go from oh-my-pi

## Q&A Record

### Q1: Feature Scope
**Q:** The gap analysis identified 4 tiers of features. Which tier(s) do you want to target for this enhancement round? Options:

- **A) Tier 1 only** — Model Roles, AI Git Commits, Subagents, LSP (high value, higher effort)
- **B) Tier 1 + Tier 2** — Add Ask/Todo tools, @file auto-read, TUI enhancements, web fetch, background jobs
- **C) Cherry-pick** — Select specific features across tiers (please list which ones)
- **D) All feasible** — Everything that makes sense for Go/ADK architecture

**A:** A — Tier 1 only: Model Roles, AI Git Commits, Subagents, LSP Integration

### Q2: Model Roles — Routing Strategy
**Q:** oh-my-pi supports 5 model roles: `default`, `smol` (fast/cheap), `slow` (powerful), `plan` (planning), `commit` (git commits). For pi-go, how should role-to-model mapping work?

- **A) Config-based** — User defines role→model mappings in `~/.pi-go/config.json` (e.g., `"roles": {"smol": "gemini-2.0-flash", "slow": "claude-opus-4"}`)
- **B) Convention-based** — Auto-resolve roles from available providers (e.g., smol picks cheapest available model)
- **C) Both** — Config overrides with sensible auto-defaults

**A:** A — Config-based. User defines role→model mappings in config.json.

### Q3: AI Git Commits — Scope
**Q:** oh-my-pi's git commit feature includes: tool-based git inspection, split commits (separating unrelated changes), hunk-level staging, changelog generation, and conventional commit validation. Which capabilities do you want?

- **A) Full** — All of the above (git-overview, git-file-diff, git-hunk tools + split commit logic + changelog + validation)
- **B) Core** — Git inspection tools + conventional commit generation (no split commits or changelog)
- **C) Minimal** — Just a `/commit` slash command that generates a commit message from staged changes

**A3:** B — Core: Git inspection tools (git-overview, git-file-diff, git-hunk) + conventional commit message generation. No split commits or changelog.

### Q4: Subagent System — Architecture
**Q:** oh-my-pi has 6 bundled agents (explore, plan, designer, reviewer, task, quick_task) with worktree isolation and parallel execution. For pi-go's subagent system:

- **A) ADK-native** — Use ADK Go's built-in multi-agent composition (`agent.Agent` with sub-agents). Agents share the same process, communicate via ADK session events.
- **B) Process-based** — Spawn subagents as separate `pi` processes with their own sessions. Communicate via RPC/JSON. Enables worktree isolation naturally.
- **C) Hybrid** — ADK-native for lightweight tasks (explore, quick_task), process-based for isolated work (plan, reviewer with worktree).

**A4:** B — Process-based. Spawn subagents as separate `pi` processes with own sessions. Communicate via RPC/JSON. Enables worktree isolation.

### Q5: Subagent Types — Which Agents?
**Q:** oh-my-pi bundles 6 agent types. Which ones should pi-go support initially?

- **explore** — Search/read codebase to answer questions
- **plan** — Create implementation plans from requirements
- **designer** — UI/UX design suggestions
- **reviewer** — Code review with structured findings
- **task** — General-purpose subtask execution
- **quick_task** — Lightweight one-shot tasks

Pick any combination (e.g., "explore, plan, task") or "all":

**A5:** All 6 agent types: explore, plan, designer, reviewer, task, quick_task.

### Q6: LSP Integration — Scope
**Q:** oh-my-pi has 11 LSP operations and 40+ language configs. For pi-go's initial LSP support:

- **A) Full** — All 11 operations (diagnostics, definition, type_definition, implementation, references, hover, symbols, rename, code_actions, status, reload) + format-on-write + diagnostics-on-edit
- **B) Essential** — diagnostics, definition, references, hover, symbols, format-on-write (6 ops — covers 90% of use cases)
- **C) Minimal** — diagnostics + format-on-write only (validate code after edits)

**A6:** B — Essential: diagnostics, definition, references, hover, symbols, format-on-write (6 operations).

### Q7: LSP Language Support — Initial Languages
**Q:** oh-my-pi ships with 40+ language server configs. Which languages should pi-go support out of the box? (Others can be added via config later.)

- **A) Go-centric** — Go (gopls) only, since pi-go is a Go project
- **B) Core languages** — Go, TypeScript/JavaScript, Python, Rust (~5 language servers)
- **C) Broad** — 10-15 popular languages (Go, TS/JS, Python, Rust, Java, C/C++, Ruby, PHP, etc.)

**A7:** B — Core languages: Go (gopls), TypeScript/JavaScript (typescript-language-server), Python (pyright/pylsp), Rust (rust-analyzer). User-configurable for more.

### Q8: Subagent Concurrency & Isolation
**Q:** For process-based subagents, how should concurrency and isolation work?

- **A) Sequential** — One subagent at a time, simple to reason about
- **B) Parallel with limit** — Up to N concurrent subagents (e.g., 3-5), each in optional git worktree
- **C) Unlimited parallel** — No concurrency limit, user manages resource usage

And for worktree isolation:
- **i) Always** — Every subagent gets a git worktree
- **ii) Optional** — Subagent requester decides; default is shared working directory
- **iii) By type** — Some agent types (task, reviewer) get worktrees; others (explore, quick_task) share cwd

**A8:** B+iii — Parallel with limit (configurable, default ~5 concurrent). Worktree isolation by agent type: task/reviewer/designer get worktrees; explore/plan/quick_task share cwd.

### Q9: Git Tools — Implementation Approach
**Q:** For the git inspection tools (git-overview, git-file-diff, git-hunk), should these be:

- **A) Native tools** — Registered as ADK tools alongside read/write/edit, callable by the LLM directly
- **B) Slash command** — A `/commit` command that orchestrates git inspection internally, presents results to user
- **C) Both** — Tools available to the LLM for general git awareness + `/commit` command for the commit workflow

**A9:** C — Both. Git tools (git-overview, git-file-diff, git-hunk) registered as ADK tools for LLM use + `/commit` slash command for interactive commit workflow.

### Q10: Model Roles — Which Roles?
**Q:** oh-my-pi defines 5 roles: default, smol, slow, plan, commit. For pi-go:

- **A) Same 5** — default, smol, slow, plan, commit
- **B) Extended** — Add more roles (e.g., review, explore, design) to match subagent types
- **C) Flexible** — Arbitrary user-defined role names with no fixed set

**A10:** A — Same 5 roles: default, smol, slow, plan, commit.

### Q11: LSP Lifecycle
**Q:** How should language servers be managed?

- **A) On-demand** — Start the language server when first needed for a file type, shut down after idle timeout
- **B) Eager** — Start all configured language servers on agent startup, keep alive for session duration
- **C) On-demand + persistent** — Start on first use, keep alive for session (no idle shutdown)

**A11:** C — On-demand + persistent. Start language server on first use for that file type, keep alive for entire session.

### Q12: LSP as Tools or Hooks?
**Q:** How should LSP integrate with the agent workflow?

- **A) Tools** — LSP operations (diagnostics, definition, etc.) exposed as ADK tools the LLM can call explicitly
- **B) Hooks** — LSP runs automatically: format-on-write after write/edit tools, diagnostics injected into tool results
- **C) Both** — Automatic format-on-write + diagnostics-on-edit via hooks, plus explicit LSP tools for definition/references/hover/symbols when the LLM needs them

**A12:** C — Both. Auto format-on-write + diagnostics-on-edit via hooks. Explicit LSP tools (definition, references, hover, symbols) for LLM to call when needed.

### Q13: Subagent Model Assignment
**Q:** When spawning a subagent, which model should it use?

- **A) Inherit** — Subagent uses the parent's current model
- **B) Role-based** — Each agent type maps to a model role (e.g., explore→smol, plan→slow, reviewer→default)
- **C) Explicit** — Parent specifies model per spawn, with role-based defaults as fallback

**A13:** B — Role-based. Each agent type maps to a model role (e.g., explore→smol, plan→slow, reviewer→default, task→default, designer→slow, quick_task→smol).

### Q14: Subagent Communication
**Q:** How should subagents report results back to the parent agent?

- **A) Return value** — Subagent runs to completion, returns final text result via RPC
- **B) Streaming** — Real-time event streaming from subagent to parent (tool calls, text deltas visible)
- **C) Artifacts** — Subagent writes files/artifacts to disk; parent reads them after completion

**A14:** B — Streaming. Real-time event streaming from subagent to parent via RPC (tool calls, text deltas visible to parent).

### Q15: Backward Compatibility
**Q:** Should these enhancements maintain full backward compatibility with existing pi-go config, sessions, and behavior?

- **A) Strict** — No breaking changes. All new features opt-in. Existing configs, sessions, and tool behavior unchanged.
- **B) Minor breaks OK** — Config schema can evolve (e.g., new required fields with defaults). Existing sessions still loadable.
- **C) Clean slate** — OK to restructure config, session format, etc. if it produces a better design.

**A15:** C — Clean slate. OK to restructure config, session format, etc. for better design.
