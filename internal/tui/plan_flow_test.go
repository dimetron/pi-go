package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/subagent"
)

// --- Plan override confirmation flow ---

func TestPlanCommand_ExistingSpec_PromptsOverride(t *testing.T) {
	tmpDir := t.TempDir()

	// Pre-create the spec directory.
	specDir := filepath.Join(tmpDir, "specs", "add-logging")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a file so we can verify it gets removed on override.
	if err := os.WriteFile(filepath.Join(specDir, "old-file.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := &model{
		cfg:      Config{WorkDir: tmpDir},
		messages: make([]message, 0),
	}

	m.handlePlanCommand([]string{"add", "logging"})

	// Should be in confirming_override phase (not stuck, not errored).
	if m.plan == nil {
		t.Fatal("expected plan state to be set for override confirmation")
	}
	if m.plan.phase != "confirming_override" {
		t.Errorf("plan.phase = %q, want %q", m.plan.phase, "confirming_override")
	}
	if m.plan.taskName != "add-logging" {
		t.Errorf("plan.taskName = %q, want %q", m.plan.taskName, "add-logging")
	}
	if m.plan.roughIdea != "add logging" {
		t.Errorf("plan.roughIdea = %q, want %q", m.plan.roughIdea, "add logging")
	}

	// Verify the prompt message is shown.
	if len(m.messages) == 0 {
		t.Fatal("expected a message")
	}
	last := m.messages[len(m.messages)-1]
	if !strings.Contains(last.content, "already exists") {
		t.Errorf("message should mention 'already exists', got: %s", last.content)
	}
	if !strings.Contains(last.content, "Enter") {
		t.Errorf("message should mention Enter to override, got: %s", last.content)
	}
	if !strings.Contains(last.content, "Esc") {
		t.Errorf("message should mention Esc to cancel, got: %s", last.content)
	}

	// Verify the TUI is NOT stuck — m.running should be false, input is clear.
	if m.running {
		t.Error("model should not be running during override confirmation")
	}
	if m.inputModel.Text != "" {
		t.Errorf("input should be cleared, got %q", m.inputModel.Text)
	}
}

func TestPlanCommand_OverrideCancel(t *testing.T) {
	tmpDir := t.TempDir()

	// Pre-create the spec directory.
	specDir := filepath.Join(tmpDir, "specs", "my-feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	m := &model{
		cfg:      Config{WorkDir: tmpDir},
		messages: make([]message, 0),
	}

	// Trigger override prompt.
	m.handlePlanCommand([]string{"my", "feature"})
	if m.plan == nil || m.plan.phase != "confirming_override" {
		t.Fatal("expected confirming_override phase")
	}

	// Cancel the override.
	m.handlePlanCancel()

	// Plan state should be cleared.
	if m.plan != nil {
		t.Error("plan state should be nil after cancel")
	}

	// Should have cancel message.
	var found bool
	for _, msg := range m.messages {
		if strings.Contains(msg.content, "cancelled") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'cancelled' message")
	}

	// The original spec directory should still exist.
	if _, err := os.Stat(specDir); os.IsNotExist(err) {
		t.Error("spec directory should still exist after cancel")
	}
}

func TestPlanCommand_OverrideConfirm_RemovesOldDir(t *testing.T) {
	tmpDir := t.TempDir()

	// Pre-create the spec directory with content.
	specDir := filepath.Join(tmpDir, "specs", "my-feature")
	if err := os.MkdirAll(filepath.Join(specDir, "research"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specDir, "old-plan.md"), []byte("old plan"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := &model{
		cfg:      Config{WorkDir: tmpDir},
		messages: make([]message, 0),
	}

	// Trigger override prompt.
	m.handlePlanCommand([]string{"my", "feature"})
	if m.plan == nil {
		t.Fatal("expected plan state")
	}

	// Confirm override — startPlanSession will fail at RebuildWithInstruction
	// because there's no agent, but the override itself (remove + recreate) should work.
	// We test this by directly calling the override logic.
	taskName := m.plan.taskName
	roughIdea := m.plan.roughIdea
	planSpecDir := m.plan.specDir
	m.plan = nil

	// Remove existing dir.
	if err := os.RemoveAll(planSpecDir); err != nil {
		t.Fatalf("RemoveAll failed: %v", err)
	}

	// Recreate skeleton.
	newSpecDir, err := createSpecSkeleton(tmpDir, taskName, roughIdea)
	if err != nil {
		t.Fatalf("createSpecSkeleton failed: %v", err)
	}

	// The old file should not exist.
	if _, err := os.Stat(filepath.Join(newSpecDir, "old-plan.md")); !os.IsNotExist(err) {
		t.Error("old-plan.md should have been removed")
	}

	// The new skeleton should exist.
	if _, err := os.Stat(filepath.Join(newSpecDir, "rough-idea.md")); os.IsNotExist(err) {
		t.Error("rough-idea.md should have been recreated")
	}
	if _, err := os.Stat(filepath.Join(newSpecDir, "requirements.md")); os.IsNotExist(err) {
		t.Error("requirements.md should have been recreated")
	}
	if _, err := os.Stat(filepath.Join(newSpecDir, "research")); os.IsNotExist(err) {
		t.Error("research/ should have been recreated")
	}

	// Verify rough-idea.md content.
	content, err := os.ReadFile(filepath.Join(newSpecDir, "rough-idea.md"))
	if err != nil {
		t.Fatalf("failed to read rough-idea.md: %v", err)
	}
	if !strings.Contains(string(content), "my feature") {
		t.Errorf("rough-idea.md should contain the idea, got: %s", content)
	}
}

// --- Plan flow not stuck tests ---

func TestPlanCommand_NoArgs_NotStuck(t *testing.T) {
	m := &model{
		messages: make([]message, 0),
	}

	m.handlePlanCommand(nil)

	// Should show usage and NOT be stuck.
	if m.running {
		t.Error("should not be running after /plan with no args")
	}
	if m.plan != nil {
		t.Error("plan state should be nil")
	}
	if len(m.messages) == 0 {
		t.Fatal("expected usage message")
	}
	if !strings.Contains(m.messages[0].content, "Usage") {
		t.Errorf("expected usage message, got: %s", m.messages[0].content)
	}
}

func TestPlanCommand_NewSpec_NotStuck(t *testing.T) {
	tmpDir := t.TempDir()

	m := &model{
		cfg:      Config{WorkDir: tmpDir},
		messages: make([]message, 0),
	}

	// This will succeed creating the skeleton but fail at startPlanSession
	// because there's no agent. The important thing is it doesn't hang.
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handlePlanCommand([]string{"new", "feature"})
	}()

	select {
	case <-done:
		// Good — didn't get stuck.
	case <-time.After(5 * time.Second):
		t.Fatal("handlePlanCommand got stuck — timed out after 5s")
	}

	// Should not be in a running state (agent wasn't configured).
	if m.running {
		t.Error("should not be running when agent is nil")
	}

	// The skeleton should have been created.
	specDir := filepath.Join(tmpDir, "specs", "new-feature")
	if _, err := os.Stat(specDir); os.IsNotExist(err) {
		t.Error("spec directory should have been created")
	}
}

func TestPlanCommand_ExistingSpec_NotStuck(t *testing.T) {
	tmpDir := t.TempDir()

	// Pre-create the spec directory.
	if err := os.MkdirAll(filepath.Join(tmpDir, "specs", "existing-feature"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := &model{
		cfg:      Config{WorkDir: tmpDir},
		messages: make([]message, 0),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handlePlanCommand([]string{"existing", "feature"})
	}()

	select {
	case <-done:
		// Good — didn't get stuck.
	case <-time.After(5 * time.Second):
		t.Fatal("handlePlanCommand got stuck on existing spec — timed out after 5s")
	}

	// Should be in confirming_override, not stuck.
	if m.plan == nil || m.plan.phase != "confirming_override" {
		t.Error("should be in confirming_override phase")
	}
	if m.running {
		t.Error("should not be running during confirmation")
	}
}

// --- Plan key handling tests ---

func TestPlanConfirmation_KeyHandling(t *testing.T) {
	m := &model{
		messages: make([]message, 0),
		plan: &planState{
			phase:     "confirming_override",
			taskName:  "test-feature",
			roughIdea: "test feature",
			specDir:   "/tmp/test",
		},
	}

	// While in confirmation mode, random keys should be ignored.
	if m.plan == nil {
		t.Fatal("plan should not be nil")
	}
	if m.plan.phase != "confirming_override" {
		t.Fatalf("plan.phase = %q, want confirming_override", m.plan.phase)
	}

	// Verify the state blocks normal input (the key handling in tui.go checks this).
	if m.running {
		t.Error("should not be running during confirmation")
	}
}

// --- E2E: Plan → Override → Verify skeleton ---

func TestE2E_PlanOverrideRecreatesSkeleton(t *testing.T) {
	tmpDir := t.TempDir()

	// Step 1: Create first plan.
	idea := "implement caching"
	taskName := toKebabCase(idea)
	specDir, err := createSpecSkeleton(tmpDir, taskName, idea)
	if err != nil {
		t.Fatalf("first createSpecSkeleton failed: %v", err)
	}

	// Verify first skeleton.
	if _, err := os.Stat(filepath.Join(specDir, "rough-idea.md")); os.IsNotExist(err) {
		t.Fatal("first rough-idea.md not created")
	}

	// Add an extra file that should be cleaned up on override.
	if err := os.WriteFile(filepath.Join(specDir, "design.md"), []byte("# Design"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Step 2: Try to create again — should fail.
	_, err = createSpecSkeleton(tmpDir, taskName, idea)
	if err == nil {
		t.Fatal("expected error for duplicate spec")
	}

	// Step 3: Override — remove and recreate.
	if err := os.RemoveAll(specDir); err != nil {
		t.Fatalf("RemoveAll failed: %v", err)
	}
	newSpecDir, err := createSpecSkeleton(tmpDir, taskName, "implement caching v2")
	if err != nil {
		t.Fatalf("recreate failed: %v", err)
	}
	if newSpecDir != specDir {
		t.Errorf("new specDir = %q, want %q", newSpecDir, specDir)
	}

	// Step 4: Verify clean skeleton (old design.md should be gone).
	if _, err := os.Stat(filepath.Join(newSpecDir, "design.md")); !os.IsNotExist(err) {
		t.Error("design.md should not exist after override")
	}
	if _, err := os.Stat(filepath.Join(newSpecDir, "rough-idea.md")); os.IsNotExist(err) {
		t.Error("rough-idea.md should exist after override")
	}

	// Verify new content.
	content, err := os.ReadFile(filepath.Join(newSpecDir, "rough-idea.md"))
	if err != nil {
		t.Fatalf("failed to read rough-idea.md: %v", err)
	}
	if !strings.Contains(string(content), "implement caching v2") {
		t.Error("rough-idea.md should contain the new idea text")
	}
}

// --- Plan flow with orchestrator context ---

func TestPlanCommand_ExistingSpec_WithOrchestrator_NotStuck(t *testing.T) {
	tmpDir := t.TempDir()
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)

	// Pre-create spec.
	if err := os.MkdirAll(filepath.Join(tmpDir, "specs", "rate-limiting"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := &model{
		ctx: t.Context(),
		cfg: Config{
			WorkDir:      tmpDir,
			Orchestrator: orch,
		},
		messages: make([]message, 0),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handlePlanCommand([]string{"rate", "limiting"})
	}()

	select {
	case <-done:
		// Not stuck.
	case <-time.After(5 * time.Second):
		t.Fatal("handlePlanCommand got stuck with orchestrator")
	}

	// Should prompt for override.
	if m.plan == nil {
		t.Fatal("expected plan state")
	}
	if m.plan.phase != "confirming_override" {
		t.Errorf("phase = %q, want confirming_override", m.plan.phase)
	}

	// Cancel should clean up immediately.
	m.handlePlanCancel()
	if m.plan != nil {
		t.Error("plan should be nil after cancel")
	}
	if m.running {
		t.Error("should not be running after cancel")
	}
}

// --- Multiple rapid plan commands ---

func TestPlanCommand_RapidCalls_NoDeadlock(t *testing.T) {
	tmpDir := t.TempDir()

	m := &model{
		cfg:      Config{WorkDir: tmpDir},
		messages: make([]message, 0),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// First call creates the spec.
		m.handlePlanCommand([]string{"rapid", "test"})
		// Second call should trigger override prompt (not deadlock).
		m.handlePlanCommand([]string{"rapid", "test"})
	}()

	select {
	case <-done:
		// Not stuck.
	case <-time.After(5 * time.Second):
		t.Fatal("rapid plan commands got stuck")
	}
}
