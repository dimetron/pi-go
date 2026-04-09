package tui

import (
	"context"
	"strings"
	"testing"
)

func TestRenderSidebar_Minimal(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 20,
	})
	if result == "" {
		t.Error("expected non-empty sidebar")
	}
	if !strings.Contains(result, "Context") {
		t.Error("expected Context section")
	}
	if !strings.Contains(result, "Model") {
		t.Error("expected Model section")
	}
	if !strings.Contains(result, "Mode") {
		t.Error("expected Mode section")
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
	result := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 20,
		Messages: []message{
			{role: "user", content: strings.Repeat("x", 8000)},
		},
	})
	// Should show token count estimate.
	if !strings.Contains(result, "tokens") {
		t.Error("expected token count display")
	}
}

func TestRenderSidebar_SmallTokenCount(t *testing.T) {
	result := RenderSidebar(SidebarRenderInput{
		Width:  30,
		Height: 20,
		Messages: []message{
			{role: "user", content: "hi"},
		},
	})
	if !strings.Contains(result, "tokens") {
		t.Error("expected token count display")
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
	t.Setenv("HOME", t.TempDir())
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

func (m *sidebarMockTokenTracker) TotalUsed() int64     { return m.totalUsed }
func (m *sidebarMockTokenTracker) Limit() int64         { return m.limit }
func (m *sidebarMockTokenTracker) Remaining() int64     { return m.remaining }
func (m *sidebarMockTokenTracker) PercentUsed() float64 { return m.percentUsed }
