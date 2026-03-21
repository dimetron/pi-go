package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// SidebarWidth is the fixed width of the right sidebar.
const SidebarWidth = 30

// SidebarRenderInput provides data needed by the sidebar.
type SidebarRenderInput struct {
	Width        int
	Height       int
	Eyes         string
	Mode         string
	ProviderName string
	ModelName    string
	GitBranch    string
	DiffAdded    int
	DiffRemoved  int
	Running      bool
	TokenTracker TokenTracker
	Messages     []message
	ActiveTool   string
	LoadingItems map[string]bool
}

// RenderSidebar renders the right sidebar panel.
func RenderSidebar(in SidebarRenderInput) string {
	w := in.Width
	if w < 10 {
		w = 10
	}

	fg := lipgloss.Color("252")
	dimFg := lipgloss.Color("246")
	borderFg := lipgloss.Color("245")
	headingFg := lipgloss.Color("75") // blue

	dim := lipgloss.NewStyle().Foreground(dimFg)
	heading := lipgloss.NewStyle().Foreground(headingFg).Bold(true)
	bright := lipgloss.NewStyle().Foreground(fg)
	_ = bright

	innerW := w - 3 // padding + border

	var lines []string

	// --- Eyes / mood ---
	if in.Eyes != "" {
		eyeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
		moodLine := eyeStyle.Render(fmt.Sprintf("  %s", in.Eyes))
		lines = append(lines, "", moodLine, "")
	}

	// --- Context section ---
	lines = append(lines, heading.Render("  Context"))
	if tt := in.TokenTracker; tt != nil && tt.Limit() > 0 {
		total := tt.TotalUsed()
		limit := tt.Limit()
		pct := tt.PercentUsed()
		lines = append(lines, dim.Render(fmt.Sprintf("  %s / %s",
			formatTokenCount(total), formatTokenCount(limit))))
		lines = append(lines, "  "+renderContextBar(pct, lipgloss.NoColor{}))
	} else {
		ctxChars := 0
		for _, msg := range in.Messages {
			ctxChars += len(msg.content) + len(msg.tool) + len(msg.toolIn)
		}
		ctxTokens := ctxChars / 4
		if ctxTokens >= 1000 {
			lines = append(lines, dim.Render(fmt.Sprintf("  ~%.1fk tokens", float64(ctxTokens)/1000)))
		} else {
			lines = append(lines, dim.Render(fmt.Sprintf("  ~%d tokens", ctxTokens)))
		}
	}
	lines = append(lines, "")

	// --- Model section ---
	lines = append(lines, heading.Render("  Model"))
	if in.ProviderName != "" {
		lines = append(lines, dim.Render("  "+in.ProviderName))
	}
	if in.ModelName != "" {
		name := in.ModelName
		if len(name) > innerW {
			name = name[:innerW-1] + "…"
		}
		lines = append(lines, dim.Render("  "+name))
	}
	lines = append(lines, "")

	// --- Git section ---
	if in.GitBranch != "" {
		lines = append(lines, heading.Render("  Git"))
		lines = append(lines, dim.Render(fmt.Sprintf("  ⎇ %s", in.GitBranch)))
		if in.DiffAdded > 0 || in.DiffRemoved > 0 {
			addStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("35"))
			delStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
			lines = append(lines, "  "+
				addStyle.Render(fmt.Sprintf("+%d", in.DiffAdded))+
				dim.Render(" ")+
				delStyle.Render(fmt.Sprintf("-%d", in.DiffRemoved)))
		}
		lines = append(lines, "")
	}

	// --- Mode section ---
	lines = append(lines, heading.Render("  Mode"))
	mode := in.Mode
	if mode == "" {
		mode = "chat"
	}
	if mode == "plan" {
		modeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
		lines = append(lines, modeStyle.Render("  [plan]"))
	} else {
		lines = append(lines, dim.Render("  ["+mode+"]"))
	}

	// Status
	if in.Running {
		if in.ActiveTool != "" {
			lines = append(lines, dim.Render("  ⚡ "+in.ActiveTool))
		} else {
			lines = append(lines, dim.Render("  thinking..."))
		}
	}
	lines = append(lines, "")

	// --- Loading section ---
	if in.LoadingItems != nil {
		lines = append(lines, heading.Render("  Loading"))
		for _, name := range sortedKeys(in.LoadingItems) {
			if in.LoadingItems[name] {
				okStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("35"))
				lines = append(lines, okStyle.Render("  ✓ "+name))
			} else {
				loadStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
				lines = append(lines, loadStyle.Render("  ◌ "+name+"..."))
			}
		}
		lines = append(lines, "")
	}

	// Join content and pad to fill height.
	content := strings.Join(lines, "\n")
	contentLines := strings.Split(content, "\n")
	for len(contentLines) < in.Height {
		contentLines = append(contentLines, "")
	}
	if len(contentLines) > in.Height {
		contentLines = contentLines[:in.Height]
	}
	content = strings.Join(contentLines, "\n")

	// Wrap in a styled box with left border.
	box := lipgloss.NewStyle().
		Width(w).
		BorderStyle(lipgloss.Border{Left: "│"}).
		BorderLeft(true).
		BorderForeground(borderFg)

	return box.Render(content)
}
