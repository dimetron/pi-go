package sop

import (
	"fmt"
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
	// Create a fake home directory with a global SOP
	dir := t.TempDir()
	globalSOP := "# Global PDD SOP\nThis is a global override."
	globalPath := filepath.Join(dir, ".pi-go", "sops")
	if err := os.MkdirAll(globalPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalPath, "pdd.md"), []byte(globalSOP), 0o644); err != nil {
		t.Fatal(err)
	}

	content, err := LoadPDDWithHome(t.TempDir(), dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != globalSOP {
		t.Errorf("expected global override SOP, got: %s", content)
	}
}

func TestLoadPDD_UserHomeDirError(t *testing.T) {
	// Temporarily swap userHomeDir to return an error
	orig := userHomeDir
	userHomeDir = func() (string, error) { return "", fmt.Errorf("no home") }
	defer func() { userHomeDir = orig }()

	content, err := LoadPDD(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != DefaultPDDSOP {
		t.Error("expected embedded default when userHomeDir fails")
	}
}

func TestLoadPDDWithHome_EmptyHomeDir(t *testing.T) {
	content, err := LoadPDDWithHome(t.TempDir(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != DefaultPDDSOP {
		t.Error("expected embedded default when homeDir is empty")
	}
}

func TestLoadPDD_ProjectOverGlobal(t *testing.T) {
	// If project override exists, it should take precedence even when global exists
	dir := t.TempDir()
	sopDir := filepath.Join(dir, ".pi-go", "sops")
	if err := os.MkdirAll(sopDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectSOP := "# Project SOP"
	if err := os.WriteFile(filepath.Join(sopDir, "pdd.md"), []byte(projectSOP), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a global SOP too
	homeDir := t.TempDir()
	globalPath := filepath.Join(homeDir, ".pi-go", "sops")
	if err := os.MkdirAll(globalPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalPath, "pdd.md"), []byte("# Global SOP"), 0o644); err != nil {
		t.Fatal(err)
	}

	content, err := LoadPDDWithHome(dir, homeDir)
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
