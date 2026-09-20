# Feature comparison: pi-go vs. DevoxxGenie

A side-by-side comparison of pi-go against the DevoxxGenie IntelliJ plugin
(v1.14.2, `tmp/harness/DevoxxGenieIDEAPlugin`), producing a ranked list of
features to consider adopting.

The two are different species, and most of the document follows from that:

- **DevoxxGenie** is an IDE-native assistant — Java, IntelliJ Platform,
  Langchain4j, GUI-centric. Its strength is keeping a human in the loop and
  putting the model's work into editor-native surfaces.
- **pi-go** is a terminal/agent harness — Go, ADK, tool-driven. Its strength is
  the agent runtime: sandboxing, session persistence, compaction, subagents.

Neither dominates. pi-go is well ahead on agent runtime; DevoxxGenie is ahead on
**human control of the agent** and **editor-native surfaces**. Three of the five
recommendations below are gaps in pi-go's safety and visibility, not missing
features.

---

## Method and verification

Source-level comparison of both repositories, read with `rg` and targeted reads.
No code was modified in either repo.

Findings were gathered by six parallel codebase explorations (three per repo),
then the load-bearing claims were re-verified directly against the source. The
directly re-checked claims are marked **[verified]** below with the command or
read that confirmed them. Claims that come from a source read but were not
independently re-checked carry a `file:line` citation so they can be checked in
one step. Nothing here is inferred from documentation alone — where docs and
code disagreed, both are recorded (see [Doc/code drift](#doccode-drift)).

### Verification of the load-bearing claims

| Claim | How verified |
|---|---|
| pi-go has no tool-call approval, permission, or command blacklist | [verified] `rg -i 'approval\|permission\|dangerous\|blacklist\|denylist' internal/` returns only OAuth token-exchange code, kagent protobuf enums, and SOP prose — no gate on a tool call |
| pi-go has no max-turns / max-tool-calls cap | [verified] `rg 'MaxIterations\|maxTurns\|maxToolCalls' internal/` returns only mermaid pathfinder and an OpenAI response-field string |
| pi-go's stuck detector is TUI-only | [verified] `rg -l 'stuckDetector\|maxRepeatToolCalls' internal/` → only `internal/tui/agent_loop.go` and its tests |
| pi-go hooks cannot block | [verified] `internal/extension/hooks.go:148-157` returns `nil, nil` unconditionally; comment reads "Non-fatal: log and continue" |
| Memory Palace is absent from the TUI and ACP server | [verified] `rg -c 'palace' internal/cli/interactive.go` → 0; `internal/acp/server/runtime.go` → 0. `WakeUp` called only at `internal/cli/cli.go:1316` |
| `CostFor` has one non-test caller | [verified] `rg 'CostFor' internal/ --glob '!*_test.go'` → only `internal/cli/model.go:318` |
| Subagent `tools:` frontmatter is parsed but not enforced | [verified] `AgentConfig.Tools` set at `internal/subagent/agents.go:36,144`; `internal/subagent/spawner.go:124-128` passes only `--system` and `--lsp` |
| DevoxxGenie has an approval provider with a read-only allow-list | [verified] `AgentApprovalProvider.java:36-44` (22 tools), decision at `:124-136` |
| DevoxxGenie has a user-editable command blacklist with 5 defaults | [verified] `Constant.java:129-135` |
| pi-go's full static tool inventory (41 tools) | [verified] `eval.Inventory()` executed directly to dump group→name pairs |

---

## What pi-go already does better

Adopt nothing here; these are areas where pi-go should not regress toward the
plugin's design.

| pi-go | DevoxxGenie |
|---|---|
| **Sandbox on every fs op** — `os.Root` opened on the working directory, `..` rejection in `resolveToRoot` (`internal/tools/sandbox.go:59-99, 206-273`) | Path checks only: `..` rejection plus `VfsUtilCore.isAncestor` (`WriteFileToolExecutor.java:42,70`) |
| **Loop protection**: repeat-call, error-streak, cycle, and output-degeneration detection (`internal/tui/agent_loop.go:151-608`) | Tool-call count only |
| **Auto-compaction**: two-stage shed-then-summarize with a clean-boundary split (`internal/session/compaction.go`, `autocompact.go`, `compaction_shed.go`) | **No compaction exists** [verified: `rg -i 'compact\|summariz'` over `service/` finds only unrelated log-summarising helpers] |
| **Session persistence**: append-only JSONL, branching, resume, archive-on-delete, `session/list` over ACP | Active chat memory is in-memory only (`ChatMemoryService` — `ConcurrentHashMap`), lost on IDE restart |
| **Extensions**: hooks, skills, plugin marketplaces, MCP + OAuth, A2A/kagent, worktree subagents with per-agent role and LSP surface | — |
| **Context breakdown** attributing the window to system prompt / tool defs / rules / skills / MCP / conversation (`internal/tui/context_breakdown.go`) | Simpler percentage bar |
| **Provider breadth**: 10 providers incl. `agentgateway`, `opencode`, xAI server-side tools, Gemini grounding | 20+ but largely OpenAI-compatible gateways |

Two structural advantages worth naming explicitly, because they are the reason
several plugin features should *not* be ported:

1. **pi-go is model-agnostic in a way an IDE plugin cannot be.** DevoxxGenie's
   ACP and CLI runners bypass Langchain4j entirely and return `null` chat models
   (`PromptExecutionStrategyFactory.java:29-40`) — a workaround for being inside
   an IDE. pi-go's ACP client is a first-class subagent transport.
2. **pi-go's memory is a queryable store, not prompt stuffing.** The palace has
   FTS5 + semantic hybrid search, a bitemporal knowledge graph, and a
   conversation miner (`internal/palace/`). DevoxxGenie's equivalent is passive
   context injection.

---

## What DevoxxGenie does better

| DevoxxGenie | pi-go |
|---|---|
| **Approval gate**: 22 tools in `READ_ONLY_TOOLS` auto-approvable; everything else prompts (`AgentApprovalProvider.java:36-44, 124-136`) | **No approval, no confirmation, no policy, no config key** [verified] |
| **Command blacklist**: token-position independent, catches compound commands (`cd x && git reset --hard`), flag-combining aware (`-rf` matches `-fr`) (`CommandBlacklist.java:59-129`); user-editable, ASK or BLOCK | Only a name-confusion guard on `bash_wait(`/`bash_kill(` (`internal/tools/bash.go:219-242`) |
| **Approval ergonomics**: 120 s timeout → deny (`AgentApprovalService.java:54`), headless auto-approve, "don't ask again" suppressed for forced dialogs | ACP auto-approves everything, *including explicit deny options* (`internal/acp/permissions.go:19-58`) |
| **Deterministic budget**: `AGENT_MAX_TOOL_CALLS=50` + 300 s wall clock, returning a *soft* error so the model wraps up gracefully (`AgentLoopTracker.java:129-135`) | No cap, no `MaxIterations` (`internal/agent/agent.go:470-480`) |
| **Real diff surfaces**: IntelliJ diff viewer in the write-approval dialog (`AgentDiffPreviewFactory`) and a post-run changed-files list with `+N/-M` per file (`AgentFileChangeTracker`) | ACP never emits `ToolCallContentDiff`; TUI shows aggregate counts only |
| **RAG over the project**, with auto-reindex file watcher and incremental manifest (`ProjectIndexerService`, `RAGFileWatcher.java`) | Palace RAG exists but never reaches the TUI or ACP server [verified] |
| **Inline code completion (FIM)** via Ollama/LM Studio, with debounce, LRU cache and suffix-overlap trimming (`CompletionPostProcessor`) | None anywhere, including the VS Code extension |
| **User-editable tool descriptions** for 20 built-in tools (`BuiltInToolDescriptions`) | Tool descriptions are compiled-in string literals |
| **Spec runner with dependency ordering**: Kahn topological layers, cycle detection, sequential or per-layer parallel (`TaskDependencySorter.java`) | SOP/PDD plans with stage cycles, but no dependency graph |
| **Cost/token display per exchange** and a context-window warning in plain language | No cost surfaced anywhere [verified] |

---

## Bugs and defects found along the way

These are not feature gaps — they are things that look wrong in the code as it
stands. Four in pi-go, then a set of doc/code disagreements in the plugin.

### pi-go

**1. The Memory Palace is unreachable from the primary interface.** [verified]

`palace.PalaceTools` is registered only in the non-interactive CLI
(`internal/cli/cli.go:1304`) and the `pi memory wake-up` command
(`internal/cli/memory_wakeup.go:51`). `internal/cli/interactive.go` contains
zero references to the package, and `internal/acp/server/runtime.go` the same.
`internal/tui/memory.go` polls the palace database every 30 s for a read-only
sidebar status — it reads `palace.db`, it never queries it.

So a headline feature — semantic memory over the project, with a knowledge
graph — is invisible to the TUI, which is the primary way pi-go is used, and to
every editor that talks to it over ACP. `ROADMAP.md` does not list this.

**2. Subagent `tools:` frontmatter is parsed and silently ignored.** [verified]

`AgentConfig.Tools` is populated from frontmatter (`internal/subagent/agents.go:144`)
and documented as "Allowed tool names (empty = all tools)" (`agents.go:36`), but
no code consumes it: `spawner.go:124-128` forwards only `--system` and `--lsp`
to the child process. The same is true of the `tools` field in skill frontmatter
(`internal/extension/skills.go`).

A restriction that silently does not restrict is worse than a field that does not
exist, because it invites a security assumption. Either enforce it or remove it.

**3. The `grep` tool's name is not stable across builds.** [verified]

`newGrepTool` picks its own name at construction (`internal/tools/grep.go:131-134`):

```go
grepToolName := "grep"
if rgAvailable {
    grepToolName = "ripgrep"
}
```

Everything that names a tool by string — subagent allow-lists, hook `tools:`
filters, the README, user muscle memory — is therefore host-dependent. This is
also why recommendation #1 below cannot simply be "ship a deny-list keyed on
tool name".

**4. Cost data is fully implemented and never shown.** [verified]

`internal/provider/pricing.go:127` implements `CostFor` against a bundled
models.dev snapshot, and `internal/cli/model.go:318` is its **only** non-test
caller — it prints a price table. Meanwhile `guardrail.Tracker` already tracks
per-session and per-day tokens and persists them to `~/.pi-go/usage.json`, and
`pirpc` hardcodes `"cost": 0` (`internal/rpc.go:460`). `eval.TokenMetrics.CostUSD`
and ATIF `Metrics.CostUSD` are declared but never populated.

### DevoxxGenie (doc/code drift)

Recorded for completeness; not actionable for pi-go, but they mean the plugin's
documentation is not a reliable design source.

| Claim in docs | Reality in code |
|---|---|
| "17 built-in backlog tools" | 20 declared and registered (10 task + 5 document + 5 milestone) |
| Security binaries are not bundled or auto-downloaded | `SecurityBinaryManager.resolveBinary` has a bundled-classpath fallback (step 2) |
| Gitleaks scans "source code and git history" | Runs `--no-git` — working tree only |
| `ExternalPromptService.setPromptText` "populates the input and submits it" | Sets text and requests focus; does not submit |
| Docs advertise parallel sub-agents with `fetch_page` | `ReadOnlyToolProvider` registers exactly `read_file`, `list_files`, `search_files` |
| Chat memory default is 10 messages | Default is 50 (`MAX_MEMORY`); 10 is only the fallback |
| "No timeout" on the spec task runner | Correct — `specTaskRunnerTimeoutMinutes` is read only by the settings UI and never by the runner |

Also worth noting for anyone reading the plugin as a reference: its ACP path
auto-approves **every** agent-initiated `session/request_permission`
(`AgentRequestHandler.java:117-120`), and never injects the Backlog MCP server
from the chat path (`AcpClient.java:240-241` passes `includeBacklogMcp=false`).
The plugin's in-IDE agent is carefully gated; its ACP runners are not.

---

## Recommendations, ranked

### 1. Let `before_tool` hooks deny a tool call — *recommended first move*

The cheapest high-value change in the repository. The ADK `BeforeToolCallback`
already returns `(map[string]any, error)`, and the hook chain is wired into every
entry point — TUI (`internal/cli/interactive.go:572-574`), print/json
(`cli.go:789-791`), and ACP (`acp/server/runtime.go:478-480`). Today the hook's
outcome is discarded:

```go
// internal/extension/hooks.go:151-156
if err := runHookCommand(ctx, hook, t.Name(), args); err != nil {
    hookLogf("hook %q failed for tool %q: %v", hook.Command, t.Name(), err)
    // Non-fatal: log and continue.
}
return nil, nil
```

Define a blocking exit code and return an error from the callback when a
`before_tool` hook uses it. That gives users a policy layer **without pi-go
having to ship an opinionated blacklist**, and it composes with the existing
(but currently inert) `tools:` allow-list and hook `tools:` filter.

Then add a small built-in matcher for the obvious destructive cases, borrowing
DevoxxGenie's *approach* rather than its code: scan every token position as a
possible pattern start (so `cd x && git reset --hard` matches), skip only
`-`-prefixed tokens between pattern tokens, and treat short flags as a set so
`-rf` also matches `-fr` and `-rfv`.

Note the trap in pi-go bug #3: a deny-list keyed on tool name cannot assume the
tool is called `grep`. Key it on `bash` command text, which is what actually
matters for `rm -rf` and `git push --force`.

- **Effort:** small — one exit-code convention plus error propagation; the
  matcher is a self-contained function with table-driven tests.
- **Impact:** closes the largest safety gap in the repo. pi-go currently has no
  way for a user to say "never run that".

### 2. Add a per-turn budget in `internal/agent`

`stuckDetector` lives only in `internal/tui/agent_loop.go`. Print, JSON, socket
and ACP modes therefore have **no loop protection at all**: a turn with
non-repeating work runs unbounded, because the agent is built with no
`MaxIterations` (`internal/agent/agent.go:470-480`; verified against the ADK
source — `MaxIterations` exists only on `LoopAgent`, which pi-go does not use).

The plugin's design is worth copying exactly: 50 tool calls plus a 300 s wall
clock, and on exhaustion a **soft error string** rather than an exception, so the
model wraps up with what it has:

> "Error: Agent loop limit reached (N tool calls). Provide your best answer…"
> — `AgentLoopTracker.java:129-135`

A budget that degrades into a good answer beats one that aborts the turn. Put it
in `internal/agent` (not the TUI) so every entry point inherits it, and move the
stuck detector out of `internal/tui` in the same change.

- **Effort:** small-to-medium — new counter in the agent, config plumbing,
  plus relocating the stuck detector.
- **Impact:** prevents runaway runs in headless and CI use, where nothing
  currently stops them.

### 3. Accumulate and display session cost

pi-go has the expensive part done: a models.dev pricing snapshot and
`provider.CostFor`. The gap is purely plumbing and presentation —
`internal/cli/model.go:318` is the only caller, and `pirpc` hardcodes
`"cost": 0`.

`guardrail.Tracker` already tracks per-session and per-day tokens and persists to
`~/.pi-go/usage.json`. Accumulating USD alongside them and surfacing it in the
status bar plus a `/cost` command is small and self-contained. The plugin's
per-exchange display (`ChatMessageContext.setTokenUsageAndCost`) is the model.

Two bonuses fall out for free: the daily-token guardrail becomes legible in
dollars, and the already-declared-but-unpopulated `eval.TokenMetrics.CostUSD` /
ATIF `Metrics.CostUSD` fields can finally be filled.

- **Effort:** small.
- **Impact:** immediate, and it makes an existing budget feature understandable.

### 4. Emit real diffs over ACP

The SDK type exists (`acp.NewToolCallContentDiff`) and Zed, JetBrains and the VS
Code extension all render it — but pi-go only ever emits text content
(`internal/acp/adapter/toolcall.go:350-353`). The VS Code webview compensates by
reimplementing LCS line diffing client-side (`vscode/src/shared/diff.ts`), which
is duplicated work that the other two editors cannot reuse.

Emitting `ToolCallContentDiff{Path, OldText, NewText}` for `write` and `edit`
calls gets editor-native diff review across all three editors from one change.
The read-before-overwrite ledger (`internal/tools/ledger.go`) already holds the
prior content needed for the "before" side.

- **Effort:** small.
- **Impact:** disproportionate — one change, three editors, and it deletes
  duplicated logic in the VS Code extension.

### 5. Wire the Palace into the TUI and ACP server

Not a new feature — a wiring fix for pi-go bug #1. Follow the non-interactive
path (`internal/cli/cli.go:1263-1319`): register `PalaceTools`, call `WakeUp` for
L0+L1 context, and gate tool advertisement on `DrawerCount > 0`, which is the
existing token-cost gate (the 11 palace tool declarations cost ~1.6 k tokens per
request).

Worth doing at the same time, and related: L0 identity and L2 recall are
implemented but unwired in production — `WithIdentityFile` has no caller outside
tests, and `Palace.Recall` is called by nothing. Either wire them or note them as
intentionally dormant.

- **Effort:** small.
- **Impact:** a shipped headline feature becomes reachable from the interface
  most users actually use.

### 6. Bounded repo map in the prompt — *judgment call*

The plugin's "add whole project" pipeline is the closest analogue: scan to a
directory tree plus file contents, truncate at `getInputMaxTokens()` with an
explicit `--- Project context truncated ---` marker, wrap in `<Context>`. pi-go
has **nothing equivalent** — no symbol index, no repo map. Nearest are the `tree`
tool (500-entry cap), `lsp-workspace-symbol`, and the async palace miner, which
is an offline RAG index rather than in-prompt context.

I rank this **below** the five above, and lower than its surface appeal suggests,
because pi-go's design is read-on-demand and a large prompt blob fights the
context-breakdown tooling that makes the window legible. If it is built, derive
it from `tree` plus LSP symbols into a **bounded** prompt section — not file
contents, and not a truncation-marker blob.

- **Effort:** medium.
- **Impact:** uncertain; helps navigation on unfamiliar repos, hurts the context
  budget and the "dumb zone" accounting.

### Lower priority, or actively skip

| Feature | Verdict |
|---|---|
| **Inline code completion (FIM)** | Real gap — the plugin supports it via Ollama/LM Studio with debounce, LRU cache and suffix-overlap trimming. But it is IDE-shaped: it needs `InlineCompletionItemProvider` in the VS Code extension or a new JetBrains plugin. Large lift, off-thesis for a CLI harness. |
| **Security scanners as tools** (gitleaks / opengrep / trivy → auto-filed Backlog tasks) | Interesting, and pi-go's `govulncheck` and `grype` skills cover part of it. Better as a bundled skill than three new tools, matching pi-go's own design. |
| **Personas** (named system prompts, per-tab) | Already covered by pi-go's skills and subagent role definitions. |
| **Kanban / spec browser** | Already on `ROADMAP.md` ("Kanban board for agent tasks"). |
| **Spec runner dependency ordering** | Genuinely useful and genuinely missing, but pi-go's SOP/PDD path is a different execution model. Defer until a task list exists to order. |
| **User-editable tool descriptions** | Cheap and nice; low impact until tool descriptions become the thing users complain about. |
| **Chat memory across restarts** | The plugin does not have it either. pi-go's session store is already better. |

---

## Appendix

### pi-go static tool inventory (41 tools)

Produced by executing `eval.Inventory()` (`internal/eval/inventory.go:46-125`),
which the codebase describes as the source of truth for which tools exist.
Runtime tools not listed — MCP server tools, and skill-dispatched commands.

```
├── 🤖 core (16)
│   ├── read, read_image, write, edit, bash, tree, ls
│   ├── bash_wait, bash_kill            (bash-control group)
│   ├── ripgrep | grep                  (name chosen at build time)
│   ├── find, git-overview, git-file-diff, git-hunk
│   └── session-stats, web_search
├── 🧠 memory (3)      mem-search, mem-timeline, mem-get
├── 🏛 palace (11)     palace-status, palace-search, palace-add-drawer,
│                      palace-traverse, palace-kg-{add,query,invalidate,
│                      timeline,extract}, palace-diary-{write,read}
├── 🔍 lsp (7)         lsp-symbols, lsp-diagnostics, lsp-definition,
│                      lsp-references, lsp-hover, lsp-workspace-symbol,
│                      lsp-code-action
├── 🌐 llms (1)        fetch_docs
├── 🔗 a2a (1)         a2a
├── 🤝 subagent (1)    subagent
└── ✨ provider (1)    google_search        (Gemini only; PI_NO_GROUNDING)
```

### Limit comparison

Hard-coded on the pi-go side, mostly user-facing settings on the plugin side.

| Limit | pi-go | DevoxxGenie |
|---|---|---|
| Tool calls per turn | **none** | 50 default, UI 1–500 |
| Wall clock per turn | **none** | 300 s |
| Loop protection | heuristic, TUI only | soft count limit, all modes |
| Approval timeout | **n/a — no approvals** | 120 s → deny (MCP 60 s) |
| Tool output cap | 256 KB, 500 chars/line (`tools/truncate.go:6-7`) | 10 000 chars for `run_command` |
| `bash` timeout | 60 s default, 600 s max, idle 90 s; **backgrounded on expiry, not killed** | 30 s, killed |
| Read cap | 2000 lines, 256 KB, 2000 chars/line | — |
| Subagent concurrency | pool 3 default (≤64), 8 tasks per call, depth-halving | fixed thread pool 5, ≤10 configured |
| Subagent timeout | 20 m absolute / 5 m inactivity | 120 s default, UI 10–600 |
| Session retention | none; archive-on-delete; 20-session in-memory cache | conversation DB pruned at 50 MB, 10 at a time |
| Diff snapshot cache | n/a | 20 runs, 1 MiB per file |

### Source layout for the comparison

```
DevoxxGenieIDEAPlugin/                      pi-go/
├── src/main/java/com/devoxx/genie/         ├── internal/
│   ├── service/agent/    approval, loop,   │   ├── agent/       runtime, instructions
│   │                     file-change       │   ├── tools/       sandbox, ledger, registry
│   │                     tracker, blacklist│   ├── session/     JSONL, compaction, branch
│   ├── service/rag/      Chroma + Ollama   │   ├── palace/      memory, KG, miners
│   ├── service/spec/     Backlog.md,       │   ├── subagent/    pool, worktrees, ACP spawn
│   │                     topological runner│   ├── extension/   hooks, skills, MCP
│   ├── service/acp/      hand-rolled       │   ├── acp/         server + client
│   │                     JSON-RPC client   │   └── eval/        inventory, metrics
│   └── service/security/ gitleaks etc.     └── vscode/          extension (ACP client)
```

---

## Summary of recommended actions

| # | Action | Effort | Impact |
|---|---|---|---|
| 1 | `before_tool` hook denial + destructive-command matcher | small | **highest** — closes the largest safety gap |
| 2 | Per-turn tool/turn budget in `internal/agent`, move stuck detector out of TUI | small–med | prevents unbounded runs in headless/CI |
| 3 | Session cost accumulation + `/cost` + status bar | small | makes existing guardrail legible |
| 4 | `ToolCallContentDiff` over ACP | small | three editors, and deletes duplicated logic |
| 5 | Wire Palace tools + `WakeUp` into TUI and ACP server | small | fixes an unreachable headline feature |
| 6 | Bounded repo map | medium | judgment call — may fight context tooling |

Bugs to fix regardless of feature work: palace unreachable from the TUI (#1
above), subagent/skill `tools:` frontmatter silently unenforced, `grep` tool name
unstable across hosts, cost data unimplemented at the display layer.
