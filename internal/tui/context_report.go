package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/subagent"
)

// The /context report is a linear accumulator: eight independent sections, each
// self-contained, appended in a fixed order. Written as one function it reached
// a cyclomatic complexity of 37 and fetched m.cfg.TokenTracker five separate
// times, each nil-check re-deriving values the previous one had already read.
//
// Splitting it per section buys two things beyond the number. The tracker is
// resolved once, at the top, and passed down — so "is there a tracker" is
// answered in one place instead of five. And each section is reachable from a
// test without building a whole model, which is what testdata/context_report_*
// .golden depend on to pin this output character for character.

// formatContextUsage builds a context usage display similar to Claude Code's /context.
func (m *model) formatContextUsage() string {
	var b strings.Builder

	// One lookup for the whole report. Every section that needs the tracker
	// takes it as an argument, so a nil tracker is a nil argument rather than a
	// re-check, and the sections stay callable from a test with a stub.
	tt := m.cfg.TokenTracker
	est := estimateConversation(m.chatModel.Messages)

	m.writeContextBreakdown(&b, tt)
	m.writeUsageHeadline(&b, tt, est)
	writeUsageByCategory(&b, m.chatModel.Messages, est)
	writeDailyTokenUsage(&b, tt)

	// The window and cache sections both describe the last response, so they
	// share one gate: a cache reading printed without the window reading beside
	// it would look like it came from somewhere else.
	if tt != nil && tt.LastPromptTokens() > 0 {
		writeContextWindow(&b, tt)
		writePromptCache(&b, tt)
	}

	writeSkillsSection(&b, m.cfg.Skills)
	writeSubagentsSection(&b, m.cfg.Orchestrator)
	writeCompactionSection(&b, m.cfg.CompactMetrics)

	return b.String()
}

// contextEstimate is the ~4-characters-per-token view of the conversation,
// split by the role that produced each message. It is the same estimate the
// status bar shows, and it is computed once because two sections read from it.
type contextEstimate struct {
	user      int64
	assistant int64
	tool      int64
	total     int64
}

// estimateConversation sums message text by role.
//
// Anything that is neither user nor assistant counts toward tool output, which
// is why the per-role message *counts* in the listing still come from
// countByRole rather than from a counter kept here: a message with some other
// role must contribute its characters without being reported as a tool call.
func estimateConversation(msgs []message) contextEstimate {
	userChars, assistantChars, toolChars := 0, 0, 0
	for _, msg := range msgs {
		size := messageChars(msg)
		switch msg.role {
		case "user":
			userChars += size
		case "assistant":
			assistantChars += size
		default: // tool
			toolChars += size
		}
	}
	// The total is derived from the character sum, not from adding the three
	// per-role token counts: estimateTokens truncates, so summing afterwards
	// would lose up to three tokens against the number the status bar shows.
	return contextEstimate{
		user:      estimateTokens(userChars),
		assistant: estimateTokens(assistantChars),
		tool:      estimateTokens(toolChars),
		total:     estimateTokens(userChars + assistantChars + toolChars),
	}
}

// writeContextBreakdown emits the segmented view of what is filling the window.
// It answers "what should I trim", which is the question a user asks before
// deciding anything else, so it leads the report.
func (m *model) writeContextBreakdown(b *strings.Builder, tt TokenTracker) {
	bd := m.cfg.ContextBreakdown
	if bd == nil {
		return
	}

	used, window := int64(0), int64(0)
	if tt != nil {
		used = tt.LastPromptTokens()
		window = tt.ContextWindowSize()
	}
	// Before the first response no provider has reported a prompt size, so fall
	// back to the char estimate plus the overhead measured at startup.
	if used == 0 {
		used = estimateContextTokenCount(m.chatModel.Messages) + bd.FixedTotal()
	}
	if window <= 0 {
		window = autoRangeWindow(used)
	}

	b.WriteString("*Context usage*\n\n")
	b.WriteString(RenderContextBreakdown(
		bd.withConversationFrom(used), window, min(m.chatWidth()-4, 64), m.palette))
	b.WriteString("\n\n")
}

// writeUsageHeadline emits the daily-budget bar and the model line beside it.
func (m *model) writeUsageHeadline(b *strings.Builder, tt TokenTracker, est contextEstimate) {
	const barLen = 20

	var usedBlocks int
	var limitTokens int64
	switch {
	case tt != nil && tt.Limit() > 0:
		limitTokens = tt.Limit()
		usedBlocks = barFill(float64(tt.TotalUsed())/float64(limitTokens), barLen)
	case est.total > 0:
		// No limit to measure against — show the context against a nominal 100k
		// window instead, and never less than one block, so a short conversation
		// still reads as "something is in here".
		usedBlocks = 1
		if est.total > 10000 {
			usedBlocks = max(barFill(float64(est.total)/100000, barLen), 1)
		}
	}
	bar := barGlyphs(usedBlocks, barLen)

	modelLabel := m.cfg.ModelName
	if m.cfg.ProviderName != "" {
		modelLabel = m.cfg.ProviderName + " | " + modelLabel
	}

	b.WriteString("**Context Usage**\n\n")
	if limitTokens > 0 {
		fmt.Fprintf(b, "`%s`  %s · %s/%s tokens (%.0f%%)\n\n",
			bar, modelLabel,
			formatTokenCount(tt.TotalUsed()), formatTokenCount(limitTokens), tt.PercentUsed())
		return
	}
	fmt.Fprintf(b, "`%s`  %s · ctx ~%s tokens\n\n",
		bar, modelLabel, formatTokenCount(est.total))
}

// writeUsageByCategory breaks the char estimate down by who produced the text.
func writeUsageByCategory(b *strings.Builder, msgs []message, est contextEstimate) {
	b.WriteString("*Estimated usage by category*\n")
	fmt.Fprintf(b, "- **User messages**: ~%s tokens (%d msgs)\n",
		formatTokenCount(est.user), countByRole(msgs, "user"))
	fmt.Fprintf(b, "- **Assistant messages**: ~%s tokens (%d msgs)\n",
		formatTokenCount(est.assistant), countByRole(msgs, "assistant"))
	fmt.Fprintf(b, "- **Tool calls**: ~%s tokens (%d calls)\n",
		formatTokenCount(est.tool), countByRole(msgs, "tool"))
	fmt.Fprintf(b, "- **Total context**: ~%s tokens (%d messages)\n",
		formatTokenCount(est.total), len(msgs))
}

// writeDailyTokenUsage reports what the tracker actually recorded today, as
// opposed to the estimate above it. Silent until something has been spent —
// "0 tokens consumed" is noise on a session that has not started.
func writeDailyTokenUsage(b *strings.Builder, tt TokenTracker) {
	if tt == nil {
		return
	}
	total := tt.TotalUsed()
	if total <= 0 {
		return
	}

	b.WriteString("\n*Daily token usage*\n")
	fmt.Fprintf(b, "- **Consumed today**: %s tokens\n", formatTokenCount(total))
	if tt.Limit() > 0 {
		fmt.Fprintf(b, "- **Remaining**: %s tokens\n", formatTokenCount(tt.Remaining()))
	}
}

// writeContextWindow reports the last LLM response's prompt size against the
// model's window. Callers gate on LastPromptTokens() > 0.
func writeContextWindow(b *strings.Builder, tt TokenTracker) {
	promptTokens := tt.LastPromptTokens()
	ctxWindow := tt.ContextWindowSize()

	b.WriteString("\n*Context window*\n")
	if ctxWindow <= 0 {
		fmt.Fprintf(b, "- **Last prompt**: %s tokens (window size unknown)\n",
			formatTokenCount(promptTokens))
		return
	}

	pct := tt.ContextPercentUsed()
	freeTokens := max(ctxWindow-promptTokens, 0)

	const ctxBarLen = 20
	ctxBar := barGlyphs(barFill(pct/100, ctxBarLen), ctxBarLen)

	fmt.Fprintf(b, "`%s`  %s / %s (%.0f%%)\n",
		ctxBar,
		formatTokenCount(promptTokens), formatTokenCount(ctxWindow), pct)
	fmt.Fprintf(b, "- **Used**: %s tokens\n", formatTokenCount(promptTokens))
	fmt.Fprintf(b, "- **Free**: %s tokens (%.0f%%)\n",
		formatTokenCount(freeTokens), 100-pct)
}

// writePromptCache reports cache reuse. Reported explicitly even at zero hits —
// a silent section reads the same whether caching works or is entirely absent.
// Callers gate on LastPromptTokens() > 0, which is also what makes the
// percentage below safe to divide by.
func writePromptCache(b *strings.Builder, tt TokenTracker) {
	promptTokens := tt.LastPromptTokens()

	b.WriteString("\n*Prompt cache*\n")
	if cached := tt.LastCachedTokens(); cached > 0 {
		fmt.Fprintf(b, "- **Last request**: %s of %s prompt tokens cached (%.0f%%)\n",
			formatTokenCount(cached), formatTokenCount(promptTokens),
			float64(cached)/float64(promptTokens)*100)
	} else {
		b.WriteString("- **Last request**: no cache hit\n")
	}
	if today := tt.CachedTokensToday(); today > 0 {
		fmt.Fprintf(b, "- **Today**: %s tokens read from cache (%.0f%% of input)\n",
			formatTokenCount(today), tt.CacheHitRateToday())
	}
	if prefix := tt.CachePrefixTokens(); prefix > 0 {
		fmt.Fprintf(b, "- **Stable prefix**: %s tokens · **body since**: %s tokens\n",
			formatTokenCount(prefix), formatTokenCount(tt.BodyTokens()))
	}
}

// writeSkillsSection lists the loaded skills and what each costs.
func writeSkillsSection(b *strings.Builder, skills []extension.Skill) {
	if len(skills) == 0 {
		return
	}

	b.WriteString("\n*Skills* ")
	fmt.Fprintf(b, "(%d loaded)\n", len(skills))

	// Stable, alphabetical listing for predictable /context output.
	names := make([]string, 0, len(skills))
	byName := make(map[string]extension.Skill, len(skills))
	for _, s := range skills {
		names = append(names, s.Name)
		byName[s.Name] = s
	}
	sort.Strings(names)

	for _, name := range names {
		s := byName[name]
		source := s.Source
		if source == "" {
			source = "user"
		}
		var bodyDesc string
		if size, ok := extension.SkillBodySize(skills, s.Name); ok {
			bodyDesc = fmt.Sprintf("body: %s", formatTokenCount(int64(size)))
		} else {
			bodyDesc = "body: not loaded"
		}
		desc := s.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(b, "- /%s — %s [%s]  %s\n", s.Name, desc, source, bodyDesc)
	}
}

// writeSubagentsSection summarizes spawned agents by outcome.
func writeSubagentsSection(b *strings.Builder, orch *subagent.Orchestrator) {
	if orch == nil {
		return
	}
	agents := orch.List()
	if len(agents) == 0 {
		return
	}

	running, done, failed := 0, 0, 0
	for _, a := range agents {
		switch a.Status {
		case "running":
			running++
		case "failed":
			failed++
		default:
			done++
		}
	}

	b.WriteString("\n*Subagents*\n")
	fmt.Fprintf(b, "- **Total**: %d (running: %d, done: %d, failed: %d)\n",
		len(agents), running, done, failed)
}

// writeCompactionSection appends the compactor's own stats block verbatim.
func writeCompactionSection(b *strings.Builder, cm CompactStatsProvider) {
	if cm == nil {
		return
	}
	stats := cm.FormatStats()
	if stats == "" {
		return
	}

	b.WriteString("\n*Output compaction*\n")
	b.WriteString(stats)
}
