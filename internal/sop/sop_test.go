package sop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dimetron/pi-go/internal/testenv"
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

func TestLoadPDD_GlobalOverrideFromHome(t *testing.T) {
	// Point HOME at a temp dir so os.UserHomeDir resolves there, then place a
	// global override and confirm it is loaded when no project override exists.
	home := t.TempDir()
	testenv.SetHome(t, home)

	globalSOPDir := filepath.Join(home, ".pi-go", "sops")
	if err := os.MkdirAll(globalSOPDir, 0o755); err != nil {
		t.Fatal(err)
	}
	globalSOP := "# Global PDD SOP\nThis is a global override."
	if err := os.WriteFile(filepath.Join(globalSOPDir, "pdd.md"), []byte(globalSOP), 0o644); err != nil {
		t.Fatal(err)
	}

	// workDir has no project override, so resolution should fall to the global file.
	workDir := t.TempDir()
	content, err := LoadPDD(workDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != globalSOP {
		t.Errorf("expected global override SOP, got: %s", content)
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

// The default SOP must teach the Coordinator → Worker → Verifier model, and
// must generate a PROMPT.md that /run's verifier can act on. Without these
// sections a plan produces a single monolithic agent whose context outgrows
// the model before the last slice lands.
func TestDefaultPDDSOP_CarriesCoordinatorWorkflow(t *testing.T) {
	for _, want := range []string{
		"## Execution Model",
		"Coordinator → Worker → Verifier",
		"## Done Criteria",
		"code-reviewer",
		"VERDICT: PASS",
		"parallel-safe",
	} {
		if !contains(DefaultPDDSOP, want) {
			t.Errorf("default PDD SOP missing %q", want)
		}
	}
}

// Delegating to a [worktree] agent from /plan or /run drops the edits into a
// nested worktree that is never merged, so the SOP must rule it out.
func TestDefaultPDDSOP_ForbidsWorktreeAgents(t *testing.T) {
	if !contains(DefaultPDDSOP, "Never delegate to a [worktree] agent") {
		t.Error("default PDD SOP should forbid delegating to [worktree] agents")
	}
	if !contains(DefaultPDDSOP, "explore") {
		t.Error("default PDD SOP should name the research subagent to delegate to")
	}
}

// A plan longer than the read tool's window is served to workers a page at a
// time, so the SOP must budget for it at planning time and say what to do
// instead of letting the plan grow.
func TestDefaultPDDSOP_BudgetsPlanLength(t *testing.T) {
	for _, want := range []string{
		"Keep plan.md under 2000 lines",
		"read tool's window",
		"design.md has the same ceiling",
		"split the *feature* into",
	} {
		if !contains(DefaultPDDSOP, want) {
			t.Errorf("default PDD SOP missing %q", want)
		}
	}
}
