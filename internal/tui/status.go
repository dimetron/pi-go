package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/dimetron/pi-go/internal/subagent"
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
	Messages     []message     // for context estimate
	TokenTracker TokenTracker  // may be nil
	Orchestrator *subagent.Orchestrator // may be nil
	TraceCount   int
	RunCycle     *runCycleInfo // may be nil
}

// runCycleInfo carries /run state for the status bar.
type runCycleInfo struct {
	SpecName   string
	Cycle      int
	MaxRetries int
}

// Render renders the status bar string.
func (s *StatusModel) Render(in StatusRenderInput) string {
	bg := lipgloss.Color("236")
	fg := lipgloss.Color("252")
	dimFg := lipgloss.Color("243")

	bright := lipgloss.NewStyle().Background(bg).Foreground(fg)
	dim := lipgloss.NewStyle().Background(bg).Foreground(dimFg)
	bar := lipgloss.NewStyle().Background(bg).Width(s.Width)

	sep := dim.Render("  |  ")

	var parts []string

	// Provider | Model.
	if in.ProviderName != "" {
		parts = append(parts, bright.Render(fmt.Sprintf(" %s | %s", in.ProviderName, in.ModelName)))
	} else {
		parts = append(parts, bright.Render(fmt.Sprintf(" %s", in.ModelName)))
	}

	// Context size estimate (rough: ~4 chars per token).
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

	// Token usage guardrail.
	if tt := in.TokenTracker; tt != nil {
		total := tt.TotalUsed()
		limit := tt.Limit()
		if limit > 0 {
			pct := tt.PercentUsed()
			var tokenStyle lipgloss.Style
			switch {
			case pct >= 100:
				tokenStyle = lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("196")) // red
			case pct >= 80:
				tokenStyle = lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("214")) // orange
			default:
				tokenStyle = dim
			}
			parts = append(parts, tokenStyle.Render(fmt.Sprintf("tokens: %s/%s (%.0f%%)",
				formatTokenCount(total), formatTokenCount(limit), pct)))
		} else if total > 0 {
			parts = append(parts, dim.Render(fmt.Sprintf("tokens: %s", formatTokenCount(total))))
		}
	}

	// Git branch.
	if s.GitBranch != "" {
		parts = append(parts, bright.Render(fmt.Sprintf("\u2387 %s", s.GitBranch)))
	}

	// Active tools or thinking status.
	if len(s.ActiveTools) > 1 {
		// Multiple tools running in parallel.
		var toolNames []string
		for name := range s.ActiveTools {
			toolNames = append(toolNames, name)
		}
		sort.Strings(toolNames)
		parts = append(parts, bright.Render(fmt.Sprintf("tools[%d]: %s", len(toolNames), strings.Join(toolNames, ", "))))
	} else if s.ActiveTool != "" {
		elapsed := time.Since(s.ToolStart).Truncate(time.Millisecond)
		parts = append(parts, bright.Render(fmt.Sprintf("tool: %s (%s)", s.ActiveTool, elapsed)))
	} else if in.Running {
		parts = append(parts, dim.Render("thinking..."))
	}

	// Subagent status.
	if in.Orchestrator != nil {
		agents := in.Orchestrator.List()
		if len(agents) > 0 {
			var runningNames []string
			total := len(agents)
			failed := 0
			for _, a := range agents {
				switch a.Status {
				case "running":
					name := a.Type
					if len(name) > 12 {
						name = name[:12]
					}
					runningNames = append(runningNames, name)
				case "failed":
					failed++
				}
			}
			agentFg := lipgloss.Color("35") // green
			if len(runningNames) > 0 {
				agentFg = lipgloss.Color("214") // orange when active
			}
			if failed > 0 {
				agentFg = lipgloss.Color("196") // red if any failed
			}
			agentStyle := lipgloss.NewStyle().Background(bg).Foreground(agentFg)
			var label string
			if len(runningNames) > 0 {
				label = fmt.Sprintf("agents[%d]: %s", total, strings.Join(runningNames, ", "))
			} else {
				label = fmt.Sprintf("agents: %d done", total)
			}
			if failed > 0 {
				label += fmt.Sprintf(" (%d failed)", failed)
			}
			parts = append(parts, agentStyle.Render(label))
		}
	}

	// /run cycle indicator.
	if in.RunCycle != nil {
		runStyle := lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("214"))
		parts = append(parts, runStyle.Render(fmt.Sprintf("run[%s]: cycle %d/%d",
			in.RunCycle.SpecName, in.RunCycle.Cycle, in.RunCycle.MaxRetries)))
	}

	// Trace count.
	if in.TraceCount > 0 {
		parts = append(parts, dim.Render(fmt.Sprintf("trace: %d", in.TraceCount)))
	}

	return bar.Render(strings.Join(parts, sep))
}
