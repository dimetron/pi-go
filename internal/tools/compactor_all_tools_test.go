package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// prodTool is a minimal tool.Tool carrying only the registered name.
type prodTool struct{ name string }

func (p *prodTool) Name() string        { return p.name }
func (p *prodTool) Description() string { return "" }
func (p *prodTool) IsLongRunning() bool { return false }

// prodResult mirrors ADK's functiontool.Run: a typed output struct is
// json.Marshal'd then json.Unmarshal'd into map[string]any
// (adk v2.4.0 internal/typeutil/convert.go, ConvertToWithJSONSchema).
// Nested arrays therefore arrive as []any of map[string]any — NOT typed slices.
func prodResult(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

// prodPayloads builds one realistically-shaped payload per registered
// bulk-output tool. Shared by the before/after measurement and the guard tests
// so the two can never disagree on shape.
func prodPayloads(t *testing.T) []struct {
	Tool   string
	Result map[string]any
} {
	t.Helper()
	const n = 400

	lines := make([]string, n)
	for i := range lines {
		lines[i] = strings.Repeat("A", 60) + fmt.Sprintf(" line %d", i)
	}
	bigText := strings.Join(lines, "\n")

	matches := make([]GrepMatch, n)
	for i := range matches {
		matches[i] = GrepMatch{
			File:    fmt.Sprintf("internal/pkg%d/file%d.go", i%7, i),
			Line:    i + 1,
			Content: strings.Repeat("B", 50) + fmt.Sprintf(" match %d", i),
		}
	}
	files := make([]string, n)
	for i := range files {
		files[i] = fmt.Sprintf("internal/pkg%d/file%d.go", i%7, i)
	}
	entries := make([]LsEntry, n)
	for i := range entries {
		entries[i] = LsEntry{Name: fmt.Sprintf("entry%d.go", i), Size: int64(1000 + i)}
	}
	commits := make([]string, n)
	for i := range commits {
		commits[i] = fmt.Sprintf("%07x %s", i, strings.Repeat("c", 60))
	}
	staged := make([]string, n)
	for i := range staged {
		staged[i] = fmt.Sprintf("staged/file%d.go", i)
	}
	hunks := make([]Hunk, n)
	for i := range hunks {
		hunks[i] = Hunk{Header: "@@ -1,5 +1,7 @@", Content: strings.Repeat("D", 60), Added: 1, Removed: 1}
	}

	return []struct {
		Tool   string
		Result map[string]any
	}{
		{"bash", prodResult(t, BashOutput{Stdout: bigText, ExitCode: 0})},
		{"read", prodResult(t, ReadOutput{Content: bigText, TotalLines: n})},
		{"ripgrep", prodResult(t, GrepOutput{Matches: matches, TotalMatches: n})},
		{"find", prodResult(t, FindOutput{Files: files, TotalFiles: n})},
		{"tree", prodResult(t, TreeOutput{Tree: bigText, Dirs: 40, Files: n})},
		{"ls", prodResult(t, LsOutput{Entries: entries, TotalEntries: n})},
		{"git-file-diff", prodResult(t, GitFileDiffOutput{File: "x.go", Diff: bigText, LinesAdded: 1})},
		{"git-overview", prodResult(t, GitOverviewOutput{
			Branch: "main", RecentCommits: commits,
			StagedFiles: staged, UnstagedFiles: staged, UntrackedFiles: staged,
		})},
		{"git-hunk", prodResult(t, GitHunkOutput{File: "x.go", Hunks: hunks, TotalHunks: n})},
	}
}

// TestCompactor_EveryToolCompacts is the end-to-end guard for the compaction
// fixes: for each registered bulk-output tool it runs the real
// BuildCompactorCallback against a result built from that tool's real output
// struct, and asserts the encoded result actually shrank.
//
// Before the fix this test would have reported 7 of 9 dead — only bash and read
// compacted. It asserts the count rather than logging it so a silently
// re-registered tool or a pipeline that starts declining cannot pass unnoticed.
func TestCompactor_EveryToolCompacts(t *testing.T) {
	cfg := DefaultCompactorConfig()
	cfg.Enabled = true

	type row struct {
		tool, verdict string
		before, after int
	}
	var rows []row

	for _, c := range prodPayloads(t) {
		before := encodedSize(c.Result)
		metrics := NewCompactMetrics()
		cb := BuildCompactorCallback(cfg, metrics)

		got, err := cb(nil, &prodTool{name: c.Tool}, map[string]any{}, c.Result, nil)
		if err != nil {
			t.Fatalf("%s: callback error: %v", c.Tool, err)
		}
		after := encodedSize(got)

		verdict := "DEAD (no-op)"
		if after < before {
			verdict = "COMPACTED"
		}
		rows = append(rows, row{c.Tool, verdict, before, after})
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].tool < rows[j].tool })
	t.Logf("%-15s %-14s %9s %9s %9s", "tool", "verdict", "before", "after", "saved")
	t.Logf("%s", strings.Repeat("-", 62))
	dead := 0
	for _, r := range rows {
		saved := "0.0%"
		if r.before > 0 {
			saved = fmt.Sprintf("%.1f%%", 100*float64(r.before-r.after)/float64(r.before))
		}
		if r.verdict == "DEAD (no-op)" {
			dead++
			t.Errorf("%s did not compact (before=%d after=%d)", r.tool, r.before, r.after)
		}
		t.Logf("%-15s %-14s %9d %9d %9s", r.tool, r.verdict, r.before, r.after, saved)
	}
	t.Logf("%s", strings.Repeat("-", 62))
	t.Logf("dead: %d of %d (was 7 of 9 before the fix)", dead, len(rows))
}

// encodedSize is the byte size the model actually sees: ADK passes the result
// map through json.Marshal (contents_processor.stringify) before it reaches the
// prompt, so the JSON encoding — not the in-memory map — is what costs tokens.
func encodedSize(m map[string]any) int {
	b, _ := json.Marshal(m)
	return len(b)
}
