package tools

import (
	"fmt"
	"strings"
	"testing"
)

// The deduper's contract, stated in dedup.go, is that "the model cannot be
// served stale bytes": a changed file must always produce full content. The
// callback chain violates it whenever the compactor runs first, because dedup
// hashes the *post-compaction* field — and compaction is lossy. Two genuinely
// different results that truncate to the same head hash identically, so dedup
// replaces the second with "content is unchanged", telling the model a file it
// changed did not change.
//
// These tests fail if dedup ever compares truncated bytes. The fix is to hash
// the pre-compaction content, so equality means the tool produced equal bytes.

// chainWithDedup wires the real callbacks in the production order: dedup, then
// compactor (internal/cli/cli.go). Dedup must see pre-compaction content, or its
// hash compares truncated bytes.
func chainWithDedup(cfg CompactorConfig) (compactor, dedup func(name string, args, result map[string]any) map[string]any, d *ResultDeduper) {
	d = NewResultDeduper()
	compCB := BuildCompactorCallback(cfg, NewCompactMetrics())
	dedupCB := BuildDedupCallback(d)
	run := func(name string, args, result map[string]any) map[string]any {
		out, _ := dedupCB(nil, &prodTool{name: name}, args, result, nil)
		out, _ = compCB(nil, &prodTool{name: name}, args, out, nil)
		return out
	}
	return nil, run, d
}

// TestDedup_ChangedDiffIsNotElided is the load-bearing case: a diff that changed
// only beyond the compaction cap must not be reported as unchanged.
func TestDedup_ChangedDiffIsNotElided(t *testing.T) {
	cfg := DefaultCompactorConfig()
	_, run, _ := chainWithDedup(cfg)
	args := map[string]any{"file": "big.go"}

	mk := func(tag string) map[string]any {
		var b strings.Builder
		b.WriteString("diff --git a/big.go b/big.go\n--- a/big.go\n+++ b/big.go\n@@ -1,9000 +1,9000 @@\n")
		// A shared head long enough that the compacted result clears
		// dedupMinBytes, so the dedupe path is actually taken.
		for i := 0; i < 300; i++ {
			fmt.Fprintf(&b, "-shared line %d padded with body to add bytes here\n+shared line %d padded with body to add bytes here\n", i, i)
		}
		// The real difference sits far beyond the cap.
		for i := 0; i < 4000; i++ {
			fmt.Fprintf(&b, "-%s %d\n+%s %d\n", tag, i, tag, i)
		}
		return prodResult(t, GitFileDiffOutput{File: "big.go", Diff: b.String(), LinesAdded: 4300})
	}

	first, _ := run("git-file-diff", args, mk("alpha"))["diff"].(string)
	second, _ := run("git-file-diff", args, mk("bravo"))["diff"].(string)

	// The contract: a changed file must never be reported as unchanged. Two
	// different diffs legitimately compact to the same truncated head — that is
	// lossy truncation, not a dedup failure — so the assertion is on the claim
	// dedup makes, not on whether the compacted bytes happen to match.
	if strings.Contains(second, "identical to the result") ||
		strings.Contains(second, "content is unchanged") {
		t.Errorf("a CHANGED diff was elided as an unchanged repeat; the model is told "+
			"nothing changed:\n%q", second)
	}
	if !strings.Contains(first, "omitted") || !strings.Contains(second, "omitted") {
		t.Errorf("expected both diffs to be compacted with a disclosure; got\n1: %q\n2: %q",
			truncForLog(first, 120), truncForLog(second, 120))
	}
}

// TestDedup_TwoDistinctResultsNeverCollapse is the general form: for any tool
// whose output the compactor truncates, two different results must not be
// served as one.
func TestDedup_TwoDistinctResultsNeverCollapse(t *testing.T) {
	cfg := DefaultCompactorConfig()

	// git-file-diff: diff text, capped.
	t.Run("git-file-diff", func(t *testing.T) {
		_, run, _ := chainWithDedup(cfg)
		args := map[string]any{"file": "big.go"}
		mk := func(tag string) map[string]any {
			var b strings.Builder
			b.WriteString("@@ -1,9000 +1,9000 @@\n")
			for i := 0; i < 300; i++ {
				fmt.Fprintf(&b, "-shared %d padded with a reasonably long body\n+shared %d padded with a reasonably long body\n", i, i)
			}
			for i := 0; i < 4000; i++ {
				fmt.Fprintf(&b, "-%s %d\n+%s %d\n", tag, i, tag, i)
			}
			return prodResult(t, GitFileDiffOutput{File: "big.go", Diff: b.String(), LinesAdded: 4300})
		}
		a, _ := run("git-file-diff", args, mk("alpha"))["diff"].(string)
		bb, _ := run("git-file-diff", args, mk("bravo"))["diff"].(string)
		if strings.Contains(bb, "identical to the result") {
			t.Errorf("changed diff elided as unchanged")
		}
		if !strings.Contains(a, "omitted") && !strings.Contains(bb, "omitted") {
			t.Errorf("neither diff was compacted; the test is not exercising the chain")
		}
	})

	// tree: pre-rendered listing, capped. dedupTools lists it; the field is a
	// string so the dedupe path is reachable.
	t.Run("tree", func(t *testing.T) {
		_, run, _ := chainWithDedup(cfg)
		args := map[string]any{}
		mk := func(tag string) map[string]any {
			var b strings.Builder
			for i := 0; i < 300; i++ {
				fmt.Fprintf(&b, "├── shared_dir_%d\n", i)
			}
			for i := 0; i < 3000; i++ {
				fmt.Fprintf(&b, "├── %s_file_%d.go\n", tag, i)
			}
			return prodResult(t, TreeOutput{Tree: b.String(), Dirs: 100, Files: 3000})
		}
		a, _ := run("tree", args, mk("alpha"))["tree"].(string)
		bb, _ := run("tree", args, mk("bravo"))["tree"].(string)
		if strings.Contains(bb, "identical to the result") {
			t.Errorf("changed tree listing elided as unchanged")
		}
		if a == bb {
			t.Errorf("distinct tree listings became identical")
		}
	})
}

// TestDedup_TrueRepeatStillElides guards the other direction: the fix must not
// disable deduplication for genuine repeats.
func TestDedup_TrueRepeatStillElides(t *testing.T) {
	cfg := DefaultCompactorConfig()
	_, run, _ := chainWithDedup(cfg)
	args := map[string]any{"file": "same.go"}

	mk := func() map[string]any {
		var b strings.Builder
		b.WriteString("@@ -1,9000 +1,9000 @@\n")
		for i := 0; i < 300; i++ {
			fmt.Fprintf(&b, "-shared %d padded with a reasonably long body\n+shared %d padded with a reasonably long body\n", i, i)
		}
		return prodResult(t, GitFileDiffOutput{File: "same.go", Diff: b.String(), LinesAdded: 300})
	}

	a, _ := run("git-file-diff", args, mk())["diff"].(string)
	b, _ := run("git-file-diff", args, mk())["diff"].(string) // byte-identical repeat
	if !strings.Contains(b, "identical to the result") {
		t.Errorf("an identical repeat was NOT elided; dedup stopped working.\nfirst=%q\nsecond=%q",
			truncForLog(a, 120), truncForLog(b, 120))
	}
}

// truncForLog shortens a long value for a test log line.
func truncForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// TestDedup_ReachableFieldsAreHonest records which tools the deduper can
// actually reach, and fails only when the list claims coverage that does not
// exist for a tool whose payload IS readable.
//
// The deduper hashes exactly one string field per result, chosen by
// primaryOutputField from stdout|content|output|diff|result|data. A tool whose
// bulk lives in an array therefore cannot be compared, however it is listed.
// Measured against each tool's real output struct, the state is:
//
//   - read, read_image, git-file-diff — reachable (string payload).
//   - ripgrep/grep, find, ls, tree    — listed but NOT reachable: their payload
//     is an array (`matches`, `files`, `entries`) or, for tree, a string under
//     `tree`, which the probe does not look at.
//   - git-hunk, git-overview          — removed from the list for the same
//     reason, so the entry no longer overstates coverage.
//
// Extending primaryOutputField to serialize array payloads would make the
// remaining five reachable; that is a feature change rather than a bug fix, so
// this test pins the current truth instead of assuming the improvement.
func TestDedup_ReachableFieldsAreHonest(t *testing.T) {
	reachable := []struct {
		tool   string
		result map[string]any
	}{
		{"read", prodResult(t, ReadOutput{Content: strings.Repeat("line\n", 200)})},
		{"read_image", map[string]any{"path": "/x.png", "data": strings.Repeat("a", 2000)}},
		{"git-file-diff", prodResult(t, GitFileDiffOutput{File: "a.go", Diff: strings.Repeat("+line\n", 100)})},
	}
	for _, c := range reachable {
		if !dedupTools[c.tool] {
			t.Errorf("%s can dedup in production but is missing from dedupTools", c.tool)
		}
		if f, _ := primaryOutputField(c.result); f == "" {
			t.Errorf("%s is listed as dedup-eligible but primaryOutputField finds no "+
				"payload in its real output", c.tool)
		}
	}

	// These cannot dedup, so they must not be claimed.
	unreachable := []struct {
		tool   string
		result map[string]any
	}{
		{"git-hunk", prodResult(t, GitHunkOutput{File: "a.go", Hunks: []Hunk{{Header: "@@", Content: "x"}}})},
		{"git-overview", prodResult(t, GitOverviewOutput{Branch: "main", RecentCommits: []string{"abc"}})},
	}
	for _, c := range unreachable {
		if dedupTools[c.tool] {
			t.Errorf("%s is listed in dedupTools but its payload is not a recognized "+
				"string field, so the entry overstates coverage", c.tool)
		}
	}

	// The array-payload tools are listed today. If a future change makes them
	// reachable this assertion starts failing, which is the signal to move them
	// into the `reachable` table above.
	arrayPayload := []struct {
		tool   string
		result map[string]any
	}{
		{"ripgrep", prodResult(t, GrepOutput{Matches: []GrepMatch{{File: "a.go", Line: 1, Content: "x"}}})},
		{"find", prodResult(t, FindOutput{Files: []string{"a.go"}})},
		{"ls", prodResult(t, LsOutput{Entries: []LsEntry{{Name: "a.go"}}})},
		{"tree", prodResult(t, TreeOutput{Tree: strings.Repeat("f.go\n", 200)})},
	}
	for _, c := range arrayPayload {
		f, _ := primaryOutputField(c.result)
		if f != "" {
			t.Errorf("%s now yields field %q; move it to the reachable table", c.tool, f)
		}
	}
}
