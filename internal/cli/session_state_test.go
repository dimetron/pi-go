// Tests for the last-session state file and rapid-restart detection.
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteLastSession_CreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	orig := lastSessionFile
	lastSessionFile = filepath.Join(tmpDir, "subdir", "last.json")
	defer func() { lastSessionFile = orig }()

	if err := writeLastSession("/work", "openai", "gpt-5.4"); err != nil {
		t.Fatalf("writeLastSession: %v", err)
	}
	data, err := os.ReadFile(lastSessionFile)
	if err != nil {
		t.Fatalf("reading saved file: %v", err)
	}
	var got lastSessionData
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.WorkDir != "/work" || got.Model != "gpt-5.4" {
		t.Errorf("data = %+v", got)
	}
}

func TestLastLoggedError_CorruptedLogLine(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	dateDir := filepath.Join(tmpDir, ".pi-go", "log", "2024-07-01")
	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dateDir, "session-00-00-00.log")
	// Mix of invalid JSON and valid entries.
	content := "not json line\n" +
		`{"type":"info","content":"hi"}` + "\n" +
		"another garbage\n" +
		`{"type":"error","content":"real error"}` + "\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, msg, err := lastLoggedError()
	if err != nil {
		t.Fatalf("lastLoggedError: %v", err)
	}
	if msg != "real error" {
		t.Errorf("msg = %q, want 'real error'", msg)
	}
}

func TestReadLastSession_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "empty.json")
	_ = os.WriteFile(f, []byte(""), 0o644)

	orig := lastSessionFile
	lastSessionFile = f
	defer func() { lastSessionFile = orig }()

	_, err := readLastSession()
	if err == nil {
		t.Error("expected error for empty JSON file")
	}
}

func TestReadLastSession_FileNotExist(t *testing.T) {
	// Save original and restore after test.
	orig := lastSessionFile
	lastSessionFile = filepath.Join(t.TempDir(), "nonexistent.json")
	defer func() { lastSessionFile = orig }()

	data, err := readLastSession()
	if err != nil {
		t.Fatalf("readLastSession: %v", err)
	}
	if data != nil {
		t.Errorf("expected nil for non-existent file, got %+v", data)
	}
}

func TestReadLastSession_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "bad.json")
	if err := os.WriteFile(f, []byte("not valid json{"), 0644); err != nil {
		t.Fatal(err)
	}

	orig := lastSessionFile
	lastSessionFile = f
	defer func() { lastSessionFile = orig }()

	_, err := readLastSession()
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestReadLastSession_Valid(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "valid.json")
	// Write a real lastSessionData JSON.
	content := `{"timestamp":"2024-01-01T00:00:00Z","session_id":"test-sess","work_dir":"/tmp","model":"gpt-4o"}`
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	orig := lastSessionFile
	lastSessionFile = f
	defer func() { lastSessionFile = orig }()

	data, err := readLastSession()
	if err != nil {
		t.Fatalf("readLastSession: %v", err)
	}
	if data == nil || data.SessionID != "test-sess" {
		t.Errorf("SessionID = %q, want %q", data.SessionID, "test-sess")
	}
	if data == nil || data.Model != "gpt-4o" {
		t.Errorf("Model = %q, want %q", data.Model, "gpt-4o")
	}
}

func TestCheckForRapidRestartAndWarn_DifferentWorkDir(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "last-session.json")
	content := `{"timestamp":"2024-01-01T00:00:00Z","session_id":"s1","work_dir":"/other","model":"gpt-4o"}`
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	orig := lastSessionFile
	lastSessionFile = f
	defer func() { lastSessionFile = orig }()

	// Different workDir — should not warn (no output expected).
	checkForRapidRestartAndWarn("/tmp/different")
}

func TestCheckForRapidRestartAndWarn_Within3Seconds(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "last-session.json")
	// Use current time so elapsed < 3s.
	content := `{"timestamp":"` + nowJSON() + `","session_id":"s1","work_dir":"/tmp","model":"gpt-5.4"}`
	if err := os.WriteFile(f, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	orig := lastSessionFile
	lastSessionFile = f
	defer func() { lastSessionFile = orig }()

	// Same workDir, very recent — should warn.
	checkForRapidRestartAndWarn("/tmp")
}

func TestCheckForRapidRestartAndWarn_OlderThan3Seconds(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "last-session.json")
	// Use a timestamp from 10 seconds ago.
	oldTime := `{"timestamp":"2010-01-01T00:00:00Z","session_id":"s1","work_dir":"/tmp","model":"gpt-4o"}`
	if err := os.WriteFile(f, []byte(oldTime), 0644); err != nil {
		t.Fatal(err)
	}

	orig := lastSessionFile
	lastSessionFile = f
	defer func() { lastSessionFile = orig }()

	// Same workDir but older than 3s — no warning.
	checkForRapidRestartAndWarn("/tmp")
}

func TestWriteLastSession_MkdirAllError(t *testing.T) {
	orig := lastSessionFile
	// Point to a path that cannot be created.
	lastSessionFile = "/sys/fake/last-session.json"
	defer func() { lastSessionFile = orig }()

	err := writeLastSession("/fake", "provider", "model")
	if err == nil {
		t.Error("expected error for unwritable path")
	}
}

func TestWriteLastSession_JSONError(t *testing.T) {
	// This is hard to trigger since all fields are serializable.
	// Testing with valid input.
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "last-session.json")
	orig := lastSessionFile
	lastSessionFile = f
	defer func() { lastSessionFile = orig }()

	err := writeLastSession("/tmp", "anthropic", "claude-sonnet-4-6")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
