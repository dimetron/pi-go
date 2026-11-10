# Upstream `pi` (earendil-works/pi) vs pi-go — context management

> Status: **research, not implemented**
> Researched: 2026-11-07
> Upstream: `tmp/pi` @ `e61f41f64` (2026-11-06 22:58 UTC), `earendil-works/pi`
> Related: [`../008-short-context-discipline/`](../008-short-context-discipline/README.md),
> [`../007-codex-context-compaction/`](../007-codex-context-compaction/README.md)
> Scope: **context/compaction only.** Not a general feature gap analysis.

## Why this exists

`tmp/pi` is the TypeScript harness pi-go was modelled on, checked out at a HEAD
one day old. It is not a competitor to benchmark against — it is the design source
for pi-go's compaction, and reading it resolves four questions that the
short-context spec left open by showing a working answer to each.

Two things need stating up front, because they change how to read this document:

1. **Upstream solved the problem the spec called "Proposal C" — and did not do it
   the way the spec suggested.** The spec proposed spilling a transcript to a file
   and telling the model to `grep` it. Upstream instead keeps every entry forever
   and makes *context* a projection. That is a stronger design, and it is also why
   upstream's version needs no retention policy.
2. **Upstream's thresholds do not map to pi-go's**, because the denominators
   differ. Its `reserveTokens: 16384` looks close to pi-go's `KeepUserMessageTokens:
   20000` and has nothing to do with it.

## Upstream's compaction, end to end

### The entry model is append-only

Sessions are JSONL where entries form a tree via `id`/`parentId`
(`docs/session-format.md`). Three entry types matter:

| Entry | Role |
|---|---|
| `message` | a stored `AgentMessage`; never mutated |
| `compaction` | a summary plus `firstKeptEntryId`; **appended**, not substituted |
| `context_edit` | append-only edit of an earlier entry; `replacement: null` **omits it from model context** |

The last one is the design insight. From `docs/session-format.md:141`:

> "Append-only edit of one earlier context-producing entry. It changes only
> future model context; the target entry and its metadata remain unchanged in raw
> history, UI, exports, and session accounting."

Context is built by walking the leaf to root, honouring the latest `compaction`,
then applying the latest `context_edit` per target
(`docs/session-format.md:232-248`). **The raw transcript is never rewritten.**
`docs/sessions.md:50` states it plainly: "Compaction adds a summary and keeps
recent messages. It does not delete the original session entries."

### The trigger is a reserve, not a percentage

From `packages/coding-agent/src/core/compaction/compaction.ts:269`:

```ts
export function shouldCompact(contextTokens, contextWindow, settings): boolean {
  if (!settings.enabled) return false;
  return contextTokens > contextWindow - settings.reserveTokens;
}
```

`contextTokens` is *total* context. Defaults (`compaction.ts:126-130`):

| Setting | Default | Meaning |
|---|---|---|
| `reserveTokens` | 16,384 | free space below which compaction starts |
| `keepRecentTokens` | 20,000 | recent tokens retained unsummarized |
| `compaction.modelOverrides` | none | per-`provider/modelId` token settings |

Per-model overrides resolve independently: model override → global setting →
built-in default (`docs/settings.md:76-80`). **This is the mechanism pi-go lacks
entirely** — see "What pi-go should take" below.

### The cut point works backwards by token budget

`compaction.ts:431-472`: walk back through the projection accumulating token
estimates until `>= keepRecentTokens`, cut there. Valid cut points are user
messages, assistant messages, bash-execution messages, and custom messages —
**never tool results**, which must stay with their call.

When a single user-message span exceeds `keepRecentTokens`, the cut lands inside
it at an assistant message ("split user-message span", `docs/compaction.md:100`)
and two summaries are generated and merged: history summary + turn-prefix
summary. The turn-prefix prompt (`compaction.ts:929`) is `## Original Request` /
`## Progress So Far` / `## Context Needed to Continue`.

### Repeated compaction includes prior survivors

`docs/compaction.md:76`:

> "On repeated compactions, the summarized span starts at the previous
> compaction's kept boundary (`firstKeptEntryId`), not at the compaction entry
> itself, falling back to the entry after the previous compaction if that kept
> entry cannot be found in the path. ... This preserves messages that survived
> the earlier compaction by including them in the next summarization pass as
> well."

This is the answer to the recursive-loss problem. pi-go's `BuildSummarizedEvents`
rebuilds from the *current* event list each time; whether the previous summary's
surviving messages are re-included depends on where the split falls, not on a
recorded boundary. Upstream makes the boundary explicit and *grows the summarized
span* only forward from it.

### The summarization prompt is a fixed schema

Two prompts, both schema-enforced. `compaction.ts:507` (`SUMMARIZATION_PROMPT`)
asks for *"this EXACT format"* and gives it:

```
## Goal
## Constraints & Preferences
## Progress
### Done
### In Progress
### Blocked
## Key Decisions
## Next Steps
## Critical Context
```

The update variant (`:577`) adds rules: **PRESERVE** all existing information,
**ADD** new, **UPDATE** progress (move items to Done), **PRESERVE** exact paths,
function names, error messages. It is fed `<previous-summary>` tags.

A separate system prompt (`utils.ts:162`) keeps the summarizer in its lane:

> "You are a context summarization assistant. ... Do NOT continue the
> conversation. Do NOT respond to any questions in the conversation. ONLY output
> the structured summary."

### File operations are tracked and appended

`compaction.ts:1043-1055`:

```ts
const { readFiles, modifiedFiles } = computeFileLists(fileOps);
summary += formatFileOperations(readFiles, modifiedFiles);
```

rendering as `<read-files>` / `<modified-files>` blocks (`utils.ts:78-88`), and
persisted in `details` so the next compaction merges them
(`compaction.ts:73-78`). **This is pi-go's "absolute paths of files read or
modified" — but accumulated across compactions rather than re-derived.**

### Failure handling is explicit

`getSummarizationFailure` (`compaction.ts:585`) refuses to persist a summary when
`stopReason === "error"` or `"length"`:

> "A length stop contains partial text and must not become a session checkpoint."

pi-go's `summarizeWithLLM` discards a partial answer on error (the comment says so
at `compaction.go:186-190`), but the two paths are not symmetric — worth checking
whether a length stop is treated as complete there.

## Side-by-side

| Concern | Upstream `pi` | pi-go | Verdict |
|---|---|---|---|
| Trigger | `contextTokens > window - reserveTokens` (16K reserve) | `pct(bodyTokens) >= 90` | **pi-go's is the weaker one** — body excludes the first request, so the trigger can land past the window |
| Denominator | total context tokens | body growth after first request | pi-go under-counts by design |
| Retained recent | backwards token walk, `keepRecentTokens: 20000` | `KeepRecentEvents: 10` **events** | neither is wrong; events vs tokens is a real difference |
| Cut safety | never cut at tool results | `advanceToCleanBoundary` keeps call/response together (`autocompact.go:143`) | **pi-go has this** — good |
| History durability | append-only entries + `context_edit` omissions | `rewriteEvents` **overwrites** `events.jsonl` | **upstream is strictly better** |
| Retrieval after compaction | raw entries remain; exports/UI/billing/history-search can still read them | gone | upstream is strictly better |
| Repeated compactions | span grows from `firstKeptEntryId` | rebuilt from current list | upstream is more predictable |
| Summary schema | fixed 8-section schema, two prompts (create/update) | free-form 5-bullet list | upstream is more structured; note the paper's App. G is closer to pi-go's free-form |
| Prior summary handling | `<previous-summary>` + PRESERVE/ADD/UPDATE rules | previous summary rides inside the transcript being summarized | **upstream is better**; this is the paper's recursive-loss mechanism |
| File tracking | accumulated `read-files`/`modified-files`, persisted in `details` | absolute paths, prompt-only, per-compaction | upstream is better |
| Partial-summary guard | refuses to persist on `length` | discards on error; length path unverified | verify pi-go |
| Per-model thresholds | `compaction.modelOverrides` keyed `provider/modelId` | global only | **pi-go has nothing here** |
| Branch summarization | on `/tree` navigation, with its own prompt | branch switching exists (`branch.go`) with **no summarization** | gap |
| Compaction hooks | `session_before_compact`, `session_after_*`, `context_edit` from extensions | none | gap; matches `007-codex-context-compaction` deferrals |
| Tool-output caps | 2000 lines / 50KB, `truncateHead`/`truncateTail` | 256KB safety net + per-tool pipelines | different layers; not comparable |
| Manual command | `/compact [instructions]` | `/compact` | upstream takes focus instructions |
| Overflow recovery | provider overflow → one compact-and-retry | not found | verify pi-go |

## What pi-go should take

Ordered by what the evidence supports, not by what is easiest. **None of the
four overlap with the short-context spec's A/B/C except where noted.**

### 1. Per-model compaction overrides — highest value, smallest scope

`compaction.modelOverrides` keyed by `provider/modelId` solves the problem the
short-context spec could not solve: the paper shows Qwen 3 Coder Next collapsing
at 16K/12K while MiniMax M2.7 holds, so one global threshold is wrong for a
heterogeneous-model harness. pi-go drives 9 providers through one compactor and
has **no per-model setting at all**.

Shape: extend `AutoCompactConfig` with an override map, resolve
override → global → default, exactly as `docs/settings.md:76-80` describes. This
is a config-plumbing change, not a compaction-mechanism change, and it makes the
spec's Proposal B safe: you can lower the default without imposing it on verbose
models.

**This is the single change I would take first**, and it is the one upstream has
that none of the spec's proposals address.

### 2. Fix the denominator before touching thresholds

`meter.go:51-59` computes `BodyTokens = lastPromptTokens - cachePrefixTokens`
where the prefix is whatever the *first* request cost. Upstream's
`shouldCompact` uses total context against `window - reserve`. The reserve
formulation is strictly better because it cannot fire past the window by
construction.

This is a bug in pi-go independent of any paper — the arithmetic in the
short-context spec's compliance section shows a 60K prefix pushing the 90% trigger
to 240K on a 200K window. Adopting `window - reserveTokens` would fix it without
changing `ShedPercent`/`SummarizePercent`.

### 3. Make compaction append-only — this is the real answer to Proposal C

Upstream never deletes. The short-context spec proposed spilling a transcript to
`history-N.jsonl` and pointing the model at it; upstream's `context_edit` shows
the better shape: **keep the entries, append an omission, let the projection
decide what the model sees.**

For pi-go that means `rewriteEvents` becomes wrong by construction. The cheaper
first step is not a rewrite — it is to stop *overwriting* and start *appending*:
a compaction record plus a projection that skips summarized events. That reuses
the existing event log format, needs no new file type, and makes the `history-N`
spill unnecessary.

Caveat: this is a real architectural change to `internal/session/store.go`, which
`specs/research/007-codex-context-compaction` and the memory specs also touch.
It deserves its own design doc, not a slice of this one.

### 4. Structured summary schema + `<previous-summary>`

Upstream's 8-section schema with explicit PRESERVE/ADD/UPDATE rules is a concrete
instance of what the paper's Appendix A argues for. It is also the thing the
short-context spec's Proposal A reaches for, but upstream supplies the exact
wording, which is cheaper than inventing it.

Note the tension worth preserving: the paper's winning structure includes
"completion requirements — when to consider the task complete", which upstream's
schema **lacks** (`## Next Steps` is not a completion condition). So upstream is
not a complete template. Taking its schema and adding one section is better than
taking either alone.

## What not to take

- **`keepRecentTokens` by token walk vs `KeepRecentEvents` by count.** Upstream's
  is more principled but pi-go's event indices are load-bearing for the shed
  boundary and `advanceToCleanBoundary`. Changing this is churn without evidence.
- **The 16,384 / 20,000 defaults.** They are tuned for total-context accounting.
  Copying them onto pi-go's body-growth accounting would silently change behaviour.
- **Branch summarization.** Real gap, but it is a feature, not a fix, and
  `branch.go` has no summarization to extend. Worth its own spec.
- **Extension hooks (`session_before_compact`).** Already tracked as deferred in
  `007-codex-context-compaction`. Nothing here changes that.

## Correction to the short-context spec

The spec's Proposal C — "spill the pre-compaction transcript to `history-N.jsonl`
and tell the resuming model it may `grep` it" — is superseded. Upstream shows the
better mechanism, and it requires no spill because the source is never destroyed.
The spec's open question 3 asked whether C should write to the session dir or a
temp dir; the answer from upstream is **neither** — stop rewriting the entry log.

I have not edited that spec's proposals. Doing so would rewrite a document that
Codex reviewed against its current text. Flagging it here instead; the two specs
should be read together.

## What was checked and what was not

Verified by reading upstream source at `e61f41f`:

- `shouldCompact`, `reserveTokens`/`keepRecentTokens` defaults, `modelOverrides`
  doc — `compaction.ts:126-130,269`, `docs/settings.md:69-80`
- Cut-point walk and split-turn handling — `compaction.ts:431-472`,
  `docs/compaction.md:100`
- Both summarization prompts, the system prompt, the failure guard —
  `compaction.ts:507,577,585,929`, `utils.ts:162`
- File-operation tracking and rendering — `compaction.ts:1043-1055`,
  `utils.ts:78-88`
- `context_edit` semantics and append-only claims — `docs/session-format.md:141-147,232-248`,
  `docs/sessions.md:50`
- Repeated-compaction boundary rule — `docs/compaction.md:76`

**Not verified:** whether pi-go treats `stopReason: "length"` as a complete
summary (the code path was not traced end to end); whether pi-go has any
overflow-recovery attempt; how upstream's `truncate.ts` caps interact with its
compaction cut (they are independent layers); and nothing about upstream's
provider layer, tool layer, or TUI, which were out of scope.

No pi-go code was changed. `tmp/pi` is a read-only reference checkout and was not
modified.
