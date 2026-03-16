package sop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPDD_EmbeddedDefault(t *testing.T) {
	// Use a temp dir with no override files
	dir := t.TempDir()
	content, err := LoadPDD(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != DefaultPDDSOP {
		t.Error("expected embedded default SOP")
	}
	if content == "" {
		t.Error("embedded SOP should not be empty")
	}
}

func TestLoadPDD_ProjectOverride(t *testing.T) {
	dir := t.TempDir()
	sopDir := filepath.Join(dir, ".pi-go", "sops")
	if err := os.MkdirAll(sopDir, 0o755); err != nil {
		t.Fatal(err)
	}
	customSOP := "# Custom Project PDD SOP\nThis is a project-level override."
	if err := os.WriteFile(filepath.Join(sopDir, "pdd.md"), []byte(customSOP), 0o644); err != nil {
		t.Fatal(err)
	}

	content, err := LoadPDD(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != customSOP {
		t.Errorf("expected project override SOP, got: %s", content)
	}
}

func TestLoadPDD_GlobalOverride(t *testing.T) {
	// We can't easily test the global override without mocking os.UserHomeDir.
	// Instead, verify that when project override doesn't exist, the function
	// falls through (to global, then embedded). With no global override set up,
	// it should return the embedded default.
	dir := t.TempDir()
	content, err := LoadPDD(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != DefaultPDDSOP {
		t.Error("expected embedded default when no overrides exist")
	}
}

func TestLoadPDD_ProjectOverGlobal(t *testing.T) {
	// If project override exists, it should take precedence
	dir := t.TempDir()
	sopDir := filepath.Join(dir, ".pi-go", "sops")
	if err := os.MkdirAll(sopDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectSOP := "# Project SOP"
	if err := os.WriteFile(filepath.Join(sopDir, "pdd.md"), []byte(projectSOP), 0o644); err != nil {
		t.Fatal(err)
	}

	content, err := LoadPDD(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != projectSOP {
		t.Errorf("expected project SOP to take precedence, got: %s", content)
	}
}

func TestLoadPDD_UnreadableFile(t *testing.T) {
	// If the override file exists but is unreadable, should fall back
	dir := t.TempDir()
	sopDir := filepath.Join(dir, ".pi-go", "sops")
	if err := os.MkdirAll(sopDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create a directory with the same name as the expected file (unreadable as file)
	if err := os.Mkdir(filepath.Join(sopDir, "pdd.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	content, err := LoadPDD(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should fall back to embedded default since the "file" is actually a directory
	if content != DefaultPDDSOP {
		t.Error("expected fallback to embedded default on unreadable file")
	}
}

func TestDefaultPDDSOP_NotEmpty(t *testing.T) {
	if DefaultPDDSOP == "" {
		t.Error("DefaultPDDSOP constant should not be empty")
	}
	// Verify it contains key PDD phases
	phases := []string{
		"Requirements Clarification",
		"Research",
		"Design",
		"Implementation Plan",
		"PROMPT.md",
		"Gates",
	}
	for _, phase := range phases {
		if !contains(DefaultPDDSOP, phase) {
			t.Errorf("DefaultPDDSOP missing expected phase: %s", phase)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
