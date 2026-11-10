package tui

import (
	"image/color"
	"strings"
	"testing"
)

func TestThemeManagerLoad(t *testing.T) {
	tm := NewThemeManager()

	if got := tm.ThemeCount(); got != 40 {
		t.Errorf("ThemeCount() = %d, want 40", got)
	}

	if got := tm.CurrentName(); got != DefaultThemeName {
		t.Errorf("CurrentName() = %q, want %q", got, DefaultThemeName)
	}
}

func TestThemeManagerSetTheme(t *testing.T) {
	tm := NewThemeManager()

	if err := tm.SetTheme("dracula"); err != nil {
		t.Fatalf("SetTheme(dracula) error: %v", err)
	}
	if got := tm.CurrentName(); got != "dracula" {
		t.Errorf("CurrentName() = %q, want dracula", got)
	}

	if err := tm.SetTheme("nonexistent"); err == nil {
		t.Error("SetTheme(nonexistent) should return error")
	}
	if got := tm.CurrentName(); got != "dracula" {
		t.Errorf("CurrentName() = %q, want dracula (should not change on error)", got)
	}
}

func TestThemeManagerFallback(t *testing.T) {
	tm := &ThemeManager{themes: make(map[string]Theme)}
	if err := tm.loadFromJSON([]byte("not json")); err == nil {
		t.Error("loadFromJSON with corrupt data should return error")
	}
	tm.loadFallback()

	if _, ok := tm.themes["pi-classic"]; !ok {
		t.Error("fallback should load pi-classic theme")
	}
}

func TestThemeColors(t *testing.T) {
	tm := NewThemeManager()
	colors := tm.Colors()

	checks := map[string]string{
		"Text":            colors.Text,
		"Base":            colors.Base,
		"Primary":         colors.Primary,
		"Tool":            colors.Tool,
		"Success":         colors.Success,
		"Error":           colors.Error,
		"Secondary":       colors.Secondary,
		"Info":            colors.Info,
		"Warning":         colors.Warning,
		"DiffAdded":       colors.DiffAdded,
		"DiffRemoved":     colors.DiffRemoved,
		"DiffAddedText":   colors.DiffAddedText,
		"DiffRemovedText": colors.DiffRemovedText,
	}
	for name, val := range checks {
		if val == "" {
			t.Errorf("color %s is empty", name)
		}
		if !strings.HasPrefix(val, "#") {
			t.Errorf("color %s = %q, want hex value starting with #", name, val)
		}
	}
}

func TestThemeColorsConversion(t *testing.T) {
	tm := NewThemeManager()
	colors := tm.Colors()

	// Verify all color.Color helpers don't panic.
	// lipgloss.Color is just a string alias.
	colorMethods := []struct {
		name string
		fn   func() any
	}{
		{"TextColor", func() any { return colors.TextColor() }},
		{"BaseColor", func() any { return colors.BaseColor() }},
		{"PrimaryColor", func() any { return colors.PrimaryColor() }},
		{"ToolColor", func() any { return colors.ToolColor() }},
		{"SuccessColor", func() any { return colors.SuccessColor() }},
		{"ErrorColor", func() any { return colors.ErrorColor() }},
		{"SecondaryColor", func() any { return colors.SecondaryColor() }},
		{"InfoColor", func() any { return colors.InfoColor() }},
		{"WarningColor", func() any { return colors.WarningColor() }},
		{"DiffAddedColor", func() any { return colors.DiffAddedColor() }},
		{"DiffRemovedColor", func() any { return colors.DiffRemovedColor() }},
		{"DiffAddedTextColor", func() any { return colors.DiffAddedTextColor() }},
		{"DiffRemovedTextColor", func() any { return colors.DiffRemovedTextColor() }},
	}
	for _, tc := range colorMethods {
		if c := tc.fn(); c == nil {
			t.Errorf("%s() returned nil", tc.name)
		}
	}
}

func TestThemeManagerList(t *testing.T) {
	tm := NewThemeManager()

	themes := tm.List()
	if len(themes) != 40 {
		t.Errorf("List() returned %d themes, want 40", len(themes))
	}

	for i := 1; i < len(themes); i++ {
		if themes[i].Name < themes[i-1].Name {
			t.Errorf("themes not sorted: %q before %q", themes[i-1].Name, themes[i].Name)
			break
		}
	}
}

func TestThemeManagerIsDark(t *testing.T) {
	tm := NewThemeManager()

	if !tm.IsDark() {
		t.Error("tokyo-night should be dark")
	}

	if err := tm.SetTheme("github-light"); err != nil {
		t.Fatalf("SetTheme error: %v", err)
	}
	if tm.IsDark() {
		t.Error("github-light should not be dark")
	}
}

func TestThemeManagerHasTheme(t *testing.T) {
	tm := NewThemeManager()

	if !tm.HasTheme("dracula") {
		t.Error("should have dracula theme")
	}
	if tm.HasTheme("nonexistent") {
		t.Error("should not have nonexistent theme")
	}
}

func TestThemeManagerClosestMatches(t *testing.T) {
	tm := NewThemeManager()

	matches := tm.ClosestMatches("cat", 5)
	if len(matches) == 0 {
		t.Fatal("ClosestMatches(cat) should find catppuccin themes")
	}
	for _, m := range matches {
		if !strings.Contains(m, "catppuccin") {
			t.Errorf("unexpected match %q for query cat", m)
		}
	}

	matches = tm.ClosestMatches("dark", 2)
	if len(matches) > 2 {
		t.Errorf("ClosestMatches with max=2 returned %d results", len(matches))
	}
}

func TestThemeManagerCurrent(t *testing.T) {
	tm := NewThemeManager()

	theme := tm.Current()
	if theme.Name != "tokyo-night" {
		t.Errorf("Current().Name = %q, want tokyo-night", theme.Name)
	}
	if theme.DisplayName != "Tokyo Night" {
		t.Errorf("Current().DisplayName = %q, want Tokyo Night", theme.DisplayName)
	}
	if theme.ThemeType != "dark" {
		t.Errorf("Current().ThemeType = %q, want dark", theme.ThemeType)
	}
}

func TestAllThemesHaveRequiredColors(t *testing.T) {
	tm := NewThemeManager()

	for _, theme := range tm.List() {
		c := theme.Colors
		fields := map[string]string{
			"Text":    c.Text,
			"Base":    c.Base,
			"Primary": c.Primary,
			"Tool":    c.Tool,
			"Success": c.Success,
			"Error":   c.Error,
		}
		for field, val := range fields {
			if val == "" {
				t.Errorf("theme %q: color %s is empty", theme.Name, field)
			}
		}
	}
}

func TestNewThemeManagerFromJSON(t *testing.T) {
	data := []byte(`{
		"test-theme": {
			"name": "test-theme",
			"displayName": "Test Theme",
			"themeType": "dark",
			"colors": {
				"text": "#ffffff",
				"base": "#000000",
				"primary": "#ff0000",
				"tool": "#00ff00",
				"success": "#00ff00",
				"error": "#ff0000",
				"secondary": "#808080",
				"info": "#0000ff",
				"warning": "#ffff00",
				"diffAdded": "#003300",
				"diffRemoved": "#330000",
				"diffAddedText": "#00ff00",
				"diffRemovedText": "#ff0000"
			}
		}
	}`)

	tm, err := NewThemeManagerFromJSON(data)
	if err != nil {
		t.Fatalf("NewThemeManagerFromJSON error: %v", err)
	}
	if tm.ThemeCount() != 1 {
		t.Errorf("ThemeCount() = %d, want 1", tm.ThemeCount())
	}
	if !tm.HasTheme("test-theme") {
		t.Error("should have test-theme")
	}

	_, err = NewThemeManagerFromJSON([]byte("invalid"))
	if err == nil {
		t.Error("NewThemeManagerFromJSON should error on invalid JSON")
	}
}

// TestPaletteForLightThemeRendersDifferently pins the fix for light themes:
// before the palette was threaded into the renderers, switching to a light
// theme changed nothing on screen because every renderer hardcoded dark Mocha
// colors. paletteFor must produce a palette whose key differs from the dark
// default, so the render cache and the renderers actually pick up the change.
func TestPaletteForLightThemeRendersDifferently(t *testing.T) {
	tm := NewThemeManager()
	if err := tm.SetTheme("github-light"); err != nil {
		t.Fatalf("SetTheme(github-light) error: %v", err)
	}
	light := paletteFor(tm.Current())
	if !light.Valid {
		t.Fatal("light palette should be valid")
	}
	if paletteKey(light) == paletteKey(darkPalette) {
		t.Error("light palette key equals dark palette key; light theme would not re-render")
	}
	// The light palette's text must be dark (legible on a light background),
	// not the dark theme's light text.
	if colorString(light.Text) == colorString(darkPalette.Text) {
		t.Errorf("light theme text %q should differ from dark theme text %q",
			colorString(light.Text), colorString(darkPalette.Text))
	}
}

// TestPaletteForDarkThemeOverlaysThemeTokens replaces the older
// TestPaletteForDarkThemeMatchesDefault, which asserted that the default theme
// resolved to exactly darkPalette. That was the bug, written down: the default
// theme is tokyo-night, and every renderer draws from paletteFor, so pinning
// paletteFor to Mocha meant `/theme tokyo-night` was indistinguishable from the
// startup screen and the whole 40-theme file was decorative.
//
// The contract is now: a dark theme keeps the Mocha palette's *structure* (so
// roles the theme file does not name stay contrast-checked and dark), and
// overlays the tokens it does name. Absent tokens must leave the default in
// place rather than blanking the role.
func TestPaletteForDarkThemeOverlaysThemeTokens(t *testing.T) {
	tm := NewThemeManager()
	if err := tm.SetTheme("tokyo-night"); err != nil {
		t.Fatalf("SetTheme(tokyo-night) error: %v", err)
	}
	got := paletteFor(tm.Current())
	if !got.Valid {
		t.Fatal("dark palette should be valid")
	}

	// The named tokens must come from the theme file.
	colors := tm.Colors()
	for _, tc := range []struct {
		role string
		got  color.Color
		want string
	}{
		{"text", got.Text, colors.Text},
		{"primary", got.Primary, colors.Primary},
		{"tool", got.Tool, colors.Tool},
		{"success", got.Success, colors.Success},
		{"error", got.Error, colors.Error},
		{"warning", got.Warning, colors.Warning},
		{"diffAdded", got.DiffAdded, colors.DiffAdded},
		{"diffRemoved", got.DiffRemoved, colors.DiffRemoved},
		{"diffAddedText", got.DiffAddedText, colors.DiffAddedText},
		{"diffRemovedText", got.DiffRemovedText, colors.DiffRemovedText},
	} {
		if gotHex := colorString(tc.got); !strings.EqualFold(gotHex, tc.want) {
			t.Errorf("%s = %s, want the theme's %s", tc.role, gotHex, tc.want)
		}
	}

	// An unnamed role must keep the variant default, not go unset. Subtext is
	// not a theme token; it carries body-adjacent text and so must stay on the
	// contrast-checked ladder rather than following the theme's comment color.
	if colorString(got.Subtext) != colorString(darkPalette.Subtext) {
		t.Errorf("Subtext = %s, want the dark default %s (it is not a themeable role)",
			colorString(got.Subtext), colorString(darkPalette.Subtext))
	}

	// The default theme must no longer render identically to bare Mocha, or
	// /theme would have nothing to change on a fresh session.
	if paletteKey(got) == paletteKey(darkPalette) {
		t.Error("default theme palette key equals bare darkPalette; the theme file's tokens are being ignored")
	}
}

// TestPaletteForDarkAndLightThemesDiffer checks that every theme resolves to a
// distinct palette, so switching always repaints. A shared key would leave the
// previous theme's output in the render cache (renderKey folds paletteKey).
func TestPaletteForDistinctThemesHaveDistinctKeys(t *testing.T) {
	tm := NewThemeManager()
	seen := make(map[uint64]string, tm.ThemeCount())
	for _, th := range tm.List() {
		if err := tm.SetTheme(th.Name); err != nil {
			t.Fatalf("SetTheme(%s) error: %v", th.Name, err)
		}
		key := paletteKey(paletteFor(tm.Current()))
		if prev, ok := seen[key]; ok && prev != th.Name {
			t.Errorf("themes %q and %q resolve to the same palette key %d", prev, th.Name, key)
		}
		seen[key] = th.Name
	}
}
