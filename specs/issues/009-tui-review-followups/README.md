# Issue 009: Deferred Follow-ups from the TUI Review and PR #134 Review

## Summary

A review of `internal/tui` and of PR #134 (eval run harness) produced nine merged
changes and this list of what was deliberately **not** done. Each item below records
what was found, the evidence, why it was left, and what fixing it would cost.

Nothing here is speculative. Every item was found while reading code for a different
purpose and has a file:line citation. Several were found by an agent and then
independently reproduced before being written down; where a claim was *not*
reproduced, it says so.

### What already landed

| PR | Change |
|---|---|
| #135 | Backgrounded commands render as running, not `exit -1` |
| #136 | Tool results bind to their own call ID, on both the live and replay paths |
| #137 | `t.Error` → `t.Fatal` on four nil-check-then-dereference sites |
| #138 | Ollama routes by the model's cloud tag, not by whether an API key is set |
| #139 | One proportional bar and one context estimate instead of four and three |
| #140 | `/context` split into one writer per section (41 → 3) |
| #141 | Tool-result dispatch became an ordered table (34 → 3) |
| #142 | Eval report layer: UTF-8, ordering, latency denominator |
| #143 | Agent loop stops reading `m.cfg` while Update writes it |

### What is left

| # | Item | Kind | Size |
|---|---|---|---|
| 1 | `/run` binds results to the newest empty card of *any* name | correctness | M |
| 2 | `TokenMetrics.CostUSD` is never populated | correctness | S |
| 3 | `looksLikeError` over-matches and feeds the judge | correctness | S |
| 4 | `childBudget` duplicates `subagent.childConcurrency` | drift risk | S |
| 5 | `TestSupervisor_SinkStreamsOutput` flake is unexplained | unknown | ? |
| 6 | `golangci-lint` never lints `e2e`-tagged files | tooling | XS |
| 7 | The `run.go` branch-separator fix is riding inside #134 | process | S |
| 8 | `eval-judge` defaults to a superseded grader model | process | XS |
| 9 | Two dead fields on `model` | cleanup | XS |
| 10 | Popups still live in `tui.go` | cleanup | M |
| 11 | Three small state groupings on `model` | cleanup | M |
| 12 | `View` should take a DTO like `RenderSidebar` does | structure | M |
| 13 | Tests name the `model` struct shape 413 times | test infra | L |
| 14 | `agentState` extraction — **recommended against** | structure | XL |
| 15 | `toolCallSummary` is now the worst function in its file | cleanup | M |
| 16 | The 120-column clip is byte-indexed and duplicated | latent | S |
| 17 | `/context` prints two contradictory totals | UX | S |
| 18 | 112 inline `lipgloss.NewStyle()` calls on the render path | perf/cleanup | M |

---

## 1. `/run` binds tool results to the newest empty card of any name

**Where:** `internal/tui/run.go:656-662`

PR #136 fixed the chat path: a tool result now binds to the card carrying its own
call ID, because a turn routinely issues several calls to the same tool at once and
the old "newest same-named empty card" scan transposed them.

The `/run` subagent event path has the same shape and is **looser** — it fills the
newest empty tool card of *any* name, so a `read` result can land on a `bash` card.

**Why deferred:** the `agentEvent` type carries no call ID. Fixing it means plumbing
one through the subagent event stream, which is a wider change than the one #136
made and touches a file three other branches were editing at the time.

**Fix:** carry the call ID on `agentEvent`, then reuse `matchToolResultCard`
(`agent_loop.go`), which is already a package-level pure function over `[]message`
precisely so it can be shared — the chat and session-restore paths both call it.

---

## 2. `TokenMetrics.CostUSD` is declared, rendered, and never populated

**Where:** declared `internal/eval/metrics.go:619`, rendered `internal/eval/report.go:133`

Nothing in the tree assigns it. "Estimated cost" therefore cannot appear in a real
report — it renders as zero, which reads as free rather than as unmeasured.

**Why deferred:** the fix is a product decision, not a defect repair. Either wire it
to `internal/provider/modeldata/` (which `atif.CostUSD` already uses) or delete the
field. There is also a `feat/token-cost` branch in flight that may already own this.

**Fix:** pick one. If wiring it up, `atif.CostUSD` is the precedent to follow.

---

## 3. `looksLikeError` over-matches, and its output reaches the grader

**Where:** `internal/eval/metrics.go:592`

It matches `"failed"`, `"not found"` or `"error:"` anywhere in a result string. A
successful `grep` that legitimately returns "not found" is counted as a tool error.

This is honestly documented as a conservative heuristic, and as a *metric* that is
defensible. The problem is downstream: these counts feed the LLM judge prompt, so
the grader is told about failures that did not happen and grades accordingly.

**Why deferred:** tightening it changes what the numbers mean and therefore what
past reports compare against. That is the harness owner's call.

**Fix options:** require the marker at the start of the result; exclude tools whose
"not found" is a normal outcome (`grep`, `find`, `ls`); or keep the loose count for
the metric but stop feeding it to the judge.

---

## 4. `childBudget` duplicates `subagent.childConcurrency` with nothing pinning them

**Where:** `internal/eval/metrics.go:398`

It re-implements the halving-with-floor-of-1 rule rather than importing it,
deliberately, "to avoid exporting a production symbol". Reasonable — but the two can
drift silently, and when they do the report misstates `worker_budget` while looking
authoritative.

**Fix:** a guard test in `internal/subagent` asserting the two agree across a range
of inputs. That closes the drift without exporting anything. Requires deciding
whether a test may reach the unexported symbol (it can, in-package).

---

## 5. `TestSupervisor_SinkStreamsOutput` flaked once, unexplained

**Where:** `internal/tools/bash_supervisor_test.go`

Observed failing with `ExitCode = -1` during a repo-wide `go test ./... -count=1`,
i.e. under parallel-package load. Passed 3/3 in isolation for the reporter and 20/20
under `-count=20` when re-checked here.

**What is established:** it is *not* caused by the #135 supervisor change — the
branch it failed on does not contain that change (`grep -c limitsHint` returns 0).

**What is not established:** anything else. A timing-sensitive test that fails only
under load may be a bad test or may be a real scheduling bug in the supervisor. It
is recorded here as unexplained rather than dismissed as flaky.

**Fix:** reproduce under load (`go test ./... -count=5` with `-p` high, or `stress`),
then decide. Do not simply add a retry.

---

## 6. `golangci-lint` never lints `e2e`-tagged files

**Where:** `Makefile` `lint` target — `golangci-lint run ./...`

Build-tagged files are invisible to the default pass. `internal/tui/run_eval_e2e_test.go`
is 709 lines and was the single largest new file in PR #134; neither the lint target
nor the pre-commit hook ever saw it.

This was discovered by accident: a `--build-tags e2e` run caught a misspelling in a
comment written seconds earlier, in a file the normal pass had already declared clean.

**Fix:** add a tagged pass to the `lint` target.

```make
lint:
	golangci-lint run ./...
	golangci-lint run --build-tags e2e ./...
	golangci-lint run --build-tags integration ./...
```

The backlog this exposes is one line. On `main` today:

```
$ golangci-lint run --build-tags e2e ./...
internal/tui/run_eval_e2e_test.go:311:2: ineffectual assignment to cur (ineffassign)
1 issues.
```

That is the harness's `default:` branch dropping `tea.BatchMsg` — the same defect
PR #142 fixes for its own reasons, and the linter would have named it on the day it
landed. Fix that one issue and the tagged pass is clean, so the target can be
extended without a backlog.

---

## 7. The `run.go` branch-separator fix is riding inside PR #134

**Where:** `internal/tui/run.go:294`, plus expectation flips in `run_test.go` and
`run_backup_test.go`

`runBackupBranchName` now joins its suffix with `-` instead of `/`
(`run/spec/part-1` → `run/spec-part-1`). This is a **correct fix for a real bug**:
git cannot hold `refs/heads/run/spec` and `refs/heads/run/spec/part-1` at once — the
first is a file where the second needs a directory — so a parallel run after a single
run failed to create its backup branch.

It is also production branch-naming behaviour with nothing to do with an eval
harness, and it orphans any existing `run/<spec>/part-N` branches.

**Fix:** split it into its own PR with a note about the orphaned branches. Deferred
because doing it means rewriting an open PR, which is the author's call.

---

## 8. `eval-judge` defaults to a superseded grader

**Where:** `Makefile:94` — `PI_EVAL_JUDGE_MODEL ?= claude-sonnet-4-6`

It resolves (`internal/provider/model_catalog.go:41`), so this is not broken.
But `claude-sonnet-5` is in the same catalog and newer, and `internal/eval/eval.md`
advises grading with a different model than the one under test — which makes the
default worth a deliberate choice rather than a leftover.

---

## 9. Two dead fields on `model`

**Where:** `internal/tui/tui.go:27` (`cancel`), `internal/tui/tui.go:135` (`slashCommandSelected`)

- `cancel` is assigned once at `tui.go:499` and never read. The apparent hits are
  `m.cancelAgent()`, a different symbol.
- `slashCommandSelected` is never written in production. It is read by
  `teatest_test.go:1443` and `tui_update_test.go:578`, which therefore assert a
  constant zero value and prove nothing.

**Fix:** delete both; adjust the two tests that assert against the dead one. ~2
production lines, ~16 test lines.

---

## 10. Popups still live in `tui.go`

`tui.go` is 2528 lines. Moving the popup code to `popup.go` is a pure file move of
~350 lines with **zero test edits**, and takes the file to ~2180.

Listed separately from item 11 because it moves no fields and so carries no risk
beyond the move itself.

---

## 11. Three small state groupings on `model`

`model` has ~51 fields, but the framing "51 flat fields" overstates it: 14 are
already whole subsystems behind one name (`runState` at `run.go:36` is itself 19
fields; `searchPopupState` at `tui.go:253` is 7). The genuinely loose scalars number
about 35.

Three groups are cheap because **no test literal initialises them**:

| Group | Fields | Where | Test cost |
|---|---|---|---|
| `/plan` worktree state | 5 | `tui.go:56-60` | 0 references anywhere |
| frame geometry + mouse selection | 6 | `tui.go:84-97` | 0 literal inits; 47 refs survive embedding |
| deferred init / loading | 6 | `tui.go:106-110`, `:139` | ~26 literal edits |

**Migrate by embedding, not named fields.** Go's field promotion keeps all 155
method bodies compiling unchanged; only keyed composite literals break, and the
compiler catches 100% of those.

Bundled with this: `running` carries two meanings that have drifted — "a turn is in
flight" (`startNextPrompt:523`, `handleAgentDone`) and "the UI is busy, refuse this"
(`handleCommitKey:1061`, `handleLoginKey`, `shouldShowSlashCommandPopup:2190`, and
seven more). The second is a **predicate, not state**. `func (m *model) busy() bool`
addresses it for zero test edits; moving the field into a sub-struct does not.

---

## 12. `View` should take a DTO, the way `RenderSidebar` already does

**Where:** `internal/tui/tui.go:1383`

`RenderSidebar(SidebarRenderInput)` (`sidebar.go:111`) is the best seam in the
package: 22 helpers hang off it and none of them can see `model`. `StatusModel.Render`
follows the same shape. `matchToolResultCard` (landed in #136) is a third instance,
arrived at independently — pulled out as a pure function over `[]message`, which is
exactly what let session-restore reuse it.

`View` is the holdout, and it reaches into **23 fields** — more than any other
function in the package.

**Why this beats item 14:** it decouples the struct's largest consumer without moving
a single field, so it costs zero test-literal edits.

**Trap:** `View` currently *mutates* (`syncPalette:1387`, `applyResize:1414`,
`lastFrame`, `frameRows`). Do not try to make it pure in the same change.

---

## 13. Tests name the `model` struct shape 413 times

413 keyed `model{…}` literals across 34 of 65 test files, backing 1340 test
functions. Dominant keys: `chatModel:` 248, `cfg:` 163, `inputModel:` 69,
`running:` 60, `run:` 47, `height:` 39, `width:` 38, `ctx:` 29, `agentCh:` 21.

This is the binding constraint on every structural change above. Until tests stop
naming the shape, each field move costs 20–250 mechanical edits and the resulting PR
cannot be reviewed by inspection.

**Fix:** a `testModel(t, opts...)` builder. Two half-duplicated helpers already exist
(`teatest_test.go:19`, `coverage_boost_test.go:424`) and should be folded into it.
Best done opportunistically, one file at a time, rather than as one 400-edit change.

---

## 14. `agentState` extraction — recommended against, for now

Grouping the 7 agent-execution fields (`tui.go:63-75`) would cost **~98 test literal
edits across 13+ files** and touch `agent_loop.go` and `run.go`, both of which are
actively contended.

**No bug is attributable to the flat namespace.** The one ownership bug that was
found — the `m.cfg` race, fixed in #143 — was fixed by *passing a parameter*, which
is what commit `35c3b25` did for the sibling `agentCh` case. A field grouping would
not have prevented either: the race is about who may read `m`, not how `m` is laid
out.

Also relevant: complexity linters are deliberately disabled (`.golangci.yml:34`) and
`fieldalignment` is explicitly excluded (`:122`). Nothing in the toolchain is asking
for this.

**What would justify revisiting:** item 13 has landed, **and** a concrete defect is
traced to agent-state ownership the way the `cfg` race was traced to `cfg`. Neither
is true today.

---

## 15. `toolCallSummary` is now the worst function in its file

**Where:** `internal/tui/tool_display.go:502`, cyclomatic complexity 27

After #141 took `formatToolResult` from 34 to 3, this is the largest function left in
the file. It is the same species of problem — a long switch over tool names — and the
same fix should work: an ordered table keyed by tool name.

---

## 16. The 120-column clip is byte-indexed and duplicated

**Where:** `internal/tui/tool_display.go` (`clipToSummaryWidth`, extracted in #141)
and a third copy in `toolResultSummary`

The clip slices by byte, so it can split a multi-byte rune in half — the same class
of bug as the eval sparkline fixed in #142, which produced invalid UTF-8 in every
report. A rune-safe `truncateRunes` already exists in the same file.

**Why deferred:** reconciling them changes output, and #141 was held to a
byte-for-byte-identical guarantee. This needs to be its own change with its own
before/after.

---

## 17. `/context` prints two contradictory totals

**Where:** `internal/tui/context_report.go` (post-#140)

`/context` prints `*Context usage*` — the real `RenderContextBreakdown`, driven by
the provider's `LastPromptTokens()` — and then immediately `**Context Usage**`, a
`len(text)/4` estimate over `m.chatModel.Messages`.

They cannot agree. The display list excludes the system prompt, tool definitions,
rules, skills and MCP tool schemas, every one of which the breakdown counts as its
own segment. In the captured full-session golden the same conversation reads:

```
24% Full  ~48.0k / 200.0k Tokens        ← breakdown
- **Total context**: ~1.6k tokens        ← legacy estimate, 30x lower
```

**Git history confirms it is a leftover.** The legacy block arrived 2026-03-20 in
`374744e`, where it *was* the whole command. `ContextBreakdown` arrived 2026-08-04 in
`449e2ca` — **41 insertions, 0 deletions**. The new section was prepended and the old
one was never opened.

**But it does not all deserve deletion.** Two things exist nowhere else: the per-role
split (user/assistant/tool with message counts — the breakdown has a single
`Conversation` segment) and the model label line.

**Proposal:** when `ContextBreakdown != nil`, drop the `**Context Usage**` header and
the `**Total context**` line, keeping the model line and the per-role split under the
existing `*Estimated usage by category*` sub-header. When `ContextBreakdown == nil`,
leave it exactly as-is — there it is the only reading available.

The golden fixtures added in #140 make this a small diff with a reviewable record of
precisely what the user's screen loses.

---

## 18. 112 inline `lipgloss.NewStyle()` calls on the render path

Counted in non-test files: `tool_display.go` 25, `status.go` 19, `tui.go` 16,
`sidebar.go` 16, `chat.go` 11, and 25 more across seven files. Most are inside render
functions that run per frame; some are inside loop bodies building the identical
style per line.

This is the same hot path where `blankFast` (`collapse.go:32`) already earned a
hand-written ANSI scanner because profiling attributed **24% of all CPU** to the
`ansi.Strip` call it replaced.

**Fix:** a style set derived from the palette once, rebuilt on theme change.

**Why deferred, and why it should go last:** it touches `tool_display.go`,
`status.go`, `tui.go`, `sidebar.go` and `chat.go` — every file the other work was
editing. Sequencing it after the rest avoids a merge fight over the exact functions
being restructured.

---

## Suggested order

1. **6** (lint tags) — extra-small, and it closes a blind spot that hid a real issue.
2. **9** (dead fields) and **8** (judge default) — trivial.
3. **1** (`/run` result matching) — the only remaining correctness item with a user-visible symptom.
4. **7** (split the branch-separator fix) — while #134 is still open.
5. **5** (the flake) — before it gets normalised as "just flaky".
6. **17** (`/context` duplication) — user-visible, and #140's goldens make it cheap.
7. **13** (test builder), then **12** (`View` DTO), then **10**/**11** — in that order; the builder makes the rest cheap.
8. **18** (style set) — last, once the files stop moving.
9. **2**, **3**, **4**, **15**, **16** — as they come up.
10. **14** — not now.
