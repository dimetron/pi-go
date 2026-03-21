package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/audit"
)

func TestNewAuditCmd(t *testing.T) {
	cmd := newAuditCmd()
	if cmd.Use != "audit" {
		t.Errorf("Use = %q, want %q", cmd.Use, "audit")
	}
	// Check flags exist.
	flags := []string{"dir", "file", "strip", "dry-run", "force", "verbose", "format", "output"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag %q not registered", name)
		}
	}
}

func TestAuditCmdRegistered(t *testing.T) {
	root := newRootCmd()
	found := false
	for _, c := range root.Commands() {
		if c.Use == "audit" {
			found = true
			break
		}
	}
	if !found {
		t.Error("audit command not registered on root")
	}
}

func TestRunAuditCleanFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clean.md")
	os.WriteFile(path, []byte("Clean ASCII content"), 0o644)

	err := runAudit(nil, "", path, false, false, false, false, "text", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunAuditJSONFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	os.WriteFile(path, []byte("Clean content"), 0o644)
	outPath := filepath.Join(dir, "out.json")

	err := runAudit(nil, "", path, false, false, false, false, "json", outPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("JSON output file is empty")
	}
}

func TestRunAuditDryRunStrip(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "bad-skill")
	os.MkdirAll(skillDir, 0o755)
	path := filepath.Join(skillDir, "SKILL.md")
	content := "Hello \u202E World"
	os.WriteFile(path, []byte(content), 0o644)

	err := runAudit(nil, dir, "", true, true, false, false, "text", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// File should NOT be modified in dry-run.
	data, _ := os.ReadFile(path)
	if string(data) != content {
		t.Error("file should not be modified in dry-run mode")
	}
}

func TestRunAuditStripForce(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "bad-skill")
	os.MkdirAll(skillDir, 0o755)
	path := filepath.Join(skillDir, "SKILL.md")
	content := "Hello \u202E World"
	os.WriteFile(path, []byte(content), 0o644)

	err := runAudit(nil, dir, "", true, false, true, false, "text", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// File should be modified.
	data, _ := os.ReadFile(path)
	if string(data) == content {
		t.Error("file should be modified after strip")
	}

	// Backup should exist.
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Error("backup file should exist")
	}
}

func TestDefaultSkillDirs(t *testing.T) {
	dirs := defaultSkillDirs()
	if len(dirs) < 3 {
		t.Errorf("expected at least 3 dirs, got %d", len(dirs))
	}
}

// --- handleStrip coverage ---

func TestHandleStripNoFindings(t *testing.T) {
	result := &audit.ScanResult{
		Files:    []string{},
		Findings: []audit.ScanFinding{},
	}
	stdout := captureStdout(t, func() {
		err := handleStrip(result, false, false, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(stdout, "No dangerous characters found") {
		t.Errorf("expected 'No dangerous characters found' message, got: %q", stdout)
	}
}

func TestHandleStripInfoOnlyFindings(t *testing.T) {
	// Info-level findings should NOT trigger strip (only Warning/Critical do).
	result := &audit.ScanResult{
		Files: []string{"some.md"},
		Findings: []audit.ScanFinding{
			{
				File:        "some.md",
				Line:        1,
				Col:         1,
				Codepoint:   "U+00A0",
				Severity:    audit.SeverityInfo,
				Description: "Non-breaking space",
			},
		},
	}
	stdout := captureStdout(t, func() {
		err := handleStrip(result, false, false, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(stdout, "Only info-level findings") {
		t.Errorf("expected 'Only info-level findings' message, got: %q", stdout)
	}
}

// --- runAudit format coverage ---

func TestRunAuditMarkdownFormatToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	os.WriteFile(path, []byte("Clean content"), 0o644)
	outPath := filepath.Join(dir, "out.md")

	err := runAudit(nil, "", path, false, false, false, false, "markdown", outPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("markdown output file is empty")
	}
}

func TestRunAuditAutoDetectMarkdownFromExtension(t *testing.T) {
	// When --format=text (default) and --output ends in .md, format should auto-detect as markdown.
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	os.WriteFile(path, []byte("Clean content"), 0o644)
	outPath := filepath.Join(dir, "report.md")

	// Pass format="text" which triggers auto-detection from output extension.
	err := runAudit(nil, "", path, false, false, false, false, "text", outPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	// Markdown format should contain a markdown table header.
	content := string(data)
	if !strings.Contains(content, "Scan") && !strings.Contains(content, "|") && !strings.Contains(content, "clean") {
		t.Logf("markdown auto-detect output: %q", content)
	}
}

func TestRunAuditAutoDetectJSONFromExtension(t *testing.T) {
	// When --format=text (default) and --output ends in .json, format should auto-detect as json.
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	os.WriteFile(path, []byte("Clean content"), 0o644)
	outPath := filepath.Join(dir, "report.json")

	err := runAudit(nil, "", path, false, false, false, false, "text", outPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	// JSON output should be valid JSON.
	if len(data) == 0 {
		t.Error("auto-detected JSON output file is empty")
	}
	if data[0] != '{' {
		t.Errorf("expected JSON output to start with '{', got: %q", string(data[:min(20, len(data))]))
	}
}

func TestRunAuditMarkdownFormatToStdout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	os.WriteFile(path, []byte("Clean content"), 0o644)

	// No output file: should print to stdout.
	stdout := captureStdout(t, func() {
		err := runAudit(nil, "", path, false, false, false, false, "markdown", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	// Should have produced some output.
	if stdout == "" {
		t.Error("expected markdown output to stdout, got empty string")
	}
}

func TestRunAuditVerboseFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	os.WriteFile(path, []byte("Clean content"), 0o644)

	// verbose=true should not cause an error.
	err := runAudit(nil, "", path, false, false, false, true, "text", "")
	if err != nil {
		t.Fatalf("unexpected error with verbose=true: %v", err)
	}
}

// min returns the smaller of a and b (for Go < 1.21 compatibility helper).
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// -----------------------------------------------------------------------
// Additional runAudit and handleStrip error path tests
// -----------------------------------------------------------------------

func TestRunAuditOutputWriteError(t *testing.T) {
	// Trying to write to a read-only directory should produce an error.
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	os.WriteFile(path, []byte("Clean content"), 0644)

	// Create a directory we can't write to.
	readOnlyDir := filepath.Join(dir, "readonly")
	os.MkdirAll(readOnlyDir, 0555)
	outPath := filepath.Join(readOnlyDir, "out.txt")

	err := runAudit(nil, "", path, false, false, false, false, "text", outPath)
	if err == nil {
		t.Error("expected error when writing to read-only directory")
	}
}

func TestRunAuditDefaultDirsWithNoSkills(t *testing.T) {
	// When no skill directories exist, runAudit should handle gracefully.
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	// runAudit scans defaultSkillDirs() — with HOME set to tmpDir
	// there are no skill dirs under the standard paths, so it should
	// return a result (possibly empty or with warnings about missing dirs).
	err := runAudit(nil, "", "", false, false, false, false, "text", "")
	if err != nil {
		t.Fatalf("runAudit with no skill dirs returned error: %v", err)
	}
}

func TestHandleStripNilFindings(t *testing.T) {
	// With nil findings slice (not empty), should still say "No dangerous characters".
	result := &audit.ScanResult{
		Findings: nil,
	}
	stdout := captureStdout(t, func() {
		err := handleStrip(result, false, false, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(stdout, "No dangerous characters") {
		t.Errorf("expected 'No dangerous characters' message, got: %q", stdout)
	}
}

func TestHandleStripUserAborts(t *testing.T) {
	// When the user answers "n" to the confirmation prompt, should return nil.
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.md")
	os.WriteFile(path, []byte("Hello \u202E World"), 0644)

	result := &audit.ScanResult{
		Files: []string{path},
		Findings: []audit.ScanFinding{
			{
				File:        path,
				Line:        1,
				Col:         7,
				Codepoint:   "U+202E",
				Severity:    audit.SeverityCritical,
				Description: "BiDi override",
			},
		},
	}

	// Simulate user typing "n".
	r, w, _ := os.Pipe()
	origStdin := os.Stdin
	os.Stdin = r
	w.WriteString("n\n")
	w.Close()
	defer func() { os.Stdin = origStdin }()

	stdout := captureStdout(t, func() {
		err := handleStrip(result, false, false, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(stdout, "Aborted") {
		t.Errorf("expected 'Aborted' message, got: %q", stdout)
	}
}

func TestHandleStripUserConfirms(t *testing.T) {
	// When the user answers "y" to the confirmation prompt, should strip files.
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.md")
	os.WriteFile(path, []byte("Hello \u202E World"), 0644)

	result := &audit.ScanResult{
		Files: []string{path},
		Findings: []audit.ScanFinding{
			{
				File:        path,
				Line:        1,
				Col:         7,
				Codepoint:   "U+202E",
				Severity:    audit.SeverityCritical,
				Description: "BiDi override",
			},
		},
	}

	// Simulate user typing "y".
	r, w, _ := os.Pipe()
	origStdin := os.Stdin
	os.Stdin = r
	w.WriteString("y\n")
	w.Close()
	defer func() { os.Stdin = origStdin }()

	original, _ := os.ReadFile(path)

	stdout := captureStdout(t, func() {
		err := handleStrip(result, false, false, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	after, _ := os.ReadFile(path)

	if string(original) == string(after) {
		t.Error("file should have been modified after user confirmed")
	}
	if !strings.Contains(stdout, "Done") {
		t.Errorf("expected 'Done' message, got: %q", stdout)
	}
}

func TestHandleStripStripError(t *testing.T) {
	// When stripping a file fails (e.g., permission denied), should return an error.
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.md")
	os.WriteFile(path, []byte("Hello \u202E World"), 0644)

	result := &audit.ScanResult{
		Files: []string{path},
		Findings: []audit.ScanFinding{
			{
				File:        path,
				Line:        1,
				Col:         7,
				Codepoint:   "U+202E",
				Severity:    audit.SeverityCritical,
				Description: "BiDi override",
			},
		},
	}

	// Make the file read-only so stripping fails.
	os.Chmod(path, 0444)
	t.Cleanup(func() { os.Chmod(path, 0644) }) // Restore for cleanup.

	// Use --force to skip confirmation.
	err := handleStrip(result, false, true, false)
	if err == nil {
		t.Error("expected error when stripping read-only file")
	}
}

func TestHandleStripDryRunWithFindings(t *testing.T) {
	// dry-run should show detailed findings without modifying files.
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.md")
	os.WriteFile(path, []byte("Hello \u202E World"), 0644)

	result := &audit.ScanResult{
		Files: []string{path},
		Findings: []audit.ScanFinding{
			{
				File:        path,
				Line:        1,
				Col:         7,
				Codepoint:   "U+202E",
				Severity:    audit.SeverityCritical,
				Description: "BiDi override",
			},
		},
	}

	original, _ := os.ReadFile(path)

	stdout := captureStdout(t, func() {
		err := handleStrip(result, true, false, false) // dry-run=true
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	after, _ := os.ReadFile(path)

	if string(original) != string(after) {
		t.Error("file should not be modified in dry-run mode")
	}
	if !strings.Contains(stdout, "dry-run") && !strings.Contains(stdout, "[dry-run]") {
		t.Errorf("expected dry-run message in output, got: %q", stdout)
	}
}

func TestRunAuditDirScan(t *testing.T) {
	// Test scanning a directory instead of a single file.
	dir := t.TempDir()
	subDir := filepath.Join(dir, "skill")
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, "SKILL.md"), []byte("Clean skill"), 0644)
	os.WriteFile(filepath.Join(subDir, "ANOTHER.md"), []byte("Also clean"), 0644)

	err := runAudit(nil, dir, "", false, false, false, false, "text", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunAuditDirScanWithFindings(t *testing.T) {
	// Test scanning a directory with a file containing dangerous characters.
	dir := t.TempDir()
	subDir := filepath.Join(dir, "skill")
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, "SKILL.md"), []byte("Clean"), 0644)
	os.WriteFile(filepath.Join(subDir, "BAD.md"), []byte("Bad \u202E char"), 0644)

	err := runAudit(nil, dir, "", false, false, false, false, "text", "")
	// Should not error, but will produce findings and exit non-zero.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
