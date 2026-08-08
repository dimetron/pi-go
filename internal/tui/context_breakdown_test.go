package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// realisticBreakdown mirrors the shape of the reference screenshot, so the
// tests exercise proportions a real session actually produces rather than
// round numbers that hide rounding bugs.
func realisticBreakdown() ContextBreakdown {
	return ContextBreakdown{
		SystemPrompt: 527,
		ToolDefs:     8_700,
		Rules:        20_100,
		Skills:       4_300,
		MCPTools:     14_700,
		Subagents:    901,
		Conversation: 35_800,
	}
}

// I1: the segmented run must sum to exactly the cells it was given, at every
// width. Largest-remainder allocation is precisely where an off-by-one hides.
func TestSegmentWidths_SumExactly(t *testing.T) {
	b := realisticBreakdown()
	for cells := 1; cells <= 300; cells++ {
		w := segmentWidths(b, cells)
		sum := 0
		for _, n := range w {
			sum += n
		}
		if sum != cells {
			t.Fatalf("cells=%d: segments sum to %d", cells, sum)
		}
	}
}

func TestRenderSegmentedGauge_ExactWidth(t *testing.T) {
	b := realisticBreakdown()
	for _, cells := range []int{1, 3, 7, 20, 40, 76, 120, 200} {
		if got := ansi.StringWidth(renderSegmentedGauge(b, cells, darkPalette)); got != cells {
			t.Errorf("cells=%d: rendered %d", cells, got)
		}
	}
}

// A section that is real but tiny (527 tokens against ~85k) must still be
// visible rather than rounded away — otherwise the legend lists a section the
// bar does not show.
func TestSegmentWidths_SmallSectionSurvives(t *testing.T) {
	b := realisticBreakdown()
	w := segmentWidths(b, 120)
	if w[SegSystemPrompt] < 1 {
		t.Error("a small but non-zero section must get at least one cell")
	}
	if w[SegSubagents] < 1 {
		t.Error("subagent definitions rounded away")
	}
}

func TestSegmentWidths_ZeroSectionsGetNothing(t *testing.T) {
	b := ContextBreakdown{SystemPrompt: 1000, Conversation: 1000}
	w := segmentWidths(b, 50)
	for _, k := range []ContextSegmentKind{SegToolDefs, SegRules, SegSkills, SegMCPTools, SegSubagents} {
		if w[k] != 0 {
			t.Errorf("%s has no tokens but got %d cells", k.Label(), w[k])
		}
	}
}

func TestSegmentWidths_Proportional(t *testing.T) {
	// Conversation is ~42% of this breakdown; at 100 cells it should dominate
	// and be the largest single segment.
	w := segmentWidths(realisticBreakdown(), 100)
	for k := ContextSegmentKind(0); k < segCount; k++ {
		if k == SegConversation {
			continue
		}
		if w[k] > w[SegConversation] {
			t.Errorf("%s (%d cells) exceeds conversation (%d)", k.Label(), w[k], w[SegConversation])
		}
	}
}

func TestSegmentWidths_DegenerateInputs(t *testing.T) {
	if got := segmentWidths(ContextBreakdown{}, 50); got != [segCount]int{} {
		t.Error("an empty breakdown must allocate nothing")
	}
	if got := segmentWidths(realisticBreakdown(), 0); got != [segCount]int{} {
		t.Error("zero cells must allocate nothing")
	}
	if got := segmentWidths(realisticBreakdown(), -5); got != [segCount]int{} {
		t.Error("negative cells must allocate nothing")
	}
}

func TestContextBreakdown_Totals(t *testing.T) {
	b := realisticBreakdown()
	wantFixed := int64(527 + 8_700 + 20_100 + 4_300 + 14_700 + 901)
	if b.FixedTotal() != wantFixed {
		t.Errorf("FixedTotal = %d, want %d", b.FixedTotal(), wantFixed)
	}
	if b.Total() != wantFixed+35_800 {
		t.Errorf("Total = %d, want %d", b.Total(), wantFixed+35_800)
	}
}

// The provider's reported prompt size is authoritative for the total, so
// conversation is the remainder. A mis-measured section must show up as a wrong
// section, never as a wrong total.
func TestContextBreakdown_ConversationIsDerived(t *testing.T) {
	b := realisticBreakdown()
	b.Conversation = 0
	got := b.withConversationFrom(100_000)
	if got.Total() != 100_000 {
		t.Errorf("Total = %d, want the reported 100000", got.Total())
	}
	if got.Conversation != 100_000-b.FixedTotal() {
		t.Errorf("Conversation = %d, want %d", got.Conversation, 100_000-b.FixedTotal())
	}
}

// A reported total below the measured overhead means the model is not seeing
// everything we measured; conversation clamps at zero rather than going
// negative and inverting the bar.
func TestContextBreakdown_ConversationClampsAtZero(t *testing.T) {
	got := realisticBreakdown().withConversationFrom(100)
	if got.Conversation != 0 {
		t.Errorf("Conversation = %d, want 0", got.Conversation)
	}
}

func TestContextSegmentKind_LabelsAndColorsAreDistinct(t *testing.T) {
	seenLabel := map[string]bool{}
	seenColor := map[string]bool{}
	for k := ContextSegmentKind(0); k < segCount; k++ {
		l := k.Label()
		if l == "" || l == "Unknown" {
			t.Errorf("kind %d has no label", k)
		}
		if seenLabel[l] {
			t.Errorf("duplicate label %q", l)
		}
		seenLabel[l] = true

		c := truecolorFragment(k.Color(darkPalette))
		if seenColor[c] {
			t.Errorf("%s reuses another segment's color", l)
		}
		seenColor[c] = true
	}
}

// The rule must stay exactly the terminal width whether or not a breakdown is
// supplied — the segmented path and the flat path both fill the same run.
func TestContextRule_SegmentedKeepsExactWidth(t *testing.T) {
	b := realisticBreakdown()
	for _, width := range []int{20, 40, 80, 120, 200} {
		for _, used := range []int64{1_000, 85_000, 200_000, 400_000, 1_000_000} {
			out := renderContextRule(contextRuleInput{
				Width: width, UsedTokens: used, Breakdown: &b,
			}, darkPalette)
			if got := ansi.StringWidth(out); got != width {
				t.Errorf("width=%d used=%d: rule is %d cells", width, used, got)
			}
		}
	}
}

func TestContextRule_SegmentedUsesSegmentColors(t *testing.T) {
	b := realisticBreakdown()
	out := renderContextRule(contextRuleInput{Width: 160, UsedTokens: 85_000, Breakdown: &b}, darkPalette)
	for _, k := range []ContextSegmentKind{SegToolDefs, SegRules, SegMCPTools, SegConversation} {
		if !strings.Contains(out, truecolorFragment(k.Color(darkPalette))) {
			t.Errorf("segmented rule missing %s color", k.Label())
		}
	}
}

func TestRenderContextBreakdown_ListsEveryNonZeroSection(t *testing.T) {
	b := realisticBreakdown()
	out := ansi.Strip(RenderContextBreakdown(b, 200_000, 60, darkPalette))
	for k := ContextSegmentKind(0); k < segCount; k++ {
		if b.Tokens(k) <= 0 {
			continue
		}
		if !strings.Contains(out, k.Label()) {
			t.Errorf("panel omits %q", k.Label())
		}
	}
	if !strings.Contains(out, "42% Full") {
		t.Errorf("panel headline wrong; got:\n%s", out)
	}
}

func TestRenderContextBreakdown_OmitsZeroSections(t *testing.T) {
	b := ContextBreakdown{SystemPrompt: 500, Conversation: 5_000}
	out := ansi.Strip(RenderContextBreakdown(b, 200_000, 60, darkPalette))
	if strings.Contains(out, "MCP & dynamic tools") {
		t.Error("a zero section must not be listed")
	}
	if !strings.Contains(out, "System prompt") {
		t.Error("a non-zero section must be listed")
	}
}

func TestRenderContextBreakdown_NarrowWidthDoesNotPanic(t *testing.T) {
	for _, w := range []int{0, 1, 10, 24, 40} {
		out := RenderContextBreakdown(realisticBreakdown(), 200_000, w, darkPalette)
		if out == "" {
			t.Errorf("width=%d produced nothing", w)
		}
	}
}
