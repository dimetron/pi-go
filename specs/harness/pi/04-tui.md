# 04 — Terminal UI

## Side-by-side summary

| | pi-go | pi |
|---|---|---|
| LOC | 19,923 | 19,182 |
| Framework | Bubble Tea v2 | own renderer |
| Screen modes | 1 (normal) | 2 (main + alt, hot-swappable) |
| Differential rendering | ✗ (delegated) | ✓ |
| Synchronized output | ✗ | ✓ CSI 2026 |
| Bounded writes | ✗ | ✓ 1 MiB, surrogate-safe |
| Keybindings | ~10 hard-coded | 45 IDs + conflict detection |
| Editor | single-line | multiline + kill ring + undo |
| Native modules | ✗ | ✓ clipboard + modifiers |
| Inline images | ✗ | ✓ Kitty + iTerm2 |
| Markdown | glamour | marked + custom renderer |
| Highlighting | chroma (in glamour) | highlight.js (injectable) |
| Sidebar / minimap | ✓ | ✗ |
| Render cache | ✓ FNV-1a | △ per-component |

## 4.1 Terminal architecture

### pi-go

Bubble Tea v2 (`charm.land/bubbletea/v2 v2.0.9`) + lipgloss/v2 + bubbles/v2
`textinput` + glamour + `charmbracelet/x/ansi`. **Only `textinput` and
`spinner` from bubbles** — viewport and list are hand-rolled.

Root model is unexported `struct` at `internal/tui/tui.go:28`: `Init()` `:586`,
`Update()` `:663`, `View() tea.View` `:1525`, launched at `:573`.

**Rendering mode: main screen, NOT alt-screen.** `v.AltScreen = false`
(`tui.go:1701`); `v.MouseMode = tea.MouseModeCellMotion` (`:1702`).

There is no alt-screen mode. Instead a **full RIS reset before `tea.NewProgram`**:
`prepareTerminal()` (`terminal_prepare.go:32`), sequence `"\x1bc"` (`:56`). The
rationale (`:11-31`) is that JCEF-based embedded terminals corrupt the first
frame's Unicode cells unless fully reset. `drainTerminalResponses()` runs first
(`:33`) so stale terminal replies (OSC color, DECRPM, CPR) are not parsed as
keystrokes.

A compat shim (`terminal_compat.go:22-42`) downgrades JediTerm's advertised
xterm-256color to `TERM=linux` so lipgloss avoids tab movement — scoped to Bubble
Tea only, so subprocesses keep the real TERM.

**Update routing** is a 4-handler first-match-wins chain (`tui.go:668-673`):
`updateTerminal`, `updateAgentStream`, `updateRunWorkflow`, `updateSession`.
The comment at `:676-682` explains why there is deliberately no blanket
`waitForAgent` re-arm.

### pi

**Two runtime deps total**: `marked@18.0.11` + `get-east-asian-width@1.6.0`. No
framework.

Core types in `packages/tui/src/tui.ts` (1,493 LOC): `Component` `:117`,
`Focusable` `:180`, `OverlayHandle` `:298`, `Container` `:347`, `TUI` `:453`,
`ViewportTUI` `:486`, `TuiBase` `:495`.

**Cursor model**: a zero-width APC sequence embedded in render output,
`CURSOR_MARKER = "\x1b_pi:c\x07"` (`:196`). The TUI strips it and positions the
hardware cursor — for **IME candidate window placement**. pi-go gets this from
Bubble Tea for free.

**Component protocol**: 4 methods — `render(width)`, `handleInput?(data)`,
`handleMouse?(event)`, `invalidate()` + `wantsKeyRelease?` (`:117-144`).

Two screen modes: `TuiMainScreen` (`tui-main-screen.ts:123`) and `TuiAltScreen`
(`tui-alt-screen.ts:201`, implements `ViewportTUI`). **`interactive-mode.ts`
swaps between them at runtime** (`:889`, `:909` capturing/restoring
`TuiMainScreenRenderState`).

## 4.2 Differential rendering — pi's strongest TUI asset

### The algorithm

`TuiMainScreen.doRender()` (`tui-main-screen.ts:242-575`), in order:

1. Compute `width/height`, detect size change (`:244-245`)
2. `newLines = this.render(width)` — **full re-render every frame** (`:255`)
3. **Composite overlays before the diff** (`:259-261`) so overlay changes
   participate in the diff rather than bypassing it
4. `extractCursorPosition` **before** `applyLineResets` (`:264-266`)
5. **Full-redraw triggers** (each returns early):
   - first render (`:324-328`)
   - width changed — *"wrapping changes"* (`:331-335`)
   - height changed **unless Termux** (`:338-343`): Termux changes height when the
     software keyboard toggles, and a full redraw replays the entire history
   - `clearOnShrink` + content shrank below working area + no overlays (`:348-352`)
6. **Otherwise: single linear scan** for first/last changed line (`:356-372`)
7. Appended-lines fast path: `lastChanged = newLines.length - 1` (`:373-380`)
8. **Kitty image range expansion** (`:382-387`): a changed line adjacent to a
   multi-row image expands the range to cover the whole image block, because a
   partial redraw of a Kitty image row corrupts it
9. No change → reposition cursor only, return (`:390-395`)
10. All changes in deleted lines → `\r\x1b[2K` clearing loop (`:399-448`)
11. **Viewport guard**: `if (firstChanged < prevViewportTop) fullRender(true)`
    (`:451-455`) — *"Differential rendering can only touch what was actually
    visible"*
12. Scroll-into-view via relative moves (`:461-473`)
13. Write only `[firstChanged..lastChanged]` (`:488`) — the comment names the
    motivation: *"This reduces flicker when only a single line changes (e.g.,
    spinner animation)"*
14. **Width-overflow guard** (`:521-547`): if `visibleWidth(line) > width` for a
    non-image line, it writes a crash log with every rendered line, calls
    `this.stop()`, and throws naming `visibleWidth()`/`truncateToWidth()`

**Write chunking** — `BoundedTerminalWriter` (`:19-70`),
`MAX_RENDER_WRITE_CHARS = 1024*1024`: *"Streams terminal output in 1 MiB chunks so
a full render never forms one string large enough to exceed V8's limit."*
`append()` splits at chunk boundaries **preserving surrogate pairs** (`:38-47`).

**Synchronized output (CSI 2026)** — constants at `tui-alt-screen.ts:71-72`, used
in main-screen at `:280`, `:302`, `:403`, `:435`, `:460`, `:567`. Every emit path
wraps the whole write in begin…end — no path is unwrapped. The terminal buffers
the frame and presents it atomically, so a multi-line diff never shows as a
partially-updated screen.

`TuiAltScreen.doRender()` (`:1670-1754`) is a **different, absolute-addressed**
algorithm:

```ts
// tui-alt-screen.ts:1737
buffer += `\x1b[${row + 1};1H\x1b[2K${preparedKittyScreen.lines[row] ?? ""}`
```

Compositing order at `:1680-1686`: layout → strip OSC133 zone markers → search
highlights → scroll-to-end indicator → overlays → clip to height → selection
highlight → flashes → cursor extract → line resets + width clamp.

**WezTerm special case** (`:1723-1733`): WezTerm erases intersecting Kitty image
cells when a later EL clears a covered row, so for WezTerm frames containing
images the clears are hoisted into a separate pass *before* any drawing —
detected by `WEZTERM_PANE` env or `TERM_PROGRAM === "wezterm"`.

**Debug instrumentation**: `PI_TUI_DEBUG_REDRAW=1` writes every full-redraw reason
to `pi-tui-debug.log` (`:305-317`), e.g. `"terminal width changed (80 -> 100)"`,
`"kitty image pre-clear would scroll"`, `"firstChanged < viewportTop"`.

### Review — rendering

**This is the one domain where pi-go is strictly behind, and it is a framework
choice rather than an execution gap.** Bubble Tea v2's standard renderer does not
differential rendering, does not emit CSI 2026, and does not chunk writes. Getting
any of it means replacing the renderer.

**Does pi-go need it?** The evidence says no. pi-go's `View()` composes a single
string per frame into a `strings.Builder` (`tui.go:1540-1581`) and the dominant
cost is *markdown rendering*, not terminal I/O — which is why pi-go has an FNV-1a
render cache (§4.4) and why `record-pgo` merges the TUI render loop into the PGO
profile (a live profile attributed ~24% of CPU to `View()`-per-token rendering).

**The specific thing pi-go loses is flicker on partial updates.** Bubbletea
redraws the whole frame; on a slow terminal with a spinner ticking, that is
visible. pi's synchronized-output wrapping is the direct mitigation and is
roughly 20 lines if bubbletea allows escape injection in `View()` output (it
does — `\x1b[?2026h` / `\x1b[?2026l` around the composed frame).

**Two cheap wins:**

1. Wrap every frame in CSI 2026 begin/end. Prevents partial-frame tearing.
2. Adopt the **viewport guard**: Bubble Tea cannot know what is visible, but
   pi-go's `clipMessagesToViewport` (`tui.go:1562`) already computes
   `visibleMessages, startLine, endLine` — so pi-go knows exactly which lines
   changed and could emit a targeted clear+rewrite.

Neither requires forking Bubble Tea.

## 4.3 Markdown & syntax highlighting

### pi-go

**Renderer**: `glamour` v1.0.0. `newMarkdownRenderer` (`chat.go:466-472`) —
`WithStyles` + `WithWordWrap(width)` + `WithEmoji`. Rebuilt on resize
(`UpdateRenderer` `chat.go:475`) and **on theme change** (`RefreshTheme` `:496`),
because the glamour stylesheet is baked into the `TermRenderer` and invisible to
the render key.

`markdownStyleFor` (`chat.go:439-464`) contains three hard-won fixes:

1. **Style chosen from the palette, never probed.** `glamour.WithAutoStyle` would
   emit an OSC-11 query;
   `TestUpdateRendererDoesNotQueryTerminalBackground` enforces this.
2. **Chroma `Error` background dropped.** Both stock stylesheets paint unknown
   tokens white-on-red, and chroma's fallback lexer classes box-drawing runes
   (the repo's own README tree diagrams) as errors → a column of red bars. Fixed
   by `chroma.Error = gansi.StylePrimitive{Color: chroma.Text.Color}` (`:460`),
   after copying `chroma := *cfg.CodeBlock.Chroma` because the StyleConfigs are
   package-level vars and Chroma is a pointer (`:457-461`).
3. **Light-theme override**: glamour's salmon (ANSI 203) inline code is ~3:1 on
   light gray, under the AA floor → overridden to palette Mauve/Surface0 (`:446-452`).

**Highlighting**: `chroma/v2 v2.27.0`, used inside glamour for fenced code, plus
`highlightToolOutput` (`tool_display.go:701`).

**Render cache** — FNV-1a inlined, not `hash/fnv` (which heap-allocates an
interface per message per frame): `chat.go:160-183`. `renderKey` (`:197`)
fingerprints message fields + width + tool mode + positional flags + theme key +
blink. The comment at `:187-196` records that the cache used to be **disabled
while running**, which re-rendered the whole scrollback several times a second.

**Mermaid**: `RenderMermaid(source, width, p Palette)` (`mermaid.go:85`). Fences
are split out (`splitMermaidFences` `:277`) and the art spliced in *after*
glamour runs, because glamour treats escape bytes as literal text
(`chat.go:613-619`). Unrenderable fences get a stabilized language so the block
does not shimmer (`stableFenceLang` `mermaid.go:253`).

**Links**: markdown links expanded to inline; bare HTTP(S) URLs wrapped in
**OSC 8** (`chat.go:522-524`).

### pi

**Parser**: `marked@18.0.11` — the only markdown dep, re-exported from the package
root. Then a hand-written TUI renderer.

Two custom tokenizer extensions in `components/markdown.ts`:

1. **`StrictStrikethroughTokenizer`** (`:10-26`) — replaces marked's `~~del~~` with
   a regex requiring no whitespace after `~~` and a matching non-space before the
   closing `~~`. GFM-compliant.
2. **LaTeX tokenizer** (`:27-100+`) — `tokenizeInlineLatex` handles `$$…$$`,
   `\(…\)`, `\[…\]`, `$…$`, with `findClosingDelimiter` skipping backslash-escaped
   delimiters (`:39-44`) and four explicit **false-positive rejections** (`:64-77`):
   trailing whitespace, a digit immediately after the closing `$` (currency), an
   all-caps identifier (env var), or a backtick inside. Emits `latex`/`latexBlock`
   with a `pending` flag so half-typed math renders live.

`latex.ts` is 1,506 LOC of TeX rendering.

**Highlighting is a host-injected callback, not a dependency**
(`markdown.ts:215` `highlightCode?: (code, lang) => string[]`), supplied by
coding-agent at `theme.ts:1003-1022`:

```ts
export function highlightCode(code: string, lang?: string): string[] {
  const validLang = lang && supportsLanguage(lang) ? lang : undefined;
  if (!validLang) return code.split("\n").map((line) => theme.fg("mdCodeBlock", line));
  return highlight(code, opts).split("\n");
}
```

The language is validated **before** highlighting — comment at `:1007-1009`:
"to avoid stderr spam from cli-highlight" and because "cli-highlight's
auto-detection is unreliable and can misidentify prose as AppleScript". Backed by
`highlight.js@10.7.3`.

**Mermaid**: `components/mermaid.ts` (89) — a `MarkdownTransformer` backed by
`grok-mermaid`, with a per-mode dispatch (`off`/`final`/`streaming`, default
`streaming`).

**Markdown transformers** (`markdown-transform.ts`) — extensions can rewrite
markdown given `{messageType, isStreaming, availableWidth}`.

### Review — markdown

**The two approaches are genuinely different, and each has a cost the other
avoids.**

glamour (pi-go) produces a complete styled terminal document. That gives correct
ANSI handling, wrapping, and styling for free, and it is why pi-go's markdown
"just works". The costs are: the stylesheet is baked in at construction (so theme
switches rebuild the renderer and invalidate every cache), auto-detection is
unusable (must be patched out), and its default error styling is actively wrong
on box-drawing content.

marked + custom renderer (pi) gives full control over composition order,
caching, and streaming, and enables features glamour has no concept of (LaTeX,
strict GFM strikethrough, per-block transformers). The cost is writing and
maintaining a renderer, and reimplementing what glamour provided.

**pi-go's chroma fix is the interesting one.** pi avoids the problem by
validating the language before highlighting, so the fallback lexer never runs on
unknown content. pi-go patches the symptom (`chroma.Error` → text color) which is
correct but leaves the fallback lexer still classifying box-drawing runes as
errors — it just renders them invisibly. The better fix is the same as pi's:
validate the fence language against a supported set and skip highlighting
entirely when unsupported.

**The glamour coupling is a real cost.** Every theme switch invalidates the whole
render cache. pi's injectable `highlightCode` + OKLCH re-derivation makes a theme
switch a palette lookup. With 8 themes and a session-long cache, this matters.

**Neither side has clickable links that actually open.** pi-go emits OSC 8 but
never activates it. pi has `openUrl` (`tui-alt-screen.ts:186`) and
`getOsc8LinkAtColumn` (`utils.ts:359`) with `pressedUrl` tracking (`:222`) — but
only in alt-screen mode, and pi-go has no alt-screen mode, so this needs both the
activation and the screen mode.

## 4.4 Layout & panels

### pi-go

`View()` composes the frame by string concatenation (`tui.go:1540-1581`):
main/chat/sidebar widths, `clipMessagesToViewport`, overlay search popup, `hr`
separator, optional matrix rain bar.

| Panel | File | Detail |
|---|---|---|
| Chat | `chat.go` (1,030) | messages, scroll, rendering (hot path — PGO target) |
| Input | `input.go` (385) | bubbles `textinput` |
| Status | `status.go` (274) | bottom bar, `Eyes string` from mascot mood |
| Tool display | `tool_display.go` (1,458) | per-kind renderers, failure sniffing |
| **Sidebar** | `sidebar.go` (829) | **`SidebarWidth = 23`** (`:31`); mascot, mode, provider/model/thinking, git branch, context line, diff stats, run cycle, folder, host, **live subagent list** (`sidebar.go:544`, running first, `:565`), compiled SOP graphs |
| Layout | `layout.go` (116) | Narrow <60, Normal 60-100, Wide >100 |
| **Minimap** | `minimap.go` (381) | **`railWidth = 1`** (`:19`) — the minimap *is* the panel separator, not a strip beside it. Glyphs `│ / ◉ / ┴`; `thumbRows = 3` fixed |
| Setup wizard | `setup_wizard.go` (529) | first-run provider wizard |
| Face | `face.go` (143) | mascot + `AgentMood` |
| Theme | `theme.go` (248) + `palette.go` (234) | `themes.json`, contrast-tested |
| Completion | `completion.go` (416) | autocomplete + `@` mentions |
| SOP graph | `sop_graph.go` (211) | 5 stage states |
| Context breakdown | `context_breakdown.go` (319) | gauge segmented by origin |
| Context rule | `context_rule.go` (344) | rule below input doubles as context bar |
| Others | `plan.go` (488), `run.go` (2,146), `prautofix.go` (364), `commit.go` (287), `login.go` (503), `ping.go`, `matrix.go`, `selection.go`, `collapse.go`, `history.go`, `create_skill.go`, `refs/` | |

**Width-safety is treated as a correctness invariant.** `mascotEars` uses ASCII
`/\___/\` not `╱ ╲` (U+2571/2572) because those are East Asian Ambiguous and
render 2 cells wide, shoving the whole sidebar out of alignment (`face.go:34-36`).
Same reason every face glyph is Neutral/Narrow: `★` and `◑` were ambiguous and made
the happy/processing faces one cell wider than the rest (`:44-47`). Test:
`TestMascotGlyphsAreWidthSafe`. The minimap uses `◉` (U+25C9) not `●` (U+25CF)
for the same reason (`minimap.go:23-27`).

**Context gauge calibration**: `context_rule.go:13-33` — 100% on the rule means
*entered the dumb zone* (≥70% of window), not 100% of window. Zones green/peach/red.

`ContextBreakdown` (`context_breakdown.go:87`) segments the gauge **by origin**:
system prompt, tools, rules, skills, MCP, subagents.

**Mouse selection is hand-rolled** (`selection.go:11-19`): enabling mouse
reporting takes click-drag away from the terminal, so pi-go re-implements
drag-select + copy-on-release rather than requiring a bypass modifier.

### pi — 50 interactive components

`packages/coding-agent/src/modes/interactive/` (21,653 LOC) plus
`packages/tui/src/components/` (16 widgets).

**Core (`packages/tui/src/components/`)**: `Text` (108), `TruncatedText` (65),
`Input` (494), `Editor` (2,472), `Markdown` (1,025), `Loader` (101),
`CancellableLoader` (40), `SelectList` (273), `SettingsList` (328),
`ScrollView` (224), `MouseRegion` (33), `Spacer` (28), `Box` (167),
`stack.ts` (154 → `VStack`/`HStack`), `Image` (127),
`AltScreenFlashContainer` (51).

**Host components**, grouped:

| Group | Components |
|---|---|
| Messages | `assistant-message`, `user-message`, `tool-execution` (+ per-tool renderers in `core/tools/renderers/`), `bash-execution`, `diff`, `markdown-transform`, `mermaid`, `branch-summary-message`, `compaction-summary-message`, `skill-invocation-message`, `custom-message`/`custom-entry` |
| Selectors | `model-selector`, `scoped-models-selector`, `thinking-selector`, `session-selector`(+`-search`), `tree-selector`, `settings-selector`(+`-submenu`), `theme-selector`, `oauth-selector`, `radius-login-selector`, `trust-selector`, `show-images-selector`, `config-selector` |
| Dialogs | `login-dialog`, `auth-url`, `first-time-setup`, `pi-logo`, `earendil-announcement`, `armin` |
| Chrome | `footer`, `status-indicator`, `keybinding-hints`, `countdown-timer`, `bordered-loader`, `custom-editor`, `visual-truncate` |
| Easter eggs | `easter-egg-3d` (1,292), `armin` |
| Top-level | `interactive-mode.ts`, `chat-viewport.ts`, `tui-renderer.ts`, `external-editor.ts`, `model-search.ts`, `model-catalog-refresh.ts`, `session-share.ts`, `bug-report.ts`, `theme/*.json`, `assets/*.png` |

Session features: tree navigation + fork/clone, compaction UI, **HTML export**
(`core/export-html/`), **session share via secret GitHub gist**, **bug report
upload**, keybinding hints, image display, dynamic border resize.

### Review — layout & panels

**pi-go's sidebar and minimap have no pi equivalent and are a genuine product
asset.** The sidebar carries live subagent list, git branch, diff stats, run
cycle, and compiled SOP graphs — a persistent operational dashboard. pi puts
session/model state in dialogs and a footer.

pi-go's `railWidth = 1` minimap design (the minimap *is* the separator) is a
cleaner use of horizontal space than a separate strip, and its fixed
`thumbRows = 3` is honest about not being proportional.

**Both are obsessive about East Asian Ambiguous width, from opposite directions.**
pi-go treats it as a correctness invariant with named tests and comments citing
the specific codepoint. pi ships `get-east-asian-width` as a dependency and
implements `visibleWidth` (`utils.ts:250`), used everywhere. pi-go's approach
catches its own mistakes; pi's makes mistakes impossible. Both are correct; pi-go's
is more legible to a reader.

**pi's selector set is much richer** — 12 selectors vs pi-go's 4 screens
(`selection.go`, `login.go`, `plan.go`, `create_skill.go`). Several are things
pi-go lacks entirely: scoped-models (Ctrl+P cycling), tree-selector (branch
navigation in a session tree — pi-go's `/branch` is a different shape), trust
selector, image display toggle.

**pi-go's `/plan` and `/run` are the bigger product surface** — `run.go` at 2,146
LOC drives the SOP engine, `prautofix.go` at 364 is a PDD workflow. pi has no
equivalent; its closest is 3 prompt-template presets as slash commands.

## 4.5 Input, keybindings, editor

### pi-go

**Input**: wraps bubbles `textinput.Model` (`input.go:73`) — **single-line**.
`HandleKey` delegated wholesale (`tui.go:1402`).

**Terminal-response leak filter**: `terminalResponseRe` (`input.go:39-44`) —
CSI CPR, DECRPM tails, hex triplets, `rgb:`, OSC intro. Plus a
`resizeDraining()` gate in `handleKeyPressMsg` (`tui.go:718-722`) suppressing text
fragments for a moment after resize.

**Key bindings** — all hard-coded, no registry:

| Key | Action | Ref |
|---|---|---|
| Esc | close popup / cancel turn | `tui.go:1255-1263` |
| Ctrl+C | cancel+warn → 2nd press quits, 2s reset | `:1265-1279` |
| F12 | no-op (swallowed) | `:1281` |
| Ctrl+R | retry last failed prompt | `:1293` |
| Ctrl+O | toggle compact/expanded tool output | `:1299` |
| Ctrl+B | branch popup | `:1307` |
| Ctrl+H | history search popup | `:1329` |
| Up / Down | history popup / chat scroll | `:1352-1359` |
| PgUp / PgDn | chat page | `:1366-1373` |
| Tab | popup cycle (Shift+Tab reverses) | `:1428` |
| Enter | submit | `:1431` |

Precedence (`tui.go:1126-1136`): overlays get first refusal **even while running**,
then loading blocks everything (`:1139`), then toggles/history/scroll, then input.

**Completion** (`completion.go`): `CompletionType` = None/Command/Skill/Spec/File.
Commands + skills for `/`. **`@mentions` are forward-slash paths**
(`findMentionAtCursor` `:355`, `extractMentions` `:373-390`); comment at `:291`:
*"@mentions are written with forward slashes — that is what a user types."*

**24 slash commands** (`slashCommandSpecs` `commands.go:97-135`). Order is
load-bearing: `/run` and `/pr-autofix` precede `/retry` so `/r` completes to
`/run` (`:122`). Hidden: `/skill-list`, `/skill-load`, `/skill-create`.

### pi — keybindings system

`packages/tui/src/keybindings.ts` (320 LOC).

`Keybindings` interface (`:9-58`) is a **declaration-merging point**: downstream
packages add their own IDs. **45 IDs across 5 namespaces**:
`tui.editor.*` (23), `tui.input.*` (4), `tui.select.*` (6), `tui.altScreen.*` (12).

`TUI_KEYBINDINGS` (`:61-207`): every entry `{defaultKeys, description}`. **Multiple
keys per action is first-class**: `cursorLeft: ["left", "ctrl+b"]`,
`deleteCharForward: ["delete", "ctrl+d"]`.

`KeybindingsManager` (`:229-315`):
- `rebuild()` (`:236-262`) — user bindings **fully replace** the default for that
  action, and builds a `userClaims` map to **detect conflicts** (`:249-253`):
  one key claimed by >1 user binding is pushed to `this.conflicts`
- `matches(data, keybinding)` (`:264`), `getConflicts()` (`:283`),
  `getResolvedBindings()` (`:296`)
- Global singleton (`:303-313`)

**Idempotent scopes**: `tui.altScreen.*` IDs *intentionally shadow* the unmodified
editor bindings in fullscreen mode (`:150-151`).

`keys.ts` (1,401 LOC): `KeyId` is a **compile-time-checked template-literal union**
(`:141-152`), so `"ctrl+shift+alt+meta+z"` is a type error.

### The editor widget

`components/editor.ts` — `class Editor implements Component, Focusable` (`:296`),
**2,472 LOC, the largest file in `packages/tui`**.

- **Multiline**: `tui.input.newLine: ["shift+enter", "ctrl+j"]` (`keybindings.ts:135`)
- **Not a modal editor.** Insert-mode-only, Emacs-shaped. No normal/visual/operator
  modes, no `hjkl`, no counts, no macros
- **Kill ring** (`kill-ring.ts`, 46 LOC): `push(text, {prepend, accumulate})` —
  consecutive kills merge into one entry, prepending for backward deletion and
  appending for forward. `peek()` `:28`, `rotate()` `:33` for yank-pop
  (`ctrl+y`/`alt+y`)
- **Undo** (`undo-stack.ts`, 28 LOC): `structuredClone` snapshot stack, bound to
  `ctrl+-`
- **Grapheme-correct throughout**: `Intl.Segmenter` for graphemes and words
  (`:25-26`); CJK-aware wrapping via `cjkBreakRegex`
- **Paste markers as atomic units** — the most unusual feature.
  `segmentWithMarkers()` `:48-101` merges graphemes inside `[paste #N +123 lines]`
  markers into single atomic segments, so cursor movement, deletion and word-wrap
  treat a collapsed 5,000-line paste as **one token**. A `validIds` gate means
  stale marker IDs don't merge
- **Autocomplete**: triggers `["@", "#"]` (`:253`), 20 ms debounce (`:252`),
  supports quoted paths (`@"..."`) and CJK punctuation

### Native modules

Three prebuilt N-API `.node` binaries, per platform, x64+arm64:

```
native/darwin/prebuilds/{darwin-arm64,darwin-x64}/darwin-platform.node
native/linux/prebuilds/{linux-arm64,linux-x64}/linux-platform-x11.node
native/win32/prebuilds/...
```

Exports (`darwin-platform.m:126-130`): `isModifierPressed`, `getText`, `setText`,
`getImage`, `getFilePaths`. Type `NativePlatformHelper = NativeClipboard &
{enableVirtualTerminalInput?, isModifierPressed?}` (`native-platform.ts:19-23`).

**Why native:**

1. **Modifier keys** — `CGEventSourceFlagsState(kCGEventSourceStateCombinedSessionState)`
   (`darwin-platform.m:9-15`, `:33`) reports the *real global modifier state*. A
   terminal cannot: xterm only sends modifiers with the keypress it modifies, so
   the TUI can never know "the user is holding Shift" independent of a key event.
2. **Clipboard with image + file-URL support** — `getImage()` reads PNG/TIFF and
   re-encodes (`:104-110`); `getFilePaths()` uses
   `NSPasteboardURLReadingFileURLsOnlyKey` (`:71`). Linux X11 needs a
   `clipboard-worker.h` to retain ownership.
3. **Windows VT input** — `enableVirtualTerminalInput()` adds
   `ENABLE_VIRTUAL_TERMINAL_INPUT` (0x0200) to the stdin console handle.
   Comment `terminal.ts:377-381`: without it "libuv's `ReadConsoleInputW` discards
   modifier state and Shift+Tab arrives as plain `\t`".

Loading (`native-platform.ts:25-49`): x64/arm64 only, validates that `getText`
**and** `getImage` are functions before accepting, caches the result — comment `:24`:
*"Cache module loading, not display availability: a disconnected display can
recover."*

**Escape-sequence fallbacks need no native module**: Kitty keyboard protocol
(`terminal.ts:398`), `modifyOtherKeys` (`:365-366`), OSC 52 clipboard
(`tui-alt-screen.ts:200-201`).

### Review — input & keybindings

**pi-go's single-line input is the sharpest UX gap in the TUI.** Three
consequences:

1. No multi-line prompt. A user cannot paste a stack trace, a diff, or a long
   instruction and have it survive the Enter key. This is a daily-use tax.
2. No undo. A mistaken Ctrl+U loses the whole line.
3. No grapheme/CJK awareness. A user typing Chinese or an emoji sequence gets
   cursor artifacts.

pi-go already handles CJK correctly in *rendering* (the width-safety work in §4.4
is exactly this) but not in *input*. That asymmetry is worth closing.

**The keybinding registry is a smaller gap than it looks, and pi's design is the
right one.** 45 named IDs with conflict detection and user overrides is genuinely
better than 11 hard-coded chords — but pi-go's chords are already
discoverable (`?` for `/help`) and the marginal value of rebinding is low for
most users. The `KeyId` compile-time-checked template union is a TypeScript
affordance pi-go can approximate with a typed key-name enum.

**Where pi-go has something pi does not**: F12 is explicitly swallowed
(`tui.go:1281`) — a real finding from embedded-terminal behavior. And pi-go's
mouse selection workaround (`selection.go:11-19`) solves the same problem pi
solves in alt-screen mode.

**Native modules are out of reach for pi-go.** N-API has no Go equivalent, and
Bubble Tea does not expose a modifier-state callback. The practical substitute is
the Kitty keyboard protocol, which pi also uses as a fallback (`terminal.ts:398`)
and which reports modifier state per keypress. That would give pi-go Ctrl+Shift+Tab
distinction without native code.

**Paste-marker atomicity is the cleverest single feature in either TUI.**
Treating a 5,000-line paste as one undo/word-wrap unit is the kind of detail that
only shows up after real use. It is implementable in Go with the same
grapheme-segmentation approach.

## 4.6 Images, color, and identity

### Images

pi has `packages/tui/src/terminal-image.ts` (731 LOC).
`ImageProtocol = "kitty" | "iterm2" | null` (`:7`).
`detectCapabilities` `:140-159` probes env: `KITTY_WINDOW_ID`/`TERM_PROGRAM=kitty`
→ kitty; `ITERM_SESSION_ID`/`TERM_PROGRAM=iterm.app` → iterm2.
`encodeKitty` `:220` → `\x1b_G` APC; `encodeITerm2` `:287` → `\x1b]1337;File=`.
`isImageLine(line)` `:201-208` is the primitive the differential renderer uses to
avoid corrupting image rows.

Image lifetime: `kittyImageMetadata` Map `:339`, LRU capped at 1000 (`:346-348`).
`deleteAllKittyPlacements()` `:283` deletes placements but **keeps the uploads** —
much cheaper for a scroll.

pi-go has **no inline image support**. Grepping `kitty|iterm|image` in
`internal/tui` returns nothing. `read_image` makes an image *visible to the
model*; nothing makes it visible to the *user*.

### Color

pi-go: 256/RGB via lipgloss, contrast-tested palette
(`palette_contrast_test.go`, `themes.json` with hex strings).

pi: **perceptual color** — `colors.ts` (367) + `oklab.ts` (233):
`colorToOklch`, `colorToOkhsl`, `oklchColor`, `okhslColor`, `mixColors`,
`backgroundAnsi`/`foregroundAnsi`, `TerminalColorMode` (truecolor/256/16).

### Easter eggs

pi-go: `AgentMood` 7 states (`face.go:8-16`: Idle, Thinking, Processing, ToolCall,
Speaking, Happy, Sad). `FaceRenderer` is a mutex-guarded mood holder (`:104-107`)
so the agent goroutine can `SetMood` while `View` reads on the render goroutine.
Plus matrix rain (`matrix.go`, 277 LOC) and animated window title
(`terminal_title.go`, 162 LOC — routed through `View` not a direct write, because
a direct stdout write would race the frame writes).

pi: `components/easter-egg-3d.ts` (**1,292 LOC**) — a real software ray-caster.
Per the comment at `:27-28`: *"A braille cell holds 2x4 roughly square dots, so one
pixel of a half-block bitmap is exactly 2x2 dots."* Ray cast per braille dot with
a `depth[]` buffer (`:600-614`), depth fog `clamp01(0.15 - cameraDistance * (1 -
depth) * 0.12)` (`:629`), halftone-quantized color (`LOGO_COLOR_STEP = 5`,
`HALO_LEVELS = 32` — because a braille dot is one cell, colors must be quantized
or the frame dithers), starfield, screen-dissolve with seeded per-cell delay.
Gated to alt-screen + lazy-loaded (`easter-egg-3d.lazy.ts:9-19`). Plus an XBM
bitmap Armin (`armin.ts`, 384 LOC, 31×36).

### Review — images, color, identity

**No inline images is a real feature gap**, not a nicety. Tool output frequently
contains a plot, a screenshot, a diagram, or a generated image; today the user
sees `[image: foo.png]` and must open it separately. pi-go already has the
hard part — `internal/mermaid` renders diagrams to terminal art at ~17.5k LOC —
so it has the terminal-drawing competence; it lacks only the Kitty/iTerm2 upload
protocol, which is a few hundred lines.

**The `AgentMood` mascot is a better product signal than pi's easter eggs.**
Seven states mapped to actual agent lifecycle transitions, surfaced in both the
sidebar and the status bar, is ambient UI that tells the user what the agent is
doing without them reading the transcript. pi's 3D ray-caster is a fun artifact
and nothing else — it carries no state.

**pi's OKLCH color is the better model.** Mixing colors in OKLab produces
perceptually uniform results; mixing in hex/sRGB does not. pi-go's palette is
hand-picked hex values with contrast tests, which works but does not scale —
there is no principled way to derive a 10-shade ramp from a base color in sRGB.
Adopting `okhsl` or `oklch` conversion would let pi-go generate themes
programmatically.

## Review summary — TUI

**pi-go leads on**: persistent operational dashboard (sidebar with live subagent
list, git branch, diff stats, SOP graph state), the minimap-as-separator design,
East-Asian-width correctness as a documented invariant with tests, the FNV-1a
allocation-free render cache, the agent-mood mascot, the full workflow surfaces
(`/plan`, `/run`, `/pr-autofix`), and its own 17.5k-LOC mermaid engine versus a
third-party dependency.

**pi leads on**: differential rendering with synchronized output, alt-screen
mode with runtime swapping, a real keybinding registry with conflict detection, a
2,472-LOC multiline editor with kill ring and undo and grapheme correctness,
native clipboard and modifier-key state, inline images, LaTeX, OSC 8 click-to-open,
perceptual color, and the sheer breadth of selectors and session actions
(fork/clone/tree, HTML export, gist share, bug report).

**pi-go's three concrete TUI fixes, in order of daily-use value:**

1. **Multiline input.** A multiline `textinput` replacement with grapheme
   segmentation, undo, and paste-atomicity. This is the single largest
   day-to-day UX gap and is entirely self-contained.
2. **CSI 2026 synchronized output** around each composed frame. ~20 lines,
   removes partial-frame tearing.
3. **Tail-truncation for bash output with a 0600 temp-file spill.** Carried over
   from §3.1 — it is a tool-layer fix but the presentation of the truncated
   result is TUI-layer.

**What not to attempt**: replacing Bubble Tea with a hand-written differential
renderer. pi-go's cost profile is markdown rendering, not terminal I/O, and the
render cache already addresses the real bottleneck. Wrapping frames in CSI 2026
captures most of the visible benefit at a fraction of the cost.