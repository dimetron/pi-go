# 03 — Tools, Extensions, Security

## Side-by-side summary

| | pi-go | pi |
|---|---|---|
| Model-facing tools | **~29** | 8 (+4 extensions) |
| LSP tools | 7 | ✗ |
| Memory tools | 3 | ✗ |
| Git tools | 3 | ✗ |
| Extension events | 2 (shell) | 36 (in-process) |
| Hook can block | ✗ | ✓ |
| Hook can rewrite args/results | ✗ | ✓ |
| UI extension hooks | ✗ | 30 |
| Output compaction | ✓ 9 pipelines | ✗ |
| Result dedup | ✓ | ✗ |
| Read-before-write ledger | ✓ | ✗ |
| Skill injection scanner | ✓ blocking | ✗ |
| MCP | client + server | client only |
| MCP conformance | ✗ | ✓ 3 versions, baselined |
| OS sandbox | ✓ Seatbelt profile | delegated to docs |
| Per-tool approval | ✗ | ✗ |

## 3.1 Tool inventory

### pi-go — 16 core + 13 conditional

Registration is positional through one generic wrapper,
`newTool[TArgs,TResults](name, description, handler, aliases...)`
(`internal/tools/registry.go:279`), which wraps the ADK `functiontool` in a
`coercingTool` (`:302-315`). `CoreTools` (`:92-148`) builds 12 builders plus
`session-stats` plus opt-in `web_search`.

Two schema flavors per tool — a deliberate split documented at
`registry.go:166-172`:

- **runtime** = `lenientSchema` → every object gets `additionalProperties:{}`,
  `Required=nil` (`relaxSchema`, `:177-203`)
- **model-facing declaration** = `declarationSchema` (required preserved),
  swapped in by `coercingTool.Declaration()` (`:319-331`)

| Tool | File | Notable limits |
|---|---|---|
| `read` | `read.go:79` | 2000 lines, 256 KB byte budget, 2000 chars/line. `next_offset` precomputed. 12 param aliases. |
| `write` | `write.go:27` | gated on read-ledger |
| `edit` | `edit.go:33` | exact-string replace |
| `read_image` | `read_image.go:103` | path or URL; re-resolved inside sandbox as defense (`:231`) |
| `bash` | `bash.go:146` | default 60s, **max 600s**. Timeout **backgrounds, never kills**. Rejects pasted `bash_wait(...)` text (`:155`) |
| `bash_wait` | `bash.go:251` | `maxBashWait=60s` (`:118`) |
| `bash_kill` | `bash.go:263` | kills process tree |
| `grep`/`ripgrep` | `grep.go:133` | `maxGrepMatches=200` (`:20`) |
| `find` | `find.go:35` | — |
| `ls` | `ls.go:37` | — |
| `tree` | `tree.go:47` | default 3, max 10 |
| `git-overview` | `git_overview.go:52` | — |
| `git-file-diff` | `git_diff.go:36` | — |
| `git-hunk` | `git_hunk.go:42` | — |
| `session-stats` | `session_stats.go:67` | — |
| `web_search` | `web_search.go:97` | **opt-in only** (`PI_WEB_SEARCH=1`) |
| `lsp-*` ×7 | `lsp.go:133-193` | diagnostics, definition, references, hover, symbols, workspace-symbol, code-action |
| `mem-*` ×3 | `mem_search.go:36,102,163` | search, timeline, get |
| `subagent` | `subagent.go:98` | single/parallel/chain |
| `a2a` | `a2a.go:376` | + gRPC-web + kagent variants |
| `fetch_docs` | `llms.go:203` | llms.txt protocol |

**Global caps** (`truncate.go:5-7`): `maxOutputBytes = 256*1024`,
`maxLineLength = 500`, UTF-8-safe cutting (`truncate.go:13-24`). Secrets
scrubbed by 8 regex families incl. `sk-`, `ghp_`/`gho_`, JWT, `AKIA`
(`redact.go:6-31`).

**Tool-name instability.** `grep` renames itself to `ripgrep` at runtime if
`rg --version` succeeds (`grep.go:97`, `:128-132`). A model-visible name that
changes by host.

### pi — 8 built-ins, richer plumbing

`ToolName` union at `core/tools/index.ts:95-105`. Each tool has a
`createXToolDefinition` (TypeBox schema) + `createXToolOperations` (injectable
for testing/remote).

| Tool | File | Notable |
|---|---|---|
| `read` | `read.ts:74` | 2000 lines, **50 KB** bytes. Handles **images natively** (`:122-129`). `constrainedSampling:{type:"json_schema",strict:"prefer"}` (`:80`) |
| `write` | `write.ts:50` | — |
| `edit` | `edit.ts:149` | **batch array** of `{oldText,newText,replaceAll}`; 556 LOC of diff logic; serialized by `file-mutation-queue.ts` |
| `grep` | `grep.ts:76` | `DEFAULT_LIMIT=100`, `GREP_MAX_LINE_LENGTH=500`. Extra params: **`literal`**, **`context`** |
| `find` | `find.ts:76` | `DEFAULT_LIMIT=1000` |
| `ls` | `ls.ts:60` | `DEFAULT_LIMIT=500`. **Includes dotfiles** |
| `bash` | `bash.ts:418` | **no default timeout**. `MAX_TIMEOUT_SECONDS = 2147483.647`. **Truncates to the tail**; full output to temp file. `STRUCTURED_OUTPUT_MAX_BYTES = 1MB` for codemode |
| `powershell` | `powershell.ts:40` | **a separate tool, not a description switch** |

Two structural concepts pi-go lacks:

- **`BashOperations` / `BashSpawnHook`** — bash is an *injectable interface*, so
  an extension can supply a remote or containerized executor without touching
  the tool. This is the hook the sandbox docs describe.
- **Every tool has `*Operations` + `*ToolDetails` + `*ToolOptions` + a renderer**
  (`renderers/read.ts` 176, `edit.ts` 238, `write.ts` 181, `bash.ts` 146, …).
  TUI presentation is decoupled from execution.

`output-accumulator.ts` (263) handles live streaming; `truncate.ts` (314)
provides `truncateHead`/`truncateTail`/`truncateMiddle` with `formatSize`.

### Review — tools

**pi-go's tool set is 3× larger and, in several cases, better built:**

- `read` returns `next_offset` precomputed and a `note` on empty files, past-EOF,
  or clamped lines — the comment at `read.go:71-75` says it outright: *"Silence is
  the most expensive thing this tool can return."*
- pi-go's `bash` **backgrounds on timeout** and exposes `bash_wait`/`bash_kill`
  with a handle. pi's `bash` has a near-infinite timeout and no handle — a long
  command blocks the turn entirely.
- pi-go's strict-declaration/lenient-runtime split is a genuine LLM-robustness
  technique pi does not have.
- pi-go's `grep` cap is 200 vs pi's 100, but pi's `find` is 1000 vs pi-go's
  unstated.

**Where pi is better:**

- `edit` takes a **batch array** with `replaceAll`. pi-go's single exact-string
  replace means three edits to one file is three tool calls and three file
  rewrites.
- `grep` has `literal` and `context` params. pi-go has neither.
- `read` handles images natively; pi-go needs a separate `read_image` tool,
  which costs a round trip and a schema slot.
- `powershell` is a real tool rather than a description switch.
- **Truncation strategy.** pi-go's `truncateOutput` keeps the **head** only and
  appends `\n... (output truncated)`. For `bash`, the discarded tail is usually
  the error and the summary — the part you need. pi's `bash` uses `truncateTail`
  and spills the full output to a temp file; MCP output uses `truncateMiddle`.
  **This is the single cheapest improvement available in this domain.**

### The `grep` → `ripgrep` rename

`grepToolName = "ripgrep"` when `rg` is present (`grep.go:131`). This is a
deliberate capability-self-naming design, and it has been bitten before — the
AGENTS.md notes that a test requiring `"ripgrep"` by name fails on a host without
`rg`. Beyond the test problem, a session that recorded `grep` and is resumed on a
host with `rg` has a transcript referencing a tool that no longer exists. pi's
`grep` is always `grep`.

Verdict: capability self-naming is defensible for the *implementation* but wrong
for the *model-visible name*. Keep `grep` as the tool name; note ripgrep in the
description if it is being used.

## 3.2 Extension surface — the largest gap

### pi-go — two shell hook events

`HookConfig` (`internal/extension/hooks.go:30-40`):

```go
type HookConfig struct {
  // Event is "before_tool" or "after_tool".
  Event   string   `json:"event"`
  Command string   `json:"command"`
  Tools   []string `json:"tools,omitempty"`
  Timeout int      `json:"timeout,omitempty"`
}
```

Two events. The command receives JSON on stdin (`runHookCommand` `:497-521`:
`{"tool":..., "data":...}`); stdout is discarded.

**What they can actually do: almost nothing.**

```go
// internal/extension/hooks.go:152-157
// Non-fatal: log and continue
hookLogf(...)
return nil, nil
```

`BuildBeforeToolCallbacks` returns `nil, nil` **unconditionally** — even when the
hook command fails. Against ADK's contract (`base_flow.go:1421-1434`, where a
non-nil return short-circuits and the tool never runs), this means:

| Capability | pi-go |
|---|---|
| Block a tool call | ✗ |
| Rewrite tool arguments | ✗ |
| Rewrite tool results | ✗ |
| Terminate the batch | ✗ |
| Rewrite history | ✗ |

There is no extension registration API at all — no way to add a tool, a command,
a renderer, a provider, or a virtual model from outside the binary.

### pi — 36 events, ~28 registration methods

`ExtensionAPI` (`core/extensions/types.ts:1519+`, a 2,245-line type surface):

`registerTool` `:1580` · `registerCommand` `:1628` · `registerShortcut` ·
`registerFlag`/`getFlag` · `registerMessageRenderer` `:1663` ·
`registerMarkdownTransformer` · `registerEntryRenderer` `:1669` ·
`sendMessage`/`sendUserMessage`/`appendEntry` · `setSessionName`/`setLabel` ·
`exec` · `getActiveTools`/`setActiveTools`/`getAllTools` · `setModel`/
`getThinkingLevel`/`setThinkingLevel` · `registerProvider` `:1803` ·
`registerMcpServer`/`getMcpServers` · `registerVirtualModel` + `route()` ·
`events` bus.

The two events that matter most:

```ts
// types.ts:1402-1411
export interface ToolCallEventResult {
  /** Block tool execution. To modify arguments, mutate `event.input` in place instead. */
  block?: boolean;
  reason?: string;
  terminate?: boolean;   // stop the batch — only if EVERY finalized result sets it
}
```

```ts
// tool_result event: replace content, structuredContent, usage
// context event:      ContextEventResult{messages?} — full rewrite of history
// before_provider_headers: mutates ProviderHeaders — can inject/rewrite auth headers
```

So pi **can block, rewrite arguments, rewrite results, terminate the batch,
rewrite history, and intercept credentials** — all six, from an in-process
extension.

**`ExtensionUIContext` — 30 hooks** (`types.ts:149-303`): `select`, `confirm`,
`input`, `notify`, `onTerminalInput`, `setStatus`, `setWorkingMessage/Visible/
Indicator`, `setHiddenThinkingLabel`, `setWidget`, `setFooter`, `setHeader`,
`setTitle`, `pasteToEditor`, `setEditorText`/`getEditorText`, `editor` (modal
prompt), `addAutocompleteProvider`, `setEditorComponent`/`getEditorComponent`,
`getAllThemes`/`getTheme`/`setTheme`, `getToolsExpanded`/`setToolsExpanded`.

Plus `ExtensionContext` proper (`:350-434`): `getContextUsage`, `compact`,
`getSystemPrompt`, `executeTool`, `waitForIdle`, `newSession`, `fork`,
`navigateTree`, `switchSession`, `reload`, `abort`, `shutdown`,
`isProjectTrusted`. An extension can **fully drive the TUI including the editor**.

Full 36-event list with block/mutate powers is in
[99-verification.md](99-verification.md).

**Loading**: `jiti` runtime TS transpile, no build step (`loader.ts:12`, `:569-574`).
Discovery precedence: `cwd/.pi/extensions/` → `<agentDir>/extensions/` → explicit
configured paths (`loader.ts:821-869`). Each dir accepts a `package.json` with a pi
manifest **or** a bare `index.ts`.

**Distribution**: `core/package-manager.ts` (2,459+ lines), `ParsedSource =
NpmSource | GitSource | LocalSource` (`:156`). No curated marketplace — npm + git
+ local, the same "copy config over" philosophy as MCP.

### ToolExposure — pi's context-budget mechanism

```ts
// types.ts:493-510
// - `direct`: declared to the model while active, and callable while active.
// - `model-only`: declared to the model while active, never callable.
// - `codemode`: callable whenever registered. Not declared unless explicitly activated.
// - `deferred`: like `codemode`, but tool search can find it.
// - `hidden`: registered but unreachable.
export type ToolExposure = "direct" | "model-only" | "codemode" | "deferred" | "hidden";
```

Why it matters: it decouples three things pi-go conflates — **what the model
sees** (`declared`), **what other tools can invoke** (`callable`), and **how the
model discovers it** (inline in a codemode description, or findable via
tool-search). With 8 core tools + N MCP servers, declaring everything is untenable;
`prepareLoadout` (`:615`) returns `{descriptions?, hiddenDeclarations?}` — a lever
for shrinking the declared surface without deactivating, surviving `/tree` and
resume.

Supporting: `ToolAnnotations` `:512-525` (MCP's `readOnlyHint`/`destructiveHint`/
`idempotentHint`/`openWorldHint`, explicitly *"not verified; permission extensions
can use them to decide which calls to confirm"*), `ToolNamespace` `:527-538`,
`ToolLoadout` `:540-551`.

### Review — extension surface

**This is the gap that most constrains what pi-go can become.** pi-go's hooks are
a shell-script escape hatch that cannot block. pi's are a typed in-process API
that can do everything.

But the two are not directly comparable in ambition: pi's `ExtensionAPI` is
2,245 lines of type surface plus a 1,551-line runner plus a 869-line loader plus
TypeScript runtime loading. pi-go cannot match that without becoming a different
program.

**What pi-go can adopt cheaply, in order of value:**

1. **Make `before_tool` able to deny.** The ADK contract already supports it —
   return non-nil or an error. pi-go discards both. Returning the hook's non-zero
   exit as an error is a ~10-line change and immediately turns every existing
   hook into a real gate. *This is the highest-leverage fix in the whole report.*
2. **Let `after_tool` rewrite results.** `BuildAfterToolCallbacks` already
   returns `result, nil` (`:178`); allowing a hook to substitute content is
   similarly small.
3. **A typed Go extension interface** for in-process extensions, even a minimal
   one: `Name() string`, `Tools() []Tool`, `BeforeTool(...) (deny bool, reason string)`.
   Go plugins are awkward, but an in-repo registry is not — pi-go already has
   `internal/plugin` for git-distributed *config*; the same mechanism could
   distribute compiled binaries.
4. **`ToolExposure`-style tiers.** pi-go has 29 tools, all declared
   unconditionally. The 7 LSP tools and 3 memory tools are the obvious
   candidates for a tier system — they should not cost context in a session that
   never uses them. The compactor already works per-tool, so the mechanism
   exists.

**Do not** attempt to match the 30-UI-hook surface. pi-go's TUI is a single
`tea.Model` with fixed sub-models; a UI extension API is a much larger project
than the extension API itself.

## 3.3 Skills

### pi-go

1. **Discovery** — `DefaultSkillDirsIn(root)` (`skills.go:473-513`) in
   precedence order (later overrides): installed plugin skill dirs →
   `~/.pi-go/skills` → `.pi-go/skills`, `.claude/skills`, `.cursor/skills` (each
   found by walking **up** from cwd, `findNearestDir` `:530-544`)
2. **Layout** — `<dir>/SKILL.md` or `<dir>/<name>/SKILL.md`, plus
   symlink-to-dir support (`:217-226`)
3. **Precedence** — `seen map[string]int`; `appendOrOverrideSkill` (`:183-190`)
   *replaces in place*, so project beats user beats bundled
4. **Audit gate** — every candidate is scanned before load (`:162`). `AuditBlock`
   (default) rejects critical findings
5. **To the prompt** — two levels:
   - **Level 1 (menu)**: `appendSkillsMenu` (`agent.go:918-928`) emits
     `- /<name>: <description>`. Only name+description, so bodies cost nothing.
   - **Level 2 (activation)**: `AppendActiveSkill(prompt, skill, body)`
     (`agent.go:939-941`) appends the body **per turn**
6. **Bundled** — 4: `agents-md`, `memory-index`, `osx-tuning`, `ponytail`

The multi-directory support (`.claude/skills`, `.cursor/skills`) means pi-go
picks up skills authored for other agents — a real interoperability win.

### pi

`resources_discover` event (`:692`) lets extensions *contribute*
`skillPaths`/`promptPaths`/`themePaths`; a skill is discovered from
`.pi/skills` and `~/.pi/agent/skills`; `enableSkillCommands` defaults true.
Progressive disclosure is the same two-level model (documented in
`docs/skills.md`). Loaded via `loader.ts` / jiti.

pi has **no skill content scanner**. Its defense is the `project_trust` gate,
which is a *load-time* switch, not a *content* analysis. A skill in
`~/.agents/skills` is trusted by construction.

### Review — skills

pi-go's implementation is better in two ways: cross-agent directory support
(`.claude/skills`, `.cursor/skills`) and the blocking content scan.

**The audit scanner is the most sophisticated prompt-injection defense on either
side** and pi has no equivalent. `internal/audit/chars.go:38-61` flags:

- Unicode tag chars (U+E0001–U+E007F) as **critical**
- BiDi overrides as **critical**
- Variation Selectors Supplement as **critical**, named the "Glassworm vector"

with three policy modes (`AuditBlock` / `AuditWarn` / `AuditSkip`,
`skills.go:39-46`) and a `pi audit` CLI with text/json/markdown formatters.
Unicode tag characters are invisible in most terminals and are the standard way
to smuggle instructions past a human reviewer; blocking them at load time is the
right place.

**The gap is that pi-go does not scan at the other end.** It audits *skill
content* but not *tool output* or *model output* — a README fetched by the model
carries the same tag-character payload as a malicious SKILL.md.

## 3.4 MCP

### pi-go

**Config** (`mcp.go:62-70`): `{name, command, args[], url, headers{}, oauth}` —
`command` = stdio subprocess, `url` = Streamable HTTP. Loaded from
`internal/config/config.go:434`.

**Registration** — `BuildMCPToolsets` (`:75`) returns ADK `[]tool.Toolset`, not
tools; tools are pulled lazily per request. **A failing server is logged and
skipped, never fatal** (`:78-80`).

**Resilience** — `resilientToolset` (`:153-335`): on transport error it closes,
reconnects, retries `listTools`, and returns a **model-visible failure notice**
instead of an exception (`:199-215`). `connTrackingTransport` (`:113`) +
`respawnTransport` (`:97`) exist because a stdio child that dies poisons the
transport permanently ("Stdout already set").

**OAuth** — more than pi's own docs suggest. `SetInteractiveOAuth` (`:53`) gates
auto-relogin on 401/403; `mcpOAuthConnectTimeout = 10 * time.Minute` (`:59`) sized
for a browser round-trip; injectable loopback listener for headless tests
(`:45-48`); `isMCPAuthError` (`:383`) string-matches "unauthorized"/"forbidden"
because the transport returns no typed errors; `withoutAuthorization` (`:358`)
strips the stale header before retry; `canReauthorize` (`:264`) refuses for
stdio (no bearer to renew). Tokens persisted by `mcp_oauth_store.go`.

Tool-name collision handling in `internal/agent/toolnames.go:33` `dedupeToolsets`:
resolved to `<toolset>_<tool>` / `<tool>_2`, assignments pinned for session
lifetime (`:120` memoizes), truncated **from the front** to 64 chars (`:174`).

Uses the official `modelcontextprotocol/go-sdk v1.7.0` — so it gets **server**
support too.

### pi

`packages/mcp` (3,128 LOC) — hand-rolled, **client only**, zero official SDK.

**Transports** (`src/transports/`): `stdio.ts`, `streamable-http.ts` (SSE + POST,
exponential reconnect 1s→30s, max 5 retries, 8 KiB error-body cap),
`in-memory.ts` (testing), `transport.ts` (`DEFAULT_MAX_MESSAGE_BYTES = 16 MiB`).

**Client** (`client.ts:615`): `connect`, `notify`, `ping`, `listTools`,
`listResources`/`listResourcesPage`, `listResourceTemplates`, `readResource`,
`callTool`, `setRequestHandler` (so it can act as a server-ish peer),
`onNotification`, `onError`/`onClose`.

**Auth**: `auth-provider.ts` `AuthProvider.token()` + `onUnauthorized({response,
serverUrl, fetch, token})` for 401/403 `insufficient_scope`, plus a full OAuth
module (`discovery`, `flow`, `provider`, `callback`, `errors`).

**Exposure** — `src/extensions/mcp/`: naming at `tools.ts:82-91`:

```ts
const name = `mcp__${server}__${tool}`.replace(/[^A-Za-z0-9_]/g, "_");
if (name.length <= 64 && !isTaken(name)) return name;   // MAX_TOOL_NAME_LENGTH
return `${name.slice(0, 64 - hash.length - 1)}_${hash}`;  // sha256 suffix
```

The hash suffix exists because sanitizing **collides** (`a-b` and `a_b` both →
`a_b`) — a real bug prevented.

**The >20 KB rule** (`MCP_OUTPUT_MAX_BYTES = 20*1024`, `tools.ts:46`): head+tail,
middle cut, Codex-style:

```ts
// tools.ts:127-138
const truncation = truncateMiddle(combined, MCP_OUTPUT_MAX_BYTES);
if (!truncation.truncated) return { content };
const tokens = Math.ceil(truncation.totalBytes / 4);
const text = `Warning: truncated output (original token count: ${tokens})\n...`;
```

Full text → temp file with **`mode: 0o600`** and the comment *"Results can carry
private data, so only the user may read the file"* (`:65-70`).

**Critical asymmetry**: codemode scripts get the **whole `CallToolResult`, never
truncated** — because it is the tool's `structuredContent` (`:7-10`).
Truncation is a *model-token* budget, not a data-loss budget.

**Security note worth quoting**: *"Project files cannot use `"auth": {"provider":
...}`, so a repository cannot pick where the credential goes."*

**Conformance** — `test/mcp-conformance/run.ts:365` runs the **official**
`@modelcontextprotocol/conformance@0.2.0-alpha.11` suite across **three
negotiated protocol versions** (`2025-03-26`, `2025-06-18`, `2025-11-25`) and
diffs every check against a committed `baseline.json`. CI job `mcp-conformance`.
`client.ts` exercises the *production* connection with a **simulated browser**:
fetches the auth URL, delivers the redirect to pi's loopback callback, re-signs-in
up to 3×.

**Known failures are documented, not hidden**: `elicitation-sep1034` (no
elicitation), `auth/basic-cimd` (dynamic client registration, not CIMD),
`auth/scope-retry-limit` (fails by design).

### Review — MCP

**pi-go wins on substrate**: the official Go SDK gives it MCP **server** support
for free, plus spec conformance by construction. pi hand-rolled 3,128 LOC and
got client only.

**pi wins on discipline**: the conformance suite with a committed baseline is the
strongest verification asset in either repo. pi-go has
`hack/test/mcp` and hand-written unit tests. Running the official suite against
pi-go's `go-sdk` client would be close to free — the SDK ships its own conformance
harness — and would catch protocol drift.

**The 0o600 temp-file detail is worth copying.** pi-go's `truncateOutput` has no
spill path at all; when it truncates it discards the tail entirely. When pi-go
adds a spill, MCP results must not go to a world-readable temp file.

**The codemode asymmetry is also worth copying in spirit**: pi-go's compactor
truncates what reaches the model. A future tool that needs the *full* payload
should be able to say so. The compactor's `CompactResult{CompactWrite{Key,Value}}`
structure (§3.5) is the natural place for a "never truncate me" flag.

## 3.5 Output compaction & dedup

### pi-go — this is a genuine lead

**Compactor** (`compactor.go`): `CompactorConfig` is a 17-field per-tool config;
`DefaultCompactorConfig` (`:39-63`) enables all seven stages. Caps:
`MaxChars 24000`, `MaxLines 440`, `MaxTestFailures 10`/`MaxTestFailLines 8`,
`MaxBuildErrors 10`/`MaxBuildErrLines 20`, `MaxDiffLines 100`/`MaxDiffHunkLines 20`,
`MaxStatusFiles 10`, `MaxLogEntries 40`, `MaxLinterRules/Files 20`,
`MaxSearchPerFile 20`, `MaxSearchTotal 100`. Split across `compactor_bash.go`
(560), `_git.go` (480), `_search.go` (251), `_read.go`, `_ansi.go`, `_metrics.go`.

The design note at `compactor.go:65-77` is a post-mortem worth reading in full —
it records that the previous design *guessed* a target field from a fixed probe
order, "which silently wrote to the wrong field or to none, and is why **seven of
nine pipelines were no-ops in production**". AGENTS.md then records the second
bug: handing ADK the callback slice ran only OTEL tracing, so "dedup and the
compactor was dead for months" (`hooks.go:184-211`).

**Dedup** (`dedup.go`): replaces byte-identical repeats with a one-line pointer.
Motivated by measurement — *"27% of read/search output bytes were byte-identical
repeats"* (`dedup.go:18-20`). `dedupMinBytes=512`. The allowlist `dedupTools`
(`:39`) is read-only only and **enforced against reality by a test**
(`TestDedup_ToolListMatchesReachableFields`), because `git-hunk` carries an array
and `git-overview` has no recognized key, so listing them "would claim coverage
that cannot exist".

### pi

No output compaction and no result dedup. MCP output gets a 20 KB middle-truncation
with a temp-file spill, and that is the whole story. Nothing shrinks `read` output,
nothing elides repeats.

### Review — compaction & dedup

**pi-go is clearly ahead.** 27% byte-identical repeat elimination plus nine
per-tool compaction pipelines is a real, measured saving that pi has no answer to.

The honesty of the two post-mortems is worth more than the feature. Both were
invisible failures — no crash, no red test, just a config that did nothing. The
guards built afterward (`TestDedup_ReachableFieldsAreHonest`,
`TestCompactor_EveryToolCompacts`, `TestCompactorRouting_EveryRegisteredTool`)
are the right shape, and AGENTS.md's "never trust a big win without a proof check"
is the right institutional memory.

**The one gap**: pi-go's compaction is invisible to the model. A stubbed tool
result says something was superseded but the model has no record of what. pi's
MCP truncation at least states the original token count (`tools.ts:128`). Adding
`truncated: true` + original size to each compacted result is cheap and would let
the model know it is looking at an elision.

## 3.6 Sandbox & permissions

### pi-go — two different things share the name

**(a) `internal/tools/sandbox.go` — a filesystem jail. Real but narrow.**

Holds `*os.Root` (Go 1.24 `os.OpenRoot`, `:87`) plus `extraRoots` (`:116`).
`Resolve` rejects `..` escapes (`:224-227`, `:249-252`); all I/O goes through
`resolveToRoot` → `root.ReadFile/Open/Lstat/MkdirAll` (`:260-367`). Because it
is `os.Root`, symlink escape is **kernel-enforced**, not string-checked.

**Critically: `bash` does not go through it.**

```go
// internal/tools/bash.go:181-186
out, err := sup.Run(parentCtx, runRequest{
  dir:      sb.Dir(),   // ← the only use of the sandbox
  command:  input.Command,
```

No seccomp, no rlimit, no syscall filter, no command allowlist, no env scrubbing,
no network policy. `bash_supervisor.go` (757 LOC) is a *concurrency/reaping*
subsystem, not a security one. Neither is `web_search`, `fetch_docs`, or `a2a`.

**(b) `cmd/pi-sandbox/` — the real boundary. Opt-in, macOS-only.**

`pi-profile.sb` is a `sandbox-exec` (Seatbelt) profile, embedded and applied by a
launcher wrapping the whole `pi` process: `(deny default)` + `(trace default)`,
then allow `file-read*` on `/usr`, `/System`, `/opt/homebrew`, `$CWD`,
`$HOME/.pi-go`, `$HOME/go`; `file-write*` on `$CWD`, `$HOME/.pi-go`, `/tmp`;
network narrowed to `*:443` + `*:11434` (Ollama) with `(deny network-outbound)`
last. Handles the documented DTrace/SIP limitation via `log stream` +
`sandbox.log`.

**Per-tool approval: none exists.** A repo-wide grep for
`approv|confirm|permission|allowlist|denylist|consent` across `internal/tools/` and
`internal/extension/` returns only web-search host allowlisting, the read_image
sandbox re-check, and MCP *OAuth* approval. **Nothing gates `bash`/`write`/`edit`.**

The only guardrail is the **read-before-write ledger** (`ledger.go`, 119 LOC):
`read` records the file and whether the view was partial; `write`/`edit` reject
overwriting a file not fully seen. That is a *correctness* guard against blind
overwrite, not a security control.

### pi — no boundary, but documented

`docs/security.md:5-7` opens:

> "Pi can read, change, and execute files with the permissions of the account
> that started it, **and it does not ask for approval before every tool call**.
> Extensions, package installers, language servers, and other child processes run
> with those same permissions unless an operating-system or virtualization
> boundary restricts them."

…then explicitly refuses to overclaim: *"Watching the transcript, using project
trust, and reviewing changes do not create a security boundary."*

**Project trust is the only built-in gate.** `trust-manager.ts:30-39` lists what
triggers it: `.pi/{settings.json, mcp.json, extensions, skills, prompts, themes,
SYSTEM.md, APPEND_SYSTEM.md}` or `.agents/skills` in cwd-or-any-ancestor
(`:186-207`, which walks up and explicitly ignores the user's own
`~/.agents/skills` so cwd=`$HOME` does not self-prompt).

Resolution order (`docs/security.md:60-67`): `--approve`/`--no-approve` →
extension `project_trust` handler (first yes/no owns it) → nearest saved decision
in `~/.pi/agent/trust.json` (`findNearestTrustEntry:46-59`) →
`defaultProjectTrust` (default `"ask"`). Non-interactive: `"always"` loads,
`"ask"`/`"never"` skip.

**Documented gap, stated in the doc**: *"Pi reads the project `sessionDir` setting
while selecting or creating a session, **before it resolves project trust**… it
cannot undo that initial session-directory lookup."* Also: AGENTS.md/CLAUDE.md
load **regardless of trust**.

**Isolation is delegated, not shipped** — `docs/containerization.md` gives a
3-row table and `examples/extensions/sandbox` (uses
`@anthropic-ai/sandbox-runtime`) + `examples/extensions/gondolin` as *reference*
implementations. `ToolAnnotations` is documented as a permission **substrate**,
not a gate.

### Review — sandbox & permissions

**Neither side has per-tool approval.** That is a shared gap, not a difference —
and pi's is a documented decision while pi-go's is an unstated one.

**pi-go has the better actual boundary**: a `(deny default)` Seatbelt profile with
network narrowed to 443 + 11434 is a real OS-enforced restriction. pi
documents three containerization patterns and ships none.

**pi-go's naming is the problem.** `internal/tools/sandbox.go` carries a
"SECURITY MODEL" comment and 541 lines of real `os.Root` containment — and
`bash` borrows only `sb.Dir()` as a cwd. A reader skimming `sandbox.go` concludes
a boundary exists for process execution where none does. The DeepSeek harness
review in this repo already flagged this as "the gap that matters most". It
should be fixed by **documenting** the actual boundary in the file's header, not
by building the missing enforcement — pi-go already has the enforcement
(`cmd/pi-sandbox`), it is just opt-in and unnamed from the tool layer.

**The ledger is worth keeping and worth explaining.** It is not a security
control and should not be described as one — it prevents the model from
overwriting a file it has not fully seen, which is a *correctness* property.

**pi's trust model is the one genuinely missing control.** pi-go installs
git-based plugins (`internal/plugin`), discovers project agents
(`internal/subagent/agents.go`), loads `.claude/skills` and `.cursor/skills` from
ancestor directories, and reads project `.pi/` config — all with no trust gate.
A hostile repo can ship a `.pi-go/agents/` agent and pi-go will merge it
unconditionally.

Two things would close it, and both are small:

1. A `defaultProjectTrust: ask|always|never` setting plus a `--approve` flag.
2. Run `internal/audit` on project-discovered agents and skills, not just on
   user-level skills. The scanner already exists and already blocks the
   dangerous Unicode classes — it is simply not applied on the project path.

pi-go is **stronger than pi** on the OS boundary and **weaker than pi** on the
project-trust boundary, and pi is **stronger on documentation of its own posture**
than both. `docs/security.md` is the single most valuable artifact in this
domain, and it is prose, not code.

## Review summary — tools, extensions, security

**pi-go leads on**: tool count (29 vs 11), LLM-robustness layer (param aliases,
coercion, strict/lenient schema split), output compaction (9 pipelines),
result dedup (27% measured), read-before-write ledger, blocking prompt-injection
scanner, and the OS sandbox profile.

**pi leads on**: the extension surface by a wide margin (36 events + ~28
registration methods + 30 UI hooks vs 2 shell hooks that cannot block); MCP
conformance discipline; codemode (a real QuickJS sandbox); tool-exposure tiers;
`edit` batching; truncation strategy; and documented security posture.

**pi-go's three concrete fixes in this domain, in order:**

1. **Make `before_tool` hooks able to deny.** The ADK contract supports it;
   `hooks.go:156` discards it. ~10 lines, and every existing hook becomes a real
   gate immediately.
2. **Truncate bash output to the tail, and spill the full output to a 0600 temp
   file.** pi-go currently keeps the head and discards the error.
3. **Add a project-trust gate** for `.pi-go/agents/`, `.claude/skills`,
   `.cursor/skills`, and `.pi/` config, and run the existing `internal/audit`
   scanner on the project path.

**What not to attempt**: matching the 30-hook UI extension API. pi-go's TUI is a
single `tea.Model`; making it extensible is a larger project than the rest of the
extension work combined.