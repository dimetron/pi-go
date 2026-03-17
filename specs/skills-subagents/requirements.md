# Requirements — Skills Subagents

## Q&A Record

### Q1: Should the existing 6 hardcoded agent types (explore, plan, designer, reviewer, task, quick_task) be converted to bundled markdown files, or kept as Go code alongside the new discoverable agents?

Converting them means a single unified system. Keeping them means two parallel systems but zero risk of breaking existing behavior.

**A1:** Convert to bundled markdown files. Single unified system.

### Q2: Agent discovery locations — should we follow pi-superpowers-plus conventions or align with pi-go's existing skill discovery paths?

Current pi-go skills load from: `~/.pi-go/skills/`, `.pi-go/skills/`, `.claude/skills/`, `.cursor/skills/`
Pi-superpowers-plus agents load from: `~/.pi/agent/agents/`, `.pi/agents/`

Options:
- **A)** `~/.pi-go/agents/` (user) + `.pi-go/agents/` (project) — consistent with pi-go skill conventions
- **B)** Follow pi-superpowers-plus paths
- **C)** Other

**A2:** Option A — `~/.pi-go/agents/` (user) + `.pi-go/agents/` (project), consistent with pi-go conventions.

### Q3: Should the new tool replace the existing `agent` tool or be a separate `subagent` tool?

- **A)** Replace `agent` tool — extend its schema to support single/parallel/chain modes (backward compatible: existing `{type, prompt}` becomes single mode)
- **B)** New `subagent` tool alongside `agent` — new tool uses `{agent, task}` naming from pi-superpowers-plus, old tool stays unchanged
- **C)** Replace `agent` tool and rename to `subagent`

**A3:** Option C — Replace and rename to `subagent`.

### Q4: For the bundled agents, which set should we ship?

Pi-superpowers-plus ships: implementer, worker, code-reviewer, spec-reviewer
Pi-go currently has: explore, plan, designer, reviewer, task, quick_task

Options:
- **A)** Keep pi-go's existing 6 as bundled markdown, add pi-superpowers-plus's 4 (total: 10)
- **B)** Merge — keep pi-go's 6, replace `reviewer` with the split `code-reviewer` + `spec-reviewer` (total: 8)
- **C)** Start fresh with pi-superpowers-plus's 4 only
- **D)** Other

**A4:** Option B — Keep pi-go's 6, replace `reviewer` with `code-reviewer` + `spec-reviewer` (total: 8 bundled agents).

### Q5: For the TUI pipeline visualization, when should it appear?

- **A)** Only for parallel/chain modes — single mode keeps current event stream rendering
- **B)** All modes get the box visualization (single = one box, parallel = side-by-side, chain = arrow flow)
- **C)** Box visualization is optional — add a `/agents` view or toggle, keep default rendering simple

**A5:** Option A — Pipeline visualization only for parallel/chain. Single mode keeps current event stream.

### Q6: Timeouts — should we implement both inactivity and absolute timeouts like pi-superpowers-plus?

- **A)** Yes, both: inactivity (120s default) + absolute (10min default), configurable via env vars
- **B)** Just absolute timeout (simpler, covers the main risk)
- **C)** No timeouts for now, add later

**A6:** Option A — Both timeouts: inactivity (120s) + absolute (10min), configurable via env vars.

### Q7: Environment filtering for subagent processes — should we implement a strict allowlist like pi-superpowers-plus (only pass PATH, HOME, SHELL, API keys, etc.) or inherit the full parent environment?

- **A)** Strict allowlist — explicit prefixes (PI_, NODE_, etc.) + explicit vars (PATH, HOME, SHELL, etc.) + configurable passthrough via env var
- **B)** Inherit full env but strip sensitive vars (simpler, less isolation)
- **C)** Inherit full env (current behavior, no change)

**A7:** Option A — Strict allowlist with configurable passthrough.

### Q8: Should the `subagent` tool support an `agentScope` parameter like pi-superpowers-plus, allowing the LLM to choose between user/project/both agent sources at call time?

- **A)** Yes — `agentScope` param with "user" (default), "project", "both"; project agents require user confirmation prompt
- **B)** No — always load from both, project agents auto-confirmed (simpler, trust the repo)
- **C)** Config-level setting only, not per-call

**A8:** Option A — Per-call `agentScope` with project agent confirmation prompt.

### Q9: Should the `/agents` slash command (currently lists running agents) be enhanced to also list available agent definitions?

- **A)** Yes — `/agents` shows both available definitions and running agents
- **B)** Split: `/agents` lists definitions, `/agents status` shows running
- **C)** Keep `/agents` as-is (running only), add `/agents list` for definitions

**A9:** Option B — `/agents` lists definitions, `/agents status` shows running.

### Q10: Priority for implementation phases — what's most important to deliver first?

- **A)** Agent discovery + single mode subagent tool (foundation first, parallel/chain/TUI later)
- **B)** All three modes (single/parallel/chain) first, TUI visualization later
- **C)** Full feature set including TUI pipeline visualization in one shot

**A10:** Option C — Full feature set in one shot.

### Q11: Should bundled agent markdown files be embedded in the Go binary (via `go:embed`) or shipped as external files alongside the binary?

- **A)** Embedded via `go:embed` — single binary, no file dependencies, bundled agents always available
- **B)** External files in a known directory (e.g., installed to `~/.pi-go/agents/bundled/`) — easier to inspect/modify
- **C)** Both — embedded as defaults, but allow overriding from filesystem

**A11:** Option C — Embedded defaults via `go:embed`, overridable from filesystem. Priority: project > user > bundled.

### Q12: Should the agent markdown format support a `timeout` field for per-agent absolute timeout override (like pi-superpowers-plus)?

- **A)** Yes — optional `timeout` in frontmatter (e.g., `timeout: 300000` ms), falls back to env var, then default 10min
- **B)** No — global timeout only, configured via env var

**A12:** Option A — Per-agent `timeout` in frontmatter, fallback chain: agent → env var → default 10min.

### Q13: Should the agent markdown format support a `worktree` field to control git worktree isolation per agent?

Current pi-go types have this per-type (e.g., designer=true, explore=false). Pi-superpowers-plus doesn't have worktree support.

- **A)** Yes — optional `worktree: true/false` in frontmatter, default false
- **B)** No — worktree controlled only at call time via tool parameter

**A13:** Option A — `worktree: true/false` in frontmatter, default false.

### Q14: For the `{previous}` placeholder in chain mode — should we also support structured output (JSON) or just plain text piping between chain steps?

- **A)** Plain text only — `{previous}` replaced with the previous agent's final text output
- **B)** Both — `{previous}` for text, `{previous_json}` for structured output when available

**A14:** Option B — Both `{previous}` (text) and `{previous_json}` (structured).

### Q15: Concurrency limit — should we increase the default pool size from 5 to match pi-superpowers-plus's 6, and make it configurable via env var?

- **A)** Yes — default 6, configurable via `PI_SUBAGENT_CONCURRENCY` env var
- **B)** Keep at 5, but add the env var configurability

**A15:** Option A — Default 6, configurable via `PI_SUBAGENT_CONCURRENCY`.

---
*Requirements clarification complete. Proceeding to design.*

