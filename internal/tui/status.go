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
//
// The bar is a list of fields joined by a separator, and each statusXxxField
// helper below owns exactly one of them. A helper returns nil when its field is
// not shown this frame, so appending with "..." keeps the separator bookkeeping
// here and out of every field.
func (s *StatusModel) Render(in StatusRenderInput) string {
	p := paletteOrDark(in.Palette)

	dim := lipgloss.NewStyle().Foreground(p.Dim)
	bar := lipgloss.NewStyle().Width(s.Width)

	sepStyle := lipgloss.NewStyle().Foreground(p.Surface)
	sep := sepStyle.Render("  │  ")

	parts := []string{s.statusBracketField(in, p)}

	// Loading progress (replaces normal status content during init).
	if in.LoadingItems != nil {
		parts = append(parts, statusLoadingField(in.LoadingItems, dim, p))
		return bar.Render(strings.Join(parts, sep))
	}

	parts = append(parts, statusQueuedField(in, p)...)
	parts = append(parts, statusContextField(in, dim, p))
	parts = append(parts, statusTokenField(in, dim, p)...)
	parts = append(parts, statusLocationField(in, dim, p)...)
	parts = append(parts, s.statusToolField(p)...)
	parts = append(parts, statusRunCycleField(in, p)...)

	return bar.Render(strings.Join(parts, sep))
}

// statusBracketField renders the bracketed field: [chat], [plan], the spinner
// verb while running — and a flash when there is one.
//
// A flash takes this slot over rather than adding a segment of its own. It is
// already the fixed-width, bracketed field the eye returns to, so a notice
// lands where the user is looking and the bar's geometry does not shift. The
// mode is not lost by borrowing it for three seconds: it has not changed, and
// it comes straight back.
func (s *StatusModel) statusBracketField(in StatusRenderInput, p Palette) string {
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
		verbStyle := lipgloss.NewStyle().Foreground(p.Blue)
		return verbStyle.Render(fmt.Sprintf(" [%s]", spinnerVerb()))
	default:
		verbStyle := lipgloss.NewStyle().Foreground(p.Blue)
		return verbStyle.Render(fmt.Sprintf(" [%s]", paddedStatusMode(mode)))
	}
}

// statusLoadingField renders the init progress list: each item green with a
// tick once done, orange with an ellipsis while still loading.
func statusLoadingField(loading map[string]bool, dim lipgloss.Style, p Palette) string {
	var items []string
	for _, name := range sortedKeys(loading) {
		if loading[name] {
			okStyle := lipgloss.NewStyle().Foreground(p.Green)
			items = append(items, okStyle.Render(name+" \u2713"))
		} else {
			loadStyle := lipgloss.NewStyle().Foreground(p.Peach)
			items = append(items, loadStyle.Render(name+"..."))
		}
	}
	return dim.Render("load: ") + strings.Join(items, dim.Render(" "))
}

// statusQueuedField renders the queued-prompt count. Pending prompts are shown
// even while the active response is streaming, so the user can see that Enter
// was accepted without waiting for it.
func statusQueuedField(in StatusRenderInput, p Palette) []string {
	if in.Pending <= 0 {
		return nil
	}
	queueStyle := lipgloss.NewStyle().Foreground(p.Peach)
	return []string{queueStyle.Render(fmt.Sprintf("queued: %d", in.Pending))}
}

// statusContextField renders the context % bar (visual bar with color coding),
// or — with no tracker limit to scale against — the same rough estimate
// /context shows, so the two cannot disagree about the same conversation.
func statusContextField(in StatusRenderInput, dim lipgloss.Style, p Palette) string {
	if tt := in.TokenTracker; tt != nil && tt.Limit() > 0 {
		noBg := p.Transparent
		return renderContextBar(tt.PercentUsed(), noBg, p)
	}
	ctxTokens := estimateContextTokenCount(in.Messages)
	if ctxTokens >= 1000 {
		return dim.Render(fmt.Sprintf("ctx: %.1fk", float64(ctxTokens)/1000))
	}
	return dim.Render(fmt.Sprintf("ctx: %d", ctxTokens))
}

// statusTokenField renders the numeric token tally, colored by how close the
// session is to its limit.
func statusTokenField(in StatusRenderInput, dim lipgloss.Style, p Palette) []string {
	tt := in.TokenTracker
	if tt == nil {
		return nil
	}
	total := tt.TotalUsed()
	limit := tt.Limit()
	if limit <= 0 {
		if total <= 0 {
			return nil
		}
		return []string{dim.Render(fmt.Sprintf("tkn: %s", formatTokenCount(total)))}
	}

	var tokenStyle lipgloss.Style
	switch pct := tt.PercentUsed(); {
	case pct >= 100:
		tokenStyle = lipgloss.NewStyle().Foreground(p.Red)
	case pct >= 80:
		tokenStyle = lipgloss.NewStyle().Foreground(p.Peach)
	default:
		tokenStyle = dim
	}
	return []string{tokenStyle.Render(fmt.Sprintf("tkn: %s/%s",
		formatTokenCount(total), formatTokenCount(limit)))}
}

// statusLocationField renders "directory | host".
func statusLocationField(in StatusRenderInput, dim lipgloss.Style, p Palette) []string {
	if in.FolderName == "" && in.HostName == "" {
		return nil
	}
	dirStyle := lipgloss.NewStyle().Foreground(p.Mauve)
	hostStyle := lipgloss.NewStyle().Foreground(p.Sky)

	var locationParts []string
	if in.FolderName != "" {
		locationParts = append(locationParts, dirStyle.Render(in.FolderName))
	}
	if in.HostName != "" {
		locationParts = append(locationParts, hostStyle.Render(in.HostName))
	}
	return []string{strings.Join(locationParts, dim.Render(" | "))}
}

// statusToolField renders the parallel-tool list, or the single active tool
// with how long it has been running.
func (s *StatusModel) statusToolField(p Palette) []string {
	toolStyle := lipgloss.NewStyle().Foreground(p.Sapphire)
	if len(s.ActiveTools) > 1 {
		var toolNames []string
		for name := range s.ActiveTools {
			toolNames = append(toolNames, name)
		}
		sort.Strings(toolNames)
		return []string{toolStyle.Render(fmt.Sprintf("tools[%d]: %s", len(toolNames), strings.Join(toolNames, ", ")))}
	}
	if s.ActiveTool != "" {
		elapsed := time.Since(s.ToolStart).Truncate(time.Millisecond)
		return []string{toolStyle.Render(fmt.Sprintf("tool: %s (%s)", s.ActiveTool, elapsed))}
	}
	return nil
}

// statusRunCycleField renders the /run cycle indicator. The spec name is
// omitted: it is long enough to wrap the status bar onto a second line, and it
// is already shown in the sidebar.
func statusRunCycleField(in StatusRenderInput, p Palette) []string {
	if in.RunCycle == nil {
		return nil
	}
	runStyle := lipgloss.NewStyle().Foreground(p.Peach)
	return []string{runStyle.Render(fmt.Sprintf("cycle %d/%d",
		in.RunCycle.Cycle, in.RunCycle.MaxRetries))}
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
