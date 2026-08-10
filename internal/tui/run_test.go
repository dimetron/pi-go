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
	specDir := filepath.Join(tmpDir, "specs", "features", "alpha-feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specDir, "PROMPT.md"), []byte("# Alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	if !strings.Contains(last.content, "| Feature | Run command |") {
		t.Errorf("expected available features table, got: %s", last.content)
	}
	if !strings.Contains(last.content, "| `alpha-feature` | `/run features/alpha-feature` |") {
		t.Errorf("expected feature run row, got: %s", last.content)
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
	if !strings.Contains(last.content, "| Feature | Run command |") {
		t.Errorf("expected available features table, got: %s", last.content)
	}
	if !strings.Contains(last.content, "| `existing-spec` | `/run existing-spec` |") {
		t.Errorf("expected existing spec table row, got: %s", last.content)
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

// TestHandleRunAgentEvent_ToolCall_PopulatesToolIn verifies that the tool_call
// handler in run mode extracts toolIn from ev.ToolArgs (the subagent args
// plumbed from the JSONL stream). Before the fix, run.go:454 only set
// `tool: ev.Content`, so the chat card showed a bare "bash" with no command
// — the parent path (agent_loop.go) already filled toolIn; this pins the
// run path to the same behavior.
func TestHandleRunAgentEvent_ToolCall_PopulatesToolIn(t *testing.T) {
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
		},
		running: true,
	}

	// read with a file_path arg.
	m.handleRunAgentEvent(runAgentEventMsg{
		event: subagent.Event{
			Type:     "tool_call",
			Content:  "read",
			ToolArgs: map[string]any{"file_path": "internal/tui/run.go"},
		},
	})
	if got := m.chatModel.Messages[len(m.chatModel.Messages)-1]; got.tool != "read" || got.toolIn != "internal/tui/run.go" {
		t.Errorf("read tool message: tool=%q toolIn=%q, want tool=read toolIn=internal/tui/run.go", got.tool, got.toolIn)
	}

	// bash with a command arg (long enough to be truncated by toolCallSummary).
	longCmd := "go test ./internal/tui/... -run TestHandleRunAgentEvent -v -count=1"
	m.handleRunAgentEvent(runAgentEventMsg{
		event: subagent.Event{
			Type:     "tool_call",
			Content:  "bash",
			ToolArgs: map[string]any{"command": longCmd},
		},
	})
	got := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if got.tool != "bash" {
		t.Errorf("bash tool message: tool=%q, want bash", got.tool)
	}
	// toolCallSummary preserves the full command; the renderer clips to width.
	if got.toolIn != longCmd {
		t.Errorf("bash toolIn should preserve the full command, got %d chars: %q", len(got.toolIn), got.toolIn)
	}
	if got.toolIn == "" {
		t.Error("bash toolIn should be populated from args.command")
	}

	// tool_call with no args should still create a message with empty toolIn.
	m.handleRunAgentEvent(runAgentEventMsg{
		event: subagent.Event{Type: "tool_call", Content: "bash"},
	})
	got = m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if got.tool != "bash" || got.toolIn != "" {
		t.Errorf("bash tool message (no args): tool=%q toolIn=%q, want tool=bash toolIn=empty", got.tool, got.toolIn)
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
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	orch.SetStatusForTest("task-123", "completed")
	m := &model{
		cfg: Config{
			Orchestrator: orch,
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

func TestBuildResumePrompt_IncludesGateOutput(t *testing.T) {
	promptMD := "# My Feature\n\n## Objective\n\nBuild something.\n"
	gateOutput := "Gate `test` (`go test ./...`) FAILED:\nFAIL pkg/foo\n\n"

	result := buildResumePrompt("my-feature", promptMD, "Gate failed", gateOutput)

	if !strings.Contains(result, "Gate failed") {
		t.Error("resume prompt should name the reason the cycle stopped")
	}
	if !strings.Contains(result, "## State") {
		t.Error("resume prompt should contain the carried-over state section")
	}
	if !strings.Contains(result, "FAIL pkg/foo") {
		t.Error("resume prompt should include gate failure output")
	}
	if !strings.Contains(result, promptMD) {
		t.Error("resume prompt should include original PROMPT.md")
	}
	if !strings.Contains(result, "specs/my-feature/plan.md") {
		t.Error("resume prompt should reference the spec's plan.md")
	}
	if !strings.Contains(result, "## Resume Instructions") {
		t.Error("resume prompt should include resume instructions")
	}
}

// A resume prompt with no carried state must not emit an empty State section.
func TestBuildResumePrompt_NoStateSection(t *testing.T) {
	result := buildResumePrompt("my-feature", "# My Feature\n", "agent exited with non-zero status", "")

	if strings.Contains(result, "## State") {
		t.Error("resume prompt should omit the State section when there is no carried state")
	}
	if !strings.Contains(result, "agent exited with non-zero status") {
		t.Error("resume prompt should still name the reason")
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
	if !strings.Contains(prompt, "ONE OF TWO coordinators") {
		t.Error("should explain parallel assignment")
	}
	if !strings.Contains(prompt, "Your Role: Coordinator") {
		t.Error("parallel prompt should carry the coordinator contract")
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
	if got := runWorktreeName("features/TOO/004-acp-subagent", ""); got != "features-TOO-004-acp-subagent" {
		t.Fatalf("runWorktreeName() = %q, want %q", got, "features-TOO-004-acp-subagent")
	}
	if got := runWorktreeName("features/SUB/skills-subagents", "part-2"); got != "features-SUB-skills-subagents-part-2" {
		t.Fatalf("runWorktreeName() with suffix = %q", got)
	}
}

// TestHandleRunAgentDone_CompletedStatus_ProceedsToGatesOrMerge verifies
// that when the orchestrator reports the subagent's status as
// "completed" (i.e. clean exit code 0), handleRunAgentDone continues
// to the normal gate/merge flow. This is the "happy path" of the
// post-spawn verification step.
func TestHandleRunAgentDone_CompletedStatus_ProceedsToGatesOrMerge(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	orch.SetStatusForTest("task-1", "completed")

	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		running:   true,
		cfg:       Config{Orchestrator: orch},
		run: &runState{
			specName: "test-spec",
			phase:    "running",
			agentID:  "task-1",
			gates:    nil, // no gates → goes straight to merge
		},
	}

	m.handleRunAgentDone(runAgentDoneMsg{agentID: "task-1"})

	if m.run.phase != "merging" {
		t.Errorf("phase = %q, want merging (completed status, no gates)", m.run.phase)
	}
	if m.running {
		t.Error("model should not still be running after done")
	}
}

// TestHandleRunAgentDone_FailedStatus_SkipsGatesAndMerge verifies that
// when the subagent exited non-zero (status != "completed"), the run
// is marked failed, gates/merge are skipped, and the worktree is
// preserved. This is the behavior the user asked for: "check if
// subagent finished <> 0 exit code".
func TestHandleRunAgentDone_FailedStatus_SkipsGatesAndMerge(t *testing.T) {
	tmpDir := t.TempDir()
	orch := subagent.NewOrchestrator(&config.Config{}, tmpDir, nil)
	orch.SetStatusForTest("task-1", "failed")

	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		running:   true,
		cfg: Config{
			Orchestrator: orch,
			WorkDir:      tmpDir,
		},
		run: &runState{
			specName: "test-spec",
			phase:    "running",
			agentID:  "task-1",
			gates:    []Gate{{Name: "test", Command: "go test ./..."}},
		},
	}

	// Pre-create a PROMPT.md so writeRunSummary has somewhere to write.
	if err := os.MkdirAll(filepath.Join(tmpDir, "specs", "test-spec"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "specs", "test-spec", "PROMPT.md"),
		[]byte("# Test\n\n## Gates\n\n- **test**: `go test ./...`\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m.handleRunAgentDone(runAgentDoneMsg{agentID: "task-1"})

	if m.run.phase != "failed" {
		t.Errorf("phase = %q, want failed (subagent exited non-zero)", m.run.phase)
	}

	// User-facing message should mention the non-zero status and the
	// /run command they can use to resume.
	var saw bool
	for _, msg := range m.chatModel.Messages {
		if strings.Contains(msg.content, "non-zero status") &&
			strings.Contains(msg.content, "/run test-spec") {
			saw = true
			break
		}
	}
	if !saw {
		t.Error("expected a chat message mentioning non-zero status and the /run command to resume")
	}

	// Should NOT contain the gate validation message (we skip gates).
	for _, msg := range m.chatModel.Messages {
		if strings.Contains(msg.content, "validating gates") {
			t.Error("should not announce gate validation when subagent failed")
		}
	}

	// SUMMARY.md should be written with the new agent_failed outcome.
	reportPath := filepath.Join(tmpDir, "specs", "test-spec", "SUMMARY.md")
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("expected SUMMARY.md: %v", err)
	}
	if !strings.Contains(string(data), "agent_failed") {
		t.Errorf("SUMMARY.md should mention agent_failed outcome; got:\n%s", string(data))
	}
}

// TestHandleRunAgentDone_UnknownStatus_TreatedAsFailed verifies that
// when the orchestrator has no record of the agent at all (e.g. the
// events channel closed before the orchestrator recorded a final
// status), we err on the side of warning the user rather than silently
// merging.
func TestHandleRunAgentDone_UnknownStatus_TreatedAsFailed(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	// No SetStatusForTest — the orchestrator has no record of this agent.

	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		running:   true,
		cfg:       Config{Orchestrator: orch},
		run: &runState{
			specName: "test-spec",
			phase:    "running",
			agentID:  "task-unknown",
			gates:    []Gate{{Name: "test", Command: "true"}},
		},
	}

	m.handleRunAgentDone(runAgentDoneMsg{agentID: "task-unknown"})

	if m.run.phase != "failed" {
		t.Errorf("phase = %q, want failed (unknown agent)", m.run.phase)
	}
}

// TestHandleRunAgentDone_ParallelOneFailed_AbortsRun verifies that if
// even one parallel agent exits non-zero, the whole run is marked
// failed and gates/merge are skipped — neither the failing agent nor
// the successful one should be merged.
func TestHandleRunAgentDone_ParallelOneFailed_AbortsRun(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	orch.SetStatusForTest("a-1", "completed")
	orch.SetStatusForTest("a-2", "killed")

	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		running:   true,
		cfg:       Config{Orchestrator: orch},
		run: &runState{
			specName: "test-spec",
			phase:    "running",
			parallel: []*parallelAgent{
				{agentID: "a-1", done: true},
				{agentID: "a-2", done: true},
			},
		},
	}

	m.handleRunAgentDone(runAgentDoneMsg{agentID: "a-1"}) // a-1 already done, a-2 also done
	// (a-2 was already marked done in setup; handleRunAgentDone's
	//  parallel branch only sets done=true on the matching agentID.)
	m.handleRunAgentDone(runAgentDoneMsg{agentID: "a-2"})

	if m.run.phase != "failed" {
		t.Errorf("phase = %q, want failed (a-2 was killed)", m.run.phase)
	}
}

// TestHandleRunAgentDone_ParallelAllCompleted_ProceedsToGates verifies
// the parallel happy path: every agent is "completed" → gates run.
func TestHandleRunAgentDone_ParallelAllCompleted_ProceedsToGates(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	orch.SetStatusForTest("a-1", "completed")
	orch.SetStatusForTest("a-2", "completed")

	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		running:   true,
		cfg:       Config{Orchestrator: orch},
		run: &runState{
			specName: "test-spec",
			phase:    "running",
			gates:    []Gate{{Name: "test", Command: "true"}}, // has gates → phase "gating"
			parallel: []*parallelAgent{
				{agentID: "a-1", done: true}, // already done
				{agentID: "a-2", done: false},
			},
		},
	}

	m.handleRunAgentDone(runAgentDoneMsg{agentID: "a-2"})

	if m.run.phase != "gating" {
		t.Errorf("phase = %q, want gating (parallel all completed)", m.run.phase)
	}
}

func TestHandleRunAgentDone_UsesTerminalStatusAfterOrchestratorEviction(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		running:   true,
		cfg:       Config{Orchestrator: orch},
		run: &runState{
			specName: "evicted-agent",
			phase:    "running",
			agentID:  "task-evicted",
		},
	}

	// The terminal event is authoritative even though the bounded orchestrator
	// status map no longer contains the completed agent.
	m.handleRunAgentDone(runAgentDoneMsg{agentID: "task-evicted", status: "completed"})

	if m.run.phase != "merging" {
		t.Fatalf("phase = %q, want merging", m.run.phase)
	}
}

// --- Verifier tests: gates passing must not merge an incomplete plan ---

// A green build over a half-implemented plan is the failure the Verifier
// exists to catch: gates pass, but slices remain unchecked, so the run must
// loop instead of merging.
func TestVerifier_IncompletePlanDoesNotMerge(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := &model{
		ctx:       ctx,
		cfg:       Config{Orchestrator: orch},
		chatModel: ChatModel{Messages: make([]message, 0)},
		run: &runState{
			specName:   "half-done",
			promptMD:   "# Test\n",
			agentID:    "task-1",
			phase:      "gating",
			retries:    0,
			maxRetries: 3,
			checklist: []ChecklistStep{
				{Title: "Core", Done: true},
				{Title: "HTTP", Done: false},
			},
		},
	}

	m.handleRunGateResult(runGateResultMsg{
		results: []GateResult{{Name: "build", Command: "go build ./...", Passed: true}},
		passed:  true,
	})

	if m.run.phase == "merging" || m.run.phase == "done" {
		t.Fatalf("phase = %q — must not merge while slices are unchecked", m.run.phase)
	}
	if m.run.retries != 1 {
		t.Errorf("retries = %d, want 1 (verifier should trigger a cycle)", m.run.retries)
	}

	var sawVerdict bool
	for _, msg := range m.chatModel.Messages {
		if strings.Contains(msg.content, "Verifier: FAIL") {
			sawVerdict = true
		}
	}
	if !sawVerdict {
		t.Error("expected a 'Verifier: FAIL' message")
	}
}

// With every slice ticked, the Verifier passes and the run proceeds to merge.
func TestVerifier_CompletePlanProceedsToMerge(t *testing.T) {
	m := &model{
		cfg:       Config{Orchestrator: subagent.NewOrchestrator(&config.Config{}, "", nil)},
		chatModel: ChatModel{Messages: make([]message, 0)},
		run: &runState{
			specName:   "all-done",
			agentID:    "task-1",
			phase:      "gating",
			maxRetries: 3,
			checklist: []ChecklistStep{
				{Title: "Core", Done: true},
				{Title: "HTTP", Done: true},
			},
		},
	}

	m.handleRunGateResult(runGateResultMsg{
		results: []GateResult{{Name: "build", Command: "go build ./...", Passed: true}},
		passed:  true,
	})

	if m.run.phase != "merging" {
		t.Fatalf("phase = %q, want merging", m.run.phase)
	}
	if m.run.retries != 0 {
		t.Errorf("retries = %d, want 0 — a complete plan should not retry", m.run.retries)
	}
}

// A spec with no plan.md checklist has nothing to verify; the run must still
// merge rather than stalling forever.
func TestVerifier_NoChecklistProceedsToMerge(t *testing.T) {
	m := &model{
		cfg:       Config{Orchestrator: subagent.NewOrchestrator(&config.Config{}, "", nil)},
		chatModel: ChatModel{Messages: make([]message, 0)},
		run: &runState{
			specName:   "no-plan",
			agentID:    "task-1",
			phase:      "gating",
			maxRetries: 3,
		},
	}

	m.handleRunGateResult(runGateResultMsg{passed: true})

	if m.run.phase != "merging" {
		t.Fatalf("phase = %q, want merging", m.run.phase)
	}
}

// Once the retry budget is spent, an incomplete plan ends the run as
// verify_failed — it must never fall through to a merge.
func TestVerifier_ExhaustedRetriesFailsWithoutMerging(t *testing.T) {
	m := &model{
		cfg:       Config{Orchestrator: subagent.NewOrchestrator(&config.Config{}, "", nil)},
		chatModel: ChatModel{Messages: make([]message, 0)},
		run: &runState{
			specName:   "stuck",
			agentID:    "task-1",
			phase:      "gating",
			retries:    3,
			maxRetries: 3,
			checklist:  []ChecklistStep{{Title: "HTTP", Done: false}},
		},
	}

	m.handleRunGateResult(runGateResultMsg{passed: true})

	if m.run.phase != "failed" {
		t.Fatalf("phase = %q, want failed", m.run.phase)
	}
	var sawNotMerging bool
	for _, msg := range m.chatModel.Messages {
		if strings.Contains(msg.content, "Not merging") {
			sawNotMerging = true
		}
	}
	if !sawNotMerging {
		t.Error("expected the failure message to state that nothing was merged")
	}
}

// --- Agent-failure retry: a dropped provider stream must not end the run ---

// A subagent that exits non-clean (429/502 mid-stream) used to end the whole
// run. It must now consume a retry cycle instead.
func TestAgentFailure_TriggersRetryInsteadOfGivingUp(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := &model{
		ctx:       ctx,
		cfg:       Config{Orchestrator: orch},
		chatModel: ChatModel{Messages: make([]message, 0)},
		running:   true,
		run: &runState{
			specName:   "dropped-stream",
			promptMD:   "# Test\n",
			agentID:    "task-1",
			phase:      "running",
			retries:    0,
			maxRetries: 10,
			checklist:  []ChecklistStep{{Title: "Core", Done: false}},
		},
	}

	m.handleRunAgentDone(runAgentDoneMsg{agentID: "task-1", status: "failed"})

	if m.run.retries != 1 {
		t.Errorf("retries = %d, want 1 — an agent failure should start a new cycle", m.run.retries)
	}
	if m.run.phase == "gating" || m.run.phase == "merging" {
		t.Errorf("phase = %q — a failed agent must not reach gates or merge", m.run.phase)
	}
}

// With the retry budget spent, an agent failure reports agent_failed and stops.
func TestAgentFailure_ExhaustedRetriesReportsFailure(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	m := &model{
		cfg:       Config{Orchestrator: orch},
		chatModel: ChatModel{Messages: make([]message, 0)},
		running:   true,
		run: &runState{
			specName:   "dropped-stream",
			agentID:    "task-1",
			phase:      "running",
			retries:    10,
			maxRetries: 10,
		},
	}

	m.handleRunAgentDone(runAgentDoneMsg{agentID: "task-1", status: "failed"})

	if m.run.phase != "failed" {
		t.Fatalf("phase = %q, want failed", m.run.phase)
	}
}

// --- Unfinished-slice context carried into the next cycle ---

func TestUnfinishedSlicesContext(t *testing.T) {
	m := &model{
		run: &runState{
			checklist: []ChecklistStep{
				{Title: "Core", Done: true},
				{Title: "HTTP handler", Done: false},
				{Title: "Docs", Done: false},
			},
		},
	}

	got := m.unfinishedSlicesContext()

	if !strings.Contains(got, "2 of 3 slices unfinished") {
		t.Errorf("context should count unfinished slices, got:\n%s", got)
	}
	if !strings.Contains(got, "Step 2: HTTP handler") {
		t.Error("context should list unfinished slices with their 1-based index")
	}
	if strings.Contains(got, "Step 1: Core") {
		t.Error("context should not list completed slices")
	}
}

func TestUnfinishedSlicesContext_AllDone(t *testing.T) {
	m := &model{run: &runState{checklist: []ChecklistStep{{Title: "Core", Done: true}}}}
	if got := m.unfinishedSlicesContext(); got != "" {
		t.Errorf("context should be empty when all slices are done, got %q", got)
	}
}

// --- Coordinator contract present in the /run prompt ---

func TestBuildRunPrompt_CarriesCoordinatorContract(t *testing.T) {
	prompt := buildRunPrompt("my-feature", "# My Feature\n", nil)

	for _, want := range []string{
		"Your Role: Coordinator",
		`{agent: "worker"`,
		"code-reviewer",
		"VERDICT: PASS",
		"specs/my-feature/plan.md",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("run prompt missing %q", want)
		}
	}

	// Worktree agents would silently discard their edits into a nested worktree.
	if !strings.Contains(prompt, "Never spawn `task` or `designer`") {
		t.Error("run prompt should forbid delegating to [worktree] agents")
	}
}

// Terminal verifier reporting runs on paths that may have no orchestrator at
// all; looking up the worktree there must not panic.
func TestVerifier_ExhaustedRetriesWithoutOrchestrator(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		run: &runState{
			specName:   "stuck",
			agentID:    "task-1",
			phase:      "gating",
			retries:    3,
			maxRetries: 3,
			checklist:  []ChecklistStep{{Title: "HTTP", Done: false}},
		},
	}

	m.verifyRunComplete()

	if m.run.phase != "failed" {
		t.Fatalf("phase = %q, want failed", m.run.phase)
	}
}

func TestRunWorktreePath_NilOrchestrator(t *testing.T) {
	m := &model{}
	if got := m.runWorktreePath("task-1"); got != "" {
		t.Errorf("runWorktreePath = %q, want empty with no orchestrator", got)
	}
}

// --- Spec length budget: warn when a spec outgrows the read window ---

func writeSpecFile(t *testing.T, workDir, specName, file string, lines int) {
	t.Helper()
	dir := filepath.Join(workDir, "specs", specName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("a line of plan prose\n", lines)
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOversizedSpecFiles_UnderWindowIsSilent(t *testing.T) {
	dir := t.TempDir()
	writeSpecFile(t, dir, "small", "plan.md", readWindowLines)

	if got := oversizedSpecFiles(dir, "small"); len(got) != 0 {
		t.Errorf("a plan exactly at the window should not warn, got %+v", got)
	}
}

func TestOversizedSpecFiles_OverWindowIsReported(t *testing.T) {
	dir := t.TempDir()
	writeSpecFile(t, dir, "big", "plan.md", readWindowLines+1)

	got := oversizedSpecFiles(dir, "big")
	if len(got) != 1 {
		t.Fatalf("got %d oversized files, want 1", len(got))
	}
	if got[0].Name != "plan.md" {
		t.Errorf("Name = %q, want plan.md", got[0].Name)
	}
	if got[0].Lines != readWindowLines+1 {
		t.Errorf("Lines = %d, want %d", got[0].Lines, readWindowLines+1)
	}
}

// design.md is read by workers too, so it carries the same ceiling.
func TestOversizedSpecFiles_ChecksDesignToo(t *testing.T) {
	dir := t.TempDir()
	writeSpecFile(t, dir, "big", "plan.md", 10)
	writeSpecFile(t, dir, "big", "design.md", readWindowLines+50)

	got := oversizedSpecFiles(dir, "big")
	if len(got) != 1 || got[0].Name != "design.md" {
		t.Fatalf("got %+v, want only design.md", got)
	}
}

// PROMPT.md is embedded whole via os.ReadFile, so the window never applies.
func TestOversizedSpecFiles_IgnoresPromptMD(t *testing.T) {
	dir := t.TempDir()
	writeSpecFile(t, dir, "big", "PROMPT.md", readWindowLines*2)

	if got := oversizedSpecFiles(dir, "big"); len(got) != 0 {
		t.Errorf("PROMPT.md is never windowed, got %+v", got)
	}
}

// A spec with no design.md is normal, not an error.
func TestOversizedSpecFiles_MissingFilesAreSkipped(t *testing.T) {
	dir := t.TempDir()
	if got := oversizedSpecFiles(dir, "nonexistent"); len(got) != 0 {
		t.Errorf("missing spec should yield no warnings, got %+v", got)
	}
}

func TestCountLines(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"one line no newline", "a", 1},
		{"one line trailing newline", "a\n", 1},
		{"two lines trailing newline", "a\nb\n", 2},
		{"two lines no trailing newline", "a\nb", 2},
		{"blank line counts", "a\n\nb\n", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := countLines([]byte(tc.in)); got != tc.want {
				t.Errorf("countLines(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestFormatOversizedSpecWarning(t *testing.T) {
	out := formatOversizedSpecWarning([]oversizedSpecFile{{Name: "plan.md", Lines: 2500}})

	for _, want := range []string{"plan.md", "2500", "2000", "sequential specs", "Running anyway"} {
		if !strings.Contains(out, want) {
			t.Errorf("warning missing %q, got:\n%s", want, out)
		}
	}
}

// --- Worktree ownership survives a retry ---

// A retry agent is spawned with WorkDir set to the existing worktree and never
// creates one of its own. Gates, checklist refresh and the merge must keep
// asking the original owner, or they look up an agent that has no worktree.
func TestRetry_KeepsWorktreeOwner(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := &model{
		ctx:       ctx,
		cfg:       Config{Orchestrator: orch},
		chatModel: ChatModel{Messages: make([]message, 0)},
		run: &runState{
			specName:        "spec",
			promptMD:        "# Test\n",
			agentID:         "task-original",
			worktreeAgentID: "task-original",
			phase:           "running",
			maxRetries:      10,
		},
	}

	// Spawn fails in this environment, but the owner must be untouched either
	// way — it is never the retry agent's to claim.
	m.retryRun("gate failed", "")

	if m.run.worktreeAgentID != "task-original" {
		t.Errorf("worktreeAgentID = %q, want it to stay task-original", m.run.worktreeAgentID)
	}
}

// Collapsing a parallel run to a single resuming coordinator must not discard
// the other agents' worktrees — that is where the second agent's work lives.
func TestCollapseParallel_CarriesOtherWorktreesToTheMerge(t *testing.T) {
	rs := &runState{
		specName:        "spec",
		worktreeAgentID: "task-1",
		parallel: []*parallelAgent{
			{agentID: "task-1"},
			{agentID: "task-2"},
		},
	}

	rs.collapseParallel()

	if rs.isParallel() {
		t.Error("collapseParallel should end parallel fan-in")
	}
	var carried []string
	for _, c := range rs.carried {
		carried = append(carried, c.agentID)
		if c.backup == "" {
			t.Errorf("carried agent %s has no backup branch", c.agentID)
		}
	}
	// task-1 is the owner and merges as the primary; duplicating it would
	// merge the same branch twice.
	if len(carried) != 1 || carried[0] != "task-2" {
		t.Errorf("carried = %v, want exactly [task-2]", carried)
	}
}

// A single-agent run has nothing to carry.
func TestCollapseParallel_SingleAgentCarriesNothing(t *testing.T) {
	rs := &runState{specName: "spec", worktreeAgentID: "task-1"}
	rs.collapseParallel()
	if len(rs.carried) != 0 {
		t.Errorf("carried = %v, want none", rs.carried)
	}
}

// --- Checklist union across parallel worktrees ---

// writePlanChecklist writes a plan.md into a fake worktree with the given
// checkbox states, mirroring what an agent leaves behind in its own tree.
func writePlanChecklist(t *testing.T, wtPath, specName string, done []bool) {
	t.Helper()
	dir := filepath.Join(wtPath, "specs", specName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("# Plan\n\n## Progress\n\n")
	for i, d := range done {
		mark := " "
		if d {
			mark = "x"
		}
		fmt.Fprintf(&b, "- [%s] Step %d: slice %d\n", mark, i+1, i+1)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func doneFlags(steps []ChecklistStep) []bool {
	out := make([]bool, len(steps))
	for i, s := range steps {
		out[i] = s.Done
	}
	return out
}

// Each parallel agent ticks only its own slices in its own worktree. Reading
// one tree alone leaves the other agent's slices unchecked forever, so the
// union is what lets a parallel run ever verify complete.
func TestChecklistFromWorktrees_UnionsParallelViews(t *testing.T) {
	wt1, wt2 := t.TempDir(), t.TempDir()
	writePlanChecklist(t, wt1, "spec", []bool{true, true, false, false})
	writePlanChecklist(t, wt2, "spec", []bool{false, false, true, true})

	got := checklistFromWorktrees([]string{wt1, wt2}, "spec")

	want := []bool{true, true, true, true}
	if diff := doneFlags(got); !slicesEqualBool(diff, want) {
		t.Errorf("done flags = %v, want %v", diff, want)
	}
}

// The union must never un-tick: a slice done in the first tree stays done even
// though the second tree still shows it open.
func TestChecklistFromWorktrees_NeverUnticks(t *testing.T) {
	wt1, wt2 := t.TempDir(), t.TempDir()
	writePlanChecklist(t, wt1, "spec", []bool{true, true})
	writePlanChecklist(t, wt2, "spec", []bool{false, false})

	got := checklistFromWorktrees([]string{wt1, wt2}, "spec")

	if flags := doneFlags(got); !slicesEqualBool(flags, []bool{true, true}) {
		t.Errorf("done flags = %v, want [true true] — the union must not un-tick", flags)
	}
}

// A worktree whose plan.md was rewritten to a different length must be skipped,
// not allowed to overwrite a good view.
func TestChecklistFromWorktrees_MismatchedLengthIsSkipped(t *testing.T) {
	wt1, wt2 := t.TempDir(), t.TempDir()
	writePlanChecklist(t, wt1, "spec", []bool{true, true, false})
	writePlanChecklist(t, wt2, "spec", []bool{false})

	got := checklistFromWorktrees([]string{wt1, wt2}, "spec")

	if len(got) != 3 {
		t.Fatalf("len = %d, want the 3-step view preserved", len(got))
	}
	if flags := doneFlags(got); !slicesEqualBool(flags, []bool{true, true, false}) {
		t.Errorf("done flags = %v, want the first view unchanged", flags)
	}
}

// Missing, empty and blank paths are skipped rather than treated as a plan
// with zero slices, which would read as "nothing to do".
func TestChecklistFromWorktrees_SkipsUnreadable(t *testing.T) {
	wt := t.TempDir()
	writePlanChecklist(t, wt, "spec", []bool{true, false})

	got := checklistFromWorktrees([]string{"", filepath.Join(wt, "nope"), wt}, "spec")

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 from the one readable worktree", len(got))
	}
}

func TestChecklistFromWorktrees_NoWorktreesYieldsNothing(t *testing.T) {
	if got := checklistFromWorktrees(nil, "spec"); got != nil {
		t.Errorf("got %v, want nil when there is nothing to read", got)
	}
}

// A single-agent run still reads its own worktree.
func TestChecklistFromWorktrees_SingleWorktree(t *testing.T) {
	wt := t.TempDir()
	writePlanChecklist(t, wt, "spec", []bool{true, false, true})

	got := checklistFromWorktrees([]string{wt}, "spec")

	if flags := doneFlags(got); !slicesEqualBool(flags, []bool{true, false, true}) {
		t.Errorf("done flags = %v, want [true false true]", flags)
	}
}

func slicesEqualBool(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- Merge target collection ---

func targetIDs(targets []mergeTarget) []string {
	out := make([]string, len(targets))
	for i, t := range targets {
		out[i] = t.agentID
	}
	return out
}

func TestMergeTargets_SingleAgentUsesTheWorktreeOwner(t *testing.T) {
	// agentID is the retry agent, which owns no worktree; the merge must
	// target the owner instead.
	rs := &runState{specName: "spec", agentID: "task-retry", worktreeAgentID: "task-owner"}

	got := rs.mergeTargets()

	if ids := targetIDs(got); len(ids) != 1 || ids[0] != "task-owner" {
		t.Errorf("targets = %v, want [task-owner]", ids)
	}
	if got[0].backup == "" {
		t.Error("single-agent target should carry a backup branch")
	}
}

func TestMergeTargets_ParallelCoversEveryAgent(t *testing.T) {
	rs := &runState{
		specName:        "spec",
		worktreeAgentID: "task-1",
		parallel:        []*parallelAgent{{agentID: "task-1"}, {agentID: "task-2"}},
	}

	got := rs.mergeTargets()

	if ids := targetIDs(got); len(ids) != 2 || ids[0] != "task-1" || ids[1] != "task-2" {
		t.Errorf("targets = %v, want [task-1 task-2]", ids)
	}
	if got[0].backup == got[1].backup {
		t.Error("parallel agents must get distinct backup branches")
	}
}

// After collapsing, the carried worktrees must still be merged — that is where
// the second agent's slices live.
func TestMergeTargets_IncludesCarriedAfterCollapse(t *testing.T) {
	rs := &runState{
		specName:        "spec",
		worktreeAgentID: "task-1",
		parallel:        []*parallelAgent{{agentID: "task-1"}, {agentID: "task-2"}},
	}
	rs.collapseParallel()

	got := targetIDs(rs.mergeTargets())

	if len(got) != 2 {
		t.Fatalf("targets = %v, want the owner plus the carried worktree", got)
	}
	var seenOwner, seenCarried bool
	for _, id := range got {
		switch id {
		case "task-1":
			seenOwner = true
		case "task-2":
			seenCarried = true
		}
	}
	if !seenOwner || !seenCarried {
		t.Errorf("targets = %v, want both task-1 and task-2", got)
	}
}

// The owner must not be merged twice once it has also been carried.
func TestMergeTargets_NoDuplicateAfterCollapse(t *testing.T) {
	rs := &runState{
		specName:        "spec",
		worktreeAgentID: "task-1",
		parallel:        []*parallelAgent{{agentID: "task-1"}, {agentID: "task-2"}},
	}
	rs.collapseParallel()

	seen := map[string]int{}
	for _, id := range targetIDs(rs.mergeTargets()) {
		seen[id]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("agent %s appears %d times; merging one branch twice", id, n)
		}
	}
}

// --- Worktree path gathering ---

// A parallel run must offer every agent's worktree to the checklist reader,
// not just the one the run owns.
func TestRunChecklistPaths_CoversOwnerAndParallelAgents(t *testing.T) {
	wm := subagent.NewWorktreeManager(t.TempDir())

	m := &model{
		run: &runState{
			worktreeAgentID: "task-1",
			parallel:        []*parallelAgent{{agentID: "task-1"}, {agentID: "task-2"}},
		},
	}

	paths := m.runChecklistPaths(wm)

	// No worktrees are registered, so every lookup is empty — but the owner
	// slot must always be present, and checklistFromWorktrees skips blanks.
	if len(paths) != 1 {
		t.Fatalf("paths = %v, want just the owner slot when nothing is registered", paths)
	}
	if got := checklistFromWorktrees(paths, "spec"); got != nil {
		t.Errorf("got %v, want nil when no worktree has a plan.md", got)
	}
}

// A single-agent run asks for exactly one worktree.
func TestRunChecklistPaths_SingleAgent(t *testing.T) {
	wm := subagent.NewWorktreeManager(t.TempDir())
	m := &model{run: &runState{worktreeAgentID: "task-1"}}

	if paths := m.runChecklistPaths(wm); len(paths) != 1 {
		t.Errorf("paths = %v, want exactly one entry", paths)
	}
}

// refreshRunChecklist must not panic or wipe state when there is no
// orchestrator or worktree manager to ask.
func TestRefreshRunChecklist_NoOrchestratorLeavesChecklistIntact(t *testing.T) {
	original := []ChecklistStep{{Title: "A", Done: true}}
	m := &model{run: &runState{specName: "spec", checklist: original}}

	m.refreshRunChecklist()

	if len(m.run.checklist) != 1 || !m.run.checklist[0].Done {
		t.Errorf("checklist = %v, want it untouched without an orchestrator", m.run.checklist)
	}
}

// An unreadable worktree must leave the last good checklist in place rather
// than clearing it — a cleared checklist reads as "no slices", which the
// Verifier would treat as complete.
func TestRefreshRunChecklist_UnreadableWorktreeKeepsLastGoodView(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, t.TempDir(), nil)
	original := []ChecklistStep{{Title: "A", Done: true}, {Title: "B", Done: false}}

	m := &model{
		cfg: Config{Orchestrator: orch},
		run: &runState{specName: "spec", worktreeAgentID: "task-1", checklist: original},
	}

	m.refreshRunChecklist()

	if len(m.run.checklist) != 2 {
		t.Fatalf("checklist = %v, want the previous 2-step view kept", m.run.checklist)
	}
	if !m.run.checklist[0].Done {
		t.Error("a failed refresh must not un-tick completed slices")
	}
}

// End-to-end over real git worktrees: the union is what makes a parallel run
// verifiable, so it is worth proving against actual worktrees rather than
// only against the pure helper.
func TestRefreshRunChecklist_UnionsRealParallelWorktrees(t *testing.T) {
	repo := initRunTestRepo(t)
	orch := subagent.NewOrchestrator(&config.Config{}, repo, nil)
	t.Cleanup(orch.Shutdown)

	const agent1, agent2 = "task-union-1", "task-union-2"
	wt1, err := orch.Worktree().Create(agent1)
	if err != nil {
		t.Fatalf("creating worktree 1: %v", err)
	}
	wt2, err := orch.Worktree().Create(agent2)
	if err != nil {
		t.Fatalf("creating worktree 2: %v", err)
	}

	// Each agent ticks only its own half, in its own tree.
	writePlanChecklist(t, wt1, "spec", []bool{true, true, false, false})
	writePlanChecklist(t, wt2, "spec", []bool{false, false, true, true})

	m := &model{
		cfg: Config{Orchestrator: orch},
		run: &runState{
			specName:        "spec",
			worktreeAgentID: agent1,
			parallel:        []*parallelAgent{{agentID: agent1}, {agentID: agent2}},
		},
	}

	m.refreshRunChecklist()

	if n := len(m.run.checklist); n != 4 {
		t.Fatalf("checklist length = %d, want 4", n)
	}
	if pending := m.run.unfinishedSlices(); len(pending) != 0 {
		t.Errorf("unfinished = %v, want none — both worktrees together complete the plan", pending)
	}
}

// Without the union, the primary worktree alone leaves the second agent's
// slices unchecked. This pins that the verifier would have failed.
func TestRefreshRunChecklist_PrimaryAloneIsIncomplete(t *testing.T) {
	repo := initRunTestRepo(t)
	orch := subagent.NewOrchestrator(&config.Config{}, repo, nil)
	t.Cleanup(orch.Shutdown)

	const agent1 = "task-solo-1"
	wt1, err := orch.Worktree().Create(agent1)
	if err != nil {
		t.Fatalf("creating worktree: %v", err)
	}
	writePlanChecklist(t, wt1, "spec", []bool{true, true, false, false})

	m := &model{
		cfg: Config{Orchestrator: orch},
		run: &runState{specName: "spec", worktreeAgentID: agent1},
	}

	m.refreshRunChecklist()

	if pending := m.run.unfinishedSlices(); len(pending) != 2 {
		t.Errorf("unfinished = %v, want the 2 slices the other agent owns", pending)
	}
}

// A collapsed run keeps the owner on its part-N backup name rather than falling
// back to the bare one, so the merge still holds a distinct branch per agent
// (run/spec-part-1 for the owner, run/spec-part-2 for the carried worktree).
func TestCollapseParallel_OwnerKeepsItsPartName(t *testing.T) {
	rs := &runState{
		specName:        "spec",
		worktreeAgentID: "task-1",
		parallel:        []*parallelAgent{{agentID: "task-1"}, {agentID: "task-2"}},
	}

	rs.collapseParallel()

	targets := rs.mergeTargets()
	if len(targets) != 2 {
		t.Fatalf("targets = %v, want owner plus carried", targetIDs(targets))
	}

	bare := runBackupBranchName("spec", "")
	for _, tgt := range targets {
		if tgt.backup == bare {
			t.Errorf("agent %s uses the bare backup %q instead of its part-N name", tgt.agentID, bare)
		}
		if !strings.HasPrefix(tgt.backup, bare+"-part-") {
			t.Errorf("agent %s backup = %q, want a %s-part-N sibling", tgt.agentID, tgt.backup, bare)
		}
	}
}

// A run that never went parallel still uses the plain backup name.
func TestMergeTargets_NeverParallelUsesBareBackup(t *testing.T) {
	rs := &runState{specName: "spec", worktreeAgentID: "task-1"}

	got := rs.mergeTargets()

	if want := runBackupBranchName("spec", ""); got[0].backup != want {
		t.Errorf("backup = %q, want %q", got[0].backup, want)
	}
}
