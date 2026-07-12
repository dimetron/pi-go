package tui

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// StatusModel manages the status bar display at the bottom of the TUI.
type StatusModel struct {
	// GitBranch is the current git branch (detected at startup).
	GitBranch string
	// ActiveTool is the name of the currently executing tool (single).
	ActiveTool string
	// ActiveTools tracks parallel tool execution: name → start time.
	ActiveTools map[string]time.Time
	// ToolStart is when the current single tool started.
	ToolStart time.Time
	// Width is the terminal width for rendering.
	Width int
}

// StatusRenderInput provides data from other models needed by the status bar.
type StatusRenderInput struct {
	ProviderName string
	ModelName    string
	Running      bool
	Mode         string       // "chat" or "plan"
	Eyes         string       // mood eyes e.g. "◕ ◕"
	Messages     []message    // for context estimate
	TokenTracker TokenTracker // may be nil
	DiffAdded    int
	DiffRemoved  int
	RunCycle     *runCycleInfo   // may be nil
	FolderName   string          // current working directory basename
	HostName     string          // local hostname
	LoadingItems map[string]bool // item -> done; nil means not loading
	Flash        string          // transient notice ("Copied!"); empty when none
}

// runCycleInfo carries /run state for the status bar.
type runCycleInfo struct {
	SpecName   string
	Cycle      int
	MaxRetries int
}

// contextBarWidth is the number of characters used for the visual context bar.
const contextBarWidth = 10

// renderContextBar returns a color-coded visual bar like "████░░░░░░ 42%".
// Colors: green < 60%, orange 60-80%, red > 80%.
func renderContextBar(pct float64, bg color.Color) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}

	filled := int(pct / 100 * contextBarWidth)
	if filled > contextBarWidth {
		filled = contextBarWidth
	}
	empty := contextBarWidth - filled

	var fg color.Color
	switch {
	case pct >= 80:
		fg = lipgloss.Color("#f38ba8") // Mocha red
	case pct >= 60:
		fg = lipgloss.Color("#fab387") // Mocha peach
	default:
		fg = lipgloss.Color("#a6e3a1") // Mocha green
	}

	filledStyle := lipgloss.NewStyle().Background(bg).Foreground(fg)
	emptyStyle := lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("#585b70")) // Mocha surface2
	pctStyle := lipgloss.NewStyle().Background(bg).Foreground(fg)

	return filledStyle.Render(strings.Repeat("█", filled)) +
		emptyStyle.Render(strings.Repeat("░", empty)) +
		pctStyle.Render(fmt.Sprintf(" %.0f%%", pct))
}

// Render renders the status bar string.
func (s *StatusModel) Render(in StatusRenderInput) string {
	dimFg := lipgloss.Color("#bac2de") // Mocha subtext1

	dim := lipgloss.NewStyle().Foreground(dimFg)
	bar := lipgloss.NewStyle().Width(s.Width)

	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#585b70")) // Mocha surface2
	sep := sepStyle.Render("  │  ")

	var parts []string

	// The bracketed field: [chat], [plan], the spinner verb while running — and a
	// flash when there is one.
	//
	// A flash takes this slot over rather than adding a segment of its own. It is
	// already the fixed-width, bracketed field the eye returns to, so a notice
	// lands where the user is looking and the bar's geometry does not shift. The
	// mode is not lost by borrowing it for three seconds: it has not changed, and
	// it comes straight back.
	mode := in.Mode
	if mode == "" {
		mode = "chat"
	}
	switch {
	case in.Flash != "":
		flashStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1")).Bold(true) // Mocha green
		parts = append(parts, flashStyle.Render(fmt.Sprintf(" [%s]", paddedStatusMode(in.Flash))))
	case mode == "plan":
		modeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387")) // Mocha peach
		parts = append(parts, modeStyle.Render(fmt.Sprintf(" [%s]", paddedStatusMode(mode))))
	case in.Running && s.ActiveTool == "":
		verbStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#89b4fa")) // Mocha blue
		parts = append(parts, verbStyle.Render(fmt.Sprintf(" [%s]", spinnerVerb())))
	default:
		verbStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#89b4fa")) // Mocha blue
		parts = append(parts, verbStyle.Render(fmt.Sprintf(" [%s]", paddedStatusMode(mode))))
	}

	// Loading progress (replaces normal status content during init).
	if in.LoadingItems != nil {
		var items []string
		for _, name := range sortedKeys(in.LoadingItems) {
			if in.LoadingItems[name] {
				okStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1")) // Mocha green
				items = append(items, okStyle.Render(name+" \u2713"))
			} else {
				loadStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387")) // Mocha peach
				items = append(items, loadStyle.Render(name+"..."))
			}
		}
		parts = append(parts, dim.Render("load: ")+strings.Join(items, dim.Render(" ")))
		return bar.Render(strings.Join(parts, sep))
	}

	// Context % bar (visual bar with color coding).
	noBg := lipgloss.Color("#00000000") // transparent for context bar
	if tt := in.TokenTracker; tt != nil && tt.Limit() > 0 {
		pct := tt.PercentUsed()
		parts = append(parts, renderContextBar(pct, noBg))
	} else {
		// Fallback: rough context size estimate (~4 chars per token).
		ctxChars := 0
		for _, msg := range in.Messages {
			ctxChars += len(msg.content) + len(msg.tool) + len(msg.toolIn)
		}
		ctxTokens := ctxChars / 4
		switch {
		case ctxTokens >= 1000:
			parts = append(parts, dim.Render(fmt.Sprintf("ctx: %.1fk", float64(ctxTokens)/1000)))
		default:
			parts = append(parts, dim.Render(fmt.Sprintf("ctx: %d", ctxTokens)))
		}
	}

	// Token usage (numeric).
	if tt := in.TokenTracker; tt != nil {
		total := tt.TotalUsed()
		limit := tt.Limit()
		if limit > 0 {
			pct := tt.PercentUsed()
			var tokenStyle lipgloss.Style
			switch {
			case pct >= 100:
				tokenStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#f38ba8")) // Mocha red
			case pct >= 80:
				tokenStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387")) // Mocha peach
			default:
				tokenStyle = dim
			}
			parts = append(parts, tokenStyle.Render(fmt.Sprintf("tkn: %s/%s",
				formatTokenCount(total), formatTokenCount(limit))))
		} else if total > 0 {
			parts = append(parts, dim.Render(fmt.Sprintf("tkn: %s", formatTokenCount(total))))
		}
	}

	// Directory | host.
	if in.FolderName != "" || in.HostName != "" {
		dirStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#cba6f7"))  // Mocha mauve
		hostStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#89dceb")) // Mocha sky

		var locationParts []string
		if in.FolderName != "" {
			locationParts = append(locationParts, dirStyle.Render(in.FolderName))
		}
		if in.HostName != "" {
			locationParts = append(locationParts, hostStyle.Render(in.HostName))
		}
		parts = append(parts, strings.Join(locationParts, dim.Render(" | ")))
	}

	// Active tools or thinking status.
	toolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#74c7ec")) // Mocha sapphire
	if len(s.ActiveTools) > 1 {
		var toolNames []string
		for name := range s.ActiveTools {
			toolNames = append(toolNames, name)
		}
		sort.Strings(toolNames)
		parts = append(parts, toolStyle.Render(fmt.Sprintf("tools[%d]: %s", len(toolNames), strings.Join(toolNames, ", "))))
	} else if s.ActiveTool != "" {
		elapsed := time.Since(s.ToolStart).Truncate(time.Millisecond)
		parts = append(parts, toolStyle.Render(fmt.Sprintf("tool: %s (%s)", s.ActiveTool, elapsed)))
	}

	// /run cycle indicator. The spec name is omitted: it is long enough to wrap
	// the status bar onto a second line, and it is already shown in the sidebar.
	if in.RunCycle != nil {
		runStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387")) // Mocha peach
		parts = append(parts, runStyle.Render(fmt.Sprintf("cycle %d/%d",
			in.RunCycle.Cycle, in.RunCycle.MaxRetries)))
	}

	return bar.Render(strings.Join(parts, sep))
}

// sortedKeys returns map keys in sorted order.
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
