package tui

import (
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
