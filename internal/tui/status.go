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
	LoadingItems map[string]bool // item -> done; nil means not loading
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
	fg := lipgloss.Color("#cdd6f4")    // Mocha text
	dimFg := lipgloss.Color("#bac2de") // Mocha subtext1

	bright := lipgloss.NewStyle().Foreground(fg)
	dim := lipgloss.NewStyle().Foreground(dimFg)
	bar := lipgloss.NewStyle().Width(s.Width)

	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#585b70")) // Mocha surface2
	sep := sepStyle.Render("  │  ")

	var parts []string

	// Mode indicator: [chat] or [plan], with spinner verb when running.
	mode := in.Mode
	if mode == "" {
		mode = "chat"
	}
	if mode == "plan" {
		modeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387")) // Mocha peach
		parts = append(parts, modeStyle.Render(fmt.Sprintf(" [%s]", mode)))
	} else if in.Running && s.ActiveTool == "" {
		verbStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#89b4fa")) // Mocha blue
		parts = append(parts, verbStyle.Render(fmt.Sprintf(" [%s]", spinnerVerb())))
	} else {
		parts = append(parts, dim.Render(fmt.Sprintf(" [%s]", mode)))
	}

	// Provider | Model.
	modelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f9e2af")) // Mocha yellow
	if in.ProviderName != "" {
		parts = append(parts, bright.Render(in.ProviderName+" │ ")+modelStyle.Render(in.ModelName))
	} else {
		parts = append(parts, modelStyle.Render(in.ModelName))
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
		parts = append(parts, dim.Render("loading: ")+strings.Join(items, dim.Render(" ")))
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
			parts = append(parts, tokenStyle.Render(fmt.Sprintf("tokens: %s/%s",
				formatTokenCount(total), formatTokenCount(limit))))
		} else if total > 0 {
			parts = append(parts, dim.Render(fmt.Sprintf("tokens: %s", formatTokenCount(total))))
		}
	}

	// Git branch.
	if s.GitBranch != "" {
		branchStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#94e2d5")) // Mocha teal
		parts = append(parts, branchStyle.Render(fmt.Sprintf("\u2387 %s", s.GitBranch)))
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

	// /run cycle indicator.
	if in.RunCycle != nil {
		runStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387")) // Mocha peach
		parts = append(parts, runStyle.Render(fmt.Sprintf("run[%s]: cycle %d/%d",
			in.RunCycle.SpecName, in.RunCycle.Cycle, in.RunCycle.MaxRetries)))
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
