package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The expanded sidebar carries exactly one control, on its version row: a glyph
// that collapses it. The row has to keep the whole version string while doing
// so — the build name is the reason the row is topmost, and spending its
// columns on a button would trade the diagnostic for the affordance.
func TestSidebarVersionRowCarriesTheCollapseGlyph(t *testing.T) {
	const version = "1.4.2+a1b2c3d"
	out := ansi.Strip(RenderSidebar(SidebarRenderInput{
		Width:      SidebarWidth,
		Height:     40,
		AppVersion: version,
	}))

	first, _, _ := strings.Cut(out, "\n")
	if !strings.Contains(first, "pi-go "+version) {
		t.Errorf("version row %q does not carry the full build string %q", first, "pi-go "+version)
	}
	if !strings.Contains(first, sidebarHideGlyph) {
		t.Errorf("version row %q has no collapse glyph %q", first, sidebarHideGlyph)
	}
	if got := ansi.StringWidth(first); got != SidebarWidth {
		t.Errorf("version row is %d cells, want %d", got, SidebarWidth)
	}
}

// The glyph is drawn even when no version is configured. A build with no
// version string is exactly where the panel still needs a way to be put away,
// so the control must not be conditional on the label.
func TestSidebarCollapseGlyphSurvivesAnEmptyVersion(t *testing.T) {
	out := ansi.Strip(RenderSidebar(SidebarRenderInput{Width: SidebarWidth, Height: 20}))
	first, _, _ := strings.Cut(out, "\n")
	if !strings.Contains(first, sidebarHideGlyph) {
		t.Errorf("version row %q has no collapse glyph without an AppVersion", first)
	}
}

// The whole point of the feature, driven through the real click path rather than
// by setting the flag: a click on the version row collapses the panel, the chat
// takes the columns it gave up, and a click on the strip puts it back.
//
// The width assertions are what prove the click did something observable rather
// than only flipping model state: mainWidth is recomputed from sidebarWidth, so
// a stale layout would leave the transcript wrapped for the old panel.
func TestSidebarCollapseAndRestoreByClick(t *testing.T) {
	m := historyModel(t, "")
	m.width, m.height = 120, 24
	m.applyResize()
	m.cfg.AppVersion = "1.4.2"
	m.chatModel.Messages = append(m.chatModel.Messages, realisticChat()...)
	m.syncPalette()
	m.View() // establishes frameRows and the sidebar's column

	if got, want := m.mainWidth(), 120-SidebarWidth; got != want {
		t.Fatalf("expanded mainWidth = %d, want %d", got, want)
	}

	// The version row, in the sidebar column. Click coordinates are absolute
	// terminal rows, so they are offset by where the frame starts — the frame
	// does not always begin on row 0.
	top := m.frameTop()
	x := m.mainWidth() + SidebarWidth/2
	next, _ := m.handleMouseClick(tea.MouseClickMsg(tea.Mouse{X: x, Y: top, Button: tea.MouseLeft}))
	m, ok := next.(*model)
	if !ok {
		t.Fatalf("handleMouseClick returned %T, want *model", next)
	}
	if !m.sidebarCollapsed {
		t.Fatal("the click on the version row did not collapse the sidebar")
	}
	if got, want := m.mainWidth(), 120-SidebarStripWidth; got != want {
		t.Errorf("collapsed mainWidth = %d, want %d — the chat did not take the columns back", got, want)
	}
	if got := m.sidebarWidth(); got != SidebarStripWidth {
		t.Errorf("collapsed sidebarWidth = %d, want %d", got, SidebarStripWidth)
	}

	// Collapsed, the frame must still be a rectangle: every row the terminal's
	// width, so the strip cannot shift the rail or overhang the right edge.
	assertFrameWidth(t, m, 120)

	// The strip is one column at the right edge, so the restore click lands on it.
	next, _ = m.handleMouseClick(tea.MouseClickMsg(tea.Mouse{
		X: 119, Y: m.topSectionRows() - 2, Button: tea.MouseLeft,
	}))
	m, ok = next.(*model)
	if !ok {
		t.Fatalf("handleMouseClick returned %T, want *model", next)
	}
	if m.sidebarCollapsed {
		t.Fatal("the click on the strip did not restore the sidebar")
	}
	if got, want := m.mainWidth(), 120-SidebarWidth; got != want {
		t.Errorf("restored mainWidth = %d, want %d", got, want)
	}
	assertFrameWidth(t, m, 120)
}

// Only the sidebar's own control toggles it. A click in the chat panel on the
// same row is a selection gesture, and the sidebar must survive it — otherwise
// the first click of a drag-to-copy would collapse the panel out from under the
// pointer.
func TestSidebarToggleIgnoresClicksInTheChatPanel(t *testing.T) {
	m := historyModel(t, "")
	m.width, m.height = 120, 24
	m.applyResize()
	m.cfg.AppVersion = "1.4.2"
	m.View()

	next, _ := m.handleMouseClick(tea.MouseClickMsg(tea.Mouse{X: 10, Y: 0, Button: tea.MouseLeft}))
	m, ok := next.(*model)
	if !ok {
		t.Fatalf("handleMouseClick returned %T, want *model", next)
	}
	if m.sidebarCollapsed {
		t.Error("a click on the chat panel's first row collapsed the sidebar")
	}
	if !m.sel.dragging {
		t.Error("a click on the chat panel did not start a selection")
	}
}

// The strip replaces the whole panel, so it has to hold the frame's shape at
// every size the panel itself is drawn at — and it has to be visible, or the
// collapse would be a one-way trip with no way to click back.
func TestCollapsedSidebarKeepsTheFrameIntact(t *testing.T) {
	for _, width := range []int{100, 120, 172, 200} {
		for _, height := range []int{24, 40} {
			m := historyModel(t, "")
			m.width, m.height = width, height
			m.cfg.AppVersion = "1.4.2"
			m.sidebarCollapsed = true
			m.applyResize()
			m.chatModel.Messages = append(m.chatModel.Messages, realisticChat()...)
			m.syncPalette()

			frame := m.View().Content
			assertFrameWidth(t, m, width)

			if got, want := m.sidebarWidth(), SidebarStripWidth; got != want {
				t.Fatalf("%dx%d: sidebarWidth = %d, want %d", width, height, got, want)
			}

			// The restore control is on the frame, in the strip's column.
			rows := strings.Split(ansi.Strip(frame), "\n")
			if !strings.Contains(rows[0], sidebarRestoreGlyph) {
				t.Fatalf("%dx%d: strip row %q carries no restore glyph", width, height, rows[0])
			}
			// And every top-section row still has the rail in its own column.
			for row := 0; row < m.topSectionRows() && row < len(rows); row++ {
				if g := runeAtCol(rows[row], m.mainWidth()-railWidth); !isRailCell(g) {
					t.Fatalf("%dx%d: row %d has %q at rail column %d, want the rail\n%q",
						width, height, row, g, m.mainWidth()-railWidth, rows[row])
				}
			}
		}
	}
}

// A frame too narrow to hold the sidebar at all has nowhere to collapse to, so
// there is no strip and no restore control. Clicking where the sidebar would
// have been must not toggle anything or index past the frame.
func TestNarrowFrameKeepsTheStripAndCanToggle(t *testing.T) {
	m := historyModel(t, "")
	m.width, m.height = 70, 30
	m.applyResize()

	// The rule -- not the user -- collapses it, so the preference flag stays off.
	if got := m.sidebarWidth(); got != SidebarStripWidth {
		t.Fatalf("sidebarWidth = %d on a 70-column frame, want the strip", got)
	}
	if m.sidebarCollapsed {
		t.Error("the auto rule set the user's collapse preference")
	}

	m.syncPalette()
	m.View() // lay out the strip so the hit test has coordinates
	// The strip is at the right edge, so the restore click lands on it.
	next, _ := m.handleMouseClick(tea.MouseClickMsg(tea.Mouse{X: 69, Y: m.frameTop(), Button: tea.MouseLeft}))
	got, ok := next.(*model)
	if !ok {
		t.Fatalf("handleMouseClick returned %T, want *model", next)
	}
	if w := got.sidebarWidth(); w != SidebarWidth {
		t.Errorf("sidebarWidth = %d after clicking the strip on a narrow frame, want %d", w, SidebarWidth)
	}
}

// Both glyphs are chrome the TUI draws, so a terminal that resolves ambiguous
// width as wide must not be able to re-measure them: a two-cell glyph would
// shove the rail and the chat a column left the moment the panel collapsed.
func TestSidebarToggleGlyphsAreWidthSafe(t *testing.T) {
	for _, g := range []string{sidebarHideGlyph, sidebarRestoreGlyph} {
		if got := ansi.StringWidth(g); got != 1 {
			t.Errorf("toggle glyph %q measures %d cells, want 1", g, got)
		}
		for _, r := range g {
			if !widthSafe(r) {
				t.Errorf("toggle glyph %q is width-unsafe (U+%04X)", g, r)
			}
		}
	}
	if sidebarHideGlyph == sidebarRestoreGlyph {
		t.Error("the collapse and restore glyphs are the same; the state is invisible")
	}
	for _, rail := range []string{railGlyph, railThumb, railFoot} {
		if sidebarRestoreGlyph == rail {
			t.Errorf("the restore glyph %q is the rail's own %q, so the strip reads as a bare rail",
				sidebarRestoreGlyph, rail)
		}
	}
}

// assertFrameWidth fails unless every row of the current frame is exactly the
// terminal's width — a short row lets the sidebar slide left, a long one pushes
// it past the right edge.
func assertFrameWidth(t *testing.T, m *model, width int) {
	t.Helper()
	for row, line := range strings.Split(m.View().Content, "\n") {
		if got := ansi.StringWidth(line); got != width {
			t.Fatalf("row %d is %d cells, want %d\n%q", row, got, width, ansi.Strip(line))
		}
	}
}

// A click above the frame's first row is not on the sidebar. The terminal can
// report a coordinate the frame does not cover — scrollback rows above it — and
// mapping that to a frame row yields a negative index.
//
// The collapsed state is the one that matters: the strip's hit test is
// `row < topSectionRows`, and a negative row satisfies it, so without the guard
// a click in the scrollback just above a collapsed strip would restore the
// panel. The expanded test (`row == 0`) happens to reject -1 on its own, which
// is why this case has to be the collapsed one to prove anything.
func TestSidebarToggleIgnoresClicksAboveTheFrame(t *testing.T) {
	m := historyModel(t, "")
	m.width, m.height = 120, 24
	m.applyResize()
	// A short transcript produces a frame shorter than the terminal, which is the
	// state this test needs: the frame's first row is below row 0.
	m.chatModel.Messages = append(m.chatModel.Messages, realisticChat()[:3]...)
	m.sidebarCollapsed = true
	m.syncPalette()
	m.View()
	if top := m.frameTop(); top == 0 {
		t.Fatal("frameTop = 0; the frame did not start below row 0, so there is no row above it to click")
	}

	x := m.mainWidth() + 5
	if m.hitSidebarToggle(x, 0) {
		t.Error("a click above the frame reported a hit on the collapsed strip")
	}
	next, _ := m.handleMouseClick(tea.MouseClickMsg(tea.Mouse{X: x, Y: 0, Button: tea.MouseLeft}))
	got, ok := next.(*model)
	if !ok {
		t.Fatalf("handleMouseClick returned %T, want *model", next)
	}
	if !got.sidebarCollapsed {
		t.Error("a click above the frame restored the sidebar")
	}
}

// Collapsing needs the strip's column plus a chat panel still worth reading.
// Between those two floors there is no room for both, and the panel must stay
// expanded rather than collapse to a strip that leaves the chat 80 columns.
func TestSidebarWidthFollowsTheSmallScreenRule(t *testing.T) {
	for _, tc := range []struct {
		width, height int
		collapsed     bool
		expanded      bool
		want          int
		why           string
	}{
		{60, 30, false, false, SidebarStripWidth, "below the width threshold the rule collapses it"},
		{SidebarAutoCollapseWidth - 1, 30, false, false, SidebarStripWidth, "still below the threshold"},
		{SidebarAutoCollapseWidth, 30, false, false, SidebarWidth, "at the threshold the panel fits"},
		{160, 160, false, false, SidebarWidth, "a square is not portrait"},
		{200, 60, false, false, SidebarWidth, "wide landscape keeps the panel"},
		{200, 60, true, false, SidebarStripWidth, "a user's collapse wins over a roomy frame"},
		{60, 30, false, true, SidebarWidth, "a click overrides the rule for this frame"},
		{60, 30, true, true, SidebarStripWidth, "an explicit collapse still wins over the override"},
	} {
		m := &model{
			width: tc.width, height: tc.height,
			sidebarCollapsed: tc.collapsed, sidebarExpanded: tc.expanded,
		}
		if got := m.sidebarWidth(); got != tc.want {
			t.Errorf("w=%d h=%d collapsed=%v expanded=%v: sidebarWidth = %d, want %d — %s",
				tc.width, tc.height, tc.collapsed, tc.expanded, got, tc.want, tc.why)
		}
	}
}
