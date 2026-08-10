package tui

import (
	"strings"
	"testing"
)

// formatterIndex returns the position of a named formatter in the dispatch
// table, failing the test if the name is gone. A rename that loses an entry is
// as much a reorder as moving one.
func formatterIndex(t *testing.T, name string) int {
	t.Helper()
	for i, f := range resultFormatters {
		if f.name == name {
			return i
		}
	}
	t.Fatalf("no formatter named %q in resultFormatters", name)
	return -1
}

// TestResultFormatters_Order pins the pairs whose relative order is load-bearing.
//
// Before the table existed these constraints lived only in the sequence of an
// if-chain, unstated. Inserting a branch in the wrong place silently changed how
// a tool rendered, which is exactly how a running command came to be reported as
// "exit -1".
func TestResultFormatters_Order(t *testing.T) {
	pairs := []struct {
		first, second string
		why           string
	}{
		{
			first: "bash window", second: "bash exit",
			why: "a backgrounded command carries a handle and a -1 exit-code placeholder; the exit shape would call it a crash",
		},
		{
			first: "read content", second: "line count",
			why: "a read result carries both; the content is what the user asked for",
		},
		{
			first: "grep matches", second: "match count",
			why: "a grep result carries both; the matches are what the user asked for",
		},
		{
			first: "find files", second: "file count",
			why: "a find result carries both; the paths are what the user asked for",
		},
	}

	for _, p := range pairs {
		t.Run(p.first+" before "+p.second, func(t *testing.T) {
			if i, j := formatterIndex(t, p.first), formatterIndex(t, p.second); i >= j {
				t.Errorf("%q (index %d) must be probed before %q (index %d): %s", p.first, i, p.second, j, p.why)
			}
		})
	}
}

// TestResultFormatters_WellFormed guards the table itself: a nil probe or format
// panics on the first result that reaches it, and a duplicate name makes the
// ordering test above assert against the wrong entry.
func TestResultFormatters_WellFormed(t *testing.T) {
	seen := make(map[string]struct{}, len(resultFormatters))
	for i, f := range resultFormatters {
		if f.name == "" {
			t.Errorf("formatter at index %d has no name", i)
		}
		if _, dup := seen[f.name]; dup {
			t.Errorf("duplicate formatter name %q", f.name)
		}
		seen[f.name] = struct{}{}
		if f.probe == nil {
			t.Errorf("formatter %q has a nil probe", f.name)
		}
		if f.format == nil {
			t.Errorf("formatter %q has a nil format", f.name)
		}
	}
}

// The ordering constraints, asserted through formatToolResult rather than
// through the table — these are the renderings the order exists to protect, so
// they hold even if the table is replaced by something else entirely.
func TestFormatToolResult_AmbiguousShapesPickTheRightOne(t *testing.T) {
	tests := []struct {
		name     string
		data     map[string]any
		want     string
		wantNot  string
		contains bool
	}{
		{
			// The regression that motivated the whole ordering: a running command
			// has a handle and the -1 placeholder at the same time.
			name: "handle beats exit_code while running",
			data: map[string]any{
				"handle": "bg_9", "running": true,
				"exit_code": float64(-1), "stdout": "go test ./...\n", "elapsed": "3s",
			},
			want: "running (bg_9), 3s elapsed", contains: true,
			wantNot: "exit -1:",
		},
		{
			// Still true once the command has finished: the window reports
			// "exit 0 (bg_9)", the foreground shape would report bare output.
			name: "handle beats exit_code after it finishes",
			data: map[string]any{
				"handle": "bg_9", "running": false,
				"exit_code": float64(0), "stdout": "ok",
			},
			want: "exit 0 (bg_9)", contains: true,
		},
		{
			name: "content beats total_lines",
			data: map[string]any{
				"content": "     1\tpackage main\n", "total_lines": float64(1),
			},
			want: "     1\tpackage main\n",
		},
		{
			name: "matches beat total_matches",
			data: map[string]any{
				"matches":       []any{map[string]any{"file": "a.go", "line": float64(3), "content": "x"}},
				"total_matches": float64(1),
			},
			want: "a.go:3: x",
		},
		{
			name: "files beat total_files",
			data: map[string]any{
				"files": []any{"a.go", "b.go"}, "total_files": float64(2),
			},
			want: "a.go\nb.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatToolResult(tt.data)
			switch {
			case tt.contains && !strings.Contains(got, tt.want):
				t.Errorf("formatToolResult() = %q, want it to contain %q", got, tt.want)
			case !tt.contains && got != tt.want:
				t.Errorf("formatToolResult() = %q, want %q", got, tt.want)
			}
			if tt.wantNot != "" && strings.Contains(got, tt.wantNot) {
				t.Errorf("formatToolResult() = %q, must not contain %q", got, tt.wantNot)
			}
		})
	}
}

// A write result missing its path matches no shape and lands in the raw-JSON
// fallback. That is the pre-existing behavior of the nested if it replaced —
// pinned here because the split probe/format pair makes it easy to "fix" the
// probe to bytes_written alone and start printing a size for a nameless file.
func TestFormatToolResult_WriteWithoutPathFallsThrough(t *testing.T) {
	got := formatToolResult(map[string]any{"bytes_written": float64(10)})

	if strings.Contains(got, "bytes)") {
		t.Errorf("a pathless write must not render as a write summary, got %q", got)
	}
	if !strings.HasPrefix(got, "{") {
		t.Errorf("expected the raw-JSON fallback, got %q", got)
	}
}

// Empty diagnostics mean the file is clean, not that there is a blank line to
// print, so the result falls through to the fallback like any other unknown
// shape.
func TestFormatToolResult_EmptyDiagnosticsFallThrough(t *testing.T) {
	got := formatToolResult(map[string]any{"lsp_diagnostics": "", "file": "main.go"})

	if !strings.HasPrefix(got, "{") {
		t.Errorf("expected the raw-JSON fallback, got %q", got)
	}
}
