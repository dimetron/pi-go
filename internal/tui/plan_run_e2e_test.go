package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/subagent"
)

// --- E2E: /plan creates spec skeleton ---

func TestE2E_PlanCreatesSpecSkeleton(t *testing.T) {
	tmpDir := t.TempDir()

	// Simulate /plan command input (without agent — only skeleton creation).
	idea := "add rate limiting to API"
	taskName := toKebabCase(idea)
	specDir, err := createSpecSkeleton(tmpDir, taskName, idea)
	if err != nil {
		t.Fatalf("createSpecSkeleton failed: %v", err)
	}

	// Verify directory structure.
	if _, err := os.Stat(specDir); os.IsNotExist(err) {
		t.Error("spec directory not created")
	}
	if _, err := os.Stat(filepath.Join(specDir, "research")); os.IsNotExist(err) {
		t.Error("research/ subdirectory not created")
	}
	if _, err := os.Stat(filepath.Join(specDir, "rough-idea.md")); os.IsNotExist(err) {
		t.Error("rough-idea.md not created")
	}
	if _, err := os.Stat(filepath.Join(specDir, "requirements.md")); os.IsNotExist(err) {
		t.Error("requirements.md not created")
	}

	// Verify rough-idea.md content.
	content, err := os.ReadFile(filepath.Join(specDir, "rough-idea.md"))
	if err != nil {
		t.Fatalf("failed to read rough-idea.md: %v", err)
	}
	if !strings.Contains(string(content), idea) {
		t.Errorf("rough-idea.md should contain the idea text")
	}

	// Verify the taskName is correct kebab-case.
	if taskName != "add-rate-limiting-to-api" {
		t.Errorf("taskName = %q, want %q", taskName, "add-rate-limiting-to-api")
	}

	// Verify the spec path is under specs/.
	expectedDir := filepath.Join(tmpDir, "specs", "add-rate-limiting-to-api")
	if specDir != expectedDir {
		t.Errorf("specDir = %q, want %q", specDir, expectedDir)
	}

}

// --- E2E: /run reads PROMPT.md and spawns agent ---

func TestE2E_RunReadsPromptAndDetectsGates(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a spec with PROMPT.md containing gates.
	specDir := filepath.Join(tmpDir, "specs", "my-feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	promptContent := `# Add Rate Limiting

## Objective

Add rate limiting to the API.

## Gates

- **build**: ` + "`go build ./...`" + `
- **test**: ` + "`go test ./...`" + `
- **vet**: ` + "`go vet ./...`" + `

## Reference

See design.md.
`
	if err := os.WriteFile(filepath.Join(specDir, "PROMPT.md"), []byte(promptContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Read and parse.
	content, err := readPromptMD(tmpDir, "my-feature")
	if err != nil {
		t.Fatalf("readPromptMD failed: %v", err)
	}

	gates := parseGates(content)
	if len(gates) != 3 {
		t.Fatalf("expected 3 gates, got %d", len(gates))
	}
	if gates[0].Name != "build" || gates[0].Command != "go build ./..." {
		t.Errorf("gate[0] = %+v, want build/go build ./...", gates[0])
	}
	if gates[1].Name != "test" || gates[1].Command != "go test ./..." {
		t.Errorf("gate[1] = %+v, want test/go test ./...", gates[1])
	}
	if gates[2].Name != "vet" || gates[2].Command != "go vet ./..." {
		t.Errorf("gate[2] = %+v, want vet/go vet ./...", gates[2])
	}

	// Verify prompt construction includes content and execution instructions.
	prompt := buildRunPrompt("my-feature", content)
	if !strings.Contains(prompt, "Add Rate Limiting") {
		t.Error("run prompt should contain PROMPT.md content")
	}
	if !strings.Contains(prompt, "## Execution Instructions") {
		t.Error("run prompt should contain execution instructions")
	}
}

// --- E2E: Gate parsing roundtrip ---

func TestE2E_GateParsingRoundtrip(t *testing.T) {
	tmpDir := t.TempDir()

	// Step 1: /plan creates skeleton.
	idea := "build a rate limiter"
	taskName := toKebabCase(idea)
	specDir, err := createSpecSkeleton(tmpDir, taskName, idea)
	if err != nil {
		t.Fatalf("createSpecSkeleton failed: %v", err)
	}

	// Step 2: Simulate PDD producing a PROMPT.md with gates.
	promptContent := `# Build a Rate Limiter

## Objective

Implement a sliding window rate limiter.

## Gates

- **build**: ` + "`go build ./...`" + `
- **test**: ` + "`go test ./...`" + `

## Constraints

- Go only.
`
	if err := os.WriteFile(filepath.Join(specDir, "PROMPT.md"), []byte(promptContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Step 3: /run reads it back — simulating the roundtrip.
	content, err := readPromptMD(tmpDir, taskName)
	if err != nil {
		t.Fatalf("readPromptMD failed: %v", err)
	}
	gates := parseGates(content)

	if len(gates) != 2 {
		t.Fatalf("expected 2 gates, got %d", len(gates))
	}
	if gates[0].Name != "build" {
		t.Errorf("gate[0].Name = %q, want %q", gates[0].Name, "build")
	}
	if gates[1].Name != "test" {
		t.Errorf("gate[1].Name = %q, want %q", gates[1].Name, "test")
	}

	// Verify spec shows up in listAvailableSpecs.
	specs, err := listAvailableSpecs(tmpDir)
	if err != nil {
		t.Fatalf("listAvailableSpecs failed: %v", err)
	}
	if len(specs) != 1 || specs[0] != taskName {
		t.Errorf("specs = %v, want [%q]", specs, taskName)
	}
}

// --- E2E: Gate pass → merge ---

func TestE2E_GatePassTriggersMerge(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, "")

	m := &model{
		cfg: Config{
			Orchestrator: orch,
		},
		messages: make([]message, 0),
		run: &runState{
			specName:   "test-spec",
			agentID:    "task-e2e-1",
			phase:      "gating",
			maxRetries: 3,
			gates: []Gate{
				{Name: "build", Command: "go build ./..."},
				{Name: "test", Command: "go test ./..."},
			},
		},
	}

	// Simulate all gates passing.
	msg := runGateResultMsg{
		results: []GateResult{
			{Name: "build", Command: "go build ./...", Passed: true},
			{Name: "test", Command: "go test ./...", Passed: true},
		},
		passed: true,
	}

	m.handleRunGateResult(msg)

	if m.run.phase != "merging" {
		t.Errorf("phase = %q, want %q", m.run.phase, "merging")
	}

	// Verify messages contain gate results and merge trigger.
	var hasGateResults, hasMergeMsg bool
	for _, msg := range m.messages {
		if strings.Contains(msg.content, "Gate Results") {
			hasGateResults = true
		}
		if strings.Contains(msg.content, "All gates passed") {
			hasMergeMsg = true
		}
	}
	if !hasGateResults {
		t.Error("expected gate results message")
	}
	if !hasMergeMsg {
		t.Error("expected merge trigger message")
	}
}

// --- E2E: Gate fail → retry ---

func TestE2E_GateFailTriggersRetry(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, "")

	m := &model{
		ctx: t.Context(),
		cfg: Config{
			Orchestrator: orch,
		},
		messages: make([]message, 0),
		run: &runState{
			specName:   "test-spec",
			promptMD:   "# Test\n\n## Objective\nDo stuff.\n",
			agentID:    "task-e2e-2",
			phase:      "gating",
			retries:    0,
			maxRetries: 3,
		},
	}

	// Simulate gate failure.
	msg := runGateResultMsg{
		results: []GateResult{
			{Name: "build", Command: "go build ./...", Passed: true},
			{Name: "test", Command: "go test ./...", Passed: false, Output: "FAIL pkg/rate_limiter"},
		},
		passed: false,
	}

	m.handleRunGateResult(msg)

	// Should attempt retry (retry count incremented).
	if m.run.retries != 1 {
		t.Errorf("retries = %d, want 1", m.run.retries)
	}

	// Gate output should be captured.
	if !strings.Contains(m.run.gateOutput, "FAIL pkg/rate_limiter") {
		t.Error("gateOutput should contain failure output")
	}

	// Since orchestrator can't actually spawn (no config), it should fail to "failed" phase.
	// This is expected in unit test context — the retry attempt was made but spawn failed.
	if m.run.phase != "failed" {
		t.Errorf("phase = %q, want %q (spawn fails in test context)", m.run.phase, "failed")
	}

	// Verify retry message was shown.
	var hasRetryMsg bool
	for _, msg := range m.messages {
		if strings.Contains(msg.content, "Gate failed") && strings.Contains(msg.content, "retry 1") {
			hasRetryMsg = true
			break
		}
	}
	if !hasRetryMsg {
		t.Error("expected retry message")
	}
}

// --- Edge case: /plan with very long idea text ---

func TestE2E_PlanVeryLongIdea(t *testing.T) {
	tmpDir := t.TempDir()

	longIdea := "implement a comprehensive rate limiting system with sliding window algorithm and redis backend for distributed rate limiting across multiple application servers with configurable limits per endpoint and per user authentication tokens"
	taskName := toKebabCase(longIdea)

	if len(taskName) > 50 {
		t.Errorf("taskName length = %d, want <= 50", len(taskName))
	}
	if strings.HasSuffix(taskName, "-") {
		t.Error("taskName should not end with hyphen")
	}

	// Should still create skeleton successfully.
	specDir, err := createSpecSkeleton(tmpDir, taskName, longIdea)
	if err != nil {
		t.Fatalf("createSpecSkeleton failed: %v", err)
	}

	// Verify rough-idea.md contains the full (untruncated) idea.
	content, err := os.ReadFile(filepath.Join(specDir, "rough-idea.md"))
	if err != nil {
		t.Fatalf("failed to read rough-idea.md: %v", err)
	}
	if !strings.Contains(string(content), longIdea) {
		t.Error("rough-idea.md should contain the full idea text (not truncated)")
	}
}

// --- Edge case: /run while another agent is running ---

func TestE2E_RunWhileAgentRunning(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a spec.
	specDir := filepath.Join(tmpDir, "specs", "my-feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specDir, "PROMPT.md"), []byte("# Test"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := &model{
		cfg: Config{
			WorkDir:      tmpDir,
			Orchestrator: subagent.NewOrchestrator(&config.Config{}, ""),
		},
		messages: make([]message, 0),
		running:  true, // already running
	}

	// /run should still process (the handleRunCommand doesn't check m.running;
	// the orchestrator handles concurrent agents). The important thing is it
	// doesn't panic or deadlock.
	m.handleRunCommand([]string{"my-feature"})

	// Should have attempted to spawn (will fail due to empty config, but no panic).
	if len(m.messages) == 0 {
		t.Error("expected at least one message")
	}
}

// --- Edge case: /run with spec that has no plan.md (only PROMPT.md) ---

func TestE2E_RunWithOnlyPromptMD(t *testing.T) {
	tmpDir := t.TempDir()

	// Create spec with PROMPT.md but NO plan.md.
	specDir := filepath.Join(tmpDir, "specs", "no-plan-spec")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	promptContent := "# Feature\n\n## Objective\n\nBuild it.\n"
	if err := os.WriteFile(filepath.Join(specDir, "PROMPT.md"), []byte(promptContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// readPromptMD should work fine — plan.md is not required for reading PROMPT.md.
	content, err := readPromptMD(tmpDir, "no-plan-spec")
	if err != nil {
		t.Fatalf("readPromptMD failed: %v", err)
	}
	if content != promptContent {
		t.Errorf("content mismatch")
	}

	// No gates should be parsed.
	gates := parseGates(content)
	if len(gates) != 0 {
		t.Errorf("expected 0 gates, got %d", len(gates))
	}

	// Build prompt should still work.
	prompt := buildRunPrompt("no-plan-spec", content)
	if !strings.Contains(prompt, "Build it.") {
		t.Error("prompt should contain PROMPT.md content")
	}
}

// --- Edge case: /plan duplicate spec name ---

func TestE2E_PlanDuplicateSpecName(t *testing.T) {
	tmpDir := t.TempDir()

	idea := "add logging"
	taskName := toKebabCase(idea)

	// First creation should succeed.
	_, err := createSpecSkeleton(tmpDir, taskName, idea)
	if err != nil {
		t.Fatalf("first createSpecSkeleton failed: %v", err)
	}

	// Second creation should fail.
	_, err = createSpecSkeleton(tmpDir, taskName, idea)
	if err == nil {
		t.Error("expected error for duplicate spec name")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention 'already exists', got: %v", err)
	}
}

// --- Edge case: Multiple specs listed ---

func TestE2E_MultipleSpecsListed(t *testing.T) {
	tmpDir := t.TempDir()
	specsDir := filepath.Join(tmpDir, "specs")

	// Create 3 specs with PROMPT.md.
	for _, name := range []string{"alpha-feature", "beta-feature", "gamma-feature"} {
		dir := filepath.Join(specsDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "PROMPT.md"), []byte("# "+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Create one without PROMPT.md (in-progress).
	if err := os.MkdirAll(filepath.Join(specsDir, "delta-wip"), 0o755); err != nil {
		t.Fatal(err)
	}

	specs, err := listAvailableSpecs(tmpDir)
	if err != nil {
		t.Fatalf("listAvailableSpecs failed: %v", err)
	}

	if len(specs) != 3 {
		t.Fatalf("expected 3 specs, got %d: %v", len(specs), specs)
	}

	// Verify sorted order.
	expected := []string{"alpha-feature", "beta-feature", "gamma-feature"}
	for i, s := range specs {
		if s != expected[i] {
			t.Errorf("specs[%d] = %q, want %q", i, s, expected[i])
		}
	}

	// /run no-args should list them.
	m := &model{
		cfg:      Config{WorkDir: tmpDir},
		messages: make([]message, 0),
	}
	m.handleRunCommand(nil)

	last := m.messages[len(m.messages)-1]
	for _, name := range expected {
		if !strings.Contains(last.content, name) {
			t.Errorf("expected %q in available specs, got: %s", name, last.content)
		}
	}
}

// --- E2E: Full retry prompt construction chain ---

func TestE2E_RetryPromptChain(t *testing.T) {
	// Simulate the full chain: PROMPT.md → gates fail → retry prompt construction.
	promptMD := `# My Feature

## Objective

Build something.

## Gates

- **build**: ` + "`go build ./...`" + `
- **test**: ` + "`go test ./...`" + `
`

	gates := parseGates(promptMD)
	if len(gates) != 2 {
		t.Fatalf("expected 2 gates, got %d", len(gates))
	}

	// Simulate gate results with test failure.
	results := []GateResult{
		{Name: "build", Command: "go build ./...", Passed: true, Output: ""},
		{Name: "test", Command: "go test ./...", Passed: false, Output: "--- FAIL: TestFoo\nExpected 42, got 0"},
	}

	gateOutput := formatGateFailures(results)
	if !strings.Contains(gateOutput, "FAIL: TestFoo") {
		t.Error("gate output should contain failure details")
	}
	if strings.Contains(gateOutput, "build") {
		t.Error("gate output should not include passed gates")
	}

	// Build retry prompt.
	retryPrompt := buildRetryPrompt("my-feature", promptMD, gateOutput)

	// Verify retry prompt contains all expected sections.
	if !strings.Contains(retryPrompt, "failed gate validation") {
		t.Error("retry prompt should mention gate validation failure")
	}
	if !strings.Contains(retryPrompt, "## Gate Failures") {
		t.Error("retry prompt should contain gate failures section")
	}
	if !strings.Contains(retryPrompt, "FAIL: TestFoo") {
		t.Error("retry prompt should contain specific failure output")
	}
	if !strings.Contains(retryPrompt, "## Original Task") {
		t.Error("retry prompt should contain original task")
	}
	if !strings.Contains(retryPrompt, "Build something.") {
		t.Error("retry prompt should contain original objective")
	}
	if !strings.Contains(retryPrompt, "Fix the issues") {
		t.Error("retry prompt should contain fix instructions")
	}
}

// --- E2E: Merge success flow ---

func TestE2E_MergeSuccessFlow(t *testing.T) {
	m := &model{
		messages: make([]message, 0),
		run: &runState{
			specName: "rate-limiter",
			agentID:  "task-merge-1",
			phase:    "merging",
		},
	}

	// Simulate successful merge.
	msg := runMergeResultMsg{output: "Merge made by 'ort' strategy."}
	m.handleRunMergeResult(msg)

	if m.run.phase != "done" {
		t.Errorf("phase = %q, want %q", m.run.phase, "done")
	}

	var found bool
	for _, msg := range m.messages {
		if strings.Contains(msg.content, "rate-limiter") && strings.Contains(msg.content, "merged successfully") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected success message mentioning spec name")
	}
}

// --- E2E: Agent done with gates triggers gating, then pass triggers merge ---

func TestE2E_AgentDoneGatePassMergeFlow(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, "")

	m := &model{
		cfg: Config{
			Orchestrator: orch,
		},
		messages: make([]message, 0),
		running:  true,
		run: &runState{
			specName: "my-feature",
			agentID:  "task-flow-1",
			phase:    "running",
			gates: []Gate{
				{Name: "build", Command: "echo ok"},
			},
		},
	}

	// Step 1: Agent done → transitions to gating.
	m.handleRunAgentDone()
	if m.run.phase != "gating" {
		t.Fatalf("phase after done = %q, want %q", m.run.phase, "gating")
	}
	if m.running {
		t.Error("running should be false after done")
	}

	// Step 2: Gates pass → transitions to merging.
	gateMsg := runGateResultMsg{
		results: []GateResult{
			{Name: "build", Command: "echo ok", Passed: true},
		},
		passed: true,
	}
	m.handleRunGateResult(gateMsg)
	if m.run.phase != "merging" {
		t.Fatalf("phase after gates = %q, want %q", m.run.phase, "merging")
	}

	// Step 3: Merge succeeds → done.
	mergeMsg := runMergeResultMsg{output: "Already up to date."}
	m.handleRunMergeResult(mergeMsg)
	if m.run.phase != "done" {
		t.Errorf("phase after merge = %q, want %q", m.run.phase, "done")
	}
}
