package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The search-family pipelines read the fields their tools actually emit.
//
// ADK converts a tool's typed output struct to map[string]any by marshaling it
// to JSON and unmarshaling into a map (adk v2.4.0
// internal/typeutil/convert.go). A []GrepMatch therefore arrives as []any of
// map[string]any, and a []string as []any of string — never as the Go slice
// type. These pipelines cap the array and keep its type, because the same keys
// are re-read by the TUI result summaries (internal/tui/tool_display.go:897-984)
// and handed to the model. Re-rendering an array to a string would change the
// shape both of those depend on.
//
// Each pipeline reports nil when it would not shrink the result. That is the
// never-worse guard rtk applies to every filter (tmp/rtk/src/core/guard.rs):
// a "compaction" that costs more than the original must not be applied.

// capArray truncates an []any to at most max items, reporting whether it cut.
func capArray(v any, max int) ([]any, bool) {
	arr, ok := v.([]any)
	if !ok || max <= 0 || len(arr) <= max {
		return arr, false
	}
	return arr[:max], true
}

// jsonLen is the encoded byte size of a result map — the bytes the model is
// billed for, since ADK json.Marshals the response before it reaches the prompt
// (adk internal/llminternal/contents_processor.go, stringify).
func jsonLen(m map[string]any) int {
	b, err := json.Marshal(m)
	if err != nil {
		return 0
	}
	return len(b)
}

// measureCompaction reports the encoded size before and after applying writes,
// without mutating result. Measuring the actual writes keeps the reported saving
// honest even when a write adds a field (truncated) as well as removing one.
func measureCompaction(result map[string]any, writes []CompactWrite) (origSize, compSize int) {
	origSize = jsonLen(result)

	view := make(map[string]any, len(result))
	for k, v := range result {
		view[k] = v
	}
	for _, w := range writes {
		view[w.Key] = w.Value
	}
	return origSize, jsonLen(view)
}

// capResult builds a CompactResult for a pure array cap, or nil when nothing
// would be removed.
//
// totalField and truncField are the tool's own count/flag keys, preserved so the
// caller can still tell a partial list from a complete one — the rtk invariant
// that the reported total is counted before the cap, never after
// (tmp/rtk/src/cmds/system/search.rs, test_grep_overflow_uses_uncapped_total).
//
// truncField is written unconditionally, not only when already present: these
// structs declare `truncated` with omitempty, so a complete result carries no
// such key. Writing it is what stops a capped list from reading as the whole
// answer — the one case where dropping items without a marker would be a
// correctness bug rather than a saving.
func capResult(result map[string]any, key, totalField, truncField, technique string, max int) *CompactResult {
	arr, ok := result[key].([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	capped, cut := capArray(arr, max)
	if !cut {
		return nil
	}

	writes := []CompactWrite{
		{Key: key, Value: capped},
		{Key: truncField, Value: true},
	}

	origSize, compSize := measureCompaction(result, writes)
	if compSize >= origSize {
		return nil // never-worse guard
	}
	return &CompactResult{
		Writes:     writes,
		Techniques: []string{technique},
		OrigSize:   origSize,
		CompSize:   compSize,
	}
}

// compactGrep caps the matches array of a grep/ripgrep result.
func compactGrep(result, _ map[string]any, cfg CompactorConfig) *CompactResult {
	return capResult(result, "matches", "total_matches", "truncated", "search-cap", cfg.MaxSearchTotal)
}

// compactFind caps the files array of a find result.
func compactFind(result, _ map[string]any, cfg CompactorConfig) *CompactResult {
	return capResult(result, "files", "total_files", "truncated", "find-cap", cfg.MaxSearchTotal)
}

// compactLs caps the entries array of an ls result.
func compactLs(result, _ map[string]any, cfg CompactorConfig) *CompactResult {
	return capResult(result, "entries", "total_entries", "truncated", "ls-cap", cfg.MaxSearchTotal)
}

// compactTree caps the tree listing.
//
// tree emits one pre-rendered string, so this is a text cap rather than an array
// cap. It reads "tree" — not "files" — so it no longer shares compactFind's
// failure mode, which is what made the old compactTree dead whenever compactFind
// was.
func compactTree(result, _ map[string]any, cfg CompactorConfig) *CompactResult {
	tree, ok := result["tree"].(string)
	if !ok || tree == "" {
		return nil
	}

	origSize := len(tree)
	var techniques []string

	tree = runStage(tree, &techniques, "hard-truncate", func(s string) (string, bool) {
		return hardTruncate(s, cfg.MaxChars)
	})
	tree = runStage(tree, &techniques, "hard-truncate-lines", func(s string) (string, bool) {
		return hardTruncateLines(s, cfg.MaxLines)
	})
	techniques = dedup(techniques)

	if len(tree) >= origSize {
		return nil // never-worse guard
	}

	return &CompactResult{
		Writes:     []CompactWrite{{Key: "tree", Value: tree}},
		Techniques: techniques,
		OrigSize:   origSize,
		CompSize:   len(tree),
	}
}

// groupSearchOutput groups search results by file with match counts.
func groupSearchOutput(s string, cfg CompactorConfig) (string, bool) {
	lines := strings.Split(s, "\n")
	if len(lines) < 20 {
		return s, false
	}

	byFile, fileOrder := groupSearchLinesByFile(lines)
	if len(byFile) == 0 {
		return s, false
	}

	var b strings.Builder
	totalShown := 0
	for _, file := range fileOrder {
		totalShown = writeSearchFileGroup(&b, file, byFile[file], totalShown, cfg)
		if totalShown >= cfg.MaxSearchTotal {
			fmt.Fprintf(&b, "\n... (%d total matches shown, limited to %d)\n",
				totalShown, cfg.MaxSearchTotal)
			break
		}
	}

	result := b.String()
	if len(result) >= len(s) {
		return s, false
	}
	return result, true
}

// groupSearchLinesByFile buckets search result lines by their file prefix
// (the file:line:content or file:content pattern), returning the buckets and
// the order in which the files were first seen. Lines that are empty or carry
// no colon are dropped.
func groupSearchLinesByFile(lines []string) (map[string][]string, []string) {
	byFile := make(map[string][]string)
	var fileOrder []string

	for _, line := range lines {
		if line == "" {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		file := line[:idx]
		if _, seen := byFile[file]; !seen {
			fileOrder = append(fileOrder, file)
		}
		byFile[file] = append(byFile[file], line)
	}

	return byFile, fileOrder
}

// writeSearchFileGroup writes one file's header and matches to b, honoring the
// per-file and running limits, and returns the updated running total.
func writeSearchFileGroup(b *strings.Builder, file string, matches []string, totalShown int, cfg CompactorConfig) int {
	fmt.Fprintf(b, "%s (%d matches):\n", file, len(matches))

	shown := 0
	for _, m := range matches {
		if totalShown >= cfg.MaxSearchTotal {
			break
		}
		if shown >= cfg.MaxSearchPerFile {
			fmt.Fprintf(b, "  ... and %d more matches\n", len(matches)-shown)
			break
		}
		b.WriteString("  ")
		b.WriteString(stripSearchLinePrefix(m))
		b.WriteString("\n")
		shown++
		totalShown++
	}

	return totalShown
}

// stripSearchLinePrefix drops the leading file: prefix from a match line for
// cleaner grouped output, returning the line unchanged when it has no colon.
func stripSearchLinePrefix(m string) string {
	if idx := strings.Index(m, ":"); idx >= 0 {
		return m[idx+1:]
	}
	return m
}
