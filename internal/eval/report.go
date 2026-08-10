package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WriteReport writes the run report as JSON and Markdown into outDir and
// returns both paths. The Markdown rendering is also returned so the caller
// can print it to stdout without re-reading the file.
func WriteReport(report *RunReport, outDir string) (jsonPath, mdPath, md string, err error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", "", "", fmt.Errorf("create report dir: %w", err)
	}

	jsonBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", "", "", fmt.Errorf("marshal report: %w", err)
	}

	ts := report.Metadata.Timestamp.Format("20060102-150405")
	jsonPath = filepath.Join(outDir, fmt.Sprintf("eval-%s-%s.json", report.Metadata.Spec, ts))
	if err := os.WriteFile(jsonPath, jsonBytes, 0o644); err != nil {
		return "", "", "", fmt.Errorf("write json report: %w", err)
	}

	md = RenderMarkdown(report)
	mdPath = filepath.Join(outDir, fmt.Sprintf("eval-%s-%s.md", report.Metadata.Spec, ts))
	if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
		return "", "", "", fmt.Errorf("write md report: %w", err)
	}

	return jsonPath, mdPath, md, nil
}

// RenderMarkdown renders a RunReport as human-readable Markdown for stdout.
func RenderMarkdown(r *RunReport) string {
	var b strings.Builder

	meta := r.Metadata
	fmt.Fprintf(&b, "# Eval run: %s\n\n", meta.Spec)
	fmt.Fprintf(&b, "- **mode**: %s  \n", meta.Mode)
	fmt.Fprintf(&b, "- **model**: %s  \n", meta.Model)
	fmt.Fprintf(&b, "- **binary**: %s  \n", meta.Binary)
	fmt.Fprintf(&b, "- **git head**: `%s`  \n", shortHead(meta.GitHead))
	if meta.BaseRef != "" {
		fmt.Fprintf(&b, "- **base ref**: `%s` (`%s`)  \n", meta.BaseRef, shortHead(meta.BaseCommit))
	}
	fmt.Fprintf(&b, "- **timestamp**: %s  \n", meta.Timestamp.Format(time.RFC3339))
	fmt.Fprintf(&b, "- **duration**: %s  \n", meta.Duration)

	fmt.Fprintf(&b, "\n## Outcome\n\n")
	fmt.Fprintf(&b, "- **final phase**: `%s`  \n", r.Outcome.FinalPhase)
	fmt.Fprintf(&b, "- **retries**: %d  \n", r.Outcome.Retries)
	if r.Outcome.Reason != "" {
		fmt.Fprintf(&b, "\n```\n%s\n```\n", strings.TrimSpace(r.Outcome.Reason))
	}
	renderGates(&b, r.Outcome.GateResults)
	renderGolden(&b, "Golden check", r.Outcome.GoldenCheck, r.Outcome.GoldenPass)
	if r.Outcome.BaselineRef != "" {
		fmt.Fprintf(&b, "\n- **baseline ref**: `%s`  \n", r.Outcome.BaselineRef)
		renderGolden(&b, "Baseline check", r.Outcome.BaselineCheck, r.Outcome.BaselinePass)
	}

	renderJudge(&b, r.Judge)

	fmt.Fprintf(&b, "\n## Trajectory\n\n")
	tr := r.Trajectory
	fmt.Fprintf(&b, "- **sessions**: %d  \n", len(tr.Sessions))
	fmt.Fprintf(&b, "- **total steps**: %d  \n", tr.TotalSteps)
	fmt.Fprintf(&b, "- **total tool calls**: %d  \n", tr.TotalToolCalls)
	fmt.Fprintf(&b, "- **nested agent calls**: %d  \n", tr.NestedAgentCalls)
	fmt.Fprintf(&b, "- **max nesting depth**: %d  \n", tr.MaxDepth)

	if len(tr.Sessions) > 0 {
		fmt.Fprintf(&b, "\n| session | agent | model | depth | steps | tool calls | subagent refs | duration |\n")
		fmt.Fprintf(&b, "|---|---|---|---|---|---|---|---|\n")
		for _, s := range tr.Sessions {
			model := s.Model
			if model == "" {
				model = "—"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %d | %d | %d | %d | %s |\n",
				shortID(s.SessionID), s.AgentName, model, s.Depth, s.Steps, s.ToolCalls, s.SubagentRefs, s.Duration)
		}
	}

	fmt.Fprintf(&b, "\n## Concurrency\n\n")
	cc := r.Concurrency
	fmt.Fprintf(&b, "- **pool budget**: %d  \n", cc.PoolBudget)
	fmt.Fprintf(&b, "- **worker budget** (nested): %d  \n", cc.WorkerBudget)
	fmt.Fprintf(&b, "- **max concurrent**: %d  \n", cc.MaxRunning)
	fmt.Fprintf(&b, "- **mean concurrent**: %.1f  \n", cc.MeanRunning)
	fmt.Fprintf(&b, "- **agents seen**: %d  \n", cc.AgentsSeen)
	fmt.Fprintf(&b, "- **parallel overlap** (fraction of samples with >1 running): %.2f  \n", cc.ParallelOverlap)
	if len(cc.Samples) > 0 {
		fmt.Fprintf(&b, "\n```\n%s\n```\n", sparkline(cc.Samples))
	}

	fmt.Fprintf(&b, "\n## Tools efficiency\n\n")
	tm := r.Tools
	fmt.Fprintf(&b, "- **total calls**: %d  \n", tm.TotalCalls)
	fmt.Fprintf(&b, "- **total results**: %d  \n", tm.TotalResults)
	fmt.Fprintf(&b, "- **wasted calls** (no result): %d  \n", tm.Wasted)
	fmt.Fprintf(&b, "- **duplicate calls**: %d  \n", tm.Duplicates)
	fmt.Fprintf(&b, "- **nested agent calls** (subagent tool): %d  \n", tm.NestedAgentCalls)

	if len(tm.ByTool) > 0 {
		fmt.Fprintf(&b, "\n| tool | calls | results | errors | wasted | duplicates | avg bytes | avg latency |\n")
		fmt.Fprintf(&b, "|---|---|---|---|---|---|---|---|\n")
		for name, st := range tm.ByTool {
			fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %d | %d | %d | %dms |\n",
				name, st.Calls, st.Results, st.Errors, st.Wasted, st.Duplicates, st.AvgResultBytes, st.AvgLatencyMs)
		}
	}

	return b.String()
}

// renderJudge writes the LLM grader's verdict. The verdict is advisory — it is
// shown alongside the measured numbers, never in place of them — so a judge
// that could not run degrades to a one-line note.
func renderJudge(b *strings.Builder, j *JudgeVerdict) {
	if j == nil {
		return
	}
	fmt.Fprintf(b, "\n## LLM judge\n\n")
	if j.Error != "" {
		fmt.Fprintf(b, "- **judge**: unavailable (%s)\n", j.Error)
		return
	}
	fmt.Fprintf(b, "- **judge model**: %s  \n", j.Model)
	fmt.Fprintf(b, "- **verdict**: %s  \n", strings.ToUpper(j.Verdict))
	fmt.Fprintf(b, "- **overall**: %.2f / 5  \n", j.Overall)
	if j.Summary != "" {
		fmt.Fprintf(b, "\n%s\n", j.Summary)
	}
	if len(j.Scores) > 0 {
		fmt.Fprintf(b, "\n| dimension | score | rationale |\n|---|---|---|\n")
		for _, s := range j.Scores {
			fmt.Fprintf(b, "| %s | %d/5 | %s |\n", s.Dimension, s.Score, escapeCell(s.Rationale))
		}
	}
	if len(j.Issues) > 0 {
		fmt.Fprintf(b, "\n**Issues raised:**\n\n")
		for _, issue := range j.Issues {
			fmt.Fprintf(b, "- %s\n", issue)
		}
	}
}

// escapeCell keeps model-authored prose from breaking the Markdown table it
// lands in.
func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

func renderGates(b *strings.Builder, gates []GateResult) {
	if len(gates) == 0 {
		fmt.Fprintf(b, "- **gates**: none\n")
		return
	}
	fmt.Fprintf(b, "\n| gate | command | result |\n|---|---|---|\n")
	for _, g := range gates {
		mark := "✓"
		if !g.Passed {
			mark = "✗"
		}
		fmt.Fprintf(b, "| %s | `%s` | %s |\n", g.Name, g.Command, mark)
	}
}

func renderGolden(b *strings.Builder, title string, checks []GoldenFile, pass bool) {
	mark := "PASS"
	if !pass {
		mark = "FAIL"
	}
	fmt.Fprintf(b, "\n**%s**: %s\n\n", title, mark)
	for _, g := range checks {
		state := "match"
		if !g.Match {
			state = "mismatch"
		}
		fmt.Fprintf(b, "- `%s`: %s", g.Name, state)
		if g.Error != "" {
			fmt.Fprintf(b, " (%s)", g.Error)
		}
		fmt.Fprintf(b, "\n")
		if g.Diff != "" {
			fmt.Fprintf(b, "```\n%s\n```\n", strings.TrimRight(g.Diff, "\n"))
		}
	}
}

// sparkline renders the running-count series as an ASCII sparkline, bucketing
// samples so the line stays at most ~100 characters wide.
func sparkline(samples []ConcurrencySample) string {
	const width = 100
	n := len(samples)
	if n == 0 {
		return ""
	}
	// Aggregate into buckets when there are more samples than columns.
	buckets := make([]int, 0, min(n, width))
	if n <= width {
		for _, s := range samples {
			buckets = append(buckets, s.Running)
		}
	} else {
		per := float64(n) / width
		for i := 0; i < width; i++ {
			lo, hi := int(float64(i)*per), int(float64(i+1)*per)
			if hi > n {
				hi = n
			}
			max := 0
			for j := lo; j < hi; j++ {
				if samples[j].Running > max {
					max = samples[j].Running
				}
			}
			buckets = append(buckets, max)
		}
	}

	levels := "▁▂▃▄▅▆▇█"
	scale := 1
	for _, v := range buckets {
		if v > scale {
			scale = v
		}
	}
	if scale < 1 {
		scale = 1
	}
	var b strings.Builder
	for _, v := range buckets {
		idx := (v * (len(levels) - 1)) / scale
		b.WriteByte(levels[idx])
	}
	fmt.Fprintf(&b, "  (max %d, %d samples)", scale, n)
	return b.String()
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12] + "…"
}

func shortHead(head string) string {
	if len(head) <= 12 {
		return head
	}
	return head[:12]
}
