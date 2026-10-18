# 07 — Gaps & Recommendations

Ranked by value-per-effort. Each item names the exact file, the exact change, and
what pi does that pi-go does not.

## How to read this

**Tier 1** — correctness or data-loss risks. Fix regardless of feature plans.
**Tier 2** — cheap wins with clear user-visible value.
**Tier 3** — structural work that changes what pi-go can become.
**Tier 4** — long-tail capability, only worth doing after Tier 3.

Effort estimates assume familiarity with the pi-go codebase.

---

## Tier 1 — correctness and data-loss

### 1.1 Torn JSONL tail permanently poisons a session

**File**: `internal/session/store.go:1113-1136` (`readEvents`)
**pi does**: `packages/durable/src/storage/jsonl/storage.ts:797-803`

`readEvents` splits on `\n` and unmarshals per line. **Any unparseable line
returns an error and the whole load fails.** A partial final line from a
`SIGKILL` mid-write makes the session unrecoverable.

```ts
// pi's fix, jsonl/storage.ts:797-803
let completeSize = bytes.length;
if (completeSize > 0 && bytes[completeSize - 1] !== 0x0a) {
  completeSize = bytes.lastIndexOf(0x0a) + 1;
  const truncated = await fs.truncateFile(path, completeSize, context);
  if (!truncated.ok) throw errorFromFile(`torn-line truncation of ${name}`, truncated.error);
}
```

Also: invalid UTF-8 in a *complete* line throws a loud corruption error rather
than silently skipping.

**Change**: detect a missing trailing `\n` on load; truncate the in-memory view to
the last complete line; if a *complete* line fails to parse, return an error that
names the line number rather than a bare unmarshal failure.

**Effort**: ~1 hour. **Value**: converts a permanently-broken session into a
truncated one. This is the highest value-per-line change in the entire report.

### 1.2 No session schema version

**File**: `internal/session/store.go:48` (`Meta`), `store.go:1084` (`readMeta`),
`store.go:637` (`SetSessionVersion`)

`Meta.Version` records the pi-go **build string**, not a schema version. There is
no `migrate*` function anywhere in `internal/session/`. Any future field change is
a silent behavioral break for every existing session.

**pi does**: `CURRENT_SESSION_VERSION = 3` (`session-manager.ts:41`) with ordered
`migrateV1ToV2`/`migrateV2ToV3` (`:337`), called on load with a file rewrite
(`:1093`). Per-document: `checkRecordVersion` throws both directions and
`migrate(value, fromVersion)` runs lazily on read (`documents.ts:175`, `:199`).

**Change**: add `"schemaVersion": 1` to `Meta`; add a `migrateMeta(m Meta, from
int) (Meta, error)` dispatch; call it from `readMeta`. Write the new version on
save.

**Effort**: ~2 hours. **Value**: makes every future session-format change safe.

### 1.3 Branch heads are integer indices

**File**: `internal/session/branch.go:15`, updated at `internal/session/store.go:477`

`BranchInfo.Head` is a bare `int` event index, updated on every append.
Compaction rewrites the event list (`store.go:1325`), so after any summarize
pass the head pointer is wrong.

**Change**: key `BranchInfo` on the head event's ID. ADK events have stable
identifiers; storing the ID instead of the index makes branches robust to
compaction, insertion, and truncation.

**Effort**: ~4 hours (touches branch create/switch/append). **Value**: removes a
class of silent branch corruption.

### 1.4 Thought signatures not dropped on model switch

**File**: `internal/provider/openai_thought_signature.go`

`oaiThoughtSignature(rawJSON)` reads `extra_content.google.thought_signature` from
tool calls. Nothing strips it when the model changes mid-session.

**pi does**: `transform-messages.ts:131-133`:
```ts
if (!isSameModel && toolCall.thoughtSignature) delete ...
```

Switching from Gemini 3 to an OpenAI model mid-session leaves a signature on a
tool call the new model has never seen.

**Change**: in the model-switch path, strip signatures whose provider does not
match the new model.

**Effort**: ~2 hours. **Value**: fixes malformed requests after a `/model` change.

### 1.5 No 1h cache-write accounting

**File**: `internal/provider/pricing.go`, `internal/provider/anthropic.go:590-591`

Anthropic's 1h cache tier bills at 2× input. pi-go has no 1h tier, so cost
reported for a user on that tier is wrong.

**pi does**: `models.ts:1193-1211`
```ts
const longWrite = usage.cacheWrite1h ?? 0;
const shortWrite = usage.cacheWrite - longWrite;
usage.cost.cacheWrite = (rates.cacheWrite * shortWrite + rates.input * 2 * longWrite) / 1000000;
```

Plus `ttl` on the cache-control marker (`anthropic-messages.ts:91`) and a
declarative `promptCache?` retention field (`types.ts:116`).

**Change**: add `cacheWrite1h` to the usage struct; add a 1h rate to the pricing
table; compute `cacheWrite` as the weighted blend.

**Effort**: ~4 hours. **Value**: correct spend reporting on a real tier.

### 1.6 `make fetch-models` can silently produce an empty catalog

**File**: `internal/provider/catalog.go:100-130` (`RefreshCatalog`),
`internal/provider/modeldata/README.md:61-63`

If a fetch returns `{}` or a partial payload, the XDG cache is overwritten and
`CatalogFor` unions it with the embedded snapshot — so the model list silently
loses entries. The README documents a **manual** step: "check for `skip` lines".

**pi does**: `generate-models.ts --strict` turns 11 soft failures into hard
errors (`:1294`, `:1318`, `:1324`, `:1339`, `:1427`, `:2660`, `:2669`, `:2701`).

**Change**: validate before writing — non-empty result, and every model has a
non-empty `id`. On failure, leave the existing cache intact and return an error.
This inverts the current behavior, which is fail-open.

**Effort**: ~3 hours. **Value**: closes a silent data-loss path in the catalog.

---

## Tier 2 — cheap wins with visible value

### 2.1 Make `before_tool` hooks able to deny

**File**: `internal/extension/hooks.go:152-157`

The ADK contract supports blocking — returning non-nil or an error
(`base_flow.go:1421-1434`) — but pi-go returns `nil, nil`
**unconditionally**, even when the hook command fails:

```go
// hooks.go:152-157
// Non-fatal: log and continue
hookLogf(...)
return nil, nil
```

**pi does**: `ToolCallEventResult{block?, reason?, terminate?}`
(`types.ts:1402-1411`) plus in-place `event.input` mutation for argument
rewrites.

**Change**: if the hook command exits non-zero, return an error. Turn every
existing `before_tool` hook into a real gate at zero config cost.

**Effort**: ~1 day (needs a decision on the failure contract: hard-fail vs
configurable). **Value**: the single highest-leverage change in the extension
domain. It converts an advisory mechanism into an enforceable one.

### 2.2 Tail-truncate bash output and spill to a 0600 temp file

**File**: `internal/tools/truncate.go:13-24`, `internal/tools/bash.go:146`

`truncateOutput` keeps the **head** and appends `\n... (output truncated)`. For
`bash`, the discarded tail is usually the error and the summary.

**pi does**: `bash` uses `truncateTail` and spills the full output to a temp file
(`bashOutputSchema:56-63`). MCP uses `truncateMiddle` with a `0o600` temp file
(`mcp/tools.ts:127-138`, `:65-70`) and the comment *"Results can carry private
data, so only the user may read the file"*.

**Change**: make truncation direction per-tool. `bash` → tail. `read`/`grep` →
head. Any spill path must create the file `0o600`.

**Effort**: ~1 day. **Value**: the error message is currently the thing you lose.

### 2.3 Gate project-discovered agents and skills on trust

**File**: `internal/subagent/agents.go:261-307`, `internal/extension/skills.go:473-513`

`DiscoverAgents` merges project agents unconditionally, walking **up** from cwd
looking for `.pi-go/agents/`. Skills are discovered from `.pi-go/skills`,
`.claude/skills`, and `.cursor/skills` in every ancestor directory. A hostile
repository can ship either and pi-go will use it.

**pi does**: `defaultProjectTrust: ask|always|never` plus `--approve`/`--no-approve`;
interactive runs prompt via `confirmProjectAgents` + `ctx.isProjectTrusted()`
(`subagent/index.ts:519-530`).

**Change**:
1. Add `defaultProjectTrust` to `Config`, with `--approve`/`--no-approve` flags.
2. When trust is `ask` and there is a UI, prompt before loading project agents or
   skills. Non-interactive: `always` loads, `never` skips.
3. Run `internal/audit` on project-discovered skills — the scanner already exists
   and already blocks Unicode tag chars, BiDi overrides, and Variation Selectors
   as critical (`internal/audit/chars.go:38-61`). It is currently applied only on
   the user path.

**Effort**: ~2 days. **Value**: closes the most plausible hostile-input vector in
pi-go. The scanner is already written; this is plumbing.

### 2.4 Wrap every frame in synchronized output (CSI 2026)

**File**: `internal/tui/tui.go:1525` (`View`)

**pi does**: `\x1b[?2026h` / `\x1b[?2026l` around every emit path
(`tui-alt-screen.ts:71-72`; main-screen `:280`, `:302`, `:403`, `:435`, `:460`,
`:567`). The terminal buffers the frame and presents it atomically, so a
multi-line diff never shows as a partially-updated screen.

**Change**: emit the begin/end sequences around the composed frame in `View()`.
Bubbletea passes `View` output through to the terminal unmodified.

**Effort**: ~2 hours. **Value**: removes partial-frame tearing on slow terminals.
Cheapest visible TUI improvement available.

### 2.5 Multiline input with grapheme segmentation

**File**: `internal/tui/input.go:73` (wraps `bubbles/v2 textinput`)

pi-go has single-line input. No multi-line prompt, no undo, no grapheme-aware
cursor motion.

**pi does**: `components/editor.ts` (2,472 LOC) — multiline
(`shift+enter`/`ctrl+j`), kill ring with yank-pop, `structuredClone` undo stack,
`Intl.Segmenter` for graphemes and words, CJK-aware wrapping via `cjkBreakRegex`,
and **paste-marker atomicity** (`segmentWithMarkers()` `:48-101`): graphemes
inside `[paste #N +123 lines]` markers merge into single atomic segments, so a
collapsed 5,000-line paste moves and deletes as one token.

**Change**: implement a multiline input model. Go's `golang.org/x/text` provides
the grapheme segmentation Bubble Tea does not; the paste-marker trick is
~50 lines once segmentation exists.

**Effort**: ~1 week. **Value**: the largest day-to-day UX gap. The paste-marker
idea specifically is the kind of detail that only matters after real use, and it
is cheap to copy.

### 2.6 Back up the branch before `MergeBack`

**File**: `internal/subagent/worktree.go:615` (`MergeBack`), `:500`
(`CreateBackupBranch`)

`MergeBack` aborts the merge on conflict, so a subagent's work is lost on
conflict. `CreateBackupBranch` already exists and already survives `Cleanup`.

**Change**: call `CreateBackupBranch` before attempting the merge. On conflict,
leave the branch and report where it is rather than discarding.

**Effort**: ~2 hours. **Value**: a conflicted subagent merge currently loses the
work; with the backup branch it is one `git diff` away.

### 2.7 Accept `tools: [a, b]` array form in agent frontmatter

**File**: `internal/subagent/agents.go:183` (`parseAgentFrontmatterLine`)

pi-go accepts only the comma-string form. Every other agent-definition format in
the ecosystem uses the array form.

**pi does**: `parseToolList` (`agents.ts:53-61`) accepts both.

**Change**: in `parseAgentToolList`, detect a leading `[` and split on commas
after stripping it.

**Effort**: ~30 minutes. **Value**: portability with agent definitions written
for other tools.

### 2.8 Write `docs/config.md` and `docs/security.md`

**Files**: new `docs/config.md`, `docs/security.md`

pi-go's `config.json` is **27 top-level + ~50 nested keys** and is documented only
in `internal/config/config.go` comments. pi ships `settings.md` documenting every
setting with type, default, and purpose.

`internal/tools/sandbox.go` carries a "SECURITY MODEL" comment next to 541 lines
of real `os.Root` containment — while `bash` borrows only `sb.Dir()` as a cwd. A
reader concludes a process-execution boundary exists where none does.

**pi does**: `docs/security.md` states the posture plainly — *"Pi can read,
change, and execute files with the permissions of the account that started it,
and it does not ask for approval before every tool call"* — and explicitly refuses
to overclaim: *"Watching the transcript, using project trust, and reviewing
changes do not create a security boundary."*

**Change**: two documents. `config.md` tables every key. `security.md` states
what `cmd/pi-sandbox` enforces, what `os.Root` enforces, what the ledger enforces
(a correctness guard, not a security control), and what nothing enforces.

**Effort**: ~1 day. **Value**: pi-go has the better boundary and the worse
description of it. `security.md` is the single most valuable artifact in this
domain on either side, and it is prose.

---

## Tier 3 — structural work

### 3.1 A typed in-process extension interface

**Files**: new `internal/ext/` package; `internal/tools/registry.go:302`
(`coercingTool`) as the wrapping point

pi-go has no extension API. The nearest analogue for an external process is
`internal/plugin`, which distributes *configuration* (Claude-Code-compatible
marketplace), not code.

**pi does**: `ExtensionAPI` (2,245-line type surface, `types.ts:1519+`) with ~28
registration methods and 36 events, loaded via `jiti`, distributed via npm/git/local
(`core/package-manager.ts`, 2,459+ lines).

**Recommendation**: do not attempt to match pi's surface. A minimal Go
interface that captures the capability, not the API:

```go
// Proposal — deliberately minimal.
type Extension interface {
    Name() string
    Tools() []Tool                              // may be nil
    BeforeTool(ctx, name string, args map[string]any) (Verdict, error)
    AfterTool(ctx, name string, res map[string]any) (map[string]any, error)
}
type Verdict struct{ Allow bool; Reason string }
```

Registered in-process (a compiled Go extension must be linked in, or shipped as a
separate binary speaking a protocol — the latter is what `pi-sandbox`'s launch
model already does for the whole process).

**Effort**: ~2 weeks for the interface + `BeforeTool`/`AfterTool`.
**Value**: makes 2.1 and 2.2 expressible in-process, and is the precondition for
every other extension feature.

### 3.2 Compaction that records what it dropped

**Files**: `internal/session/compaction_shed.go:316`, `internal/session/store.go:1325`

`rewriteEvents` overwrites `events.jsonl` atomically. The pre-compaction event
list is gone: no record, no undo, no branch.

**pi does** two things pi-go has neither of:

1. A `compaction` entry recording `firstKeptEntryId` and `tokensBefore` —
   append-only, so raw entries stay on disk forever.
2. `context_edit` (`session-manager.ts:175`) — an append-only record that
   *replaces or nulls an entry's context contribution* without touching the entry
   itself. `buildSessionProjection` (`:543`) then reconstructs the model-visible
   message list from the log.

pi-go's tool-result stubs are already projection-only in spirit — they preserve
call/response pairing without deleting anything. They just are not persisted as a
projection.

**Change, staged**:
- **Minimal**: write a marker event (`Author:"system"`) recording
  `eventsDropped` and `tokensBefore` before `rewriteEvents`. Cheap, and a
  transcript can at least explain itself.
- **Correct**: adopt `context_edit` — persist stubs as entries that override
  projection rather than rewriting history. This is the structurally right answer
  and makes compaction undoable.

**Effort**: ~1 day minimal, ~2 weeks correct. **Value**: the correct version also
fixes 1.3 (branch heads) as a side effect, since projection changes no longer
move event indices.

### 3.3 Convert provider dispatch from a switch to a per-model API table

**File**: `internal/provider/provider.go:725-776`

`NewLLM` is a 10-arm switch on `info.Provider`. Adding a provider is a code
change, and a provider that is not OpenAI-compatible is genuinely new work.

**pi does**: the wire API is a property of the **model** (`model.api`), so one
provider can serve several wire protocols — which is why `minimax` runs
Anthropic-wire on an OpenAI-shaped host, and why `openai-codex` is both a provider
and a distinct api id. 42 providers across 10 wire APIs.

pi-go already does this *narrowly* and per-route:
`opencodeGoModelCatalog` (`opencode.go:69-79`) and `GatewayRoutesToGemini`
(`agentgateway.go:52-59`).

**Change**: replace the switch with a registry:

```go
// Proposal — shape, not final.
type ProviderSpec struct {
    Name       string
    APIModels  map[string]string        // model family -> wire api
    BaseURL    string
    EnvVar     string
    Build      func(ctx, Spec, ModelOpts) (model.LLM, error)
}
```

with `APIModels` covering `"chat"`, `"responses"`, `"messages"`,
`"completions"`, `"gemini"` — which is exactly the vocabulary pi-go's
`opencodeGoModelCatalog` already uses.

**Effort**: ~2 weeks. **Value**: converts ~30 OpenAI-compatible providers from
code changes into data, and generalizes the per-route special cases. Leaves
Bedrock and Vertex as genuine new wire implementations.

### 3.4 A tool-exposure tier system

**File**: `internal/tools/registry.go:92-148`

pi-go has 29 tools, **all declared to the model unconditionally**. The 7 LSP
tools and 3 memory tools cost declaration context in every session that never
touches them.

**pi does**: `ToolExposure = "direct" | "model-only" | "codemode" | "deferred" |
"hidden"` (`types.ts:493-510`) decouples what the model **sees** (`declared`),
what other tools can **invoke** (`callable`), and how the model **discovers** it.
`prepareLoadout` (`:615`) shrinks the declared surface without deactivating, and
survives `/tree` and resume (`:553-558`).

pi-go already has the right hook: the compactor works per-tool via
`CompactResult{CompactWrite{Key, Value}}`, and the model-facing declaration is
swapped independently of the runtime schema (`coercingTool.Declaration()`,
`registry.go:319-331`). A declaration filter is a natural extension of that split.

**Change**: add `Exposure` to each tool; filter `Declaration()` by the active set;
default the LSP and memory tools to `deferred`.

**Effort**: ~1 week. **Value**: direct context savings in the majority of
sessions.

### 3.5 Add a `Replay` policy to the tool interface

**Files**: `internal/tools/registry.go`, `internal/session/store.go:416`
(`AppendEvent`)

pi-go has no way for a tool to declare "re-running me is fine". `bash`, `write`,
and `edit` are all ambiguous after a crash, and pi-go has no mechanism to
distinguish them from `read`.

**pi does**: `AgentTool.replay?: "never" | "safe"` (`types.ts:487-488`) —
*"Recovery policy for an effect whose durable intent exists but whose outcome is
unknown."* The recovery gate is a logical AND of the stored policy and the
current policy (`durable/src/harness/tool.ts:93-111`), so upgrading a tool
retroactively makes past interruptions replayable.

**Change**: add `ReplayPolicy() ReplayPolicy` to the tool interface
(`Never`/`Safe`). Nothing consumes it yet — that is Tier 3.6 — but it is the
declaration that makes correct recovery expressible.

**Effort**: ~3 hours for the interface and the per-tool declarations.
**Value**: precondition for 3.6.

### 3.6 Journal tool intent, and fail unreconciled tool calls on load

**Files**: `internal/tools/registry.go:302` (`coercingTool`),
`internal/session/store.go:1113` (`readEvents`)

pi-go cannot fully adopt pi's crash recovery without either forking ADK or
wrapping every tool. **The wrapper is feasible and is the natural place** —
`coercingTool` already wraps every tool.

**Change, staged**:
1. In `coercingTool.Execute`, append a durable "tool intent" record
   (`{tool, args, callID}`) before delegating.
2. On session load, scan for any `FunctionCall` with no matching
   `FunctionResponse`, and append a synthetic error result: *"Tool X was
   interrupted and may have partially run"*.
3. With 3.5 in place, re-run when both the recorded and current policy say `Safe`.

Step 2 alone delivers the guarantee pi claims: **the transcript is never left
silently wrong**. The model is told a tool may have partially run instead of
being handed a dangling call.

**Effort**: ~1 week for steps 1–2; step 3 depends on 3.5.
**Value**: the headline gap from §01. Note that pi's own `durable` package is
**not on its production path** — so this design is a reference, not a proven
default. pi-go could ship it where pi has not.

---

## Tier 4 — long-tail capability

Ordered by value, with pi's reference implementation noted.

| # | Capability | pi's implementation | Effort |
|---|---|---|---|
| 4.1 | **MCP conformance suite** | `test/mcp-conformance/run.ts:365` — official suite, 3 protocol versions, committed `baseline.json`, CI job, simulated-browser auth client, known failures documented rather than hidden | ~1 week |
| 4.2 | **MCP server output cap** — pi truncates at 20 KB with `truncateMiddle` + `0o600` spill; pi-go has no MCP output bound | `mcp/tools.ts:127-138` | ~2 days |
| 4.3 | **Session fork / clone** | `session-manager.ts:1815` (`forkFrom`) + `getTree()`; durable goes further with per-document `asOf`/`current`/`initial` fork semantics (`durable/src/session/forks.ts:26`) | ~1 week |
| 4.4 | **Inline images** (Kitty/iTerm2) | `packages/tui/src/terminal-image.ts` (731 LOC) — APC `encodeKitty` `:220`, `encodeITerm2` `:287`, placement-vs-upload deletion split `:283`, LRU 1000 `:346` | ~1 week |
| 4.5 | **Click-to-open hyperlinks** — pi-go emits OSC 8 but never activates it | `getOsc8LinkAtColumn` (`utils.ts:359`), `openUrl` (`tui-alt-screen.ts:186`) | ~3 days (needs 4.6) |
| 4.6 | **Alt-screen / fullscreen mode** — enables transcript search, overlay stack, click-to-open, 3D easter eggs | `tui-alt-screen.ts` (absolute `CSI row;col` addressing `:1737`, `ScrollView`, `LayoutFrame`) | ~2 weeks |
| 4.7 | **Keybinding registry** — 45 named IDs, declaration merging, user overrides, **conflict detection** | `keybindings.ts:229-315`; `rebuild()` `:236`, `getConflicts()` `:283` | ~1 week |
| 4.8 | **HTML session export + gist share + bug report** | `core/export-html/`, `session-share.ts`, `bug-report.ts` | ~1 week each |
| 4.9 | **Remaining providers** — groq, cerebras, together, deepseek, fireworks, nvidia, huggingface, baseten are all OpenAI-compatible and become data after 3.3 | 42 `*.models.ts` shards + generated catalog | ~1 day each |
| 4.10 | **Bedrock / Vertex** — AWS credential chain, ADC, regional endpoints, `bedrock-converse-stream` | `amazon-bedrock` provider (4 auth ids), `google-vertex` (3 auth ids) | ~2 weeks each |
| 4.11 | **Classifiers** — 3 classifier APIs including local llama.cpp | `CLASSIFIER_MODELS` (`models.generated.ts:223`), `api/llama-cpp-classify.ts` (458 LOC) | ~2 weeks |
| 4.12 | **Image generation** | `api/openrouter-images.ts` (198 LOC) + `images-api-registry.ts` | ~1 week |
| 4.13 | **Per-subagent usage line in the card** — `formatUsageStats` shows tokens + cost per task, which is how a user attributes spend | `subagent/index.ts:45-69` | ~2 days |
| 4.14 | **Per-task `cwd`** in the subagent contract | `TaskItem.cwd` (`subagent/index.ts:442-457`) | ~2 hours |
| 4.15 | **Retry config block** — pi-go's retry constants are compiled in | `settings.retry.{enabled, maxRetries, baseDelayMs, maxAgentDelayMs, provider.*}` (`settings-manager.ts:46-52`) | ~3 days |
| 4.16 | **Credential expiry check** — pi-go stores a codex token once and never refreshes | `resolve.ts:113-126` (double-checked refresh); `expiresSoon` | ~3 days |
| 4.17 | **LaTeX rendering** | `latex.ts` (1,506 LOC) + `tokenizeInlineLatex` with 4 false-positive rejections (`markdown.ts:64-77`) | ~3 days (use a Go LaTeX lib) |
| 4.18 | **Per-model thinking-level defaults** | `settings.modelThinkingLevels: Record<"provider/modelId", ThinkingLevel>` (`settings-manager.ts:138`) | ~2 days |
| 4.19 | **Coverage threshold** — neither side has one | — | ~1 day |
| 4.20 | **Lint `scripts/` and `hack/`** — pi-go's shell scripts are unlinted | pi covers `scripts/` with TS-AST analysis | ~1 day |

---

## Do not attempt

Explicitly out of scope, with reasons.

**A 30-hook TUI extension API.** pi-go's TUI is a single `tea.Model` with fixed
sub-models (`ChatModel`, `InputModel`, `StatusModel`, `Sidebar`). Making it
extensible is a larger project than the entire extension API, and the payoff is
small for a single-user coding agent.

**Replacing Bubble Tea with a hand-written differential renderer.** pi-go's cost
profile is markdown rendering, not terminal I/O — `record-pgo` exists because a
live profile attributed ~24% of CPU to `View()`-per-token rendering, and the
FNV-1a render cache (`chat.go:160-197`) already addresses that. Bubbletea's lack
of differential rendering costs some flicker, which CSI 2026 (2.4) largely
mitigates for ~20 lines. A hand-written renderer would be weeks of work and
terminal-compatibility risk for a cosmetic gain.

**Exposing pi-go's compiled binaries as a marketplace.** `internal/plugin`
distributes configuration, which is safe. Distributing *compiled native code*
means a different trust model entirely, and pi-go has no answer for it.

**Matching pi's provider count before changing the dispatch model.** Adding
OpenAI-compatible providers to a switch is easy and each one is a maintenance
commit forever. 3.3 first; then 4.9 becomes a data change.

---

## Sequencing

```
Week 1     1.1  torn-tail repair            (~1h)     ← highest value per line
           1.2  session schema version       (~2h)
           1.4  thought-signature on switch  (~2h)
           2.4  CSI 2026 frame wrapper       (~2h)
           2.7  tools array form             (~30m)

Week 2-3   2.1  before_tool can deny         (~1d)
           1.3  branch heads by event ID     (~4h)
           2.2  tail-truncate + 0600 spill   (~1d)
           1.5  1h cache-write accounting     (~4h)
           2.3  project trust gate           (~2d)
           2.6  backup branch pre-merge      (~2h)
           2.8  docs/config + docs/security  (~1d)
           1.6  strict catalog refresh       (~3h)

Month 2    2.5  multiline input              (~1w)
           3.4  tool exposure tiers          (~1w)
           4.1  MCP conformance              (~1w)
           4.7  keybinding registry          (~1w)
           3.2  compaction record (minimal)  (~1d)

Month 3+   3.3  provider registry            (~2w)
           3.5  replay policy interface      (~3h)
           3.6  tool intent journaling       (~1w)
           3.2  context_edit compaction      (~2w)
           3.1  typed extension interface    (~2w)
           4.9+ remaining providers          (~1d each)
```

The Tier 1 + Tier 2 set is roughly **three weeks** and closes every correctness
risk plus the four cheapest user-visible gaps. Tier 3 is where pi-go's shape
changes, and 3.5 → 3.6 is the sequence that would let pi-go claim something pi
itself does not yet ship by default.

## What pi-go should not copy from pi

Three pi designs that would be net-negative in pi-go:

**`packages/durable`'s document model.** Scope × history × fork semantics on
every document, plus `materializeDocument` version checks in both directions, is
the right answer for a collaborative multi-session server. pi-go is a
single-user CLI writing one session file; `context_edit` alone gets the important
property (non-destructive compaction) at a fraction of the complexity.

**`agent-session.ts` at 4,348 lines.** It is the orchestrator for agent
lifecycle, event subscription with persistence, model/thinking management,
compaction, bash execution, session switching and branching. A Go port of that
file would be the largest single file in the repo and a permanent maintenance
burden. pi-go's separation of `agent` / `session` / `autocompact` / `tui` is
better factored.

**The 30-hook UI extension API**, for the reason in §Do not attempt above.

Two more worth naming:

**pi's provider catalog as generated-and-replace.** pi-go's union-of-three
survives a partial fetch, which matters for a CLI that must work offline. Keep
the union; add pi's validation and drift checking.

**pi's in-memory default credential store.** The interface is right; shipping
in-memory as the default means a user's tokens vanish between runs. pi-go's
0600 `.env` is more durable — add a keychain *provider* rather than replacing
the file with something that forgets.