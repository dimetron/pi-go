package tui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// searchItems builds n placeholder entries named cmd-0..cmd-(n-1).
func searchItems(n int) []SearchItem {
	items := make([]SearchItem, n)
	for i := range items {
		items[i] = SearchItem{Text: fmt.Sprintf("cmd-%d", i), Description: fmt.Sprintf("desc-%d", i)}
	}
	return items
}

// newSearchPopupModel builds a model whose prompt reads "PRE" and whose popup
// is in the requested state, so a test can tell an untouched prompt from one
// the popup wrote to.
func newSearchPopupModel(t *testing.T, mode searchMode, n, selected int, search string, height, scrollOff int) *model {
	t.Helper()
	m := &model{cfg: Config{}, inputModel: NewInputModel(nil, nil, nil, ""), width: 80, height: 40}
	m.inputModel.SetText("PRE")
	items := searchItems(n)
	m.searchPopup = &searchPopupState{
		mode:      mode,
		entries:   items,
		filtered:  items,
		selected:  selected,
		search:    search,
		height:    height,
		scrollOff: scrollOff,
	}
	return m
}

func TestSearchPopupSelectPrev(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                           string
		n, selected, height, scrollOff int
		wantSelected, wantScrollOff    int
	}{
		{"mid list steps up and follows the window", 10, 4, 3, 3, 3, 1},
		{"inside the window does not scroll", 10, 2, 3, 0, 1, 0},
		{"from the first wraps to the last and scrolls there", 10, 0, 3, 0, 9, 7},
		{"single item stays put", 1, 0, 3, 0, 0, 0},
		{"empty list stays put", 0, 0, 3, 0, 0, 0},
		{"near the top clamps scrollOff at zero", 10, 1, 3, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sp := newSearchPopupModel(t, searchModeCommands, tt.n, tt.selected, "", tt.height, tt.scrollOff).searchPopup
			sp.selectPrev()
			if sp.selected != tt.wantSelected || sp.scrollOff != tt.wantScrollOff {
				t.Errorf("selectPrev() = (sel %d, off %d), want (sel %d, off %d)",
					sp.selected, sp.scrollOff, tt.wantSelected, tt.wantScrollOff)
			}
		})
	}
}

func TestSearchPopupSelectNext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                           string
		n, selected, height, scrollOff int
		wantSelected, wantScrollOff    int
	}{
		{"steps down without scrolling inside the window", 10, 0, 3, 0, 1, 0},
		{"steps down and pushes the window", 10, 2, 3, 0, 3, 1},
		{"already scrolled stays consistent", 10, 4, 3, 3, 5, 3},
		{"single item stays put", 1, 0, 3, 0, 0, 0},
		{"empty list stays put", 0, 0, 3, 0, 0, 0},
		// KNOWN QUIRK, pinned deliberately: wrapping from the last item back to
		// the first moves the highlight to 0 but leaves scrollOff where it was,
		// so the highlight lands outside the visible window. selectPrev has no
		// such gap because it recomputes scrollOff unconditionally. Preserved as
		// found; if this is ever fixed, this expectation is the one to change.
		{"wrap to first leaves scrollOff stale", 10, 9, 3, 7, 0, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sp := newSearchPopupModel(t, searchModeCommands, tt.n, tt.selected, "", tt.height, tt.scrollOff).searchPopup
			sp.selectNext()
			if sp.selected != tt.wantSelected || sp.scrollOff != tt.wantScrollOff {
				t.Errorf("selectNext() = (sel %d, off %d), want (sel %d, off %d)",
					sp.selected, sp.scrollOff, tt.wantSelected, tt.wantScrollOff)
			}
		})
	}
}

func TestSearchPopupSelectByTab(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                           string
		shift                          bool
		n, selected, height, scrollOff int
		wantSelected, wantScrollOff    int
	}{
		{"tab advances", false, 10, 0, 3, 0, 1, 0},
		{"tab advances and scrolls", false, 10, 2, 3, 0, 3, 1},
		{"tab stops on the last item without wrapping", false, 10, 9, 3, 7, 9, 7},
		{"shift+tab steps back", true, 10, 9, 3, 7, 8, 6},
		{"shift+tab wraps to the last from the first", true, 10, 0, 3, 0, 9, 7},
		{"single item stays put", false, 1, 0, 3, 0, 0, 0},
		// An empty list is left completely alone — scrollOff included, which is
		// why the guard has to come before the scroll recompute.
		{"empty list is untouched", false, 0, 0, 3, 5, 0, 5},
		{"empty list is untouched under shift", true, 0, 0, 3, 5, 0, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sp := newSearchPopupModel(t, searchModeCommands, tt.n, tt.selected, "", tt.height, tt.scrollOff).searchPopup
			sp.selectByTab(tt.shift)
			if sp.selected != tt.wantSelected || sp.scrollOff != tt.wantScrollOff {
				t.Errorf("selectByTab(%v) = (sel %d, off %d), want (sel %d, off %d)",
					tt.shift, sp.selected, sp.scrollOff, tt.wantSelected, tt.wantScrollOff)
			}
		})
	}
}

func TestAcceptSearchSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		mode          searchMode
		n, selected   int
		wantText      string
		wantPopupOpen bool
	}{
		{"a command gets a trailing space", searchModeCommands, 5, 0, "cmd-0 ", false},
		{"a later command is taken by index", searchModeCommands, 5, 3, "cmd-3 ", false},
		{"a history entry is restored verbatim", searchModeHistory, 4, 1, "cmd-1", false},
		{"an empty list does nothing", searchModeCommands, 0, 0, "PRE", true},
		{"a stale highlight does nothing", searchModeCommands, 3, 7, "PRE", true},
		// A mode this function does not know leaves the prompt alone AND leaves
		// the popup open, rather than closing on a key that did nothing.
		{"an unknown mode does nothing", searchMode("bogus"), 3, 1, "PRE", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newSearchPopupModel(t, tt.mode, tt.n, tt.selected, "", 3, 0)
			m.acceptSearchSelection()
			if m.inputModel.Text != tt.wantText {
				t.Errorf("prompt = %q, want %q", m.inputModel.Text, tt.wantText)
			}
			if open := m.searchPopup != nil; open != tt.wantPopupOpen {
				t.Errorf("popup open = %v, want %v", open, tt.wantPopupOpen)
			}
		})
	}
}

func TestBackspaceSearch(t *testing.T) {
	t.Parallel()

	t.Run("trims the filter and refilters", func(t *testing.T) {
		t.Parallel()
		m := newSearchPopupModel(t, searchModeCommands, 5, 3, "cmd", 2, 2)
		m.backspaceSearch()
		if m.searchPopup == nil {
			t.Fatal("popup closed, want it still open")
		}
		if got := m.searchPopup.search; got != "cm" {
			t.Errorf("search = %q, want %q", got, "cm")
		}
		// filterSearch resets the cursor to the top of the new result set.
		if m.searchPopup.selected != 0 || m.searchPopup.scrollOff != 0 {
			t.Errorf("after refilter: sel %d off %d, want 0 and 0",
				m.searchPopup.selected, m.searchPopup.scrollOff)
		}
	})

	t.Run("an empty filter closes the popup", func(t *testing.T) {
		t.Parallel()
		m := newSearchPopupModel(t, searchModeCommands, 5, 0, "", 3, 0)
		m.backspaceSearch()
		if m.searchPopup != nil {
			t.Error("popup still open, want it closed")
		}
		if m.inputModel.Text != "PRE" {
			t.Errorf("prompt = %q, want it untouched", m.inputModel.Text)
		}
	})
}

// TestTypeIntoSearch pins which keystrokes reach the filter. Declining matters
// as much as claiming: a declined key falls through to the prompt, so widening
// this predicate would swallow keystrokes the input still needs.
func TestTypeIntoSearch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		key         tea.Key
		wantHandled bool
		wantSearch  string
	}{
		{"a printable rune is appended", tea.Key{Code: 'a', Text: "a"}, true, "xa"},
		{"a space is appended", tea.Key{Code: tea.KeySpace, Text: " "}, true, "x "},
		{"a modified rune is declined", tea.Key{Code: 'a', Text: "a", Mod: tea.ModCtrl}, false, "x"},
		{"a key with no text is declined", tea.Key{Code: tea.KeyF1}, false, "x"},
		{"a rune that carries no text is declined", tea.Key{Code: 'z'}, false, "x"},
		// len() counts bytes, so any non-ASCII character is more than one byte
		// and never reaches the filter. Pinned as found; see the report.
		{"a multi-byte character is declined", tea.Key{Code: 'é', Text: "é"}, false, "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newSearchPopupModel(t, searchModeCommands, 5, 0, "x", 3, 0)
			if got := m.typeIntoSearch(tt.key); got != tt.wantHandled {
				t.Errorf("typeIntoSearch() handled = %v, want %v", got, tt.wantHandled)
			}
			if got := m.searchPopup.search; got != tt.wantSearch {
				t.Errorf("search = %q, want %q", got, tt.wantSearch)
			}
		})
	}
}

// TestHandleSearchPopupKeyDispatch checks the routing itself: which keys the
// popup claims, and that a closed popup claims nothing at all.
func TestHandleSearchPopupKeyDispatch(t *testing.T) {
	t.Parallel()

	claimed := []struct {
		name string
		key  tea.Key
	}{
		{"up", tea.Key{Code: tea.KeyUp}},
		{"down", tea.Key{Code: tea.KeyDown}},
		{"tab", tea.Key{Code: tea.KeyTab}},
		{"shift+tab", tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}},
		{"enter", tea.Key{Code: tea.KeyEnter}},
		{"esc", tea.Key{Code: tea.KeyEsc}},
		{"backspace", tea.Key{Code: tea.KeyBackspace}},
		{"printable rune", tea.Key{Code: 'a', Text: "a"}},
	}
	for _, tt := range claimed {
		t.Run("claims "+tt.name, func(t *testing.T) {
			t.Parallel()
			m := newSearchPopupModel(t, searchModeCommands, 5, 1, "", 3, 0)
			if !m.handleSearchPopupKey(tt.key) {
				t.Errorf("handleSearchPopupKey(%v) = false, want it claimed", tt.name)
			}
		})
		t.Run("declines "+tt.name+" with no popup", func(t *testing.T) {
			t.Parallel()
			m := &model{cfg: Config{}, inputModel: NewInputModel(nil, nil, nil, ""), width: 80, height: 40}
			if m.handleSearchPopupKey(tt.key) {
				t.Errorf("handleSearchPopupKey(%v) = true with no popup, want declined", tt.name)
			}
		})
	}

	declined := []struct {
		name string
		key  tea.Key
	}{
		{"modified rune", tea.Key{Code: 'a', Text: "a", Mod: tea.ModCtrl}},
		{"function key", tea.Key{Code: tea.KeyF1}},
		{"multi-byte character", tea.Key{Code: 'é', Text: "é"}},
	}
	for _, tt := range declined {
		t.Run("declines "+tt.name, func(t *testing.T) {
			t.Parallel()
			m := newSearchPopupModel(t, searchModeCommands, 5, 1, "", 3, 0)
			if m.handleSearchPopupKey(tt.key) {
				t.Errorf("handleSearchPopupKey(%v) = true, want declined", tt.name)
			}
			// A declined key must leave the popup exactly as it found it.
			if m.searchPopup == nil || m.searchPopup.selected != 1 || m.searchPopup.search != "" {
				t.Error("a declined key mutated the popup")
			}
		})
	}

	t.Run("esc closes the popup", func(t *testing.T) {
		t.Parallel()
		m := newSearchPopupModel(t, searchModeCommands, 5, 1, "", 3, 0)
		m.handleSearchPopupKey(tea.Key{Code: tea.KeyEsc})
		if m.searchPopup != nil {
			t.Error("popup still open after Esc")
		}
	})
}
