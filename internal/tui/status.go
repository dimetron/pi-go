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
	Pending      int          // prompts waiting behind the active response
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
	Palette      Palette         // resolved theme palette; zero = dark default
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
func renderContextBar(pct float64, bg color.Color, p Palette) string {
	pct = min(max(pct, 0), 100)

	filled := barFill(pct/100, contextBarWidth)
	empty := contextBarWidth - filled

	var fg color.Color
	switch {
	case pct >= 80:
		fg = p.Red
	case pct >= 60:
		fg = p.Peach
	default:
		fg = p.Green
	}

	filledStyle := lipgloss.NewStyle().Background(bg).Foreground(fg)
	emptyStyle := lipgloss.NewStyle().Background(bg).Foreground(p.Surface)
	pctStyle := lipgloss.NewStyle().Background(bg).Foreground(fg)

	return filledStyle.Render(strings.Repeat(barFilled, filled)) +
		emptyStyle.Render(strings.Repeat(barEmpty, empty)) +
		pctStyle.Render(fmt.Sprintf(" %.0f%%", pct))
}

// Render renders the status bar string.
func (s *StatusModel) Render(in StatusRenderInput) string {
	p := paletteOrDark(in.Palette)
	bar := lipgloss.NewStyle().Width(s.Width)
	sep := lipgloss.NewStyle().Foreground(p.Surface).Render("  │  ")

	parts := []string{s.modeField(in, p)}

	// Loading progress replaces the normal status content during init.
	if in.LoadingItems != nil {
		parts = append(parts, loadingField(in.LoadingItems, p))
		return bar.Render(strings.Join(parts, sep))
	}

	// Fields render themselves out, in bar order; "" means the field has
	// nothing to say for this input and takes no segment.
	for _, field := range []string{
		queueField(in.Pending, p),
		contextField(in, p),
		tokenField(in.TokenTracker, p),
		locationField(in, p),
		s.toolsField(p),
		runCycleField(in.RunCycle, p),
	} {
		if field != "" {
			parts = append(parts, field)
		}
	}

	return bar.Render(strings.Join(parts, sep))
}

// modeField renders the bracketed field: [chat], [plan], the spinner verb while
// running — and a flash when there is one.
//
// A flash takes this slot over rather than adding a segment of its own. It is
// already the fixed-width, bracketed field the eye returns to, so a notice
// lands where the user is looking and the bar's geometry does not shift. The
// mode is not lost by borrowing it for three seconds: it has not changed, and
// it comes straight back.
func (s *StatusModel) modeField(in StatusRenderInput, p Palette) string {
	mode := in.Mode
	if mode == "" {
		mode = "chat"
	}
	switch {
	case in.Flash != "":
		flashStyle := lipgloss.NewStyle().Foreground(p.Green).Bold(true)
		return flashStyle.Render(fmt.Sprintf(" [%s]", paddedStatusMode(in.Flash)))
	case mode == "plan":
		modeStyle := lipgloss.NewStyle().Foreground(p.Peach)
		return modeStyle.Render(fmt.Sprintf(" [%s]", paddedStatusMode(mode)))
	case in.Running && s.ActiveTool == "":
		// spinnerVerb picks its verb at random, so this branch alone makes the
		// status bar — and with it View — render differently for identical model
		// state. See matrixState.tick for the other source and for what the pair
		// of them rules out.
		verbStyle := lipgloss.NewStyle().Foreground(p.Blue)
		return verbStyle.Render(fmt.Sprintf(" [%s]", spinnerVerb()))
	default:
		verbStyle := lipgloss.NewStyle().Foreground(p.Blue)
		return verbStyle.Render(fmt.Sprintf(" [%s]", paddedStatusMode(mode)))
	}
}

// loadingField lists the startup subsystems, ticked as each finishes.
func loadingField(items map[string]bool, p Palette) string {
	dim := lipgloss.NewStyle().Foreground(p.Dim)

	var rendered []string
	for _, name := range sortedKeys(items) {
		if items[name] {
			okStyle := lipgloss.NewStyle().Foreground(p.Green)
			rendered = append(rendered, okStyle.Render(name+" ✓"))
		} else {
			loadStyle := lipgloss.NewStyle().Foreground(p.Peach)
			rendered = append(rendered, loadStyle.Render(name+"..."))
		}
	}
	return dim.Render("load: ") + strings.Join(rendered, dim.Render(" "))
}

// queueField counts the prompts waiting behind the active response. It is shown
// even while that response is streaming, so the user can see Enter was accepted
// without waiting for the turn to end.
func queueField(pending int, p Palette) string {
	if pending <= 0 {
		return ""
	}
	queueStyle := lipgloss.NewStyle().Foreground(p.Peach)
	return queueStyle.Render(fmt.Sprintf("queued: %d", pending))
}

// contextField renders the color-coded context bar, or — when no tracker has
// reported a limit yet — the same rough estimate /context shows, so the two
// cannot disagree about the same conversation.
func contextField(in StatusRenderInput, p Palette) string {
	if tt := in.TokenTracker; tt != nil && tt.Limit() > 0 {
		return renderContextBar(tt.PercentUsed(), p.Transparent, p)
	}
	dim := lipgloss.NewStyle().Foreground(p.Dim)
	ctxTokens := estimateContextTokenCount(in.Messages)
	if ctxTokens >= 1000 {
		return dim.Render(fmt.Sprintf("ctx: %.1fk", float64(ctxTokens)/1000))
	}
	return dim.Render(fmt.Sprintf("ctx: %d", ctxTokens))
}

// tokenField renders the numeric token usage, colored by how close to the limit
// it is. Without a limit it reports the running total alone.
func tokenField(tt TokenTracker, p Palette) string {
	if tt == nil {
		return ""
	}
	dim := lipgloss.NewStyle().Foreground(p.Dim)
	total := tt.TotalUsed()
	limit := tt.Limit()
	if limit <= 0 {
		if total <= 0 {
			return ""
		}
		return dim.Render(fmt.Sprintf("tkn: %s", formatTokenCount(total)))
	}

	tokenStyle := dim
	switch pct := tt.PercentUsed(); {
	case pct >= 100:
		tokenStyle = lipgloss.NewStyle().Foreground(p.Red)
	case pct >= 80:
		tokenStyle = lipgloss.NewStyle().Foreground(p.Peach)
	}
	return tokenStyle.Render(fmt.Sprintf("tkn: %s/%s",
		formatTokenCount(total), formatTokenCount(limit)))
}

// locationField renders "directory | host", dropping whichever half is unknown.
func locationField(in StatusRenderInput, p Palette) string {
	if in.FolderName == "" && in.HostName == "" {
		return ""
	}
	dirStyle := lipgloss.NewStyle().Foreground(p.Mauve)
	hostStyle := lipgloss.NewStyle().Foreground(p.Sky)
	dim := lipgloss.NewStyle().Foreground(p.Dim)

	var locationParts []string
	if in.FolderName != "" {
		locationParts = append(locationParts, dirStyle.Render(in.FolderName))
	}
	if in.HostName != "" {
		locationParts = append(locationParts, hostStyle.Render(in.HostName))
	}
	return strings.Join(locationParts, dim.Render(" | "))
}

// toolsField names the tools currently executing: the parallel set when there
// is more than one, otherwise the single tool with how long it has been going.
func (s *StatusModel) toolsField(p Palette) string {
	toolStyle := lipgloss.NewStyle().Foreground(p.Sapphire)
	if len(s.ActiveTools) > 1 {
		toolNames := make([]string, 0, len(s.ActiveTools))
		for name := range s.ActiveTools {
			toolNames = append(toolNames, name)
		}
		sort.Strings(toolNames)
		return toolStyle.Render(fmt.Sprintf("tools[%d]: %s", len(toolNames), strings.Join(toolNames, ", ")))
	}
	if s.ActiveTool == "" {
		return ""
	}
	elapsed := time.Since(s.ToolStart).Truncate(time.Millisecond)
	return toolStyle.Render(fmt.Sprintf("tool: %s (%s)", s.ActiveTool, elapsed))
}

// runCycleField renders the /run cycle indicator. The spec name is omitted: it
// is long enough to wrap the status bar onto a second line, and it is already
// shown in the sidebar.
func runCycleField(cycle *runCycleInfo, p Palette) string {
	if cycle == nil {
		return ""
	}
	runStyle := lipgloss.NewStyle().Foreground(p.Peach)
	return runStyle.Render(fmt.Sprintf("cycle %d/%d", cycle.Cycle, cycle.MaxRetries))
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
