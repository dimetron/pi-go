package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ToolsReport is the machine-readable output of one tool-coverage eval run
// (make eval-tools): per-scenario verdicts, the coverage matrix against the
// registered tool inventory, and the usual tool/token metrics over every
// trajectory the scenarios produced.
type ToolsReport struct {
	Metadata  ToolsReportMetadata `json:"metadata"`
	Scenarios []ScenarioResult    `json:"scenarios"`
	Coverage  Coverage            `json:"coverage"`
	Tools     ToolsMetrics        `json:"tools"`
	Tokens    TokenMetrics        `json:"tokens"`
	// Judge is the LLM grader's advisory verdict, absent when no judge ran.
	Judge *JudgeVerdict `json:"judge,omitempty"`
}

// ToolsReportMetadata describes what was run and when.
type ToolsReportMetadata struct {
	Model     string    `json:"model"`
	Binary    string    `json:"binary"`
	GitHead   string    `json:"git_head"`
	Timestamp time.Time `json:"timestamp"`
	Duration  string    `json:"duration"`
	// Selected is the scenario filter in effect (PI_EVAL_SCENARIO), empty for
	// the whole suite. A filtered run's coverage gap is not meaningful.
	Selected string `json:"selected,omitempty"`
	Passed   int    `json:"passed"`
	Failed   int    `json:"failed"`
	Skipped  int    `json:"skipped"`
	Errored  int    `json:"errored"`
}

// Tally counts the scenario statuses into the metadata.
func (r *ToolsReport) Tally() {
	m := &r.Metadata
	m.Passed, m.Failed, m.Skipped, m.Errored = 0, 0, 0, 0
	for _, s := range r.Scenarios {
		switch s.Status {
		case StatusPass:
			m.Passed++
		case StatusFail:
			m.Failed++
		case StatusSkip:
			m.Skipped++
		default:
			m.Errored++
		}
	}
}

// WriteToolsReport writes the report as JSON and Markdown into outDir
// (eval-tools-<ts>.json / .md) and returns both paths plus the Markdown.
func WriteToolsReport(report *ToolsReport, outDir string) (jsonPath, mdPath, md string, err error) {
	jsonBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", "", "", fmt.Errorf("marshal tools report: %w", err)
	}
	md = RenderToolsMarkdown(report)
	jsonPath, mdPath, err = writeReportFiles(outDir, "tools", report.Metadata.Timestamp, jsonBytes, md)
	return jsonPath, mdPath, md, err
}

// writeReportFiles writes the JSON and Markdown renderings of a report under
// outDir as eval-<kind>-<ts>.{json,md}.
func writeReportFiles(outDir, kind string, ts time.Time, jsonBytes []byte, md string) (jsonPath, mdPath string, err error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create report dir: %w", err)
	}
	stamp := ts.Format("20060102-150405")
	jsonPath = filepath.Join(outDir, fmt.Sprintf("eval-%s-%s.json", kind, stamp))
	if err := os.WriteFile(jsonPath, jsonBytes, 0o644); err != nil {
		return "", "", fmt.Errorf("write json report: %w", err)
	}
	mdPath = filepath.Join(outDir, fmt.Sprintf("eval-%s-%s.md", kind, stamp))
	if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
		return "", "", fmt.Errorf("write md report: %w", err)
	}
	return jsonPath, mdPath, nil
}

// RenderToolsMarkdown renders a ToolsReport as human-readable Markdown.
func RenderToolsMarkdown(r *ToolsReport) string {
	var b strings.Builder
	meta := r.Metadata

	fmt.Fprintf(&b, "# Eval run: tool coverage\n\n")
	fmt.Fprintf(&b, "- **model**: %s  \n", meta.Model)
	fmt.Fprintf(&b, "- **binary**: %s  \n", meta.Binary)
	fmt.Fprintf(&b, "- **git head**: `%s`  \n", shortHead(meta.GitHead))
	fmt.Fprintf(&b, "- **timestamp**: %s  \n", meta.Timestamp.Format(time.RFC3339))
	fmt.Fprintf(&b, "- **duration**: %s  \n", meta.Duration)
	if meta.Selected != "" {
		fmt.Fprintf(&b, "- **selected**: `%s` (filtered run — coverage gap is partial)  \n", meta.Selected)
	}
	fmt.Fprintf(&b, "- **scenarios**: %d passed, %d failed, %d skipped, %d errored  \n",
		meta.Passed, meta.Failed, meta.Skipped, meta.Errored)

	renderScenarios(&b, r.Scenarios)
	renderCoverage(&b, &r.Coverage)
	renderJudge(&b, r.Judge)
	renderToolsTable(&b, &r.Tools)
	renderTokens(&b, &r.Tokens)
	return b.String()
}

func renderScenarios(b *strings.Builder, scenarios []ScenarioResult) {
	fmt.Fprintf(b, "\n## Scenarios\n\n")
	if len(scenarios) == 0 {
		fmt.Fprintf(b, "- none run\n")
		return
	}
	fmt.Fprintf(b, "| scenario | status | tools | sessions | tool calls | duration | detail |\n")
	fmt.Fprintf(b, "|---|---|---|---|---|---|---|\n")
	for _, s := range scenarios {
		fmt.Fprintf(b, "| `%s` | %s | %s | %d | %d | %s | %s |\n",
			s.Name, statusMark(s.Status), toolsCell(s.Tools), s.Sessions, s.ToolCalls, s.Duration, escapeCell(truncate(s.Reason, 300)))
	}
	for _, s := range scenarios {
		if len(s.Checks) == 0 {
			continue
		}
		fmt.Fprintf(b, "\n**%s** checks:\n\n", s.Name)
		for _, c := range s.Checks {
			mark := "✓"
			if !c.Passed {
				mark = "✗"
			}
			fmt.Fprintf(b, "- %s `%s`", mark, c.Check)
			if c.Detail != "" {
				fmt.Fprintf(b, " — %s", escapeCell(c.Detail))
			}
			fmt.Fprintf(b, "\n")
		}
	}
}

// toolsCell renders the per-target outcomes as "name ✓/✗" pairs.
func toolsCell(tools []ToolOutcome) string {
	parts := make([]string, 0, len(tools))
	for _, t := range tools {
		mark := "✓"
		if !t.OK {
			mark = "✗"
		}
		parts = append(parts, fmt.Sprintf("`%s` %s", t.Tool, mark))
	}
	return strings.Join(parts, ", ")
}

func statusMark(status string) string {
	switch status {
	case StatusPass:
		return "PASS"
	case StatusFail:
		return "FAIL"
	case StatusSkip:
		return "skip"
	default:
		return strings.ToUpper(status)
	}
}

func renderCoverage(b *strings.Builder, c *Coverage) {
	fmt.Fprintf(b, "\n## Tool coverage\n\n")
	fmt.Fprintf(b, "- **registered tools**: %d  \n", c.Total)
	fmt.Fprintf(b, "- **exercised ok**: %d  \n", c.OK)
	fmt.Fprintf(b, "- **errors only**: %d  \n", c.Errors)
	fmt.Fprintf(b, "- **not called**: %d  \n", c.NotCalled)
	fmt.Fprintf(b, "- **excluded**: %d  \n", c.Excluded)
	fmt.Fprintf(b, "- **unmapped** (no scenario, no exclusion): %d  \n", c.Unmapped)
	if len(c.Gap) > 0 {
		fmt.Fprintf(b, "- **gap**: %s  \n", strings.Join(backticked(c.Gap), ", "))
	}
	if len(c.Unknown) > 0 {
		fmt.Fprintf(b, "- **called but not inventoried**: %s  \n", strings.Join(backticked(c.Unknown), ", "))
	}
	if len(c.Tools) == 0 {
		return
	}
	fmt.Fprintf(b, "\n| tool | group | status | calls | results | errors | wasted | scenarios / exclusion |\n")
	fmt.Fprintf(b, "|---|---|---|---|---|---|---|---|\n")
	for _, t := range c.Tools {
		mapping := strings.Join(t.Scenarios, ", ")
		if t.Excluded != "" {
			mapping = "excluded: " + t.Excluded
		}
		fmt.Fprintf(b, "| `%s` | %s | %s | %d | %d | %d | %d | %s |\n",
			t.Name, t.Group, t.Status, t.Calls, t.Results, t.Errors, t.Wasted, escapeCell(mapping))
	}
}

func backticked(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "`" + n + "`"
	}
	return out
}

func renderToolsTable(b *strings.Builder, tm *ToolsMetrics) {
	fmt.Fprintf(b, "\n## Tools efficiency\n\n")
	fmt.Fprintf(b, "- **total calls**: %d  \n", tm.TotalCalls)
	fmt.Fprintf(b, "- **total results**: %d  \n", tm.TotalResults)
	fmt.Fprintf(b, "- **wasted calls** (no result): %d  \n", tm.Wasted)
	fmt.Fprintf(b, "- **duplicate calls**: %d  \n", tm.Duplicates)
	if len(tm.ByTool) == 0 {
		return
	}
	fmt.Fprintf(b, "\n| tool | calls | results | errors | wasted | duplicates | avg bytes | avg latency |\n")
	fmt.Fprintf(b, "|---|---|---|---|---|---|---|---|\n")
	for _, name := range sortedKeys(tm.ByTool) {
		st := tm.ByTool[name]
		fmt.Fprintf(b, "| `%s` | %d | %d | %d | %d | %d | %d | %dms |\n",
			name, st.Calls, st.Results, st.Errors, st.Wasted, st.Duplicates, st.AvgResultBytes, st.AvgLatencyMs)
	}
}

func renderTokens(b *strings.Builder, tk *TokenMetrics) {
	fmt.Fprintf(b, "\n## Tokens\n\n")
	fmt.Fprintf(b, "- **prompt tokens**: %d  \n", tk.PromptTokens)
	fmt.Fprintf(b, "- **completion tokens**: %d  \n", tk.CompletionTokens)
	if tk.CachedTokens > 0 {
		fmt.Fprintf(b, "- **cached tokens**: %d  \n", tk.CachedTokens)
	}
	fmt.Fprintf(b, "- **total tokens**: %d  \n", tk.TotalTokens)
}
