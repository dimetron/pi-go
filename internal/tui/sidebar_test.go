package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/palace"
	"github.com/dimetron/pi-go/internal/testenv"
)

func TestRenderSidebar_Minimal(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 20,
	})
	if result == "" {
		t.Error("expected non-empty sidebar")
	}
	// The Context section was removed: the bottom rule is the canonical gauge.
	// Only Model and Mode must still appear.
	if strings.Contains(result, "Context") {
		t.Error("sidebar should no longer render a Context section — use the bottom gauge")
	}
	if !strings.Contains(result, "Model") {
		t.Error("expected Model section")
	}
	if !strings.Contains(result, "Mode") {
		t.Error("expected Mode section")
	}
}

func TestRenderSidebar_WithoutPiSection(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:      30,
		Height:     20,
		AppVersion: "1.2.3",
		HostName:   "dev-host",
		FolderName: "pi-go",
	})
	if strings.Contains(result, "Pi") {
		t.Error("expected Pi section to be hidden")
	}
	if strings.Contains(result, "pi #1.2.3") {
		t.Error("expected pi version to be hidden")
	}
	if strings.Contains(result, "host: dev-host") {
		t.Error("expected hostname to be hidden")
	}
	if strings.Contains(result, "dir: pi-go") {
		t.Error("expected folder name to be hidden")
	}
}

func TestRenderSidebar_WithAllSections(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:        30,
		Height:       30,
		Eyes:         "(o o)",
		Mode:         "plan",
		ProviderName: "anthropic",
		ModelName:    "claude-sonnet",
		GitBranch:    "feature-branch",
		DiffAdded:    10,
		DiffRemoved:  3,
		Running:      true,
		ActiveTool:   "bash",
		TokenTracker: &sidebarMockTokenTracker{totalUsed: 5000, limit: 100000, remaining: 95000, percentUsed: 5.0},
		Messages:     nil,
		LoadingItems: map[string]bool{
			"lsp":    true,
			"memory": false,
		},
	})
	if !strings.Contains(result, "anthropic") {
		t.Error("expected provider name")
	}
	if !strings.Contains(result, "claude-sonnet") {
		t.Error("expected model name")
	}
	if !strings.Contains(result, "feature-branch") {
		t.Error("expected git branch")
	}
	if !strings.Contains(result, "plan") {
		t.Error("expected plan mode")
	}
	if !strings.Contains(result, "bash") {
		t.Error("expected active tool")
	}
	if !strings.Contains(result, "Loading") {
		t.Error("expected Loading section")
	}
}

func TestRenderSidebar_NarrowWidth(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:  5,
		Height: 10,
	})
	if result == "" {
		t.Error("expected non-empty sidebar even with narrow width")
	}
}

func TestRenderSidebar_LongModelName(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:     20,
		Height:    10,
		ModelName: "this-is-a-very-long-model-name-that-exceeds-the-width",
	})
	if !strings.Contains(result, "…") {
		t.Error("expected truncated model name with ellipsis")
	}
}

func TestRenderSidebar_RunningNoTool(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:   30,
		Height:  10,
		Running: true,
	})
	if !strings.Contains(result, "...") {
		t.Error("expected spinner verb with '...' when running without active tool")
	}
}

func TestRenderSidebar_NoTokenTracker(t *testing.T) {
	// Without a TokenTracker and without the now-removed Context section, the
	// sidebar has nothing to say about tokens. Assert it stays non-empty and
	// does not surface a stale token estimate.
	result := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 20,
		Messages: []message{
			{role: "user", content: strings.Repeat("x", 8000)},
		},
	})
	if result == "" {
		t.Error("expected non-empty sidebar")
	}
	if strings.Contains(result, "tokens") {
		t.Error("the sidebar no longer renders a token estimate — only the bottom gauge does")
	}
}

func TestRenderSidebar_SmallTokenCount(t *testing.T) {
	// Same as TestRenderSidebar_NoTokenTracker: the sidebar does not display
	// tokens anywhere now. Pinned so a future regression that re-adds the
	// Context section is caught.
	result := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 20,
		Messages: []message{
			{role: "user", content: "hi"},
		},
	})
	if strings.Contains(result, "tokens") {
		t.Error("the sidebar no longer renders a token count")
	}
}

func TestRenderSidebar_ChatMode(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 10,
		Mode:   "chat",
	})
	if !strings.Contains(result, "chat") {
		t.Error("expected 'chat' mode")
	}
}

func TestRenderSidebar_HeightTruncation(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:     30,
		Height:    3, // Very small height.
		Eyes:      "(o o)",
		GitBranch: "main",
		Mode:      "chat",
	})
	if result == "" {
		t.Error("expected non-empty sidebar")
	}
}

// --- sortedKeys ---

func TestSortedKeys(t *testing.T) {
	m := map[string]bool{
		"zebra": true,
		"alpha": false,
		"mango": true,
	}
	keys := sortedKeys(m)
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
	if keys[0] != "alpha" || keys[1] != "mango" || keys[2] != "zebra" {
		t.Errorf("expected sorted keys, got %v", keys)
	}
}

func TestSortedKeys_Empty(t *testing.T) {
	keys := sortedKeys(nil)
	if len(keys) != 0 {
		t.Errorf("expected 0 keys, got %d", len(keys))
	}
}

// --- handleRestartCommand ---

func TestHandleRestartCommand(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	newM, cmd := m.handleRestartCommand()
	mm := newM.(*model)

	if !mm.quitting {
		t.Error("expected quitting to be true")
	}
	if cmd == nil {
		t.Error("expected non-nil cmd")
	}
}

// --- historyPathPlain ---

func TestHistoryPathPlain(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	path := historyPathPlain()
	// Should return a non-empty path under HOME.
	if path == "" {
		t.Error("expected non-empty path")
	}
	if !strings.Contains(path, "history") {
		t.Errorf("expected 'history' in path, got %q", path)
	}
}

// --- cancelAgent with face ---

func TestCancelAgent_WithFace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	face := NewFaceRenderer()
	face.SetMood(MoodThinking)

	m := &model{
		ctx:       ctx,
		cancel:    cancel,
		face:      face,
		chatModel: ChatModel{},
		running:   true,
	}

	m.cancelAgent()
	if m.running {
		t.Error("expected running to be false")
	}
}

func TestRenderSidebar_RunChecklist(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 30,
		RunChecklist: []ChecklistStep{
			{Title: "Setup project", Done: true},
			{Title: "Implement feature", Done: false},
			{Title: "Write tests", Done: true},
		},
		RunPhase:    "running",
		RunSpec:     "my-spec",
		RunCycle:    2,
		RunMaxCycle: 10,
		Running:     true,
		ActiveTool:  "edit",
	})

	if !strings.Contains(result, "Run: my-spec") {
		t.Error("expected run heading")
	}
	if !strings.Contains(result, "cycle 2/10") {
		t.Error("expected cycle info")
	}
	if !strings.Contains(result, "[x] Setup project") {
		t.Error("expected checked item with [x]")
	}
	if !strings.Contains(result, "[ ] Implement feature") {
		t.Error("expected unchecked item with [ ]")
	}
	if !strings.Contains(result, "edit") {
		t.Error("expected active tool shown")
	}
	// Should NOT show the normal [chat] mode section.
	if strings.Contains(result, "[chat]") {
		t.Error("should not show chat mode when run checklist is active")
	}
}

func TestRenderSidebar_RunChecklistTruncatesLongTitles(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 20,
		RunChecklist: []ChecklistStep{
			{Title: "This is a very long title that should get truncated", Done: false},
		},
		RunPhase:    "running",
		RunSpec:     "spec",
		RunCycle:    1,
		RunMaxCycle: 5,
	})

	if !strings.Contains(result, "…") {
		t.Error("expected truncated title with ellipsis")
	}
}

func TestRenderSidebar_RunChecklistThinkingNoTool(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 20,
		RunChecklist: []ChecklistStep{
			{Title: "Step 1", Done: false},
		},
		RunPhase:    "running",
		RunSpec:     "spec",
		RunCycle:    1,
		RunMaxCycle: 5,
		Running:     true,
	})

	if !strings.Contains(result, "thinking") {
		t.Error("expected 'thinking...' when running without active tool")
	}
}

func TestRenderSidebar_EmptyChecklistShowsNormalMode(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:    30,
		Height:   20,
		RunPhase: "running",
		RunSpec:  "spec",
	})

	// Empty checklist should fall through to normal mode display.
	if !strings.Contains(result, "Mode") {
		t.Error("expected normal Mode section with empty checklist")
	}
}

// sidebarMockTokenTracker implements TokenTracker for sidebar tests.
type sidebarMockTokenTracker struct {
	totalUsed   int64
	limit       int64
	remaining   int64
	percentUsed float64
}

func (m *sidebarMockTokenTracker) TotalUsed() int64            { return m.totalUsed }
func (m *sidebarMockTokenTracker) Limit() int64                { return m.limit }
func (m *sidebarMockTokenTracker) Remaining() int64            { return m.remaining }
func (m *sidebarMockTokenTracker) PercentUsed() float64        { return m.percentUsed }
func (m *sidebarMockTokenTracker) LastPromptTokens() int64     { return 0 }
func (m *sidebarMockTokenTracker) SetLastPromptTokens(int64)   {}
func (m *sidebarMockTokenTracker) ResetContextWindow()         {}
func (m *sidebarMockTokenTracker) ContextWindowSize() int64    { return 0 }
func (m *sidebarMockTokenTracker) ContextPercentUsed() float64 { return 0 }
func (m *sidebarMockTokenTracker) LastCachedTokens() int64     { return 0 }
func (m *sidebarMockTokenTracker) CachedTokensToday() int64    { return 0 }
func (m *sidebarMockTokenTracker) CacheHitRateToday() float64  { return 0 }
func (m *sidebarMockTokenTracker) BodyTokens() int64           { return 0 }
func (m *sidebarMockTokenTracker) CachePrefixTokens() int64    { return 0 }

func TestAgentStatusPriority(t *testing.T) {
	tests := []struct {
		status string
		want   int
	}{
		{"running", 0},
		{"done", 1},
		{"completed", 1},
		{"failed", 2},
		{"killed", 3},
		{"", 4},
		{"unknown", 4},
	}
	for _, tt := range tests {
		if got := agentStatusPriority(tt.status); got != tt.want {
			t.Errorf("agentStatusPriority(%q) = %d, want %d", tt.status, got, tt.want)
		}
	}
}

func TestRenderSidebar_MCPTools(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 40,
		MCPTools: []extension.MCPToolEntry{
			{Server: "filesystem", Tool: "read_file"},
			{Server: "filesystem", Tool: "write_file"},
			{Server: "search", Tool: "web_search"},
		},
	})
	if !strings.Contains(result, "MCP Tools") {
		t.Error("expected 'MCP Tools' heading in sidebar")
	}
	if !strings.Contains(result, "filesystem") {
		t.Error("expected server name 'filesystem' in sidebar")
	}
	if strings.Contains(result, "read_file") {
		t.Error("expected tool 'read_file' to be hidden from sidebar")
	}
	if strings.Contains(result, "write_file") {
		t.Error("expected tool 'write_file' to be hidden from sidebar")
	}
	if !strings.Contains(result, "search") {
		t.Error("expected server name 'search' in sidebar")
	}
	if strings.Contains(result, "web_search") {
		t.Error("expected tool 'web_search' to be hidden from sidebar")
	}
	if !strings.Contains(result, "[3]") {
		t.Error("expected '[3]' tool count in MCP Tools heading")
	}
	if !strings.Contains(result, "[2]") {
		t.Error("expected '[2]' tool count for filesystem server")
	}
	if !strings.Contains(result, "[1]") {
		t.Error("expected '[1]' tool count for search server")
	}
}

func TestRenderSidebar_A2AAgents(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 40,
		A2AAgents: []A2AAgentEntry{
			{Name: "k8s-agent"},
			{Name: "istio-agent"},
			{Name: "helm-agent"},
		},
	})
	if !strings.Contains(result, "A2A Agents") {
		t.Error("expected 'A2A Agents' heading in sidebar")
	}
	if !strings.Contains(result, "[3]") {
		t.Error("expected '[3]' agent count in A2A Agents heading")
	}
	for _, name := range []string{"k8s-agent", "istio-agent", "helm-agent"} {
		if !strings.Contains(result, name) {
			t.Errorf("expected agent %q in sidebar", name)
		}
	}
}

func TestRenderSidebar_A2AAgents_EmptyHidden(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 20,
	})
	if strings.Contains(result, "A2A Agents") {
		t.Error("expected no 'A2A Agents' section when A2AAgents is nil")
	}
}

func TestRenderSidebar_A2AAppearsAfterMCPTools(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 40,
		MCPTools: []extension.MCPToolEntry{
			{Server: "filesystem", Tool: "read_file"},
		},
		A2AAgents: []A2AAgentEntry{
			{Name: "k8s-agent"},
		},
	})
	if strings.Index(result, "A2A Agents [1]") < strings.Index(result, "MCP Tools [1]") {
		t.Error("expected A2A Agents section below MCP Tools section")
	}
}

func TestRenderSidebar_Skills(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 30,
		Skills: []extension.Skill{
			{Name: "go-code-review"},
			{Name: "go-testing"},
		},
		MCPTools: []extension.MCPToolEntry{{Server: "filesystem", Tool: "read_file"}},
	})
	if !strings.Contains(result, "Skills [2]") {
		t.Error("expected 'Skills [2]' heading in sidebar")
	}
	if !strings.Contains(result, "MCP Tools [1]") {
		t.Error("expected 'MCP Tools [1]' heading in sidebar")
	}
	if strings.Index(result, "Skills [2]") > strings.Index(result, "MCP Tools [1]") {
		t.Error("expected Skills section above MCP Tools section")
	}
}

func TestRenderSidebar_MCPTools_Empty(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:    30,
		Height:   20,
		MCPTools: nil,
	})
	if strings.Contains(result, "MCP Tools") {
		t.Error("expected no 'MCP Tools' section when MCPTools is nil")
	}
}

func TestRenderSidebar_MCPTools_LongNames(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 30,
		MCPTools: []extension.MCPToolEntry{
			{Server: "very-long-server-name-that-exceeds-width", Tool: "a_very_long_tool_name_that_also_exceeds_sidebar_width"},
		},
	})
	if !strings.Contains(result, "MCP Tools") {
		t.Error("expected 'MCP Tools' heading")
	}
	_ = result
}

func TestRenderSidebar_MemoryStatus_Full(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:  40,
		Height: 40,
		MemoryStatus: &palace.PalaceStatus{
			DrawerCount: 42,
			WingCount:   3,
			RoomCount:   7,
			KG: &palace.KGStats{
				EntityCount:   12,
				TripleCount:   30,
				ActiveTriples: 25,
			},
			ModelLoaded: true,
		},
	})
	if !strings.Contains(result, "Memory [42]") {
		t.Error("expected 'Memory [42]' heading with drawer count")
	}
	if !strings.Contains(result, "model ready") {
		t.Error("expected 'model ready' line when ModelLoaded is true")
	}
	if !strings.Contains(result, "12 entities") {
		t.Error("expected '12 entities' line when KG is non-nil")
	}
	if !strings.Contains(result, "7 rooms") {
		t.Error("expected '7 rooms' line for RoomCount")
	}
}

func TestRenderSidebar_MemoryStatus_NoKG(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:  40,
		Height: 40,
		MemoryStatus: &palace.PalaceStatus{
			DrawerCount: 5,
			RoomCount:   2,
			KG:          nil,
			ModelLoaded: false,
		},
	})
	if !strings.Contains(result, "Memory [5]") {
		t.Error("expected 'Memory [5]' heading")
	}
	if strings.Contains(result, "model ready") {
		t.Error("did not expect 'model ready' when ModelLoaded is false")
	}
	if strings.Contains(result, "entities") {
		t.Error("did not expect 'entities' line when KG is nil")
	}
	if !strings.Contains(result, "2 rooms") {
		t.Error("expected '2 rooms' line for RoomCount")
	}
}

func TestRenderSidebar_MemoryStatus_Nil(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:        40,
		Height:       20,
		MemoryStatus: nil,
	})
	if strings.Contains(result, "Memory [") {
		t.Error("did not expect Memory section when MemoryStatus is nil")
	}
}

func TestRenderSidebar_Artifacts_EmptyHidden(t *testing.T) {
	baseline := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 30,
	})
	nilList := RenderSidebar(SidebarRenderInput{
		Width:     30,
		Height:    30,
		Artifacts: nil,
	})
	emptyList := RenderSidebar(SidebarRenderInput{
		Width:     30,
		Height:    30,
		Artifacts: []ArtifactEntry{},
	})
	for name, got := range map[string]string{
		"baseline": baseline,
		"nil":      nilList,
		"empty":    emptyList,
	} {
		if strings.Contains(got, "Artifacts [") {
			t.Errorf("case %s: did not expect Artifacts section when list is empty", name)
		}
	}
}

func TestRenderSidebar_Artifacts_Populated(t *testing.T) {
	// Sizes picked so each tier renders distinctly:
	// 124_000 bytes → "124 KB" (just under the 128 KB boundary? no — 124_000/1024 = 121 → "121 KB")
	// Actually 124*1024 = 126976 → 126976/1024 = 124 → "124 KB". Good.
	// 2_200_000 → 2_200_000/1024 = 2148 KB, /1024 = 2.098 → "2.1 MB"
	// 812 → "812 B"
	result := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 30,
		Artifacts: []ArtifactEntry{
			{Filename: "screenshot.png", Size: 124 * 1024, Mime: "image/png"},
			{Filename: "error.jpg", Size: 2_200_000, Mime: "image/jpeg"},
			{Filename: "notes.txt", Size: 812, Mime: "text/plain"},
		},
	})
	if !strings.Contains(result, "Artifacts [3]") {
		t.Error("expected Artifacts [3] heading")
	}
	// image/* entries get the framed-picture icon, others get the paperclip.
	if !strings.Contains(result, "🖼") {
		t.Error("expected image icon for image/* entries")
	}
	if !strings.Contains(result, "📎") {
		t.Error("expected paperclip icon for non-image entry")
	}
	// Sizes: 124 KB, 2.1 MB, 812 B.
	for _, want := range []string{"124 KB", "2.1 MB", "812 B"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected size %q in rendered output", want)
		}
	}
	// Filenames that fit. "error.jpg" (9 chars) and "notes.txt" (9 chars)
	// fit the innerW-12 budget for a 30-wide sidebar. "screenshot.png" is
	// 14 chars and gets truncated to "screenshot.…" — assert that behavior
	// explicitly.
	stripped := ansi.Strip(result)
	for _, want := range []string{"error.jpg", "notes.txt", "screenshot.…"} {
		if !strings.Contains(stripped, want) {
			t.Errorf("expected %q in rendered output", want)
		}
	}
	// Truncated filename should NOT appear in full.
	if strings.Contains(stripped, "screenshot.png") {
		t.Error("did not expect untruncated 'screenshot.png' — sidebar too narrow")
	}
}

func TestRenderSidebar_PlanChecklist(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 30,
		Mode:   "plan",
		PlanPhases: []PlanPhase{
			{Name: "Idea", Done: true},
			{Name: "Requirements", Done: false},
			{Name: "Research", Done: false},
			{Name: "Design", Done: false},
			{Name: "Outline", Done: false},
			{Name: "Plan", Done: false},
			{Name: "Prompt", Done: false},
		},
	})
	stripped := ansi.Strip(result)
	for _, want := range []string{"Plan", "[x] Idea", "▶ Requirements", "[ ] Research"} {
		if !strings.Contains(stripped, want) {
			t.Errorf("expected %q in rendered output:\n%s", want, stripped)
		}
	}
	if strings.Contains(stripped, "[chat]") {
		t.Error("did not expect [chat] mode in plan-mode sidebar")
	}
}

func TestRenderSidebar_NoPlanSection(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 20,
		Mode:   "chat",
	})
	stripped := ansi.Strip(result)
	if strings.Contains(stripped, "▶ ") {
		t.Error("did not expect a current-phase marker when no PlanPhases are present")
	}
	if strings.Contains(stripped, "[x] Idea") {
		t.Error("did not expect a plan checklist row when no PlanPhases are present")
	}
}
