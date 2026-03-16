package logger

import (
	"os"
	"path/filepath"
	"testing"
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
