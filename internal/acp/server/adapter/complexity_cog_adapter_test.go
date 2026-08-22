package adapter

import (
	"sort"
	"strings"
	"testing"
)

// These tests pin formatArgsForDisplay exactly: which key wins, where each
// truncation boundary falls, and what the map-iteration fallback emits. The
// expectations were captured by running the same cases against the
// pre-refactor implementation, so a passing run proves the flattening changed
// no output.
//
// The fallback path iterates a Go map, so its ordering is deliberately not
// asserted — only its contents and its three-item ceiling.

func TestFormatArgsForDisplayPriorityKeys(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"nil args", nil, ""},
		{"empty args", map[string]any{}, ""},
		{"path", map[string]any{"path": "/tmp/a.go"}, "/tmp/a.go"},
		{"file_path", map[string]any{"file_path": "/tmp/b.go"}, "/tmp/b.go"},
		{"command", map[string]any{"command": "git status"}, "git status"},
		{"cmd", map[string]any{"cmd": "ls -la"}, "ls -la"},
		{"prompt", map[string]any{"prompt": "explain"}, "explain"},
		{"query", map[string]any{"query": "func main"}, "func main"},
		{"pattern", map[string]any{"pattern": "TODO"}, "TODO"},
		{"name", map[string]any{"name": "explore"}, "explore"},
		{"url", map[string]any{"url": "https://x"}, "https://x"},
		{"description", map[string]any{"description": "a task"}, "a task"},
		{
			name: "path outranks every later priority key",
			args: map[string]any{"description": "d", "url": "u", "path": "p", "command": "c"},
			want: "p",
		},
		{
			name: "file_path outranks command when path is absent",
			args: map[string]any{"command": "c", "file_path": "f"},
			want: "f",
		},
		{
			name: "command outranks cmd",
			args: map[string]any{"cmd": "second", "command": "first"},
			want: "first",
		},
		{
			name: "an empty priority value is skipped for the next priority key",
			args: map[string]any{"path": "", "command": "fallback-cmd"},
			want: "fallback-cmd",
		},
		{
			name: "a non-string priority value is skipped for the next priority key",
			args: map[string]any{"path": 42, "command": "still-here"},
			want: "still-here",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatArgsForDisplay(tt.args); got != tt.want {
				t.Errorf("formatArgsForDisplay = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatArgsForDisplayPriorityTruncationBoundary(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"49 bytes is untouched", strings.Repeat("a", 49), strings.Repeat("a", 49)},
		{"50 bytes is untouched", strings.Repeat("a", 50), strings.Repeat("a", 50)},
		{"51 bytes clips to 47 plus an ellipsis", strings.Repeat("a", 51), strings.Repeat("a", 47) + "..."},
		{"a long value clips to the same 50-byte result", strings.Repeat("a", 400), strings.Repeat("a", 47) + "..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatArgsForDisplay(map[string]any{"path": tt.in})
			if got != tt.want {
				t.Errorf("formatArgsForDisplay len=%d = %q, want %q", len(tt.in), got, tt.want)
			}
		})
	}
}

func TestFormatArgsForDisplayFallbackSingleKey(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			name: "one non-priority string key becomes key=value",
			args: map[string]any{"zz_custom": "hello"},
			want: "zz_custom=hello",
		},
		{
			name: "30 bytes is untouched",
			args: map[string]any{"zz": strings.Repeat("b", 30)},
			want: "zz=" + strings.Repeat("b", 30),
		},
		{
			name: "31 bytes clips to 27 plus an ellipsis",
			args: map[string]any{"zz": strings.Repeat("b", 31)},
			want: "zz=" + strings.Repeat("b", 27) + "...",
		},
		{
			name: "an empty value contributes nothing",
			args: map[string]any{"zz": ""},
			want: "",
		},
		{
			name: "non-string values are skipped entirely",
			args: map[string]any{"num": 42},
			want: "",
		},
		{
			name: "a nil value is skipped entirely",
			args: map[string]any{"nothing": nil},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatArgsForDisplay(tt.args); got != tt.want {
				t.Errorf("formatArgsForDisplay = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestFormatArgsForDisplayFallbackJoinsUpToThree pins the shape of the
// fallback join without depending on Go's randomized map ordering: three
// entries produce exactly those three pairs, in some order, joined by ", ".
func TestFormatArgsForDisplayFallbackJoinsUpToThree(t *testing.T) {
	got := formatArgsForDisplay(map[string]any{"aa": "1", "bb": "2", "cc": "3"})
	parts := strings.Split(got, ", ")
	sort.Strings(parts)
	want := []string{"aa=1", "bb=2", "cc=3"}
	if len(parts) != len(want) {
		t.Fatalf("formatArgsForDisplay = %q, want three pairs", got)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("formatArgsForDisplay = %q, sorted parts %v, want %v", got, parts, want)
		}
	}
}

// TestFormatArgsForDisplayFallbackStopsAfterThreeEntries pins the ceiling: the
// fallback visits at most three map entries, so it can never emit a fourth
// pair however many keys are supplied.
func TestFormatArgsForDisplayFallbackStopsAfterThreeEntries(t *testing.T) {
	args := map[string]any{"aa": "1", "bb": "2", "cc": "3", "dd": "4", "ee": "5", "ff": "6"}
	for i := 0; i < 20; i++ {
		got := formatArgsForDisplay(args)
		if n := len(strings.Split(got, ", ")); n > 3 {
			t.Fatalf("formatArgsForDisplay = %q, emitted %d pairs, want at most 3", got, n)
		}
	}
}

// TestFormatArgsForDisplayFallbackCountsSkippedEntries pins a subtle
// pre-existing property: the three-entry budget is spent on every visited map
// entry, including non-string ones that emit nothing. With four non-string
// entries and one string entry, the string may or may not be reached, so the
// only invariant is that the result is either empty or exactly that one pair.
func TestFormatArgsForDisplayFallbackCountsSkippedEntries(t *testing.T) {
	args := map[string]any{"n1": 1, "n2": 2, "n3": 3, "n4": 4, "zz": "seen"}
	for i := 0; i < 50; i++ {
		got := formatArgsForDisplay(args)
		if got != "" && got != "zz=seen" {
			t.Fatalf("formatArgsForDisplay = %q, want %q or empty", got, "zz=seen")
		}
	}
}

// TestBuildTitleFallsBackThroughFormatArgs pins buildTitle's use of
// formatArgsForDisplay: the summary is appended to the tool name whenever the
// name-specific rule finds nothing.
func TestBuildTitleFallsBackThroughFormatArgs(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		args     map[string]any
		want     string
	}{
		{"unknown tool with a priority key", "custom", map[string]any{"url": "https://x"}, "custom https://x"},
		{"unknown tool with no args", "custom", nil, "custom"},
		{"unknown tool with only non-string args", "custom", map[string]any{"n": 1}, "custom"},
		{"bash without a command falls back to the summary", "bash", map[string]any{"url": "https://x"}, "bash https://x"},
		{"read without a path falls back to the summary", "read", map[string]any{"url": "https://x"}, "read https://x"},
		{"grep without a pattern falls back to the summary", "grep", map[string]any{"url": "https://x"}, "grep https://x"},
		{"subagent without an agent falls back to the summary", "subagent", map[string]any{"url": "https://x"}, "subagent https://x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildTitle(tt.toolName, tt.args); got != tt.want {
				t.Errorf("buildTitle = %q, want %q", got, tt.want)
			}
		})
	}
}
