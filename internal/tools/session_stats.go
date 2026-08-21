package tools

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
)

// SessionStatsInput defines the parameters for the session-stats tool.
type SessionStatsInput struct {
	// Number of hours to look back. Default: 24.
	Hours int `json:"hours,omitempty"`
	// Minimum tool calls to flag as high-usage. Default: 20.
	HighToolCalls int `json:"high_tool_calls,omitempty"`
	// Minimum turns to flag as excessive cycles. Default: 5.
	HighTurns int `json:"high_turns,omitempty"`
	// Show all sessions (not just anomalous ones). Default: false.
	All bool `json:"all,omitempty"`
	// Session directory override. Default: ~/.pi-go/sessions.
	SessionDir string `json:"session_dir,omitempty"`
}

// SessionStatsOutput contains the session analysis results.
type SessionStatsOutput struct {
	// Markdown-formatted report.
	Content string `json:"content"`
	// Total sessions scanned.
	TotalSessions int `json:"total_sessions"`
	// Sessions with anomalies.
	AnomalousSessions int `json:"anomalous_sessions"`
	// Runs ended by the agent loop guard.
	AbortedRuns int `json:"aborted_runs"`
	// Tokens recoverable by capping oversized results and dropping duplicates.
	ReclaimableTokens int `json:"reclaimable_tokens"`
	// Provider-reported prompt tokens across the window.
	PromptTokens int `json:"prompt_tokens"`
}

// sessionStat holds the computed stats for one session.
type sessionStat struct {
	ID          string
	Lines       int
	ToolCalls   int
	ToolResults int
	Turns       int
	Errors      int
	GitOps      int
	Duration    time.Duration
	FirstTime   time.Time
	LastTime    time.Time
	Anomalies   []string
}

// defaultHighGitOps is the threshold above which a session is flagged for
// excessive git operations. It is not currently user-configurable.
const defaultHighGitOps = 50

func newSessionStatsTool() (tool.Tool, error) {
	return newTool("session-stats",
		`Analyze session logs for anomalies: high tool call counts, excessive turns/cycles, errors, and git issues. Scans session directories and reports findings without LLM calls.

Required: none.
Optional: hours (lookback period, default 24), high_tool_calls (threshold, default 20), high_turns (threshold, default 5), all (show all sessions, default false), session_dir (override path).`,
		func(_ agent.Context, input SessionStatsInput) (SessionStatsOutput, error) {
			return sessionStatsHandler(input)
		})
}

// SessionStats runs the session analysis and returns the report. Exported so
// the CLI can reach the same code path the agent tool uses, rather than a
// second implementation that drifts.
func SessionStats(input SessionStatsInput) (SessionStatsOutput, error) {
	return sessionStatsHandler(input)
}

// sessionStatsSettings holds the resolved inputs for one scan: every optional
// field of SessionStatsInput filled in with its default.
type sessionStatsSettings struct {
	dir           string
	highToolCalls int
	highTurns     int
	hours         int
	cutoff        time.Time
}

// resolveSessionStatsSettings fills in a default for every unset input field.
// A non-positive threshold counts as unset, so a negative value from a model
// does not disable the check it belongs to.
func resolveSessionStatsSettings(input SessionStatsInput) (sessionStatsSettings, error) {
	settings := sessionStatsSettings{
		dir:           input.SessionDir,
		highToolCalls: input.HighToolCalls,
		highTurns:     input.HighTurns,
		hours:         input.Hours,
	}
	if settings.dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return sessionStatsSettings{}, fmt.Errorf("getting home dir: %w", err)
		}
		settings.dir = filepath.Join(home, ".pi-go", "sessions")
	}
	if settings.highToolCalls <= 0 {
		settings.highToolCalls = 20
	}
	if settings.highTurns <= 0 {
		settings.highTurns = 5
	}
	if settings.hours <= 0 {
		settings.hours = 24
	}
	settings.cutoff = time.Now().Add(-time.Duration(settings.hours) * time.Hour)
	return settings, nil
}

// collectSessionStats analyzes every session directory touched since the
// cutoff and returns the stats newest-first, alongside the sweep totals
// gathered from the same files. Entries that are not directories, the archive
// directory, and sessions with no readable events.jsonl are skipped.
func collectSessionStats(settings sessionStatsSettings) ([]sessionStat, *sweepTotals, error) {
	entries, err := os.ReadDir(settings.dir)
	if err != nil {
		return nil, nil, fmt.Errorf("reading sessions dir: %w", err)
	}

	var stats []sessionStat
	totals := newSweepTotals()
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "archive" {
			continue
		}
		eventsPath := filepath.Join(settings.dir, entry.Name(), "events.jsonl")
		info, err := os.Stat(eventsPath)
		if err != nil {
			continue
		}
		// Skip sessions older than cutoff.
		if info.ModTime().Before(settings.cutoff) {
			continue
		}

		stats = append(stats, analyzeSessionFile(entry.Name(), eventsPath, settings.highToolCalls, settings.highTurns))
		accumulateSweepFile(totals, eventsPath)
	}

	// Sort by time (newest first).
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].LastTime.After(stats[j].LastTime)
	})
	return stats, totals, nil
}

// countAnomalousSessions reports how many sessions carry at least one anomaly.
func countAnomalousSessions(stats []sessionStat) int {
	n := 0
	for _, stat := range stats {
		if len(stat.Anomalies) > 0 {
			n++
		}
	}
	return n
}

// writeSessionTable appends the summary table. Unless all is set, sessions
// without anomalies are counted in the header but omitted from the rows.
func writeSessionTable(b *strings.Builder, stats []sessionStat, all bool) {
	b.WriteString("| Session | Lines | Tool Calls | Turns | Errors | Duration | Anomalies |\n")
	b.WriteString("|---------|-------|------------|-------|--------|----------|-----------|\n")
	for _, s := range stats {
		if !all && len(s.Anomalies) == 0 {
			continue
		}
		dur := s.Duration.Truncate(time.Second)
		anomalyStr := strings.Join(s.Anomalies, ", ")
		if anomalyStr == "" {
			anomalyStr = "—"
		}
		fmt.Fprintf(b, "| %s | %d | %d | %d | %d | %s | %s |\n",
			s.ID, s.Lines, s.ToolCalls, s.Turns, s.Errors, dur, anomalyStr)
	}
}

// writeAnomalyDetails appends the per-session breakdown for anomalous
// sessions. Nothing at all is written — not even the heading — when every
// session looks normal.
func writeAnomalyDetails(b *strings.Builder, stats []sessionStat) {
	anomalous := func(s sessionStat) bool { return len(s.Anomalies) > 0 }
	if !slices.ContainsFunc(stats, anomalous) {
		return
	}

	b.WriteString("\n### Anomaly Details\n\n")
	for _, s := range stats {
		if !anomalous(s) {
			continue
		}
		fmt.Fprintf(b, "**%s**\n", s.ID)
		fmt.Fprintf(b, "- Lines: %d, Tool calls: %d, Turns: %d, Errors: %d\n",
			s.Lines, s.ToolCalls, s.Turns, s.Errors)
		fmt.Fprintf(b, "- Duration: %s\n", s.Duration.Truncate(time.Second))
		fmt.Fprintf(b, "- Anomalies: %s\n\n", strings.Join(s.Anomalies, "; "))
	}
}

func sessionStatsHandler(input SessionStatsInput) (SessionStatsOutput, error) {
	settings, err := resolveSessionStatsSettings(input)
	if err != nil {
		return SessionStatsOutput{}, err
	}

	stats, totals, err := collectSessionStats(settings)
	if err != nil {
		return SessionStatsOutput{}, err
	}

	var b strings.Builder
	totalAnomalous := countAnomalousSessions(stats)
	fmt.Fprintf(&b, "## Session Stats (last %d hours)\n\n", settings.hours)
	fmt.Fprintf(&b, "Scanned %d sessions, %d with anomalies.\n\n", len(stats), totalAnomalous)

	if len(stats) == 0 {
		b.WriteString("No sessions found in the specified time range.\n")
		return SessionStatsOutput{
			Content:           b.String(),
			TotalSessions:     0,
			AnomalousSessions: 0,
		}, nil
	}

	writeSessionTable(&b, stats, input.All)
	writeAnomalyDetails(&b, stats)

	// The per-session table above says which runs look odd. These sections say
	// what went wrong across all of them, what it cost, and whether the
	// recording pipelines are alive — the questions a nightly check exists for.
	renderSweep(&b, totals, len(stats))
	renderPipelineHealth(&b, len(stats), settings.cutoff)

	return SessionStatsOutput{
		Content:           b.String(),
		TotalSessions:     len(stats),
		AnomalousSessions: totalAnomalous,
		AbortedRuns:       totalAborts(totals),
		ReclaimableTokens: reclaimableTokens(totals),
		PromptTokens:      totals.PromptTokens,
	}, nil
}

// sessionEvent is the subset of an events.jsonl record the scan reads.
type sessionEvent struct {
	Content      any    `json:"Content"`
	Partial      bool   `json:"Partial"`
	TurnComplete bool   `json:"TurnComplete"`
	Interrupted  bool   `json:"Interrupted"`
	ErrorCode    string `json:"ErrorCode"`
	ErrorMessage string `json:"ErrorMessage"`
	FinishReason string `json:"FinishReason"`
	ID           string `json:"ID"`
	Timestamp    string `json:"Timestamp"`
	Author       string `json:"Author"`
}

// sessionCounters accumulates the tallies for one session file as its events
// are scanned. The zero value is ready to use.
type sessionCounters struct {
	lines       int
	toolCalls   int
	toolResults int
	turns       int
	errors      int
	gitOps      int
	firstTS     time.Time
	lastTS      time.Time
}

// analyzeSessionFile reads a single events.jsonl and computes stats.
func analyzeSessionFile(sessionID, path string, highToolCalls, highTurns int) sessionStat {
	f, err := os.Open(path)
	if err != nil {
		return sessionStat{ID: sessionID}
	}
	defer f.Close()

	var c sessionCounters
	scanner := bufio.NewScanner(f)
	// Increase scan buffer for potentially large JSON lines.
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	for scanner.Scan() {
		c.observe(scanner.Bytes())
	}

	stat := c.stat(sessionID)
	stat.Anomalies = sessionAnomalies(stat, highToolCalls, highTurns)
	return stat
}

// observe folds one events.jsonl line into the running tallies. A line that is
// not valid JSON still counts toward the line total, because the total
// describes the file rather than the events that parsed out of it.
func (c *sessionCounters) observe(line []byte) {
	c.lines++

	var ev sessionEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return
	}

	c.observeTimestamp(ev.Timestamp)
	c.observeParts(ev.Content)

	// Count turns: TurnComplete events from the model.
	if ev.TurnComplete && ev.Author != "user" {
		c.turns++
	}

	// Count errors: a single event is at most one error, even if both
	// ErrorCode/ErrorMessage and Interrupted are set.
	if ev.ErrorCode != "" || ev.ErrorMessage != "" || ev.Interrupted {
		c.errors++
	}
}

// observeTimestamp widens the session time range. Absent and unparseable
// timestamps are ignored rather than treated as the zero time.
func (c *sessionCounters) observeTimestamp(raw string) {
	if raw == "" {
		return
	}
	ts, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return
	}
	if c.firstTS.IsZero() || ts.Before(c.firstTS) {
		c.firstTS = ts
	}
	if ts.After(c.lastTS) {
		c.lastTS = ts
	}
}

// observeParts counts the functionCall and functionResponse parts of one
// event. Content is decoded as any because events.jsonl stores the wire form,
// where each part is a single-key object.
func (c *sessionCounters) observeParts(content any) {
	contentMap, ok := content.(map[string]any)
	if !ok {
		return
	}
	parts, ok := contentMap["parts"].([]any)
	if !ok {
		return
	}
	for _, p := range parts {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if fc, ok := pm["functionCall"].(map[string]any); ok {
			c.toolCalls++
			if isGitOperation(fc) {
				c.gitOps++
			}
		}
		if _, ok := pm["functionResponse"]; ok {
			c.toolResults++
		}
	}
}

// stat converts the tallies into a sessionStat. Duration stays zero unless
// both ends of the time range were seen.
func (c *sessionCounters) stat(sessionID string) sessionStat {
	stat := sessionStat{
		ID:          sessionID,
		Lines:       c.lines,
		ToolCalls:   c.toolCalls,
		ToolResults: c.toolResults,
		Turns:       c.turns,
		Errors:      c.errors,
		GitOps:      c.gitOps,
		FirstTime:   c.firstTS,
		LastTime:    c.lastTS,
	}
	if !c.firstTS.IsZero() && !c.lastTS.IsZero() {
		stat.Duration = c.lastTS.Sub(c.firstTS)
	}
	return stat
}

// sessionAnomalies lists the reasons a session is worth a second look. The
// order is the order the report prints them in; an empty result means normal.
func sessionAnomalies(stat sessionStat, highToolCalls, highTurns int) []string {
	var anomalies []string
	if stat.ToolCalls > highToolCalls {
		anomalies = append(anomalies, fmt.Sprintf("high tool calls (%d)", stat.ToolCalls))
	}
	if stat.Turns > highTurns {
		anomalies = append(anomalies, fmt.Sprintf("excessive turns (%d)", stat.Turns))
	}
	if stat.Errors > 0 {
		anomalies = append(anomalies, fmt.Sprintf("errors (%d)", stat.Errors))
	}
	if stat.GitOps > defaultHighGitOps {
		anomalies = append(anomalies, fmt.Sprintf("many git operations (%d)", stat.GitOps))
	}
	if stat.Duration > 30*time.Minute && stat.ToolCalls < 5 {
		anomalies = append(anomalies, "long idle session")
	}
	return anomalies
}

// isGitOperation reports whether a functionCall part represents a git
// operation. It recognizes dedicated git tools (git-overview, git-file-diff,
// git-hunk, any name with the "git-" prefix) and shell invocations of `git`.
func isGitOperation(fc map[string]any) bool {
	name, _ := fc["name"].(string)
	switch name {
	case "git-overview", "git-file-diff", "git-hunk":
		return true
	case "bash":
		args, ok := fc["args"].(map[string]any)
		if !ok {
			return false
		}
		cmd, ok := args["command"].(string)
		if !ok {
			return false
		}
		first := strings.TrimLeft(cmd, " \t")
		if i := strings.IndexAny(first, " \t"); i >= 0 {
			first = first[:i]
		}
		return first == "git"
	}
	return strings.HasPrefix(name, "git-")
}
