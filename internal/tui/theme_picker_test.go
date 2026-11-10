package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// themePickerModel builds a model with the theme picker open. The real
// ThemeManager is used deliberately: previewing mutates it, and the whole point
// of the payload is that a revert can undo that mutation.
func themePickerModel(t *testing.T) *model {
	t.Helper()
	m := newTestModelFull(t)
	m.themeManager = NewThemeManager()
	m.palette = paletteFor(m.themeManager.Current())
	m.newSearchPopup(searchModeThemes)
	return m
}

func TestThemePickerListsEveryTheme(t *testing.T) {
	m := themePickerModel(t)

	if m.searchPopup == nil {
		t.Fatal("bare theme picker did not open a popup")
	}
	if got, want := len(m.searchPopup.entries), m.themeManager.ThemeCount(); got != want {
		t.Errorf("picker lists %d themes, want %d", got, want)
	}
	// The current theme is marked, so a user who opens the picker can see what
	// they are already on without moving the cursor.
	found := false
	for _, item := range m.searchPopup.entries {
		if item.Text == m.themeManager.CurrentName() {
			found = true
			if item.Description == "" {
				t.Error("current theme row has no description marking it")
			}
		}
	}
	if !found {
		t.Errorf("current theme %q missing from picker", m.themeManager.CurrentName())
	}
}

func TestThemePickerPreviewsOnMove(t *testing.T) {
	m := themePickerModel(t)
	original := m.themeManager.CurrentName()

	sp := m.searchPopup
	if len(sp.filtered) < 2 {
		t.Fatal("need at least two themes to test preview")
	}
	// Open selects index 0, which may or may not be the current theme. Move to a
	// row that is definitely a different theme and assert the live manager followed.
	target := -1
	for i, item := range sp.filtered {
		if item.Text != original {
			target = i
			break
		}
	}
	if target < 0 {
		t.Fatal("no non-current theme in the picker")
	}
	sp.selected = target
	m.previewThemeSelection()

	if got, want := m.themeManager.CurrentName(), sp.filtered[target].Text; got != want {
		t.Errorf("preview did not apply theme: CurrentName() = %q, want %q", got, want)
	}
	// A preview must not persist. saveThemeToConfig is not exercised here; the
	// contract under test is that moving writes only the in-memory theme.
	if m.searchPopup == nil {
		t.Error("preview closed the picker")
	}
}

func TestThemePickerOpeningDoesNotChangeTheme(t *testing.T) {
	// Opening a picker is not a choice. Selecting row 0 on open would repaint
	// the whole TUI just because the user pressed /theme, and the first Esc
	// would then look like it reverted a preview they never asked for.
	base := newTestModelFull(t)
	base.themeManager = NewThemeManager()
	opened := base.themeManager.CurrentName()

	m := themePickerModel(t)

	if got := m.themeManager.CurrentName(); got != opened {
		t.Errorf("opening the picker changed the theme to %q, want %q", got, opened)
	}
	if m.searchPopup.selected >= len(m.searchPopup.entries) {
		t.Fatalf("selection %d out of range", m.searchPopup.selected)
	}
	if got, want := m.searchPopup.filtered[m.searchPopup.selected].Text, opened; got != want {
		t.Errorf("picker opened on %q, want the active theme %q", got, want)
	}
}

func TestThemePickerEscRevertsToOpenedTheme(t *testing.T) {
	// Capture the theme before the picker opens: opening selects the current
	// row, which may itself be a preview.
	base := newTestModelFull(t)
	base.themeManager = NewThemeManager()
	opened := base.themeManager.CurrentName()
	m := themePickerModel(t)

	// Move twice, previewing both times, then revert.
	sp := m.searchPopup
	for i := 0; i < 2 && len(sp.filtered) > 1; i++ {
		sp.selectNext()
		m.previewThemeSelection()
	}
	if m.themeManager.CurrentName() == opened && len(sp.filtered) > 1 {
		t.Log("preview landed back on the opened theme; revert still must not change it")
	}

	if !m.handleSearchPopupKey(tea.Key{Code: tea.KeyEsc}) {
		t.Fatal("Esc was not handled by the theme picker")
	}
	if got, want := m.themeManager.CurrentName(), opened; got != want {
		t.Errorf("Esc reverted to %q, want the theme the picker opened with (%q)", got, want)
	}
	if m.searchPopup != nil {
		t.Error("Esc left the picker open")
	}
}

func TestThemePickerPayloadCapturedOnce(t *testing.T) {
	// A payload re-captured per keystroke would make "original" whatever the
	// last preview left behind, and Esc would revert to a preview rather than to
	// what the user started with. Pin that it does not move.
	base := newTestModelFull(t)
	base.themeManager = NewThemeManager()
	opened := base.themeManager.CurrentName()
	m := themePickerModel(t)
	if m.searchPopup.payload == nil {
		t.Fatal("theme picker has no payload")
	}
	if got := m.searchPopup.payload.originalTheme; got != opened {
		t.Errorf("payload captured %q, want %q", got, opened)
	}

	sp := m.searchPopup
	sp.selectNext()
	m.previewThemeSelection()
	if got := m.searchPopup.payload.originalTheme; got != opened {
		t.Errorf("payload re-captured after a move: %q, want %q", got, opened)
	}
}

func TestThemePickerEnterCommitsAndCloses(t *testing.T) {
	m := themePickerModel(t)

	// Choose a theme that is not current so the commit is observable.
	target := -1
	current := m.themeManager.CurrentName()
	for i, item := range m.searchPopup.filtered {
		if item.Text != current {
			target = i
			break
		}
	}
	if target < 0 {
		t.Fatal("no non-current theme to commit")
	}
	m.searchPopup.selected = target
	m.previewThemeSelection()
	want := m.searchPopup.filtered[target].Text

	if !m.handleSearchPopupKey(tea.Key{Code: tea.KeyEnter}) {
		t.Fatal("Enter was not handled by the theme picker")
	}
	if got := m.themeManager.CurrentName(); got != want {
		t.Errorf("Enter committed %q, want %q", got, want)
	}
	if m.searchPopup != nil {
		t.Error("Enter left the picker open")
	}
}

func TestThemePickerFilterNarrowsAndPreviews(t *testing.T) {
	m := themePickerModel(t)

	// Typing narrows the list instead of redisplaying everything.
	m.searchPopup.search = "tokyo"
	m.searchPopup.filterSearch()
	m.previewThemeSelection()

	if len(m.searchPopup.filtered) == 0 {
		t.Fatal("filter for a known theme prefix returned nothing")
	}
	for _, item := range m.searchPopup.filtered {
		if !containsFold(item.Text, "tokyo") && !containsFold(item.Description, "tokyo") {
			t.Errorf("row %q survived a filter it does not match", item.Text)
		}
	}
}

func TestThemePickerNoMatchStaysEmpty(t *testing.T) {
	m := themePickerModel(t)
	m.searchPopup.search = "zzzz-no-such-theme"
	m.searchPopup.filterSearch()
	if len(m.searchPopup.filtered) != 0 {
		t.Errorf("no-match filter returned %d rows, want 0", len(m.searchPopup.filtered))
	}
	// Previewing an empty list must not panic or change the theme.
	before := m.themeManager.CurrentName()
	m.previewThemeSelection()
	if got := m.themeManager.CurrentName(); got != before {
		t.Errorf("preview on an empty list changed the theme to %q", got)
	}
}

func TestThemePickerSecondCommandClosesAndReverts(t *testing.T) {
	m := newTestModelFull(t)
	m.themeManager = NewThemeManager()
	m.palette = paletteFor(m.themeManager.Current())
	opened := m.themeManager.CurrentName()

	// First bare /theme opens and may preview row 0.
	_, _ = m.handleThemeCommand(nil)
	if m.searchPopup == nil {
		t.Fatal("bare /theme did not open the picker")
	}
	// Second bare /theme closes it, reverting like Esc.
	_, _ = m.handleThemeCommand(nil)
	if m.searchPopup != nil {
		t.Error("second /theme left the picker open")
	}
	if got := m.themeManager.CurrentName(); got != opened {
		t.Errorf("closing the picker left theme %q, want %q", got, opened)
	}
}

func containsFold(haystack, needle string) bool {
	return len(needle) == 0 || indexFold(haystack, needle) >= 0
}

func indexFold(haystack, needle string) int {
	h := make([]byte, len(haystack))
	for i := 0; i < len(haystack); i++ {
		c := haystack[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		h[i] = c
	}
	n := make([]byte, len(needle))
	for i := 0; i < len(needle); i++ {
		c := needle[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		n[i] = c
	}
	for i := 0; i+len(n) <= len(h); i++ {
		match := true
		for j := range n {
			if h[i+j] != n[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
