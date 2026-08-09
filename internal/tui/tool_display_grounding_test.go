package tui

import (
	"regexp"
	"testing"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// The synthetic grounding tool call is summarized by the query it searched for,
// the same way grep is summarized by its pattern. Without this the chat shows a
// bare "google_search" and never says what was searched.
func TestToolCallSummaryGrounding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			name: "query is the summary",
			args: map[string]any{"query": "who won the 2026 world cup"},
			want: "who won the 2026 world cup",
		},
		{
			name: "no query yields no summary",
			args: map[string]any{},
			want: "",
		},
		{
			name: "non-string query yields no summary",
			args: map[string]any{"query": 42},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := toolCallSummary(groundingToolName, tt.args); got != tt.want {
				t.Errorf("toolCallSummary(%q, %v) = %q, want %q",
					groundingToolName, tt.args, got, tt.want)
			}
		})
	}
}

// Bash output is heterogeneous, so the lexer often fails to tokenise it. Either
// way every input line must come back as exactly one output line — the renderer
// pairs these against the originals, so dropping or merging a line corrupts the
// block.
func TestHighlightBashOutputPreservesLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		lines []string
	}{
		{
			name:  "unlexable program output",
			lines: []string{">>> ok 1", ">>> ok 2", ">>> done"},
		},
		{
			name:  "source-like output the sniffer can lex",
			lines: []string{"package main", "", "func main() {}"},
		},
		{
			name:  "single line",
			lines: []string{"hello"},
		},
		{
			name:  "blank lines are kept",
			lines: []string{"a", "", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := highlightBashOutput(tt.lines, darkPalette)

			if len(got) != len(tt.lines) {
				t.Fatalf("got %d lines, want %d: %q", len(got), len(tt.lines), got)
			}
			for i, line := range got {
				if plain := ansiRE.ReplaceAllString(line, ""); plain != tt.lines[i] {
					t.Errorf("line %d = %q (plain %q), want %q", i, line, plain, tt.lines[i])
				}
			}
		})
	}
}

func TestHighlightBashOutputEmpty(t *testing.T) {
	t.Parallel()

	if got := highlightBashOutput(nil, darkPalette); len(got) != 0 {
		t.Errorf("highlightBashOutput(nil, darkPalette) = %q, want empty", got)
	}
}
