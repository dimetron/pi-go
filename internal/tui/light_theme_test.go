package tui

import (
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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

// The main window body — the chat pane, thinking blocks, separators, status bar
// — must sit on the theme's background under a light theme. Before this, only
// the sidebar painted its background, so the grayed thinking text rendered on
// the terminal's default surface instead of the theme's. Dark themes keep the
// body unpainted (they rely on a dark terminal), so this only asserts the light
// path.
func TestLightThemePaintsTheWholeFrame(t *testing.T) {
	m := historyModel(t, "first")
	m.themeManager = NewThemeManager()
	_ = m.themeManager.SetTheme("catppuccin-latte")
	m.syncPalette()
	m.chatModel.Messages = []message{{role: "thinking", content: "let me think"}}
	m.chatModel.UpdateRenderer(80)
	m.applyResize()

	if !m.palette.IsLight {
		t.Fatal("catppuccin-latte resolved to a dark palette")
	}

	frame := m.View().Content
	mainW := m.mainWidth()
	for i, row := range strings.Split(frame, "\n") {
		// The body is everything before the rail; the sidebar legitimately
		// paints its own background, so scope the check to the body region.
		body := ansi.Cut(row, 0, mainW-1)
		if !strings.Contains(body, "48;2;") {
			t.Errorf("light row %d body has no themed background: %q", i, ansi.Strip(body))
		}
	}
}
