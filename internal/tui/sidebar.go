package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"

	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/palace"
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
	AppVersion   string
	HostName     string
	FolderName   string
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
	Skills       []extension.Skill        // skills section; nil = hidden
	MCPTools     []extension.MCPToolEntry // MCP tools section; nil = hidden
	MemoryStatus *palace.PalaceStatus     // memory palace status; nil = hidden
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

	sidebarBg := lipgloss.Color("#11111bcc") // Catppuccin Mocha crust with alpha for dark transparency

	// --- Context section (session context window usage) ---
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

	// --- Skills section ---
	if len(in.Skills) > 0 {
		skillsHeading := lipgloss.NewStyle().Foreground(lipgloss.Color("#f9e2af")).Bold(true) // Mocha yellow
		lines = append(lines, skillsHeading.Render(fmt.Sprintf("  Skills [%d]", len(in.Skills))))
		lines = append(lines, "")
	}

	// --- Memory section ---
	if in.MemoryStatus != nil {
		memHeading := lipgloss.NewStyle().Foreground(lipgloss.Color("#f5c2e7")).Bold(true) // Mocha pink
		dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6adc8"))              // Mocha subtext0
		lines = append(lines, memHeading.Render(fmt.Sprintf("  Memory [%d]", in.MemoryStatus.DrawerCount)))
		if in.MemoryStatus.ModelLoaded {
			lines = append(lines, dimStyle.Render("  ⬡ model ready"))
		}
		if in.MemoryStatus.KG != nil {
			lines = append(lines, dimStyle.Render(fmt.Sprintf("  ⬡ %d entities", in.MemoryStatus.KG.EntityCount)))
		}
		lines = append(lines, dimStyle.Render(fmt.Sprintf("  ⬡ %d rooms", in.MemoryStatus.RoomCount)))
		lines = append(lines, "")
	}

	// --- MCP Tools section ---
	if len(in.MCPTools) > 0 {
		mcpHeading := lipgloss.NewStyle().Foreground(lipgloss.Color("#cba6f7")).Bold(true) // Mocha mauve
		countStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6adc8"))            // dim

		// Count tools by server name without listing each tool.
		seenOrder := []string{}
		toolCounts := map[string]int{}
		for _, e := range in.MCPTools {
			if _, ok := toolCounts[e.Server]; !ok {
				seenOrder = append(seenOrder, e.Server)
			}
			toolCounts[e.Server]++
		}

		totalTools := len(in.MCPTools)
		lines = append(lines, mcpHeading.Render(fmt.Sprintf("  MCP Tools [%d]", totalTools)))

		for _, srv := range seenOrder {
			srvLabel := srv
			countLabel := fmt.Sprintf(" [%d]", toolCounts[srv])
			maxServerW := innerW - 4 - len(countLabel)
			if maxServerW < 1 {
				maxServerW = 1
			}
			if len(srvLabel) > maxServerW {
				srvLabel = srvLabel[:maxServerW-1] + "…"
			}
			lines = append(lines, countStyle.Render("  ⬡ "+srvLabel+countLabel))
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

	// A fixed-size box, flush to the right edge of the terminal. Width alone only
	// pads — a line longer than w would still push the box wider and drag the
	// whole frame past the screen, so MaxWidth clamps it. Height/MaxHeight pin
	// it to the panel beside it: a taller sidebar makes JoinHorizontal pad the
	// panel with blank rows, which is what left a gap under the prompt.
	//
	// No left border: the main panel's rail already divides the two, and drawing
	// a border here as well put two vertical rules side by side.
	box := lipgloss.NewStyle().
		Width(w).
		MaxWidth(w).
		Background(sidebarBg)
	if in.Height > 0 {
		box = box.Height(in.Height).MaxHeight(in.Height)
	}

	return box.Render(content)
}

func sidebarFolderName(workDir string) string {
	if workDir == "" {
		return ""
	}
	return filepath.Base(filepath.Clean(workDir))
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
