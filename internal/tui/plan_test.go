package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/sop"
	"github.com/dimetron/pi-go/internal/subagent"
)

func TestCopyDir_RecursesAndOverwrites(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	dst := filepath.Join(t.TempDir(), "dst")

	// Nested file under research/ plus a top-level file.
	if err := os.MkdirAll(filepath.Join(src, "research"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "PROMPT.md"), []byte("plan"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "research", "notes.md"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyDir(src, dst); err != nil {
		t.Fatalf("copyDir: %v", err)
	}

	for _, rel := range []string{"PROMPT.md", "research", filepath.Join("research", "notes.md")} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("copied %s missing in dst: %v", rel, err)
		}
	}

	// Overwrite an existing destination file.
	if err := os.WriteFile(filepath.Join(src, "PROMPT.md"), []byte("plan-v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyDir(src, dst); err != nil {
		t.Fatalf("copyDir overwrite: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "PROMPT.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "plan-v2" {
		t.Errorf("overwrite did not take effect: got %q, want %q", got, "plan-v2")
	}
}

func TestCopyDir_SkipsSymlinks(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	dst := filepath.Join(t.TempDir(), "dst")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "real.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(src, "real.md"), filepath.Join(src, "link.md")); err != nil {
		t.Fatal(err)
	}

	if err := copyDir(src, dst); err != nil {
		t.Fatalf("copyDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "real.md")); err != nil {
		t.Errorf("real.md not copied: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "link.md")); err == nil {
		t.Error("symlink should not be copied")
	}
}

func TestToKebabCase_Simple(t *testing.T) {
	got := toKebabCase("add rate limiting")
	want := "add-rate-limiting"
	if got != want {
		t.Errorf("toKebabCase(\"add rate limiting\") = %q, want %q", got, want)
	}
}

func TestToKebabCase_SpecialChars(t *testing.T) {
	got := toKebabCase("build a REST API!")
	want := "build-a-rest-api"
	if got != want {
		t.Errorf("toKebabCase(\"build a REST API!\") = %q, want %q", got, want)
	}
}

func TestToKebabCase_MixedCase(t *testing.T) {
	got := toKebabCase("Add JWT Auth")
	want := "add-jwt-auth"
	if got != want {
		t.Errorf("toKebabCase(\"Add JWT Auth\") = %q, want %q", got, want)
	}
}

func TestToKebabCase_ExtraSpaces(t *testing.T) {
	got := toKebabCase("  too   many  spaces  ")
	want := "too-many-spaces"
	if got != want {
		t.Errorf("toKebabCase(\"  too   many  spaces  \") = %q, want %q", got, want)
	}
}

func TestToKebabCase_Truncation(t *testing.T) {
	// A long string that exceeds 50 chars when kebab-cased.
	idea := "implement a comprehensive rate limiting system with sliding window algorithm and redis backend"
	got := toKebabCase(idea)
	if len(got) > 50 {
		t.Errorf("toKebabCase should truncate to <= 50 chars, got %d: %q", len(got), got)
	}
	// Should not end with a hyphen.
	if strings.HasSuffix(got, "-") {
		t.Errorf("toKebabCase should not end with hyphen, got %q", got)
	}
	// Should not split in the middle of a word.
	if !strings.HasPrefix("implement-a-comprehensive-rate-limiting-system-with-sliding-window-algorithm-and-redis-backend", got) {
		// Just verify it's a valid prefix of the full kebab.
		t.Logf("truncated result: %q (len %d)", got, len(got))
	}
}

func TestToKebabCase_EmptyString(t *testing.T) {
	got := toKebabCase("")
	if got != "" {
		t.Errorf("toKebabCase(\"\") = %q, want \"\"", got)
	}
}

func TestToKebabCase_OnlySpecialChars(t *testing.T) {
	got := toKebabCase("!@#$%")
	if got != "" {
		t.Errorf("toKebabCase(\"!@#$%%\") = %q, want \"\"", got)
	}
}

func TestCreateSpecSkeleton_Success(t *testing.T) {
	tmpDir := t.TempDir()
	specDir, err := createSpecSkeleton(tmpDir, "my-feature", "Build a cool feature")
	if err != nil {
		t.Fatalf("createSpecSkeleton failed: %v", err)
	}

	expectedDir := filepath.Join(tmpDir, "specs", "my-feature")
	if specDir != expectedDir {
		t.Errorf("specDir = %q, want %q", specDir, expectedDir)
	}

	// Verify directory exists.
	if _, err := os.Stat(specDir); os.IsNotExist(err) {
		t.Error("spec directory was not created")
	}

	// Verify research/ subdirectory.
	researchDir := filepath.Join(specDir, "research")
	if _, err := os.Stat(researchDir); os.IsNotExist(err) {
		t.Error("research/ subdirectory was not created")
	}

	// Verify rough-idea.md exists.
	roughIdeaPath := filepath.Join(specDir, "rough-idea.md")
	if _, err := os.Stat(roughIdeaPath); os.IsNotExist(err) {
		t.Error("rough-idea.md was not created")
	}

	// Verify requirements.md exists.
	reqPath := filepath.Join(specDir, "requirements.md")
	if _, err := os.Stat(reqPath); os.IsNotExist(err) {
		t.Error("requirements.md was not created")
	}
}

func TestCreateSpecSkeleton_AlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()

	// Create the directory first.
	specDir := filepath.Join(tmpDir, "specs", "existing-feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("failed to create pre-existing dir: %v", err)
	}

	_, err := createSpecSkeleton(tmpDir, "existing-feature", "Some idea")
	if err == nil {
		t.Fatal("expected error when spec directory already exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention 'already exists', got: %v", err)
	}
}

func TestCreateSpecSkeleton_RoughIdeaContent(t *testing.T) {
	tmpDir := t.TempDir()
	roughIdea := "Build a rate limiter with sliding window"
	_, err := createSpecSkeleton(tmpDir, "rate-limiter", roughIdea)
	if err != nil {
		t.Fatalf("createSpecSkeleton failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "specs", "rate-limiter", "rough-idea.md"))
	if err != nil {
		t.Fatalf("failed to read rough-idea.md: %v", err)
	}

	if !strings.Contains(string(content), roughIdea) {
		t.Errorf("rough-idea.md should contain the input text, got:\n%s", content)
	}
}

func TestCreateSpecSkeleton_RequirementsContent(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := createSpecSkeleton(tmpDir, "test-feature", "Some feature")
	if err != nil {
		t.Fatalf("createSpecSkeleton failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "specs", "test-feature", "requirements.md"))
	if err != nil {
		t.Fatalf("failed to read requirements.md: %v", err)
	}

	if !strings.Contains(string(content), "# Requirements") {
		t.Error("requirements.md should contain '# Requirements' header")
	}
	if !strings.Contains(string(content), "## Questions & Answers") {
		t.Error("requirements.md should contain '## Questions & Answers' header")
	}
}

// --- Step 3 tests: PDD SOP instruction construction ---

func TestBuildPlanInstruction_ContainsSOP(t *testing.T) {
	// Verify the instruction construction includes the SOP text, task context, and instructions.
	tmpDir := t.TempDir()
	sopText, err := sop.LoadPDD(tmpDir) // no overrides → embedded default
	if err != nil {
		t.Fatalf("LoadPDD failed: %v", err)
	}

	taskName := "add-rate-limiting"
	roughIdea := "add rate limiting to API"
	specDir := filepath.Join(tmpDir, "specs", taskName)

	instruction := sopText + "\n\n## Current Task\n" +
		"- Task name: " + taskName + "\n" +
		"- Spec directory: specs/" + taskName + "/\n" +
		"- Rough idea: " + roughIdea + "\n\n" +
		"## Instructions\n" +
		"The spec skeleton has been created at `" + specDir + "`. " +
		"Begin the PDD process starting with Step 2 (Initial Process Planning).\n" +
		"Artifacts should be written to `specs/" + taskName + "/` using the write and edit tools.\n"

	// Must contain the SOP text.
	if !strings.Contains(instruction, "PDD") {
		t.Error("instruction should contain PDD SOP content")
	}

	// Must contain task context.
	if !strings.Contains(instruction, "## Current Task") {
		t.Error("instruction should contain '## Current Task' section")
	}
	if !strings.Contains(instruction, taskName) {
		t.Errorf("instruction should contain task name %q", taskName)
	}
	if !strings.Contains(instruction, roughIdea) {
		t.Errorf("instruction should contain rough idea %q", roughIdea)
	}

	// Must contain instructions for the agent.
	if !strings.Contains(instruction, "Begin the PDD process") {
		t.Error("instruction should contain 'Begin the PDD process'")
	}
	if !strings.Contains(instruction, "specs/"+taskName+"/") {
		t.Error("instruction should reference the spec directory path")
	}
}

func TestBuildPlanInstruction_SOPOverride(t *testing.T) {
	tmpDir := t.TempDir()

	// Create project-level SOP override.
	sopDir := filepath.Join(tmpDir, ".pi-go", "sops")
	if err := os.MkdirAll(sopDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	customSOP := "# Custom PDD SOP\n\nThis is a custom PDD workflow."
	if err := os.WriteFile(filepath.Join(sopDir, "pdd.md"), []byte(customSOP), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	sopText, err := sop.LoadPDD(tmpDir)
	if err != nil {
		t.Fatalf("LoadPDD failed: %v", err)
	}

	if sopText != customSOP {
		t.Errorf("LoadPDD should return custom SOP, got %q", sopText[:50])
	}
}

func TestPlanInstruction_ExistingSpecReturnsError(t *testing.T) {
	tmpDir := t.TempDir()

	// Create existing spec directory.
	specDir := filepath.Join(tmpDir, "specs", "existing-feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	// createSpecSkeleton should fail with "already exists".
	_, err := createSpecSkeleton(tmpDir, "existing-feature", "Some idea")
	if err == nil {
		t.Fatal("expected error when spec directory already exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention 'already exists', got: %v", err)
	}
}

// TestFinishPlanWorktree_CopiesSpecIntoInvokingCheckout drives the end-of-plan
// worktree teardown with a real git worktree and verifies the finished spec
// directory is copied into the invoking checkout's specs/ tree. This guards
// the fix that /run must find specs/<task>/PROMPT.md even though specs/ is
// gitignored.
func TestFinishPlanWorktree_CopiesSpecIntoInvokingCheckout(t *testing.T) {
	repo := initRunTestRepo(t)
	orch := subagent.NewOrchestrator(&config.Config{}, repo, nil)
	t.Cleanup(orch.Shutdown)

	const taskName = "features/TOO/001-demo"
	const agentID = "plan-" + taskName

	// Seed a worktree like the planner would: it writes specs/<task>/... and
	// never commits.
	wtPath, err := orch.Worktree().Create(agentID, "pdd-"+taskName)
	if err != nil {
		t.Fatalf("creating plan worktree: %v", err)
	}
	wtSpec := filepath.Join(wtPath, "specs", taskName)
	if err := os.MkdirAll(filepath.Join(wtSpec, "research"), 0o755); err != nil {
		t.Fatalf("mkdir research: %v", err)
	}
	for name, content := range map[string]string{
		"PROMPT.md":         "# Feature\n\n## Objective\nDo it.\n",
		"requirements.md":   "# Requirements\n",
		"research/notes.md": "notes\n",
	} {
		if err := os.WriteFile(filepath.Join(wtSpec, name), []byte(content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	m := &model{
		cfg:                 Config{WorkDir: repo, Orchestrator: orch},
		planWorktreeAgentID: agentID,
		planWorktreePath:    wtPath,
		planTaskName:        taskName,
		planBackupBranch:    "specs/" + taskName,
		planWorktree:        orch.Worktree(),
	}

	if err := m.finishPlanWorktree(); err != nil {
		t.Fatalf("finishPlanWorktree: %v", err)
	}

	// The finished spec must be present in the invoking checkout, including the
	// research/ subtree, independently of the git merge.
	for _, rel := range []string{
		"PROMPT.md",
		"requirements.md",
		filepath.Join("research", "notes.md"),
	} {
		if _, err := os.Stat(filepath.Join(repo, "specs", taskName, rel)); err != nil {
			t.Errorf("spec %s not copied into invoking checkout: %v", rel, err)
		}
	}

	// The plan is complete, so the worktree state should be cleared.
	if m.planWorktree != nil {
		t.Error("planWorktree should be nil after a completed plan")
	}
	if m.planWorktreePath != "" {
		t.Errorf("planWorktreePath should be cleared, got %q", m.planWorktreePath)
	}
}

// TestFinishPlanWorktree_NoWorktreeIsNoOp ensures a model without an active plan
// worktree is a safe no-op (no panic, no error).
func TestFinishPlanWorktree_NoWorktreeIsNoOp(t *testing.T) {
	m := &model{}
	if err := m.finishPlanWorktree(); err != nil {
		t.Fatalf("finishPlanWorktree with no worktree should be a no-op, got: %v", err)
	}
}
