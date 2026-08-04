package tui

import (
	"strings"
	"testing"
)

// flexTokenTracker is a configurable TokenTracker for render tests.
type flexTokenTracker struct {
	limit, remaining, totalUsed     int64
	percentUsed, ctxPercentUsed     float64
	lastPromptTokens, ctxWindowSize int64
	lastCachedTokens, cachedToday   int64
	cacheHitRate                    float64
	bodyTokens, cachePrefixTokens   int64
}

func (m flexTokenTracker) Limit() int64                { return m.limit }
func (m flexTokenTracker) Remaining() int64            { return m.remaining }
func (m flexTokenTracker) PercentUsed() float64        { return m.percentUsed }
func (m flexTokenTracker) TotalUsed() int64            { return m.totalUsed }
func (m flexTokenTracker) LastPromptTokens() int64     { return m.lastPromptTokens }
func (m flexTokenTracker) ContextWindowSize() int64    { return m.ctxWindowSize }
func (m flexTokenTracker) ContextPercentUsed() float64 { return m.ctxPercentUsed }
func (m flexTokenTracker) LastCachedTokens() int64     { return m.lastCachedTokens }
func (m flexTokenTracker) CachedTokensToday() int64    { return m.cachedToday }
func (m flexTokenTracker) CacheHitRateToday() float64  { return m.cacheHitRate }
func (m flexTokenTracker) BodyTokens() int64           { return m.bodyTokens }
func (m flexTokenTracker) CachePrefixTokens() int64    { return m.cachePrefixTokens }

func TestRenderSidebar_Variations(t *testing.T) {
	base := SidebarRenderInput{
		Width: 30, Height: 40,
		Mode: "chat", ProviderName: "ollama", ModelName: "qwen3",
		GitBranch: "main", DiffAdded: 12, DiffRemoved: 3,
		AppVersion: "1.2.3", HostName: "host", FolderName: "pi-go",
	}

	cases := []struct {
		name string
		in   SidebarRenderInput
	}{
		{"minimal", base},
		{"with-eyes", func() SidebarRenderInput { c := base; c.Eyes = "^_^"; return c }()},
		{"with-mascot", func() SidebarRenderInput { c := base; c.Mascot = "(o_o)\n/|\\\n/ \\"; return c }()},
		{"known-context-window", func() SidebarRenderInput {
			c := base
			c.TokenTracker = flexTokenTracker{ctxWindowSize: 32000, lastPromptTokens: 8000, ctxPercentUsed: 25}
			return c
		}()},
		{"unknown-window-with-tokens", func() SidebarRenderInput {
			c := base
			c.TokenTracker = flexTokenTracker{lastPromptTokens: 1500}
			return c
		}()},
		{"estimate-from-messages-small", func() SidebarRenderInput {
			c := base
			c.Messages = []message{{role: "user", content: "hi"}}
			return c
		}()},
		{"estimate-from-messages-large", func() SidebarRenderInput {
			c := base
			c.Messages = []message{{role: "user", content: strings.Repeat("x", 8000), tool: "bash", toolIn: "ls"}}
			return c
		}()},
		{"running-with-active-tool", func() SidebarRenderInput {
			c := base
			c.Running = true
			c.ActiveTool = "bash"
			c.StatusLine = "thinking"
			c.MatrixLines = "010\n101"
			return c
		}()},
		{"loading-items", func() SidebarRenderInput {
			c := base
			c.LoadingItems = map[string]bool{"lsp": true, "mcp": false, "memory": true}
			return c
		}()},
		{"run-checklist", func() SidebarRenderInput {
			c := base
			c.RunPhase = "build"
			c.RunSpec = "feature-x"
			c.RunCycle = 1
			c.RunMaxCycle = 3
			c.RunChecklist = []ChecklistStep{{Title: "compile", Done: true}, {Title: "test", Done: false}}
			return c
		}()},
		{"otel-enabled", func() SidebarRenderInput {
			c := base
			c.OTELEnabled = true
			return c
		}()},
		{"tiny-width", func() SidebarRenderInput { c := base; c.Width = 5; return c }()},
		{"token-limit-tracker", func() SidebarRenderInput {
			c := base
			c.TokenTracker = flexTokenTracker{limit: 100000, remaining: 40000, percentUsed: 60, totalUsed: 60000}
			return c
		}()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := RenderSidebar(tc.in)
			if out == "" {
				t.Fatal("RenderSidebar returned empty output")
			}
		})
	}
}

func TestSidebarFolderName(t *testing.T) {
	if got := sidebarFolderName(""); got == "" {
		// empty workdir falls back to something non-panicking; just ensure no crash.
		_ = got
	}
	if got := sidebarFolderName("/a/b/pi-go"); got != "pi-go" {
		t.Fatalf("sidebarFolderName = %q, want pi-go", got)
	}
}

func TestAgentStatusPriority_Ordering(t *testing.T) {
	running := agentStatusPriority("running")
	done := agentStatusPriority("done")
	unknown := agentStatusPriority("something-else")
	if running >= done {
		t.Errorf("running (%d) should sort before done (%d)", running, done)
	}
	if unknown < 0 {
		t.Errorf("unknown priority should be non-negative, got %d", unknown)
	}
}

// --- tool_display rendering ------------------------------------------------

func TestRenderToolMessage_Variants(t *testing.T) {
	for _, compact := range []bool{false, true} {
		td := &ToolDisplayModel{Width: 80, CompactTools: compact}
		msgs := []message{
			{role: "tool", tool: "bash", toolIn: "ls -la", content: `{"stdout":"file.go\n"}`},
			{role: "tool", tool: "read", toolIn: "main.go", content: `{"content":"package main"}`},
			{role: "tool", tool: "agent", agentType: "claude", agentTitle: "do work", agentEvents: []agentEv{
				{kind: "text", content: "working"},
				{kind: "tool_call", content: "bash"},
			}},
			{role: "tool", tool: "grep", toolIn: "func", content: `{"matches":["a.go:1"]}`},
			{role: "tool", tool: "write", toolIn: "out.txt", content: `{"ok":true}`},
		}
		for _, m := range msgs {
			if out := td.RenderToolMessage(m); out == "" {
				t.Errorf("compact=%v: RenderToolMessage(%s) returned empty", compact, m.tool)
			}
		}
	}
}

func TestToolCallSummary_Variants(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
	}{
		{"bash", map[string]any{"command": "ls -la"}},
		{"read", map[string]any{"file_path": "/a/b.go"}},
		{"write", map[string]any{"file_path": "/a/b.go", "content": "data"}},
		{"grep", map[string]any{"pattern": "func", "path": "."}},
		{"agent", map[string]any{"type": "claude", "prompt": "do it"}},
		{"unknown", map[string]any{"foo": "bar"}},
		{"empty", map[string]any{}},
	}
	for _, tc := range cases {
		// Should never panic and should return a non-nil string for known tools.
		_ = toolCallSummary(tc.name, tc.args)
	}
}

func TestToolResultSummary_Variants(t *testing.T) {
	inputs := []string{
		`{"stdout":"hello\nworld"}`,
		`{"content":"package main\nfunc main(){}"}`,
		`{"error":"boom"}`,
		`{"matches":["a.go:1","b.go:2"]}`,
		`not json at all`,
		``,
		`{}`,
	}
	for _, in := range inputs {
		_ = toolResultSummary(in)
	}
}

func TestCollapseAndSoftWrap(t *testing.T) {
	if got := collapseToSingleLine("a\nb\n  c  "); strings.Contains(got, "\n") {
		t.Errorf("collapseToSingleLine left a newline: %q", got)
	}
	lines := softWrap(strings.Repeat("word ", 40), 20)
	if len(lines) < 2 {
		t.Errorf("expected softWrap to split long text, got %d lines", len(lines))
	}
}
