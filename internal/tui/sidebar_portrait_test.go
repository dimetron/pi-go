package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A portrait frame has no sidebar. The mobile case — `pi serve` opened in a
// phone browser — is tall and narrow, and 23 fixed columns there are a quarter
// of the width spent on short labels while the chat is squeezed into the rest.
func TestSidebarHiddenWhenPortrait(t *testing.T) {
	portrait := [][2]int{{60, 120}, {80, 100}, {100, 120}, {90, 91}}
	for _, dim := range portrait {
		width, height := dim[0], dim[1]
		m := historyModel(t, "first")
		m.width, m.height = width, height
		m.applyResize()

		if m.sidebarVisible() {
			t.Errorf("%dx%d: sidebar visible in a portrait frame", width, height)
		}
		if got := m.mainWidth(); got != width {
			t.Errorf("%dx%d: mainWidth = %d, want the full %d — the sidebar's columns were not returned",
				width, height, got, width)
		}
	}
}

// Landscape and square frames keep the sidebar: the columns are the thing that
// makes it affordable, and a wide-then-short window has them.
func TestSidebarShownWhenLandscape(t *testing.T) {
	landscape := [][2]int{{120, 40}, {100, 30}, {160, 40}, {120, 120}}
	for _, dim := range landscape {
		width, height := dim[0], dim[1]
		m := historyModel(t, "first")
		m.width, m.height = width, height
		m.applyResize()

		if !m.sidebarVisible() {
			t.Errorf("%dx%d: sidebar hidden in a landscape frame", width, height)
		}
		if got := m.mainWidth(); got != width-SidebarWidth {
			t.Errorf("%dx%d: mainWidth = %d, want %d", width, height, got, width-SidebarWidth)
		}
	}
}

// Too narrow for the sidebar regardless of orientation: at 80 columns the
// panel would be 57 wide, which is not worth the split in either direction.
func TestSidebarHiddenWhenNarrow(t *testing.T) {
	for _, width := range []int{40, 60, 80} {
		m := historyModel(t, "first")
		m.width, m.height = width, 24
		m.applyResize()
		if m.sidebarVisible() {
			t.Errorf("width %d: sidebar visible, panel would be %d columns wide", width, width-SidebarWidth)
		}
	}
}

// An unsized frame is not a portrait one. Before the first WindowSizeMsg the
// model has no height, and dropping the sidebar there would make the first
// frame differ from every frame after it.
func TestSidebarVisibleWithoutHeight(t *testing.T) {
	m := &model{width: 120}
	if !m.sidebarVisible() {
		t.Error("sidebar hidden before the first WindowSizeMsg")
	}
	if got := m.mainWidth(); got != 120-SidebarWidth {
		t.Errorf("mainWidth = %d, want %d", got, 120-SidebarWidth)
	}
}

// The frame itself: in a portrait window the chat owns every column, and no
// row carries a sidebar. This is the assertion that fails if sidebarVisible is
// wired into the width math but not into View.
func TestPortraitFrameHasNoSidebar(t *testing.T) {
	width, height := 60, 120
	m := newTestModel(t)
	m.width, m.height = width, height
	m.applyResize()
	m.chatModel.Messages = append(m.chatModel.Messages, realisticChat()...)

	rows := strings.Split(m.View().Content, "\n")
	for row, line := range rows {
		if got := ansi.StringWidth(line); got != width {
			t.Fatalf("row %d: width %d, want %d", row, got, width)
		}
	}
	// The provider row is the sidebar's unmistakable marker: it appears there
	// and nowhere else in the frame. Its absence is the whole point of the
	// change — at this width the sidebar would need columns the chat cannot
	// spare.
	if strings.Contains(ansi.Strip(m.View().Content), "test-provider") {
		t.Error("a portrait frame still renders the sidebar")
	}
}

// The same model in landscape does render the sidebar, so the assertion above
// fails for the reason claimed rather than because the marker never appears.
func TestLandscapeFrameHasSidebar(t *testing.T) {
	width, height := 120, 40
	m := newTestModel(t)
	m.width, m.height = width, height
	m.applyResize()
	m.chatModel.Messages = append(m.chatModel.Messages, realisticChat()...)

	if !strings.Contains(ansi.Strip(m.View().Content), "test-provider") {
		t.Error("a landscape frame has no sidebar")
	}
}
