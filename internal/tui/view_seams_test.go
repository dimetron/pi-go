package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestVisibleMessageWindow pins the viewport clamp. The window it returns also
// drives the minimap, so an off-by-one here moves the scrollbar thumb as well
// as the text — which is why every boundary gets a case.
func TestVisibleMessageWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                           string
		total, availableHeight, scroll int
		wantStart, wantEnd             int
	}{
		{"buffer taller than viewport, unscrolled", 100, 10, 0, 90, 100},
		{"scrolled back by five", 100, 10, 5, 85, 95},
		{"scrolled to the very top", 100, 10, 90, 0, 10},
		{"scroll past the top clamps to zero", 100, 10, 500, 0, 10},
		{"buffer shorter than viewport starts at zero", 5, 10, 0, 0, 5},
		{"buffer exactly fills the viewport", 10, 10, 0, 0, 10},
		{"empty buffer", 0, 10, 0, 0, 0},
		{"zero-height viewport", 100, 0, 0, 100, 100},
		{"single row buffer", 1, 10, 0, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			start, end := visibleMessageWindow(tt.total, tt.availableHeight, tt.scroll)
			if start != tt.wantStart || end != tt.wantEnd {
				t.Errorf("visibleMessageWindow(%d,%d,%d) = (%d,%d), want (%d,%d)",
					tt.total, tt.availableHeight, tt.scroll, start, end, tt.wantStart, tt.wantEnd)
			}
			// The range must always be a valid slice of the buffer.
			if start < 0 || end < start || end > tt.total {
				t.Errorf("range [%d,%d) is not a valid slice of %d rows", start, end, tt.total)
			}
		})
	}
}

func TestRenderStartupView(t *testing.T) {
	t.Parallel()

	t.Run("no loading items shows the matrix line alone", func(t *testing.T) {
		t.Parallel()
		m := newTestModelFull(t)
		m.width = 0
		got := ansi.Strip(m.renderStartupView().Content)
		if strings.Contains(got, "✓") {
			t.Errorf("unexpected tick in %q", got)
		}
		if !strings.HasSuffix(got, "\n") {
			t.Errorf("startup view = %q, want a trailing newline", got)
		}
	})

	t.Run("items are listed in sorted order and ticked when done", func(t *testing.T) {
		t.Parallel()
		m := newTestModelFull(t)
		m.width = 0
		m.loadingItems = map[string]bool{"mcp": true, "lsp": false, "agent": true}
		m.loadingTotal = 3
		lines := strings.Split(strings.TrimRight(ansi.Strip(m.renderStartupView().Content), "\n"), "\n")
		if len(lines) != 4 {
			t.Fatalf("want the matrix line plus 3 items, got %d lines: %q", len(lines), lines)
		}
		want := []string{"  ✓ agent", "    lsp", "  ✓ mcp"}
		for i, w := range want {
			if lines[i+1] != w {
				t.Errorf("line %d = %q, want %q", i+1, lines[i+1], w)
			}
		}
	})

	t.Run("an empty but non-nil map still renders the list form", func(t *testing.T) {
		t.Parallel()
		m := newTestModelFull(t)
		m.width = 0
		m.loadingItems = map[string]bool{}
		got := ansi.Strip(m.renderStartupView().Content)
		if strings.Count(strings.TrimRight(got, "\n"), "\n") != 0 {
			t.Errorf("want just the matrix line, got %q", got)
		}
	})
}

func TestSidebarInputFor(t *testing.T) {
	t.Parallel()

	t.Run("height is the panel row count, not the terminal height", func(t *testing.T) {
		t.Parallel()
		m := newTestModelFull(t)
		m.height = 60
		in := m.sidebarInputFor(30, 17)
		if in.Height != 17 {
			t.Errorf("Height = %d, want the panel's 17 rows", in.Height)
		}
		if in.Width != 30 {
			t.Errorf("Width = %d, want 30", in.Width)
		}
	})

	t.Run("no run state leaves the run fields zero", func(t *testing.T) {
		t.Parallel()
		m := newTestModelFull(t)
		in := m.sidebarInputFor(30, 10)
		if in.RunPhase != "" || in.RunSpec != "" || in.RunCycle != 0 || in.RunMaxCycle != 0 {
			t.Errorf("run fields set without a run: %+v", in)
		}
	})

	t.Run("a run with no phase is not shown", func(t *testing.T) {
		t.Parallel()
		m := newTestModelFull(t)
		m.run = &runState{specName: "spec-a", retries: 1, maxRetries: 3}
		in := m.sidebarInputFor(30, 10)
		if in.RunPhase != "" || in.RunSpec != "" {
			t.Errorf("a phaseless run leaked into the sidebar: %+v", in)
		}
	})

	t.Run("an active run fills the checklist block", func(t *testing.T) {
		t.Parallel()
		m := newTestModelFull(t)
		m.run = &runState{phase: "implement", specName: "spec-a", retries: 1, maxRetries: 3}
		in := m.sidebarInputFor(30, 10)
		if in.RunPhase != "implement" || in.RunSpec != "spec-a" {
			t.Errorf("phase/spec = %q/%q, want implement/spec-a", in.RunPhase, in.RunSpec)
		}
		// Cycle is 1-based for display: the first attempt is cycle 1, not 0.
		if in.RunCycle != 2 || in.RunMaxCycle != 3 {
			t.Errorf("cycle = %d/%d, want 2/3", in.RunCycle, in.RunMaxCycle)
		}
	})
}
