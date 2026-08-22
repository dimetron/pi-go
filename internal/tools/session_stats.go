package tools

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// sessionStatsOptions is SessionStatsInput with the defaults applied and the
// lookback window turned into a cutoff time.
type sessionStatsOptions struct {
	sessionDir    string
	highToolCalls int
	highTurns     int
	hours         int
	cutoff        time.Time
}

func resolveSessionStatsOptions(input SessionStatsInput) (sessionStatsOptions, error) {
	opts := sessionStatsOptions{
		sessionDir:    input.SessionDir,
		highToolCalls: input.HighToolCalls,
		highTurns:     input.HighTurns,
		hours:         input.Hours,
	}

	// Resolve session directory.
	if opts.sessionDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return sessionStatsOptions{}, fmt.Errorf("getting home dir: %w", err)
		}
		opts.sessionDir = filepath.Join(home, ".pi-go", "sessions")
	}

	// Resolve thresholds.
	if opts.highToolCalls <= 0 {
		opts.highToolCalls = 20
	}
	if opts.highTurns <= 0 {
		opts.highTurns = 5
	}

	// Resolve lookback.
	if opts.hours <= 0 {
		opts.hours = 24
	}
	opts.cutoff = time.Now().Add(-time.Duration(opts.hours) * time.Hour)
	return opts, nil
}

// scanSessions analyzes every session directory whose events file was touched
// since the cutoff, returning the per-session stats and the cross-session
// aggregate the deep sections are built from.
func scanSessions(opts sessionStatsOptions) ([]sessionStat, *sweepTotals, error) {
	entries, err := os.ReadDir(opts.sessionDir)
	if err != nil {
		return nil, nil, fmt.Errorf("reading sessions dir: %w", err)
	}

	var stats []sessionStat
	totals := newSweepTotals()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Skip archive directory.
		if entry.Name() == "archive" {
			continue
		}
		eventsPath := filepath.Join(opts.sessionDir, entry.Name(), "events.jsonl")
		info, err := os.Stat(eventsPath)
		if err != nil {
			continue
		}
		// Skip sessions older than cutoff.
		if info.ModTime().Before(opts.cutoff) {
			continue
		}

		stats = append(stats, analyzeSessionFile(entry.Name(), eventsPath, opts.highToolCalls, opts.highTurns))
		accumulateSweepFile(totals, eventsPath)
	}
	return stats, totals, nil
}

func sessionStatsHandler(input SessionStatsInput) (SessionStatsOutput, error) {
	opts, err := resolveSessionStatsOptions(input)
	if err != nil {
		return SessionStatsOutput{}, err
	}

	stats, totals, err := scanSessions(opts)
	if err != nil {
		return SessionStatsOutput{}, err
	}

	// Sort by time (newest first).
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].LastTime.After(stats[j].LastTime)
	})

	// Build report.
	var b strings.Builder
	totalAnomalous := 0
	for _, s := range stats {
		if len(s.Anomalies) > 0 {
			totalAnomalous++
		}
	}

	fmt.Fprintf(&b, "## Session Stats (last %d hours)\n\n", opts.hours)
	fmt.Fprintf(&b, "Scanned %d sessions, %d with anomalies.\n\n", len(stats), totalAnomalous)

	if len(stats) == 0 {
		b.WriteString("No sessions found in the specified time range.\n")
		return SessionStatsOutput{
			Content:           b.String(),
			TotalSessions:     0,
			AnomalousSessions: 0,
		}, nil
	}

	renderSessionTable(&b, stats, input.All)
	renderAnomalyDetails(&b, stats)

	// The per-session table above says which runs look odd. These sections say
	// what went wrong across all of them, what it cost, and whether the
	// recording pipelines are alive — the questions a nightly check exists for.
	renderSweep(&b, totals, len(stats))
	renderPipelineHealth(&b, len(stats), opts.cutoff)

	return SessionStatsOutput{
		Content:           b.String(),
		TotalSessions:     len(stats),
		AnomalousSessions: totalAnomalous,
		AbortedRuns:       totalAborts(totals),
		ReclaimableTokens: reclaimableTokens(totals),
		PromptTokens:      totals.PromptTokens,
	}, nil
}

// renderSessionTable writes the summary table. Unless all is set, only sessions
// with anomalies get a row.
func renderSessionTable(b *strings.Builder, stats []sessionStat, all bool) {
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

// renderAnomalyDetails writes the detailed breakdown for anomalous sessions,
// and nothing at all when no session has an anomaly.
func renderAnomalyDetails(b *strings.Builder, stats []sessionStat) {
	hasAnomalies := false
	for _, s := range stats {
		if len(s.Anomalies) == 0 {
			continue
		}
		hasAnomalies = true
		break
	}
	if !hasAnomalies {
		return
	}
	b.WriteString("\n### Anomaly Details\n\n")
	for _, s := range stats {
		if len(s.Anomalies) == 0 {
			continue
		}
		fmt.Fprintf(b, "**%s**\n", s.ID)
		fmt.Fprintf(b, "- Lines: %d, Tool calls: %d, Turns: %d, Errors: %d\n",
			s.Lines, s.ToolCalls, s.Turns, s.Errors)
		fmt.Fprintf(b, "- Duration: %s\n", s.Duration.Truncate(time.Second))
		fmt.Fprintf(b, "- Anomalies: %s\n\n", strings.Join(s.Anomalies, "; "))
	}
}

// analyzeSessionFile reads a single events.jsonl and computes stats.
func analyzeSessionFile(sessionID, path string, highToolCalls, highTurns int) sessionStat {
	f, err := os.Open(path)
	if err != nil {
		return sessionStat{ID: sessionID}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Increase scan buffer for potentially large JSON lines.
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	var counts sessionCounters
	for scanner.Scan() {
		counts.lines++

		var ev sessionEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}
		counts.observe(&ev)
	}

	stat := counts.stat(sessionID)
	stat.Anomalies = sessionAnomalies(stat, highToolCalls, highTurns)
	return stat
}

// sessionEvent is the subset of an events.jsonl record the stats read.
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

// sessionCounters accumulates the per-event tallies for one session file.
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

// observe folds one decoded event into the counters.
func (c *sessionCounters) observe(ev *sessionEvent) {
	c.observeTimestamp(ev.Timestamp)
	c.observeContent(ev.Content)

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

// observeTimestamp widens the session's time range. An absent or unparseable
// timestamp is ignored rather than treated as the zero time.
func (c *sessionCounters) observeTimestamp(timestamp string) {
	if timestamp == "" {
		return
	}
	ts, err := time.Parse(time.RFC3339Nano, timestamp)
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

// observeContent counts tool calls and results: events with FunctionCall or
// FunctionResponse parts in Content, which is decoded untyped.
func (c *sessionCounters) observeContent(content any) {
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

// stat converts the accumulated counters into the stat for one session.
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

// sessionAnomalies lists what looks wrong about one session's totals.
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
