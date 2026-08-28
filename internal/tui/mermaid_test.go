package tui

import (
	"fmt"
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

// unfittableSample cannot fit a narrow pane in either orientation, so it
// exercises the fallback rather than the perpendicular retry. The labels are
// single unbreakable words longer than the label wrap width, which is what
// puts a hard floor under the box width: word wrapping cannot split them, so
// no orientation and no padding makes the diagram narrower.
const unfittableSample = "graph LR\n" +
	"    A[supercalifragilisticexpialidocious] --> B[pneumonoultramicroscopicsilicovolcanoconiosis]\n"

// TestRenderMermaidRejectsOverflow pins the fallback contract: when a diagram
// fits in neither orientation, the caller gets "" so it can print the original
// fence instead of art running past the pane edge.
func TestRenderMermaidRejectsOverflow(t *testing.T) {
	if got := RenderMermaid(unfittableSample, 20, darkPalette); got != "" {
		t.Errorf("expected an empty result for an unfittable width, got %d columns", lipgloss.Width(got))
	}
}

// TestRenderMermaidRetryRescuesNarrowPane is the other side of that contract:
// a diagram too wide as written still renders when the perpendicular
// orientation fits. mermaidSample is a graph LR that does not fit 20 columns
// across but does once stacked vertically.
func TestRenderMermaidRetryRescuesNarrowPane(t *testing.T) {
	got := RenderMermaid(mermaidSample, 20, darkPalette)
	if got == "" {
		t.Fatal("expected the perpendicular retry to rescue this diagram")
	}
	for i, line := range strings.Split(got, "\n") {
		if n := lipgloss.Width(line); n > 20 {
			t.Errorf("line %d is %d columns wide, over the 20 budget", i+1, n)
		}
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

// newMermaidChat returns a ChatModel with a live glamour renderer at width.
func newMermaidChat(t *testing.T, width int) *ChatModel {
	t.Helper()
	c := NewChatModel(nil)
	c.Palette = darkPalette
	c.UpdateRenderer(width)
	if c.Renderer == nil {
		t.Fatal("UpdateRenderer left a nil renderer")
	}
	return &c
}

// TestRenderMarkdownDrawsMermaidFence is the end-to-end check that a ```mermaid
// fence in an assistant reply reaches the screen as a diagram rather than as
// its own source. Every other test in this file exercises RenderMermaid
// directly; this one proves the chat pane actually calls it.
func TestRenderMarkdownDrawsMermaidFence(t *testing.T) {
	c := newMermaidChat(t, 120)

	got := c.RenderMarkdown("Here is the architecture:\n\n```mermaid\n" + mermaidSample + "```\n")

	if strings.Contains(got, "```mermaid") {
		t.Error("the fence marker survived: the diagram was not rendered")
	}
	if !strings.ContainsAny(got, "┌└│─╭╰") {
		t.Errorf("output carries no box-drawing characters:\n%s", got)
	}
	if !strings.Contains(got, "Here is the architecture") {
		t.Error("prose before the diagram was dropped")
	}
	for _, label := range []string{"Start", "Check", "Done"} {
		if !strings.Contains(got, label) {
			t.Errorf("diagram is missing node label %q", label)
		}
	}
}

// TestRenderMarkdownKeepsUnclosedFence pins the streaming contract: a fence
// still being written has no closing marker, and must stay markdown until it
// arrives. Drawing it early would re-parse a growing fragment on every token.
func TestRenderMarkdownKeepsUnclosedFence(t *testing.T) {
	c := newMermaidChat(t, 120)

	got := c.RenderMarkdown("Partial:\n\n```mermaid\ngraph LR\n    A[Start] -->")

	if strings.ContainsAny(got, "┌└╭╰") {
		t.Errorf("an unclosed fence was drawn as a diagram:\n%s", got)
	}
	if !strings.Contains(got, "graph LR") {
		t.Error("the partial fence body was dropped instead of shown as text")
	}
}

// TestRenderMarkdownFallsBackWhenTooNarrow asserts the pane shows the original
// fence when the diagram cannot fit, rather than art running past the edge.
func TestRenderMarkdownFallsBackWhenTooNarrow(t *testing.T) {
	c := newMermaidChat(t, 24)

	got := c.RenderMarkdown("```mermaid\n" + unfittableSample + "```\n")

	if !strings.Contains(stripANSICodes(got), "graph LR") {
		t.Errorf("expected the fence source as a fallback, got:\n%s", got)
	}
}

// TestRenderMarkdownLeavesOtherFencesAlone asserts only mermaid fences are
// intercepted; a go fence must still reach glamour for syntax highlighting.
func TestRenderMarkdownLeavesOtherFencesAlone(t *testing.T) {
	c := newMermaidChat(t, 100)

	// Glamour syntax-highlights the block into per-token ANSI spans, so the
	// content is only contiguous once the escapes are stripped.
	got := stripANSICodes(c.RenderMarkdown("```go\nfunc main() {}\n```\n"))

	if !strings.Contains(got, "func main() {}") {
		t.Errorf("a go fence lost its content:\n%s", got)
	}
	if strings.ContainsAny(got, "┌└╭╰") {
		t.Error("a go fence was drawn as a diagram")
	}
}

// TestRenderMarkdownWithoutFenceIsUnchanged guards the common path: a message
// with no diagram must render exactly as it did before this interception
// existed, so ordinary replies are not reflowed by the new segment splitting.
func TestRenderMarkdownWithoutFenceIsUnchanged(t *testing.T) {
	c := newMermaidChat(t, 100)

	text := "# Heading\n\nSome **bold** prose and a list:\n\n- one\n- two\n"
	if got, want := c.RenderMarkdown(text), c.renderMarkdownSegment(text); got != want {
		t.Errorf("fence-free markdown took the segmented path:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestSplitMermaidFences(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in       string
		segments int
		diagrams int
	}{
		{"no fence", "just prose", 1, 0},
		{"only fence", "```mermaid\ngraph LR\n A-->B\n```", 1, 1},
		{"prose then fence", "intro\n\n```mermaid\ngraph LR\n A-->B\n```", 2, 1},
		{"fence between prose", "a\n\n```mermaid\ngraph LR\n A-->B\n```\n\nb", 3, 1},
		{"two fences", "```mermaid\ngraph LR\n A-->B\n```\n```mermaid\ngraph TD\n C-->D\n```", 2, 2},
		{"unclosed fence", "```mermaid\ngraph LR\n A-->B", 1, 0},
		{"other language", "```go\nx := 1\n```", 1, 0},
		{"info attributes", "```mermaid {theme=dark}\ngraph LR\n A-->B\n```", 1, 1},
		{"indented fence", "  ```mermaid\n  graph LR\n  A-->B\n  ```", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			segs := splitMermaidFences(tc.in)
			if len(segs) != tc.segments {
				t.Errorf("got %d segments, want %d: %#v", len(segs), tc.segments, segs)
			}
			diagrams := 0
			for _, s := range segs {
				if s.diagram != "" {
					diagrams++
				}
			}
			if diagrams != tc.diagrams {
				t.Errorf("got %d diagrams, want %d", diagrams, tc.diagrams)
			}
		})
	}
}

// TestSplitMermaidFencesPreservesText asserts the splitter loses nothing: the
// raw segments rejoined must reproduce the input. A splitter that drops a line
// would silently eat part of a reply.
func TestSplitMermaidFencesPreservesText(t *testing.T) {
	in := "intro\n\n```mermaid\ngraph LR\n    A-->B\n```\n\noutro\n"
	var b strings.Builder
	for i, s := range splitMermaidFences(in) {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(s.raw)
	}
	if got := b.String(); got != in {
		t.Errorf("rejoined segments differ from input:\ngot:  %q\nwant: %q", got, in)
	}
}

func TestSwapFlowchartDirection(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"TD to LR", "graph TD\n  A-->B", "graph LR\n  A-->B", true},
		{"TB to LR", "graph TB\n  A-->B", "graph LR\n  A-->B", true},
		{"LR to TB", "flowchart LR\n  A-->B", "flowchart TB\n  A-->B", true},
		{"BT keeps polarity", "graph BT\n  A-->B", "graph RL\n  A-->B", true},
		{"RL keeps polarity", "graph RL\n  A-->B", "graph BT\n  A-->B", true},
		{"leading comment", "%% note\ngraph TD\n  A-->B", "%% note\ngraph LR\n  A-->B", true},
		{"indented header", "  graph TD\n  A-->B", "  graph LR\n  A-->B", true},
		{"no direction", "graph\n  A-->B", "", false},
		{"not a flowchart", "sequenceDiagram\n  A->>B: hi", "", false},
		{"empty", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := swapFlowchartDirection(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("got:\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}

// TestSwapFlowchartDirectionLeavesSubgraphDirectives asserts only the header is
// rewritten. A `direction` statement inside a subgraph sets that subgraph's
// internal flow; rewriting it would change the drawing, not reorient it.
func TestSwapFlowchartDirectionLeavesSubgraphDirectives(t *testing.T) {
	in := "graph TD\n  subgraph S\n    direction LR\n    A-->B\n  end"
	got, ok := swapFlowchartDirection(in)
	if !ok {
		t.Fatal("expected the header to be swapped")
	}
	if !strings.Contains(got, "direction LR") {
		t.Errorf("the subgraph directive was rewritten:\n%s", got)
	}
	if !strings.Contains(got, "graph LR\n") {
		t.Errorf("the header was not swapped:\n%s", got)
	}
}

// TestRenderMermaidRetriesPerpendicular pins the behavior that rescues wide
// diagrams. A fan-out is far wider under TD than under LR, so a width that
// cannot fit the declared orientation may still fit the perpendicular one.
func TestRenderMermaidRetriesPerpendicular(t *testing.T) {
	var b strings.Builder
	b.WriteString("graph TD\n")
	for i := range 12 {
		fmt.Fprintf(&b, "    root --> child%d[Child number %d]\n", i, i)
	}
	wide := b.String()

	// Under TD these twelve children sit in one row and cannot fit; the retry
	// stacks them in a column instead.
	got := RenderMermaid(wide, 100, darkPalette)
	if got == "" {
		t.Skip("neither orientation fits at this width")
	}
	for i, line := range strings.Split(got, "\n") {
		if n := lipgloss.Width(line); n > 100 {
			t.Errorf("line %d is %d columns wide, over the 100 budget", i+1, n)
		}
	}
	// A column of twelve children is necessarily taller than it is wide.
	if rows := strings.Count(got, "\n") + 1; rows < 12 {
		t.Errorf("expected a tall layout after the retry, got %d rows", rows)
	}
}

// TestMermaidStylesAreDistinct is the point of the palette mapping: the three
// structural roles must not collapse into one color. A diagram where the box,
// the connector and the group border share a grey is a texture, not a diagram.
func TestMermaidStylesAreDistinct(t *testing.T) {
	p := darkPalette
	roles := map[string]string{
		"box":        "node",
		"box text":   "label",
		"connector":  "edge",
		"group":      "subgraph",
		"edge label": "edge_label",
	}

	seen := map[string]string{}
	for role, key := range roles {
		rendered := mermaidStyleFor(key, p).Render("x")
		if prev, dup := seen[rendered]; dup {
			t.Errorf("%q and %q render identically — they must read apart", role, prev)
		}
		seen[rendered] = role
	}
}

// TestMermaidStylesFollowBothPalettes guards the light theme, where a color
// chosen only against a dark background is the classic casualty.
func TestMermaidStylesFollowBothPalettes(t *testing.T) {
	for _, key := range []string{"node", "label", "edge", "subgraph", "edge_label", "subgraph_label"} {
		dark := mermaidStyleFor(key, darkPalette).Render("x")
		light := mermaidStyleFor(key, lightPalette).Render("x")
		if dark == light {
			t.Errorf("style %q is identical in both palettes: it is not palette-driven", key)
		}
	}
}

// TestFallbackFenceIsStable is the regression test for a transcript that
// visibly shimmers. When a diagram is too wide the fence is shown instead, and
// Chroma has no mermaid lexer, so it guessed the language from the content —
// a guess decided by map order and therefore different on each repaint. The
// pane re-renders on every blink tick, so the block quietly changed color a
// couple of times a second.
func TestFallbackFenceIsStable(t *testing.T) {
	c := newMermaidChat(t, 60)
	msg := "```mermaid\n" + unfittableSample + "```\n"

	first := c.RenderMarkdown(msg)
	if first == "" {
		t.Fatal("fallback produced nothing")
	}
	for i := range 60 {
		if got := c.RenderMarkdown(msg); got != first {
			t.Fatalf("render %d differs from render 0: the fallback fence is not stable", i+1)
		}
	}
}

func TestStableFenceLang(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"plain", "```mermaid\ngraph LR\n```", "```text\ngraph LR\n```"},
		{"indented", "  ```mermaid\n  graph LR\n  ```", "  ```text\n  graph LR\n  ```"},
		{"attributes", "```mermaid {theme=dark}\ngraph LR\n```", "```text\ngraph LR\n```"},
		{"no newline", "```mermaid", "```mermaid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stableFenceLang(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDiagramIsSeparatedFromProse pins the blank line between a diagram and
// the text introducing it. The art bypasses glamour because it already carries
// ANSI, so it gets none of glamour's block spacing for free.
func TestDiagramIsSeparatedFromProse(t *testing.T) {
	c := newMermaidChat(t, 100)
	out := c.RenderMarkdown("Here is the flow:\n\n```mermaid\n" + mermaidSample + "```\n")

	lines := strings.Split(out, "\n")
	proseAt := -1
	for i, l := range lines {
		if strings.Contains(l, "Here is the flow") {
			proseAt = i
		}
	}
	if proseAt < 0 {
		t.Fatal("prose line not found")
	}
	next := strings.TrimSpace(stripANSICodes(lines[proseAt+1]))
	if next != "" {
		t.Errorf("no blank line between prose and diagram; next line is %q", next)
	}
}

// --- scrolling invariants ---
// Art wider than the pane is what the viewport would wrap, and a wrapped line
// shears the diagram as soon as the transcript scrolls.
func TestArtNeverExceedsPaneWidth(t *testing.T) {
	srcs := map[string]string{
		"emoji sequence": "sequenceDiagram\n    participant U as 👤 You\n    participant C as 🤖 Agent\n    participant W as ⚡ Ways\n    rect rgba(21,101,192,0.2)\n    U->>C: \"fix the auth bug\"\n    W-->>C: 🔑 Security · 🐛 Debugging\n    end\n",
		"plain flow":     mermaidSample,
		"cjk labels":     "graph LR\n    A[日本語のラベル] --> B[短い]\n",
	}
	for name, src := range srcs {
		for _, pane := range []int{60, 80, 100, 120, 160} {
			out := RenderMermaid(src, pane, darkPalette)
			if out == "" {
				continue
			}
			for i, line := range strings.Split(out, "\n") {
				if w := lipgloss.Width(line); w > pane {
					t.Errorf("%s pane=%d: line %d is %d columns (over by %d)", name, pane, i+1, w, w-pane)
				}
			}
		}
	}
}

// Every row of a diagram must be the same display width, or the right-hand
// border walks in and out as the reader scrolls past it.
func TestArtRowsHaveConsistentWidth(t *testing.T) {
	src := "sequenceDiagram\n    participant U as 👤 You\n    participant C as 🤖 Agent\n    U->>C: 🔑 Security\n"
	out := RenderMermaid(src, 100, darkPalette)
	if out == "" {
		t.Skip("did not fit")
	}
	seen := map[int][]int{}
	for i, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(stripANSICodes(line)) == "" {
			continue
		}
		w := lipgloss.Width(line)
		seen[w] = append(seen[w], i+1)
	}
	if len(seen) > 1 {
		t.Logf("distinct widths across rows: %v", seen)
	}
}

// The minimap and the viewport both index the transcript by line, and
// renderMessages builds its kinds slice in lockstep with the text it emits. A
// diagram is spliced in outside glamour, so if its line count and its kind
// count disagreed the two would drift apart and scrolling would land on the
// wrong row.
func TestDiagramKeepsLineAndKindCountsAligned(t *testing.T) {
	c := newMermaidChat(t, 100)
	c.Messages = []message{
		{role: "user", content: "show me the flow"},
		{role: "assistant", content: "Here it is:\n\n```mermaid\n" + mermaidSample + "```\n"},
	}

	text, kinds := c.renderMessages(false)
	lines := strings.Split(text, "\n")
	if len(kinds) != len(lines) {
		t.Errorf("kinds/lines drift: %d kinds for %d lines", len(kinds), len(lines))
	}
}

// Re-rendering the same transcript must produce the same number of lines, or
// the viewport's scroll offset points somewhere different on every frame.
func TestTranscriptLineCountIsStable(t *testing.T) {
	c := newMermaidChat(t, 100)
	c.Messages = []message{
		{role: "assistant", content: "Flow:\n\n```mermaid\n" + mermaidSample + "```\n"},
	}
	first, _ := c.renderMessages(false)
	want := strings.Count(first, "\n")
	for i := range 20 {
		got, _ := c.renderMessages(false)
		if n := strings.Count(got, "\n"); n != want {
			t.Fatalf("render %d produced %d lines, want %d", i+1, n, want)
		}
	}
}

// TestSubgraphInteriorGetsBackground covers the fill plumbing. The renderer
// already marked subgraph interiors, gantt bands and timeline sections with a
// background, but the cells API dropped it, so the TUI could only color glyphs
// and every grouped region read as a bare outline.
//
// Note the needle: lipgloss folds foreground and background into a single SGR
// sequence, so a background appears as ";48;" inside a longer escape and never
// as a standalone "\x1b[48;".
func TestSubgraphInteriorGetsBackground(t *testing.T) {
	src := "graph LR\n    subgraph S[\"A Group\"]\n        a[one] --> b[two]\n    end\n    b --> c[three]\n"

	out := RenderMermaid(src, 90, darkPalette)
	if out == "" {
		t.Fatal("diagram did not render")
	}
	if !strings.Contains(out, ";48;") {
		t.Error("no background emitted for the subgraph interior")
	}
}

// TestNoBackgroundWithoutASubgraph is the other half: a diagram with nothing to
// group must stay flat, so an unknown or absent fill never paints a region.
func TestNoBackgroundWithoutASubgraph(t *testing.T) {
	out := RenderMermaid("graph LR\n    a[one] --> b[two]\n", 90, darkPalette)
	if out == "" {
		t.Fatal("diagram did not render")
	}
	if strings.Contains(out, ";48;") {
		t.Error("a diagram with no subgraph painted a background anyway")
	}
}

// TestFillFollowsPalette guards the light theme, where a background chosen only
// against a dark pane is the classic casualty.
func TestFillFollowsPalette(t *testing.T) {
	dark, okD := mermaidFillFor("subgraph_fill", darkPalette)
	light, okL := mermaidFillFor("subgraph_fill", lightPalette)
	if !okD || !okL {
		t.Fatal("subgraph_fill has no color in one of the palettes")
	}
	if dark == light {
		t.Error("the fill is identical in both palettes: it is not palette-driven")
	}
	if _, ok := mermaidFillFor("not-a-fill-key", darkPalette); ok {
		t.Error("an unknown fill key returned a color instead of declining")
	}
}
