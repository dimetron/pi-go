# 05 — Subagents & Multi-Agent

## Side-by-side summary

| | pi-go | pi |
|---|---|---|
| LOC | 3,511 | 1,195 (example extension) |
| Status | **built-in ADK tool** | example extension, symlink install |
| Modes | single / parallel / chain | single / parallel / chain |
| Parallel cap | 8 | 8 |
| Concurrency | 4 | 4 |
| Bundled agents | **18** | 4 samples |
| Discovery tiers | bundled / user / project | user / project |
| Worktree isolation | ✓ full `WorktreeManager` | ✗ |
| `base:` diff trick | ✓ | ✗ |
| Concurrency pool | ✓ process-wide | ✗ per-call only |
| Nesting budget | ✓ halved per depth | ✗ |
| Env allowlist | ✓ 50 entries | ✗ |
| Spawn retry | ✓ 3, classified | ✗ |
| Timeout | ✓ typed error | ✗ |
| External CLI agents | ✓ 7 agents, 3 transports | ✗ |
| Trust gate on project agents | ✗ | ✓ |
| Trajectory linkage | ✓ ATIF ref | ✗ |

## 5.1 The tool contract

Both sides have single / parallel / chain modes with a `{previous}` chain
placeholder and a parallel cap of 8. The differences are in strictness and
reporting.

### pi-go

`internal/tools/subagent.go` (637 LOC) — a **built-in ADK tool**.

**Input** (`SubagentInput` `:30-45`):
- Single: `Agent`, `Task`, and `Base` — the worktree base ref (`:36-40`)
- Parallel: `Tasks []TaskItem` (`{agent, task}`)
- Chain: `Chain []ChainItem` (`{agent, task}`)

**Mode detection** (`detectMode` `:196-210`): chain > parallel > single, in that
priority. Single is deliberately lenient — accepted if *either* `agent` or `task`
is non-empty, *"to help recover from LLM mistakes"*. Zero modes → an error message
listing all three shapes verbatim (`:180-189`).

**Limits**: `maxParallelTasks = 8` (`:25`), `maxChainSteps = 8` (`:28`), enforced
by `checkSubagentPipeline` (`:355-393`) **before anything spawns**.

**Parameter alias repair** (`:102-109`) — LLM-robustness:

```go
map[string]string{
  "type": "agent", "prompt": "task", "message": "task",
  "items": "tasks", "steps": "chain",
}
```

**Parallel** (`:490-522`): one goroutine per task, results into a pre-sized
`results[idx]` slice so order is preserved regardless of completion order.

**Chain** (`:527-566`): strictly sequential, **stops on first non-`completed`
status** (`:553-555`). `expandChainTemplate` (`:569-583`) — `{previous}` raw,
`{previous_json}` escaped for `\`, `"`, `\n`, `\r`, `\t`.

**Single** (`:213-278`): uses a *separate* event drainer `forwardSingleModeEvents`
(`:290-320`) that also handles `run_done` and maps `ev.Status == "timeout"`. The
comment at `:277-289` explains why the paths are not folded: they report
differently.

**Events to the TUI**: `SubagentEvent` (`:78-90`) carries `AgentID, Kind, Content,
PipelineID, Mode, Step, Total`. `Kind` ∈ spawn / text_delta / tool_call /
tool_result / error / done. Every event is stamped with its pipeline position so
the TUI can group parallel/chain output.

**The tool description is generated at runtime** (`buildSubagentDescription`
`:121-170`) and reports **both** numbers (`:155-166`):

> "Report the pool size, not just the per-call cap. They are different numbers
> and the model needs the smaller one: maxParallelTasks limits how many tasks a
> single call may name, while the pool is what actually gates a spawn. Naming
> more tasks than the pool allows does not run them in parallel — they queue, and
> the one tool call takes proportionally longer, which is how a 'parallel' batch
> turns into a timeout."

Plus worktree/`base` guidance (`:133-140`) and a note that
"parallel mode gives no speed-up here" when the pool is ≤1.

### pi — an example extension

`packages/coding-agent/examples/extensions/subagent/` — **not built in**. Requires
manual symlink install (`README:35-55`).

`index.ts` (1,038 LOC), `agents.ts` (157), `README.md` (177), 4 agent `.md`,
3 prompt presets.

**Parameters** (`SubagentParams` `:459-468`, TypeBox): `agent`, `task`, `tasks`,
`chain`, `agentScope` (`"user"|"project"|"both"`), `confirmProjectAgents`
(default true), `cwd`. `TaskItem`/`ChainItem` (`:442-457`) each carry
`{agent, task, cwd?}`.

**Mode detection** (`:492-497`) counts non-empty modes and **requires exactly
one** — `if (modeCount !== 1)` returns a text error listing available agents
(`:508-517`). Stricter than pi-go's lenient single mode; no priority ordering.

**Constants** (`:33-36`): `MAX_PARALLEL_TASKS = 8`, `MAX_CONCURRENCY = 4`,
`COLLAPSED_ITEM_COUNT = 10`, `PER_TASK_OUTPUT_CAP = 50 * 1024`.

**Parallel** (`:645`): `mapWithConcurrencyLimit(params.tasks, MAX_CONCURRENCY, ...)`
— a worker pool with a shared `nextIndex` cursor (`:219-238`), `Promise.all` over
4 workers. **No process-level pool, no nesting budget, no env propagation.**

**Trust gate** (`README:57-65`): project-local agents are repo-controlled
prompts. Default scope is **user-only**; `agentScope: "both"` opts in.
Interactive runs **prompt for confirmation** in untrusted projects (`:519-530`),
gated on `ctx.hasUI && !ctx.isProjectTrusted()`; `confirmProjectAgents: false`
disables.

### pi's other subagent — durable/experimental

`packages/coding-agent/src/experimental/durable/subagent.ts` (55 LOC) is a
**completely different, in-process** model:

```ts
parameters: Type.Object({ task: Type.String(...) })   // one param only
```

Creates a **child conversation** in the durable store owned by the task:
`tx.createConversation({ownership: {kind: "task", taskId: api.taskId}})` (`:39`),
then `configure(tx, created.id, {extensions: {remove: [Subagent]}})` — "without
this extension it cannot delegate further", i.e. **depth 1 by construction**.

`replay: "safe"` (`:35`): a rerun after a crash finds the child it already created
via `tx.scanConversations({ownerTaskId})` (`:37`). "The child outlives the call, so
the user can switch to it and keep talking" (`:23-25`).

### Review — tool contract

The two contracts are nearly identical in shape, which is a good sign that the
design is converged. Four differences matter:

1. **pi-go is lenient, pi is strict.** pi-go accepts single mode if either field is
   present; pi rejects ambiguous input with an error. pi-go's choice is defensible
   — an LLM that names only `task` and forgets `agent` should still get a run
   against the default agent rather than an error. But pi's error message *lists
   the available agents*, which is a better recovery path than pi-go's.
2. **pi-go's alias repair** (`type`→`agent`, `prompt`→`task`, `items`→`tasks`,
   `steps`→`chain`) is a real robustness feature pi lacks.
3. **pi-go's runtime-generated tool description** reporting both the per-call cap
   and the pool size is the best-in-class approach. A model that names 8 tasks
   against a pool of 3 will learn why it took longer than expected.
4. **pi has `PER_TASK_OUTPUT_CAP = 50 KB`**; pi-go has no per-subagent output cap.

**The trust gate is the real gap.** pi-go's `DiscoverAgents`
(`internal/subagent/agents.go:261-307`) merges project agents unconditionally:
bundled → user → project, each pass replacing same-named entries in place.
A hostile repository can ship `.pi-go/agents/planner.md` and pi-go will use it.
pi asks first. See §3.6 — this is the same trust gap as for skills and MCP config,
and the same fix.

**pi's `cwd` per task item** is a small feature pi-go lacks and is worth having:
routing one task in a subdirectory without `cd` in the prompt.

## 5.2 Orchestration & concurrency

### pi-go

`internal/subagent/orchestrator.go` (929 LOC). `Orchestrator` (`:48-77`) composes
`Pool` + `Spawner` + `WorktreeManager` + config + registry + live `agents` map +
`recentTasks` dedup map + ACP JSONL log.

**`DefaultPoolSize = 3`** (`:29`), with the measured justification at `:21-28`:

> "Three rather than five, because the budget is per process and a spawned
> coordinator builds its own pool… Measured over six hours of runs, 39% of sessions
> died on the provider's per-minute token limit at a peak of eight agents in
> flight."

**`ConcurrencyFromEnv()`** (`concurrency.go:26-51`): reads
`PI_SUBAGENT_CONCURRENCY` (`:14`); falls back to `DefaultPoolSize` on
unset/unparseable/non-positive (**each with a `slog.Warn`**), clamps to
`maxConcurrencyBudget = 64` (`:19`). Never returns <1 — *"a zero-sized pool would
block the first Acquire forever, turning a misconfiguration into a hang."*

**`childConcurrency(parent)`** (`concurrency.go:66-71`): `parent < 2 ? 1 : parent/2`.
Halving with a floor makes total concurrency **converge** with nesting depth
instead of growing as depth×budget.

**`ChildEnv(parentBudget)`** (`environ.go:88-107`) takes `FilterEnv(nil)`,
**strips** any inherited `PI_SUBAGENT_CONCURRENCY=`, and appends
`childConcurrency(parent)`. Comment `:91-100`: `PI_` is a forwarded prefix, so
without the rewrite the parent's budget is inherited verbatim and each child gets
a *fresh* allowance — the exact 8-agents-in-flight bug.

**Env allowlist** (`environ.go:12-66`): `HOME/USER/PATH/LANG/LC_*/TERM/TMPDIR/
XDG_/EDITOR`, Go toolchain, `GIT_*` + `SSH_AUTH_SOCK`, `PI_*`, provider API keys
(`ANTHROPIC_*`, `OPENAI_*`, `GEMINI_*`, `GOOGLE_API_KEY`, `AGENTGATEWAY_*`,
`OLLAMA_HOST`, `CODEX_HOME`), `DOCKER_HOST`/`KUBECONFIG`/`NODE_PATH`/`PYTHONPATH`.
Everything else dropped. `matchesAllowlist` `:110` — trailing `_` = prefix,
otherwise exact.

**Pool** (`pool.go`, 48 LOC): buffered-channel semaphore. `Acquire` selects on
`p.sem <- struct{}{}` vs `ctx.Done()` (`:23-30`).

**`Spawn`** (`orchestrator.go:419-513`): shutdown check → `cfg.ResolveRole
(agent.Role)` → `pool.Acquire` → `agentID = "<name>-<uniqueNano>"` →
`resolveWorktreeUsage` → `resolveWorkDir` → `SpawnOpts` → `dispatchSpawn` →
`trackAgent` → `go o.forwardAgentEvents(...)`. Release on every failure path via
`abandonSpawn` (`:590-597`).

**`SpawnWithRetry`** (`:316-358`): up to 3 re-spawns (`clampRetries` `:363-371`).
Distinguishes three outcomes (`:378-393`): `attemptHealthy`,
`attemptCrashed` (terminal event while tracked failed/killed), `attemptSilent`
(stream closed with no terminal event). Only healthy-or-silent-on-last-attempt
returns the stream. `maxRetries == 0` short-circuits `awaitAttemptOutcome`
entirely so the caller gets the stream undrained (`:344-347`).

**Retained state**: `recentTaskTTL = 30 * time.Minute` (`:32`),
`maxCompletedAgents = 50` with oldest-completed eviction (`:37`,
`evictCompletedAgentsLocked` `:683`). `FindRecentTask`/`RecordTask` dedupe by
normalized prompt, pruned by a lazy ticker (`ensurePruneLoop` `:191`).

### pi

`mapWithConcurrencyLimit(…, 4)` per tool call. No process pool, no nesting budget,
no env rewrite.

### Review — orchestration

**pi-go's subagent orchestration is far ahead, and it is the clearest single
example of pi-go holding knowledge pi does not.**

The **halving-per-depth budget** is a genuinely good idea that only arises when
you have measured the failure. `childConcurrency(parent) = parent/2` means a
depth-3 tree with a root budget of 8 has 8 + 4 + 2 + 1 = 15 concurrent agents, not
8³ = 512. Combined with `ChildEnv` stripping the inherited `PI_` var — and the
comment explaining exactly why — this is a fix for a bug that would otherwise
recur in every nested run.

**pi has no equivalent at all.** `MAX_CONCURRENCY = 4` limits *one call*. A child
`pi` that itself delegates can multiply the count without bound. If pi's subagent
extension ever had a workflow prompt that delegated (and `implement.md` does:
scout→planner→worker), the concurrency is 4 at the top and 4 per child, per level,
with no convergence.

**The retry classification is also ahead.** `attemptHealthy` / `attemptCrashed` /
`attemptSilent` is the right decomposition — a silent close (no terminal event) and
a crash (terminal failure event) need different retry policies, and treating them
the same means retrying deterministic failures.

**The dedup of recent tasks** (30-minute TTL, normalized prompt, 50-agent cap) is
a real cost-control feature: it prevents the model from re-running the same task
in a retry loop, which is the most common way a subagent system burns a token
budget.

**What pi-go lacks here**: per-subagent output caps (pi has 50 KB), `cwd` per task,
and any visibility into *why* a spawn failed beyond the status string.

## 5.3 Worktree isolation — pi-go's clearest lead

`internal/subagent/worktree.go` (858 LOC) has no pi equivalent at all.

`WorktreeManager` (`:16-28`): mutex, `active` map, `inflight` WaitGroup, `closed`
flag, and an **injectable `gitRunner` for tests** — `:25-27` explains why:
*"Create's recovery paths depend on what the repo looks like between two of its
git invocations… which is otherwise unreachable from a test."*

### The invariant

`worktree.go:102-118`, quoted because it explains every design choice:

> An entry Create pushed may leave the stash list only after it has been
> successfully applied back to the working tree. Every other exit — a failed
> `worktree add`, a failed apply, a run abandoned before MergeBack — leaves the
> entry where the user can see it and reports why.
>
> `git stash drop` on an entry that was never applied is therefore never correct
> here: it deletes the content instead of restoring it, leaving it reachable only
> as a dangling commit until gc. A stash list with one extra entry in it is cheap;
> the work inside that entry is not.

### The Create sequence (`:307+`)

1. Reject if `closed` / already active
2. **`rev-parse --verify --quiet <base>^{commit}`** — resolved *before any git
   state changes* (`:322-330`), so a typo cannot leave a half-created worktree
   plus a stashed working tree
3. `stashBeforeWorktreeAdd` (`:232-247`): `git stash push -u -m <msg>`.
   Detection is **entry-count delta**, not string-matching — `stashIndexAfter()`
   (`:91-100`) counts lines before/after, because git's exit code (0=stashed,
   1=nothing, 128=fatal) and output are locale-dependent (`:222-230`).
   Message: `"pi-go-worktree-<agentID>-<unixNano>"` (`:85-87`), unique per Create
4. `claimExistingWorktree` (`:200-218`): a re-run of `/run` produces a
   deterministic branch name, so a leftover branch would break `worktree add -b`.
   Returns `reuse=true` if the branch is attached to exactly the worktree we'd
   create; **errors** if attached elsewhere — *"the alternative there is silently
   overwriting a live worktree"*
5. `addWorktree` (`:256-267`): `git worktree add [-b branch] <path> HEAD`.
   Comment `:253-255`: *"A base ref does not belong here: it is applied to the new
   worktree's index afterwards… Branching here instead would check out the base's
   content and leave nothing for `git diff` to report."*
6. **The `CreateAt` trick** (`:279-303`): the worktree is created at HEAD, then
   **the index is reset to `base`**, so `base..HEAD` appears as an *uncommitted
   diff*. This is what makes a reviewer see an already-merged PR: `git diff` —
   the only view `git-file-diff`/`git-hunk` have — reports it, and file reads give
   post-change content. A real branch (not detached HEAD) is kept so
   `CommitAll → MergeBack → Cleanup` still works

### Stash pop — the careful part

`popStashByMessage` (`:148-164`) uses `git stash apply` then `git stash drop`,
**never `git stash pop`**:

```go
if out, err := m.git("stash", "apply", "--quiet", oid); err != nil {
  // If apply failed, the entry is still in the stash list — return the
  // error but don't drop, so the user can recover manually.
  return fmt.Errorf("git stash apply %s (%s): %w: %s", oid, msg, err, out)
}
m.dropStashEntry(entry, oid, msg)
```

Located **by message, never by `stash@{0}`** (`:131-140`): anything else with repo
access can push a stash in between, and applying the top blindly would restore
someone else's work over the tree while leaving ours behind.

`dropStashEntry` (`:173-190`): `git stash drop` won't take an object id ("is not a
stash reference"), so the ref is **re-resolved and its oid re-checked immediately
beforehand**; a mismatch → leave the list alone. Comment `:177-181`: *"Deleting the
wrong entry, by contrast, destroys work — so a mismatch is always resolved in
favor of keeping both."* The manager mutex cannot help — "it does not serialize
git invocations from other processes" (`:146-147`).

### The rest

`CommitAll` `:446` uses write-tree/commit-tree plumbing so the merge still follows
the user's own config (`:441-445`); `CreateBackupBranch` `:500` survives
`Cleanup`; `Cleanup` `:523`; `cleanupWorktree` `:552` re-adds the entry on failure
so callers can retry and restores an un-restored stash; `MergeBack` `:615`
aborts the merge on conflict so the main repo is left clean; `findStashByMessage`
`:669` uses `git stash list --format=%s`, which renders the subject as `%s`, not
`%gs` (`:667-668`).

### Review — worktree isolation

**This is the most valuable piece of engineering in either subagent system, and
pi has no counterpart.**

The reasoning throughout is about **destroying a developer's work**. Every
decision — never `stash pop`, locate by message not index, re-verify the oid
immediately before dropping, leave the entry on any failure — is oriented around
"the failure mode is the user losing work, not the tool being untidy." The comment
at `:102-118` states the invariant explicitly, which is what makes the code
reviewable.

**The `CreateAt` index-reset trick is the most clever single idea in this report.**
Resetting the index to `base` after creating the worktree at HEAD makes an
already-committed change visible as an uncommitted diff. Since pi-go's only diff
tools are `git-file-diff` and `git-hunk` (both read `git diff`), and since
`/plan`'s and `/pr-autofix`'s workflows exist to review already-merged work, this
is what makes that possible at all. It required reading the comment at `:253-255`
to understand — which means it should be in the docs.

**Injectable `gitRunner` for testing mid-sequence git state** (`:25-27`) is
unusual and correct. The recovery paths depend on what the repo looks like
*between* two git invocations, which is otherwise untestable.

**Where pi-go could improve**: `MergeBack` aborting on conflict means a
subagent's work is discarded on conflict rather than left for the user to resolve.
Creating a backup branch *before* attempting the merge (the `CreateBackupBranch`
helper already exists at `:500`) would make conflicts recoverable. That is a
small change with real value.

## 5.4 Agent definitions & discovery

### pi-go

`AgentConfig` (`agents.go:29-45`): `Name, Description, Role, Worktree, Timeout,
Instruction, Tools, LSP, Source`.

**Frontmatter parser is hand-rolled** (`:82-126`): a line scanner toggling on
`---`, then `parseAgentFrontmatterLine` (`:183`, `SplitN(line, ":", 2)`). Unknown
keys silently ignored. Name defaults to the filename stem (`:84-85`).

Three parsing decisions worth noting:

- **`tools:` is appended, not assigned** (`:147`):
  `cfg.Tools = append(cfg.Tools, parseAgentToolList(value)...)`
- **`timeout:` is in milliseconds and sub-1000 values are rejected**
  (`parseAgentTimeout` `:159-171`):

```go
// agents.go:161-164
// The unit is milliseconds, which reads as seconds at a glance — a bundled
// agent shipped `timeout: 30` and was SIGKILLed 30ms in, every time, unable to
// emit a single token. Anything under a second cannot be deliberate, so treat
// it as the unit mistake it is rather than honoring a value that guarantees
// the agent never runs.
if ms < minAgentTimeoutMs { slog.Warn(...); return 0, false }
```

- **`lsp:`** (`:44-47`, applied `:146`) selects the child's language-server
  surface: `"off" | "min" | "full"`, defaulting to the child process's own `min`.
  Comment `:42-46`: *"Set it in frontmatter on agents that navigate code, so the
  wide LSP surface is bought per-agent instead of by every session."* Plumbed as
  `--lsp` in `spawnArgs` (`spawner.go:125-127`)

**Discovery** (`DiscoverAgents` `:261-307`):

1. **bundled** — `LoadBundledAgents()` (`embed.go:16-43`), `//go:embed bundled/*.md`
2. **user** — `~/.pi-go/agents/`
3. **project** — `findNearestProjectAgentsDir` `:219-232` walks **up** from cwd
   looking for `.pi-go/agents/`

Merged by `mergeAgentsByName` (`:239-259`) bundled → user → project, each pass
replacing same-named entries in place. **Precedence: project > user > bundled.**
`AgentScope` (`:20-27`): `both` / `bundled` / `project`.

### pi

`agents.ts`: paths `~/.pi/agent/agents` + project `.pi/agents/` found by walking
up (`findNearestProjectAgentsDir` `:112-123`).

Uses the **real YAML parser** `parseFrontmatter` from the host, so
`AgentFrontmatter` values are `unknown` (`:35-40`). `parseToolList` (`:53-61`)
accepts **both** `tools: read, bash` (string) and `tools: [read, bash]` (array).

`AgentConfig` has `model` (pi-go routes through `role` instead) and **no**
`worktree`, `timeout`, or `lsp`.

`discoverAgents` (`:125-143`): user then project into a Map, so **project > user**
on collision. **No bundled tier.**

### Review — agent definitions

**pi-go's `role:` indirection is the better model.** Routing through
`cfg.ResolveRole` means the same agent definition works with whatever cheap model
the operator has configured for that role, and swapping models is a config change
rather than an agent-file edit. pi's `model:` frontmatter hardcodes a provider/model
string per agent, so a user with different models has to fork the definitions.

**pi-go's `timeout` unit guard is a bug that already happened.** The comment
records it: a bundled agent shipped `timeout: 30` (meaning 30ms) and was SIGKILLed
before emitting a token, every time. The fix — reject sub-second values with a
warning rather than honoring them — is right, and the fact that it is written down
is why it will not recur.

**pi's YAML parser is better** and pi-go's hand-rolled scanner is the weaker
choice. But note pi's `AgentFrontmatter` values are `unknown` because of it, so
pi pays for the real parser with a loss of type safety. pi-go's `SplitN(line,
":", 2)` handles the common case and silently ignores unknown keys — acceptable
for a 7-key schema.

**pi's `tools: [array]` form should be adopted by pi-go** — it is strictly more
standard and costs 5 lines.

**pi-go's `lsp: off|min|full` per agent is a good idea with no pi analogue** and
directly serves pi-go's differentiator: 7 LSP tools declared for every child is
a lot of context for an agent that only runs `bash`.

**Neither side gates project agents on trust.** pi does in its *extension*
(`confirmProjectAgents` + `ctx.isProjectTrusted()`); pi-go does not.

## 5.5 The 18 bundled agents

`internal/subagent/bundled/*.md`, embedded via `//go:embed bundled/*.md`.

| Agent | Role | Worktree | Timeout | Transport |
|---|---|---|---|---|
| `explore` | smol | — | — | pi |
| `quick-task` | smol | — | — | pi |
| `memory-compressor` | smol | — | 600000 | pi (no tools) |
| `plan` | plan | — | — | pi |
| `code-reviewer` | slow | — | — | pi |
| `spec-reviewer` | slow | — | — | pi |
| `architect` | slow | — | — | pi |
| `discovery` | system | — | — | pi |
| `worker` | default | — | — | pi |
| **`designer`** | slow | **✓** | — | pi |
| **`task`** | default | **✓** | 1800000 | pi |
| `claude` | default | — | — | **ACP** |
| `gemini` | default | — | — | **ACP** |
| `cursor` | default | — | — | **ACP** |
| `copilot` | default | — | — | **ACP** |
| `agy` | default | — | — | **ACP** |
| `codex` | default | — | — | **Codex** |
| `codex-review` | default | — | — | **Codex** (read-only) |

`Role` maps to `cfg.ResolveRole` for model selection. Only 2 of 18 are
worktree-isolated — the two that edit.

pi ships 4 samples: `scout` (haiku, recon), `planner` (sonnet, plans),
`reviewer` (sonnet, review, has `bash`), `worker` (sonnet, full capabilities, no
`tools:` restriction).

## 5.6 Spawn transports

`dispatchSpawn` (`orchestrator.go:577-585`) — 3-way switch:

```go
switch {
case isACPAgent(agentName):   return dispatchACP(ctx, spawnOpts, agentName)
case isCodexAgent(agentName): return dispatchCodex(ctx, spawnOpts, agentName)
default:                      return o.spawner.Spawn(ctx, spawnOpts)
}
```

| Transport | File | Agents | Mechanism |
|---|---|---|---|
| **pi subprocess** | `spawner.go` (393) | 11 built-ins | `pi --mode json --model M --system S --lsp L "<prompt>"` |
| **ACP** | `spawner_acp.go` (277) | claude, gemini, cursor, copilot, agy (`:84-89`) | `acpSession` iface `:17-22`; per-runner clients under `internal/acp/client/{agy,claudecode,copilot,cursor,gemini}` |
| **Codex** | `spawner_codex.go` (180) | codex, codex-review (`:19-22`) | direct JSON-RPC to `codex app-server` — "no broker and no ACP shim" (`:15-16`) |

Details worth noting:

- `NewSpawner("")` (`spawner.go:66-77`) defaults to `os.Executable()` so children
  match the parent binary version; falls back to `"pi"`
- `spawnArgs` (`:110-131`): `--mode json` first, then
  `--model/--url/--insecure/--header*/--system/--lsp`, prompt positional **last**
- Env: `ChildEnv(ConcurrencyFromEnv())` (`:139`)
- `cmd.WaitDelay = 3 * time.Second` (`:147`)
- **`stderrCollector`** (`spawner.go:186-190`) avoids a deadlock: *"Reading them in
  sequence — stdout to EOF, then stderr — deadlocks the moment the child writes
  more than a pipe buffer (~64KB) to stderr… The child then sits there until a
  timeout kills it, and because stderr was never drained the error arrives with no
  diagnostic text attached at all."* Buffer capped at 64 KB (`:32`)
- **`ErrSubagentTimeout`** (`spawner.go:25`) exists because `os/exec`'s `Wait`
  prefers the `*ExitError` over the context error, so `context.DeadlineExceeded`
  is discarded and every timeout surfaces as the bare text `"signal: killed"`.
  `timeoutHint` (`:35`) names the knobs
- **ACP completion sentinel** (`spawner_acp.go:37-45`):
  `acpCompletionSentinel = "<Task Completed>!"` with a loose matcher `"<Task
  Completed>"` — comment `:41-45`: *"Models routinely drop the trailing `!` or
  elide the space… (observed with Gemini emitting `Gemini CLI<Task Completed>!")"*
  Codex needs no sentinel (`spawner_codex.go:45-48`): the app-server sends an
  explicit `turn/completed` notification
- **Anti-hallucination preamble** (`spawner_acp.go:53-65`) — 6 rules injected into
  every external subagent prompt: run `git diff --name-only` before claiming
  completion, don't claim a build passes without pasting output, don't fabricate
  tool output, etc.

### Review — spawn transports

**Three spawn backends with per-vendor quirks handled is a real capability.** pi
spawns only `pi` subprocesses. Being able to say "use `explore` for fast reads,
`claude` for a hard review, `codex-review` for an independent pass" — with each
dispatched over the protocol that vendor actually supports — is a genuine
multi-agent product feature.

**The completion-sentinel handling is an excellent hard-won detail.** Using a text
sentinel to detect turn completion over ACP, and then discovering empirically that
models drop the trailing `!` or elide the space, and making the matcher loose
enough to tolerate that — with the observation recorded — is exactly the kind of
thing that only survives contact with real providers. Codex's explicit
`turn/completed` notification is the right design; ACP has no equivalent, so the
sentinel is the workaround.

**The stderr deadlock guard and `ErrSubagentTimeout` are both correctness fixes
for real `os/exec` behavior**, and both are explained at the point of the code.
The `WaitDelay` is the modern fix for the same class.

**The anti-hallucination preamble for external subagents is clever**: an external
agent that reports "done" without evidence is a specific, known failure mode, and
injecting 6 rules into the prompt is cheaper than verifying every claim.

## 5.7 TUI presentation

### pi-go

**Agent card** (`tool_display.go:235-249`): header + live event window + result
summary.

`agentBracketLabel` (`:72-96`): ACP-backed names keep their own label; all
pi-based subagents collapse to `"pi"`; compound `"claude+explore+task"` →
`"[claude+pi]"` with dedup. `acpBundledAgents` set at `:30-38`.

`agentToolColor` (`:49-65`): each ACP agent gets a distinct hue so parallel runs
are distinguishable — claude→Peach, cursor→Overlay, gemini→Blue; compound types use
the first ACP component found.

**Bounded live window** (`:455-459` `maxAgentOutputLines`): the budget is in
*rendered lines*, not events — the comment is explicit: *"A single event carries an
unbounded amount of text — a subagent's final analysis is one `text` event."* A
note line is emitted whenever output was withheld (`:359-361`).

**Failure detection** (`isFailedToolResult` `:543`): sniffs the JSON `error` field
**and** a non-zero `exit_code`.

**Sidebar live list** (`sidebar.go:544`, `sortAgentsForDisplay` `:565` — running
first).

**ATIF linkage**: `SubagentTrajectoryRef string` at `internal/atif/types.go:62` —
a *relative* path from the parent trajectory to the sub-session's
`trajectory.atif.json` (asserted in `atif/link_test.go:113`,
`atif/types_test.go:241`).

### pi

Collapsed by default: status icon + last 5-10 items + usage line
(`COLLAPSED_ITEM_COUNT = 10`). Ctrl+O expands to full task text, all tool calls,
markdown-rendered final output, per-task usage.

`formatToolCall` (`:71-131`) hand-mimics built-in tool formatting for
bash/read/write/edit/ls/find/grep with `~` home-path shortening.

`formatUsageStats` (`:45-69`):
`3 turns ↑input ↓output RcacheRead WcacheWrite $cost ctx:contextTokens model`

### Review — TUI presentation

**pi-go's per-agent color coding is the better idea.** In a parallel run of 8
subagents, knowing which card is claude and which is gemini by color is far more
useful than reading the label. pi has no equivalent and relies on the label text.

**The bounded-window-in-rendered-lines decision is correct and pi gets it wrong.**
pi's `COLLAPSED_ITEM_COUNT = 10` counts *items*; pi-go's `maxAgentOutputLines`
counts *rendered lines*. A subagent's final analysis is one `text` event carrying
thousands of characters — pi's count-10 would render all of it, pi-go's
line-count would not. The comment at `tool_display.go:455-459` explains this
exactly, which means it was learned the hard way.

**ATIF `subagent_trajectory_ref` gives pi-go something neither side surfaces in
the UI**: a durable, machine-readable link from a parent trajectory to each
child's full trajectory. pi's subagent results are `{messages[], usage, exitCode,
stderr}` — no persistent artifact. For post-hoc debugging of a multi-agent run,
pi-go's answer is better by a wide margin.

**pi's usage line per task is worth adding** — `formatUsageStats` shows tokens
and cost per subagent, which is how a user attributes spend. pi-go's sidebar shows
the agent list but not per-agent cost.

## Review summary — subagents & multi-agent

**pi-go leads by a wide margin**: built-in vs example extension; 18 bundled
agents vs 4 samples; three spawn transports vs one; a full `WorktreeManager`
with the `base:` index-reset trick and stash-by-message safety vs none; a
process-wide concurrency pool with a halving-per-depth nesting budget vs a
per-call worker pool; an env allowlist; spawn retry with crash/silent
classification; a typed timeout error; per-agent LSP surface selection; runtime-
generated tool descriptions that report the real pool size; parameter alias
repair; and ATIF trajectory linkage.

**pi leads on**: the trust gate for project agents; `cwd` per task item; a
per-task output cap; a per-task usage line; and the in-process durable subagent
that creates a *conversation* the user can switch into and keep talking to
(`durable/subagent.ts:23-25`) — a genuinely different UX model from
fire-and-forget.

**pi-go's three concrete fixes:**

1. **Gate project-discovered agents on trust**, as pi's extension does. A hostile
   repo shipping `.pi-go/agents/planner.md` is currently picked up silently.
2. **Create a backup branch before `MergeBack`** (`CreateBackupBranch` already
   exists at `worktree.go:500`) so a conflict does not discard the subagent's work.
3. **Accept `tools: [a, b]` array form** in the agent frontmatter parser, matching
   pi and every other agent-definition format in the ecosystem.

**The one capability worth considering from pi**: the in-process conversation
model, where a subagent's output is a real session the user can open. pi-go's
subagents are subprocesses whose only persistent artifact is an ATIF file. Making
`task` agents create a pi-go session instead would mean a failed subagent run
leaves behind a resumable transcript rather than only a log line.