package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// resizeTo delivers a real WindowSizeMsg, so the auto rule is exercised through
// the path the terminal drives rather than by calling the helper.
func resizeTo(m *model, width, height int) *model {
	next, _, _ := m.handleWindowSize(tea.WindowSizeMsg{Width: width, Height: height})
	resized, ok := next.(*model)
	if !ok {
		return m
	}
	return resized
}

// A small screen collapses the sidebar rather than deleting it. The distinction
// is the whole point: a hidden panel with no control left behind is a one-way
// trip, while a collapsed one keeps the restore glyph on screen.
func TestSmallScreenCollapsesRatherThanHides(t *testing.T) {
	for _, dim := range [][2]int{
		{90, 40},   // narrow: below the width threshold
		{60, 120},  // very narrow
		{100, 120}, // portrait: tall and narrow
		{90, 91},   // portrait by one row
	} {
		width, height := dim[0], dim[1]
		m := resizeTo(historyModel(t, "first"), width, height)

		if m.sidebarCollapsed {
			t.Errorf("%dx%d: the user's collapse flag was set by the auto rule; the rule is not a preference", width, height)
		}
		if got := m.sidebarWidth(); got != SidebarStripWidth {
			t.Errorf("%dx%d: sidebarWidth = %d, want the %d-column strip — the panel was hidden, not collapsed",
				width, height, got, SidebarStripWidth)
		}
		// The chat gets everything except the strip.
		if got, want := m.mainWidth(), width-SidebarStripWidth; got != want {
			t.Errorf("%dx%d: mainWidth = %d, want %d", width, height, got, want)
		}
	}
}

// Even a very narrow frame keeps the strip. The sidebar is never absent, because
// the control that would bring it back lives in that one column — hiding it
// outright is the one-way trip this rule exists to avoid.
func TestVeryNarrowFrameStillKeepsTheStrip(t *testing.T) {
	for _, width := range []int{30, 40, 60, SidebarAutoCollapseWidth - 1} {
		m := resizeTo(historyModel(t, "first"), width, 24)
		if got := m.sidebarWidth(); got != SidebarStripWidth {
			t.Errorf("width %d: sidebarWidth = %d, want the %d-column strip", width, got, SidebarStripWidth)
		}
		if got, want := m.mainWidth(), width-SidebarStripWidth; got != want {
			t.Errorf("width %d: mainWidth = %d, want %d", width, got, want)
		}
	}
}

// Growing the window back expands a panel the auto rule took away. Without this
// the sidebar would be gone for the rest of the session after one narrow resize.
func TestGrowingTheFrameRestoresTheAutoCollapsedSidebar(t *testing.T) {
	m := resizeTo(historyModel(t, "first"), 90, 40)
	if got := m.sidebarWidth(); got != SidebarStripWidth {
		t.Fatalf("sidebarWidth = %d at 90x40, want the strip; the test cannot reach the restore path", got)
	}

	m = resizeTo(m, 160, 40)
	if got := m.sidebarWidth(); got != SidebarWidth {
		t.Errorf("sidebarWidth = %d after growing, want %d — the rule did not undo its own collapse", got, SidebarWidth)
	}
	if got := m.sidebarWidth(); got != SidebarWidth {
		t.Errorf("sidebarWidth = %d after growing, want %d", got, SidebarWidth)
	}
}

// A collapse the user asked for is theirs to undo. Resizing must not expand it
// back, or dragging the window would silently reopen a panel they closed.
func TestResizeDoesNotUndoAUsersCollapse(t *testing.T) {
	m := resizeTo(historyModel(t, "first"), 160, 40)
	m.sidebarCollapsed = true

	m = resizeTo(m, 200, 60)
	if !m.sidebarCollapsed {
		t.Error("a resize reopened a sidebar the user had collapsed")
	}
	if got := m.sidebarWidth(); got != SidebarStripWidth {
		t.Errorf("sidebarWidth = %d, want the strip", got)
	}
}

// Clicking the strip on a small frame is the user overruling the rule for this
// frame. The next resize is free to apply the rule again — but the click itself
// must stick, or the control would do nothing on exactly the frames that need it.
func TestClickingTheStripOnASmallFrameExpandsIt(t *testing.T) {
	m := resizeTo(historyModel(t, "first"), 90, 40)
	if got := m.sidebarWidth(); got != SidebarStripWidth {
		t.Fatalf("sidebarWidth = %d at 90x40, want the strip", got)
	}
	m.syncPalette()
	m.View() // lays out the strip so the hit test has coordinates

	next, _ := m.handleMouseClick(tea.MouseClickMsg(tea.Mouse{
		X: m.width - 1, Y: m.frameTop(), Button: tea.MouseLeft,
	}))
	got, ok := next.(*model)
	if !ok {
		t.Fatalf("handleMouseClick returned %T, want *model", next)
	}
	if got.sidebarCollapsed {
		t.Error("clicking the strip on a small frame did not expand the sidebar")
	}
	if !got.sidebarExpanded {
		t.Error("the click did not record the override, so the next frame would re-collapse the panel")
	}
	if w := got.sidebarWidth(); w != SidebarWidth {
		t.Errorf("sidebarWidth = %d after the click, want %d", w, SidebarWidth)
	}
}

// The frame itself: on a small screen the chat is wide and the strip is on
// screen with a restore glyph. This is the assertion that fails if the rule is
// wired into the width math but not into View.
func TestSmallScreenFrameHasTheStrip(t *testing.T) {
	width, height := 90, 120
	m := newTestModel(t)
	m.width, m.height = width, height
	m.applyResize()
	m.chatModel.Messages = append(m.chatModel.Messages, realisticChat()...)

	frame := m.View().Content
	for row, line := range strings.Split(frame, "\n") {
		if got := ansi.StringWidth(line); got != width {
			t.Fatalf("row %d: width %d, want %d", row, got, width)
		}
	}

	plain := ansi.Strip(frame)
	if !strings.Contains(plain, sidebarRestoreGlyph) {
		t.Error("a small screen has no restore glyph — the sidebar was hidden, not collapsed")
	}
	// The provider row is the expanded panel's unmistakable marker.
	if strings.Contains(plain, "test-provider") {
		t.Error("a small screen still renders the expanded sidebar")
	}
}

// The same model on a large landscape frame renders the expanded panel, so the
// assertion above fails for the reason claimed rather than because the marker
// never appears.
func TestLargeLandscapeFrameHasTheExpandedSidebar(t *testing.T) {
	width, height := 160, 40
	m := newTestModel(t)
	m.width, m.height = width, height
	m.applyResize()
	m.chatModel.Messages = append(m.chatModel.Messages, realisticChat()...)

	plain := ansi.Strip(m.View().Content)
	if !strings.Contains(plain, "test-provider") {
		t.Error("a large landscape frame has no expanded sidebar")
	}
	if strings.Contains(plain, sidebarRestoreGlyph) {
		t.Error("a large landscape frame is showing the collapsed strip")
	}
}

// An unsized frame is not a small one. Before the first WindowSizeMsg the model
// has no height, and collapsing there would make the first frame differ from
// every frame after it.
func TestUnsizedFrameKeepsTheSidebar(t *testing.T) {
	m := resizeTo(historyModel(t, "first"), 160, 0)
	if m.sidebarCollapsed {
		t.Error("the sidebar collapsed before the first WindowSizeMsg")
	}
	if got, want := m.mainWidth(), 160-SidebarWidth; got != want {
		t.Errorf("mainWidth = %d, want %d", got, want)
	}
}
