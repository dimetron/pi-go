package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

const mermaidSample = `graph LR
    A[Start] --> B{Check}
    B -->|ok| C([Done])
`

func TestRenderMermaidStyled(t *testing.T) {
	got := RenderMermaid(mermaidSample, 90, darkPalette)
	if got == "" {
		t.Fatal("RenderMermaid returned empty for a valid diagram")
	}
	if !strings.Contains(got, "\x1b[") {
		t.Error("output carries no ANSI styling")
	}
	for _, label := range []string{"Start", "Check", "Done"} {
		if !strings.Contains(got, label) {
			t.Errorf("output is missing node label %q", label)
		}
	}
}

// TestRenderMermaidFitsWidth is the property the chat pane depends on: what
// comes back is never wider than the pane, because a terminal cannot scroll
// sideways to reveal the rest.
func TestRenderMermaidFitsWidth(t *testing.T) {
	for _, width := range []int{40, 60, 80, 100, 120} {
		got := RenderMermaid(mermaidSample, width, darkPalette)
		if got == "" {
			continue // rejected as too wide, which is the documented behavior
		}
		for i, line := range strings.Split(got, "\n") {
			if n := lipgloss.Width(line); n > width {
				t.Errorf("width %d: line %d is %d columns wide", width, i+1, n)
			}
		}
	}
}

// TestRenderMermaidRejectsOverflow pins the fallback contract. A three-column
// fan-out cannot fit 20 columns, and the caller must get "" so it can print
// the original fence instead of a mangled diagram.
func TestRenderMermaidRejectsOverflow(t *testing.T) {
	if got := RenderMermaid(mermaidSample, 20, darkPalette); got != "" {
		t.Errorf("expected an empty result for an unfittable width, got %d columns", lipgloss.Width(got))
	}
}

func TestRenderMermaidRejectsNonDiagram(t *testing.T) {
	for _, src := range []string{"", "   ", "this is not a diagram"} {
		if got := RenderMermaid(src, 80, darkPalette); got != "" {
			t.Errorf("expected empty for %q, got:\n%s", src, got)
		}
	}
}

func TestRenderMermaidRejectsNonPositiveWidth(t *testing.T) {
	for _, w := range []int{0, -1} {
		if got := RenderMermaid(mermaidSample, w, darkPalette); got != "" {
			t.Errorf("width %d: expected empty, got %q", w, got)
		}
	}
}

// TestRenderMermaidFollowsPalette asserts the diagram actually changes with the
// theme rather than carrying the renderer's own baked-in ANSI. Light and dark
// palettes use different foregrounds, so the escape sequences must differ.
func TestRenderMermaidFollowsPalette(t *testing.T) {
	dark := RenderMermaid(mermaidSample, 90, darkPalette)
	light := RenderMermaid(mermaidSample, 90, lightPalette)

	if dark == "" || light == "" {
		t.Fatal("one of the palettes produced no output")
	}
	if dark == light {
		t.Error("light and dark renders are byte-identical: the palette is not reaching the diagram")
	}
	if stripANSICodes(dark) != stripANSICodes(light) {
		t.Error("palette changed the layout, not just the colors")
	}
}

// TestMermaidStyleForCoversKeys asserts every style key the renderer emits maps
// to a palette color, and that an unknown key degrades to a readable default
// rather than an unset foreground.
func TestMermaidStyleForCoversKeys(t *testing.T) {
	p := darkPalette
	keys := []string{
		"node", "label", "bold_label", "italic_label", "edge", "arrow",
		"edge_label", "subgraph", "subgraph_label", "note", "default",
		"a-key-that-does-not-exist",
	}
	for _, key := range keys {
		if got := mermaidStyleFor(key, p).Render("x"); !strings.Contains(got, "\x1b[") {
			t.Errorf("style %q rendered without color", key)
		}
	}
}

// stripANSICodes removes CSI sequences so two renders can be compared on
// layout alone.
func stripANSICodes(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
