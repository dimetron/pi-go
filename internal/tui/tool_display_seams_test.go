package tui

import (
	"strings"
	"testing"
)

func TestClipSummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"short passes through", "hello", "hello"},
		{"exactly at budget is untouched", strings.Repeat("a", summaryWidth), strings.Repeat("a", summaryWidth)},
		{"one over is clipped with ellipsis", strings.Repeat("a", summaryWidth+1), strings.Repeat("a", summaryWidth-3) + "..."},
		{"long is clipped to the budget", strings.Repeat("a", 500), strings.Repeat("a", summaryWidth-3) + "..."},
		{"empty stays empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := clipSummary(tt.in)
			if got != tt.want {
				t.Errorf("clipSummary() = %q, want %q", got, tt.want)
			}
			if len(got) > summaryWidth {
				t.Errorf("clipSummary() len = %d, over budget %d", len(got), summaryWidth)
			}
		})
	}
}

// TestToolCallSummaryDispatch covers the argument-lookup table and the three
// tools that need more than a lookup.
func TestToolCallSummaryDispatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{"read takes file_path", "read", map[string]any{"file_path": "/a/b.go"}, "/a/b.go"},
		{"write takes file_path", "write", map[string]any{"file_path": "/w.go"}, "/w.go"},
		{"edit takes file_path", "edit", map[string]any{"file_path": "/e.go"}, "/e.go"},
		{"bash takes command", "bash", map[string]any{"command": "ls -la"}, "ls -la"},
		{"bash_wait takes handle", "bash_wait", map[string]any{"handle": "bg_1"}, "bg_1"},
		{"bash_output takes handle", "bash_output", map[string]any{"handle": "bg_2"}, "bg_2"},
		{"bash_kill takes handle", "bash_kill", map[string]any{"handle": "bg_3"}, "bg_3"},
		{"grep takes pattern", "grep", map[string]any{"pattern": "foo"}, "foo"},
		{"ripgrep alias takes pattern", "ripgrep", map[string]any{"pattern": "bar"}, "bar"},
		{"find takes pattern", "find", map[string]any{"pattern": "*.go"}, "*.go"},
		{"grounding takes query", groundingToolName, map[string]any{"query": "weather"}, "weather"},
		{"missing arg yields empty", "read", map[string]any{}, ""},
		{"non-string arg yields empty", "read", map[string]any{"file_path": 12}, ""},
		{"unknown tool yields empty", "no_such_tool", map[string]any{"file_path": "/z"}, ""},

		{"ls uses path", "ls", map[string]any{"path": "/tmp"}, "/tmp"},
		{"ls without path defaults to dot", "ls", map[string]any{}, "."},
		{"ls keeps an explicitly empty path", "ls", map[string]any{"path": ""}, ""},
		{"ls with non-string path defaults to dot", "ls", map[string]any{"path": 3}, "."},

		{"tree with depth", "tree", map[string]any{"path": "/x", "depth": float64(2)}, "/x (depth 2)"},
		{"tree without depth", "tree", map[string]any{"path": "/x"}, "/x"},
		{"tree empty path reads as dot", "tree", map[string]any{"path": "", "depth": float64(3)}, ". (depth 3)"},
		{"tree zero depth is omitted", "tree", map[string]any{"depth": float64(0)}, "."},
		{"tree non-numeric depth is omitted", "tree", map[string]any{"depth": "2"}, "."},

		{"agent joins type and prompt", "agent", map[string]any{"type": "explore", "prompt": "find the bug"}, "explore: find the bug"},
		{"agent with type only", "agent", map[string]any{"type": "explore"}, "explore"},
		{"agent with prompt only", "agent", map[string]any{"prompt": "just a prompt"}, "just a prompt"},
		{"agent cuts prompt at first line", "agent", map[string]any{"prompt": "first line\nsecond"}, "first line"},
		{"agent keeps a leading newline", "agent", map[string]any{"prompt": "\nlead"}, "\nlead"},
		{"agent clips a long prompt to 60", "agent", map[string]any{"type": "x", "prompt": strings.Repeat("y", 80)}, "x: " + strings.Repeat("y", 57) + "..."},
		{"agent with neither yields empty", "agent", map[string]any{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := toolCallSummary(tt.tool, tt.args); got != tt.want {
				t.Errorf("toolCallSummary(%q) = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}

// TestResultFormattersClaim checks that each formatter recognizes the result
// shape it owns and produces the expected line.
func TestResultFormattersClaim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		format resultFormatter
		data   map[string]any
		want   string
	}{
		{
			"ls marks directories",
			lsEntriesSummary,
			map[string]any{"entries": []any{map[string]any{"name": "dir", "is_dir": true}, map[string]any{"name": "f.go"}}},
			"dir/  f.go",
		},
		{"ls skips non-map entries", lsEntriesSummary, map[string]any{"entries": []any{"junk"}}, ""},
		{"ls with no entries", lsEntriesSummary, map[string]any{"entries": []any{}}, ""},
		{"tree reports counts", treeCountsSummary, map[string]any{"tree": "x", "dirs": float64(3), "files": float64(9)}, "3 dirs, 9 files"},
		{"tree without counts", treeCountsSummary, map[string]any{"tree": "x"}, "0 dirs, 0 files"},
		{
			"grep lists matches and truncation",
			grepMatchesSummary,
			map[string]any{
				"matches":       []any{map[string]any{"file": "a.go", "line": float64(3), "content": "hit"}},
				"total_matches": float64(9), "truncated": true,
			},
			"a.go:3: hit\n... (9 total matches, truncated)",
		},
		{
			"grep without truncation",
			grepMatchesSummary,
			map[string]any{"matches": []any{map[string]any{"file": "a.go", "line": float64(3), "content": "hit"}}},
			"a.go:3: hit",
		},
		{"grep count only", grepMatchesSummary, map[string]any{"total_matches": float64(5)}, "5 matches"},
		{
			"find lists files and truncation",
			findFilesSummary,
			map[string]any{"files": []any{"a.go", "b.go"}, "total_files": float64(4), "truncated": true},
			"a.go\nb.go\n... (4 total files, truncated)",
		},
		{"find count only", findFilesSummary, map[string]any{"total_files": float64(2)}, "2 files"},
		{
			"read appends the truncation marker",
			readContentSummary,
			map[string]any{"content": "one\ntwo", "total_lines": float64(10), "truncated": true},
			"one\ntwo\n... (10 total lines, truncated)",
		},
		{"read passes content through", readContentSummary, map[string]any{"content": "one"}, "one"},
		{"read count only", readContentSummary, map[string]any{"total_lines": float64(42)}, "42 lines"},
		{"read count only truncated", readContentSummary, map[string]any{"total_lines": float64(42), "truncated": true}, "42 lines (truncated)"},
		{"write names path and size", writeBytesSummary, map[string]any{"bytes_written": float64(120), "path": "/out.txt"}, "/out.txt (120 bytes)"},
		{"edit counts replacements", editReplacementsSummary, map[string]any{"replacements": float64(3)}, "3 replacements"},
		{"diagnostics pass through", diagnosticsSummary, map[string]any{"lsp_diagnostics": "⚠ bad"}, "⚠ bad"},
		{
			"bash window reports a running command",
			bashWindowSummary,
			map[string]any{"handle": "bg_1", "running": true, "elapsed": "3s"},
			"⏳ 3s elapsed\n(no new output)",
		},
		{"bash exit zero shows output", bashExitSummary, map[string]any{"exit_code": float64(0), "stdout": "done"}, "done"},
		{"bash exit zero with no output", bashExitSummary, map[string]any{"exit_code": float64(0)}, "(No output)"},
		{"bash non-zero exit is prefixed", bashExitSummary, map[string]any{"exit_code": float64(2), "stderr": "boom"}, "exit 2: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := tt.format(tt.data)
			if !ok {
				t.Fatalf("formatter declined %v, want it claimed", tt.data)
			}
			if got != tt.want {
				t.Errorf("formatter = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestResultFormattersDecline pins the fall-through cases. A formatter that
// wrongly claims a result stops the chain, so declining matters as much as
// claiming — the empty lsp_diagnostics and empty handle cases in particular
// have to keep falling through to the formatters behind them.
func TestResultFormattersDecline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		format resultFormatter
		data   map[string]any
	}{
		{"ls without entries", lsEntriesSummary, map[string]any{"tree": "x"}},
		{"tree with a non-string tree", treeCountsSummary, map[string]any{"tree": 7}},
		{"grep with a non-numeric count", grepMatchesSummary, map[string]any{"total_matches": "5"}},
		{"find with neither field", findFilesSummary, map[string]any{"content": "x"}},
		{"read with neither field", readContentSummary, map[string]any{"replacements": float64(1)}},
		{"write without a path", writeBytesSummary, map[string]any{"bytes_written": float64(120)}},
		{"write with a non-string path", writeBytesSummary, map[string]any{"bytes_written": float64(1), "path": 2}},
		{"edit without replacements", editReplacementsSummary, map[string]any{"exit_code": float64(0)}},
		{"empty diagnostics", diagnosticsSummary, map[string]any{"lsp_diagnostics": ""}},
		{"missing diagnostics", diagnosticsSummary, map[string]any{}},
		{"empty handle", bashWindowSummary, map[string]any{"handle": "", "exit_code": float64(1)}},
		{"missing handle", bashWindowSummary, map[string]any{"exit_code": float64(1)}},
		{"bash without an exit code", bashExitSummary, map[string]any{"stdout": "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got, ok := tt.format(tt.data); ok {
				t.Errorf("formatter claimed %v as %q, want declined", tt.data, got)
			}
		})
	}
}

// TestFormatToolResultOrder pins the two orderings the chain depends on: a
// full payload beats the bare count that stands in for it, and a handle beats
// an exit code so a still-running command is never rendered as "exit -1".
func TestFormatToolResultOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data map[string]any
		want string
	}{
		{
			"match list beats match count",
			map[string]any{"matches": []any{map[string]any{"file": "a.go", "line": float64(1), "content": "x"}}, "total_matches": float64(7)},
			"a.go:1: x",
		},
		{
			"file list beats file count",
			map[string]any{"files": []any{"a.go"}, "total_files": float64(7)},
			"a.go",
		},
		{
			"content beats line count",
			map[string]any{"content": "body", "total_lines": float64(7)},
			"body",
		},
		{
			"handle beats the -1 exit placeholder",
			map[string]any{"handle": "bg_1", "running": true, "exit_code": float64(-1), "stdout": "building"},
			"⏳\nbuilding",
		},
		{
			"a write with no path falls through to the edit count",
			map[string]any{"bytes_written": float64(9), "replacements": float64(3)},
			"3 replacements",
		},
		{
			"empty diagnostics fall through to the exit code",
			map[string]any{"lsp_diagnostics": "", "exit_code": float64(0), "stdout": "out"},
			"out",
		},
		{
			"an unrecognized result falls back to compact JSON",
			map[string]any{"mystery": "shape"},
			`{"mystery":"shape"}`,
		},
		{
			"the JSON fallback is clipped",
			map[string]any{"mystery": strings.Repeat("q", 300)},
			`{"mystery":"` + strings.Repeat("q", summaryWidth-3-len(`{"mystery":"`)) + "...",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := formatToolResult(tt.data); got != tt.want {
				t.Errorf("formatToolResult() = %q, want %q", got, tt.want)
			}
		})
	}
}
