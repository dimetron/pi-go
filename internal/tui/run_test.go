package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	// Process done.
	m.handleRunAgentDone()
	if m.running {
		t.Error("model should not be running after done")
	}
	if m.run.phase != "done" {
		t.Errorf("run phase = %q, want %q", m.run.phase, "done")
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
