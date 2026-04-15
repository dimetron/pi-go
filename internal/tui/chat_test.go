package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
)

func newTestRenderer(t *testing.T) *glamour.TermRenderer {
	t.Helper()
	r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(80))
	if err != nil {
		t.Fatalf("failed to create test renderer: %v", err)
	}
	return r
}

// --- wordWrap ---

func TestWordWrap_EmptyText(t *testing.T) {
	result := wordWrap("", 80)
	if len(result) != 1 || result[0] != "" {
		t.Errorf("expected [\"\"], got %v", result)
	}
}

func TestWordWrap_NarrowWidth(t *testing.T) {
	// maxWidth < 10 returns text as-is
	result := wordWrap("hello world", 5)
	if len(result) != 1 || result[0] != "hello world" {
		t.Errorf("expected [\"hello world\"], got %v", result)
	}
}

func TestWordWrap_ShortText(t *testing.T) {
	result := wordWrap("hello", 80)
	if len(result) != 1 || result[0] != "hello" {
		t.Errorf("expected [\"hello\"], got %v", result)
	}
}

func TestWordWrap_ExactWidth(t *testing.T) {
	text := strings.Repeat("a", 20)
	result := wordWrap(text, 20)
	if len(result) != 1 || result[0] != text {
		t.Errorf("expected single line of 20 chars, got %v", result)
	}
}

func TestWordWrap_BreaksAtWordBoundary(t *testing.T) {
	// "hello world foo bar" with width 12 should break at spaces
	result := wordWrap("hello world foo bar", 12)
	if len(result) < 2 {
		t.Fatalf("expected at least 2 lines, got %d: %v", len(result), result)
	}
	// First line should break at a word boundary
	for _, line := range result {
		if len(line) > 13 { // allow slight overflow before next char triggers wrap
			t.Errorf("line too long (%d): %q", len(line), line)
		}
	}
}

func TestWordWrap_LongWordNoSpaces(t *testing.T) {
	// A single long word with no spaces should be force-broken
	text := strings.Repeat("x", 30)
	result := wordWrap(text, 10)
	if len(result) < 2 {
		t.Fatalf("expected multiple lines for long word, got %d: %v", len(result), result)
	}
	// Rejoin should give us the original text
	joined := strings.Join(result, "")
	if joined != text {
		t.Errorf("joined text mismatch: got %q, want %q", joined, text)
	}
}

func TestWordWrap_NewlinesPreserved(t *testing.T) {
	result := wordWrap("line1\nline2\nline3", 80)
	if len(result) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(result), result)
	}
	if result[0] != "line1" || result[1] != "line2" || result[2] != "line3" {
		t.Errorf("unexpected lines: %v", result)
	}
}

func TestWordWrap_MixedNewlinesAndWrapping(t *testing.T) {
	// First part is short, second part needs wrapping
	text := "short\n" + strings.Repeat("word ", 20)
	result := wordWrap(text, 30)
	if len(result) < 3 {
		t.Fatalf("expected at least 3 lines, got %d: %v", len(result), result)
	}
	if result[0] != "short" {
		t.Errorf("first line should be 'short', got %q", result[0])
	}
}

func TestWordWrap_SpaceAtBreakPoint(t *testing.T) {
	// When the character at the break point is a space, it should be handled
	text := "aaa bbb ccc ddd"
	result := wordWrap(text, 10)
	if len(result) < 2 {
		t.Fatalf("expected at least 2 lines, got %d: %v", len(result), result)
	}
	// No line should have leading/trailing spaces from the break
	for _, line := range result {
		if strings.HasPrefix(line, " ") {
			t.Errorf("line has leading space: %q", line)
		}
	}
}

// --- NewChatModel ---

func TestChatModel_NewChatModel_NilRenderer(t *testing.T) {
	cm := NewChatModel(nil)
	if cm.Renderer != nil {
		t.Error("expected nil renderer")
	}
	if cm.Messages == nil {
		t.Fatal("expected non-nil Messages slice")
	}
	if len(cm.Messages) != 0 {
		t.Errorf("expected empty Messages, got %d", len(cm.Messages))
	}
}

func TestChatModel_NewChatModel_WithRenderer(t *testing.T) {
	r := newTestRenderer(t)
	cm := NewChatModel(r)
	if cm.Renderer == nil {
		t.Error("expected non-nil renderer")
	}
	if len(cm.Messages) != 0 {
		t.Errorf("expected empty Messages, got %d", len(cm.Messages))
	}
	if cm.Scroll != 0 {
		t.Errorf("expected Scroll=0, got %d", cm.Scroll)
	}
}

// --- Clear ---

func TestChatModel_Clear(t *testing.T) {
	cm := NewChatModel(nil)
	cm.Messages = append(cm.Messages,
		message{role: "user", content: "hello"},
		message{role: "assistant", content: "world"},
	)
	cm.Scroll = 5

	cm.Clear()

	if len(cm.Messages) != 0 {
		t.Errorf("expected 0 messages after Clear, got %d", len(cm.Messages))
	}
	if cm.Scroll != 0 {
		t.Errorf("expected Scroll=0 after Clear, got %d", cm.Scroll)
	}
}

// --- ResetScroll ---

func TestChatModel_ResetScroll(t *testing.T) {
	cm := NewChatModel(nil)
	cm.Scroll = 42

	cm.ResetScroll()

	if cm.Scroll != 0 {
		t.Errorf("expected Scroll=0, got %d", cm.Scroll)
	}
}

// --- ScrollUp ---

func TestChatModel_ScrollUp_Basic(t *testing.T) {
	cm := NewChatModel(nil)
	// Add enough messages so MaxScroll > 0 for a small height.
	for i := 0; i < 50; i++ {
		cm.Messages = append(cm.Messages, message{role: "user", content: "line"})
	}

	cm.ScrollUp(3, 100)
	if cm.Scroll != 3 {
		t.Errorf("expected Scroll=3, got %d", cm.Scroll)
	}
}

func TestChatModel_ScrollUp_ClampsToMax(t *testing.T) {
	cm := NewChatModel(nil)
	cm.Messages = append(cm.Messages, message{role: "user", content: "short"})

	// With very large height, MaxScroll should be 0 or small.
	cm.ScrollUp(9999, 1000)
	max := cm.MaxScroll(1000)
	if cm.Scroll != max {
		t.Errorf("expected Scroll clamped to %d, got %d", max, cm.Scroll)
	}
}

func TestChatModel_ScrollUp_EmptyMessages(t *testing.T) {
	cm := NewChatModel(nil)
	cm.ScrollUp(5, 50)
	// MaxScroll returns 0 for empty messages.
	if cm.Scroll != 0 {
		t.Errorf("expected Scroll=0 for empty messages, got %d", cm.Scroll)
	}
}

// --- ScrollDown ---

func TestChatModel_ScrollDown_Basic(t *testing.T) {
	cm := NewChatModel(nil)
	cm.Scroll = 10

	cm.ScrollDown(3)
	if cm.Scroll != 7 {
		t.Errorf("expected Scroll=7, got %d", cm.Scroll)
	}
}

func TestChatModel_ScrollDown_ClampsToZero(t *testing.T) {
	cm := NewChatModel(nil)
	cm.Scroll = 2

	cm.ScrollDown(10)
	if cm.Scroll != 0 {
		t.Errorf("expected Scroll=0, got %d", cm.Scroll)
	}
}

func TestChatModel_ScrollDown_AlreadyZero(t *testing.T) {
	cm := NewChatModel(nil)
	cm.Scroll = 0

	cm.ScrollDown(5)
	if cm.Scroll != 0 {
		t.Errorf("expected Scroll=0, got %d", cm.Scroll)
	}
}

// --- RenderMarkdown ---

func TestChatModel_RenderMarkdown_EmptyInput(t *testing.T) {
	cm := NewChatModel(newTestRenderer(t))
	result := cm.RenderMarkdown("")
	if result != "" {
		t.Errorf("expected empty string for empty input, got %q", result)
	}
}

func TestChatModel_RenderMarkdown_NilRenderer(t *testing.T) {
	cm := NewChatModel(nil)
	result := cm.RenderMarkdown("hello world")
	if result != "hello world" {
		t.Errorf("expected raw text with nil renderer, got %q", result)
	}
}

func TestChatModel_RenderMarkdown_RendersMarkdown(t *testing.T) {
	cm := NewChatModel(newTestRenderer(t))
	result := cm.RenderMarkdown("**bold**")
	if result == "**bold**" {
		t.Error("expected markdown to be rendered, got raw text")
	}
	// The rendered output should not have trailing newlines (trimmed).
	if strings.HasSuffix(result, "\n") {
		t.Error("expected trailing newlines to be trimmed")
	}
}

// --- RenderMessages ---

func TestChatModel_RenderMessages_EmptyShowsWelcome(t *testing.T) {
	cm := NewChatModel(nil)
	result := cm.RenderMessages(false)
	if !strings.Contains(result, "Welcome to pi-go") {
		t.Error("expected welcome screen for empty messages")
	}
}

func TestChatModel_RenderMessages_UserMessage(t *testing.T) {
	cm := NewChatModel(nil)
	cm.Messages = append(cm.Messages, message{role: "user", content: "hello there"})

	result := cm.RenderMessages(false)
	if !strings.Contains(result, "hello there") {
		t.Error("expected user message content in output")
	}
	if !strings.Contains(result, ">") {
		t.Error("expected '>' prefix for user message")
	}
}

func TestChatModel_RenderMessages_AssistantMessage(t *testing.T) {
	cm := NewChatModel(nil)
	cm.Messages = append(cm.Messages, message{role: "assistant", content: "I can help"})

	result := cm.RenderMessages(false)
	if !strings.Contains(result, "I can help") {
		t.Error("expected assistant message content in output")
	}
}

func TestChatModel_RenderMessages_WarningMessage(t *testing.T) {
	cm := NewChatModel(nil)
	cm.Messages = append(cm.Messages, message{
		role:      "assistant",
		content:   "watch out",
		isWarning: true,
	})

	result := cm.RenderMessages(false)
	if !strings.Contains(result, "watch out") {
		t.Error("expected warning content in output")
	}
	if !strings.Contains(result, "⚠") {
		t.Error("expected warning bullet in output")
	}
}

func TestChatModel_RenderMessages_ThinkingMessage(t *testing.T) {
	cm := NewChatModel(nil)
	cm.Messages = append(cm.Messages, message{role: "thinking", content: "let me think..."})

	result := cm.RenderMessages(false)
	if !strings.Contains(result, "let me think...") {
		t.Error("expected thinking content in output")
	}
}

func TestChatModel_RenderMessages_ThinkingEmpty(t *testing.T) {
	cm := NewChatModel(nil)
	cm.Messages = append(cm.Messages, message{role: "thinking", content: ""})

	result := cm.RenderMessages(false)
	// Empty thinking should not add any thinking-related content.
	if strings.Contains(result, "💭") {
		t.Error("did not expect thinking bullet for empty thinking message")
	}
}

func TestChatModel_RenderMessages_ToolMessage(t *testing.T) {
	cm := NewChatModel(nil)
	cm.ToolDisplay = ToolDisplayModel{Width: 80, CompactTools: true}
	cm.Messages = append(cm.Messages, message{
		role:    "tool",
		tool:    "read",
		toolIn:  "main.go",
		content: "file contents",
	})

	result := cm.RenderMessages(false)
	if !strings.Contains(result, "read") {
		t.Error("expected tool name in output")
	}
}

func TestChatModel_RenderMessages_RunningEmptyAssistant(t *testing.T) {
	cm := NewChatModel(nil)
	cm.Messages = append(cm.Messages, message{role: "assistant", content: ""})

	// Not running: empty assistant should produce no content.
	resultNotRunning := cm.RenderMessages(false)
	if strings.Contains(resultNotRunning, "...") {
		t.Error("did not expect '...' when not running")
	}

	// Running: last empty assistant should show "...".
	resultRunning := cm.RenderMessages(true)
	if !strings.Contains(resultRunning, "...") {
		t.Error("expected '...' placeholder for empty assistant while running")
	}
}

func TestChatModel_RenderMessages_RunningDoesNotUseStaleCache(t *testing.T) {
	cm := NewChatModel(nil)
	cm.Messages = append(cm.Messages,
		message{role: "assistant", content: "This"},
		message{role: "tool", tool: "tree", content: "58 dirs"},
	)

	first := cm.RenderMessages(true)
	if !strings.Contains(first, "This") {
		t.Fatal("expected initial assistant content in output")
	}

	// Simulate later streaming updates to an earlier assistant message.
	cm.Messages[0].content = "This repository is pi-go."
	second := cm.RenderMessages(true)
	if !strings.Contains(second, "This repository is pi-go.") {
		t.Fatalf("expected updated assistant content while running, got: %q", second)
	}
}

func TestChatModel_RenderMessages_Separator(t *testing.T) {
	cm := NewChatModel(nil)
	cm.Width = 80
	cm.Messages = append(cm.Messages,
		message{role: "user", content: "first"},
		message{role: "user", content: "second"},
	)

	result := cm.RenderMessages(false)
	if !strings.Contains(result, "───") {
		t.Error("expected separator between user messages")
	}
}

func TestChatModel_RenderMessages_ThinkingTruncatesLongContent(t *testing.T) {
	cm := NewChatModel(nil)
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "thinking line")
	}
	cm.Messages = append(cm.Messages, message{role: "thinking", content: strings.Join(lines, "\n")})

	result := cm.RenderMessages(false)
	// The thinking display is capped at 6 lines, so should not have all 20.
	resultLines := strings.Split(result, "\n")
	thinkingLines := 0
	for _, l := range resultLines {
		if strings.Contains(l, "thinking line") {
			thinkingLines++
		}
	}
	if thinkingLines > 6 {
		t.Errorf("expected at most 6 thinking lines, got %d", thinkingLines)
	}
}

// --- UpdateRenderer ---

func TestChatModel_UpdateRenderer(t *testing.T) {
	cm := NewChatModel(nil)
	if cm.Renderer != nil {
		t.Fatal("expected nil renderer initially")
	}

	cm.UpdateRenderer(120)

	if cm.Renderer == nil {
		t.Error("expected non-nil renderer after UpdateRenderer")
	}
	if cm.Width != 120 {
		t.Errorf("expected Width=120, got %d", cm.Width)
	}
}

func TestChatModel_UpdateRenderer_MinWidth(t *testing.T) {
	cm := NewChatModel(nil)
	cm.UpdateRenderer(10) // very narrow

	if cm.Renderer == nil {
		t.Error("expected non-nil renderer even with small width")
	}
	if cm.Width != 10 {
		t.Errorf("expected Width=10, got %d", cm.Width)
	}

	// Renderer should still work after narrow width.
	result := cm.RenderMarkdown("test content")
	if !strings.Contains(result, "test content") {
		t.Error("expected rendered content even with narrow width")
	}
}

// --- MaxScroll ---

func TestChatModel_MaxScroll_Empty(t *testing.T) {
	cm := NewChatModel(nil)
	if cm.MaxScroll(50) != 0 {
		t.Errorf("expected MaxScroll=0 for empty messages, got %d", cm.MaxScroll(50))
	}
}

func TestChatModel_MaxScroll_SmallHeight(t *testing.T) {
	cm := NewChatModel(nil)
	cm.Messages = append(cm.Messages, message{role: "user", content: "hi"})
	// Height of 1 means availableHeight = 1-3 = -2, clamped to 0 -> returns 0.
	if cm.MaxScroll(1) != 0 {
		t.Errorf("expected MaxScroll=0 for tiny height, got %d", cm.MaxScroll(1))
	}
}

// --- AppendWarning ---

func TestChatModel_AppendWarning(t *testing.T) {
	cm := NewChatModel(nil)
	cm.Scroll = 10

	cm.AppendWarning("danger!")

	if len(cm.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(cm.Messages))
	}
	msg := cm.Messages[0]
	if msg.role != "assistant" {
		t.Errorf("expected role=assistant, got %s", msg.role)
	}
	if !msg.isWarning {
		t.Error("expected isWarning=true")
	}
	if msg.content != "danger!" {
		t.Errorf("expected content='danger!', got %q", msg.content)
	}
	if cm.Scroll != 0 {
		t.Errorf("expected Scroll reset to 0, got %d", cm.Scroll)
	}
}

// --- Helper functions ---

func TestChatModel_CountByRole(t *testing.T) {
	msgs := []message{
		{role: "user", content: "a"},
		{role: "assistant", content: "b"},
		{role: "user", content: "c"},
		{role: "tool", content: "d"},
	}
	if n := countByRole(msgs, "user"); n != 2 {
		t.Errorf("expected 2 user messages, got %d", n)
	}
	if n := countByRole(msgs, "assistant"); n != 1 {
		t.Errorf("expected 1 assistant message, got %d", n)
	}
	if n := countByRole(msgs, "tool"); n != 1 {
		t.Errorf("expected 1 tool message, got %d", n)
	}
	if n := countByRole(msgs, "thinking"); n != 0 {
		t.Errorf("expected 0 thinking messages, got %d", n)
	}
}

func TestChatModel_FormatTokenCount(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0"},
		{500, "500"},
		{999, "999"},
		{1000, "1.0k"},
		{1500, "1.5k"},
		{999999, "1000.0k"},
		{1000000, "1.0M"},
		{2500000, "2.5M"},
	}
	for _, tc := range tests {
		got := formatTokenCount(tc.input)
		if got != tc.expected {
			t.Errorf("formatTokenCount(%d) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}
