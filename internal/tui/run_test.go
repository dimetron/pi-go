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

func TestListAvailableSpecs_Nested(t *testing.T) {
	tmpDir := t.TempDir()
	specsDir := filepath.Join(tmpDir, "specs")

	// Flat spec.
	flat := filepath.Join(specsDir, "flat-spec")
	if err := os.MkdirAll(flat, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(flat, "PROMPT.md"), []byte("# Flat"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Nested spec under a focus area subfolder.
	nested := filepath.Join(specsDir, "skills", "skills-audit")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "PROMPT.md"), []byte("# Nested"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Subfolder without PROMPT.md (should be excluded).
	noPrompt := filepath.Join(specsDir, "skills", "no-prompt")
	if err := os.MkdirAll(noPrompt, 0o755); err != nil {
		t.Fatal(err)
	}

	specs, err := listAvailableSpecs(tmpDir)
	if err != nil {
		t.Fatalf("listAvailableSpecs failed: %v", err)
	}

	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d: %v", len(specs), specs)
	}
	if specs[0] != "flat-spec" {
		t.Errorf("specs[0] = %q, want %q", specs[0], "flat-spec")
	}
	expected := filepath.Join("skills", "skills-audit")
	if specs[1] != expected {
		t.Errorf("specs[1] = %q, want %q", specs[1], expected)
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
	result := buildRunPrompt("my-feature", promptMD, nil)

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

func TestBuildRunPrompt_InjectsChecklist(t *testing.T) {
	promptMD := "# My Feature\n\n## Objective\n\nBuild something.\n"

	// Heading-style checklist (no checkboxes) — should inject checkbox list.
	headingSteps := []ChecklistStep{
		{Title: "PairingManager — Core Logic", Done: false},
		{Title: "HTTP Handlers — Endpoints", Done: false},
	}
	result := buildRunPrompt("my-feature", promptMD, headingSteps)

	if !strings.Contains(result, "- [ ] Step 1: PairingManager") {
		t.Error("should inject checklist for heading-only plans")
	}
	if !strings.Contains(result, "- [ ] Step 2: HTTP Handlers") {
		t.Error("should inject all steps into checklist")
	}
	if !strings.Contains(result, "FIRST action") {
		t.Error("should instruct agent to prepend checklist")
	}
}

func TestBuildRunPrompt_NoInjectionForCheckboxPlans(t *testing.T) {
	promptMD := "# My Feature\n"

	// Checkbox-style checklist (already has checkboxes) — should NOT inject.
	checkboxSteps := []ChecklistStep{
		{Title: "Step 1: Setup", Done: true},
		{Title: "Step 2: Build", Done: false},
	}
	result := buildRunPrompt("my-feature", promptMD, checkboxSteps)

	if strings.Contains(result, "FIRST action") {
		t.Error("should NOT inject checklist when plan already has checkboxes")
	}
}

func TestHandleRunCommand_NoArgs(t *testing.T) {
	tmpDir := t.TempDir()
	m := &model{
		cfg: Config{
			WorkDir: tmpDir,
		},
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	m.handleRunCommand(nil)

	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected a usage message")
	}
	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
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
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	m.handleRunCommand([]string{"some-spec"})

	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected error message")
	}
	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
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
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	m.handleRunCommand([]string{"nonexistent"})

	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected error message")
	}
	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
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
		ctx:       ctx,
		cancel:    cancel,
		chatModel: ChatModel{Messages: []message{{role: "assistant", content: ""}}},
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
	if m.chatModel.Streaming != "Hello " {
		t.Errorf("streaming = %q, want %q", m.chatModel.Streaming, "Hello ")
	}

	ev2 := runAgentEventMsg{event: subagent.Event{Type: "text_delta", Content: "world"}}
	m.handleRunAgentEvent(ev2)
	if m.chatModel.Streaming != "Hello world" {
		t.Errorf("streaming = %q, want %q", m.chatModel.Streaming, "Hello world")
	}

	// Process tool_call event.
	ev3 := runAgentEventMsg{event: subagent.Event{Type: "tool_call", Content: "bash"}}
	m.handleRunAgentEvent(ev3)
	if m.statusModel.ActiveTool != "bash" {
		t.Errorf("activeTool = %q, want %q", m.statusModel.ActiveTool, "bash")
	}

	// Process tool_result event.
	ev4 := runAgentEventMsg{event: subagent.Event{Type: "tool_result", Content: `{"exit_code": 0, "stdout": "ok"}`}}
	m.handleRunAgentEvent(ev4)
	if m.statusModel.ActiveTool != "" {
		t.Errorf("activeTool should be cleared after result, got %q", m.statusModel.ActiveTool)
	}

	// Process done — with no gates defined, it transitions to merging.
	m.handleRunAgentDone(runAgentDoneMsg{})
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
		cfg:       Config{WorkDir: tmpDir},
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	m.handleRunCommand(nil)

	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
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
		chatModel: ChatModel{Messages: make([]message, 0)},
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
	for _, msg := range m.chatModel.Messages {
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
		chatModel: ChatModel{Messages: make([]message, 0)},
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
	for _, msg := range m.chatModel.Messages {
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
		chatModel: ChatModel{Messages: make([]message, 0)},
		run: &runState{
			specName: "test-spec",
			agentID:  "task-123",
			phase:    "merging",
			checklist: []ChecklistStep{
				{Title: "Step 1", Done: false},
				{Title: "Step 2", Done: false},
				{Title: "Step 3", Done: true},
			},
		},
	}

	msg := runMergeResultMsg{output: "Merge made by 'ort' strategy."}
	m.handleRunMergeResult(msg)

	if m.run.phase != "done" {
		t.Errorf("phase = %q, want %q", m.run.phase, "done")
	}

	// All checklist items should be marked done after successful merge.
	for i, step := range m.run.checklist {
		if !step.Done {
			t.Errorf("checklist[%d] should be done after merge", i)
		}
	}

	found := false
	for _, msg := range m.chatModel.Messages {
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
		chatModel: ChatModel{Messages: make([]message, 0)},
		run: &runState{
			specName: "test-spec",
			agentID:  "task-123",
			phase:    "merging",
		},
	}

	msg := runMergeResultMsg{
		output:          "CONFLICT (content): Merge conflict in foo.go",
		err:             fmt.Errorf("merge failed: exit status 1"),
		failedAgentID:   "task-456",
		preservedWTPath: "/tmp/pi-go/worktrees/task-456",
	}
	m.handleRunMergeResult(msg)

	if m.run.phase != "failed" {
		t.Errorf("phase = %q, want %q", m.run.phase, "failed")
	}

	found := false
	for _, msg := range m.chatModel.Messages {
		if strings.Contains(msg.content, "Merge failed") && strings.Contains(msg.content, "/tmp/pi-go/worktrees/task-456") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected merge failure message with preserved worktree path")
	}
}

func TestHandleRunAgentDone_NoGatesSkipsToMerge(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		running:   true,
		run: &runState{
			specName: "test-spec",
			agentID:  "task-123",
			phase:    "running",
			gates:    nil, // no gates
		},
	}

	m.handleRunAgentDone(runAgentDoneMsg{})

	if m.run.phase != "merging" {
		t.Errorf("phase = %q, want %q (should skip to merge with no gates)", m.run.phase, "merging")
	}

	found := false
	for _, msg := range m.chatModel.Messages {
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
		chatModel: ChatModel{Messages: make([]message, 0)},
		running:   true,
		run: &runState{
			specName: "test-spec",
			agentID:  "task-123",
			phase:    "running",
			gates: []Gate{
				{Name: "build", Command: "go build ./..."},
			},
		},
	}

	m.handleRunAgentDone(runAgentDoneMsg{})

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
		chatModel: ChatModel{Messages: make([]message, 0)},
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
	for _, msg := range m.chatModel.Messages {
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
		chatModel: ChatModel{Messages: make([]message, 0)},
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

// --- Checklist parsing tests ---

func TestExtractChecklist_CheckboxFormat(t *testing.T) {
	content := `# Plan

## Checklist

- [x] Step 1: Setup project
- [ ] Step 2: Implement feature
- [X] Step 3: Write tests
- [ ] Step 4: Review

## Notes
`
	steps := extractChecklist(content)
	if len(steps) != 4 {
		t.Fatalf("expected 4 steps, got %d", len(steps))
	}

	want := []ChecklistStep{
		{Title: "Step 1: Setup project", Done: true},
		{Title: "Step 2: Implement feature", Done: false},
		{Title: "Step 3: Write tests", Done: true},
		{Title: "Step 4: Review", Done: false},
	}
	for i, w := range want {
		if steps[i].Title != w.Title {
			t.Errorf("step[%d].Title = %q, want %q", i, steps[i].Title, w.Title)
		}
		if steps[i].Done != w.Done {
			t.Errorf("step[%d].Done = %v, want %v", i, steps[i].Done, w.Done)
		}
	}
}

func TestExtractChecklist_SliceHeadings(t *testing.T) {
	content := `# Implementation Plan

### Slice 1: PairingManager

Details...

### Slice 2: HTTP Handlers

Details...

### Slice 3: WebSocket Server

Details...
`
	steps := extractChecklist(content)
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}
	if steps[0].Title != "PairingManager" {
		t.Errorf("step[0].Title = %q, want %q", steps[0].Title, "PairingManager")
	}
	if steps[1].Done {
		t.Error("slice heading steps should default to not done")
	}
}

func TestExtractChecklist_LongTitleTruncated(t *testing.T) {
	content := "- [ ] This is a very long step title that should be truncated because it exceeds forty characters\n"
	steps := extractChecklist(content)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if len(steps[0].Title) > 40 {
		t.Errorf("title should be truncated, got len=%d: %q", len(steps[0].Title), steps[0].Title)
	}
	if !strings.HasSuffix(steps[0].Title, "...") {
		t.Errorf("truncated title should end with '...', got: %q", steps[0].Title)
	}
}

func TestExtractChecklist_EmptyContent(t *testing.T) {
	steps := extractChecklist("")
	if len(steps) != 0 {
		t.Errorf("expected 0 steps for empty content, got %d", len(steps))
	}
}

func TestParsePlanChecklist_FromFile(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "test-spec")
	os.MkdirAll(specDir, 0o755)

	planContent := `# Plan

- [x] Step 1: Done
- [ ] Step 2: Not done
`
	os.WriteFile(filepath.Join(specDir, "plan.md"), []byte(planContent), 0o644)

	steps := parsePlanChecklist(dir, "test-spec")
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	if !steps[0].Done {
		t.Error("step 1 should be done")
	}
	if steps[1].Done {
		t.Error("step 2 should not be done")
	}
}

func TestParsePlanChecklistFrom(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "test-spec")
	os.MkdirAll(specDir, 0o755)

	plan := `# Plan

- [x] Step 1: Done
- [ ] Step 2: Pending
- [x] Step 3: Also done
`
	os.WriteFile(filepath.Join(specDir, "plan.md"), []byte(plan), 0o644)

	steps := parsePlanChecklistFrom(dir, "test-spec")
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}
	if !steps[0].Done || steps[1].Done || !steps[2].Done {
		t.Error("unexpected done states")
	}
}

func TestParsePlanChecklistFrom_Missing(t *testing.T) {
	steps := parsePlanChecklistFrom(t.TempDir(), "nonexistent")
	if len(steps) != 0 {
		t.Errorf("expected 0 steps for missing plan, got %d", len(steps))
	}
}

func TestRefreshRunChecklist_NilRunState(t *testing.T) {
	m := &model{}
	m.refreshRunChecklist() // should not panic
}

func TestRefreshRunChecklist_NoOrchestrator(t *testing.T) {
	m := &model{
		run: &runState{specName: "test", agentID: "a-1"},
	}
	m.refreshRunChecklist() // should not panic
}

func TestRefreshRunChecklist_NoWorktreeManager(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	m := &model{
		cfg: Config{Orchestrator: orch},
		run: &runState{specName: "test", agentID: "a-1"},
	}
	m.refreshRunChecklist() // worktree manager is nil, should not panic
}

func TestRefreshRunChecklist_UpdatesChecklist(t *testing.T) {
	// Create a fake worktree directory with a plan.md.
	wtDir := t.TempDir()
	specDir := filepath.Join(wtDir, "specs", "my-spec")
	os.MkdirAll(specDir, 0o755)
	os.WriteFile(filepath.Join(specDir, "plan.md"), []byte("- [x] Step 1: Done\n- [ ] Step 2: TODO\n"), 0o644)

	// We can't easily inject a real WorktreeManager, but we can test
	// parsePlanChecklistFrom directly (which refreshRunChecklist delegates to).
	steps := parsePlanChecklistFrom(wtDir, "my-spec")
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	if !steps[0].Done {
		t.Error("step 1 should be done")
	}
	if steps[1].Done {
		t.Error("step 2 should not be done")
	}
}

func TestChecklistHasCheckboxes(t *testing.T) {
	t.Run("has checked items", func(t *testing.T) {
		steps := []ChecklistStep{
			{Title: "Step 1: Setup", Done: true},
			{Title: "Step 2: Build", Done: false},
		}
		if !checklistHasCheckboxes(steps) {
			t.Error("should detect checkboxes when Done=true exists")
		}
	})

	t.Run("heading style with dashes", func(t *testing.T) {
		steps := []ChecklistStep{
			{Title: "PairingManager — Core Logic", Done: false},
			{Title: "HTTP Handlers — Endpoints", Done: false},
		}
		if checklistHasCheckboxes(steps) {
			t.Error("should detect heading-only format from em-dashes")
		}
	})

	t.Run("all unchecked checkbox style", func(t *testing.T) {
		steps := []ChecklistStep{
			{Title: "Step 1: Setup", Done: false},
			{Title: "Step 2: Build", Done: false},
		}
		if !checklistHasCheckboxes(steps) {
			t.Error("should assume checkbox format when no em-dashes")
		}
	})

	t.Run("heading style with en-dash", func(t *testing.T) {
		steps := []ChecklistStep{
			{Title: "Manager – Core", Done: false},
		}
		if checklistHasCheckboxes(steps) {
			t.Error("should detect heading format from en-dash")
		}
	})
}

func TestHandleRunAgentEvent_RefreshesChecklistOnEdit(t *testing.T) {
	// Verify that tool_result after write/edit triggers checklist refresh attempt.
	m := &model{
		chatModel:   ChatModel{Messages: make([]message, 0)},
		statusModel: StatusModel{ActiveTool: "edit"},
		running:     true,
		run: &runState{
			specName: "test-spec",
			agentID:  "task-123",
			phase:    "running",
			checklist: []ChecklistStep{
				{Title: "Step 1", Done: false},
			},
		},
	}

	// Add a tool message placeholder (as tool_call would).
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "tool", tool: "edit"})

	// Simulate tool_result event — this should attempt refresh (won't crash
	// even without orchestrator because refreshRunChecklist checks nil).
	ev := runAgentEventMsg{event: subagent.Event{Type: "tool_result", Content: `{"ok": true}`}}
	m.handleRunAgentEvent(ev)

	if m.statusModel.ActiveTool != "" {
		t.Error("active tool should be cleared after tool_result")
	}
}

func TestRetryOnGateFailure_MaxRetries_Exhausted(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)

	m := &model{
		cfg: Config{
			Orchestrator: orch,
		},
		chatModel: ChatModel{Messages: make([]message, 0)},
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
	for _, msg := range m.chatModel.Messages {
		if strings.Contains(msg.content, "after 3 retries") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected message mentioning max retries exhausted")
	}
}

// --- Parallel /run tests ---

func TestRunState_IsParallel(t *testing.T) {
	rs := &runState{}
	if rs.isParallel() {
		t.Error("empty parallel slice should not be parallel")
	}

	rs.parallel = []*parallelAgent{
		{agentID: "a-1"},
		{agentID: "a-2"},
	}
	if !rs.isParallel() {
		t.Error("should be parallel with 2 agents")
	}
}

func TestRunState_AllAgentsDone(t *testing.T) {
	rs := &runState{
		parallel: []*parallelAgent{
			{agentID: "a-1", done: false},
			{agentID: "a-2", done: true},
		},
	}
	if rs.allAgentsDone() {
		t.Error("should not be done when agent 1 is still running")
	}

	rs.parallel[0].done = true
	if !rs.allAgentsDone() {
		t.Error("should be done when all agents are done")
	}
}

func TestHandleRunAgentDone_ParallelPartialDone(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		running:   true,
		run: &runState{
			specName: "test-spec",
			phase:    "running",
			parallel: []*parallelAgent{
				{agentID: "a-1", done: false},
				{agentID: "a-2", done: false},
			},
		},
	}

	// Agent 1 finishes.
	m.handleRunAgentDone(runAgentDoneMsg{agentID: "a-1"})

	if !m.run.parallel[0].done {
		t.Error("agent 1 should be marked done")
	}
	if m.run.parallel[1].done {
		t.Error("agent 2 should still be running")
	}
	// Model should still be running (waiting for agent 2).
	if m.run.phase != "running" {
		t.Errorf("phase should still be running, got %q", m.run.phase)
	}
}

func TestHandleRunAgentDone_ParallelAllDone(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		running:   true,
		run: &runState{
			specName: "test-spec",
			phase:    "running",
			parallel: []*parallelAgent{
				{agentID: "a-1", done: true}, // already done
				{agentID: "a-2", done: false},
			},
		},
	}

	// Agent 2 finishes — now all done.
	m.handleRunAgentDone(runAgentDoneMsg{agentID: "a-2"})

	if !m.run.allAgentsDone() {
		t.Error("all agents should be done")
	}
	// With no gates, should transition to merging.
	if m.run.phase != "merging" {
		t.Errorf("phase = %q, want merging (no gates)", m.run.phase)
	}
}

func TestBuildParallelPrompt(t *testing.T) {
	promptMD := "# Feature\n\n## Objective\nBuild it.\n"
	checklist := []ChecklistStep{
		{Title: "Core — Logic"},
		{Title: "HTTP — Handlers"},
		{Title: "Tests — Integration"},
		{Title: "CLI — Command"},
	}

	prompt := buildParallelPrompt("my-spec", promptMD, checklist, 0, 2)

	if !strings.Contains(prompt, "Parallel Mode") {
		t.Error("should mention parallel mode")
	}
	if !strings.Contains(prompt, "Slice 1: Core") {
		t.Error("should include slice 1")
	}
	if !strings.Contains(prompt, "Slice 2: HTTP") {
		t.Error("should include slice 2")
	}
	if strings.Contains(prompt, "Slice 3") {
		t.Error("should NOT include slice 3 (assigned to other agent)")
	}
	if !strings.Contains(prompt, "ONE OF TWO agents") {
		t.Error("should explain parallel assignment")
	}
}

func TestHandleRunCommand_ParallelFlag(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, "specs", "test-spec")
	os.MkdirAll(specDir, 0o755)
	os.WriteFile(filepath.Join(specDir, "PROMPT.md"), []byte("# Test\n\n## Objective\nBuild.\n"), 0o644)
	os.WriteFile(filepath.Join(specDir, "plan.md"), []byte(
		"- [ ] Step 1: A\n- [ ] Step 2: B\n- [ ] Step 3: C\n- [ ] Step 4: D\n"), 0o644)

	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)

	m := &model{
		ctx:       context.Background(),
		cfg:       Config{WorkDir: tmpDir, Orchestrator: orch},
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	// With --parallel flag, spawn will fail (no task agent config) but
	// we can verify the parallel path was taken.
	m.handleRunCommand([]string{"test-spec", "--parallel"})

	found := false
	for _, msg := range m.chatModel.Messages {
		if strings.Contains(msg.content, "Failed to spawn") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected spawn failure message (parallel path was taken)")
	}
}

func TestRunWorktreeName(t *testing.T) {
	if got := runWorktreeName("tools/004-acp-subagent", ""); got != "tools-004-acp-subagent" {
		t.Fatalf("runWorktreeName() = %q, want %q", got, "tools-004-acp-subagent")
	}
	if got := runWorktreeName("features/SUB/skills-subagents", "part-2"); got != "features-SUB-skills-subagents-part-2" {
		t.Fatalf("runWorktreeName() with suffix = %q", got)
	}
}
