package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/testenv"
)

// -----------------------------------------------------------------------
// lastLoggedError — file-based scanning over session logs
// -----------------------------------------------------------------------

func TestLastLoggedError_NoLogDir(t *testing.T) {
	// Fresh HOME with no ~/.pi-go/log directory => returns "", "", nil.
	testenv.SetHome(t, t.TempDir())
	path, msg, err := lastLoggedError()
	if err != nil {
		t.Fatalf("lastLoggedError: %v", err)
	}
	if path != "" || msg != "" {
		t.Errorf("expected empty results, got path=%q msg=%q", path, msg)
	}
}

func TestLastLoggedError_HomeError(t *testing.T) {
	// Unset HOME so os.UserHomeDir fails. On Darwin/Linux this typically
	// falls back to /etc/passwd, which may still succeed. Skip the error
	// assertion and only verify no panic.
	testenv.UnsetHome(t)
	_, _, _ = lastLoggedError() // Should not panic.
}

func TestLastLoggedError_EmptyLogDir(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)
	// Create empty log dir.
	logDir := filepath.Join(tmpDir, ".pi-go", "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}

	path, msg, err := lastLoggedError()
	if err != nil {
		t.Fatalf("lastLoggedError: %v", err)
	}
	if path != "" || msg != "" {
		t.Errorf("empty log dir: got path=%q msg=%q", path, msg)
	}
}

func TestLastLoggedError_WithErrorEntry(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

	// Create a dated log directory with a session file.
	dateDir := filepath.Join(tmpDir, ".pi-go", "log", "2024-01-15")
	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionLog := filepath.Join(dateDir, "session-10-30-00.log")
	// Write a log file with an error entry.
	logContent := `{"time":"2024-01-15T10:30:00Z","type":"info","content":"startup"}
{"time":"2024-01-15T10:30:01Z","type":"error","content":"connection refused"}
`
	if err := os.WriteFile(sessionLog, []byte(logContent), 0o644); err != nil {
		t.Fatal(err)
	}

	path, msg, err := lastLoggedError()
	if err != nil {
		t.Fatalf("lastLoggedError: %v", err)
	}
	if msg != "connection refused" {
		t.Errorf("msg = %q, want %q", msg, "connection refused")
	}
	if !strings.Contains(path, "session-10-30-00.log") {
		t.Errorf("path = %q, want contains session log filename", path)
	}
}

func TestLastLoggedError_NoErrorEntries(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)
	dateDir := filepath.Join(tmpDir, ".pi-go", "log", "2024-01-15")
	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Only info entries; no errors.
	logContent := `{"time":"2024-01-15T10:30:00Z","type":"info","content":"hello"}
{"time":"2024-01-15T10:30:01Z","type":"user","content":"hi"}
`
	sessionLog := filepath.Join(dateDir, "session-10-30-00.log")
	if err := os.WriteFile(sessionLog, []byte(logContent), 0o644); err != nil {
		t.Fatal(err)
	}

	path, msg, err := lastLoggedError()
	if err != nil {
		t.Fatalf("lastLoggedError: %v", err)
	}
	if path != "" || msg != "" {
		t.Errorf("expected no match, got path=%q msg=%q", path, msg)
	}
}

func TestLastLoggedError_SkipsNonSessionFiles(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)
	dateDir := filepath.Join(tmpDir, ".pi-go", "log", "2024-02-20")
	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create a file that does NOT match the session-*.log pattern.
	if err := os.WriteFile(filepath.Join(dateDir, "other.txt"), []byte(`{"type":"error","content":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create a subdirectory that should be skipped too.
	if err := os.MkdirAll(filepath.Join(dateDir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Also put a valid session log with an error.
	valid := filepath.Join(dateDir, "session-01-02-03.log")
	if err := os.WriteFile(valid, []byte(`{"type":"error","content":"boom"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, msg, err := lastLoggedError()
	if err != nil {
		t.Fatalf("lastLoggedError: %v", err)
	}
	if msg != "boom" {
		t.Errorf("msg = %q, want %q", msg, "boom")
	}
	if !strings.HasSuffix(path, "session-01-02-03.log") {
		t.Errorf("path = %q, want suffix session-01-02-03.log", path)
	}
}

func TestLastLoggedError_MultipleDateDirsUsesLast(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)
	logRoot := filepath.Join(tmpDir, ".pi-go", "log")

	// Older directory with an error.
	oldDir := filepath.Join(logRoot, "2023-01-01")
	_ = os.MkdirAll(oldDir, 0o755)
	_ = os.WriteFile(filepath.Join(oldDir, "session-00-00-00.log"),
		[]byte(`{"type":"error","content":"old error"}`+"\n"), 0o644)

	// Newer directory with a different error.
	newDir := filepath.Join(logRoot, "2024-12-31")
	_ = os.MkdirAll(newDir, 0o755)
	_ = os.WriteFile(filepath.Join(newDir, "session-23-59-59.log"),
		[]byte(`{"type":"error","content":"new error"}`+"\n"), 0o644)

	_, msg, err := lastLoggedError()
	if err != nil {
		t.Fatalf("lastLoggedError: %v", err)
	}
	// Iteration is reversed, so last (newest) wins.
	if msg != "new error" {
		t.Errorf("msg = %q, want %q", msg, "new error")
	}
}

// -----------------------------------------------------------------------
// checkForRapidRestartAndWarn — exercise the lastLoggedError branch.
// -----------------------------------------------------------------------

// lastSessionJSON renders a last-session.json record for workDir stamped
// "now". It goes through the encoder rather than string concatenation because
// a Windows temp dir holds backslashes, which an unescaped `"work_dir":"C:\Users\..."`
// turns into invalid JSON -- readLastSession then fails and the warning under
// test is never printed.
func lastSessionJSON(t *testing.T, workDir string) string {
	t.Helper()
	blob, err := json.Marshal(map[string]string{
		"timestamp":  nowTimeStr(),
		"session_id": "s1",
		"work_dir":   workDir,
		"model":      "gpt",
	})
	if err != nil {
		t.Fatalf("marshal last-session: %v", err)
	}
	return string(blob)
}

func TestCheckForRapidRestartAndWarn_WithLoggedError(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

	// Set up a recent last-session.json for the same workdir.
	f := filepath.Join(tmpDir, "last-session.json")
	if err := os.WriteFile(f, []byte(lastSessionJSON(t, tmpDir)), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := lastSessionFile
	lastSessionFile = f
	defer func() { lastSessionFile = orig }()

	// Populate a log with a recent error.
	dateDir := filepath.Join(tmpDir, ".pi-go", "log", "2024-06-01")
	_ = os.MkdirAll(dateDir, 0o755)
	_ = os.WriteFile(filepath.Join(dateDir, "session-01-02-03.log"),
		[]byte(`{"type":"error","content":"previous crash"}`+"\n"), 0o644)

	stderr := captureStderr(t, func() {
		checkForRapidRestartAndWarn(tmpDir)
	})
	if !strings.Contains(stderr, "rapid restart detected") {
		t.Errorf("stderr = %q, want rapid restart warning", stderr)
	}
	if !strings.Contains(stderr, "previous crash") {
		t.Errorf("stderr should include the logged error, got %q", stderr)
	}
}

func TestCheckForRapidRestartAndWarn_NoLoggedError(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

	f := filepath.Join(tmpDir, "last-session.json")
	if err := os.WriteFile(f, []byte(lastSessionJSON(t, tmpDir)), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := lastSessionFile
	lastSessionFile = f
	defer func() { lastSessionFile = orig }()

	// No log directory => lastLoggedError returns "", "", nil.
	stderr := captureStderr(t, func() {
		checkForRapidRestartAndWarn(tmpDir)
	})
	if !strings.Contains(stderr, "rapid restart detected") {
		t.Errorf("stderr = %q, want warning", stderr)
	}
	if !strings.Contains(stderr, "no recent logged errors") {
		t.Errorf("stderr = %q, want 'no recent logged errors'", stderr)
	}
}

// -----------------------------------------------------------------------
// buildMCPServerConfigs — pure converter
// -----------------------------------------------------------------------

func TestBuildMCPServerConfigs_NilMCP(t *testing.T) {
	cfg := config.Config{MCP: nil}
	out := buildMCPServerConfigs(cfg)
	if out != nil {
		t.Errorf("expected nil, got %v", out)
	}
}

func TestBuildMCPServerConfigs_EmptyServers(t *testing.T) {
	cfg := config.Config{MCP: &config.MCPConfig{Servers: []config.MCPServer{}}}
	out := buildMCPServerConfigs(cfg)
	if len(out) != 0 {
		t.Errorf("expected empty slice, got %d entries", len(out))
	}
}

func TestBuildMCPServerConfigs_MultipleServers(t *testing.T) {
	cfg := config.Config{
		MCP: &config.MCPConfig{
			Servers: []config.MCPServer{
				{Name: "alpha", Command: "bin1", Args: []string{"--flag"}},
				{Name: "beta", Command: "bin2"},
			},
		},
	}
	out := buildMCPServerConfigs(cfg)
	if len(out) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(out))
	}
	if out[0].Name != "alpha" || out[0].Command != "bin1" || len(out[0].Args) != 1 {
		t.Errorf("out[0] = %+v, want alpha/bin1/[--flag]", out[0])
	}
	if out[1].Name != "beta" || out[1].Command != "bin2" {
		t.Errorf("out[1] = %+v, want beta/bin2", out[1])
	}
}

// -----------------------------------------------------------------------
// resolveActiveRole — cover the explicit role flags.
// -----------------------------------------------------------------------

func TestResolveActiveRole_DefaultWhenNoFlag(t *testing.T) {
	// Save and restore flags.
	origSmol, origSlow, origPlan := flagSmol, flagSlow, flagPlan
	defer func() {
		flagSmol, flagSlow, flagPlan = origSmol, origSlow, origPlan
	}()
	flagSmol, flagSlow, flagPlan = false, false, false

	if got := resolveActiveRole(); got != "default" {
		t.Errorf("resolveActiveRole() = %q, want %q", got, "default")
	}
}

func TestResolveActiveRole_Smol(t *testing.T) {
	origSmol, origSlow, origPlan := flagSmol, flagSlow, flagPlan
	defer func() { flagSmol, flagSlow, flagPlan = origSmol, origSlow, origPlan }()
	flagSmol, flagSlow, flagPlan = true, false, false

	if got := resolveActiveRole(); got != "smol" {
		t.Errorf("= %q, want smol", got)
	}
}

func TestResolveActiveRole_Slow(t *testing.T) {
	origSmol, origSlow, origPlan := flagSmol, flagSlow, flagPlan
	defer func() { flagSmol, flagSlow, flagPlan = origSmol, origSlow, origPlan }()
	flagSmol, flagSlow, flagPlan = false, true, false

	if got := resolveActiveRole(); got != "slow" {
		t.Errorf("= %q, want slow", got)
	}
}

func TestResolveActiveRole_Plan(t *testing.T) {
	origSmol, origSlow, origPlan := flagSmol, flagSlow, flagPlan
	defer func() { flagSmol, flagSlow, flagPlan = origSmol, origSlow, origPlan }()
	flagSmol, flagSlow, flagPlan = false, false, true

	if got := resolveActiveRole(); got != "plan" {
		t.Errorf("= %q, want plan", got)
	}
}
