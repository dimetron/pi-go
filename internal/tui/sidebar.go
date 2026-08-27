package tui

import (
	"bufio"
	"cmp"
	"fmt"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"

	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/palace"
	"github.com/dimetron/pi-go/internal/sop"
	"github.com/dimetron/pi-go/internal/subagent"
)

// SidebarWidth is the fixed width of the right sidebar.
//
// 23 is the 18 it used to be, widened by 5: the sidebar's rows are short labels
// and counts, so the columns it gives back to the chat panel are worth more than
// the slack it keeps. Every consumer reads this constant — mainWidth, the
// render-integrity test's column check — so the whole frame follows from it.
const SidebarWidth = 23

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
	RunChecklist []ChecklistStep // steps from plan.md during /run
	RunPhase     string          // current /run phase (empty if not running)
	RunSpec      string          // spec name during /run
	RunCycle     int             // current retry cycle
	RunMaxCycle  int             // max retries
	PlanPhases   []PlanPhase     // PDD phase checklist shown in plan mode; nil/empty = hidden
	Graph        *SOPGraph       // compiled SOP drawn under the stage list; nil = hidden
	// mergePlanGraph draws the plan section as the graph alone rather than as a
	// checklist plus a graph saying the same thing twice. Set by RenderSidebar
	// once it knows the graph will fit.
	mergePlanGraph bool
	MatrixLines    string                   // pre-rendered matrix rain (2 lines)
	StatusLine     string                   // status text shown above matrix
	Orchestrator   *subagent.Orchestrator   // may be nil — for agents section
	Skills         []extension.Skill        // skills section; nil = hidden
	MCPTools       []extension.MCPToolEntry // MCP tools section; nil = hidden
	MemoryStatus   *palace.PalaceStatus     // memory palace status; nil = hidden
	Artifacts      []ArtifactEntry          // artifacts section; nil/empty = hidden
	Palette        Palette                  // resolved theme palette; zero = dark default
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

// SOPGraph is the compiled SOP the sidebar draws underneath the stage list:
// the stages in order, the edges between them, and each stage's status. Nil
// hides the diagram.
type SOPGraph struct {
	Order  []string
	Edges  []sop.GraphEdge
	Status map[string]stageStatus
}

// PlanPhase is one PDD phase in the plan-mode sidebar checklist.
type PlanPhase struct {
	Name string // short label, e.g. "Requirements"
	Done bool   // the artifact carries real content, not just a skeleton
}

// phaseArtifacts maps each PDD phase to the spec artifact that marks it complete.
var phaseArtifacts = []struct {
	Name     string
	Artifact string // file or dir name under specDir
}{
	{"Idea", "rough-idea.md"},
	{"Requirements", "requirements.md"},
	{"Research", "research"}, // directory
	{"Design", "design.md"},
	{"Outline", "outline.md"},
	{"Plan", "plan.md"},
	{"Prompt", "PROMPT.md"},
}

// detectPlanPhases inspects each PDD phase artifact under specDir and returns
// the phases in order, with Done set when the artifact carries real content. A
// missing or unreadable specDir degrades gracefully to all-incomplete (no
// crash).
//
// Existence is not enough. createSpecSkeleton writes the skeleton up front — an
// empty research/ directory and a requirements.md holding only its two headings
// — so a "does the file exist" test ticked Requirements and Research before a
// single question had been asked.
func detectPlanPhases(specDir string) []PlanPhase {
	phases := make([]PlanPhase, 0, len(phaseArtifacts))
	for _, pa := range phaseArtifacts {
		phases = append(phases, PlanPhase{
			Name: pa.Name,
			Done: hasSubstance(filepath.Join(specDir, pa.Artifact)),
		})
	}
	return phases
}

// artifactScanLimit bounds how far hasSubstance reads looking for a body line.
// This runs on every frame, so it must not grow with the size of plan.md; a
// real document says something well inside the first few KB.
const artifactScanLimit = 4 << 10

// hasSubstance reports whether a phase artifact holds more than its skeleton: a
// directory with at least one non-empty file in it, or a file with at least one
// line that is neither blank nor a markdown heading.
func hasSubstance(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}

	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return false
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if fi, err := e.Info(); err == nil && fi.Size() > 0 {
				return true
			}
		}
		return false
	}

	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(io.LimitReader(f, artifactScanLimit))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return true
	}
	return false
}

// sidebarStyles bundles the resolved styles the sidebar draws with, derived
// from the active theme's palette. It is built once per frame in RenderSidebar
// and threaded through the section renderers so a light theme actually renders.
type sidebarStyles struct {
	dim     lipgloss.Style
	heading lipgloss.Style
	bg      color.Color
	// per-role foreground styles
	text, subtext, blue, sapphire, yellow, peach, green, red, teal, pink, mauve, overlay, surface lipgloss.Style
}

// newSidebarStyles resolves a sidebarStyles from a palette.
func newSidebarStyles(p Palette) sidebarStyles {
	return sidebarStyles{
		dim:      lipgloss.NewStyle().Foreground(p.Subtext),
		heading:  lipgloss.NewStyle().Foreground(p.Blue).Bold(true),
		bg:       p.Background,
		text:     lipgloss.NewStyle().Foreground(p.Text),
		subtext:  lipgloss.NewStyle().Foreground(p.Subtext),
		blue:     lipgloss.NewStyle().Foreground(p.Blue),
		sapphire: lipgloss.NewStyle().Foreground(p.Sapphire),
		yellow:   lipgloss.NewStyle().Foreground(p.Yellow),
		peach:    lipgloss.NewStyle().Foreground(p.Peach),
		green:    lipgloss.NewStyle().Foreground(p.Green),
		red:      lipgloss.NewStyle().Foreground(p.Red),
		teal:     lipgloss.NewStyle().Foreground(p.Teal),
		pink:     lipgloss.NewStyle().Foreground(p.Pink),
		mauve:    lipgloss.NewStyle().Foreground(p.Mauve),
		overlay:  lipgloss.NewStyle().Foreground(p.Overlay),
		surface:  lipgloss.NewStyle().Foreground(p.Surface),
	}
}

// RenderSidebar renders the right sidebar panel.
//
// The body is a sequence of independent sections, each contributing its own
// lines (nil when hidden); sidebarFrame then pads, clamps and boxes the result
// to the requested width and height.
func RenderSidebar(in SidebarRenderInput) string {
	w := max(in.Width, 10)
	innerW := w - 3 // padding + border

	st := newSidebarStyles(paletteOrDark(in.Palette))

	tail := sidebarTailLines(in, innerW, st)

	// Prefer the merged plan view: one section carrying both the progress and
	// the shape. It is tried first and kept only if the whole thing fits, since
	// the checklist alone is what a short panel can still show usefully.
	if in.Graph != nil && len(in.PlanPhases) > 0 {
		merged := in
		merged.mergePlanGraph = true
		head := sidebarHeadLines(merged, innerW, st)
		if len(head)+len(tail) <= sidebarContentHeight(in) {
			return sidebarFrame(in, append(head, tail...), w, st)
		}
	}

	head := sidebarHeadLines(in, innerW, st)

	// Outside plan mode the diagram annotates the list above it rather than
	// replacing it — a /run slice checklist is per-slice progress, which the
	// stage graph does not duplicate — so it is spliced between head and tail.
	lines := append(head, graphSection(in, len(head)+len(tail), innerW, st)...)
	lines = append(lines, tail...)

	return sidebarFrame(in, lines, w, st)
}

// sidebarHeadLines renders the sections above the SOP diagram.
func sidebarHeadLines(in SidebarRenderInput, innerW int, st sidebarStyles) []string {
	var out []string
	for _, section := range [][]string{
		sidebarMoodLines(in, st),
		sidebarModelLines(in, innerW, st),
		sidebarArtifactLines(in, innerW, st),
		sidebarGitLines(in, innerW, st),
		sidebarModeLines(in, innerW, st),
	} {
		out = append(out, section...)
	}
	return out
}

// sidebarTailLines renders the sections below the SOP diagram.
func sidebarTailLines(in SidebarRenderInput, innerW int, st sidebarStyles) []string {
	var out []string
	for _, section := range [][]string{
		sidebarAgentLines(in, innerW, st),
		sidebarSkillLines(in, st),
		sidebarMemoryLines(in, st),
		sidebarMCPLines(in, innerW, st),
		sidebarLoadingLines(in, st),
	} {
		out = append(out, section...)
	}
	return out
}

// graphSection renders the SOP diagram, or nothing when it will not fit.
//
// All or nothing, on purpose: sidebarFrame clips from the bottom, so a diagram
// that overruns the panel would be cut mid-branch — half a graph, with no sign
// that the rest exists. Dropping it whole leaves the stage list, which always
// fits.
func graphSection(in SidebarRenderInput, used, innerW int, st sidebarStyles) []string {
	if in.Graph == nil || len(in.PlanPhases) > 0 {
		return nil // plan mode draws the graph inside its own section, or not at all
	}
	graph := sidebarGraphLines(in.Graph.Order, in.Graph.Edges, in.Graph.Status, innerW, st)
	if len(graph) == 0 {
		return nil
	}
	if used+len(graph)+1 > sidebarContentHeight(in) {
		return nil
	}
	return append(graph, "")
}

// sidebarContentHeight is how many rows sidebarFrame keeps before it clips. It
// mirrors the frame's own reservation; the two must agree or the fit test above
// is a guess.
func sidebarContentHeight(in SidebarRenderInput) int {
	matrixH, statusH := 0, 0
	if in.MatrixLines != "" {
		matrixH = matrixLines
		statusH = 1
	}
	ruleH := 0
	if in.Height > 0 {
		ruleH = 1
	}
	return max(0, in.Height-matrixH-statusH-ruleH)
}

// sidebarMoodLines renders the mascot face or the eyes at the top of the
// sidebar. The two are mutually exclusive; the mascot wins if both are set.
func sidebarMoodLines(in SidebarRenderInput, st sidebarStyles) []string {
	face := cmp.Or(in.Mascot, in.Eyes)
	if face == "" {
		return nil
	}
	return []string{"", st.sapphire.Render(fmt.Sprintf("  %s", face)), ""}
}

// sidebarContextLines / sidebarOTELLines were removed: the bottom context rule
// is the canonical context gauge (calibrated to the dumb-zone framework) and the
// OTEL indicator adds nothing the user cannot already see via the run status.

// sidebarModelLines shows the active provider and model name.
func sidebarModelLines(in SidebarRenderInput, innerW int, st sidebarStyles) []string {
	lines := []string{st.heading.Render("  Model")}
	if in.ProviderName != "" {
		lines = append(lines, st.text.Render("  "+in.ProviderName))
	}
	if in.ModelName != "" {
		lines = append(lines, st.yellow.
			Render("  "+truncateLabel(in.ModelName, innerW)))
	}
	return append(lines, "")
}

// sidebarArtifactLines lists stored session artifacts with an icon and size.
func sidebarArtifactLines(in SidebarRenderInput, innerW int, st sidebarStyles) []string {
	if len(in.Artifacts) == 0 {
		return nil
	}
	lines := []string{st.peach.Bold(true).
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
		lines = append(lines, st.dim.Render(
			fmt.Sprintf("  %s %s  %s", icon, name, formatBytes(a.Size))))
	}
	return append(lines, "")
}

// sidebarGitLines shows the current branch and the working-tree diff counts.
func sidebarGitLines(in SidebarRenderInput, innerW int, st sidebarStyles) []string {
	if in.GitBranch == "" {
		return nil
	}
	// Line 1: "Git" heading + "+N -M" counts.
	gitLine := st.heading.Render("  Git")
	if in.DiffAdded > 0 || in.DiffRemoved > 0 {
		gitLine += " " +
			st.green.Render(fmt.Sprintf("+%d", in.DiffAdded)) +
			st.dim.Render(" ") +
			st.red.Render(fmt.Sprintf("-%d", in.DiffRemoved))
	}

	// Line 2: branch with ⎇ prefix, truncated to fit inner width.
	// Prefix "  ⎇ " is 5 visible cells, plus one spare cell at the right edge.
	branch := truncateLabel(in.GitBranch, max(innerW-5, 4)-1)
	return []string{gitLine, "  " + st.teal.Render("⎇ "+branch), ""}
}

// sidebarModeLines renders the /run checklist while a run is active, and the
// plain mode indicator otherwise.
func sidebarModeLines(in SidebarRenderInput, innerW int, st sidebarStyles) []string {
	if len(in.RunChecklist) > 0 && in.RunPhase != "" {
		return append(sidebarRunLines(in, innerW, st), "")
	}

	lines := []string{st.heading.Render("  Mode")}
	mode := cmp.Or(in.Mode, "chat")
	if mode == "plan" {
		lines = append(lines, st.peach.Render("  [plan]"))
	} else {
		lines = append(lines, st.dim.Render("  ["+mode+"]"))
	}
	lines = append(lines, sidebarActivityLines(in, st)...)
	if len(in.PlanPhases) > 0 {
		// Every other section is separated by a blank row; the plan section
		// read as part of the Mode block without one. Both plan renderers close
		// with their own blank, so the section ends there.
		lines = append(lines, "")
		if in.mergePlanGraph && in.Graph != nil {
			return append(lines, sidebarPlanGraphLines(in, innerW, st)...)
		}
		return append(lines, sidebarPlanLines(in, innerW, st)...)
	}
	return append(lines, "")
}

// sidebarRunLines renders the spec name, cycle/phase and step checklist of an
// in-progress /run.
func sidebarRunLines(in SidebarRenderInput, innerW int, st sidebarStyles) []string {
	lines := []string{
		st.heading.Render(truncateLabel(fmt.Sprintf("  Run: %s", in.RunSpec), innerW+2)),
		st.peach.Render(fmt.Sprintf("  cycle %d/%d ∙ %s",
			in.RunCycle, in.RunMaxCycle, in.RunPhase)),
		"",
	}

	doneStyle := st.green
	todoStyle := st.overlay
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
		lines = append(lines, sidebarActivityLines(in, st)...)
	}
	return lines
}

// sidebarPlanGraphLines renders the plan section as a single view: the compiled
// SOP with each stage's status, headed by the progress the checklist used to
// carry.
//
// The checklist and the diagram were two vocabularies for one thing —
// "Requirements" and "clarify" are the same stage — stacked on top of each
// other, which cost about twenty of the sidebar's rows to say everything twice.
func sidebarPlanGraphLines(in SidebarRenderInput, innerW int, st sidebarStyles) []string {
	graph := sidebarGraphLines(in.Graph.Order, in.Graph.Edges, in.Graph.Status, innerW, st)
	if len(graph) == 0 {
		return nil
	}

	done := 0
	for _, p := range in.PlanPhases {
		if p.Done {
			done++
		}
	}
	progress := fmt.Sprintf("%d/%d", done, len(in.PlanPhases))
	pad := max(1, innerW-len("Plan")-len(progress))
	head := st.heading.Render("  Plan") + strings.Repeat(" ", pad) + st.dim.Render(progress)

	lines := make([]string, 0, len(graph)+3)
	lines = append(lines, head, "")
	lines = append(lines, graph...)
	return append(lines, "")
}

// sidebarPlanLines renders the PDD phase checklist for plan mode. It returns nil
// (hidden) when no PlanPhases are present. The current phase is the first with
// Done == false and is marked with ▶; done phases show [x] in green, future
// phases show [ ] in overlay.
func sidebarPlanLines(in SidebarRenderInput, innerW int, st sidebarStyles) []string {
	if len(in.PlanPhases) == 0 {
		return nil
	}
	lines := []string{st.heading.Render("  Plan")}
	current := true
	for _, p := range in.PlanPhases {
		title := truncateLabel(p.Name, max(innerW-5, 10)) // room for "  [x] " prefix
		switch {
		case p.Done:
			lines = append(lines, st.green.Render("  [x] "+title))
		case current:
			lines = append(lines, st.peach.Render("  ▶ "+title))
			current = false
		default:
			lines = append(lines, st.overlay.Render("  [ ] "+title))
		}
	}
	return append(lines, "")
}

// sidebarActivityLines shows the running tool, or a thinking indicator when the
// agent is busy without a named tool. Empty when idle.
func sidebarActivityLines(in SidebarRenderInput, st sidebarStyles) []string {
	if !in.Running {
		return nil
	}
	if in.ActiveTool != "" {
		return []string{st.dim.Render("  ⚡ " + in.ActiveTool)}
	}
	return []string{st.dim.Render("  thinking...")}
}

// sidebarAgentLines lists spawned subagents, running ones first.
func sidebarAgentLines(in SidebarRenderInput, innerW int, st sidebarStyles) []string {
	if in.Orchestrator == nil {
		return nil
	}
	agents := in.Orchestrator.List()
	if len(agents) == 0 {
		return nil
	}
	sortAgentsForDisplay(agents)

	lines := []string{agentHeadingStyle(agents, st).Render(fmt.Sprintf("  Agents [%d]", len(agents)))}
	names := agentDisplayNames(agents, innerW)
	for i, a := range agents {
		lines = append(lines, agentRow(a.Status, names[i], st))
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
func agentHeadingStyle(agents []subagent.AgentStatus, st sidebarStyles) lipgloss.Style {
	var running, failed int
	for _, a := range agents {
		switch a.Status {
		case "running":
			running++
		case "failed":
			failed++
		}
	}
	style := st.green
	if running > 0 {
		style = st.peach
	}
	if failed > 0 {
		style = st.red
	}
	return style.Bold(true)
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
func agentRow(status, name string, st sidebarStyles) string {
	switch status {
	case "running":
		return st.peach.Render("  ⚡ " + name)
	case "done":
		return st.green.Render("  ✓ " + name)
	case "failed":
		return st.red.Render("  ✗ " + name)
	case "killed":
		return st.overlay.Render("  ⊘ " + name)
	default:
		return st.dim.Render("  ∙ " + name)
	}
}

// sidebarSkillLines shows how many skills are loaded.
func sidebarSkillLines(in SidebarRenderInput, st sidebarStyles) []string {
	if len(in.Skills) == 0 {
		return nil
	}
	return []string{st.yellow.Bold(true).
		Render(fmt.Sprintf("  Skills [%d]", len(in.Skills))), ""}
}

// sidebarMemoryLines summarizes memory palace state: drawers, model readiness,
// knowledge-graph entities and rooms.
func sidebarMemoryLines(in SidebarRenderInput, st sidebarStyles) []string {
	if in.MemoryStatus == nil {
		return nil
	}
	lines := []string{st.pink.Bold(true).
		Render(fmt.Sprintf("  Memory [%d]", in.MemoryStatus.DrawerCount))}
	if in.MemoryStatus.ModelLoaded {
		lines = append(lines, st.dim.Render("  ⬡ model ready"))
	}
	if in.MemoryStatus.KG != nil {
		lines = append(lines, st.dim.Render(
			fmt.Sprintf("  ⬡ %d entities", in.MemoryStatus.KG.EntityCount)))
	}
	lines = append(lines, st.dim.Render(fmt.Sprintf("  ⬡ %d rooms", in.MemoryStatus.RoomCount)))
	return append(lines, "")
}

// sidebarMCPLines counts MCP tools per server rather than listing every tool,
// keeping the section a fixed handful of rows.
func sidebarMCPLines(in SidebarRenderInput, innerW int, st sidebarStyles) []string {
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

	lines := []string{st.mauve.Bold(true).
		Render(fmt.Sprintf("  MCP Tools [%d]", len(in.MCPTools)))}
	for _, srv := range seenOrder {
		countLabel := fmt.Sprintf(" [%d]", toolCounts[srv])
		srvLabel := truncateLabel(srv, max(innerW-4-len(countLabel), 1))
		lines = append(lines, st.dim.Render("  ⬡ "+srvLabel+countLabel))
	}
	return append(lines, "")
}

// sidebarLoadingLines tracks startup subsystems, ticking each off as it loads.
func sidebarLoadingLines(in SidebarRenderInput, st sidebarStyles) []string {
	if in.LoadingItems == nil {
		return nil
	}
	lines := []string{st.heading.Render("  Loading")}
	for _, name := range sortedKeys(in.LoadingItems) {
		if in.LoadingItems[name] {
			lines = append(lines, st.green.Render("  ✓ "+name))
			continue
		}
		lines = append(lines, st.peach.Render("  ◌ "+name+"..."))
	}
	return append(lines, "")
}

// sidebarFrame pads the section lines to fill the panel height, appends the
// token status line and matrix rain when active, closes with the rule, and
// boxes the result at a fixed width.
func sidebarFrame(in SidebarRenderInput, lines []string, w int, st sidebarStyles) string {
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
		contentLines = append(contentLines, st.dim.Render("  ∙∙∙"))
	}
	if len(contentLines) > targetH {
		contentLines = contentLines[:targetH]
	}

	if hasMatrix {
		statusText := cmp.Or(in.StatusLine, "──── tokens ────")
		if maxStatusW := w - 4; runewidth.StringWidth(statusText) > maxStatusW {
			statusText = runewidth.Truncate(statusText, maxStatusW-1, "─")
		}
		contentLines = append(contentLines, st.dim.Render(statusText))
		contentLines = append(contentLines, strings.Split(in.MatrixLines, "\n")...)
	}
	if ruleH > 0 {
		contentLines = append(contentLines,
			st.surface.Render(strings.Repeat("─", w)))
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
		Background(st.bg)
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
