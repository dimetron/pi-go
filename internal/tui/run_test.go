package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/subagent"
)

func TestParseGates_Standard(t *testing.T) {
	content := `# My Spec

## Gates

- **build**: ` + "`go build ./...`" + `

## Reference

Some reference.
`
	gates := parseGates(content)
	if len(gates) != 1 {
		t.Fatalf("expected 1 gate, got %d", len(gates))
	}
	if gates[0].Name != "build" {
		t.Errorf("gate name = %q, want %q", gates[0].Name, "build")
	}
	if gates[0].Command != "go build ./..." {
		t.Errorf("gate command = %q, want %q", gates[0].Command, "go build ./...")
	}
}

func TestParseGates_Multiple(t *testing.T) {
	content := `## Gates

- **build**: ` + "`go build ./...`" + `
- **test**: ` + "`go test ./...`" + `
- **vet**: ` + "`go vet ./...`" + `
`
	gates := parseGates(content)
	if len(gates) != 3 {
		t.Fatalf("expected 3 gates, got %d", len(gates))
	}
	expected := []struct{ name, cmd string }{
		{"build", "go build ./..."},
		{"test", "go test ./..."},
		{"vet", "go vet ./..."},
	}
	for i, e := range expected {
		if gates[i].Name != e.name {
			t.Errorf("gate[%d].Name = %q, want %q", i, gates[i].Name, e.name)
		}
		if gates[i].Command != e.cmd {
			t.Errorf("gate[%d].Command = %q, want %q", i, gates[i].Command, e.cmd)
		}
	}
}

func TestParseGates_NoSection(t *testing.T) {
	content := `# My Spec

## Objective

Do something.

## Reference

Some reference.
`
	gates := parseGates(content)
	if len(gates) != 0 {
		t.Errorf("expected 0 gates, got %d", len(gates))
	}
}

func TestParseGates_Malformed(t *testing.T) {
	content := `## Gates

- **build**: ` + "`go build ./...`" + `
- this line has no backtick command
- not a gate at all
- **test**: ` + "`go test ./...`" + `
`
	gates := parseGates(content)
	if len(gates) != 2 {
		t.Fatalf("expected 2 gates (skipping malformed), got %d", len(gates))
	}
	if gates[0].Name != "build" {
		t.Errorf("gate[0].Name = %q, want %q", gates[0].Name, "build")
	}
	if gates[1].Name != "test" {
		t.Errorf("gate[1].Name = %q, want %q", gates[1].Name, "test")
	}
}

func TestParseGates_StopsAtNextHeading(t *testing.T) {
	content := `## Gates

- **build**: ` + "`go build ./...`" + `

## Constraints

- **lint**: ` + "`golangci-lint run`" + `
`
	gates := parseGates(content)
	if len(gates) != 1 {
		t.Fatalf("expected 1 gate (stops at next heading), got %d", len(gates))
	}
	if gates[0].Name != "build" {
		t.Errorf("gate name = %q, want %q", gates[0].Name, "build")
	}
}

func TestParseGates_PlainFormat(t *testing.T) {
	content := `## Gates

- build: ` + "`go build ./...`" + `
- test: ` + "`go test ./...`" + `
`
	gates := parseGates(content)
	if len(gates) != 2 {
		t.Fatalf("expected 2 gates (plain format), got %d", len(gates))
	}
	if gates[0].Name != "build" {
		t.Errorf("gate[0].Name = %q, want %q", gates[0].Name, "build")
	}
	if gates[1].Name != "test" {
		t.Errorf("gate[1].Name = %q, want %q", gates[1].Name, "test")
	}
}

func TestReadPromptMD_Success(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, "specs", "my-feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	expected := "# My Feature\n\n## Objective\n\nBuild something.\n"
	if err := os.WriteFile(filepath.Join(specDir, "PROMPT.md"), []byte(expected), 0o644); err != nil {
		t.Fatal(err)
	}

	content, err := readPromptMD(tmpDir, "my-feature")
	if err != nil {
		t.Fatalf("readPromptMD failed: %v", err)
	}
	if content != expected {
		t.Errorf("content = %q, want %q", content, expected)
	}
}

func TestReadPromptMD_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := readPromptMD(tmpDir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing PROMPT.md")
	}
	if !strings.Contains(err.Error(), "PROMPT.md not found") {
		t.Errorf("error should mention 'PROMPT.md not found', got: %v", err)
	}
}

func TestListAvailableSpecs(t *testing.T) {
	tmpDir := t.TempDir()
	specsDir := filepath.Join(tmpDir, "specs")

	// Create spec with PROMPT.md.
	spec1 := filepath.Join(specsDir, "alpha-feature")
	if err := os.MkdirAll(spec1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spec1, "PROMPT.md"), []byte("# Alpha"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create spec with PROMPT.md.
	spec2 := filepath.Join(specsDir, "beta-feature")
	if err := os.MkdirAll(spec2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spec2, "PROMPT.md"), []byte("# Beta"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create spec WITHOUT PROMPT.md (should be excluded).
	spec3 := filepath.Join(specsDir, "gamma-incomplete")
	if err := os.MkdirAll(spec3, 0o755); err != nil {
		t.Fatal(err)
	}

	specs, err := listAvailableSpecs(tmpDir)
	if err != nil {
		t.Fatalf("listAvailableSpecs failed: %v", err)
	}

	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d: %v", len(specs), specs)
	}
	if specs[0] != "alpha-feature" {
		t.Errorf("specs[0] = %q, want %q", specs[0], "alpha-feature")
	}
	if specs[1] != "beta-feature" {
		t.Errorf("specs[1] = %q, want %q", specs[1], "beta-feature")
	}
}

func TestListAvailableSpecs_NoSpecsDir(t *testing.T) {
	tmpDir := t.TempDir()
	specs, err := listAvailableSpecs(tmpDir)
	if err != nil {
		t.Fatalf("listAvailableSpecs failed: %v", err)
	}
	if len(specs) != 0 {
		t.Errorf("expected 0 specs, got %d", len(specs))
	}
}

// --- Step 5 tests: /run Subagent Spawn & Streaming ---

func TestBuildRunPrompt(t *testing.T) {
	promptMD := "# My Feature\n\n## Objective\n\nBuild something.\n"
	result := buildRunPrompt("my-feature", promptMD)

	if !strings.Contains(result, promptMD) {
		t.Error("run prompt should contain the original PROMPT.md content")
	}
	if !strings.Contains(result, "## Execution Instructions") {
		t.Error("run prompt should contain execution instructions")
	}
	if !strings.Contains(result, "specs/my-feature/plan.md") {
		t.Error("run prompt should reference the spec's plan.md")
	}
	if !strings.Contains(result, "- [ ] Step N:") {
		t.Error("run prompt should mention checklist update instructions")
	}
}

func TestHandleRunCommand_NoArgs(t *testing.T) {
	tmpDir := t.TempDir()
	m := &model{
		cfg: Config{
			WorkDir: tmpDir,
		},
		messages: make([]message, 0),
	}

	m.handleRunCommand(nil)

	if len(m.messages) == 0 {
		t.Fatal("expected a usage message")
	}
	last := m.messages[len(m.messages)-1]
	if !strings.Contains(last.content, "Usage:") {
		t.Errorf("expected usage message, got: %s", last.content)
	}
}

func TestHandleRunCommand_NoOrchestrator(t *testing.T) {
	tmpDir := t.TempDir()
	m := &model{
		cfg: Config{
			WorkDir: tmpDir,
		},
		messages: make([]message, 0),
	}

	m.handleRunCommand([]string{"some-spec"})

	if len(m.messages) == 0 {
		t.Fatal("expected error message")
	}
	last := m.messages[len(m.messages)-1]
	if !strings.Contains(last.content, "not available") {
		t.Errorf("expected 'not available' message, got: %s", last.content)
	}
}

func TestHandleRunCommand_MissingSpec(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a different spec to show in available list.
	specDir := filepath.Join(tmpDir, "specs", "existing-spec")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specDir, "PROMPT.md"), []byte("# Existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := &model{
		cfg: Config{
			WorkDir:      tmpDir,
			Orchestrator: &subagent.Orchestrator{},
		},
		messages: make([]message, 0),
	}

	m.handleRunCommand([]string{"nonexistent"})

	if len(m.messages) == 0 {
		t.Fatal("expected error message")
	}
	last := m.messages[len(m.messages)-1]
	if !strings.Contains(last.content, "PROMPT.md not found") {
		t.Errorf("expected 'not found' error, got: %s", last.content)
	}
	if !strings.Contains(last.content, "existing-spec") {
		t.Errorf("expected available specs listed, got: %s", last.content)
	}
}

func TestHandleRunCommand_StreamingEvents(t *testing.T) {
	// Create a fake events channel simulating subagent output.
	events := make(chan subagent.Event, 10)
	events <- subagent.Event{Type: "text_delta", Content: "Hello "}
	events <- subagent.Event{Type: "text_delta", Content: "world"}
	events <- subagent.Event{Type: "tool_call", Content: "bash"}
	events <- subagent.Event{Type: "tool_result", Content: `{"exit_code": 0, "stdout": "ok"}`}
	close(events)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := &model{
		ctx:      ctx,
		cancel:   cancel,
		messages: []message{{role: "assistant", content: ""}},
		run: &runState{
			specName: "test-spec",
			agentID:  "task-123",
			phase:    "running",
			events:   events,
		},
		running: true,
	}

	// Process text_delta events.
	ev1 := runAgentEventMsg{event: subagent.Event{Type: "text_delta", Content: "Hello "}}
	m.handleRunAgentEvent(ev1)
	if m.streaming != "Hello " {
		t.Errorf("streaming = %q, want %q", m.streaming, "Hello ")
	}

	ev2 := runAgentEventMsg{event: subagent.Event{Type: "text_delta", Content: "world"}}
	m.handleRunAgentEvent(ev2)
	if m.streaming != "Hello world" {
		t.Errorf("streaming = %q, want %q", m.streaming, "Hello world")
	}

	// Process tool_call event.
	ev3 := runAgentEventMsg{event: subagent.Event{Type: "tool_call", Content: "bash"}}
	m.handleRunAgentEvent(ev3)
	if m.activeTool != "bash" {
		t.Errorf("activeTool = %q, want %q", m.activeTool, "bash")
	}

	// Process tool_result event.
	ev4 := runAgentEventMsg{event: subagent.Event{Type: "tool_result", Content: `{"exit_code": 0, "stdout": "ok"}`}}
	m.handleRunAgentEvent(ev4)
	if m.activeTool != "" {
		t.Errorf("activeTool should be cleared after result, got %q", m.activeTool)
	}

	// Process done — with no gates defined, it transitions to merging.
	m.handleRunAgentDone()
	if m.running {
		t.Error("model should not be running after done")
	}
	// With no gates and no orchestrator/worktree manager, phase goes to "merging"
	// because handleRunAgentDone skips gates and attempts merge.
	if m.run.phase != "merging" {
		t.Errorf("run phase = %q, want %q", m.run.phase, "merging")
	}
}

func TestHandleRunCommand_NoArgsShowsAvailableSpecs(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, "specs", "my-feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specDir, "PROMPT.md"), []byte("# Test"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := &model{
		cfg:      Config{WorkDir: tmpDir},
		messages: make([]message, 0),
	}

	m.handleRunCommand(nil)

	last := m.messages[len(m.messages)-1]
	if !strings.Contains(last.content, "my-feature") {
		t.Errorf("expected spec name in output, got: %s", last.content)
	}
}

// --- Step 6 tests: Gate Validation & Auto-merge ---

func TestRunGates_AllPass(t *testing.T) {
	gates := []Gate{
		{Name: "echo", Command: "echo hello"},
		{Name: "true", Command: "true"},
	}

	result := runGates(context.Background(), t.TempDir(), gates)

	if !result.passed {
		t.Error("expected all gates to pass")
	}
	if len(result.results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result.results))
	}
	for i, r := range result.results {
		if !r.Passed {
			t.Errorf("gate[%d] %q should have passed", i, r.Name)
		}
	}
	// First gate should have "hello" in output.
	if !strings.Contains(result.results[0].Output, "hello") {
		t.Errorf("gate[0] output should contain 'hello', got: %q", result.results[0].Output)
	}
}

func TestRunGates_BuildFails(t *testing.T) {
	gates := []Gate{
		{Name: "build", Command: "false"},
		{Name: "test", Command: "true"},
	}

	result := runGates(context.Background(), t.TempDir(), gates)

	if result.passed {
		t.Error("expected gates to fail")
	}
	// Should stop at first failure.
	if len(result.results) != 1 {
		t.Fatalf("expected 1 result (stops at first failure), got %d", len(result.results))
	}
	if result.results[0].Passed {
		t.Error("build gate should have failed")
	}
}

func TestRunGates_TestFails(t *testing.T) {
	gates := []Gate{
		{Name: "build", Command: "true"},
		{Name: "test", Command: "false"},
	}

	result := runGates(context.Background(), t.TempDir(), gates)

	if result.passed {
		t.Error("expected gates to fail")
	}
	if len(result.results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result.results))
	}
	if !result.results[0].Passed {
		t.Error("build gate should have passed")
	}
	if result.results[1].Passed {
		t.Error("test gate should have failed")
	}
}

func TestRunGates_NoGates(t *testing.T) {
	result := runGates(context.Background(), t.TempDir(), nil)

	if !result.passed {
		t.Error("expected pass with no gates")
	}
	if len(result.results) != 0 {
		t.Errorf("expected 0 results, got %d", len(result.results))
	}
}

func TestRunGates_CapturesOutput(t *testing.T) {
	gates := []Gate{
		{Name: "output", Command: "echo stdout-text && echo stderr-text >&2"},
	}

	result := runGates(context.Background(), t.TempDir(), gates)

	if !result.passed {
		t.Error("expected gate to pass")
	}
	if !strings.Contains(result.results[0].Output, "stdout-text") {
		t.Errorf("expected stdout captured, got: %q", result.results[0].Output)
	}
	if !strings.Contains(result.results[0].Output, "stderr-text") {
		t.Errorf("expected stderr captured, got: %q", result.results[0].Output)
	}
}

func TestHandleRunGateResult_AllPass(t *testing.T) {
	m := &model{
		cfg: Config{
			Orchestrator: subagent.NewOrchestrator(&config.Config{}, "", nil),
		},
		messages: make([]message, 0),
		run: &runState{
			specName: "test-spec",
			agentID:  "task-123",
			phase:    "gating",
		},
	}

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

	// Should have gate results and merge message.
	found := false
	for _, msg := range m.messages {
		if strings.Contains(msg.content, "All gates passed") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'All gates passed' message")
	}
}

func TestHandleRunGateResult_Failure_MaxRetries(t *testing.T) {
	m := &model{
		cfg: Config{
			Orchestrator: subagent.NewOrchestrator(&config.Config{}, "", nil),
		},
		messages: make([]message, 0),
		run: &runState{
			specName:   "test-spec",
			agentID:    "task-123",
			phase:      "gating",
			retries:    3,
			maxRetries: 3,
		},
	}

	msg := runGateResultMsg{
		results: []GateResult{
			{Name: "build", Command: "go build ./...", Passed: true},
			{Name: "test", Command: "go test ./...", Passed: false, Output: "FAIL pkg/foo"},
		},
		passed: false,
	}

	m.handleRunGateResult(msg)

	if m.run.phase != "failed" {
		t.Errorf("phase = %q, want %q", m.run.phase, "failed")
	}

	if m.run.gateOutput == "" {
		t.Error("expected gateOutput to be set")
	}

	found := false
	for _, msg := range m.messages {
		if strings.Contains(msg.content, "Gate validation failed") && strings.Contains(msg.content, "after 3 retries") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'Gate validation failed after 3 retries' message")
	}
}

func TestHandleRunMergeResult_Success(t *testing.T) {
	m := &model{
		messages: make([]message, 0),
		run: &runState{
			specName: "test-spec",
			agentID:  "task-123",
			phase:    "merging",
		},
	}

	msg := runMergeResultMsg{output: "Merge made by 'ort' strategy."}
	m.handleRunMergeResult(msg)

	if m.run.phase != "done" {
		t.Errorf("phase = %q, want %q", m.run.phase, "done")
	}

	found := false
	for _, msg := range m.messages {
		if strings.Contains(msg.content, "merged successfully") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'merged successfully' message")
	}
}

func TestHandleRunMergeResult_Conflict(t *testing.T) {
	m := &model{
		cfg: Config{
			Orchestrator: subagent.NewOrchestrator(&config.Config{}, "", nil),
		},
		messages: make([]message, 0),
		run: &runState{
			specName: "test-spec",
			agentID:  "task-123",
			phase:    "merging",
		},
	}

	msg := runMergeResultMsg{
		output: "CONFLICT (content): Merge conflict in foo.go",
		err:    fmt.Errorf("merge failed: exit status 1"),
	}
	m.handleRunMergeResult(msg)

	if m.run.phase != "failed" {
		t.Errorf("phase = %q, want %q", m.run.phase, "failed")
	}

	found := false
	for _, msg := range m.messages {
		if strings.Contains(msg.content, "Merge failed") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'Merge failed' message")
	}
}

func TestHandleRunAgentDone_NoGatesSkipsToMerge(t *testing.T) {
	m := &model{
		messages: make([]message, 0),
		running:  true,
		run: &runState{
			specName: "test-spec",
			agentID:  "task-123",
			phase:    "running",
			gates:    nil, // no gates
		},
	}

	m.handleRunAgentDone()

	if m.run.phase != "merging" {
		t.Errorf("phase = %q, want %q (should skip to merge with no gates)", m.run.phase, "merging")
	}

	found := false
	for _, msg := range m.messages {
		if strings.Contains(msg.content, "No gates defined") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'No gates defined' message")
	}
}

func TestHandleRunAgentDone_WithGatesTriggersGating(t *testing.T) {
	m := &model{
		cfg: Config{
			Orchestrator: subagent.NewOrchestrator(&config.Config{}, "", nil),
		},
		messages: make([]message, 0),
		running:  true,
		run: &runState{
			specName: "test-spec",
			agentID:  "task-123",
			phase:    "running",
			gates: []Gate{
				{Name: "build", Command: "go build ./..."},
			},
		},
	}

	m.handleRunAgentDone()

	if m.run.phase != "gating" {
		t.Errorf("phase = %q, want %q", m.run.phase, "gating")
	}
}

func TestFormatGateFailures(t *testing.T) {
	results := []GateResult{
		{Name: "build", Command: "go build ./...", Passed: true, Output: "ok"},
		{Name: "test", Command: "go test ./...", Passed: false, Output: "FAIL pkg/foo\nTest failed"},
	}

	output := formatGateFailures(results)

	if strings.Contains(output, "build") && strings.Contains(output, "ok") {
		t.Error("passed gates should not be included in failure output")
	}
	if !strings.Contains(output, "test") {
		t.Error("failed gate name should be in output")
	}
	if !strings.Contains(output, "FAIL pkg/foo") {
		t.Error("failed gate output should be included")
	}
}

// --- Step 7 tests: Retry Logic on Gate Failure ---

func TestBuildRetryPrompt_IncludesGateOutput(t *testing.T) {
	promptMD := "# My Feature\n\n## Objective\n\nBuild something.\n"
	gateOutput := "Gate `test` (`go test ./...`) FAILED:\nFAIL pkg/foo\n\n"

	result := buildRetryPrompt("my-feature", promptMD, gateOutput)

	if !strings.Contains(result, "failed gate validation") {
		t.Error("retry prompt should mention gate validation failure")
	}
	if !strings.Contains(result, "## Gate Failures") {
		t.Error("retry prompt should contain gate failures section")
	}
	if !strings.Contains(result, "FAIL pkg/foo") {
		t.Error("retry prompt should include gate failure output")
	}
	if !strings.Contains(result, promptMD) {
		t.Error("retry prompt should include original PROMPT.md")
	}
	if !strings.Contains(result, "specs/my-feature/plan.md") {
		t.Error("retry prompt should reference the spec's plan.md")
	}
	if !strings.Contains(result, "Fix the issues") {
		t.Error("retry prompt should include fix instructions")
	}
}

func TestRetryOnGateFailure_FirstRetry_SpawnFails(t *testing.T) {
	// When spawn fails during retry, phase should be "failed" and retry counter should increment.
	// We use a real orchestrator with empty config — Spawn will fail on role resolution.
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := &model{
		ctx: ctx,
		cfg: Config{
			Orchestrator: orch,
		},
		messages: make([]message, 0),
		run: &runState{
			specName:   "test-spec",
			promptMD:   "# Test\n\n## Objective\nDo stuff.\n",
			agentID:    "task-123",
			phase:      "gating",
			retries:    0,
			maxRetries: 3,
		},
	}

	msg := runGateResultMsg{
		results: []GateResult{
			{Name: "test", Command: "go test ./...", Passed: false, Output: "FAIL pkg/foo"},
		},
		passed: false,
	}

	m.handleRunGateResult(msg)

	// Retry counter should increment even if spawn fails.
	if m.run.retries != 1 {
		t.Errorf("retries = %d, want 1", m.run.retries)
	}

	// Phase should be "failed" because spawn failed.
	if m.run.phase != "failed" {
		t.Errorf("phase = %q, want %q", m.run.phase, "failed")
	}

	// Should have a "Failed to spawn retry agent" message.
	found := false
	for _, msg := range m.messages {
		if strings.Contains(msg.content, "Failed to spawn retry agent") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'Failed to spawn retry agent' message")
	}
}

func TestRetryOnGateFailure_RetryCountIncrement(t *testing.T) {
	// Verify retry count increments and gateOutput updates on each failure.
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := &model{
		ctx: ctx,
		cfg: Config{
			Orchestrator: orch,
		},
		messages: make([]message, 0),
		run: &runState{
			specName:   "test-spec",
			promptMD:   "# Test\n",
			agentID:    "task-retry-1",
			phase:      "gating",
			retries:    1,
			maxRetries: 3,
			gateOutput: "previous failure",
		},
	}

	msg := runGateResultMsg{
		results: []GateResult{
			{Name: "test", Command: "go test ./...", Passed: false, Output: "FAIL pkg/bar (second attempt)"},
		},
		passed: false,
	}

	m.handleRunGateResult(msg)

	if m.run.retries != 2 {
		t.Errorf("retries = %d, want 2", m.run.retries)
	}

	// The gate output should contain the LATEST failure.
	if !strings.Contains(m.run.gateOutput, "FAIL pkg/bar") {
		t.Error("gateOutput should contain latest failure output")
	}
}

func TestRetryOnGateFailure_MaxRetries_Exhausted(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)

	m := &model{
		cfg: Config{
			Orchestrator: orch,
		},
		messages: make([]message, 0),
		run: &runState{
			specName:   "test-spec",
			promptMD:   "# Test\n",
			agentID:    "task-retry-3",
			phase:      "gating",
			retries:    3, // already at max
			maxRetries: 3,
		},
	}

	msg := runGateResultMsg{
		results: []GateResult{
			{Name: "test", Command: "go test ./...", Passed: false, Output: "FAIL"},
		},
		passed: false,
	}

	m.handleRunGateResult(msg)

	if m.run.phase != "failed" {
		t.Errorf("phase = %q, want %q", m.run.phase, "failed")
	}

	// Retry counter should NOT increment beyond max.
	if m.run.retries != 3 {
		t.Errorf("retries = %d, want 3 (should not increment beyond max)", m.run.retries)
	}

	found := false
	for _, msg := range m.messages {
		if strings.Contains(msg.content, "after 3 retries") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected message mentioning max retries exhausted")
	}
}
