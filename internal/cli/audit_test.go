package cli

import (
	"os"
	"path/filepath"
	"testing"
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
