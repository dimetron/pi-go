package tui

import (
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// TestURLArrowNavigation_ViewportClip reproduces the display bug where typing a
// URL longer than the viewport width, then pressing left arrow, rendered ALL the
// text instead of clipping to the configured width.
//
// Root cause: the textinput initializes with width=0 (unlimited viewport).
// handleOverflow only recalculates the viewport when the cursor moves outside
// current bounds. After SetWidth(n) is called on a still-unlimited viewport,
// the cursor remains within [0, len] so the viewport never narrows, and the
// full text is rendered — causing display overflow and visual instability on
// each left-arrow keypress.
func TestURLArrowNavigation_ViewportClip(t *testing.T) {
	const url = "http://localhost:8080/api/v1/endpoint"
	const width = 20 // narrower than the 36-char URL
	const promptWidth = 2

	im := NewInputModel(nil, nil, nil, "")

	// Type the URL character by character (real keyboard input)
	for _, ch := range url {
		im.HandleKey(tea.KeyPressMsg(tea.Key{Code: ch, Text: string(ch)}))
	}
	if im.Text != url {
		t.Fatalf("after typing: expected %q, got %q", url, im.Text)
	}

	im.SetWidth(width)
	view := im.View(false)
	rendered := ansi.Strip(view)
	got := lipgloss.Width(view)
	t.Logf("initial view: %q width=%d", rendered, got)

	// Cursor at end adds one trailing space (cursor indicator); text area =
	// width chars + 1 cursor = width+1. Total with prompt = width+promptWidth+1.
	maxWidth := width + promptWidth + 1
	if got > maxWidth {
		t.Fatalf("initial render overflows: width=%d want<=%d (view=%q)",
			got, maxWidth, rendered)
	}

	// Navigate left: rendered width must stay identical on every step.
	want := got
	for i := 0; i < 10; i++ {
		im.HandleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
		im.SetWidth(width)
		view = im.View(false)
		w := lipgloss.Width(view)

		if im.Text != url {
			t.Fatalf("left[%d]: text corrupted: %q", i, im.Text)
		}
		if w != want {
			t.Fatalf("left[%d]: rendered width changed from %d to %d (view=%q)",
				i, want, w, ansi.Strip(view))
		}
		if w > maxWidth {
			t.Fatalf("left[%d]: width %d exceeds max %d (view=%q)",
				i, w, maxWidth, ansi.Strip(view))
		}
	}
}

func TestURLArrowNavigation_DoesNotRewriteCursorCellInline(t *testing.T) {
	const url = "http://somehost/path"

	im := NewInputModel(nil, nil, nil, "")
	im.SetWidth(80)

	for _, ch := range url {
		im.HandleKey(tea.KeyPressMsg(tea.Key{Code: ch, Text: string(ch)}))
	}
	for range 6 {
		im.HandleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
	}

	view := im.View(false)
	// Reverse video is the standalone SGR parameter 7. The \b guards keep this
	// from matching a truecolor sequence like 38;2;137;180;250, whose 137
	// contains a 7 but is not reverse video.
	reverseVideo := regexp.MustCompile(`\x1b\[[0-9;]*\b7\b[0-9;]*m`)
	if reverseVideo.MatchString(view) {
		t.Fatalf("input view should not render an inline reverse-video cursor in URL text: %q", view)
	}

	plain := ansi.Strip(view)
	if !strings.Contains(plain, url) {
		t.Fatalf("input view lost URL text: got %q want to contain %q", plain, url)
	}
}

func TestView_UsesRealBubbleTeaCursorForInput(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.Text = "http://somehost/path"
	im.CursorPos = len("http://some")

	m := &model{
		width:      80,
		height:     20,
		inputModel: im,
		chatModel:  NewChatModel(nil),
		statusModel: StatusModel{
			Width: 80,
		},
	}

	view := m.View()
	if view.Cursor == nil {
		t.Fatal("expected view to expose a real Bubble Tea cursor for focused input")
	}
	if view.Cursor.Shape != tea.CursorBar {
		t.Fatalf("expected bar cursor, got shape %v", view.Cursor.Shape)
	}
	if view.Cursor.X <= 2 || view.Cursor.Y <= 0 {
		t.Fatalf("expected cursor to be positioned inside the input line, got %+v", view.Cursor.Position)
	}

	m.running = true
	view = m.View()
	if view.Cursor != nil {
		t.Fatalf("expected no editable input cursor while running, got %+v", view.Cursor)
	}
}
