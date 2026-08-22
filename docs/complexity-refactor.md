# Cyclomatic complexity refactor — evidence

Branch point: `15e29c3`. Scope: every function in `cmd/`, `internal/`, `piagent/`
and `pimodels/` measuring above cyclomatic complexity 15 — 81 functions across
18 packages. Tests excluded from measurement.

Tools: `gocyclo` (cyclomatic, CC) and `gocognit` (cognitive, COG). Both metrics
are reported because they disagree in useful ways: a flat `switch` scores high CC
and near-zero COG, while a deeply nested block scores the reverse. Chasing CC
alone would have rewritten harmless dispatch tables and left the genuinely
unreadable code alone.

## Result

| Metric | before | after |
|---|---|---|
| Functions | 2553 | 2858 |
| Total CC | 11525 | 11667 |
| **Decision points** (total CC − functions) | **8972** | **8809** |
| Average CC | 4.514 | 4.082 |
| **Max CC** | **37** | **15** |
| CC > 10 | 216 | 150 |
| **CC > 15** | **81** | **0** |
| CC > 20 | 38 | 0 |
| CC > 30 | 5 | 0 |
| Total COG | 12039 | 11188 |
| Max COG | 89 | 41 |
| COG > 20 | 106 | 40 |

Total CC rises by 142 while function count rises by 305. That is expected and is
not complexity moving sideways: every function carries a baseline CC of 1, so
305 new functions add 305 to the total by existing. The metric that ignores that
baseline — decision points — falls by 163. Cognitive complexity, which has no
such baseline, falls by 851.

**No function anywhere in the repo got more complex.** Verified by joining the
before and after measurements on package+function+file: the set of functions
with a higher CC after than before is empty.

## Verification

All run at the final tree state, from the worktree:

| Check | Command | Result |
|---|---|---|
| Build | `go build ./...` | clean |
| Vet | `go vet ./...` | clean |
| Tests | `go test ./... -count=1` | **45 packages, 0 failures** |
| Lint | `golangci-lint run --max-issues-per-linter=0 --max-same-issues=0 ./...` | **0 issues** |

The lint run disables golangci-lint's default truncation (`max-issues-per-linter: 50`,
`max-same-issues: 5` in `.golangci.yml`). A default run hides a systematic problem
behind a count of 5.

Tests were run with `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `OPENAI_API_KEY`,
`GEMINI_API_KEY` and `GOOGLE_API_KEY` stripped from the environment, so no test can
pass here by reaching a real endpoint and then fail in CI. Baseline coverage was
measured the same way, in a pristine detached worktree at `15e29c3`, using the
identical command — the before and after numbers are two runs of one command, not
one measurement and one recollection.

## Coverage

Every package touched by this change gained coverage. No package regressed.
Mean across all 45 packages: **89.64% → 90.29%**.

| Package | before | after | delta |
|---|---|---|---|
| `internal/tui/refs` | 87.0% | 90.7% | +3.7 |
| `internal/palace` | 83.7% | 87.1% | +3.4 |
| `internal/subagent` | 88.5% | 91.8% | +3.3 |
| `internal/webserver` | 87.5% | 90.4% | +2.9 |
| `internal/tools` | 87.5% | 89.8% | +2.3 |
| `internal/codex` | 83.7% | 86.0% | +2.3 |
| `internal/cli` | 80.5% | 82.8% | +2.3 |
| `internal/tui` | 90.5% | 92.2% | +1.7 |
| `internal/extension` | 85.9% | 87.5% | +1.6 |
| `internal/config` | 90.7% | 92.3% | +1.6 |
| `internal/provider` | 92.5% | 93.5% | +1.0 |
| `internal/auth` | 89.6% | 90.5% | +0.9 |
| `internal/pirpc` | 96.6% | 97.4% | +0.8 |
| `internal/acp/server/adapter` | 99.5% | 100.0% | +0.5 |
| `piagent` | 91.4% | 91.7% | +0.3 |
| `internal/acp/server` | 88.8% | 89.0% | +0.2 |
| `internal/session` | 88.4% | 88.6% | +0.2 |
| `internal/audit` | 95.3% | 95.4% | +0.1 |

Roughly 1,400 new test cases across 18 new `complexity_*_test.go` files.

## Largest reductions

| Function | CC before | CC after |
|---|---|---|
| `tui.(*ChatModel).renderMessages` | 33 | 7 |
| `provider.ollamaContentsToMessages` | 31 | 8 |
| `provider.antContentsToMessages` | 31 | 8 |
| `subagent.(*Orchestrator).Spawn` | 29 | 11 |
| `tui.(*model).handleSlashCommand` | 28 | 5 |
| `extension.LoadSkillsWithOptions` | 27 | 4 |
| `palace.(*DrawerService).Search` | 25 | 8 |
| `webserver.(*ServerV2).handleGeminiVoiceWS` | 24 | 8 |
| `palace.MineConversations` | 23 | 4 |
| `cli.runModelList` | 22 | 7 |
| `config.parseMCPServers` | 21 | 4 |
| `provider.(*openaiModel).generateResponses` | 21 | 3 |
| `cli.resolvePingTarget` | 20 | 4 |
| `codex.(*Session).handleItem` | 20 | 5 |

`tui.(*model).formatContextUsage` (CC 37 → 3, COG 67 → 3) and
`tools.analyzeSessionFile` (CC 30 → 4, COG 71 → 4) were the two worst functions in
the repo on both metrics.

## How behaviour preservation was established

Not by "the tests still pass" — the pre-existing tests did not reach many of these
branches, which is why the functions were able to grow this large. Three stronger
techniques were used, in descending order of strength:

1. **Byte-identical golden corpora captured from pre-refactor code.**
   `internal/tui`'s rendering path has a 350-case, 86KB corpus dumped from a
   read-only `git archive HEAD` export *before* any source edit, covering
   `renderMessages` across 4 message sets × 3 widths × streaming on/off, every
   tool-card kind × 3 widths × compact × blink, 20 status-bar configurations, 38
   `toolCallSummary` arg shapes, 29 `formatToolResult` payloads and 23 `blankFast`
   inputs. `cmp` before/after reports identical. The dump harness remains behind
   `PI_RENDER_GOLDEN_OUT` so the next person refactoring this code gets the same
   diff for free. `internal/provider` did the same for wire-format translation and
   additionally **mutation-checked** the goldens: changing the tool-result
   placeholder string in the pre-refactor source makes the golden fail, proving the
   assertions are not vacuous.

2. **Exhaustive equivalence proof** where the domain is finite. `audit.isEmoji`
   became a range table; the original `||` chain was transcribed verbatim as
   `isEmojiOriginal` and every code point from 0 to 0x10FFFF is asserted to agree.
   An off-by-one has nowhere to hide.

3. **Mechanical diff against `main`** for table conversions. The slash-command
   table was verified by extracting the case labels from both original functions
   out of `git show main:`, confirming both covered the same 25 commands, and
   diffing the reconstructed command→description mapping byte for byte. The
   autocomplete list was diffed *in order*, because `completeSlashCommand` returns
   the first prefix match — order is behaviour, not presentation.

## Judgment calls worth reviewing

Some targets were **deliberately not fully reduced**, because the metric was the
wrong master:

- `tui.blankFast` stopped at CC 9 / COG 16. It is the documented hot path. Going
  lower means splitting the per-byte scan loop, which would not inline.
  `skipFastCSI` was confirmed inlined via `-gcflags=-m`, and benchmarked at
  308µs → 302µs with allocations unchanged, so the extraction that *was* done
  costs nothing.
- `tui.(*model).updateTerminal` stopped at CC 14. Its ~12-case type switch is the
  floor; the case *bodies* were extracted (COG 17 → 5) rather than shredding arms
  to move CC elsewhere.
- `tui.toolCallSummary`'s table conversion is partial. `ls`, `tree` and `agent`
  stayed in the switch because for `ls` a *present but empty* `path` returns `""`
  while an *absent* one returns `"."`.
- `tui`'s title-sync carve-out (`/clear`, `/exit`, `/quit` skip `setSessionTitle`)
  stayed a separate switch rather than becoming a per-command flag with two states.
- `pirpc.(*Server).dispatch` (CC 14) and several other flat dispatch switches were
  left alone. A flat switch is not a defect.

**Ordering and structure that is load-bearing**, now explicit and pinned:

- `tools.readWindow` checks for the file's phantom trailing line on the **raw**
  line, before BOM/CR normalization. Normalizing first would turn a final line of
  exactly `"\r"` — or a lone BOM on line 1 — into `""` and silently drop a real line.
- `tools.coerceAtPath` takes the value as an explicit parameter rather than
  re-reading `obj[key]`. The original passed the originally-ranged value to both
  the `parent.key` and `parent.$.key` checks, not the value the first may have just
  written back.
- `provider.sendResponses` takes `*responses.ResponseNewParams`. The original
  closed over `params` by reference, so clearing `PreviousResponseID` on one
  attempt persisted into the next retry. Passing by value would have silently
  restored the stale pointer.
- `tools.formatToolResult`'s 13 probes became an ordered slice; the shapes overlap
  (a backgrounded bash poll carries both `handle` and `exit_code`). A test fails the
  build if the slice and the test's shape map desync.
- `cli.runMemoryRecent` still validates `--type` *after* opening the DB, so a run
  with both a missing DB and a bad type still reports "memory database not found"
  first.
- `codex.(*Session).handleItem` keeps explicit tool cases with no `default` arm, so
  an item type codex adds later stays silent.
- `session.rangeHasPartID` compares `id(part) == want` where `id` returns `""` for
  a wrong-kind part. This is equivalent to the original skip-then-compare **only
  because both callers guarantee `want != ""`**. The invariant is documented on the
  function and pinned from the caller side.
- `provider`'s four content converters share only the genai-*reading* half. The
  wire-*emitting* half is deliberately per-provider: Anthropic substitutes
  `"No response available for this function call."` for an unmatched call, Ollama
  substitutes `""`, and OpenAI Chat Completions uniquely *filters* calls with empty
  IDs. A shared abstraction would have silently picked one.

**Pre-existing quirks preserved and now documented rather than silently fixed:**
`webserver.csiNum` treats an explicit `0` literally where a real terminal treats it
as 1; `webserver.eraseDisplay` mode 1 blanks the cursor line's head as well as the
lines above; `palace.maxFTSRank` carries a provably dead branch, kept so the
collapse of two byte-identical copies stays mechanically checkable.

## Known issues found but NOT fixed here

These are pre-existing on `main` and are deliberately out of scope for a
behaviour-preserving refactor. Each is recorded so it cannot be lost.

1. **`subagent.(*WorktreeManager).Create` destroys uncommitted work on failure.**
   The failure path is commented "Restore stashed changes on failure" but runs
   `git stash drop`, which deletes the entry *without applying it* — and
   `stash push -u` has already reverted the working tree. Verified empirically in a
   throwaway repo: the changes do not come back and the stash list is empty. They
   survive only as a dangling commit until gc. `MergeBack` in the same file gets it
   right via `popStashByMessage` (apply, then drop). The one-line fix is to use
   `popStashByMessage(stashMsg)` here too, but that is a behaviour change and
   belongs in its own reviewable commit.
   Current behaviour is pinned by `TestCreate_DropsStashWhenWorktreeAddFails`,
   which asserts the dirty file does **not** return and carries a comment saying
   this is the bug, why it was preserved, and that a fixer should expect the test
   to fail and flip its assertions.

2. **`spawner_timeout_test.go` has pre-existing timing flakes.**
   `TestSpawn_AbsoluteCapStillApplies` and
   `TestSpawn_SteadyOutputSurvivesInactivityWindow` fail roughly 1 run in 5 under
   full-package load, racing a 700ms absolute cap against a 500ms inactivity window.
   Confirmed not caused by this change: a scratch build of the package with all five
   source files restored from `HEAD` flakes identically.

3. **Coverage in `internal/codex` is nondeterministic** (85.5–86.7% across runs).
   The variance is in pre-existing app-server subprocess tests.

4. **Uncovered paths that remain**, stated rather than hidden: `cli.ollamaPingFull`'s
   `provider.NewOllama` construction error and its stream-failure fallback (the
   function builds its own LLM with no injection point, and a stream failure pays
   real `retryStream` backoff); `piagent.buildRuntime`'s tool-construction error
   branches. All were uncovered on `main` too.

5. **`piagent.buildRuntime` takes `cfg *config.Config` deliberately** —
   `subagent.NewOrchestrator` retains the pointer, so passing a copy would detach
   the orchestrator's config from the one handed to `buildCallbacks`. `Orchestrator`
   exposes no accessor for its config, so this is the one behaviour-preservation
   claim in the change that rests on inspection rather than a test.

## Follow-up work identified

- Fix the `Create` stash-drop data loss (item 1 above). One line, own commit.
- Widen the margins in `spawner_timeout_test.go` (item 2).
- Remaining `gocognit` hotspots outside this scope: `palace/graph.go` `Traverse`
  (25), `session/compaction.go` `LLMSummarizer` (22) and `filesTouched` (21),
  `session/compaction_shed.go` `ShedSupersededToolResultsWithDedup` (22),
  `provider.ollamaGenaiToolsToOllama` (36).
- `tui.renderLiveOutput` still hand-rolls the gutter loop `writeGutterLines` owns.
- `refs.expandFolder` duplicates path resolution now living in `readRefFile`.
