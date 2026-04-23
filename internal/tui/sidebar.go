package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"

	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/subagent"
)

// SidebarWidth is the fixed width of the right sidebar.
const SidebarWidth = 30

// SidebarRenderInput provides data needed by the sidebar.
type SidebarRenderInput struct {
	Width        int
	Height       int
	Eyes         string
	Mascot       string // full 3-line mascot face (mutually exclusive with Eyes)
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
	RunChecklist []ChecklistStep          // steps from plan.md during /run
	RunPhase     string                   // current /run phase (empty if not running)
	RunSpec      string                   // spec name during /run
	RunCycle     int                      // current retry cycle
	RunMaxCycle  int                      // max retries
	MatrixLines  string                   // pre-rendered matrix rain (2 lines)
	StatusLine   string                   // status text shown above matrix
	Orchestrator *subagent.Orchestrator   // may be nil — for agents section
	MCPTools     []extension.MCPToolEntry // MCP tools section; nil = hidden
	OTELEnabled  bool                     // OTEL tracing is active
}

// RenderSidebar renders the right sidebar panel.
func RenderSidebar(in SidebarRenderInput) string {
	w := in.Width
	if w < 10 {
		w = 10
	}

	fg := lipgloss.Color("#cdd6f4")        // Mocha text
	dimFg := lipgloss.Color("#a6adc8")     // Mocha subtext0
	borderFg := lipgloss.Color("#45475a")  // Mocha surface1
	headingFg := lipgloss.Color("#89b4fa") // Mocha blue

	dim := lipgloss.NewStyle().Foreground(dimFg)
	heading := lipgloss.NewStyle().Foreground(headingFg).Bold(true)
	bright := lipgloss.NewStyle().Foreground(fg)
	_ = bright

	innerW := w - 3 // padding + border

	var lines []string

	// --- Eyes / mood ---
	if in.Mascot != "" {
		mascotStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#74c7ec")) // Mocha sapphire
		moodLine := mascotStyle.Render(fmt.Sprintf("  %s", in.Mascot))
		lines = append(lines, "", moodLine, "")
	} else if in.Eyes != "" {
		eyeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#74c7ec")) // Mocha sapphire
		moodLine := eyeStyle.Render(fmt.Sprintf("  %s", in.Eyes))
		lines = append(lines, "", moodLine, "")
	}

	// --- Context section (session context window usage) ---
	sidebarBg := lipgloss.Color("#181825") // Catppuccin Mocha mantle (subtle dark)
	lines = append(lines, heading.Render("  Context"))
	if tt := in.TokenTracker; tt != nil && tt.ContextWindowSize() > 0 {
		// Known context window: show prompt tokens / window size with bar.
		promptTokens := tt.LastPromptTokens()
		ctxWindow := tt.ContextWindowSize()
		pct := tt.ContextPercentUsed()
		lines = append(lines, dim.Render(fmt.Sprintf("  %s / %s",
			formatTokenCount(promptTokens), formatTokenCount(ctxWindow))))
		lines = append(lines, "  "+renderContextBar(pct, sidebarBg))
	} else if tt := in.TokenTracker; tt != nil && tt.LastPromptTokens() > 0 {
		// Unknown window but have actual prompt tokens from LLM.
		lines = append(lines, dim.Render(fmt.Sprintf("  %s tokens",
			formatTokenCount(tt.LastPromptTokens()))))
	} else {
		// No LLM response yet: estimate from message chars.
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
	if in.OTELEnabled {
		otelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#89b4fa")).Bold(true)
		lines = append(lines, otelStyle.Render("  ◉ OTEL"))
	}
	lines = append(lines, "")

	// --- Git section ---
	if in.GitBranch != "" {
		lines = append(lines, heading.Render("  Git"))
		lines = append(lines, dim.Render(fmt.Sprintf("  ⎇ %s", in.GitBranch)))
		if in.DiffAdded > 0 || in.DiffRemoved > 0 {
			addStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1"))
			delStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f38ba8"))
			lines = append(lines, "  "+
				addStyle.Render(fmt.Sprintf("+%d", in.DiffAdded))+
				dim.Render(" ")+
				delStyle.Render(fmt.Sprintf("-%d", in.DiffRemoved)))
		}
		lines = append(lines, "")
	}

	// --- Mode / Run section ---
	if len(in.RunChecklist) > 0 && in.RunPhase != "" {
		// Show run checklist instead of plain mode when /run is active.
		runHeading := fmt.Sprintf("  Run: %s", in.RunSpec)
		if len(runHeading) > innerW+2 {
			runHeading = runHeading[:innerW+1] + "…"
		}
		lines = append(lines, heading.Render(runHeading))

		// Phase and cycle info.
		phaseStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387"))
		lines = append(lines, phaseStyle.Render(fmt.Sprintf("  cycle %d/%d · %s",
			in.RunCycle, in.RunMaxCycle, in.RunPhase)))
		lines = append(lines, "")

		// Checklist items.
		doneStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1")) // green
		todoStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6c7086")) // gray

		for _, step := range in.RunChecklist {
			title := step.Title
			maxTitle := innerW - 5 // room for "  [x] " prefix
			if maxTitle < 10 {
				maxTitle = 10
			}
			if len(title) > maxTitle {
				title = title[:maxTitle-1] + "…"
			}
			if step.Done {
				lines = append(lines, doneStyle.Render("  [x] "+title))
			} else {
				lines = append(lines, todoStyle.Render("  [ ] "+title))
			}
		}

		// Status
		if in.Running {
			lines = append(lines, "")
			if in.ActiveTool != "" {
				lines = append(lines, dim.Render("  ⚡ "+in.ActiveTool))
			} else {
				lines = append(lines, dim.Render("  thinking..."))
			}
		}
	} else {
		lines = append(lines, heading.Render("  Mode"))
		mode := in.Mode
		if mode == "" {
			mode = "chat"
		}
		if mode == "plan" {
			modeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387"))
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
	}
	lines = append(lines, "")

	// --- Agents section ---
	if in.Orchestrator != nil {
		agents := in.Orchestrator.List()
		if len(agents) > 0 {
			// Sort agents for stable rendering: running first, then by start time.
			sort.Slice(agents, func(i, j int) bool {
				pi, pj := agentStatusPriority(agents[i].Status), agentStatusPriority(agents[j].Status)
				if pi != pj {
					return pi < pj
				}
				if !agents[i].StartedAt.Equal(agents[j].StartedAt) {
					return agents[i].StartedAt.Before(agents[j].StartedAt)
				}
				return agents[i].AgentID < agents[j].AgentID
			})

			var running, failed int
			for _, a := range agents {
				switch a.Status {
				case "running":
					running++
				case "failed":
					failed++
				}
			}

			agentHeadingFg := lipgloss.Color("#a6e3a1") // green
			if running > 0 {
				agentHeadingFg = lipgloss.Color("#fab387") // orange when active
			}
			if failed > 0 {
				agentHeadingFg = lipgloss.Color("#f38ba8") // red if any failed
			}
			agentHeading := lipgloss.NewStyle().Foreground(agentHeadingFg).Bold(true)
			lines = append(lines, agentHeading.Render(fmt.Sprintf("  Agents [%d]", len(agents))))

			// Show individual agents with name and status.
			// Add incrementing suffix when multiple agents share the same type.
			runStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387"))
			doneStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1"))
			failStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f38ba8"))
			killStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6c7086"))

			typeCounts := make(map[string]int) // count occurrences of each type
			for _, a := range agents {
				t := a.Type
				if t == "" {
					t = "agent"
				}
				typeCounts[t]++
			}
			typeSeq := make(map[string]int) // running sequence per type

			for _, a := range agents {
				name := a.Type
				if name == "" {
					name = "agent"
				}
				if typeCounts[name] > 1 {
					typeSeq[name]++
					name = fmt.Sprintf("%s-%d", name, typeSeq[name])
				}
				maxName := innerW - 6 // room for "  ⚡ " prefix + status icon
				if maxName < 6 {
					maxName = 6
				}
				if len(name) > maxName {
					name = name[:maxName-1] + "…"
				}
				switch a.Status {
				case "running":
					lines = append(lines, runStyle.Render("  ⚡ "+name))
				case "done":
					lines = append(lines, doneStyle.Render("  ✓ "+name))
				case "failed":
					lines = append(lines, failStyle.Render("  ✗ "+name))
				case "killed":
					lines = append(lines, killStyle.Render("  ⊘ "+name))
				default:
					lines = append(lines, dim.Render("  · "+name))
				}
			}
			lines = append(lines, "")
		}
	}

	// --- MCP Tools section ---
	if len(in.MCPTools) > 0 {
		mcpHeading := lipgloss.NewStyle().Foreground(lipgloss.Color("#cba6f7")).Bold(true) // Mocha mauve
		toolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6adc8"))             // dim

		// Group tools by server name.
		type serverGroup struct {
			name  string
			tools []string
		}
		seenOrder := []string{}
		grouped := map[string]*serverGroup{}
		for _, e := range in.MCPTools {
			if _, ok := grouped[e.Server]; !ok {
				seenOrder = append(seenOrder, e.Server)
				grouped[e.Server] = &serverGroup{name: e.Server}
			}
			grouped[e.Server].tools = append(grouped[e.Server].tools, e.Tool)
		}

		totalTools := len(in.MCPTools)
		lines = append(lines, mcpHeading.Render(fmt.Sprintf("  MCP Tools [%d]", totalTools)))

		for _, srv := range seenOrder {
			g := grouped[srv]
			srvLabel := g.name
			if len(srvLabel) > innerW-4 {
				srvLabel = srvLabel[:innerW-5] + "…"
			}
			lines = append(lines, dim.Render("  ⬡ "+srvLabel))
			for _, t := range g.tools {
				tName := t
				if len(tName) > innerW-6 {
					tName = tName[:innerW-7] + "…"
				}
				lines = append(lines, toolStyle.Render("    · "+tName))
			}
		}
		lines = append(lines, "")
	}

	// --- Loading section ---
	if in.LoadingItems != nil {
		lines = append(lines, heading.Render("  Loading"))
		for _, name := range sortedKeys(in.LoadingItems) {
			if in.LoadingItems[name] {
				okStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1"))
				lines = append(lines, okStyle.Render("  ✓ "+name))
			} else {
				loadStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387"))
				lines = append(lines, loadStyle.Render("  ◌ "+name+"..."))
			}
		}
		lines = append(lines, "")
	}

	// Join content and pad to fill height, reserving 3 lines for status + matrix.
	hasMatrix := in.MatrixLines != ""
	matrixH := 0
	statusH := 0
	if hasMatrix {
		matrixH = matrixLines
		statusH = 1 // 1 line for status separator
	}
	content := strings.Join(lines, "\n")
	contentLines := strings.Split(content, "\n")
	targetH := in.Height - matrixH - statusH
	if len(contentLines) < targetH {
		// Fill remaining space with subtle dim separators or spacer text.
		fillerStyle := lipgloss.NewStyle().Foreground(dimFg)
		for len(contentLines) < targetH {
			contentLines = append(contentLines, fillerStyle.Render("  ···"))
		}
	}
	if len(contentLines) > targetH {
		contentLines = contentLines[:targetH]
	}

	// Add status separator line above matrix (if active).
	if hasMatrix {
		statusStyle := lipgloss.NewStyle().Foreground(dimFg)
		statusText := in.StatusLine
		if statusText == "" {
			statusText = "──── tokens ────"
		}
		// Truncate status text to fit (rune-safe)
		maxStatusW := w - 4
		if runewidth.StringWidth(statusText) > maxStatusW {
			statusText = runewidth.Truncate(statusText, maxStatusW-1, "─")
		}
		contentLines = append(contentLines, statusStyle.Render(statusText))
		contentLines = append(contentLines, strings.Split(in.MatrixLines, "\n")...)
	}
	content = strings.Join(contentLines, "\n")

	// Wrap in a styled box with subtle dark background (Mocha mantle),
	// left border to separate from main panel.
	box := lipgloss.NewStyle().
		Width(w).
		Background(sidebarBg).
		BorderStyle(lipgloss.Border{Left: "│"}).
		BorderLeft(true).
		BorderForeground(borderFg)

	return box.Render(content)
}

// agentStatusPriority returns a sort key for agent status.
// Running agents appear first, then done, then failed/killed.
func agentStatusPriority(status string) int {
	switch status {
	case "running":
		return 0
	case "done", "completed":
		return 1
	case "failed":
		return 2
	case "killed":
		return 3
	default:
		return 4
	}
}
