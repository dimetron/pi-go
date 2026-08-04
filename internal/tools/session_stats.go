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

func sessionStatsHandler(input SessionStatsInput) (SessionStatsOutput, error) {
	// Resolve session directory.
	sessionDir := input.SessionDir
	if sessionDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return SessionStatsOutput{}, fmt.Errorf("getting home dir: %w", err)
		}
		sessionDir = filepath.Join(home, ".pi-go", "sessions")
	}

	// Resolve thresholds.
	highToolCalls := input.HighToolCalls
	if highToolCalls <= 0 {
		highToolCalls = 20
	}
	highTurns := input.HighTurns
	if highTurns <= 0 {
		highTurns = 5
	}

	// Resolve lookback.
	hours := input.Hours
	if hours <= 0 {
		hours = 24
	}
	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour)

	// Scan session directories.
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return SessionStatsOutput{}, fmt.Errorf("reading sessions dir: %w", err)
	}

	var stats []sessionStat
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Skip archive directory.
		if entry.Name() == "archive" {
			continue
		}
		eventsPath := filepath.Join(sessionDir, entry.Name(), "events.jsonl")
		info, err := os.Stat(eventsPath)
		if err != nil {
			continue
		}
		// Skip sessions older than cutoff.
		if info.ModTime().Before(cutoff) {
			continue
		}

		stat := analyzeSessionFile(entry.Name(), eventsPath, highToolCalls, highTurns)
		stats = append(stats, stat)
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

	fmt.Fprintf(&b, "## Session Stats (last %d hours)\n\n", hours)
	fmt.Fprintf(&b, "Scanned %d sessions, %d with anomalies.\n\n", len(stats), totalAnomalous)

	if len(stats) == 0 {
		b.WriteString("No sessions found in the specified time range.\n")
		return SessionStatsOutput{
			Content:           b.String(),
			TotalSessions:     0,
			AnomalousSessions: 0,
		}, nil
	}

	// Summary table.
	b.WriteString("| Session | Lines | Tool Calls | Turns | Errors | Duration | Anomalies |\n")
	b.WriteString("|---------|-------|------------|-------|--------|----------|-----------|\n")
	for _, s := range stats {
		if !input.All && len(s.Anomalies) == 0 {
			continue
		}
		dur := s.Duration.Truncate(time.Second)
		anomalyStr := strings.Join(s.Anomalies, ", ")
		if anomalyStr == "" {
			anomalyStr = "—"
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %s | %s |\n",
			s.ID, s.Lines, s.ToolCalls, s.Turns, s.Errors, dur, anomalyStr)
	}

	// Detailed breakdown for anomalous sessions.
	hasAnomalies := false
	for _, s := range stats {
		if len(s.Anomalies) == 0 {
			continue
		}
		hasAnomalies = true
		break
	}
	if hasAnomalies {
		b.WriteString("\n### Anomaly Details\n\n")
		for _, s := range stats {
			if len(s.Anomalies) == 0 {
				continue
			}
			fmt.Fprintf(&b, "**%s**\n", s.ID)
			fmt.Fprintf(&b, "- Lines: %d, Tool calls: %d, Turns: %d, Errors: %d\n",
				s.Lines, s.ToolCalls, s.Turns, s.Errors)
			fmt.Fprintf(&b, "- Duration: %s\n", s.Duration.Truncate(time.Second))
			fmt.Fprintf(&b, "- Anomalies: %s\n\n", strings.Join(s.Anomalies, "; "))
		}
	}

	return SessionStatsOutput{
		Content:           b.String(),
		TotalSessions:     len(stats),
		AnomalousSessions: totalAnomalous,
	}, nil
}

// analyzeSessionFile reads a single events.jsonl and computes stats.
func analyzeSessionFile(sessionID, path string, highToolCalls, highTurns int) sessionStat {
	f, err := os.Open(path)
	if err != nil {
		return sessionStat{ID: sessionID}
	}
	defer f.Close()

	stat := sessionStat{ID: sessionID}
	scanner := bufio.NewScanner(f)
	// Increase scan buffer for potentially large JSON lines.
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	var firstTS, lastTS time.Time
	var toolCallCount, toolResultCount, turnCount, errorCount, gitOpCount int
	var lineCount int

	for scanner.Scan() {
		line := scanner.Bytes()
		lineCount++

		// Parse the event.
		var ev struct {
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

		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}

		// Track time range.
		if ev.Timestamp != "" {
			ts, err := time.Parse(time.RFC3339Nano, ev.Timestamp)
			if err == nil {
				if firstTS.IsZero() || ts.Before(firstTS) {
					firstTS = ts
				}
				if ts.After(lastTS) {
					lastTS = ts
				}
			}
		}

		// Count tool calls: events with FunctionCall parts in Content.
		if ev.Content != nil {
			contentMap, ok := ev.Content.(map[string]any)
			if ok {
				if parts, ok := contentMap["parts"].([]any); ok {
					for _, p := range parts {
						pm, ok := p.(map[string]any)
						if !ok {
							continue
						}
						if fc, ok := pm["functionCall"].(map[string]any); ok {
							toolCallCount++
							if isGitOperation(fc) {
								gitOpCount++
							}
						}
						if _, ok := pm["functionResponse"]; ok {
							toolResultCount++
						}
					}
				}
			}
		}

		// Count turns: TurnComplete events from the model.
		if ev.TurnComplete && ev.Author != "user" {
			turnCount++
		}

		// Count errors: a single event is at most one error, even if both
		// ErrorCode/ErrorMessage and Interrupted are set.
		if ev.ErrorCode != "" || ev.ErrorMessage != "" || ev.Interrupted {
			errorCount++
		}
	}

	stat.Lines = lineCount
	stat.ToolCalls = toolCallCount
	stat.ToolResults = toolResultCount
	stat.Turns = turnCount
	stat.Errors = errorCount
	stat.GitOps = gitOpCount
	stat.FirstTime = firstTS
	stat.LastTime = lastTS

	if !firstTS.IsZero() && !lastTS.IsZero() {
		stat.Duration = lastTS.Sub(firstTS)
	}

	// Detect anomalies.
	if toolCallCount > highToolCalls {
		stat.Anomalies = append(stat.Anomalies, fmt.Sprintf("high tool calls (%d)", toolCallCount))
	}
	if turnCount > highTurns {
		stat.Anomalies = append(stat.Anomalies, fmt.Sprintf("excessive turns (%d)", turnCount))
	}
	if errorCount > 0 {
		stat.Anomalies = append(stat.Anomalies, fmt.Sprintf("errors (%d)", errorCount))
	}
	if gitOpCount > defaultHighGitOps {
		stat.Anomalies = append(stat.Anomalies, fmt.Sprintf("many git operations (%d)", gitOpCount))
	}
	if stat.Duration > 30*time.Minute && toolCallCount < 5 {
		stat.Anomalies = append(stat.Anomalies, "long idle session")
	}

	return stat
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
