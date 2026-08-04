package tui

import (
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/subagent"
)

// plain strips styling so assertions can look at the text alone.
func plain(lines []string) string {
	return ansi.Strip(strings.Join(lines, "\n"))
}

func TestTruncateLabel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		maxW  int
		want  string
	}{
		{"fits untouched", "main", 10, "main"},
		{"exact fit untouched", "main", 4, "main"},
		{"truncated with ellipsis", "feature/very-long-branch", 10, "feature/v…"},
		// Each CJK glyph is 2 cells, so only two fit alongside the ellipsis.
		{"wide runes are measured by width", "日本語のブランチ", 6, "日本…"},
		{"zero width yields ellipsis only", "abc", 1, "…"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := truncateLabel(tt.input, tt.maxW)
			if got != tt.want {
				t.Errorf("truncateLabel(%q, %d) = %q, want %q", tt.input, tt.maxW, got, tt.want)
			}
			if w := runewidth.StringWidth(got); w > tt.maxW {
				t.Errorf("truncateLabel(%q, %d) = %q with width %d, exceeds max", tt.input, tt.maxW, got, w)
			}
		})
	}
}

func TestTruncateLabelNeverSplitsRunes(t *testing.T) {
	t.Parallel()
	// A multi-byte name truncated mid-rune would produce invalid UTF-8 and
	// corrupt the frame.
	for w := 1; w <= 12; w++ {
		got := truncateLabel("日本語のブランチ名", w)
		if !utf8.ValidString(got) {
			t.Fatalf("width %d produced invalid UTF-8: %q", w, got)
		}
	}
}

func TestEstimateContextTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		msgs []message
		want string
	}{
		{"no messages", nil, "  ~0 tokens"},
		{
			"small message uses plain count",
			[]message{{content: strings.Repeat("a", 40)}},
			"  ~10 tokens",
		},
		{
			"large message switches to k",
			[]message{{content: strings.Repeat("a", 8000)}},
			"  ~2.0k tokens",
		},
		{
			"tool fields are counted too",
			[]message{{content: "ab", tool: "cd", toolIn: "ef"}},
			"  ~1 tokens",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := estimateContextTokens(tt.msgs); got != tt.want {
				t.Errorf("estimateContextTokens() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSidebarMoodLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      SidebarRenderInput
		wantLen int
		want    string
	}{
		{"neither set is hidden", SidebarRenderInput{}, 0, ""},
		{"eyes shown", SidebarRenderInput{Eyes: "^_^"}, 3, "^_^"},
		{"mascot shown", SidebarRenderInput{Mascot: "(o_o)"}, 3, "(o_o)"},
		{"mascot wins over eyes", SidebarRenderInput{Mascot: "(o_o)", Eyes: "^_^"}, 3, "(o_o)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := sidebarMoodLines(tt.in)
			if len(got) != tt.wantLen {
				t.Fatalf("sidebarMoodLines() returned %d lines, want %d", len(got), tt.wantLen)
			}
			if tt.want != "" && !strings.Contains(plain(got), tt.want) {
				t.Errorf("expected %q in %q", tt.want, plain(got))
			}
		})
	}
}

func TestSidebarHiddenSections(t *testing.T) {
	t.Parallel()
	empty := SidebarRenderInput{}

	tests := []struct {
		name string
		got  []string
	}{
		{"otel off", sidebarOTELLines(empty)},
		{"no artifacts", sidebarArtifactLines(empty, 27)},
		{"no git branch", sidebarGitLines(empty, 27)},
		{"no orchestrator", sidebarAgentLines(empty, 27)},
		{"no skills", sidebarSkillLines(empty)},
		{"no memory status", sidebarMemoryLines(empty)},
		{"no mcp tools", sidebarMCPLines(empty, 27)},
		{"no loading items", sidebarLoadingLines(empty)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if len(tt.got) != 0 {
				t.Errorf("expected a hidden section, got %d lines: %q", len(tt.got), plain(tt.got))
			}
		})
	}
}

func TestSidebarGitLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		in        SidebarRenderInput
		wantParts []string
		absent    string
	}{
		{
			name:      "branch without diff counts",
			in:        SidebarRenderInput{GitBranch: "main"},
			wantParts: []string{"Git", "⎇ main"},
			absent:    "+",
		},
		{
			name:      "branch with diff counts",
			in:        SidebarRenderInput{GitBranch: "main", DiffAdded: 12, DiffRemoved: 3},
			wantParts: []string{"Git", "+12", "-3", "⎇ main"},
		},
		{
			name:      "added only still shows both counts",
			in:        SidebarRenderInput{GitBranch: "dev", DiffAdded: 5},
			wantParts: []string{"+5", "-0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := plain(sidebarGitLines(tt.in, 27))
			for _, want := range tt.wantParts {
				if !strings.Contains(out, want) {
					t.Errorf("expected %q in:\n%s", want, out)
				}
			}
			if tt.absent != "" && strings.Contains(out, tt.absent) {
				t.Errorf("did not expect %q in:\n%s", tt.absent, out)
			}
		})
	}
}

func TestSidebarModeLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   SidebarRenderInput
		want string
	}{
		{"empty mode defaults to chat", SidebarRenderInput{}, "[chat]"},
		{"plan mode", SidebarRenderInput{Mode: "plan"}, "[plan]"},
		{"custom mode", SidebarRenderInput{Mode: "yolo"}, "[yolo]"},
		{"running shows thinking", SidebarRenderInput{Running: true}, "thinking..."},
		{"running with tool shows tool", SidebarRenderInput{Running: true, ActiveTool: "grep"}, "⚡ grep"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := plain(sidebarModeLines(tt.in, 27))
			if !strings.Contains(out, tt.want) {
				t.Errorf("expected %q in:\n%s", tt.want, out)
			}
		})
	}
}

func TestSidebarModeLinesRunChecklist(t *testing.T) {
	t.Parallel()
	in := SidebarRenderInput{
		RunPhase:    "implement",
		RunSpec:     "my-spec",
		RunCycle:    2,
		RunMaxCycle: 5,
		RunChecklist: []ChecklistStep{
			{Title: "write tests", Done: true},
			{Title: "make them pass", Done: false},
		},
	}

	out := plain(sidebarModeLines(in, 27))
	for _, want := range []string{"Run: my-spec", "cycle 2/5 ∙ implement", "[x] write tests", "[ ] make them pass"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "[chat]") {
		t.Error("run checklist should replace the plain mode indicator")
	}
}

func TestSidebarAgentLines(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	agents := []subagent.AgentStatus{
		{AgentID: "c", Type: "coder", Status: "done", StartedAt: base.Add(2 * time.Minute)},
		{AgentID: "a", Type: "coder", Status: "running", StartedAt: base},
		{AgentID: "b", Type: "tester", Status: "failed", StartedAt: base.Add(time.Minute)},
	}

	sortAgentsForDisplay(agents)
	if agents[0].Status != "running" {
		t.Errorf("running agents must sort first, got %q", agents[0].Status)
	}

	names := agentDisplayNames(agents, 27)
	if len(names) != len(agents) {
		t.Fatalf("agentDisplayNames returned %d names for %d agents", len(names), len(agents))
	}
	// Two agents share the "coder" type, so both get a numeric suffix.
	var coders int
	for _, n := range names {
		if strings.HasPrefix(n, "coder-") {
			coders++
		}
	}
	if coders != 2 {
		t.Errorf("expected both coder agents to be suffixed, got names %v", names)
	}
	// The lone tester keeps its bare type name.
	if !hasExact(names, "tester") {
		t.Errorf("expected an unsuffixed 'tester', got %v", names)
	}
}

func TestAgentDisplayNamesFallsBackToAgent(t *testing.T) {
	t.Parallel()
	agents := []subagent.AgentStatus{{AgentID: "x", Type: "", Status: "running"}}
	if got := agentDisplayNames(agents, 27); got[0] != "agent" {
		t.Errorf("expected untyped agent to be named %q, got %q", "agent", got[0])
	}
}

func TestAgentRowIcons(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status string
		want   string
	}{
		{"running", "⚡"},
		{"done", "✓"},
		{"failed", "✗"},
		{"killed", "⊘"},
		{"queued", "∙"},
		{"", "∙"},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			t.Parallel()
			got := ansi.Strip(agentRow(tt.status, "worker"))
			if !strings.Contains(got, tt.want) {
				t.Errorf("agentRow(%q) = %q, want icon %q", tt.status, got, tt.want)
			}
			if !strings.Contains(got, "worker") {
				t.Errorf("agentRow(%q) dropped the name: %q", tt.status, got)
			}
		})
	}
}

func TestAgentStatusPriorityOrdering(t *testing.T) {
	t.Parallel()
	// running < done < failed < killed < unknown
	order := []string{"running", "done", "failed", "killed", "mystery"}
	for i := 1; i < len(order); i++ {
		prev, cur := agentStatusPriority(order[i-1]), agentStatusPriority(order[i])
		if prev >= cur {
			t.Errorf("priority(%q)=%d should sort before priority(%q)=%d",
				order[i-1], prev, order[i], cur)
		}
	}
	if agentStatusPriority("completed") != agentStatusPriority("done") {
		t.Error("'completed' and 'done' should share a priority")
	}
}

func TestSidebarMCPLinesCountsPerServer(t *testing.T) {
	t.Parallel()
	in := SidebarRenderInput{MCPTools: []extension.MCPToolEntry{
		{Server: "alpha", Tool: "one"},
		{Server: "alpha", Tool: "two"},
		{Server: "beta", Tool: "three"},
	}}
	out := plain(sidebarMCPLines(in, 27))

	if !strings.Contains(out, "MCP Tools [3]") {
		t.Errorf("expected a total of 3 tools, got:\n%s", out)
	}
	if !strings.Contains(out, "alpha [2]") {
		t.Errorf("expected alpha to show 2 tools, got:\n%s", out)
	}
	if !strings.Contains(out, "beta [1]") {
		t.Errorf("expected beta to show 1 tool, got:\n%s", out)
	}
}

func TestSidebarLoadingLines(t *testing.T) {
	t.Parallel()
	in := SidebarRenderInput{LoadingItems: map[string]bool{"mcp": true, "skills": false}}
	out := plain(sidebarLoadingLines(in))

	if !strings.Contains(out, "✓ mcp") {
		t.Errorf("expected a tick for the loaded item, got:\n%s", out)
	}
	if !strings.Contains(out, "◌ skills...") {
		t.Errorf("expected a spinner for the pending item, got:\n%s", out)
	}
}

func TestSidebarLoadingLinesEmptyMapStillShowsHeading(t *testing.T) {
	t.Parallel()
	// A non-nil but empty map means "loading started, nothing reported yet",
	// which is different from nil (hidden).
	got := sidebarLoadingLines(SidebarRenderInput{LoadingItems: map[string]bool{}})
	if len(got) == 0 {
		t.Fatal("an empty (non-nil) loading map should still render the heading")
	}
	if !strings.Contains(plain(got), "Loading") {
		t.Errorf("expected the Loading heading, got %q", plain(got))
	}
}

// hasExact reports whether names contains an exact match for want.
func hasExact(names []string, want string) bool {
	return slices.Contains(names, want)
}
