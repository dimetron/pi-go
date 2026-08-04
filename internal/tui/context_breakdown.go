package tui

import (
	"fmt"
	"image/color"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// A single "42% full" number hides the thing a user actually needs to act on:
// *what* is filling the window. A 20k rules file and a 20k conversation are the
// same number and completely different problems — one is fixed overhead you can
// trim once, the other is the session doing its job.
//
// So the gauge is segmented by origin. Everything except Conversation is fixed
// per-session overhead, which is also exactly the prefix a provider caches; the
// colored run to its right is the part that actually grows.

// ContextSegmentKind identifies a category of context consumption. The order of
// these constants is the order segments are drawn, fixed overhead first, so the
// bar reads left-to-right from "cost you pay every turn" to "the session".
type ContextSegmentKind int

const (
	SegSystemPrompt ContextSegmentKind = iota
	SegToolDefs
	SegRules
	SegSkills
	SegMCPTools
	SegSubagents
	SegConversation
	segCount
)

// Label is the human-readable name shown in the legend.
func (k ContextSegmentKind) Label() string {
	switch k {
	case SegSystemPrompt:
		return "System prompt"
	case SegToolDefs:
		return "Tool definitions"
	case SegRules:
		return "Rules"
	case SegSkills:
		return "Skills"
	case SegMCPTools:
		return "MCP & dynamic tools"
	case SegSubagents:
		return "Subagent definitions"
	case SegConversation:
		return "Conversation"
	default:
		return "Unknown"
	}
}

// Color returns the segment's swatch. The palette is chosen so adjacent
// segments never share a hue and the whole set stays legible on the Mocha
// background; Conversation is warmest because it is the one that grows.
func (k ContextSegmentKind) Color() color.Color {
	switch k {
	case SegSystemPrompt:
		return lipgloss.Color("#9399b2") // overlay2 — inert overhead
	case SegToolDefs:
		return lipgloss.Color("#89b4fa") // blue
	case SegRules:
		return lipgloss.Color("#a6e3a1") // green
	case SegSkills:
		return lipgloss.Color("#f9e2af") // yellow
	case SegMCPTools:
		return lipgloss.Color("#cba6f7") // mauve
	case SegSubagents:
		return lipgloss.Color("#89dceb") // sky
	case SegConversation:
		return lipgloss.Color("#fab387") // peach — the part that grows
	default:
		return lipgloss.Color("#585b70")
	}
}

// ContextBreakdown attributes context usage to its origins. The fixed sections
// are measured once at startup; Conversation is derived per-render from the
// provider's reported prompt size.
type ContextBreakdown struct {
	// Fixed per-session overhead, in tokens.
	SystemPrompt int64
	ToolDefs     int64
	Rules        int64
	Skills       int64
	MCPTools     int64
	Subagents    int64

	// Conversation is everything else in the window: messages, tool calls and
	// tool results. Derived, not measured directly.
	Conversation int64
}

// FixedTotal is the overhead present before a single message is exchanged.
func (b ContextBreakdown) FixedTotal() int64 {
	return b.SystemPrompt + b.ToolDefs + b.Rules + b.Skills + b.MCPTools + b.Subagents
}

// Total is the whole window in use.
func (b ContextBreakdown) Total() int64 {
	return b.FixedTotal() + b.Conversation
}

// Tokens returns one segment's size.
func (b ContextBreakdown) Tokens(k ContextSegmentKind) int64 {
	switch k {
	case SegSystemPrompt:
		return b.SystemPrompt
	case SegToolDefs:
		return b.ToolDefs
	case SegRules:
		return b.Rules
	case SegSkills:
		return b.Skills
	case SegMCPTools:
		return b.MCPTools
	case SegSubagents:
		return b.Subagents
	case SegConversation:
		return b.Conversation
	default:
		return 0
	}
}

// withConversationFrom derives the conversation share from a reported prompt
// size. The provider's number is authoritative for the total, so conversation
// is whatever it exceeds the measured overhead by — that way a mis-measured
// section shows up as a wrong section, never as a wrong total.
//
// A reported total below the fixed overhead means the model is not yet seeing
// everything we measured (a skill not injected this turn, say), so conversation
// clamps at zero rather than going negative.
func (b ContextBreakdown) withConversationFrom(promptTokens int64) ContextBreakdown {
	b.Conversation = max(0, promptTokens-b.FixedTotal())
	return b
}

// segmentWidths distributes display cells across segments proportionally, so
// that the parts sum to exactly `cells`. Exactness is not cosmetic here: this
// run is part of a frame row, and a one-cell drift shifts every column to its
// right.
//
// Two properties are in tension, and the resolution is deliberate:
//
//   - Every section with a non-zero share gets at least one cell, so a small
//     but real section (527 tokens against 85k) is visible rather than rounded
//     away — the legend must not list a section the bar does not show.
//   - When there are more non-zero sections than cells, that guarantee is
//     impossible. The largest sections win the available cells and the rest are
//     dropped, because at that width the bar can only convey the broad shape.
//
// Allocation is: one cell to each included section, then the remainder handed
// out by largest fractional share. Both steps are integer-exact by
// construction, so the sum cannot drift.
func segmentWidths(b ContextBreakdown, cells int) [segCount]int {
	var out [segCount]int
	if cells <= 0 || b.Total() <= 0 {
		return out
	}

	type seg struct {
		kind ContextSegmentKind
		tok  int64
	}
	var segs []seg
	for k := ContextSegmentKind(0); k < segCount; k++ {
		if tok := b.Tokens(k); tok > 0 {
			segs = append(segs, seg{kind: k, tok: tok})
		}
	}
	if len(segs) == 0 {
		return out
	}

	// More sections than cells: keep the largest, drop the rest.
	if len(segs) > cells {
		sort.SliceStable(segs, func(i, j int) bool { return segs[i].tok > segs[j].tok })
		segs = segs[:cells]
	}

	var kept int64
	for _, s := range segs {
		kept += s.tok
	}

	// One cell each, then share out what is left over. `extra` is fixed for the
	// whole pass: deriving each segment's ideal from a running remainder would
	// shrink the basis as the loop advanced and under-allocate the tail.
	extra := cells - len(segs)
	allocated := 0
	fracs := make([]float64, len(segs))
	for i, s := range segs {
		out[s.kind] = 1
		ideal := float64(s.tok) / float64(kept) * float64(extra)
		n := int(ideal)
		out[s.kind] += n
		allocated += n
		fracs[i] = ideal - float64(n)
	}
	remaining := extra - allocated
	// Largest fractional part first, one cell each, until the budget is spent.
	for remaining > 0 {
		best, bestFrac := -1, -1.0
		for i, f := range fracs {
			if f > bestFrac {
				best, bestFrac = i, f
			}
		}
		if best < 0 {
			break
		}
		out[segs[best].kind]++
		fracs[best] = -1
		remaining--
	}
	return out
}

// renderSegmentedGauge draws the proportional bar across exactly `cells`
// columns, using the rule glyph so the row still reads as a rule.
func renderSegmentedGauge(b ContextBreakdown, cells int) string {
	if cells <= 0 {
		return ""
	}
	widths := segmentWidths(b, cells)

	var sb strings.Builder
	drawn := 0
	for k := ContextSegmentKind(0); k < segCount; k++ {
		n := widths[k]
		if n <= 0 {
			continue
		}
		style := lipgloss.NewStyle().Foreground(k.Color())
		sb.WriteString(style.Render(strings.Repeat(string(gaugeEmptyGlyph), n)))
		drawn += n
	}
	// Unused remainder of the window, in the dim rule color.
	if drawn < cells {
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#585b70"))
		sb.WriteString(dim.Render(strings.Repeat(string(gaugeEmptyGlyph), cells-drawn)))
	}
	return sb.String()
}

// RenderContextBreakdown renders the legend panel: a header with the headline
// percentage, the segmented bar, then one row per section.
func RenderContextBreakdown(b ContextBreakdown, window int64, width int) string {
	if width < 24 {
		width = 24
	}
	if window <= 0 {
		window = autoRangeWindow(b.Total())
	}

	total := b.Total()
	pct := 0.0
	if window > 0 {
		pct = float64(total) / float64(window) * 100
	}
	if pct > 100 {
		pct = 100
	}

	var sb strings.Builder

	head := fmt.Sprintf("%d%% Full", int(pct))
	tail := fmt.Sprintf("~%s / %s Tokens", formatTokenCount(total), formatTokenCount(window))
	gap := width - ansi.StringWidth(head) - ansi.StringWidth(tail)
	if gap < 1 {
		gap = 1
	}
	headStyle := lipgloss.NewStyle().Foreground(contextSeverityColor(pct)).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9399b2"))
	sb.WriteString(headStyle.Render(head) + strings.Repeat(" ", gap) + dimStyle.Render(tail))
	sb.WriteString("\n\n")

	// The bar spans the window, not just what is used, so the empty tail is
	// visible and the segments stay in proportion to the budget.
	barCells := width
	if window > 0 && total < window {
		barCells = int(float64(total) / float64(window) * float64(width))
	}
	sb.WriteString(renderSegmentedGauge(b, barCells))
	if barCells < width {
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#585b70"))
		sb.WriteString(dim.Render(strings.Repeat(string(gaugeEmptyGlyph), width-barCells)))
	}
	sb.WriteString("\n\n")

	for k := ContextSegmentKind(0); k < segCount; k++ {
		tok := b.Tokens(k)
		if tok <= 0 {
			continue
		}
		swatch := lipgloss.NewStyle().Foreground(k.Color()).Render("███")
		label := k.Label()
		count := formatTokenCount(tok)
		pad := width - ansi.StringWidth(swatch) - 1 - ansi.StringWidth(label) - ansi.StringWidth(count)
		if pad < 1 {
			pad = 1
		}
		sb.WriteString(swatch + " " +
			lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4")).Render(label) +
			strings.Repeat(" ", pad) +
			dimStyle.Render(count))
		sb.WriteString("\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}
