package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// This file is the behavior-preservation evidence for the cyclomatic-complexity
// refactor of the four render hotspots in this package: renderMessages,
// renderRegularTool / renderAgentTool, toolCallSummary / formatToolResult,
// StatusModel.Render and blankFast.
//
// Two kinds of test live here.
//
//   - renderGoldenCorpus + TestRenderGoldenDump produce the exact bytes every
//     one of those paths emits across 350 input combinations. Dumping the
//     corpus before and after the refactor and diffing it is what proved the
//     refactor is a no-op; the dump stays as the tool for the next one.
//   - The table-driven tests pin the boundaries the original branch structure
//     encoded, one table per extracted helper, so a branch that goes missing
//     fails a named case rather than shifting a wall of ANSI.

// ---------------------------------------------------------------------------
// Golden corpus
// ---------------------------------------------------------------------------

// renderGoldenCorpus produces the exact bytes every render path under test
// emits, keyed by case name.
func renderGoldenCorpus() map[string]string {
	out := map[string]string{}

	for _, w := range []int{10, 40, 120} {
		for name, msgs := range renderGoldenMessageSets() {
			for _, running := range []bool{false, true} {
				c := NewChatModel(nil)
				c.Width = w
				c.Messages = append(c.Messages, msgs...)
				text, kinds := c.renderMessages(running)
				out[fmt.Sprintf("chat/%s/w=%d/run=%v", name, w, running)] = fmt.Sprintf("%s\n--kinds--\n%v", text, kinds)
			}
		}
	}

	for _, w := range []int{0, 40, 200} {
		for _, compact := range []bool{false, true} {
			for _, blink := range []bool{false, true} {
				td := ToolDisplayModel{Width: w, CompactTools: compact, BlinkOn: blink}
				for name, msg := range renderGoldenToolMessages() {
					key := fmt.Sprintf("tool/%s/w=%d/compact=%v/blink=%v", name, w, compact, blink)
					out[key] = td.RenderToolMessage(msg)
				}
			}
		}
	}

	for name, tc := range renderGoldenStatusCases() {
		out["status/"+name] = tc.model.Render(tc.in)
	}

	for i, tc := range renderGoldenSummaryArgs() {
		out[fmt.Sprintf("summary/%02d/%s", i, tc.name)] = toolCallSummary(tc.name, tc.args)
	}
	for i, data := range renderGoldenResultData() {
		out[fmt.Sprintf("result/%02d", i)] = formatToolResult(data)
	}
	for i, s := range renderGoldenBlankLines() {
		out[fmt.Sprintf("blank/%02d", i)] = fmt.Sprintf("%v", blankFast(s))
	}
	return out
}

func renderGoldenMessageSets() map[string][]message {
	return map[string][]message{
		"empty": nil,
		"mixed": {
			{role: "user", content: "hello there this is a fairly long user line that must wrap somewhere"},
			{role: "assistant", content: "plain **assistant** reply"},
			{role: "user", content: "second"},
			{role: "thinking", content: "a\nb\nc\nd\ne\nf\ng\nh"},
			{role: "assistant", content: "boom: {\"error\":\"a very long provider error body that wraps\"}", isError: true},
			{role: "assistant", content: "careful", isWarning: true},
			{role: "assistant", content: "12 in, 34 out", isMeta: true},
			{role: "assistant", content: "\x1b[31mpre\x1b[0m", preRendered: true},
			{role: "tool", tool: "bash", toolIn: "ls -la", content: "a\nb"},
			{role: "assistant", content: ""},
			{role: "", content: "unknown role"},
		},
		"emptythink": {
			{role: "thinking", content: ""},
			{role: "assistant", content: ""},
		},
		"single-user": {
			{role: "user", content: "only"},
		},
	}
}

func renderGoldenToolMessages() map[string]message {
	long := strings.Repeat("x", 300)
	manyLines := make([]string, 0, 30)
	for i := range 30 {
		manyLines = append(manyLines, fmt.Sprintf("line %d", i))
	}
	body := strings.Join(manyLines, "\n")

	return map[string]message{
		"skill":      {role: "tool", tool: "skill", toolIn: "disk-check", content: "loaded"},
		"skill-bare": {role: "tool", tool: "skill"},
		"read":       {role: "tool", tool: "read", toolIn: "/tmp/x.go", content: "package main\nfunc main() {}"},
		"bash":       {role: "tool", tool: "bash", toolIn: "go test ./...", content: body},
		"bash-wait":  {role: "tool", tool: "bash_wait", toolIn: "bg_1", content: "still going", pollCount: 5},
		"grep":       {role: "tool", tool: "grep", toolIn: "foo", content: "a.go:1: foo\nb.go:2: foo"},
		"ripgrep":    {role: "tool", tool: "ripgrep", toolIn: "foo", content: "a.go:1: foo"},
		"find":       {role: "tool", tool: "find", toolIn: "*.go", content: "a.go\nb.go"},
		"other":      {role: "tool", tool: "ls", toolIn: ".", content: "a\nb\nc"},
		"pending":    {role: "tool", tool: "bash", toolIn: "sleep 1"},
		"longargs":   {role: "tool", tool: "bash", toolIn: long, content: "ok"},
		"jsonresult": {role: "tool", tool: "ls", toolIn: ".", content: `{"entries":[{"name":"a","is_dir":true},{"name":"b"}]}`},
		"live": {role: "tool", tool: "bash", toolIn: "make", agentEvents: []agentEv{
			{kind: "output", content: "compiling"},
			{kind: "stderr", content: "warn: x"},
			{kind: "output", content: "o1"}, {kind: "output", content: "o2"},
			{kind: "output", content: "o3"}, {kind: "output", content: "o4"},
			{kind: "heartbeat", content: "1m30s — no output"},
		}},
		"agent-empty": {role: "tool", tool: "agent", agentType: "", agentTitle: ""},
		"agent-basic": {role: "tool", tool: "agent", agentType: "claude", agentTitle: "look at x", content: "done\nwith it"},
		"agent-multi": {role: "tool", tool: "subagent", agentType: "claude+explore+task+gemini", agentTitle: "t", agentEvents: []agentEv{
			{kind: "spawn", content: "s"},
			{kind: "message_start"},
			{kind: "text", content: "   "},
			{kind: "tool_call", content: "read\n\nfoo"},
			{kind: "tool_result", content: `{"total_lines": 4}`},
			{kind: "stderr", content: strings.Repeat("e", 200)},
			{kind: "text_delta", content: "hello\n\nworld"},
			{kind: "thinking_delta", content: "hmm"},
			{kind: "custom", content: "c"},
			{kind: "custom", content: ""},
			{kind: "message_end"},
			{kind: "done"},
		}},
		"agent-onebig": {role: "tool", tool: "agent", agentType: "cursor", agentTitle: "big", agentEvents: []agentEv{
			{kind: "text", content: strings.Repeat("word ", 200)},
		}},
		"agent-longsum": {role: "tool", tool: "agent", agentType: "gemini", content: long},
	}
}

type renderGoldenStatusCase struct {
	model StatusModel
	in    StatusRenderInput
}

func renderGoldenStatusCases() map[string]renderGoldenStatusCase {
	fixed := time.Now()
	return map[string]renderGoldenStatusCase{
		"bare":  {StatusModel{Width: 100}, StatusRenderInput{}},
		"flash": {StatusModel{Width: 100}, StatusRenderInput{Flash: "Copied!", Mode: "plan"}},
		"plan":  {StatusModel{Width: 100}, StatusRenderInput{Mode: "plan"}},
		"loading": {StatusModel{Width: 100}, StatusRenderInput{
			LoadingItems: map[string]bool{"mcp": true, "lsp": false, "skills": true},
			Pending:      3,
		}},
		"loading-empty": {StatusModel{Width: 100}, StatusRenderInput{LoadingItems: map[string]bool{}}},
		"queued":        {StatusModel{Width: 100}, StatusRenderInput{Pending: 2}},
		"ctx-small":     {StatusModel{Width: 100}, StatusRenderInput{Messages: []message{{role: "user", content: "hi"}}}},
		"ctx-big": {StatusModel{Width: 100}, StatusRenderInput{
			Messages: []message{{role: "user", content: strings.Repeat("word ", 3000)}},
		}},
		"tkn-green":  {StatusModel{Width: 100}, StatusRenderInput{TokenTracker: flexTokenTracker{limit: 1000, totalUsed: 100, percentUsed: 10}}},
		"tkn-orange": {StatusModel{Width: 100}, StatusRenderInput{TokenTracker: flexTokenTracker{limit: 1000, totalUsed: 850, percentUsed: 85}}},
		"tkn-red":    {StatusModel{Width: 100}, StatusRenderInput{TokenTracker: flexTokenTracker{limit: 1000, totalUsed: 1500, percentUsed: 150}}},
		"tkn-nolimit": {StatusModel{Width: 100}, StatusRenderInput{
			TokenTracker: flexTokenTracker{limit: 0, totalUsed: 4321},
		}},
		"tkn-nolimit-zero": {StatusModel{Width: 100}, StatusRenderInput{TokenTracker: flexTokenTracker{}}},
		"loc-both":         {StatusModel{Width: 100}, StatusRenderInput{FolderName: "pi-go", HostName: "mac"}},
		"loc-dir":          {StatusModel{Width: 100}, StatusRenderInput{FolderName: "pi-go"}},
		"loc-host":         {StatusModel{Width: 100}, StatusRenderInput{HostName: "mac"}},
		"tools-many":       {StatusModel{Width: 200, ActiveTools: map[string]time.Time{"b": fixed, "a": fixed}}, StatusRenderInput{}},
		"tools-one-map":    {StatusModel{Width: 200, ActiveTools: map[string]time.Time{"a": fixed}, ActiveTool: ""}, StatusRenderInput{}},
		"runcycle":         {StatusModel{Width: 100}, StatusRenderInput{RunCycle: &runCycleInfo{SpecName: "s", Cycle: 2, MaxRetries: 5}}},
		"running-tool":     {StatusModel{Width: 100, ActiveTool: ""}, StatusRenderInput{Running: false}},
	}
}

type renderGoldenSummaryCase struct {
	name string
	args map[string]any
}

func renderGoldenSummaryArgs() []renderGoldenSummaryCase {
	return []renderGoldenSummaryCase{
		{"read", map[string]any{"file_path": "/a/b.go"}},
		{"read", map[string]any{}},
		{"read", map[string]any{"file_path": 7}},
		{"write", map[string]any{"file_path": "/w.go"}},
		{"write", nil},
		{"edit", map[string]any{"file_path": "/e.go"}},
		{"edit", map[string]any{"other": "x"}},
		{"bash", map[string]any{"command": "ls -la"}},
		{"bash", map[string]any{}},
		{"bash_wait", map[string]any{"handle": "bg_1"}},
		{"bash_output", map[string]any{"handle": "bg_2"}},
		{"bash_kill", map[string]any{"handle": "bg_3"}},
		{"bash_kill", map[string]any{}},
		{"grep", map[string]any{"pattern": "foo"}},
		{"ripgrep", map[string]any{"pattern": "bar"}},
		{"ripgrep", map[string]any{}},
		{groundingToolName, map[string]any{"query": "who is x"}},
		{groundingToolName, map[string]any{}},
		{"find", map[string]any{"pattern": "*.go"}},
		{"find", map[string]any{}},
		{"ls", map[string]any{"path": "/tmp"}},
		{"ls", map[string]any{}},
		{"ls", map[string]any{"path": ""}},
		{"ls", map[string]any{"path": 3}},
		{"tree", map[string]any{"path": "/t", "depth": 3.0}},
		{"tree", map[string]any{"path": "/t"}},
		{"tree", map[string]any{}},
		{"tree", map[string]any{"path": "", "depth": 0.0}},
		{"tree", map[string]any{"depth": -1.0}},
		{"agent", map[string]any{"type": "explore", "prompt": "find the thing"}},
		{"agent", map[string]any{"type": "explore"}},
		{"agent", map[string]any{"prompt": "just a prompt"}},
		{"agent", map[string]any{}},
		{"agent", map[string]any{"type": "t", "prompt": "first line\nsecond line"}},
		{"agent", map[string]any{"type": "t", "prompt": "\nleading newline"}},
		{"agent", map[string]any{"type": "t", "prompt": strings.Repeat("p", 100)}},
		{"unknown", map[string]any{"x": "y"}},
		{"", nil},
	}
}

func renderGoldenResultData() []map[string]any {
	return []map[string]any{
		{"entries": []any{map[string]any{"name": "a", "is_dir": true}, map[string]any{"name": "b"}, "junk"}},
		{"entries": []any{}},
		{"entries": []any{map[string]any{"name": strings.Repeat("n", 200)}}},
		{"tree": "x", "dirs": 2.0, "files": 3.0},
		{"tree": "x"},
		{"matches": []any{map[string]any{"file": "a.go", "line": 3.0, "content": "hit"}, 1}, "total_matches": 9.0, "truncated": true},
		{"matches": []any{}, "total_matches": 2.0},
		{"total_matches": 4.0},
		{"files": []any{"a.go", "b.go", 3}, "total_files": 7.0, "truncated": true},
		{"files": []any{}},
		{"total_files": 12.0},
		{"content": "hello", "total_lines": 3.0, "truncated": true},
		{"content": "hello"},
		{"total_lines": 8.0, "truncated": true},
		{"total_lines": 8.0},
		{"total_lines": 8.0, "truncated": false},
		{"bytes_written": 42.0, "path": "/p"},
		{"bytes_written": 42.0},
		{"replacements": 3.0},
		{"lsp_diagnostics": "⚠ boom"},
		{"lsp_diagnostics": ""},
		{"handle": "bg_1", "running": true, "stdout": "out", "stderr": "err"},
		{"handle": "", "exit_code": 0.0, "stdout": "hi"},
		{"exit_code": 0.0, "stdout": "hi", "stderr": ""},
		{"exit_code": 1.0, "stdout": "", "stderr": "bad"},
		{"exit_code": 0.0},
		{"unknown": "value"},
		{"unknown": strings.Repeat("v", 300)},
		{},
	}
}

func renderGoldenBlankLines() []string {
	return []string{
		"", " ", "\t\v\f\r ", "x", " x ",
		"\x1b[0m", "\x1b[38;5;245m\x1b[0m", "\x1b[38;5;245m▌\x1b[0m",
		"\x1b[", "\x1b[3", "\x1b", "\x1bX0", "\x1bX\xd0", "\x1b[ 0A", "\x1b[\x1c",
		" ", "\xa0", " ", " x", "▌", "😀",
		"\x1b]8;;http://x\x07", "\x1b[38;5;245m   \x1b[0m\t",
	}
}

// TestRenderGoldenDump writes the corpus to the file named by
// PI_RENDER_GOLDEN_OUT. Without the variable it is a no-op; with it, dump the
// corpus on both sides of a change and diff the two files to prove the change
// is invisible to the terminal.
func TestRenderGoldenDump(t *testing.T) {
	path := os.Getenv("PI_RENDER_GOLDEN_OUT")
	if path == "" {
		t.Skip("PI_RENDER_GOLDEN_OUT not set")
	}
	corpus := renderGoldenCorpus()
	keys := make([]string, 0, len(corpus))
	for k := range corpus {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "===== %s =====\n%s\n", k, corpus[k])
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRenderGoldenCorpusCovers walks the whole corpus so every render path it
// names executes at least once, and guards its size: a corpus that silently
// shrinks stops being evidence.
func TestRenderGoldenCorpusCovers(t *testing.T) {
	corpus := renderGoldenCorpus()
	if len(corpus) < 350 {
		t.Fatalf("corpus has %d cases, want at least 350", len(corpus))
	}
	for name, out := range corpus {
		if strings.Contains(out, "%!") {
			t.Errorf("%s: bad format verb in output: %q", name, out)
		}
	}
}

// ---------------------------------------------------------------------------
// toolCallSummary and its extracted helpers
// ---------------------------------------------------------------------------

// TestToolCallSummaryRender pins every branch the original switch encoded,
// including the ones the table lookup now serves: a missing key and a key
// holding a non-string both summarize as "".
func TestToolCallSummaryRender(t *testing.T) {
	tests := []struct {
		desc string
		tool string
		args map[string]any
		want string
	}{
		{"read path", "read", map[string]any{"file_path": "/a/b.go"}, "/a/b.go"},
		{"read missing", "read", map[string]any{}, ""},
		{"read wrong type", "read", map[string]any{"file_path": 7}, ""},
		{"read nil args", "read", nil, ""},
		{"write path", "write", map[string]any{"file_path": "/w.go"}, "/w.go"},
		{"edit path", "edit", map[string]any{"file_path": "/e.go"}, "/e.go"},
		{"edit other key", "edit", map[string]any{"other": "x"}, ""},
		{"bash command", "bash", map[string]any{"command": "ls -la"}, "ls -la"},
		{"bash missing", "bash", map[string]any{}, ""},
		{"bash_wait handle", "bash_wait", map[string]any{"handle": "bg_1"}, "bg_1"},
		{"bash_output handle", "bash_output", map[string]any{"handle": "bg_2"}, "bg_2"},
		{"bash_kill handle", "bash_kill", map[string]any{"handle": "bg_3"}, "bg_3"},
		{"bash_kill missing", "bash_kill", map[string]any{}, ""},
		{"grep pattern", "grep", map[string]any{"pattern": "foo"}, "foo"},
		{"ripgrep pattern", "ripgrep", map[string]any{"pattern": "bar"}, "bar"},
		{"find pattern", "find", map[string]any{"pattern": "*.go"}, "*.go"},
		{"grounding query", groundingToolName, map[string]any{"query": "who is x"}, "who is x"},
		{"grounding missing", groundingToolName, map[string]any{}, ""},

		// ls keeps its own branch: a present-but-empty path is a path, and only
		// an absent one falls back to ".".
		{"ls path", "ls", map[string]any{"path": "/tmp"}, "/tmp"},
		{"ls missing defaults to dot", "ls", map[string]any{}, "."},
		{"ls empty string is not the default", "ls", map[string]any{"path": ""}, ""},
		{"ls wrong type defaults to dot", "ls", map[string]any{"path": 3}, "."},

		{"unknown tool", "unknown", map[string]any{"x": "y"}, ""},
		{"empty tool", "", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			if got := toolCallSummary(tc.tool, tc.args); got != tc.want {
				t.Errorf("toolCallSummary(%q) = %q, want %q", tc.tool, got, tc.want)
			}
		})
	}
}

// TestTreeCallSummaryRender pins the tree branch, where an empty or absent path
// becomes "." and only a positive depth is appended.
func TestTreeCallSummaryRender(t *testing.T) {
	tests := []struct {
		desc string
		args map[string]any
		want string
	}{
		{"path and depth", map[string]any{"path": "/t", "depth": 3.0}, "/t (depth 3)"},
		{"path only", map[string]any{"path": "/t"}, "/t"},
		{"neither", map[string]any{}, "."},
		{"empty path zero depth", map[string]any{"path": "", "depth": 0.0}, "."},
		{"negative depth is dropped", map[string]any{"depth": -1.0}, "."},
		{"depth wrong type", map[string]any{"path": "/t", "depth": "3"}, "/t"},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			if got := treeCallSummary(tc.args); got != tc.want {
				t.Errorf("treeCallSummary(%v) = %q, want %q", tc.args, got, tc.want)
			}
			if got := toolCallSummary("tree", tc.args); got != tc.want {
				t.Errorf("toolCallSummary(tree) = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAgentCallSummaryRender pins the three-way "type: prompt" join and both
// prompt truncations.
func TestAgentCallSummaryRender(t *testing.T) {
	tests := []struct {
		desc string
		args map[string]any
		want string
	}{
		{"both", map[string]any{"type": "explore", "prompt": "find the thing"}, "explore: find the thing"},
		{"type only", map[string]any{"type": "explore"}, "explore"},
		{"prompt only", map[string]any{"prompt": "just a prompt"}, "just a prompt"},
		{"neither", map[string]any{}, ""},
		{"first line only", map[string]any{"type": "t", "prompt": "first line\nsecond line"}, "t: first line"},
		{"leading newline is not a cut", map[string]any{"type": "t", "prompt": "\nleading newline"}, "t: \nleading newline"},
		{
			"long prompt clipped to 60",
			map[string]any{"type": "t", "prompt": strings.Repeat("p", 100)},
			"t: " + strings.Repeat("p", 57) + "...",
		},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			if got := agentCallSummary(tc.args); got != tc.want {
				t.Errorf("agentCallSummary(%v) = %q, want %q", tc.args, got, tc.want)
			}
			if got := toolCallSummary("agent", tc.args); got != tc.want {
				t.Errorf("toolCallSummary(agent) = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// formatToolResult and its extracted shapes
// ---------------------------------------------------------------------------

// TestFormatToolResultRender pins one case per shape plus the fallback, in the
// order formatToolResult tries them.
func TestFormatToolResultRender(t *testing.T) {
	tests := []struct {
		desc string
		data map[string]any
		want string
	}{
		{"ls entries", map[string]any{"entries": []any{
			map[string]any{"name": "a", "is_dir": true},
			map[string]any{"name": "b"},
			"junk",
		}}, "a/  b"},
		{"ls empty", map[string]any{"entries": []any{}}, ""},
		{
			"ls clipped at 120 bytes",
			map[string]any{"entries": []any{map[string]any{"name": strings.Repeat("n", 200)}}},
			strings.Repeat("n", 117) + "...",
		},
		{"tree counts", map[string]any{"tree": "x", "dirs": 2.0, "files": 3.0}, "2 dirs, 3 files"},
		{"tree missing counts", map[string]any{"tree": "x"}, "0 dirs, 0 files"},
		{"grep matches", map[string]any{
			"matches":       []any{map[string]any{"file": "a.go", "line": 3.0, "content": "hit"}, 1},
			"total_matches": 9.0, "truncated": true,
		}, "a.go:3: hit\n... (9 total matches, truncated)"},
		{"grep matches wins over count", map[string]any{"matches": []any{}, "total_matches": 2.0}, ""},
		{"grep count only", map[string]any{"total_matches": 4.0}, "4 matches"},
		{"find files", map[string]any{
			"files": []any{"a.go", "b.go", 3}, "total_files": 7.0, "truncated": true,
		}, "a.go\nb.go\n... (7 total files, truncated)"},
		{"find count only", map[string]any{"total_files": 12.0}, "12 files"},
		{"read content truncated", map[string]any{
			"content": "hello", "total_lines": 3.0, "truncated": true,
		}, "hello\n... (3 total lines, truncated)"},
		{"read content plain", map[string]any{"content": "hello"}, "hello"},
		{"read count truncated", map[string]any{"total_lines": 8.0, "truncated": true}, "8 lines (truncated)"},
		{"read count plain", map[string]any{"total_lines": 8.0}, "8 lines"},
		{"write bytes", map[string]any{"bytes_written": 42.0, "path": "/p"}, "/p (42 bytes)"},
		{"edit replacements", map[string]any{"replacements": 3.0}, "3 replacements"},
		{"diagnostics", map[string]any{"lsp_diagnostics": "⚠ boom"}, "⚠ boom"},
		{"bash exit zero", map[string]any{"exit_code": 0.0, "stdout": "hi", "stderr": ""}, "hi"},
		{"bash exit nonzero", map[string]any{"exit_code": 1.0, "stdout": "", "stderr": "bad"}, "exit 1: bad"},
		{"bash no output", map[string]any{"exit_code": 0.0}, "(No output)"},
		{"fallback json", map[string]any{"unknown": "value"}, `{"unknown":"value"}`},
		{"fallback empty", map[string]any{}, "{}"},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			if got := formatToolResult(tc.data); got != tc.want {
				t.Errorf("formatToolResult(%v) =\n%q\nwant\n%q", tc.data, got, tc.want)
			}
		})
	}
}

// TestFormatToolResultShapeOrder pins the fall-through decisions that the
// original if-chain encoded by position. Each case carries keys for more than
// one shape, or keys that look like a shape but must not match it.
func TestFormatToolResultShapeOrder(t *testing.T) {
	tests := []struct {
		desc string
		data map[string]any
		want string
	}{
		{
			"handle beats exit_code",
			map[string]any{"handle": "bg_1", "exit_code": -1.0, "running": true, "stdout": "out"},
			formatBashWindow("bg_1", map[string]any{"handle": "bg_1", "exit_code": -1.0, "running": true, "stdout": "out"}),
		},
		{
			"empty handle falls through to exit_code",
			map[string]any{"handle": "", "exit_code": 0.0, "stdout": "hi"},
			"hi",
		},
		{
			"empty diagnostics falls through to the fallback",
			map[string]any{"lsp_diagnostics": ""},
			`{"lsp_diagnostics":""}`,
		},
		{
			"bytes_written without a path falls through",
			map[string]any{"bytes_written": 42.0},
			`{"bytes_written":42}`,
		},
		{
			"bytes_written without a path still reaches replacements",
			map[string]any{"bytes_written": 42.0, "replacements": 2.0},
			"2 replacements",
		},
		{
			"content beats total_lines",
			map[string]any{"content": "body", "total_lines": 9.0},
			"body",
		},
		{
			"files list beats total_files",
			map[string]any{"files": []any{"a"}, "total_files": 9.0},
			"a",
		},
		{
			"long fallback json is clipped",
			map[string]any{"u": strings.Repeat("v", 300)},
			`{"u":"` + strings.Repeat("v", 111) + "...",
		},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			if got := formatToolResult(tc.data); got != tc.want {
				t.Errorf("formatToolResult =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// TestToolResultShapesReject pins that every shape declines input that is not
// its own — the "false" half of each probe, which is what keeps the ordering
// above meaningful.
func TestToolResultShapesReject(t *testing.T) {
	other := map[string]any{"nothing": "here"}
	shapes := map[string]toolResultShape{
		"ls":          lsResultSummary,
		"tree":        treeResultSummary,
		"grep":        grepResultSummary,
		"grepCount":   grepCountSummary,
		"find":        findResultSummary,
		"findCount":   findCountSummary,
		"read":        readResultSummary,
		"readCount":   readCountSummary,
		"write":       writeResultSummary,
		"edit":        editResultSummary,
		"diagnostics": diagnosticsResultSummary,
		"bashWindow":  bashWindowResultSummary,
		"bashExit":    bashExitResultSummary,
	}
	if len(shapes) != len(toolResultShapes) {
		t.Fatalf("test covers %d shapes but formatToolResult tries %d", len(shapes), len(toolResultShapes))
	}
	for name, shape := range shapes {
		t.Run(name, func(t *testing.T) {
			if s, ok := shape(other); ok {
				t.Errorf("%s matched unrelated data, returning %q", name, s)
			}
		})
	}
}

// TestClipToolSummaryRender pins the 120-byte limit and its exact cut point.
func TestClipToolSummaryRender(t *testing.T) {
	tests := []struct {
		desc string
		in   string
		want string
	}{
		{"short", "abc", "abc"},
		{"exactly 120", strings.Repeat("a", 120), strings.Repeat("a", 120)},
		{"121 clips to 117 plus ellipsis", strings.Repeat("a", 121), strings.Repeat("a", 117) + "..."},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			if got := clipToolSummary(tc.in); got != tc.want {
				t.Errorf("clipToolSummary(len %d) = %q, want %q", len(tc.in), got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// blankFast / skipFastCSI
// ---------------------------------------------------------------------------

// TestSkipFastCSIRender pins the exact CSI grammar the fast path accepts and
// every deviation it must hand back to the parser. The -1 cases are what keep
// blankFast agreeing with ansi.Strip on malformed input.
func TestSkipFastCSIRender(t *testing.T) {
	tests := []struct {
		desc string
		line string
		i    int
		want int
	}{
		{"reset", "\x1b[0m", 0, 4},
		{"sgr 256 color", "\x1b[38;5;245m", 0, 11},
		{"no params", "\x1b[m", 0, 3},
		{"trailing content", "\x1b[0mx", 0, 4},
		{"mid-string", "x\x1b[0m", 1, 5},
		{"esc at end", "\x1b", 0, -1},
		{"not a bracket", "\x1bX0", 0, -1},
		{"unterminated params", "\x1b[3", 0, -1},
		{"bracket at end", "\x1b[", 0, -1},
		{"intermediate byte defers", "\x1b[ 0A", 0, -1},
		{"c0 control defers", "\x1b[\x1c", 0, -1},
		{"osc defers", "\x1b]8;;http://x\x07", 0, -1},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			if got := skipFastCSI(tc.line, tc.i); got != tc.want {
				t.Errorf("skipFastCSI(%q, %d) = %d, want %d", tc.line, tc.i, got, tc.want)
			}
		})
	}
}

// TestBlankFastRenderMatchesStrip re-checks the whole blank corpus against the
// reference implementation. FuzzBlankFast in collapse_bench_test.go covers
// arbitrary input; this pins the specific escapes the fast path reasons about.
func TestBlankFastRenderMatchesStrip(t *testing.T) {
	for i, line := range renderGoldenBlankLines() {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			if got, want := blankFast(line), slowBlank(line); got != want {
				t.Errorf("blankFast(%q) = %v, want %v", line, got, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// renderMessages and its extracted blocks
// ---------------------------------------------------------------------------

// renderStripChat renders a chat at the given width and returns the text with
// ANSI removed, which is what the reader actually sees.
func renderStripChat(width int, running bool, msgs ...message) string {
	c := NewChatModel(nil)
	c.Width = width
	c.Messages = append(c.Messages, msgs...)
	text, _ := c.renderMessages(running)
	return ansi.Strip(text)
}

// TestRenderMessagesBlocksRender pins the exact laid-out text each role
// produces, which is where the role switch used to live.
func TestRenderMessagesBlocksRender(t *testing.T) {
	tests := []struct {
		desc    string
		width   int
		running bool
		msgs    []message
		want    string
	}{
		{
			desc: "single user turn has no separator",
			msgs: []message{{role: "user", content: "only"}}, width: 40,
			want: "> only\n",
		},
		{
			desc:  "second user turn opens with the separator",
			width: 24,
			msgs: []message{
				{role: "user", content: "one"},
				{role: "user", content: "two"},
			},
			want: "> one\n" + strings.Repeat("─", 24) + "\n> two\n",
		},
		{
			desc: "user text wraps with a hanging indent", width: 24,
			msgs: []message{{role: "user", content: "alpha beta gamma delta epsilon"}},
			want: "> alpha beta gamma\n   delta epsilon\n",
		},
		{
			desc: "narrow width still wraps at the floor of 20", width: 5,
			msgs: []message{{role: "user", content: "alpha beta gamma delta"}},
			want: "> alpha beta gamma\n   delta\n",
		},
		{
			desc: "assistant reply gets the bullet", width: 40,
			msgs: []message{{role: "assistant", content: "hello"}},
			want: "\n◉ hello\n",
		},
		{
			desc: "empty assistant renders nothing", width: 40,
			msgs: []message{{role: "assistant", content: ""}},
			want: "",
		},
		{
			desc: "empty assistant streams an ellipsis", width: 40, running: true,
			msgs: []message{{role: "assistant", content: ""}},
			want: "\n◉ ...\n",
		},
		{
			desc: "error reply gets the cross and wraps", width: 24,
			msgs: []message{{role: "assistant", content: "alpha beta gamma delta", isError: true}},
			want: "\n✖ alpha beta gamma\n   delta\n",
		},
		{
			desc: "warning reply gets the triangle", width: 40,
			msgs: []message{{role: "assistant", content: "careful", isWarning: true}},
			want: "\n⚠ careful\n",
		},
		{
			desc: "meta reply gets sigma and no bullet", width: 40,
			msgs: []message{{role: "assistant", content: "12 in", isMeta: true}},
			want: "\nΣ 12 in\n",
		},
		{
			desc: "pre-rendered content bypasses markdown", width: 40,
			msgs: []message{{role: "assistant", content: "\x1b[31mred\x1b[0m", preRendered: true}},
			want: "\n◉ red\n",
		},
		{
			desc: "error beats warning beats meta", width: 40,
			msgs: []message{{role: "assistant", content: "x", isError: true, isWarning: true, isMeta: true}},
			want: "\n✖ x\n",
		},
		{
			desc: "warning beats meta", width: 40,
			msgs: []message{{role: "assistant", content: "x", isWarning: true, isMeta: true}},
			want: "\n⚠ x\n",
		},
		{
			desc: "thinking keeps only the last six lines", width: 40,
			msgs: []message{{role: "thinking", content: "a\nb\nc\nd\ne\nf\ng\nh"}},
			want: "\n💭 c\n   d\n   e\n   f\n   g\n   h\n",
		},
		{
			desc: "empty thinking renders nothing", width: 40,
			msgs: []message{{role: "thinking", content: ""}},
			want: "",
		},
		{
			desc: "unknown role renders nothing", width: 40,
			msgs: []message{{role: "wat", content: "ignored"}},
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			if got := renderStripChat(tc.width, tc.running, tc.msgs...); got != tc.want {
				t.Errorf("renderMessages =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// TestRenderMessagesKindsStayAlignedRender guards the invariant the loop exists
// to keep: one blockKind per line of the returned text.
func TestRenderMessagesKindsStayAlignedRender(t *testing.T) {
	c := NewChatModel(nil)
	c.Width = 40
	c.Messages = append(c.Messages, renderGoldenMessageSets()["mixed"]...)
	for _, running := range []bool{false, true} {
		text, kinds := c.renderMessages(running)
		if want := strings.Count(text, "\n") + 1; len(kinds) != want {
			t.Errorf("running=%v: %d kinds for %d lines", running, len(kinds), want)
		}
	}
}

// TestRenderMessagesCacheRender proves the extracted blocks are still routed
// through the render cache: a second render of an unchanged model must reuse
// the cached bytes and produce identical output.
func TestRenderMessagesCacheRender(t *testing.T) {
	c := NewChatModel(nil)
	c.Width = 40
	c.Messages = append(c.Messages, renderGoldenMessageSets()["mixed"]...)

	first, firstKinds := c.renderMessages(false)
	for i := range c.Messages {
		if c.Messages[i].role == "" || c.Messages[i].content == "" {
			continue
		}
		if !c.Messages[i].renderCached {
			t.Errorf("message %d (%s) was not cached", i, c.Messages[i].role)
		}
	}
	second, secondKinds := c.renderMessages(false)
	if first != second {
		t.Errorf("cached render differs:\n%q\nvs\n%q", first, second)
	}
	if len(firstKinds) != len(secondKinds) {
		t.Errorf("cached kinds length %d != %d", len(firstKinds), len(secondKinds))
	}
}

// TestRenderWrapWidthRender pins the floor every wrapped block shares.
func TestRenderWrapWidthRender(t *testing.T) {
	tests := []struct {
		total, reserve, want int
	}{
		{80, 3, 77},
		{23, 3, 20},
		{22, 3, 20},
		{10, 3, 20},
		{0, 3, 20},
		{-5, 3, 20},
		{25, 0, 25},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("total=%d/reserve=%d", tc.total, tc.reserve), func(t *testing.T) {
			if got := renderWrapWidth(tc.total, tc.reserve); got != tc.want {
				t.Errorf("renderWrapWidth(%d, %d) = %d, want %d", tc.total, tc.reserve, got, tc.want)
			}
		})
	}
}

// TestRenderMessageBlockRender exercises the role dispatch directly, including
// the tool role's leading newline, which no other test isolates.
func TestRenderMessageBlockRender(t *testing.T) {
	c := NewChatModel(nil)
	c.Width = 60
	p := paletteOrDark(c.Palette)

	toolMsg := message{role: "tool", tool: "ls", toolIn: ".", content: "a"}
	got := c.renderMessageBlock(&toolMsg, p, "◉ ", "----", false, false)
	want := "\n" + c.ToolDisplay.RenderToolMessage(toolMsg)
	if got != want {
		t.Errorf("tool block = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, "\n") {
		t.Error("tool block must open with a newline")
	}

	unknown := message{role: "nope", content: "x"}
	if got := c.renderMessageBlock(&unknown, p, "◉ ", "----", true, true); got != "" {
		t.Errorf("unknown role = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// Tool cards
// ---------------------------------------------------------------------------

// TestRenderToolCardsRender pins the laid-out text of each card kind. Width 0
// means "unknown", which both argWidth and contentWidth resolve to 80.
func TestRenderToolCardsRender(t *testing.T) {
	tests := []struct {
		desc    string
		display ToolDisplayModel
		msg     message
		want    string
	}{
		{
			desc: "skill card is a single notice line",
			msg:  message{role: "tool", tool: "skill", toolIn: "disk-check", content: "loaded"},
			want: "◉ skill(disk-check) loaded\n",
		},
		{
			desc: "skill card without args or content",
			msg:  message{role: "tool", tool: "skill"},
			want: "◉ skill\n",
		},
		{
			desc: "regular card opens a content gutter",
			msg:  message{role: "tool", tool: "ls", toolIn: ".", content: "a\nb"},
			want: "◉ ls(.)\n  │ a\n  │ b\n",
		},
		{
			desc: "poll tally is appended to the header",
			msg:  message{role: "tool", tool: "bash_wait", toolIn: "bg_1", content: "going", pollCount: 5},
			want: "◉ bash_wait(bg_1) ×5\n  │ going\n",
		},
		{
			desc: "a card polled once carries no tally",
			msg:  message{role: "tool", tool: "bash_wait", toolIn: "bg_1", content: "going", pollCount: 1},
			want: "◉ bash_wait(bg_1)\n  │ going\n",
		},
		{
			desc: "pending card blinks its bullet off",
			msg:  message{role: "tool", tool: "bash", toolIn: "sleep 1"},
			want: "  bash(sleep 1)\n",
		},
		{
			desc:    "pending card blinks its bullet on",
			display: ToolDisplayModel{BlinkOn: true},
			msg:     message{role: "tool", tool: "bash", toolIn: "sleep 1"},
			want:    "◉ bash(sleep 1)\n",
		},
		{
			// The summary collapses newlines to spaces before the first-line
			// cut, so a two-line result folds onto one row rather than losing
			// its tail.
			desc:    "compact card folds the result onto the header",
			display: ToolDisplayModel{CompactTools: true},
			msg:     message{role: "tool", tool: "ls", toolIn: ".", content: "a\nb"},
			want:    "◉ ls(.) ✓ a b\n",
		},
		{
			desc: "agent card without a type omits the bracket",
			msg:  message{role: "tool", tool: "agent"},
			want: "  agent\n",
		},
		{
			desc: "agent card brackets the label and shows the title",
			msg:  message{role: "tool", tool: "agent", agentType: "claude", agentTitle: "look at x", content: "done"},
			want: "◉ agent[claude] look at x\n  │ → done\n",
		},
		{
			desc: "subagent types are mapped and deduped",
			msg:  message{role: "tool", tool: "subagent", agentType: "claude+explore+task+gemini", content: "ok"},
			want: "◉ agent[claude+pi+gemini]\n  │ → ok\n",
		},
		{
			desc: "bundled adapters keep their names, pi subagents collapse",
			msg:  message{role: "tool", tool: "subagent", agentType: "agy+task+copilot+codex", content: "ok"},
			want: "◉ agent[agy+pi+copilot+codex]\n  │ → ok\n",
		},
		{
			desc: "agent result collapses newlines into one gutter line",
			msg:  message{role: "tool", tool: "agent", agentType: "gemini", content: "one\ntwo\n\nthree"},
			want: "◉ agent[gemini]\n  │ → one two three\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			got := ansi.Strip(tc.display.RenderToolMessage(tc.msg))
			if got != tc.want {
				t.Errorf("RenderToolMessage =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// TestRegularToolOutputClipRender pins the line budget and the marker that
// reports what was clipped — the marker must be styled after highlighting, so
// it lands on its own gutter line with no escape debris.
func TestRegularToolOutputClipRender(t *testing.T) {
	lines := make([]string, 0, 30)
	for i := range 30 {
		lines = append(lines, fmt.Sprintf("l%d", i))
	}
	var td ToolDisplayModel
	got := ansi.Strip(td.RenderToolMessage(message{
		role: "tool", tool: "ls", toolIn: ".", content: strings.Join(lines, "\n"),
	}))

	want := "◉ ls(.)\n"
	for i := range maxRegularToolLines {
		want += fmt.Sprintf("  │ l%d\n", i)
	}
	want += "  │ ... (15 more lines)\n"
	if got != want {
		t.Errorf("clipped card =\n%q\nwant\n%q", got, want)
	}

	// Exactly at the budget, nothing is hidden and no marker appears.
	atBudget := ansi.Strip(td.RenderToolMessage(message{
		role: "tool", tool: "ls", toolIn: ".", content: strings.Join(lines[:maxRegularToolLines], "\n"),
	}))
	if strings.Contains(atBudget, "more lines") {
		t.Errorf("card at the budget should carry no marker:\n%q", atBudget)
	}
}

// TestHighlightToolOutputRender pins which tool selects which highlighter. The
// highlighters themselves are covered by highlight_test.go; what matters here
// is that the dispatch survived being lifted out of renderRegularTool.
func TestHighlightToolOutputRender(t *testing.T) {
	dim := lipgloss.NewStyle().Foreground(paletteOrDark(Palette{}).Dim)
	p := paletteOrDark(Palette{})
	lines := []string{"a.go:1: hit", "second"}

	tests := []struct {
		desc string
		msg  message
		want []string
	}{
		{"read with a path", message{tool: "read", toolIn: "/x.go"}, highlightReadOutput(lines, "/x.go", p)},
		{"read without a path falls back", message{tool: "read"}, nil},
		{"bash", message{tool: "bash"}, highlightBashOutput(lines, p)},
		{"grep", message{tool: "grep"}, highlightGrepOutput(lines, p)},
		{"ripgrep is grep", message{tool: "ripgrep"}, highlightGrepOutput(lines, p)},
		{"find", message{tool: "find"}, highlightFindOutput(lines, p)},
		{"anything else is dim", message{tool: "ls"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			got := highlightToolOutput(tc.msg, lines, dim, p)
			want := tc.want
			if want == nil {
				want = []string{dim.Render(lines[0]), dim.Render(lines[1])}
			}
			if len(got) != len(want) {
				t.Fatalf("got %d lines, want %d", len(got), len(want))
			}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("line %d = %q, want %q", i, got[i], want[i])
				}
			}
		})
	}

	// bash_wait and friends take the bash branch via isBashControl.
	for _, name := range []string{"bash_wait", "bash_output", "bash_kill"} {
		if !isBashControl(name) {
			continue
		}
		got := highlightToolOutput(message{tool: name}, lines, dim, p)
		want := highlightBashOutput(lines, p)
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s line %d = %q, want %q", name, i, got[i], want[i])
			}
		}
	}
}

// TestWriteGutterLinesRender pins the gutter prefix every card body shares.
func TestWriteGutterLinesRender(t *testing.T) {
	dim := lipgloss.NewStyle().Foreground(paletteOrDark(Palette{}).Dim)

	var b strings.Builder
	writeGutterLines(&b, dim)
	if b.String() != "" {
		t.Errorf("no lines should write nothing, got %q", b.String())
	}

	b.Reset()
	writeGutterLines(&b, dim, "one", "two")
	if got, want := ansi.Strip(b.String()), "  │ one\n  │ two\n"; got != want {
		t.Errorf("writeGutterLines = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Agent card internals
// ---------------------------------------------------------------------------

// TestRenderableAgentEventsRender pins the filter: structural events never
// reach the window, and whitespace-only message text does not either.
func TestRenderableAgentEventsRender(t *testing.T) {
	in := []agentEv{
		{kind: "spawn", content: "s"},
		{kind: "message_start"},
		{kind: "message_end"},
		{kind: "done"},
		{kind: "text", content: "  \n "},
		{kind: "text_delta", content: ""},
		{kind: "text", content: "real"},
		{kind: "tool_call", content: "read"},
		{kind: "stderr", content: ""},
		{kind: "custom"},
	}
	got := renderableAgentEvents(in)
	want := []string{"text", "tool_call", "custom"}
	if len(got) != len(want) {
		t.Fatalf("kept %d events (%v), want %d", len(got), got, len(want))
	}
	for i := range got {
		if got[i].kind != want[i] {
			t.Errorf("event %d = %q, want %q", i, got[i].kind, want[i])
		}
	}
}

// TestAgentWindowLinesRender pins the line budget and both withheld-output
// notes: whole events dropped reports a count, a single over-long event
// reports that its head was cut.
func TestAgentWindowLinesRender(t *testing.T) {
	st := newAgentEventStyles("", Palette{})

	t.Run("empty stream has no lines and no note", func(t *testing.T) {
		lines, note := agentWindowLines(nil, st, 60)
		if len(lines) != 0 || note != "" {
			t.Errorf("got %d lines, note %q", len(lines), note)
		}
	})

	t.Run("under budget shows everything with no note", func(t *testing.T) {
		evs := []agentEv{{kind: "text", content: "a"}, {kind: "text", content: "b"}}
		lines, note := agentWindowLines(evs, st, 60)
		if len(lines) != 2 || note != "" {
			t.Errorf("got %d lines %v, note %q", len(lines), lines, note)
		}
		if !strings.HasSuffix(ansi.Strip(lines[1]), "b") {
			t.Errorf("newest event must be last, got %q", lines[1])
		}
	})

	t.Run("dropped events report a count", func(t *testing.T) {
		evs := make([]agentEv, 10)
		for i := range evs {
			evs[i] = agentEv{kind: "text", content: fmt.Sprintf("e%d", i)}
		}
		lines, note := agentWindowLines(evs, st, 60)
		if len(lines) != maxAgentOutputLines {
			t.Errorf("got %d lines, want %d", len(lines), maxAgentOutputLines)
		}
		if note != "... 7 earlier events" {
			t.Errorf("note = %q", note)
		}
		if got := ansi.Strip(lines[len(lines)-1]); !strings.HasSuffix(got, "e9") {
			t.Errorf("last line = %q, want the newest event", got)
		}
	})

	t.Run("one over-long event reports a clip", func(t *testing.T) {
		evs := []agentEv{{kind: "text", content: strings.Repeat("word ", 200)}}
		lines, note := agentWindowLines(evs, st, 40)
		if len(lines) != maxAgentOutputLines {
			t.Errorf("got %d lines, want %d", len(lines), maxAgentOutputLines)
		}
		if note != "... earlier output" {
			t.Errorf("note = %q", note)
		}
	})
}

// TestAgentResultSummaryRender pins the 160-byte clip on the result line.
func TestAgentResultSummaryRender(t *testing.T) {
	dim := lipgloss.NewStyle().Foreground(paletteOrDark(Palette{}).Dim)

	got := ansi.Strip(agentResultSummary("short\nresult", dim, 200))
	if want := "  │ → short result\n"; got != want {
		t.Errorf("short summary = %q, want %q", got, want)
	}

	got = ansi.Strip(agentResultSummary(strings.Repeat("x", 300), dim, 400))
	if want := "  │ → " + strings.Repeat("x", 157) + "...\n"; got != want {
		t.Errorf("clipped summary = %q, want %q", got, want)
	}
}

// TestAgentCardHeaderRender pins the header row, including the blinking bullet
// that marks a card whose subagent has not answered yet.
func TestAgentCardHeaderRender(t *testing.T) {
	tests := []struct {
		desc    string
		display ToolDisplayModel
		msg     message
		want    string
	}{
		{"bare", ToolDisplayModel{}, message{}, "  agent\n"},
		{"bare, blink on", ToolDisplayModel{BlinkOn: true}, message{}, "◉ agent\n"},
		{"done cards never blink", ToolDisplayModel{}, message{content: "x"}, "◉ agent\n"},
		{"label only", ToolDisplayModel{}, message{agentType: "task", content: "x"}, "◉ agent[pi]\n"},
		{"title only", ToolDisplayModel{}, message{agentTitle: "hi", content: "x"}, "◉ agent hi\n"},
		{"both", ToolDisplayModel{}, message{agentType: "gemini", agentTitle: "hi", content: "x"}, "◉ agent[gemini] hi\n"},
		{"agy keeps its name", ToolDisplayModel{}, message{agentType: "agy", content: "x"}, "◉ agent[agy]\n"},
		{"copilot keeps its name", ToolDisplayModel{}, message{agentType: "copilot", content: "x"}, "◉ agent[copilot]\n"},
		{"codex keeps its name", ToolDisplayModel{}, message{agentType: "codex", content: "x"}, "◉ agent[codex]\n"},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			got := ansi.Strip(tc.display.agentCardHeader(tc.msg, paletteOrDark(tc.display.Palette)))
			if got != tc.want {
				t.Errorf("agentCardHeader = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAgentTitleFitPinsTruncation pins the rune-wise title clip: the cut is
// marked with a single ellipsis rune and the result is never wider than the
// budget.
func TestAgentTitleFitPinsTruncation(t *testing.T) {
	title := strings.Repeat("a", 100)
	got := agentTitleFit(title, 60)
	if n := len([]rune(got)); n != 60 {
		t.Errorf("got %d runes, want 60", n)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix, got %q", got)
	}

	// A title that fits is returned whole.
	if got := agentTitleFit("short", 60); got != "short" {
		t.Errorf("got %q, want 'short'", got)
	}
}

// TestAgentCardHeaderWideTruncatesLess pins that a wider terminal shows a
// longer agent title: the render budget grows with the width, so the same
// stored title is clipped harder on a narrow card than a wide one.
func TestAgentCardHeaderWideTruncatesLess(t *testing.T) {
	title := strings.Repeat("x", 300) // beyond even the wide budget
	msg := message{agentType: "claude", agentTitle: title, content: "done"}

	narrow := ansi.Strip((&ToolDisplayModel{Width: 80}).agentCardHeader(msg, paletteOrDark(Palette{})))
	wide := ansi.Strip((&ToolDisplayModel{Width: 200}).agentCardHeader(msg, paletteOrDark(Palette{})))

	if len([]rune(narrow)) >= len([]rune(wide)) {
		t.Errorf("expected the wider terminal to render a longer header; narrow=%d wide=%d",
			len([]rune(narrow)), len([]rune(wide)))
	}
	if !strings.Contains(narrow, "…") || !strings.Contains(wide, "…") {
		t.Errorf("a title beyond both budgets must be clipped on both, got narrow=%q wide=%q", narrow, wide)
	}
}

// ---------------------------------------------------------------------------
// Status bar fields
// ---------------------------------------------------------------------------

// TestStatusFieldsRender pins each field of the bar in isolation: the exact
// text, and whether the field appears at all. A field that returns nil is one
// the joined bar must not open a separator for.
func TestStatusFieldsRender(t *testing.T) {
	p := paletteOrDark(Palette{})
	dim := lipgloss.NewStyle().Foreground(p.Dim)

	t.Run("queued", func(t *testing.T) {
		if got := statusQueuedField(StatusRenderInput{}, p); got != nil {
			t.Errorf("no pending prompts should render nothing, got %v", got)
		}
		if got := statusQueuedField(StatusRenderInput{Pending: -1}, p); got != nil {
			t.Errorf("negative pending should render nothing, got %v", got)
		}
		got := statusQueuedField(StatusRenderInput{Pending: 2}, p)
		if len(got) != 1 || ansi.Strip(got[0]) != "queued: 2" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("context", func(t *testing.T) {
		tests := []struct {
			desc string
			in   StatusRenderInput
			want string
		}{
			{"no tracker, no messages", StatusRenderInput{}, "ctx: 0"},
			{
				"no tracker, big conversation",
				StatusRenderInput{Messages: []message{{role: "user", content: strings.Repeat("word ", 3000)}}},
				"", // asserted by prefix below
			},
			{
				"tracker with a limit draws the bar",
				StatusRenderInput{TokenTracker: flexTokenTracker{limit: 1000, percentUsed: 42}},
				"████░░░░░░ 42%",
			},
			{
				"tracker without a limit falls back to the estimate",
				StatusRenderInput{TokenTracker: flexTokenTracker{}},
				"ctx: 0",
			},
		}
		for _, tc := range tests {
			t.Run(tc.desc, func(t *testing.T) {
				got := ansi.Strip(statusContextField(tc.in, dim, p))
				if tc.want == "" {
					if !strings.HasPrefix(got, "ctx: ") || !strings.HasSuffix(got, "k") {
						t.Errorf("got %q, want a k-suffixed estimate", got)
					}
					return
				}
				if got != tc.want {
					t.Errorf("got %q, want %q", got, tc.want)
				}
			})
		}
	})

	t.Run("token", func(t *testing.T) {
		tests := []struct {
			desc string
			tt   TokenTracker
			want string
		}{
			{"nil tracker", nil, ""},
			{"no limit and nothing used", flexTokenTracker{}, ""},
			{"no limit but used", flexTokenTracker{totalUsed: 4321}, "tkn: 4.3k"},
			{"under 80%", flexTokenTracker{limit: 1000, totalUsed: 100, percentUsed: 10}, "tkn: 100/1.0k"},
			{"at 80%", flexTokenTracker{limit: 1000, totalUsed: 800, percentUsed: 80}, "tkn: 800/1.0k"},
			{"over 100%", flexTokenTracker{limit: 1000, totalUsed: 1500, percentUsed: 150}, "tkn: 1.5k/1.0k"},
		}
		for _, tc := range tests {
			t.Run(tc.desc, func(t *testing.T) {
				got := statusTokenField(StatusRenderInput{TokenTracker: tc.tt}, dim, p)
				if tc.want == "" {
					if got != nil {
						t.Errorf("want no field, got %q", got)
					}
					return
				}
				if len(got) != 1 || ansi.Strip(got[0]) != tc.want {
					t.Errorf("got %q, want %q", got, tc.want)
				}
			})
		}
	})

	t.Run("location", func(t *testing.T) {
		tests := []struct {
			desc, folder, host, want string
		}{
			{"neither", "", "", ""},
			{"both", "pi-go", "mac", "pi-go | mac"},
			{"folder only", "pi-go", "", "pi-go"},
			{"host only", "", "mac", "mac"},
		}
		for _, tc := range tests {
			t.Run(tc.desc, func(t *testing.T) {
				got := statusLocationField(StatusRenderInput{FolderName: tc.folder, HostName: tc.host}, dim, p)
				if tc.want == "" {
					if got != nil {
						t.Errorf("want no field, got %q", got)
					}
					return
				}
				if len(got) != 1 || ansi.Strip(got[0]) != tc.want {
					t.Errorf("got %q, want %q", got, tc.want)
				}
			})
		}
	})

	t.Run("tools", func(t *testing.T) {
		now := time.Now()
		if got := (&StatusModel{}).statusToolField(p); got != nil {
			t.Errorf("idle bar should render no tool field, got %q", got)
		}
		// One entry in the map is not "parallel"; only ActiveTool speaks then.
		one := &StatusModel{ActiveTools: map[string]time.Time{"a": now}}
		if got := one.statusToolField(p); got != nil {
			t.Errorf("single map entry should render nothing, got %q", got)
		}
		many := &StatusModel{ActiveTools: map[string]time.Time{"b": now, "a": now}}
		got := many.statusToolField(p)
		if len(got) != 1 || ansi.Strip(got[0]) != "tools[2]: a, b" {
			t.Errorf("got %q, want sorted names", got)
		}
		single := &StatusModel{ActiveTool: "bash", ToolStart: now}
		got = single.statusToolField(p)
		if len(got) != 1 || !strings.HasPrefix(ansi.Strip(got[0]), "tool: bash (") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("run cycle", func(t *testing.T) {
		if got := statusRunCycleField(StatusRenderInput{}, p); got != nil {
			t.Errorf("want no field, got %q", got)
		}
		got := statusRunCycleField(StatusRenderInput{RunCycle: &runCycleInfo{Cycle: 2, MaxRetries: 5}}, p)
		if len(got) != 1 || ansi.Strip(got[0]) != "cycle 2/5" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("loading", func(t *testing.T) {
		got := ansi.Strip(statusLoadingField(map[string]bool{"mcp": true, "lsp": false, "skills": true}, dim, p))
		if want := "load: lsp... mcp ✓ skills ✓"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
		if got := ansi.Strip(statusLoadingField(map[string]bool{}, dim, p)); got != "load: " {
			t.Errorf("empty loading = %q", got)
		}
	})
}

// TestStatusBracketFieldRender pins the precedence in the bracketed slot: a
// flash borrows it from the mode, plan outranks the spinner, and the spinner
// only appears while running with no tool of its own to name.
func TestStatusBracketFieldRender(t *testing.T) {
	p := paletteOrDark(Palette{})

	tests := []struct {
		desc  string
		model StatusModel
		in    StatusRenderInput
		want  string
	}{
		{"default is chat", StatusModel{}, StatusRenderInput{}, " [" + paddedStatusMode("chat") + "]"},
		{"explicit mode", StatusModel{}, StatusRenderInput{Mode: "plan"}, " [" + paddedStatusMode("plan") + "]"},
		{"flash wins over plan", StatusModel{}, StatusRenderInput{Mode: "plan", Flash: "Copied!"}, " [" + paddedStatusMode("Copied!") + "]"},
		{"plan wins over the spinner", StatusModel{}, StatusRenderInput{Mode: "plan", Running: true}, " [" + paddedStatusMode("plan") + "]"},
		{"running with an active tool keeps the mode", StatusModel{ActiveTool: "bash"}, StatusRenderInput{Running: true}, " [" + paddedStatusMode("chat") + "]"},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			if got := ansi.Strip(tc.model.statusBracketField(tc.in, p)); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}

	// Running with no active tool shows the spinner verb, whose text rotates
	// with the clock — assert its shape, not its wording.
	running := (&StatusModel{}).statusBracketField(StatusRenderInput{Running: true}, p)
	got := ansi.Strip(running)
	if !strings.HasPrefix(got, " [") || !strings.HasSuffix(got, "]") {
		t.Errorf("spinner field = %q, want a bracketed verb", got)
	}
}

// TestStatusRenderCompositionRender pins the assembled bar: which fields appear
// together, and that loading short-circuits everything after it.
func TestStatusRenderCompositionRender(t *testing.T) {
	s := &StatusModel{Width: 200}

	full := ansi.Strip(s.Render(StatusRenderInput{
		Pending:      2,
		TokenTracker: flexTokenTracker{limit: 1000, totalUsed: 100, percentUsed: 10},
		FolderName:   "pi-go",
		HostName:     "mac",
		RunCycle:     &runCycleInfo{Cycle: 1, MaxRetries: 3},
	}))
	for _, want := range []string{"queued: 2", "█", "tkn: 100/1.0k", "pi-go | mac", "cycle 1/3"} {
		if !strings.Contains(full, want) {
			t.Errorf("bar %q is missing %q", full, want)
		}
	}
	if n := strings.Count(full, "│"); n != 5 {
		t.Errorf("bar %q has %d separators, want 5 between 6 fields", full, n)
	}

	loading := ansi.Strip(s.Render(StatusRenderInput{
		Pending:      2,
		LoadingItems: map[string]bool{"mcp": true},
		FolderName:   "pi-go",
	}))
	if !strings.Contains(loading, "load: mcp ✓") {
		t.Errorf("loading bar = %q", loading)
	}
	for _, unwanted := range []string{"queued", "pi-go", "ctx:"} {
		if strings.Contains(loading, unwanted) {
			t.Errorf("loading bar %q must not carry %q", loading, unwanted)
		}
	}

	// A bare bar is the bracket plus the context estimate, and nothing else.
	bare := ansi.Strip(s.Render(StatusRenderInput{}))
	if n := strings.Count(bare, "│"); n != 1 {
		t.Errorf("bare bar %q has %d separators, want 1", bare, n)
	}
}
