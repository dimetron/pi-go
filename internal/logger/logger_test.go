package logger

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/testenv"
)

func TestNew(t *testing.T) {
	// Create a temp directory for testing
	tmpDir := t.TempDir()

	// Set HOME env var to use temp dir
	testenv.SetHome(t, tmpDir)

	// Now New() will use our temp dir as home
	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if log == nil {
		t.Fatal("New() returned nil")
	}

	// Check path is set
	if log.Path() == "" {
		t.Error("Path() returned empty string")
	}

	// Check that the file was created
	if _, err := os.Stat(log.Path()); os.IsNotExist(err) {
		t.Errorf("log file was not created at %s", log.Path())
	}

	// Close the logger
	if err := log.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestNewHomeDirError(t *testing.T) {
	testenv.SetUnwritableHome(t)

	_, err := New()
	if err == nil {
		t.Error("New() should return error when home dir doesn't exist")
	}
}

func TestPath(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { log.Close() }() //nolint:errcheck

	path := log.Path()
	if path == "" {
		t.Error("Path() should not be empty")
	}
	if !filepath.IsAbs(path) {
		t.Errorf("Path() should return absolute path, got %s", path)
	}
}

func TestClose(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Close should work
	if err := log.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Close again may return error since file is already closed
	// Just verify it doesn't panic
	_ = log.Close()
}

func TestCloseNil(t *testing.T) {
	var l *Logger
	if err := l.Close(); err != nil {
		t.Errorf("Close() on nil should return nil error, got %v", err)
	}
}

func TestLog(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { log.Close() }() //nolint:errcheck

	// Log an entry
	log.Log(Entry{Type: "info", Content: "test message"})

	// Log on nil should not panic
	var nilLog *Logger
	nilLog.Log(Entry{Type: "info", Content: "test"})
}

func TestInfo(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { log.Close() }() //nolint:errcheck

	log.Info("test info message")
}

func TestError(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { log.Close() }() //nolint:errcheck

	log.Error("test error message")
}

func TestErrorf(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { log.Close() }() //nolint:errcheck

	log.Errorf("formatted %s %d", "error", 42)

	// Verify the formatted content was written to the log file.
	data, err := os.ReadFile(log.Path())
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	if !strings.Contains(string(data), "formatted error 42") {
		t.Errorf("expected log to contain formatted content, got %q", string(data))
	}
	if !strings.Contains(string(data), `"type":"error"`) {
		t.Errorf("expected log entry type to be error, got %q", string(data))
	}
}

func TestUserMessage(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { log.Close() }() //nolint:errcheck

	log.UserMessage("test user message")
}

func TestLLMText(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { log.Close() }() //nolint:errcheck

	log.LLMText("agent1", "some llm text")
}

func TestLLMTextCoalescesContiguousChunks(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	log.LLMText("pi", " use")
	log.LLMText("pi", " `")
	log.LLMText("pi", "g")
	log.LLMText("pi", "pt")
	log.LLMText("pi", "-")
	log.LLMText("pi", "5")
	log.LLMText("pi", ".")
	log.LLMText("pi", "4")
	log.LLMText("pi", "`,")

	if err := log.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	entries := readEntries(t, log.Path())
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	if entries[0].Type != "llm_text" {
		t.Fatalf("expected llm_text entry, got %q", entries[0].Type)
	}
	if entries[0].Agent != "pi" {
		t.Fatalf("expected agent pi, got %q", entries[0].Agent)
	}
	if entries[0].Content != " use `gpt-5.4`," {
		t.Fatalf("unexpected merged content: %q", entries[0].Content)
	}
}

func TestLLMTextFlushesOnNonLLMEntry(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	log.LLMText("pi", "hello")
	log.LLMText("pi", " world")
	log.ToolCall("pi", "search", map[string]string{"query": "docs"})

	if err := log.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	entries := readEntries(t, log.Path())
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].Type != "llm_text" || entries[0].Content != "hello world" {
		t.Fatalf("unexpected first entry: %+v", entries[0])
	}
	if entries[1].Type != "tool_call" || entries[1].Tool != "search" {
		t.Fatalf("unexpected second entry: %+v", entries[1])
	}
}

func TestLLMTextFlushesWhenAgentChanges(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	log.LLMText("pi", "hello")
	log.LLMText("subagent", "world")

	if err := log.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	entries := readEntries(t, log.Path())
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].Type != "llm_text" || entries[0].Agent != "pi" || entries[0].Content != "hello" {
		t.Fatalf("unexpected first entry: %+v", entries[0])
	}
	if entries[1].Type != "llm_text" || entries[1].Agent != "subagent" || entries[1].Content != "world" {
		t.Fatalf("unexpected second entry: %+v", entries[1])
	}
}

func TestLLMTextFlushesAfterWindow(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	log.streamFlushWindow = 5 * time.Millisecond

	log.LLMText("pi", "hel")
	time.Sleep(10 * time.Millisecond)
	log.LLMText("pi", "lo")

	if err := log.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	entries := readEntries(t, log.Path())
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Type != "llm_text" || entries[0].Content != "hel" {
		t.Fatalf("unexpected first entry: %+v", entries[0])
	}
	if entries[1].Type != "llm_text" || entries[1].Content != "lo" {
		t.Fatalf("unexpected second entry: %+v", entries[1])
	}
}

func TestThinkingCoalescesContiguousChunks(t *testing.T) {
	log := newTestLogger(t)

	log.Thinking("pi", "Let me ")
	log.Thinking("pi", "check the ")
	log.Thinking("pi", "git status.")

	if err := log.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	entries := readEntries(t, log.Path())
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Type != "thinking" {
		t.Fatalf("expected thinking entry, got %q", entries[0].Type)
	}
	if entries[0].Agent != "pi" {
		t.Fatalf("expected agent pi, got %q", entries[0].Agent)
	}
	if entries[0].Content != "Let me check the git status." {
		t.Fatalf("unexpected merged content: %q", entries[0].Content)
	}
}

// Reasoning and reply text stream from the same agent back to back, so the two
// runs must not be concatenated into whichever entry type opened first.
func TestThinkingAndLLMTextStayDistinctEntries(t *testing.T) {
	log := newTestLogger(t)

	log.Thinking("pi", "the user wants a plan")
	log.LLMText("pi", "Here is the plan.")
	log.Thinking("pi", "now verify it")

	if err := log.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	entries := readEntries(t, log.Path())
	want := []Entry{
		{Type: "thinking", Agent: "pi", Content: "the user wants a plan"},
		{Type: "llm_text", Agent: "pi", Content: "Here is the plan."},
		{Type: "thinking", Agent: "pi", Content: "now verify it"},
	}
	if len(entries) != len(want) {
		t.Fatalf("expected %d entries, got %d: %+v", len(want), len(entries), entries)
	}
	for i, w := range want {
		got := entries[i]
		if got.Type != w.Type || got.Agent != w.Agent || got.Content != w.Content {
			t.Errorf("entry %d = {%q %q %q}, want {%q %q %q}",
				i, got.Type, got.Agent, got.Content, w.Type, w.Agent, w.Content)
		}
	}
}

// A turn that reasons at length and then emits nothing else is exactly the
// runaway-generation case: without this the log shows an unexplained gap.
func TestThinkingSurvivesTurnWithNoReply(t *testing.T) {
	log := newTestLogger(t)

	log.ToolResult("pi", "read", "file contents")
	for range 500 {
		log.Thinking("pi", "Let me look at the design. ")
	}

	if err := log.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	entries := readEntries(t, log.Path())
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[1].Type != "thinking" {
		t.Fatalf("expected thinking entry, got %q", entries[1].Type)
	}
	if want := 500 * len("Let me look at the design. "); len(entries[1].Content) != want {
		t.Errorf("reasoning content = %d bytes, want %d", len(entries[1].Content), want)
	}
}

func TestToolCall(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { log.Close() }() //nolint:errcheck

	log.ToolCall("agent1", "bash", map[string]string{"command": "ls"})
}

func TestToolResult(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { log.Close() }() //nolint:errcheck

	log.ToolResult("agent1", "bash", "output text")
}

func TestSessionStart(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { log.Close() }() //nolint:errcheck

	log.SessionStart("session-123", "claude-3", "print")
}

// newTestLogger returns a logger writing under a temp HOME. The caller closes
// it, since the flush of any pending streamed entry happens on Close and the
// assertions read the file afterwards.
func newTestLogger(t *testing.T) *Logger {
	t.Helper()

	testenv.SetHome(t, t.TempDir())

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return log
}

func readEntries(t *testing.T, path string) []Entry {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log file: %v", err)
	}
	defer func() { f.Close() }() //nolint:errcheck

	var entries []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e Entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatalf("unmarshal log entry: %v", err)
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan log file: %v", err)
	}

	return entries
}
