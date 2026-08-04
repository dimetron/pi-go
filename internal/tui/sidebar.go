package tui

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
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
	Artifacts    []ArtifactEntry          // artifacts section; nil/empty = hidden
}

// ArtifactEntry is one row in the Artifacts sidebar section.
// Filename is the ADK artifact key (e.g. "screenshot.png").
// Size is the byte count of the stored part.
// Mime is optional; entries with an "image/*" MIME get a frame icon,
// everything else gets a paperclip.
type ArtifactEntry struct {
	Filename string
	Size     int64
	Mime     string
}

// Catppuccin Mocha slice used across the sidebar sections.
const (
	sidebarTextHex     = "#cdd6f4" // text
	sidebarSubtextHex  = "#a6adc8" // subtext0
	sidebarBlueHex     = "#89b4fa" // blue
	sidebarSapphireHex = "#74c7ec" // sapphire
	sidebarYellowHex   = "#f9e2af" // yellow
	sidebarPeachHex    = "#fab387" // peach
	sidebarGreenHex    = "#a6e3a1" // green
	sidebarRedHex      = "#f38ba8" // red
	sidebarTealHex     = "#94e2d5" // teal
	sidebarPinkHex     = "#f5c2e7" // pink
	sidebarMauveHex    = "#cba6f7" // mauve
	sidebarOverlayHex  = "#6c7086" // overlay0
	sidebarSurfaceHex  = "#585b70" // surface2, same as the panel rule
	// crust with alpha, for dark transparency
	sidebarBgHex = "#11111bcc"
)

var (
	sidebarBg      = lipgloss.Color(sidebarBgHex)
	sidebarDim     = lipgloss.NewStyle().Foreground(lipgloss.Color(sidebarSubtextHex))
	sidebarHeading = lipgloss.NewStyle().Foreground(lipgloss.Color(sidebarBlueHex)).Bold(true)
)

// sidebarStyle is shorthand for a plain foreground style in the Mocha palette.
func sidebarStyle(hex string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(hex))
}

// RenderSidebar renders the right sidebar panel.
//
// The body is a sequence of independent sections, each contributing its own
// lines (nil when hidden); sidebarFrame then pads, clamps and boxes the result
// to the requested width and height.
func RenderSidebar(in SidebarRenderInput) string {
	w := max(in.Width, 10)
	innerW := w - 3 // padding + border

	var lines []string
	for _, section := range [][]string{
		sidebarMoodLines(in),
		sidebarModelLines(in, innerW),
		sidebarArtifactLines(in, innerW),
		sidebarGitLines(in, innerW),
		sidebarModeLines(in, innerW),
		sidebarAgentLines(in, innerW),
		sidebarSkillLines(in),
		sidebarMemoryLines(in),
		sidebarMCPLines(in, innerW),
		sidebarLoadingLines(in),
	} {
		lines = append(lines, section...)
	}

	return sidebarFrame(in, lines, w)
}

// sidebarMoodLines renders the mascot face or the eyes at the top of the
// sidebar. The two are mutually exclusive; the mascot wins if both are set.
func sidebarMoodLines(in SidebarRenderInput) []string {
	face := cmp.Or(in.Mascot, in.Eyes)
	if face == "" {
		return nil
	}
	return []string{"", sidebarStyle(sidebarSapphireHex).Render(fmt.Sprintf("  %s", face)), ""}
}

// sidebarContextLines / sidebarOTELLines were removed: the bottom context rule
// is the canonical context gauge (calibrated to the dumb-zone framework) and the
// OTEL indicator adds nothing the user cannot already see via the run status.

// sidebarModelLines shows the active provider and model name.
func sidebarModelLines(in SidebarRenderInput, innerW int) []string {
	lines := []string{sidebarHeading.Render("  Model")}
	if in.ProviderName != "" {
		lines = append(lines, sidebarStyle(sidebarTextHex).Render("  "+in.ProviderName))
	}
	if in.ModelName != "" {
		lines = append(lines, sidebarStyle(sidebarYellowHex).
			Render("  "+truncateLabel(in.ModelName, innerW)))
	}
	return append(lines, "")
}

// sidebarArtifactLines lists stored session artifacts with an icon and size.
func sidebarArtifactLines(in SidebarRenderInput, innerW int) []string {
	if len(in.Artifacts) == 0 {
		return nil
	}
	lines := []string{sidebarStyle(sidebarPeachHex).Bold(true).
		Render(fmt.Sprintf("  Artifacts [%d]", len(in.Artifacts)))}

	for _, a := range in.Artifacts {
		icon := "📎"
		if strings.HasPrefix(a.Mime, "image/") {
			icon = "🖼"
		}
		// Layout: "  ICON NAME  SIZE" with a 2-space gap before size.
		// Reserve 10 chars for the size column so 2.1 MB lines align, and one
		// spare cell so a truncated name never butts against the size column.
		name := truncateLabel(a.Filename, max(innerW-2-2-10, 6)-1)
		lines = append(lines, sidebarDim.Render(
			fmt.Sprintf("  %s %s  %s", icon, name, formatBytes(a.Size))))
	}
	return append(lines, "")
}

// sidebarGitLines shows the current branch and the working-tree diff counts.
func sidebarGitLines(in SidebarRenderInput, innerW int) []string {
	if in.GitBranch == "" {
		return nil
	}
	// Line 1: "Git" heading + "+N -M" counts.
	gitLine := sidebarHeading.Render("  Git")
	if in.DiffAdded > 0 || in.DiffRemoved > 0 {
		gitLine += " " +
			sidebarStyle(sidebarGreenHex).Render(fmt.Sprintf("+%d", in.DiffAdded)) +
			sidebarDim.Render(" ") +
			sidebarStyle(sidebarRedHex).Render(fmt.Sprintf("-%d", in.DiffRemoved))
	}

	// Line 2: branch with ⎇ prefix, truncated to fit inner width.
	// Prefix "  ⎇ " is 5 visible cells, plus one spare cell at the right edge.
	branch := truncateLabel(in.GitBranch, max(innerW-5, 4)-1)
	return []string{gitLine, "  " + sidebarStyle(sidebarTealHex).Render("⎇ "+branch), ""}
}

// sidebarModeLines renders the /run checklist while a run is active, and the
// plain mode indicator otherwise.
func sidebarModeLines(in SidebarRenderInput, innerW int) []string {
	if len(in.RunChecklist) > 0 && in.RunPhase != "" {
		return append(sidebarRunLines(in, innerW), "")
	}

	lines := []string{sidebarHeading.Render("  Mode")}
	mode := cmp.Or(in.Mode, "chat")
	if mode == "plan" {
		lines = append(lines, sidebarStyle(sidebarPeachHex).Render("  [plan]"))
	} else {
		lines = append(lines, sidebarDim.Render("  ["+mode+"]"))
	}
	lines = append(lines, sidebarActivityLines(in)...)
	return append(lines, "")
}

// sidebarRunLines renders the spec name, cycle/phase and step checklist of an
// in-progress /run.
func sidebarRunLines(in SidebarRenderInput, innerW int) []string {
	lines := []string{
		sidebarHeading.Render(truncateLabel(fmt.Sprintf("  Run: %s", in.RunSpec), innerW+2)),
		sidebarStyle(sidebarPeachHex).Render(fmt.Sprintf("  cycle %d/%d ∙ %s",
			in.RunCycle, in.RunMaxCycle, in.RunPhase)),
		"",
	}

	doneStyle := sidebarStyle(sidebarGreenHex)
	todoStyle := sidebarStyle(sidebarOverlayHex)
	for _, step := range in.RunChecklist {
		title := truncateLabel(step.Title, max(innerW-5, 10)) // room for "  [x] " prefix
		if step.Done {
			lines = append(lines, doneStyle.Render("  [x] "+title))
			continue
		}
		lines = append(lines, todoStyle.Render("  [ ] "+title))
	}

	if in.Running {
		lines = append(lines, "")
		lines = append(lines, sidebarActivityLines(in)...)
	}
	return lines
}

// sidebarActivityLines shows the running tool, or a thinking indicator when the
// agent is busy without a named tool. Empty when idle.
func sidebarActivityLines(in SidebarRenderInput) []string {
	if !in.Running {
		return nil
	}
	if in.ActiveTool != "" {
		return []string{sidebarDim.Render("  ⚡ " + in.ActiveTool)}
	}
	return []string{sidebarDim.Render("  thinking...")}
}

// sidebarAgentLines lists spawned subagents, running ones first.
func sidebarAgentLines(in SidebarRenderInput, innerW int) []string {
	if in.Orchestrator == nil {
		return nil
	}
	agents := in.Orchestrator.List()
	if len(agents) == 0 {
		return nil
	}
	sortAgentsForDisplay(agents)

	lines := []string{agentHeadingStyle(agents).Render(fmt.Sprintf("  Agents [%d]", len(agents)))}
	names := agentDisplayNames(agents, innerW)
	for i, a := range agents {
		lines = append(lines, agentRow(a.Status, names[i]))
	}
	return append(lines, "")
}

// sortAgentsForDisplay orders agents by status (running first), then by start
// time, then by ID so rendering stays stable across frames.
func sortAgentsForDisplay(agents []subagent.AgentStatus) {
	slices.SortFunc(agents, func(a, b subagent.AgentStatus) int {
		if c := cmp.Compare(agentStatusPriority(a.Status), agentStatusPriority(b.Status)); c != 0 {
			return c
		}
		if c := a.StartedAt.Compare(b.StartedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.AgentID, b.AgentID)
	})
}

// agentHeadingStyle colors the Agents heading by the worst status present:
// red if anything failed, orange while any agent runs, green otherwise.
func agentHeadingStyle(agents []subagent.AgentStatus) lipgloss.Style {
	var running, failed int
	for _, a := range agents {
		switch a.Status {
		case "running":
			running++
		case "failed":
			failed++
		}
	}
	hex := sidebarGreenHex
	if running > 0 {
		hex = sidebarPeachHex
	}
	if failed > 0 {
		hex = sidebarRedHex
	}
	return sidebarStyle(hex).Bold(true)
}

// agentDisplayNames labels each agent by type, appending an incrementing
// suffix when several agents share a type, truncated to fit the sidebar.
func agentDisplayNames(agents []subagent.AgentStatus, innerW int) []string {
	typeCounts := make(map[string]int)
	for _, a := range agents {
		typeCounts[cmp.Or(a.Type, "agent")]++
	}

	typeSeq := make(map[string]int) // running sequence per type
	names := make([]string, len(agents))
	for i, a := range agents {
		name := cmp.Or(a.Type, "agent")
		if typeCounts[name] > 1 {
			typeSeq[name]++
			name = fmt.Sprintf("%s-%d", name, typeSeq[name])
		}
		names[i] = truncateLabel(name, max(innerW-6, 6)) // room for "  ⚡ " prefix + icon
	}
	return names
}

// agentRow renders one agent line with a status icon and matching color.
func agentRow(status, name string) string {
	switch status {
	case "running":
		return sidebarStyle(sidebarPeachHex).Render("  ⚡ " + name)
	case "done":
		return sidebarStyle(sidebarGreenHex).Render("  ✓ " + name)
	case "failed":
		return sidebarStyle(sidebarRedHex).Render("  ✗ " + name)
	case "killed":
		return sidebarStyle(sidebarOverlayHex).Render("  ⊘ " + name)
	default:
		return sidebarDim.Render("  ∙ " + name)
	}
}

// sidebarSkillLines shows how many skills are loaded.
func sidebarSkillLines(in SidebarRenderInput) []string {
	if len(in.Skills) == 0 {
		return nil
	}
	return []string{sidebarStyle(sidebarYellowHex).Bold(true).
		Render(fmt.Sprintf("  Skills [%d]", len(in.Skills))), ""}
}

// sidebarMemoryLines summarizes memory palace state: drawers, model readiness,
// knowledge-graph entities and rooms.
func sidebarMemoryLines(in SidebarRenderInput) []string {
	if in.MemoryStatus == nil {
		return nil
	}
	lines := []string{sidebarStyle(sidebarPinkHex).Bold(true).
		Render(fmt.Sprintf("  Memory [%d]", in.MemoryStatus.DrawerCount))}
	if in.MemoryStatus.ModelLoaded {
		lines = append(lines, sidebarDim.Render("  ⬡ model ready"))
	}
	if in.MemoryStatus.KG != nil {
		lines = append(lines, sidebarDim.Render(
			fmt.Sprintf("  ⬡ %d entities", in.MemoryStatus.KG.EntityCount)))
	}
	lines = append(lines, sidebarDim.Render(fmt.Sprintf("  ⬡ %d rooms", in.MemoryStatus.RoomCount)))
	return append(lines, "")
}

// sidebarMCPLines counts MCP tools per server rather than listing every tool,
// keeping the section a fixed handful of rows.
func sidebarMCPLines(in SidebarRenderInput, innerW int) []string {
	if len(in.MCPTools) == 0 {
		return nil
	}
	seenOrder := []string{}
	toolCounts := map[string]int{}
	for _, e := range in.MCPTools {
		if _, ok := toolCounts[e.Server]; !ok {
			seenOrder = append(seenOrder, e.Server)
		}
		toolCounts[e.Server]++
	}

	lines := []string{sidebarStyle(sidebarMauveHex).Bold(true).
		Render(fmt.Sprintf("  MCP Tools [%d]", len(in.MCPTools)))}
	for _, srv := range seenOrder {
		countLabel := fmt.Sprintf(" [%d]", toolCounts[srv])
		srvLabel := truncateLabel(srv, max(innerW-4-len(countLabel), 1))
		lines = append(lines, sidebarDim.Render("  ⬡ "+srvLabel+countLabel))
	}
	return append(lines, "")
}

// sidebarLoadingLines tracks startup subsystems, ticking each off as it loads.
func sidebarLoadingLines(in SidebarRenderInput) []string {
	if in.LoadingItems == nil {
		return nil
	}
	lines := []string{sidebarHeading.Render("  Loading")}
	for _, name := range sortedKeys(in.LoadingItems) {
		if in.LoadingItems[name] {
			lines = append(lines, sidebarStyle(sidebarGreenHex).Render("  ✓ "+name))
			continue
		}
		lines = append(lines, sidebarStyle(sidebarPeachHex).Render("  ◌ "+name+"..."))
	}
	return append(lines, "")
}

// sidebarFrame pads the section lines to fill the panel height, appends the
// token status line and matrix rain when active, closes with the rule, and
// boxes the result at a fixed width.
func sidebarFrame(in SidebarRenderInput, lines []string, w int) string {
	// Reserve rows for the matrix rain and its status separator.
	hasMatrix := in.MatrixLines != ""
	matrixH, statusH := 0, 0
	if hasMatrix {
		matrixH = matrixLines
		statusH = 1
	}
	// The closing rule owns the last row. It carries the panel's rule across the
	// sidebar so the two read as one line spanning the terminal, the way the
	// status bar's does, and it is the one row that is always exactly w cells
	// wide — the block can never measure narrower than the width it was given,
	// however short its content runs.
	ruleH := 0
	if in.Height > 0 {
		ruleH = 1
	}

	contentLines := strings.Split(strings.Join(lines, "\n"), "\n")
	targetH := max(0, in.Height-matrixH-statusH-ruleH)
	// Fill remaining space with subtle dim separators.
	for len(contentLines) < targetH {
		contentLines = append(contentLines, sidebarDim.Render("  ∙∙∙"))
	}
	if len(contentLines) > targetH {
		contentLines = contentLines[:targetH]
	}

	if hasMatrix {
		statusText := cmp.Or(in.StatusLine, "──── tokens ────")
		if maxStatusW := w - 4; runewidth.StringWidth(statusText) > maxStatusW {
			statusText = runewidth.Truncate(statusText, maxStatusW-1, "─")
		}
		contentLines = append(contentLines, sidebarDim.Render(statusText))
		contentLines = append(contentLines, strings.Split(in.MatrixLines, "\n")...)
	}
	if ruleH > 0 {
		contentLines = append(contentLines,
			sidebarStyle(sidebarSurfaceHex).Render(strings.Repeat("─", w)))
	}

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

	return box.Render(strings.Join(contentLines, "\n"))
}

// truncateLabel shortens s to at most maxW display cells, marking the cut with
// an ellipsis. Width is measured in cells rather than bytes so wide glyphs and
// multi-byte names cannot overflow the column or be sliced mid-rune.
func truncateLabel(s string, maxW int) string {
	if runewidth.StringWidth(s) <= maxW {
		return s
	}
	return runewidth.Truncate(s, maxW, "…")
}

func sidebarFolderName(workDir string) string {
	if workDir == "" {
		return ""
	}
	return filepath.Base(filepath.Clean(workDir))
}

// formatBytes renders a byte count as "812 B" / "124 KB" / "2.1 MB".
// No GB tier — session artifacts won't hit that scale, and the
// sidebar column is only ~10 chars wide.
func formatBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%d KB", n/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
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
