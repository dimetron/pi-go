package tools

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Pure Go SQLite driver, for the pipeline-health probes.
)

// oversizeChars is the result size above which the compactor would have
// trimmed. It mirrors DefaultCompactorConfig().MaxChars — the excess over it is
// what a missing or unwired compactor costs on every request that carries the
// result forward.
const oversizeChars = 24000

// compactedTools is the set compactToolResult actually handles. A tool outside
// it is uncapped by design, not by accident, and the distinction is the whole
// point of reporting oversize per tool: one means a component is broken, the
// other means a tool was never wired up.
var compactedTools = map[string]bool{
	"read": true, "bash": true, "grep": true, "find": true, "tree": true,
	"git_file_diff": true, "git_overview": true, "git_hunk": true,
}

// toolUsage accumulates per-tool volume across the scanned window.
type toolUsage struct {
	Name      string
	Calls     int
	Errors    int
	Bytes     int
	Oversized int // bytes above oversizeChars, summed
}

// sweepTotals is the cross-session aggregate behind the deep report.
type sweepTotals struct {
	PromptTokens int
	OutputTokens int
	ToolBytes    int
	DupBytes     int
	Tools        map[string]*toolUsage
	Aborts       map[string]int
	Models       map[string]int
}

func newSweepTotals() *sweepTotals {
	return &sweepTotals{
		Tools:  map[string]*toolUsage{},
		Aborts: map[string]int{},
		Models: map[string]int{},
	}
}

func (t *sweepTotals) tool(name string) *toolUsage {
	u, ok := t.Tools[name]
	if !ok {
		u = &toolUsage{Name: name}
		t.Tools[name] = u
	}
	return u
}

// sortedTools returns per-tool usage ordered by the given key, descending.
func (t *sweepTotals) sortedTools(key func(*toolUsage) int) []*toolUsage {
	out := make([]*toolUsage, 0, len(t.Tools))
	for _, u := range t.Tools {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		if key(out[i]) != key(out[j]) {
			return key(out[i]) > key(out[j])
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// sweepEvent is the subset of a session event the deep scan reads. Field names
// are capitalized for readability; encoding/json matches the lowercase wire
// keys case-insensitively.
type sweepEvent struct {
	Content struct {
		Parts []struct {
			FunctionCall *struct {
				Name string `json:"name"`
			} `json:"functionCall"`
			FunctionResponse *struct {
				Name     string          `json:"name"`
				Response json.RawMessage `json:"response"`
			} `json:"functionResponse"`
		} `json:"parts"`
	} `json:"content"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
	ErrorMessage string `json:"errorMessage"`
}

// accumulateSweepFile folds one session's events into the totals. seen is per
// session, so a duplicate is a result the model was shown twice inside one
// run — the thing dedup exists to prevent.
func accumulateSweepFile(totals *sweepTotals, path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	seen := map[string]bool{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 8*1024*1024)
	for scanner.Scan() {
		var ev sweepEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}

		if ev.UsageMetadata != nil {
			totals.PromptTokens += ev.UsageMetadata.PromptTokenCount
			totals.OutputTokens += ev.UsageMetadata.CandidatesTokenCount
		}
		if detail := abortDetail(ev.ErrorMessage); detail != "" {
			totals.Aborts[detail]++
		}

		for _, p := range ev.Content.Parts {
			if p.FunctionCall != nil {
				totals.tool(p.FunctionCall.Name).Calls++
			}
			if p.FunctionResponse == nil {
				continue
			}
			u := totals.tool(p.FunctionResponse.Name)
			body := string(p.FunctionResponse.Response)
			u.Bytes += len(body)
			totals.ToolBytes += len(body)
			if strings.Contains(body, `"error"`) {
				u.Errors++
			}
			if len(body) > oversizeChars {
				u.Oversized += len(body) - oversizeChars
			}
			if seen[body] {
				totals.DupBytes += len(body)
			} else {
				seen[body] = true
			}
		}
	}
}

// abortDetail extracts the reason from a loop-guard abort, or "" when the
// message is any other error.
func abortDetail(msg string) string {
	const marker = "agent loop aborted:"
	i := strings.Index(msg, marker)
	if i < 0 {
		return ""
	}
	detail := strings.TrimSpace(msg[i+len(marker):])
	if len(detail) > 120 {
		detail = detail[:120]
	}
	return detail
}

// renderSweep writes the deep sections: failures, waste, and real token spend.
func renderSweep(b *strings.Builder, totals *sweepTotals, sessions int) {
	renderSweepFailures(b, totals)
	renderSweepWaste(b, totals)
	renderSweepSpend(b, totals, sessions)
}

// renderSweepFailures writes the aborted runs and the per-tool error rates.
func renderSweepFailures(b *strings.Builder, totals *sweepTotals) {
	b.WriteString("\n## Failures\n\n")
	if len(totals.Aborts) == 0 {
		b.WriteString("No aborted runs.\n")
	} else {
		b.WriteString("Aborted by the loop guard — run the `pi-loop-forensics` skill on these:\n\n")
		for detail, n := range totals.Aborts {
			fmt.Fprintf(b, "- %dx `%s`\n", n, detail)
		}
	}

	byErrors := totals.sortedTools(func(u *toolUsage) int { return u.Errors })
	if len(byErrors) == 0 || byErrors[0].Errors == 0 {
		return
	}
	b.WriteString("\nTool errors, as a rate — a busy tool with a few failures is not the problem:\n\n")
	b.WriteString("| tool | errors | calls | rate |\n|---|---:|---:|---:|\n")
	for _, u := range byErrors {
		if u.Errors == 0 {
			break
		}
		calls := max(u.Calls, 1)
		fmt.Fprintf(b, "| %s | %d | %d | %d%% |\n", u.Name, u.Errors, calls, 100*u.Errors/calls)
	}
	b.WriteString("\n→ `pi-check-session-logs` dedupes these by pattern.\n")
}

// renderSweepWaste writes what capping oversized results and dropping
// duplicates would give back.
func renderSweepWaste(b *strings.Builder, totals *sweepTotals) {
	b.WriteString("\n## Token waste\n\n")
	overTotal := 0
	for _, u := range totals.Tools {
		overTotal += u.Oversized
	}
	if overTotal == 0 && totals.DupBytes == 0 {
		b.WriteString("Nothing oversized or duplicated.\n")
		return
	}

	reclaim := (overTotal + totals.DupBytes) / 4
	pct := 0
	if totals.ToolBytes > 0 {
		pct = 100 * (overTotal + totals.DupBytes) / totals.ToolBytes
	}
	fmt.Fprintf(b, "Reclaimable: **~%s tokens** (%d%% of tool output)\n\n", thousands(reclaim), pct)
	b.WriteString("| tool | excess over 24k | compactor covers it? |\n|---|---:|---|\n")
	for _, u := range totals.sortedTools(func(u *toolUsage) int { return u.Oversized }) {
		if u.Oversized == 0 {
			break
		}
		covered := "**no — uncapped**"
		if compactedTools[strings.ReplaceAll(u.Name, "-", "_")] {
			covered = "yes"
		}
		fmt.Fprintf(b, "| %s | %s tok | %s |\n", u.Name, thousands(u.Oversized/4), covered)
	}
	if totals.DupBytes > 0 {
		fmt.Fprintf(b, "\nDuplicate results re-sent inside one session: ~%s tokens.\n",
			thousands(totals.DupBytes/4))
	}
}

// renderSweepSpend writes the provider-reported token totals.
func renderSweepSpend(b *strings.Builder, totals *sweepTotals, sessions int) {
	b.WriteString("\n## Token spend\n\n")
	if totals.PromptTokens == 0 {
		b.WriteString("No usage metadata — the provider did not report token counts.\n")
		return
	}
	fmt.Fprintf(b, "Prompt %s · output %s (provider-reported, not estimated)\n",
		thousands(totals.PromptTokens), thousands(totals.OutputTokens))
	if sessions > 0 {
		fmt.Fprintf(b, "\nPrompt tokens are re-sent every request, so this is dominated by the fixed\nblock — system prompt plus tool declarations — not by the work. "+
			"Average %s prompt tokens per session across %d session(s).\n",
			thousands(totals.PromptTokens/sessions), sessions)
	}
}

// renderPipelineHealth reports whether the recording pipelines are alive.
//
// Both stores are best-effort by design: every failure downgrades to a warning
// nobody reads, so they can be dead for months while looking fine. The check is
// a ratio, not a presence test — a store holding a handful of rows across
// thousands of sessions is broken in the way that looks healthiest.
func renderPipelineHealth(b *strings.Builder, sessions int, since time.Time) {
	b.WriteString("\n## Pipeline health\n\n")
	home, err := os.UserHomeDir()
	if err != nil {
		b.WriteString("- home directory unavailable; skipped\n")
		return
	}

	memPath := filepath.Join(home, ".pi-go", "memory", "claude-mem.db")
	if _, statErr := os.Stat(memPath); statErr != nil {
		b.WriteString("- **observations**: no database yet\n")
	} else if recent, total, err := countObservations(memPath, since); err != nil {
		fmt.Fprintf(b, "- **observations (ERROR)**: %v\n", err)
	} else {
		expected := max(sessions, 1)
		status := "STALLED"
		switch {
		case recent >= expected:
			status = "OK"
		case recent > 0:
			status = "DEGRADED"
		}
		fmt.Fprintf(b, "- **observations (%s)**: %d in window, %d total", status, recent, total)
		if status != "OK" {
			fmt.Fprintf(b, " — expected >= %d for %d session(s); see specs/memory-fixes/", expected, sessions)
		}
		b.WriteString("\n")
	}

	for _, p := range []struct{ label, path string }{
		{"palace (home)", filepath.Join(home, ".pi-go", "palace.db")},
		{"palace (project)", filepath.Join(".pi-go", "palace.db")},
	} {
		info, statErr := os.Stat(p.path)
		if statErr != nil {
			continue
		}
		n, err := countDrawers(p.path)
		if err != nil {
			continue
		}
		age := int(time.Since(info.ModTime()).Hours() / 24)
		fmt.Fprintf(b, "- **%s**: %d drawers, last write %dd ago\n", p.label, n, age)
	}
}

// countObservations opens the observation store read-only and returns the rows
// inside the window and in total.
func countObservations(path string, since time.Time) (recent, total int, err error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return 0, 0, err
	}
	defer db.Close()
	if err := db.QueryRow("SELECT COUNT(*) FROM observations").Scan(&total); err != nil {
		return 0, 0, err
	}
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM observations WHERE created_at_epoch >= ?", since.Unix(),
	).Scan(&recent); err != nil {
		return 0, total, err
	}
	return recent, total, nil
}

func countDrawers(path string) (int, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var n int
	err = db.QueryRow("SELECT COUNT(*) FROM drawers").Scan(&n)
	return n, err
}

// thousands formats n with comma separators, so six-figure token counts stay
// readable in a terminal table.
func thousands(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

// totalAborts counts loop-guard aborts across the window.
func totalAborts(t *sweepTotals) int {
	n := 0
	for _, c := range t.Aborts {
		n += c
	}
	return n
}

// reclaimableTokens is the oversize excess plus duplicate volume, in tokens.
// Estimated at 4 chars per token, which is fine for ranking and trends and is
// not a billing figure — unlike PromptTokens, which the provider reports.
func reclaimableTokens(t *sweepTotals) int {
	over := 0
	for _, u := range t.Tools {
		over += u.Oversized
	}
	return (over + t.DupBytes) / 4
}
