package logger

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	// Create a temp directory for testing
	tmpDir := t.TempDir()

	// Set HOME env var to use temp dir
	origHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpDir); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	defer func() { os.Setenv("HOME", origHome) }() //nolint:errcheck

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
	// Set HOME to a non-existent path
	origHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", "/nonexistent/path/that/does/not/exist"); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	defer func() { os.Setenv("HOME", origHome) }() //nolint:errcheck

	_, err := New()
	if err == nil {
		t.Error("New() should return error when home dir doesn't exist")
	}
}

func TestPath(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpDir); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	defer func() { os.Setenv("HOME", origHome) }() //nolint:errcheck

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
	origHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpDir); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	defer func() { os.Setenv("HOME", origHome) }() //nolint:errcheck

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
	origHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpDir); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	defer func() { os.Setenv("HOME", origHome) }() //nolint:errcheck

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
	origHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpDir); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	defer func() { os.Setenv("HOME", origHome) }() //nolint:errcheck

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { log.Close() }() //nolint:errcheck

	log.Info("test info message")
}

func TestError(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpDir); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	defer func() { os.Setenv("HOME", origHome) }() //nolint:errcheck

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { log.Close() }() //nolint:errcheck

	log.Error("test error message")
}

func TestUserMessage(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpDir); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	defer func() { os.Setenv("HOME", origHome) }() //nolint:errcheck

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { log.Close() }() //nolint:errcheck

	log.UserMessage("test user message")
}

func TestLLMText(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpDir); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	defer func() { os.Setenv("HOME", origHome) }() //nolint:errcheck

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { log.Close() }() //nolint:errcheck

	log.LLMText("agent1", "some llm text")
}

func TestLLMTextCoalescesContiguousChunks(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpDir); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	defer func() { os.Setenv("HOME", origHome) }() //nolint:errcheck

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
	origHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpDir); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	defer func() { os.Setenv("HOME", origHome) }() //nolint:errcheck

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
	origHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpDir); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	defer func() { os.Setenv("HOME", origHome) }() //nolint:errcheck

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
	origHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpDir); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	defer func() { os.Setenv("HOME", origHome) }() //nolint:errcheck

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	log.llmFlushWindow = 5 * time.Millisecond

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

func TestToolCall(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpDir); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	defer func() { os.Setenv("HOME", origHome) }() //nolint:errcheck

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { log.Close() }() //nolint:errcheck

	log.ToolCall("agent1", "bash", map[string]string{"command": "ls"})
}

func TestToolResult(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpDir); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	defer func() { os.Setenv("HOME", origHome) }() //nolint:errcheck

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { log.Close() }() //nolint:errcheck

	log.ToolResult("agent1", "bash", "output text")
}

func TestSessionStart(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpDir); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	defer func() { os.Setenv("HOME", origHome) }() //nolint:errcheck

	log, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { log.Close() }() //nolint:errcheck

	log.SessionStart("session-123", "claude-3", "print")
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
