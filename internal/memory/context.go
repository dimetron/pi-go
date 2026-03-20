package memory

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// ContextGenerator builds markdown context from recent observations for system instruction injection.
type ContextGenerator struct {
	store       Store
	tokenBudget int
}

// contextSessionGroup holds observations and optional summary for one session.
type contextSessionGroup struct {
	sessionID string
	summary   *SessionSummary
	obs       []*Observation
}

// NewContextGenerator creates a ContextGenerator with the given store and token budget.
func NewContextGenerator(store Store, tokenBudget int) *ContextGenerator {
	if tokenBudget <= 0 {
		tokenBudget = 8000
	}
	return &ContextGenerator{store: store, tokenBudget: tokenBudget}
}

// Generate builds a markdown string of recent observations grouped by session.
// Returns empty string if there are no observations for the project.
func (g *ContextGenerator) Generate(ctx context.Context, project string) (string, error) {
	summaries, err := g.store.RecentSummaries(ctx, project, 3)
	if err != nil {
		return "", fmt.Errorf("memory: context summaries: %w", err)
	}

	observations, err := g.store.RecentObservations(ctx, project, 200)
	if err != nil {
		return "", fmt.Errorf("memory: context observations: %w", err)
	}

	// Filter observations to last 72 hours
	cutoff := time.Now().Add(-72 * time.Hour)
	var recent []*Observation
	for _, obs := range observations {
		if obs.CreatedAt.After(cutoff) {
			recent = append(recent, obs)
		}
	}

	if len(recent) == 0 && len(summaries) == 0 {
		return "", nil
	}

	var b strings.Builder
	tokens := 0

	projectName := filepath.Base(project)
	now := time.Now()
	header := fmt.Sprintf("# [%s] recent context, %s\n\n", projectName, now.Format("2006-01-02 3:04pm MST"))
	b.WriteString(header)
	tokens += estimateTokens(header)

	legend := "**Legend:** session-request | bugfix | feature | refactor | change | discovery\n\n"
	b.WriteString(legend)
	tokens += estimateTokens(legend)

	colKey := "**Column Key**:\n- **Read**: Tokens to read this observation (cost to learn it now)\n- **Work**: Tokens spent on work that produced this record\n\n"
	b.WriteString(colKey)
	tokens += estimateTokens(colKey)

	// Build session groups preserving order of first appearance
	groupMap := make(map[string]*contextSessionGroup)
	var groupOrder []string
	for _, obs := range recent {
		sg, ok := groupMap[obs.SessionID]
		if !ok {
			sg = &contextSessionGroup{sessionID: obs.SessionID}
			groupMap[obs.SessionID] = sg
			groupOrder = append(groupOrder, obs.SessionID)
		}
		sg.obs = append(sg.obs, obs)
	}

	// Attach summaries to groups
	for _, sum := range summaries {
		sg, ok := groupMap[sum.SessionID]
		if ok {
			sg.summary = sum
		}
	}

	// Render each session group
	for _, sid := range groupOrder {
		if tokens >= g.tokenBudget {
			break
		}

		sg := groupMap[sid]
		section := renderSessionGroup(sg)
		sectionTokens := estimateTokens(section)
		if tokens+sectionTokens > g.tokenBudget {
			break
		}
		b.WriteString(section)
		tokens += sectionTokens
	}

	footer := "\nAccess past observations with mem-search, mem-timeline, mem-get tools.\n"
	b.WriteString(footer)

	return b.String(), nil
}

// renderSessionGroup renders a single session's observations as markdown.
func renderSessionGroup(sg *contextSessionGroup) string {
	var b strings.Builder

	// Session header
	title := sg.sessionID
	if sg.summary != nil && sg.summary.Request != "" {
		title = sg.summary.Request
	}

	var sessionTime time.Time
	if len(sg.obs) > 0 {
		sessionTime = sg.obs[0].CreatedAt
	} else if sg.summary != nil {
		sessionTime = sg.summary.CreatedAt
	}

	b.WriteString(fmt.Sprintf("## Session: %s (%s)\n", title, formatSessionTime(sessionTime)))

	// Group observations by primary source file
	type fileGroup struct {
		file string
		obs  []*Observation
	}
	fileMap := make(map[string]*fileGroup)
	var fileOrder []string

	for _, obs := range sg.obs {
		file := "General"
		if len(obs.SourceFiles) > 0 {
			file = obs.SourceFiles[0]
		}
		fg, ok := fileMap[file]
		if !ok {
			fg = &fileGroup{file: file}
			fileMap[file] = fg
			fileOrder = append(fileOrder, file)
		}
		fg.obs = append(fg.obs, obs)
	}

	for _, file := range fileOrder {
		fg := fileMap[file]
		b.WriteString(fmt.Sprintf("\n**%s**\n", file))
		b.WriteString("| ID | Time | T | Title | Read | Work |\n")
		b.WriteString("|----|------|---|-------|------|------|\n")

		for _, obs := range fg.obs {
			b.WriteString(fmt.Sprintf("| #%d | %s | %s | %s | %d | %d |\n",
				obs.ID,
				obs.CreatedAt.Format("3:04 PM"),
				typeEmoji(obs.Type),
				obs.Title,
				estimateTokens(obs.Text),
				obs.DiscoveryTokens,
			))
		}
	}

	b.WriteString("\n")
	return b.String()
}

// typeEmoji returns a short symbol for the observation type.
func typeEmoji(t ObservationType) string {
	switch t {
	case TypeBugfix:
		return "bugfix"
	case TypeFeature:
		return "feature"
	case TypeRefactor:
		return "refactor"
	case TypeDiscovery:
		return "discovery"
	case TypeDecision:
		return "decision"
	case TypeChange:
		return "change"
	default:
		return string(t)
	}
}

// formatSessionTime formats a time for display in session headers.
func formatSessionTime(t time.Time) string {
	if t.IsZero() {
		return "unknown time"
	}
	return t.Format("Jan 2 at 3:04 PM")
}
