package tui

import (
	"os"
	"path/filepath"
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

// testSidebarStyles returns the dark sidebar styles for tests that call the
// section renderers directly.
func testSidebarStyles() sidebarStyles {
	return newSidebarStyles(darkPalette)
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
			got := sidebarMoodLines(tt.in, testSidebarStyles())
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
		{"no artifacts", sidebarArtifactLines(empty, 27, testSidebarStyles())},
		{"no git branch", sidebarGitLines(empty, 27, testSidebarStyles())},
		{"no orchestrator", sidebarAgentLines(empty, 27, testSidebarStyles())},
		{"no skills", sidebarSkillLines(empty, testSidebarStyles())},
		{"no memory status", sidebarMemoryLines(empty, testSidebarStyles())},
		{"no mcp tools", sidebarMCPLines(empty, 27, testSidebarStyles())},
		{"no loading items", sidebarLoadingLines(empty, testSidebarStyles())},
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
			out := plain(sidebarGitLines(tt.in, 27, testSidebarStyles()))
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
			out := plain(sidebarModeLines(tt.in, 27, testSidebarStyles()))
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

	out := plain(sidebarModeLines(in, 27, testSidebarStyles()))
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
			got := ansi.Strip(agentRow(tt.status, "worker", testSidebarStyles()))
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
	out := plain(sidebarMCPLines(in, 27, testSidebarStyles()))

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
	out := plain(sidebarLoadingLines(in, testSidebarStyles()))

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
	got := sidebarLoadingLines(SidebarRenderInput{LoadingItems: map[string]bool{}}, testSidebarStyles())
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

func TestDetectPlanPhases(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"rough-idea.md", "requirements.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "research"), 0o755); err != nil {
		t.Fatalf("creating research dir: %v", err)
	}
	// A research phase is done when it has produced something; the plan flow
	// creates the directory itself, up front.
	if err := os.WriteFile(filepath.Join(dir, "research", "angle.md"), []byte("findings"), 0o644); err != nil {
		t.Fatalf("writing research file: %v", err)
	}

	phases := detectPlanPhases(dir)
	if len(phases) != 7 {
		t.Fatalf("expected 7 phases, got %d", len(phases))
	}
	wantNames := []string{"Idea", "Requirements", "Research", "Design", "Outline", "Plan", "Prompt"}
	for i, want := range wantNames {
		if phases[i].Name != want {
			t.Errorf("phase %d name = %q, want %q", i, phases[i].Name, want)
		}
	}
	wantDone := []bool{true, true, true, false, false, false, false}
	for i, want := range wantDone {
		if phases[i].Done != want {
			t.Errorf("phase %q Done = %v, want %v", phases[i].Name, phases[i].Done, want)
		}
	}
}

func TestDetectPlanPhases_AllDone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"rough-idea.md", "requirements.md", "design.md", "outline.md", "plan.md", "PROMPT.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "research"), 0o755); err != nil {
		t.Fatalf("creating research dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "research", "angle.md"), []byte("findings"), 0o644); err != nil {
		t.Fatalf("writing research file: %v", err)
	}

	phases := detectPlanPhases(dir)
	if len(phases) != 7 {
		t.Fatalf("expected 7 phases, got %d", len(phases))
	}
	for _, p := range phases {
		if !p.Done {
			t.Errorf("phase %q should be Done, got Done=%v", p.Name, p.Done)
		}
	}
}

// The skeleton /plan writes up front must not tick a single checkbox beyond
// Idea: requirements.md holds only its headings and research/ is empty, so
// marking them done claimed work that had not happened yet.
func TestDetectPlanPhases_SkeletonIsNotProgress(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Exactly what createSpecSkeleton produces.
	if err := os.WriteFile(filepath.Join(dir, "rough-idea.md"),
		[]byte("# Rough Idea\n\nmake the thing\n"), 0o644); err != nil {
		t.Fatalf("writing rough-idea.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "requirements.md"),
		[]byte("# Requirements\n\n## Questions & Answers\n\n"), 0o644); err != nil {
		t.Fatalf("writing requirements.md: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "research"), 0o755); err != nil {
		t.Fatalf("creating research dir: %v", err)
	}

	phases := detectPlanPhases(dir)
	done := map[string]bool{}
	for _, p := range phases {
		done[p.Name] = p.Done
	}

	if !done["Idea"] {
		t.Error("Idea should be done: rough-idea.md carries the user's idea")
	}
	if done["Requirements"] {
		t.Error("Requirements ticked on a headings-only skeleton file")
	}
	if done["Research"] {
		t.Error("Research ticked on an empty research directory")
	}
}

// Once the agent actually records a Q&A, Requirements is done.
func TestDetectPlanPhases_RequirementsDoneOnceAnswered(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := "# Requirements\n\n## Questions & Answers\n\n### Q1. Scope?\n**A.** The sidebar only.\n"
	if err := os.WriteFile(filepath.Join(dir, "requirements.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("writing requirements.md: %v", err)
	}

	for _, p := range detectPlanPhases(dir) {
		if p.Name == "Requirements" && !p.Done {
			t.Error("Requirements should be done once a Q&A has been recorded")
		}
	}
}

func TestDetectPlanPhases_None(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	phases := detectPlanPhases(dir)
	if len(phases) != 7 {
		t.Fatalf("expected 7 phases, got %d", len(phases))
	}
	for _, p := range phases {
		if p.Done {
			t.Errorf("phase %q should not be Done in an empty dir, got Done=%v", p.Name, p.Done)
		}
	}
}

func TestSidebarPlanLines(t *testing.T) {
	t.Parallel()
	in := SidebarRenderInput{PlanPhases: []PlanPhase{
		{Name: "Idea", Done: true},
		{Name: "Requirements", Done: false},
		{Name: "Research", Done: false},
	}}
	out := plain(sidebarPlanLines(in, 27, testSidebarStyles()))
	for _, want := range []string{"Plan", "[x] Idea", "▶ Requirements", "[ ] Research"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in:\n%s", want, out)
		}
	}
}

func TestSidebarPlanLines_Hidden(t *testing.T) {
	t.Parallel()
	got := sidebarPlanLines(SidebarRenderInput{}, 27, testSidebarStyles())
	if len(got) != 0 {
		t.Errorf("expected 0 lines when no PlanPhases, got %d: %q", len(got), plain(got))
	}
}

// TestSidebarModelLinesThinkingLevel pins the reasoning level row under the
// model name.
//
// The row is the only place the configured level is visible: it is threaded
// into provider.NewLLM at startup and never surfaced again, so a level set in
// ~/.pi-go/config.json was previously unverifiable from inside a session — the
// sidebar claimed to show it (Config.ThinkingLevel's comment) and did not.
func TestSidebarModelLinesThinkingLevel(t *testing.T) {
	t.Parallel()

	t.Run("shown under the model name", func(t *testing.T) {
		t.Parallel()
		out := plain(sidebarModelLines(SidebarRenderInput{
			ProviderName:  "anthropic",
			ModelName:     "claude-opus-4-7",
			ThinkingLevel: "high",
		}, 27, testSidebarStyles()))

		if !strings.Contains(out, "◆ high") {
			t.Errorf("thinking level missing from the Model section:\n%s", out)
		}
		// Order matters: the level belongs to the model above it, so it must
		// come after the model name rather than above it.
		model := strings.Index(out, "claude-opus-4-7")
		level := strings.Index(out, "◆ high")
		if model < 0 || level < 0 || level < model {
			t.Errorf("thinking level should follow the model name:\n%s", out)
		}
	})

	t.Run("hidden when unset", func(t *testing.T) {
		t.Parallel()
		// Empty is the zero value and means "provider default", so there is
		// nothing to show — an empty row would read as a level called "".
		out := plain(sidebarModelLines(SidebarRenderInput{
			ProviderName: "anthropic",
			ModelName:    "claude-opus-4-7",
		}, 27, testSidebarStyles()))

		if strings.Contains(out, "◆") {
			t.Errorf("unset level should render no row:\n%s", out)
		}
	})

	t.Run("every level renders", func(t *testing.T) {
		t.Parallel()
		for _, level := range []string{"none", "low", "medium", "high", "max"} {
			out := plain(sidebarModelLines(SidebarRenderInput{
				ModelName:     "m",
				ThinkingLevel: level,
			}, 27, testSidebarStyles()))
			if !strings.Contains(out, "◆ "+level) {
				t.Errorf("level %q did not render:\n%s", level, out)
			}
		}
	})

	t.Run("long level is truncated, not wrapped", func(t *testing.T) {
		t.Parallel()
		// The level is capped at innerW like the model name above it, so a long
		// value ends in an ellipsis rather than running the column wide. The
		// frame's MaxWidth(w) would clamp an overflow anyway, but it would do so
		// by cutting the row mid-character with no ellipsis — the reader would
		// see a truncated word and not know it was truncated. Truncating here
		// keeps the row bounded and honest at the same time.
		out := plain(sidebarModelLines(SidebarRenderInput{
			ModelName:     "m",
			ThinkingLevel: strings.Repeat("x", 60),
		}, 12, testSidebarStyles()))
		if !strings.Contains(out, "…") {
			t.Errorf("a long level should be ellipsized, got:\n%s", out)
		}
		// The cap applies to the level text, so the row is at most 2 cells of
		// leading indent plus innerW — the same budget the model name uses.
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "x") && runewidth.StringWidth(line) > 2+12 {
				t.Errorf("level row %q exceeds the model name budget", line)
			}
		}
	})
}

// TestRenderSidebarThinkingLevelVisible is the end-to-end version: the row has
// to survive sidebarFrame's padding and clamping, not just the section
// renderer. A section that renders correctly and is then clipped by the frame
// would still be invisible to the user.
func TestRenderSidebarThinkingLevelVisible(t *testing.T) {
	t.Parallel()
	out := ansi.Strip(RenderSidebar(SidebarRenderInput{
		Width:         SidebarWidth,
		Height:        40,
		ProviderName:  "anthropic",
		ModelName:     "claude-opus-4-7",
		ThinkingLevel: "medium",
		GitBranch:     "main",
	}))
	if !strings.Contains(out, "◆ medium") {
		t.Errorf("thinking level did not survive the sidebar frame:\n%s", out)
	}
}
