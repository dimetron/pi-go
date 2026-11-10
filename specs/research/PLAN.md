# Plan: TUI Improvements Inspired by OpenCode (`tmp/opencode`)

> Status: **plan — Phases 1, 3, 4, 6, 7 and 8 implemented; two Phase 2 defects
> fixed. Phase 5 (`/theme`, `/model`, `/timeline` pickers) unstarted.**
> Implemented on `fix/tui-plan-findings`. See "Delivered so far" at the end.
> Nothing is committed yet: `git commit -S` fails in this environment
> (1Password's SSH agent refuses to sign), so the branch holds staged work.
> Written: 2026-11-09 · Plan reviewed against the code: 2026-11-09
> Reference: `tmp/opencode` @ `9e9b551e3a` (2026-11-08 17:08 -0600)
> Code baseline: `main` @ `29fdab7`

## Goal

Improve `pi-go`'s terminal UI (`internal/tui`) by adopting high-leverage UX,
readability, and ergonomics patterns from OpenCode
(`tmp/opencode/packages/tui`) while preserving `pi-go`'s Bubble Tea v2 cell-grid
architecture and the `tui-render-design` invariants.

Phases are vertical and independently verifiable, ordered P0 (bug/gap fix) →
P1 (readability & workflow) → P2 (ergonomics & polish).

## How this revision differs from the first draft

The first draft of this plan read well and was wrong in ways that would have
wasted a cycle, so each is recorded here rather than quietly fixed:

| First draft said | Verified fact |
|---|---|
| Phase 3 adds diff colour fields to `Palette` | `Palette` had **no** diff fields, but the fix that mattered was elsewhere: `git-file-diff` and `git-hunk` had **no entry** in `toolResultShapes`, so both fell through to the JSON fallback and the card printed `{"diff":"@@ -1,3 +1,4 @@\n-old\n+new\n",...}`. Colouring a diff that renders as one JSON line is decoration on a bug. |
| `regularToolOutput` renders bash `lines[:15]` | It clips at `maxRegularToolLines = 15`. The **2-head/2-tail** rule lives in `bashOutputPreview` (`tool_display.go:1222` on `main`), which is what the summaries call. The draft named the wrong function. |
| `/theme` "returns hardcoded Catppuccin palettes" | True, and worse than stated: `paletteFor` ignored `Theme.Colors` completely, so all **40** themes in `themes.json` resolved to 2 palettes. |
| Phase 4 coalesces tools where `toolBlockKind(msg.tool) == blockRead` | `toolBlockKind("ripgrep")` returns `blockTool` (10), **not** `blockRead` (6). `ripipeg` is the common case (`newGrepTool` names itself `ripgrep` when `rg` is installed). The draft's grouping would have silently excluded every search on this machine. |
| Phase 4 folds on `!msg.done` | `message` has **no** `done` field. The predicate is `msg.toolPending()` (`chat.go:261`). |
| Phase 4 lists `git-status`, `git-log` as foldable read tools | Neither is a registered tool. The git tools are `git-file-diff`, `git-hunk`, `git-overview`. |
| Phase 2 boosts from `m.sidebar.ModifiedFiles` | No such field. `model` has no sidebar model at all — the sidebar is a pure `RenderSidebar(SidebarRenderInput)`. |
| Phase 5 calls `m.previewTheme(name)` / `m.searchPopup.originalTheme` | Neither exists. `searchPopupState` has 7 fields and no per-mode payload. |
| Phase 8 calls `m.setStatus(...)` / `statusFlashClearMsg` | Neither exists. The mechanism is `m.setFlash(text) → tea.Tick(flashDuration) → flashExpiredMsg{seq}` (`tui.go:1035-1046`). |
| Phase 8 adds `stash []string` to `InputModel` | `InputModel` keeps its text in an embedded `textarea.Model`; `Text` and `CursorPos` are *derived* by `syncFromInput`. Push/pop must go through `setValue`/`SetText`, or the textarea and the mirror diverge. |
| Phase 5 uses `Ctrl+T` for timeline | `Ctrl+T` is the textarea's **transpose-character** binding (`bubbles/v2 textarea`). `Ctrl+S` and `Ctrl+X` are free. |
| Phase 0 runs `go test -v ./internal/tui/...` then `make lint` | Fine, but the draft asserted no baseline. The measured baseline is recorded below so a later claim of "made it pass" has something to compare against. |
| `.agents/skills/tui-render-design/SKILL.md` is the authority | Both `.agents/` and `.claude/` are **gitignored** (`.gitignore:46`, `~/.gitignore:13`). The invariants live on one machine; CI cannot read them. |
| Skill says the subagent window is 7 lines | It is `3` (`tool_display.go:459`), deliberately shrunk from 7 in `b98515f`. |

Two other things the review found, unrelated to this plan but worth fixing
before it is executed. `truncateRunes(s, n)` with `n < 3` panics (`r[:n-3]`);
this is currently unreachable because `argWidth()` floors at 60, so it is
latent rather than live. And `specs/research/` has no `README.md` index of its
own — the research table lives in `specs/AGENTS.md`, which is where this plan is
now listed.

---

## Architectural comparison & non-regression invariants

| Dimension | `pi-go` (`internal/tui`) | `opencode` (`tmp/opencode/packages/tui`) |
|---|---|---|
| **Renderer** | Immediate-mode string grid in Bubble Tea v2 (`View()` at `tui.go:1556`) with FNV-1a per-message cache (`renderKey`, `chat.go:199`) | Retained-mode `@opentui/solid` tree (`app.tsx:3`) with tail-first transcript mounting (`routes/session/index.tsx:419`, `mount-budget.ts`) |
| **Screen & scroll** | Normal screen (`v.AltScreen = false`, `tui.go:1732`) + cell-level mouse drag selection (`selection.go`) | Full-screen viewport + compact inline mode (`mini/footer.view.tsx`) |
| **Divider & minimap** | 1-col semantic rail, `railWidth = 1` (`minimap.go:19`), `blockKind` colours, `◉` thumb | Standard scrollbox scrollbar |
| **Context visualisation** | Calibrated dumb-zone gauge below the prompt, with token-origin breakdown (`context_rule.go`, `context_breakdown.go`) | Sidebar progress bar + percentage (`feature-plugins/sidebar/context.tsx`) |
| **Rich diagrams** | Native ASCII/Unicode Mermaid (`mermaid.go:85`) and SOP spine graphs (`sop_graph.go:72`) | Standard markdown code blocks |

### Mandatory non-regression invariants

These are enforced by `internal/tui/render_integrity_test.go`. The prose version
lives in `.agents/skills/tui-render-design/SKILL.md`, which is **gitignored** —
treat the test as the authority, because that is what CI and every other clone
can see.

- **I1 — every row is exactly the terminal width.** `ansi.StringWidth(row) == m.width`.
  Cells, not bytes or runes; `💭` is one rune and two cells.
- **I2 — one rail.** `railWidth == 1` remains the sole vertical divider between
  chat and sidebar. A panel must not draw a border on an edge that has a rail.
- **I3 — never re-style styled text.** Build raw, transform, then style. Feeding
  ANSI-bearing text to chroma or a non-ANSI-aware truncator shreds the escapes;
  a half-eaten escape makes the row measure wider than it renders and breaks I1.
- **I4 — reset before a boundary.** Emit `ansi.ResetStyle` before a column join
  so content cannot colour the divider.
- **I5 — symmetric gaps**, from the Fibonacci ladder `1, 2, 3, 5, 8, 13, 21`.
- **TUI output safety** (`AGENTS.md`): never write to `os.Stdout`/`os.Stderr`
  while the TUI is active; route diagnostics through the session logger.

---

## Phase 0 — Baseline and safety checks

### Measured baseline (2026-11-09, `main` @ `29fdab7`)

| Command | Result |
|---|---|
| `go build ./...` | pass, 4.9 s |
| `go test ./internal/tui/...` | pass, 18.8 s (`tui` + `tui/refs`) |
| `go test -race -count=1 ./internal/tui/` | pass, 26.3 s |
| `make vet` | pass |
| `golangci-lint run ./...` | 0 issues |
| `git verify-commit HEAD` | **fails locally** — GitHub-created merge commit; see `AGENTS.md`. Expected, not a regression. |

### Tasks

1. Confirm worktree state: if `git rev-parse --git-dir` equals
   `--git-common-dir` you are in the primary checkout and must create a
   worktree; otherwise work where you are.
2. Re-run the five commands above and require them to match this table.

### Acceptance criteria

- Every command matches the baseline. A difference is either a real regression
  or a stale table — decide which before writing code.

---

## Phase 1 (P0.1) — Theme palettes, diff colours, and diff rendering

> **Status: implemented** on `fix/tui-plan-findings`, pending sign-off.

### Objective

Make `/theme <name>` recolour the TUI from the 13 semantic tokens in
`themes.json`, and give the renderers the diff roles they were missing.

### Why this was one phase, not two

The draft proposed adding `Palette` diff fields as a standalone phase. That was
necessary but not sufficient: `git-file-diff` and `git-hunk` had no
`toolResultShapes` entry, so a diff card rendered as one long JSON line. A
colour on that line is invisible, so the two changes ship together.

### Reference

- `pi-go`: `palette.go` (`Palette`, `darkPalette`, `lightPalette`, `paletteFor`,
  `paletteKey`), `theme.go:21` (`ThemeColors`, 13 tokens), `themes.json`
  (40 themes, 7 light), `tool_display.go` (`toolResultShapes`,
  `highlightDiffOutput`), `render_integrity_test.go`.
- `opencode`: `packages/theme/src/tui/select.ts` — `selectThemeMode` picks the
  document's mode-specific overlay and merges it over `base`. The same shape:
  a base plus named overrides.

### What was actually wrong

1. **`Palette` carried no diff roles at all.** `ThemeColors` declared
   `DiffAdded/DiffRemoved/DiffAddedText/DiffRemovedText`, the `/theme` preview
   strip drew them (`theme_preview.go:33`), and nothing else could read them.
   The first `+`/`-` a user sees came from chroma, not from the theme.
2. **`paletteFor` ignored the theme.** It was
   `if t.ThemeType == "light" { return lightPalette }; return darkPalette`.
   40 themes → 2 palettes. `/theme dracula` and `/theme gruvbox-dark` were
   pixel-identical.
3. **Diff results fell to the JSON fallback** (see above).

### Files

- `internal/tui/palette.go` — four diff roles on `Palette`; `paletteFor`
  overlays `Theme.Colors`; `paletteKey` folds the diff roles.
- `internal/tui/tool_display.go` — `gitDiffResultSummary`,
  `gitHunkResultSummary`, `highlightDiffOutput`, both shapes registered.
- `internal/tui/theme_test.go` — rewritten `TestPaletteFor*`.
- `internal/tui/tool_display_test.go` — `TestToolResultSummary_DiffShapes`,
  `TestHighlightDiffOutput`.
- `internal/tui/render_integrity_test.go` — a diff card in `realisticChat()`.
- `internal/tui/complexity_render_test.go` — the shape-table guard.

### Design notes

- **Overlay, never replace.** A theme that names only 13 tokens leaves ~20
  `Palette` roles unset. The variant base fills those, so a theme author cannot
  accidentally ship a palette with unset colours.
- **A missing token means "keep the default", not "set black".**
  `parseHexColor` reports false for `""`, `#rgb` length errors, and bad hex.
- **`Secondary` drives rules and gutters, not text.** tokyo-night's `#565f89`
  is 2.76:1 on its own base. That is a comment colour. Mapping it onto
  `Subtext`/`Dim`/`Faint` would have made muted text unreadable on the default
  theme — a legibility regression dressed as theming.
- **The light diff pair is retuned.** Stock Catppuccin Latte is `#40a02b` on
  `#e0f0e0` = 2.82:1, below the 4.5:1 body-text floor. Shipped `#2f7d1f` on
  `#e6f4e6` = 4.53:1 and `#b30e2f` on `#fbe9ec` = 5.95:1, both of which also
  clear 4.5:1 against the page background, so a diff row is legible whether or
  not the renderer tints it.
- **`paletteKey` must fold the diff roles.** `renderKey` keys the message render
  cache on `paletteKey`. Two themes with identical chrome and different diff
  tints would share a cache entry and keep the previous theme's diff rows.
- **Diff styling is whole-line.** `highlightDiffOutput` styles each line once,
  never per-run, so `softWrap` and the I1 width check see a single styled span.
  The `+`/`-` gutter is the non-colour cue, so the meaning survives a monochrome
  terminal.
- **Default-screen change is intended.** `DefaultThemeName` is `tokyo-night`, so
  applying its tokens changes the default screen. The old test
  (`TestPaletteForDarkThemeMatchesDefault`) pinned the bug: it asserted the
  default theme resolved to bare Mocha. It is replaced by one that asserts the
  tokens are applied and unnamed roles keep the contrast-checked default.

### Acceptance criteria

- `/theme <name>` visibly changes borders, headers, tool badges, rail, and diff
  rows.
- Two dark themes with different `primary` render differently.
- `git-file-diff` and `git-hunk` cards render as readable diff lines, not JSON.
- `go test ./internal/tui/...`, `-race`, and `golangci-lint` pass.

### Delivered so far

| Change | Guard |
|---|---|
| `Palette` diff roles + light-pair contrast retune | `TestHighlightDiffOutput` |
| `paletteFor` overlays all 13 theme tokens | `TestPaletteForDarkThemeOverlaysThemeTokens`, `TestPaletteForDistinctThemesHaveDistinctKeys` |
| `paletteKey` folds diff roles (cache correctness) | `TestPaletteForDistinctThemesHaveDistinctKeys` |
| `gitDiffResultSummary` / `gitHunkResultSummary` + `highlightDiffOutput` | `TestToolResultSummary_DiffShapes`, `TestHighlightDiffOutput` |
| `gitOverviewResultSummary` / `readImageResultSummary` — two more tools that were printing raw JSON | `TestToolResultSummary_InspectorShapes` (marshaled from `internal/tools` structs) |
| `highlightGitOverviewOutput` — the overview was rendering entirely dim | `TestToolResultSummary_InspectorShapes` |
| `bashOutputPreview` tail-first, counted elision | `TestBashOutputPreview`, `TestFormatToolResult_BashTailSurvivesTruncation` |
| `toolBlockKind("ripgrep")` → `blockRead` — every real search was getting the neutral rail cell | `TestKindOf/ripgrep_tool` |
| `truncateRunes` no longer panics for `n < 3` | `TestTruncateRunes_SmallBound` |
| `filterSearch` no longer falls back to the unfiltered list on no match | `TestFilterSearch_NoMatchStaysEmpty`, `TestFilterSearch_ClearingQueryRestoresAll` |
| `@path#L10-20` split at extraction, so a range is no longer sent as a filename | `TestSplitMentionLineRange`, `TestExtractMentions_StripsLineRange` |
| Double-`Esc` interrupt confirmation, disarmed when a turn ends | `TestEscArmsThenInterrupts`, `TestEscArmExpires`, `TestEscArmClearedWhenTurnEnds` |
| `Ctrl+S` draft stash, walkable down the stack | `TestCtrlSStashesAndPopsDraft`, `TestCtrlSLIFOOrder`, `TestCtrlSInFrame` |
| 3-state `Ctrl+O` verbosity; exploration runs fold to one line; finished thinking collapses | `TestExplorationGroupFoldsContiguousRun`, `TestHandleKey_CtrlO_ToggleTools`, `TestCollapsedThinkingInModes` |
| `Verbosity` folded into `renderKey` so a mode change cannot replay cached bytes | `TestCollapsedThinkingInModes` |
| `/subagents` opens a picker; `Ctrl+X` cancels one agent; selecting expands its card to 25 rows | `TestAgentSearchItems`, `TestExpandAgentCard`, `TestExpandedAgentWidensCardWindow`, `TestCancelSelectedAgentReportsOrchestratorError` |
| Sidebar folds detail rows instead of silently clipping whole sections | `TestSidebarFoldKeepsEveryHeading`, `TestSidebarFoldReportsWhatItHid`, `TestSidebarFoldWidths` |

---

## Phase 2 (P0.2) — `@file` mentions, fuzzy ranking, and line ranges

### Objective

Wire `CompleteMention` to an inline `@` popup, replace substring filtering with
fuzzy ranking, and support `@path/to/file#L10-20`.

### Current state (verified)

- **`CompleteMention` has no caller in production.** It is used only by
  `completion_test.go`. Mentions are extracted after the fact by
  `extractMentions` (`completion.go:374`), which is called on submit
  (`input.go:135`).
- `extractMentions` splits on whitespace, `@`, and newlines only. A
  `@path#L10-20` used to survive extraction as the literal string
  `path#L10-20`, which `submit` then appended as
  `[Referenced file: path#L10-20]` (`agent_loop.go:1015`) — a filename that does
  not exist. The path/range split is now done at extraction time.
- `CompleteMention` returns **files only** (`matchingFiles` skips dirs,
  `completion.go:315-318`), capped at 20. Directory drill-down requires changing
  `matchingFiles` to emit directories too — the draft assumed this worked.
- `searchPopupState` (`tui.go:303`) has `mode, entries, filtered, selected,
  search, height, scrollOff`. There is no `originalTheme`-style per-mode payload;
  `searchMode` is a `string` with two values.
- `filterSearch` (`tui.go:476`) is `strings.Contains` on text and description, and
  it **fell back to showing all entries when nothing matched** — a query with no
  hits silently redisplayed the full list, so the search appeared to ignore the
  query and Enter acted on an entry the query had excluded. **Fixed**; an empty
  result now stays empty, which is what the popup's "No matching …" row was always
  built for. Guarded by `TestFilterSearch_NoMatchStaysEmpty`.
- **`@path#L10-20` is now split at extraction.** The range used to travel as part
  of the filename, so `submit` emitted `[Referenced file: main.go#L10-20]` and the
  agent was asked to read a file that does not exist. `splitMentionLineRange`
  separates the two; guarded by `TestSplitMentionLineRange` and
  `TestExtractMentions_StripsLineRange`. What remains is *using* the range when
  the popup accepts a mention, and annotating it as a range in `submit` rather
  than dropping it.
- Input state lives in a `textarea.Model`; `InputModel.Text` is derived by
  `syncFromInput`. Any token replacement must go through `SetText`, which also
  moves the cursor to the end.

### Files

- `internal/tui/tui.go` — `searchMode`, `searchPopupState`, `filterSearch`,
  `newSearchPopup`, `acceptSearchPopupSelection`, `handleSearchPopupKey`,
  `handleInputKey`.
- `internal/tui/completion.go` — `matchingFiles` (emit dirs), a scorer.
- `internal/tui/input.go` — cursor-position access for token detection.
- `internal/tui/completion_test.go`, `internal/tui/tui_test.go`.

### Tasks

1. Add `searchModeMentions`. Detect an `@` token immediately before the cursor:
   it must start the buffer or follow whitespace, and may not contain
   whitespace. Split a trailing `#L<start>[-<end>]` off the query and keep it to
   re-apply on accept.
2. Rank candidates with an explicit ladder, not a boolean:
   exact-prefix > path-segment prefix (`/`, `_`, `-`) > contiguous substring >
   subsequence. Tie-break by path length then name, so the ranking is
   deterministic and testable.
3. Fix the `filterSearch` no-match fallback: an empty result must render "No
   matches", not the whole list. That fallback is why the popup felt like it
   ignored the query.
4. On `Tab`/`Enter` in `searchModeMentions`, replace only the `@token`, via
   `SetText`, preserving surrounding text. If the selection is a directory,
   keep the popup open and re-query with the directory prefix.
5. Teach `extractMentions` to split `#L<start>[-<end>]` off a mention, and carry
   the range separately so `submit` can annotate it as a line range rather than
   passing it inside the path.
6. Tests: `@` detection mid-prompt, segment-boundary ranking order, determinism
   under duplicate names, directory drill-down, `#L10-20` preservation, and
   `filterSearch` returning empty rather than everything.

### Acceptance criteria

- Typing `@` anywhere in the prompt opens the popup.
- `@tdg` ranks `internal/tui/tool_display.go` above unrelated paths.
- Selecting a file rewrites only the `@token`.
- `@main.go#L10-20` reaches the agent as a path plus a range, not a 12-character
  filename.

---

## Phase 3 (P1.1) — Inline diff preview and tail-first `bash` output

> **Status: tail-first `bash` and `git-file-diff`/`git-hunk` diff rendering
> implemented; inline `edit` diff unstarted.**

### Objective

Show `edit`/`git-file-diff` hunks inline with diff colours, and tail-collapse
`bash` output so a trailing failure is never hidden.

### Current state (verified)

- **`bashOutputPreview`** (`tool_display.go:1222` on `main`) kept `lines[0], lines[1],
  lines[-2], lines[-1]` — head 2 / tail 2 — and applied it only when
  `len(lines) > 4`. So a 6-line result lost its middle two lines, and a 400-line
  build log kept 4. `maxRegularToolLines = 15` is a *different* clip, in
  `regularToolOutput`, for the expanded card.
- **`edit` cards cannot show a diff from the result.** `EditOutput` is
  `{path, replacements}` only, and `editResultSummary` formats it as
  `"N replacements"`. The `old_string`/`new_string` are in the *call* args at
  emit time (`agent_loop.go:1543`) but are not retained on `message`: the card
  keeps `toolIn`, a one-line summary. An inline diff therefore needs a new
  `message` field for the edit pair, or a diff computed at call time and stored.
  The draft's "when rendering… that has `old_string` and `new_string` in
  `msg.args`" was not available to the renderer.
- `EditOutput` carries no diff, and adding one changes a tool's output schema —
  out of scope per this plan's own "no tool schema changes" rule.

### Files

- `internal/tui/tool_display.go`
- `internal/tui/chat.go` (a field to carry the edit pair, if that route is taken)
- `internal/tui/agent_loop.go` (populate it at call time)
- `internal/tui/tool_display_test.go`, `internal/tui/render_integrity_test.go`

### Tasks (done)

1. ✅ `bashOutputPreview` → head 2 + `  ⋯ (N earlier lines)` + tail 10, with the
   window left untouched when the output fits. Tests rewritten to pin the new
   contract, including the failing-tail case.

### Tasks (remaining)

2. Carry the `edit` call's `old_string`/`new_string` onto the message at
   `handleAgentToolCall` and store a precomputed, capped diff hunk (≤ 12 lines,
   ±2 context) so the render path does no diffing. Render it under the existing
   gutter using `p.DiffAdded*`/`p.DiffRemoved*`, whole-line styled, `ansi`-aware.
3. Tests: a two-line edit renders `-`/`+` with the right palette roles; a large
   edit is capped and says how many lines it elided; I1 holds at 60/80/120/200.

### Acceptance criteria

- `bash` cards preserve the last 10 lines and state how many were elided.
- `edit` cards show a coloured `-`/`+` hunk without a second tool call.
- All rows satisfy I1.

---

## Phase 4 (P1.2) — Transcript grouping for exploration and thinking

> **Status: implemented** on `fix/tui-plan-findings`.

### Objective

Keep read-only exploration and finished thinking from flooding the viewport,
while mutations and failures stay prominent.

### Current state (verified)

- `message` has **no `done` field**; "still running" is `msg.toolPending()`
  (`chat.go:261`: `content == "" || pendingRefresh`).
- `toolBlockKind` (`minimap.go:155`) classified `read, ls, tree, grep, find,
  glob` as `blockRead` and **not `ripgrep`** — so every search on a host with
  `rg` (the common case) fell to `blockTool` and got the neutral rail cell.
  **Fixed** on `fix/tui-plan-findings`; guarded by `TestKindOf/ripgrep_tool`.
  This was a prerequisite for grouping, not a cosmetic detail: a predicate
  keyed on `blockRead` would have silently excluded every search.
- `git-status` and `git-log` are not registered tools. The git tools are
  `git-file-diff`, `git-hunk`, `git-overview`.
- `ChatModel.renderMessages` (`chat.go:745`) walks messages one at a time and
  keys the cache on `(width, CompactTools, hasSeparator, streaming, paletteKey,
  blink, message fields)`. Grouping is **cross-message**, so it cannot live in
  `renderKey`; it belongs in the loop above the per-message cache, and the
  group's key must be the fold of its members' keys.
- `Ctrl+O` currently toggles the `CompactTools` boolean (`tui.go:1324`).
- The minimap is built from `kindOf(msg)` per rendered line
  (`appendKind`, `chat.go:961`), so a coalesced line must still contribute
  `blockRead` spans or the rail desynchronises from the transcript.

### Files

- `internal/tui/minimap.go` (add `ripgrep` and the git inspect tools to
  `blockRead`)
- `internal/tui/chat.go` (`renderMessages` grouping pass)
- `internal/tui/tool_display.go` (a group summary row)
- `internal/tui/tui.go` (`Ctrl+O` cycle)
- `internal/tui/chat_test.go`, `internal/tui/minimap_test.go`

### Tasks

1. ✅ `toolBlockKind` lists `ripgrep` again, so `blockRead` is the honest predicate
   for "read-only exploration". `git-file-diff`/`git-hunk`/`git-overview` stay
   `blockTool`: they are inspection, but `git-file-diff` is also a diff card from
   Phase 1, and folding a diff under an "Explored" summary would hide the change
   being reviewed.
2. ✅ `ToolVerbosity` on `Ctrl+O`: `Smart` (default) → `Expanded` → `Compact` →
   `Smart`, flashing the mode name. `CompactTools` is now *derived* from the mode
   (`== VerbosityCompact`) rather than toggled independently, so the flash and the
   rendering cannot disagree.
3. ✅ `explorationRun` + `renderExplorationGroup` in `chat.go`: a contiguous run of
   completed, error-free `blockRead` cards folds to
   `  ▸ Explored · 5 reads, 3 searches  (ctrl+o to expand)`. A single card is left
   alone — a summary plus a count costs more than the card. The run ends at any
   non-read card, a pending card (`toolPending()`), or a failure
   (`isFailedToolResult`, which reads the JSON `error`/`exit_code` markers that
   tool results actually carry).
4. ✅ A finished thinking block collapses to
   `  ▸ Thought · <first non-blank line, 60 cols> (N lines)` in `Smart` and
   `Compact`; `Expanded` keeps the six-line window, and a live block
   (`ChatModel.Thinking != ""`) always streams.
5. ✅ The fold appends `blockRead` for the lines it produced **into the running
   kinds slice**. An earlier version appended to a fresh slice, which silently
   discarded every kind recorded before the group — the minimap would have lost its
   place while the transcript looked correct. `TestExplorationFoldKeepsFrameIntegrity`
   plus the helper's `len(kinds) == count+1` assertion guard it.
6. ✅ Tests: `TestExplorationGroupFoldsContiguousRun`,
   `TestExplorationGroupBrokenByMutation`, `TestExplorationGroupBrokenByFailure`,
   `TestExplorationGroupKeepsPendingVisible`,
   `TestExplorationGroupSingleCardNotFolded`, `TestCollapsedThinkingInModes`,
   `TestThinkingStaysLiveWhileStreaming`, `TestHandleKey_CtrlO_ToggleTools`,
   `TestExplorationFoldKeepsFrameIntegrity`.

### The cache bug the tests found

`Verbosity` was not part of `message.renderKey`, so switching Smart → Expanded
replayed the previous mode's cached bytes. The alternating-mode test caught it:
expanded view still showed the collapsed `▸ Thought` row. `renderKey` now folds
`verbosity` into the fingerprint. The bug is real for any two modes whose
`CompactTools` agree — Smart and Expanded — which is exactly the pair a user cycles
through first.

### Acceptance criteria

- Default mode folds a contiguous exploration run into one line and never hides
  a pending or failed tool.
- `Ctrl+O` reaches all three states.
- The minimap still matches the transcript.

---

## Phase 5 (P1.3) — Interactive selectors in `searchPopup`

### Objective

Turn bare `/theme`, `/model`, and a timeline command into filterable pickers,
with live theme preview.

### Current state (verified)

- Bare `/theme` appends a rendered list as a message (`commands.go:1060`);
  bare `/model` appends `formatModelInfo()` text (`commands.go:514`). Neither
  opens the popup.
- There is **no `/timeline` command** and no timeline UI. `Ctrl+T` is the
  textarea's transpose binding, so it is not free.
- The model list is not available to the TUI. `Config` has `ModelSwitcher` but
  no lister; enumerating candidates means threading one in (`internal/provider`
  has `ListModels`, currently used only from `internal/cli`).
- Theme persistence exists (`saveThemeToConfig`, `theme.go:221`), but there is no
  preview/revert: `applyTheme` (`tui.go:518`) always commits.

### Files

- `internal/tui/tui.go`, `internal/tui/commands.go`, `internal/tui/types.go`
- `internal/tui/tui_test.go`

### Tasks

1. Give `searchPopupState` a `payload any` field (or per-mode sub-structs) to
   carry the original theme, the candidate list, and the scroll targets.
2. `/theme` with no args opens `searchModeThemes` listing `ThemeManager.List()`
   with a 🌙/☀️ variant column. `Up`/`Down` preview via `applyTheme`; `Enter`
   persists via `saveThemeToConfig`; `Esc` restores the captured original.
3. `/model` with no args opens `searchModeModels` over configured `Roles` plus,
   if a lister is threaded in, provider models. Show role tags
   (`default`/`smol`/`slow`/`plan`); `Enter` calls the existing switch path.
4. Add `/timeline` (not a `Ctrl` chord) listing user turns with index and a
   truncated preview; `Enter` sets `chatModel.Scroll` to that message boundary.
   Scrolling to a boundary needs a mapping from message index to rendered line,
   which `renderMessages` already computes in `kinds`; expose it rather than
   recomputing.
5. Render right-aligned key hints in the popup, inside `searchPopupChrome`.
6. Tests: preview/commit/revert, model selection, timeline jump offset.

### Acceptance criteria

- `/theme` previews on move, saves on Enter, restores on Esc.
- `/model` opens a picker.
- `/timeline` jumps to a chosen turn.

---

## Phase 6 (P1.4) — Subagent inspector and per-agent interrupt

> **Status: implemented** on `fix/tui-plan-findings`.

### Objective

Inspect any subagent's event stream beyond the 3-line card window, and cancel one
agent.

### Current state (verified)

- The card window is `maxAgentOutputLines = 3` (`tool_display.go:459`),
  deliberately shrunk from 7 in `b98515f`.
- `/subagents` exists and appends `formatAgentsList(Orchestrator.List())` as
  text (`commands.go:464`). `AgentStatus` is
  `{AgentID, Type, Status, Prompt, StartedAt, Duration}` — there is **no event
  buffer** on it, so an inspector must read the events the TUI already holds on
  `message.agentEvents`.
- Cancellation exists: `Orchestrator.Cancel(agentID)`, which errors on a
  non-running agent (`orchestrator.go:817`). What is missing is a UI path that
  names a specific agent, not the capability.
- `Ctrl+X` is free; `Ctrl+T` is not.

### Files

- `internal/tui/tui.go`, `internal/tui/tool_display.go`, `internal/tui/commands.go`

### Tasks

1. ✅ `/subagents` now opens `searchModeAgents` instead of appending text. Each
   row carries the status icon, `type · status · duration`, and the first line of
   the prompt as its title. Running agents sort first, so a live one is reachable
   without scrolling past everything that finished. `SearchItem` gained
   `AgentID` and `Running`: the ID is what makes a row actionable, the flag is
   what gates the cancel key rather than surfacing the orchestrator's refusal as
   a surprise.
2. ✅ `Ctrl+X` in the picker cancels the selected agent through
   `Orchestrator.Cancel(id)` and flashes the orchestrator's own wording on
   refusal, so "not running (status: completed)" reaches the user instead of the
   popup closing as if it had worked. The key is handled in `handleToggleKey`,
   not `handleSearchPopupKey`, because cancelling produces a flash command and
   that helper returns only a bool; it stays unswallowed in every other popup
   mode, which is what the existing `ctrl+x must not be swallowed as search text`
   test pins.
3. ✅ Accepting a row sets `model.expandedAgent`, published each frame to
   `ToolDisplayModel.ExpandedAgentID`. That card renders
   `expandedAgentOutputLines = 25` rows instead of `maxAgentOutputLines = 3`.
   Expansion is a property of *what the user is looking at*, not of the card:
   only one is expanded at a time, selecting another moves it, and re-selecting
   collapses. No second event source — the card already owns the stream, and an
   agent listed by the orchestrator with no card in this transcript (after
   `/resume` or a compaction) says so rather than doing nothing.
4. ✅ Tests: `TestAgentSearchItems`, `TestExpandAgentCard`,
   `TestExpandAgentCardMissingReported`, `TestExpandedAgentWidensCardWindow`,
   `TestCancelSelectedAgentReportsOrchestratorError`.

### One thing the implementation had to fix to exist

Opening a popup from a slash command exposed an **initialization cycle** the
compiler rejected: `slashCommandSpecs` holds the handler funcs, a handler now
opens a search popup, the popup gathers candidates, and those read
`slashCommands` / `slashCommandByName` — which were themselves package-level
initializer expressions derived from `slashCommandSpecs`. Both derived tables are
now populated in `init`, which runs after every variable is initialized and so
adds no edge to that graph.

---

## Phase 7 (P2.1) — Sidebar budget-aware section folding

> **Status: implemented** on `fix/tui-plan-findings`.

### Objective

Stop `sidebarFrame` from silently clipping lower sections on short terminals.

### Current state (verified)

- `sidebarFrame` (`sidebar.go:778`) pads to `targetH`, then does
  `contentLines = contentLines[:targetH]` (`:803`). Silent, bottom-first
  truncation.
- `RenderSidebar` (`sidebar.go:242`) already makes one budget decision: it tries
  a merged plan+graph view and keeps it only if it fits, and `graphSection`
  drops the diagram whole rather than clipping it mid-branch. So the codebase's
  existing answer to "too tall" is drop-whole, not fold — this phase generalises
  that with a visible count.
- Section order is fixed in `sidebarHeadLines` (`:274`) and `sidebarTailLines`
  (`:290`). There is no priority taxonomy today.
- `SidebarWidth = 23`, not 30.

### Files

- `internal/tui/sidebar.go`, `internal/tui/sidebar_test.go`

### Tasks

1. ✅ Render each section as its own slice (`sidebarSections`) instead of
   concatenating as it goes. `sidebarSectionNames` fixes the panel order, and
   keeping the slices separate is what lets a fold touch one section without its
   neighbors.
2. ✅ When the total exceeds `sidebarContentHeight`, fold sections bottom-up in a
   fixed cheapest-to-lose order (`foldableSectionIndexes`): loading, skills, mcp,
   a2a, mood, memory, goal. Folding keeps the heading and drops the item rows —
   the heading carries the count, so the panel still answers "did my skills load,
   and how many". Folded rows get a ` +N` marker after an explicit
   `ansi.ResetStyle`, or the heading's style bleeds into the marker and the row
   measures wider than it renders (I4, I1).
3. ✅ Every section keeps its heading. The old unconditional
   `contentLines[:targetH]` clip is now unreachable for section content: the fold
   loop runs until the body fits, and if it somehow cannot, clamping still
   happens last — but the tests assert it never gets there.
4. ✅ Tests: `TestSidebarFoldKeepsEveryHeading` (every heading at heights
   45/34/30/24/20), `TestSidebarFoldReportsWhatItHid` (the `+N` marker appears
   only when something was actually folded), `TestSidebarFoldWidths` (I1 at two
   widths across four heights).

### What changed from the draft, and why

The draft proposed folding *whole sections* into summaries like `▸ Skills [12]`.
Checking the builders showed that is almost free: `sidebarSkillLines` already
emits exactly one heading row. The sections that actually cost height are the ones
with per-item rows — memory (counters per room), MCP (a row per server), A2A (a row
per agent), loading (a row per subsystem). So the implemented rule is "keep the
heading, drop the detail", which folds a section to one row *whatever* it contained
and needs no per-section summary rendering. It also degrades better: a 24-row
terminal folds two sections instead of losing two.

Reproducing the old behavior (`foldable := []int{}`) makes the heading test fail at
height 24 (`Loading vanished`) and height 20 (`A2A Agents`, `Loading` vanished), so
the guard is real rather than self-referential.

### Acceptance criteria

- On a 24-row terminal every section is either visible or counted.
- Every sidebar row satisfies I1.

---

## Phase 8 (P2.2) — Double-`Esc` interrupt and `Ctrl+S` draft stash

> **Status: implemented** on `fix/tui-plan-findings`.

### Objective

Stop a single stray `Esc` from killing a turn, and let users park a draft.

### Current state (verified)

- `handleInterruptKey` (`tui.go:1278`) cancels on the **first** `Esc` while
  running. There is no confirmation and no arming.
- The transient-message mechanism is `m.setFlash(text)` →
  `tea.Tick(flashDuration = 3s)` → `flashExpiredMsg{seq}`, with a sequence guard
  so a stale timer cannot clear a newer message (`tui.go:1035-1046`).
- `InputModel` embeds `textarea.Model`; `Text`/`CursorPos` are a mirror updated
  by `syncFromInput`. A stash must round-trip through `SetText`, or the textarea
  and the mirror diverge and the next keystroke resurrects the stash.
- `Ctrl+S` is free in the textarea keymap; `Ctrl+T` (transpose) is not.

### Files

- `internal/tui/tui.go`, `internal/tui/input.go`, `internal/tui/tui_test.go`

### Tasks

1. ✅ Added `escArmedUntil time.Time` — a deadline rather than a bare
   `lastEscTime`, because the rule is "a second Esc within N seconds" and an
   absolute instant makes that readable without arithmetic. Window is
   `escArmWindow = 2s`, matching Ctrl+C's reset window.
2. ✅ First `Esc` while running and no popup: arm, `setFlash("esc again to
   interrupt")`, return without cancelling. Second `Esc` before the deadline:
   cancel and clear the arm. After it: re-arm. The arm is also cleared in
   `cancelAgent` and `handleAgentDone`, so a turn that ends on its own cannot
   leave a deadline armed for the next one.
3. ✅ `Ctrl+S` stashes a non-empty prompt via `Stash()` and restores via
   `PopStash()`, both through `SetText` so the textarea and the `Text` mirror stay
   in agreement. Multi-line content is preserved verbatim.
4. ✅ Tests: `TestEscArmsThenInterrupts`, `TestEscArmExpires`,
   `TestEscArmClearedWhenTurnEnds`, `TestEscDismissesPopupWithoutArming`,
   `TestCtrlSStashesAndPopsDraft`, `TestCtrlSLIFOOrder`,
   `TestCtrlSStashesAfterEditingRestoredDraft`, `TestCtrlSInFrame`.

### One correction to the draft

The draft said "if the input is empty and `len(stash) > 0`, pop". That rule makes
the stash unwalkable: a pop leaves the prompt non-empty, so the next press
re-stashes the draft it just restored and the entry underneath is unreachable —
which the LIFO test caught immediately. The implemented rule is "park what the
user typed; pop once the prompt is empty *or* still holds the draft Ctrl+S just
handed back". An edit clears that `stashPopped` flag, so an edited draft is
parked rather than silently discarded.

### Acceptance criteria

- A single `Esc` while running shows the arming flash and does not abort.
- `Ctrl+S` parks and restores a multi-line draft.

---

## Verification matrix

Before opening or updating a PR for any phase:

```bash
go test -race -count=1 ./internal/tui/...   # unit + render integrity
make test-unit
make vet
golangci-lint run ./...                     # make lint

# Per AGENTS.md — all commits signed. GitHub-created merge commits fail the
# local check by design; only commits in the push range matter.
git log --format='%h' origin/main..HEAD | while read c; do
  printf '%s ' "$c"; git verify-commit "$c" >/dev/null 2>&1 && echo VALID || echo ">>> INVALID <<<"
done
```

Signing note for this machine: `SSH_AUTH_SOCK` defaults to Apple's empty
listener, and `op-ssh-sign` then fails with `1Password: failed to fill whole
buffer`. The 1Password agent lives at
`~/Library/Group Containers/2BUA8C4S2C.com.1password/t/agent.sock`. Even with
that socket, the agent reported the key via `ssh-add -l` but refused to sign
(`communication with agent failed`), so signing had to be unblocked at the
1Password end before any commit could land.
