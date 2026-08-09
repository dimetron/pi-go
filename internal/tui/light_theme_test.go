package tui

import (
	"context"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Before this, every renderer test passed darkPalette, so a hardcoded ANSI
// index could sit in a renderer indefinitely without any test noticing that it
// vanished on a white background. These tests render with lightPalette.

// ansi256FG matches a 256-color foreground selector, e.g. "\x1b[38;5;252m".
var ansi256FG = regexp.MustCompile(`\x1b\[[0-9;]*38;5;(\d+)`)

// bannedOnLight are the indices that used to be hardcoded in the renderers.
// 252 and 226 are near-white and pure yellow: invisible on a light terminal.
var bannedOnLight = map[string]string{
	"252": "near-white, invisible on a light background",
	"226": "pure yellow, ~1.07:1 on a light background",
	"213": "pale magenta, ~1.7:1 on a light background",
	"203": "salmon, fails AA on a light background",
	"245": "mid-gray, hardcoded tool arg color",
	"35":  "hardcoded tool green",
}

func assertNoHardcodedANSI(t *testing.T, what, out string) {
	t.Helper()
	for _, m := range ansi256FG.FindAllStringSubmatch(out, -1) {
		if why, banned := bannedOnLight[m[1]]; banned {
			t.Errorf("%s emitted hardcoded ANSI %s (%s) under a light palette; use a Palette role",
				what, m[1], why)
		}
	}
}

func TestLightPaletteRenderersUseThePalette(t *testing.T) {
	c := ChatModel{
		Palette: lightPalette,
		Width:   100,
		Messages: []message{
			{role: "user", content: "hello"},
			{role: "assistant", content: "a **markdown** reply with `code`"},
			{role: "assistant", content: "something failed", isError: true},
			{role: "assistant", content: "heads up", isWarning: true},
			{role: "tool", tool: "read", toolIn: `{"path":"main.go"}`, content: "package main"},
			{
				role: "tool", tool: "agent", agentType: "claude",
				agentTitle: "investigate the bug",
				agentEvents: []agentEv{
					{kind: "tool_call", content: "grep"},
					{kind: "text", content: "found it"},
				},
			},
		},
	}
	c.ToolDisplay.Palette = lightPalette
	c.ToolDisplay.Width = 100
	c.UpdateRenderer(100)

	assertNoHardcodedANSI(t, "RenderMessages", c.RenderMessages(false))
}

func TestHighlightBashOutputUsesThePalette(t *testing.T) {
	// Text chroma cannot tokenize takes the fallback path, which used to paint
	// near-white.
	lines := []string{"~~~ not code at all ~~~"}
	assertNoHardcodedANSI(t, "highlightBashOutput", strings.Join(highlightBashOutput(lines, lightPalette), "\n"))
}

// The markdown stylesheet is baked into the glamour renderer, so a light theme
// only works if the renderer is built from the palette.
func TestMarkdownRendererFollowsPalette(t *testing.T) {
	render := func(p Palette, md string) string {
		c := ChatModel{Palette: p}
		c.UpdateRenderer(80)
		return c.RenderMarkdown(md)
	}

	const md = "Some prose with `inline code`."
	light, dark := render(lightPalette, md), render(darkPalette, md)
	if light == dark {
		t.Fatal("light and dark palettes produced identical markdown; the stylesheet is not following the theme")
	}

	// 236 is glamour's dark inline-code background — unreadable on white.
	if strings.Contains(light, "48;5;236") {
		t.Error("light markdown used glamour's dark inline-code background (236)")
	}
	// 252 is DarkStyleConfig's document color: near-white body text.
	if strings.Contains(light, "38;5;252") {
		t.Error("light markdown used glamour's dark document color (252)")
	}
}

// An untagged fence containing box-drawing characters — our own README's
// architecture diagram — used to render as a column of white-on-red bars,
// because chroma's fallback lexer classes those runes as Error tokens.
func TestUntaggedFenceWithBoxDrawingHasNoErrorBackground(t *testing.T) {
	const md = "```\n" +
		"cmd/pi/\n" +
		"internal/\n" +
		"├── agent/    setup\n" +
		"└── tui/      ui\n" +
		"```\n"

	for _, tc := range []struct {
		name string
		p    Palette
	}{{"light", lightPalette}, {"dark", darkPalette}} {
		t.Run(tc.name, func(t *testing.T) {
			c := ChatModel{Palette: tc.p}
			c.UpdateRenderer(80)
			out := c.RenderMarkdown(md)

			// glamour's stock error background: #F05B5B (dark) / #FF5555 (light).
			for _, bad := range []string{"48;2;240;91;91", "48;2;255;85;85"} {
				if strings.Contains(out, bad) {
					t.Errorf("box-drawing fence rendered with the chroma error background (%s)", bad)
				}
			}
		})
	}
}

func TestMarkdownStyleForDoesNotMutateGlamourGlobals(t *testing.T) {
	// markdownStyleFor overrides Chroma.Error; Chroma is a pointer on a
	// package-level var, so a shallow copy would corrupt it process-wide.
	before := *markdownStyleFor(darkPalette).CodeBlock.Chroma
	_ = markdownStyleFor(lightPalette)
	after := *markdownStyleFor(darkPalette).CodeBlock.Chroma

	if colorPtr(before.Text.Color) != colorPtr(after.Text.Color) {
		t.Error("markdownStyleFor mutated glamour's shared stylesheet")
	}
}

func colorPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// A theme switch has to bring the transcript with it: the glamour stylesheet
// lives on the renderer, where renderKey cannot see it.
func TestChatRefreshThemeRebuildsRendererOnPaletteChange(t *testing.T) {
	c := ChatModel{Palette: darkPalette}
	c.UpdateRenderer(80)
	first := c.Renderer

	if c.RefreshTheme() {
		t.Error("RefreshTheme rebuilt the renderer when the palette had not changed")
	}
	if c.Renderer != first {
		t.Error("renderer replaced without a palette change")
	}

	c.Palette = lightPalette
	if !c.RefreshTheme() {
		t.Fatal("RefreshTheme did not rebuild after a palette change")
	}
	if c.Renderer == first {
		t.Error("renderer not replaced after a palette change")
	}
}

func TestThemeCommandRepaintsTranscript(t *testing.T) {
	tm := NewThemeManager()
	m := &model{
		themeManager: tm,
		chatModel:    ChatModel{Messages: []message{{role: "assistant", content: "hi"}}, Width: 80},
	}
	m.syncPalette()
	m.chatModel.UpdateRenderer(80)
	// Prime the cache so we can prove it was cleared.
	_ = m.chatModel.RenderMessages(false)
	before := m.chatModel.Renderer

	m.handleThemeCommand([]string{"catppuccin-latte"})

	if !m.palette.IsLight {
		t.Fatal("switching to a light theme left a dark palette")
	}
	if m.chatModel.Renderer == before {
		t.Error("/theme did not rebuild the markdown renderer")
	}
	if m.chatModel.Messages[0].renderCached {
		t.Error("/theme left a stale render in the cache")
	}
}

func TestBackgroundColorPicksLightThemeOnlyWhenUnconfigured(t *testing.T) {
	lightBG := tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")}

	t.Run("unconfigured terminal reporting light", func(t *testing.T) {
		m := &model{themeManager: NewThemeManager(), chatModel: ChatModel{Width: 80}}
		m.handleBackgroundColor(lightBG)
		if got := m.themeManager.CurrentName(); got != defaultLightTheme {
			t.Errorf("theme = %q, want %q", got, defaultLightTheme)
		}
		if !m.palette.IsLight {
			t.Error("palette did not follow the detected background")
		}
	})

	t.Run("explicit config wins", func(t *testing.T) {
		m := &model{
			themeManager: NewThemeManager(),
			cfg:          Config{ThemeName: "dracula"},
			chatModel:    ChatModel{Width: 80},
		}
		_ = m.themeManager.SetTheme("dracula")
		m.handleBackgroundColor(lightBG)
		if got := m.themeManager.CurrentName(); got != "dracula" {
			t.Errorf("detection overrode an explicit theme: got %q", got)
		}
	})

	t.Run("explicit /theme wins over a late reply", func(t *testing.T) {
		m := &model{themeManager: NewThemeManager(), chatModel: ChatModel{Width: 80}}
		m.handleThemeCommand([]string{"dracula"})
		m.handleBackgroundColor(lightBG)
		if got := m.themeManager.CurrentName(); got != "dracula" {
			t.Errorf("detection overrode /theme: got %q", got)
		}
	})

	t.Run("dark terminal keeps the default", func(t *testing.T) {
		m := &model{themeManager: NewThemeManager(), chatModel: ChatModel{Width: 80}}
		m.handleBackgroundColor(tea.BackgroundColorMsg{Color: lipgloss.Color("#000000")})
		if got := m.themeManager.CurrentName(); got != DefaultThemeName {
			t.Errorf("theme = %q, want %q", got, DefaultThemeName)
		}
	})
}

// The input's prompt and cursor are baked into textinput at construction, so
// they need an explicit refresh on a theme switch.
func TestInputRefreshThemeFollowsPalette(t *testing.T) {
	im := &InputModel{Palette: darkPalette}
	im.ensureInput()
	if im.stylePaletteKey != paletteKey(darkPalette) {
		t.Fatal("ensureInput did not record the palette it built from")
	}
	if im.RefreshTheme() {
		t.Error("RefreshTheme repainted without a palette change")
	}

	im.Palette = lightPalette
	if !im.RefreshTheme() {
		t.Fatal("RefreshTheme did not repaint after a palette change")
	}
	if im.stylePaletteKey != paletteKey(lightPalette) {
		t.Error("input styles did not follow the new palette")
	}
}

// --- /theme palette preview ---

func TestFormatPalettePreviewCoversEveryRole(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Palette
		want string
	}{
		{"light", lightPalette, "light"},
		{"dark", darkPalette, "dark"},
		{"zero falls back to dark", Palette{}, "dark"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := formatPalettePreview(tc.p)

			if !strings.Contains(out, "Active render palette ("+tc.want+")") {
				t.Errorf("missing or wrong palette kind in header: %q", firstLine(out))
			}
			// Every row carries a swatch, the role name and its hex.
			for _, role := range []string{"Text", "Primary", "Error", "Control", "Background"} {
				if !strings.Contains(out, role) {
					t.Errorf("missing role %q", role)
				}
			}
			p := paletteOrDark(tc.p)
			if !strings.Contains(out, colorString(p.Text)) {
				t.Errorf("missing Text hex %s", colorString(p.Text))
			}
			if !strings.Contains(out, colorString(p.Control)) {
				t.Errorf("missing Control hex %s", colorString(p.Control))
			}
			if got := strings.Count(out, "\n") + 1; got < 20 {
				t.Errorf("expected a row per palette role, got %d lines", got)
			}
		})
	}
}

func TestSwatchKeepsColumnsAlignedForUnsetColors(t *testing.T) {
	if got := swatch(nil); got != strings.Repeat(" ", len(swatchGlyph)) {
		t.Errorf("swatch(nil) = %q, want %d blanks so columns stay aligned",
			got, len(swatchGlyph))
	}
	if got := swatch(lightPalette.Text); !strings.Contains(got, swatchGlyph) {
		t.Errorf("swatch = %q, want it to contain %q", got, swatchGlyph)
	}
}

func TestRenderPaletteRowsAlignsAndOmitsEmptyNotes(t *testing.T) {
	rows := []paletteRow{
		{"Text", lightPalette.Text, "body text"},
		{"AVeryLongRoleName", lightPalette.Primary, ""},
	}
	out := renderPaletteRows(rows, lightPalette)
	lines := strings.Split(out, "\n")

	if len(lines) != len(rows) {
		t.Fatalf("expected %d rows, got %d", len(rows), len(lines))
	}
	if !strings.Contains(lines[0], "body text") {
		t.Error("note missing from the first row")
	}
	// The short role is padded to the width of the long one, so the hex
	// columns line up.
	if lipgloss.Width(lines[0]) != lipgloss.Width(lines[1])+len("  body text") {
		t.Errorf("columns not aligned: %d vs %d",
			lipgloss.Width(lines[0]), lipgloss.Width(lines[1]))
	}
}

func TestThemePaletteSubcommandRendersThePreview(t *testing.T) {
	m := &model{themeManager: NewThemeManager(), chatModel: ChatModel{Width: 80}}
	m.syncPalette()

	m.handleThemeCommand([]string{"PALETTE"}) // case-insensitive

	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	msg := m.chatModel.Messages[0]
	if !msg.preRendered {
		t.Error("the palette table carries ANSI, so it must bypass glamour")
	}
	if !strings.Contains(msg.content, "Active render palette") {
		t.Errorf("expected the palette preview, got %q", firstLine(msg.content))
	}
	// A subcommand must not be mistaken for a theme name.
	if got := m.themeManager.CurrentName(); got != DefaultThemeName {
		t.Errorf("/theme palette changed the theme to %q", got)
	}
}

// preRendered content must reach the transcript byte-for-byte; routing it
// through glamour is what prints escape bytes as text.
func TestPreRenderedMessageBypassesGlamour(t *testing.T) {
	body := formatPalettePreview(lightPalette)
	c := ChatModel{
		Palette:  lightPalette,
		Width:    100,
		Messages: []message{{role: "assistant", content: body, preRendered: true}},
	}
	c.UpdateRenderer(100)

	out := c.RenderMessages(false)
	if !strings.Contains(out, body) {
		t.Error("pre-rendered content was reformatted on its way to the transcript")
	}
	if strings.Contains(out, `\x1b`) || strings.Contains(out, "38;2;") == false {
		t.Error("expected the original ANSI to survive intact")
	}
}

func TestThemeCommandWithoutAThemeManager(t *testing.T) {
	m := &model{chatModel: ChatModel{}}
	m.handleThemeCommand([]string{"catppuccin-latte"})

	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "not available") {
		t.Errorf("expected a graceful message, got %q", m.chatModel.Messages[0].content)
	}
}

func TestBackgroundColorWithoutAThemeManagerIsANoop(t *testing.T) {
	m := &model{chatModel: ChatModel{Width: 80}}
	m.handleBackgroundColor(tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})

	if !m.bgDetected {
		t.Error("the reply should still settle the detection question")
	}
	if m.palette.Valid {
		t.Error("no theme manager means no palette to resolve")
	}
}

func TestBackgroundColorIgnoresASecondReply(t *testing.T) {
	m := &model{themeManager: NewThemeManager(), chatModel: ChatModel{Width: 80}}
	m.handleBackgroundColor(tea.BackgroundColorMsg{Color: lipgloss.Color("#000000")})
	m.handleBackgroundColor(tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})

	if got := m.themeManager.CurrentName(); got != DefaultThemeName {
		t.Errorf("a second reply changed the theme to %q", got)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Init must ask the terminal for its background on both startup paths, or a
// light terminal never gets a light default.
func TestInitRequestsTheTerminalBackground(t *testing.T) {
	t.Run("deferred init", func(t *testing.T) {
		m := &model{initCh: make(chan InitEvent, 1), themeManager: NewThemeManager()}
		if m.Init() == nil {
			t.Fatal("Init returned no command")
		}
	})

	t.Run("synchronous init", func(t *testing.T) {
		m := &model{themeManager: NewThemeManager(), cfg: Config{WorkDir: t.TempDir()}}
		if m.Init() == nil {
			t.Fatal("Init returned no command")
		}
	})
}

// The handler is only useful if the message actually reaches it.
func TestBackgroundColorMsgIsDispatched(t *testing.T) {
	m := &model{themeManager: NewThemeManager(), chatModel: ChatModel{Width: 80}}

	_, _, handled := m.updateTerminal(tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})

	if !handled {
		t.Fatal("BackgroundColorMsg was not handled")
	}
	if got := m.themeManager.CurrentName(); got != defaultLightTheme {
		t.Errorf("theme = %q, want %q", got, defaultLightTheme)
	}
}

// A theme manager whose themes.json is unusable falls back to pi-classic only,
// so the light default is genuinely missing and detection has to give up
// quietly rather than leave a half-applied theme.
func TestBackgroundColorGivesUpWhenTheLightDefaultIsMissing(t *testing.T) {
	tm, err := NewThemeManagerFromJSON([]byte(`{"only-dark":{
		"name":"only-dark","displayName":"Only Dark","themeType":"dark","colors":{}}}`))
	if err != nil {
		t.Fatalf("build theme manager: %v", err)
	}
	m := &model{themeManager: tm, chatModel: ChatModel{Width: 80}}

	m.handleBackgroundColor(tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})

	if got := tm.CurrentName(); got != "only-dark" {
		t.Errorf("theme = %q, want the theme left untouched", got)
	}
}

func TestChromaStylesResolve(t *testing.T) {
	if highlightStyle() == nil || lightHighlightStyle() == nil {
		t.Fatal("chroma style lookup returned nil")
	}
	if highlightStyle() == lightHighlightStyle() {
		t.Error("light and dark palettes share a syntax style")
	}
}

// The original bug: the markdown renderer was constructed before the theme
// manager existed, so it was always built from the dark palette.
func TestNewModelBuildsTheRendererFromTheConfiguredTheme(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t.Run("light theme from config", func(t *testing.T) {
		m := newModel(ctx, cancel, Config{ThemeName: "github-light", WorkDir: t.TempDir()})

		if got := m.themeManager.CurrentName(); got != "github-light" {
			t.Errorf("theme = %q, want github-light", got)
		}
		if !m.palette.IsLight {
			t.Fatal("configured light theme did not resolve to a light palette")
		}
		if !m.chatModel.Palette.IsLight {
			t.Error("the chat model did not receive the light palette")
		}
		// Built from the light palette, so nothing should rebuild it.
		if m.chatModel.RefreshTheme() {
			t.Error("renderer was built from the wrong palette and needed a rebuild")
		}
		out, err := m.chatModel.Renderer.Render("`code`")
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if strings.Contains(out, "48;5;236") {
			t.Error("renderer used glamour's dark inline-code background")
		}
	})

	t.Run("unknown theme falls back to the default", func(t *testing.T) {
		m := newModel(ctx, cancel, Config{ThemeName: "no-such-theme", WorkDir: t.TempDir()})
		if got := m.themeManager.CurrentName(); got != DefaultThemeName {
			t.Errorf("theme = %q, want %q", got, DefaultThemeName)
		}
		if m.palette.IsLight {
			t.Error("the default theme is dark")
		}
	})

	t.Run("deferred init", func(t *testing.T) {
		ch := make(chan InitEvent, 1)
		m := newModel(ctx, cancel, Config{DeferredInit: ch, WorkDir: t.TempDir()})
		if !m.loading {
			t.Error("deferred init should start in the loading state")
		}
		if m.initCh == nil {
			t.Error("deferred init channel not wired")
		}
	})

	t.Run("no saved history", func(t *testing.T) {
		// A home directory with no history file: loadHistory returns nil and
		// the model must still start with a usable empty slice.
		t.Setenv("HOME", t.TempDir())
		m := newModel(ctx, cancel, Config{WorkDir: t.TempDir()})
		if m.inputModel.History == nil {
			t.Error("history should be an empty slice, not nil")
		}
	})
}
