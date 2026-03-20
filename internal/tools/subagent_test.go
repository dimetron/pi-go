package tools

import (
	"strings"
	"sync"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/subagent"
)

// --- Mode detection tests ---

func TestDetectMode_Single(t *testing.T) {
	input := SubagentInput{Agent: "explore", Task: "find main.go"}
	if mode := detectMode(input); mode != "single" {
		t.Errorf("detectMode = %q, want 'single'", mode)
	}
}

func TestDetectMode_Parallel(t *testing.T) {
	input := SubagentInput{Tasks: []TaskItem{{Agent: "a", Task: "b"}}}
	if mode := detectMode(input); mode != "parallel" {
		t.Errorf("detectMode = %q, want 'parallel'", mode)
	}
}

func TestDetectMode_Chain(t *testing.T) {
	input := SubagentInput{Chain: []ChainItem{{Agent: "a", Task: "b"}}}
	if mode := detectMode(input); mode != "chain" {
		t.Errorf("detectMode = %q, want 'chain'", mode)
	}
}

func TestDetectMode_ChainPriorityOverParallel(t *testing.T) {
	input := SubagentInput{
		Chain: []ChainItem{{Agent: "a", Task: "b"}},
		Tasks: []TaskItem{{Agent: "c", Task: "d"}},
	}
	if mode := detectMode(input); mode != "chain" {
		t.Errorf("detectMode = %q, want 'chain'", mode)
	}
}

func TestDetectMode_Empty(t *testing.T) {
	input := SubagentInput{}
	if mode := detectMode(input); mode != "" {
		t.Errorf("detectMode = %q, want empty", mode)
	}
}

func TestDetectMode_AgentOnlyNoTask(t *testing.T) {
	input := SubagentInput{Agent: "explore"}
	if mode := detectMode(input); mode != "" {
		t.Errorf("detectMode = %q, want empty (agent without task)", mode)
	}
}

// --- Single mode tests ---

func TestSubagentSingleMode_UnknownAgent(t *testing.T) {
	cfg := config.Defaults()
	agents := []subagent.AgentConfig{
		{Name: "explore", Description: "test", Role: "default"},
	}
	orch := subagent.NewOrchestrator(&cfg, "", agents)

	input := SubagentInput{Agent: "nonexistent", Task: "find main.go"}
	output, err := subagentHandler(nil, orch, input, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Mode != "single" {
		t.Errorf("mode = %q, want 'single'", output.Mode)
	}
	if len(output.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(output.Results))
	}
	r := output.Results[0]
	if r.Status != "failed" {
		t.Errorf("status = %q, want 'failed'", r.Status)
	}
	if r.Error == "" {
		t.Error("expected error message for unknown agent")
	}
	if r.Agent != "nonexistent" {
		t.Errorf("agent = %q, want 'nonexistent'", r.Agent)
	}
}

func TestSubagentSingleMode_NoModeDetected(t *testing.T) {
	cfg := config.Defaults()
	orch := subagent.NewOrchestrator(&cfg, "", nil)

	input := SubagentInput{} // empty — no mode
	_, err := subagentHandler(nil, orch, input, nil)
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

// --- Parallel mode tests ---

func TestSubagentParallelMode_UnknownAgent(t *testing.T) {
	cfg := config.Defaults()
	agents := []subagent.AgentConfig{
		{Name: "explore", Description: "test", Role: "default"},
	}
	orch := subagent.NewOrchestrator(&cfg, "", agents)

	input := SubagentInput{Tasks: []TaskItem{
		{Agent: "explore", Task: "a"},
		{Agent: "nonexistent", Task: "b"},
	}}
	output, err := subagentHandler(nil, orch, input, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Mode != "parallel" {
		t.Errorf("mode = %q, want 'parallel'", output.Mode)
	}
	if len(output.Results) != 1 {
		t.Fatalf("expected 1 result (validation error), got %d", len(output.Results))
	}
	if output.Results[0].Status != "failed" {
		t.Errorf("status = %q, want 'failed'", output.Results[0].Status)
	}
	if output.Results[0].Agent != "nonexistent" {
		t.Errorf("agent = %q, want 'nonexistent'", output.Results[0].Agent)
	}
}

func TestSubagentParallelMode_TooManyTasks(t *testing.T) {
	cfg := config.Defaults()
	orch := subagent.NewOrchestrator(&cfg, "", nil)

	tasks := make([]TaskItem, maxParallelTasks+1)
	for i := range tasks {
		tasks[i] = TaskItem{Agent: "explore", Task: "a"}
	}
	input := SubagentInput{Tasks: tasks}
	output, err := subagentHandler(nil, orch, input, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Mode != "parallel" {
		t.Errorf("mode = %q, want 'parallel'", output.Mode)
	}
	if len(output.Results) != 1 {
		t.Fatalf("expected 1 result (limit error), got %d", len(output.Results))
	}
	if output.Results[0].Status != "failed" {
		t.Errorf("status = %q, want 'failed'", output.Results[0].Status)
	}
	if !strings.Contains(output.Results[0].Error, "too many") {
		t.Errorf("error should mention 'too many', got: %s", output.Results[0].Error)
	}
}

func TestSubagentParallelMode_AllUnknownAgents(t *testing.T) {
	cfg := config.Defaults()
	orch := subagent.NewOrchestrator(&cfg, "", nil)

	// All agents unknown — fails at validation before spawning.
	input := SubagentInput{Tasks: []TaskItem{
		{Agent: "unknown1", Task: "a"},
		{Agent: "unknown2", Task: "b"},
	}}
	output, err := subagentHandler(nil, orch, input, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Mode != "parallel" {
		t.Errorf("mode = %q, want 'parallel'", output.Mode)
	}
	if len(output.Results) != 1 {
		t.Fatalf("expected 1 result (validation), got %d", len(output.Results))
	}
	if output.Results[0].Status != "failed" {
		t.Errorf("status = %q, want 'failed'", output.Results[0].Status)
	}
}

// --- Chain mode tests ---

func TestSubagentChainMode_UnknownAgent(t *testing.T) {
	cfg := config.Defaults()
	agents := []subagent.AgentConfig{
		{Name: "explore", Description: "test", Role: "default"},
	}
	orch := subagent.NewOrchestrator(&cfg, "", agents)

	input := SubagentInput{Chain: []ChainItem{
		{Agent: "explore", Task: "step 1"},
		{Agent: "nonexistent", Task: "step 2"},
	}}
	output, err := subagentHandler(nil, orch, input, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Mode != "chain" {
		t.Errorf("mode = %q, want 'chain'", output.Mode)
	}
	if len(output.Results) != 1 {
		t.Fatalf("expected 1 result (validation error), got %d", len(output.Results))
	}
	if output.Results[0].Status != "failed" {
		t.Errorf("status = %q, want 'failed'", output.Results[0].Status)
	}
	if output.Results[0].Agent != "nonexistent" {
		t.Errorf("agent = %q, want 'nonexistent'", output.Results[0].Agent)
	}
}

func TestSubagentChainMode_TooManySteps(t *testing.T) {
	cfg := config.Defaults()
	orch := subagent.NewOrchestrator(&cfg, "", nil)

	chain := make([]ChainItem, maxChainSteps+1)
	for i := range chain {
		chain[i] = ChainItem{Agent: "explore", Task: "a"}
	}
	input := SubagentInput{Chain: chain}
	output, err := subagentHandler(nil, orch, input, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Mode != "chain" {
		t.Errorf("mode = %q, want 'chain'", output.Mode)
	}
	if len(output.Results) != 1 {
		t.Fatalf("expected 1 result (limit error), got %d", len(output.Results))
	}
	if output.Results[0].Status != "failed" {
		t.Errorf("status = %q, want 'failed'", output.Results[0].Status)
	}
	if !strings.Contains(output.Results[0].Error, "too many") {
		t.Errorf("error should mention 'too many', got: %s", output.Results[0].Error)
	}
}

func TestSubagentChainMode_AllUnknownAgents(t *testing.T) {
	cfg := config.Defaults()
	orch := subagent.NewOrchestrator(&cfg, "", nil)

	input := SubagentInput{Chain: []ChainItem{
		{Agent: "unknown1", Task: "step 1"},
		{Agent: "unknown2", Task: "step 2"},
	}}
	output, err := subagentHandler(nil, orch, input, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Mode != "chain" {
		t.Errorf("mode = %q, want 'chain'", output.Mode)
	}
	if len(output.Results) != 1 {
		t.Fatalf("expected 1 result (validation), got %d", len(output.Results))
	}
	if output.Results[0].Status != "failed" {
		t.Errorf("status = %q, want 'failed'", output.Results[0].Status)
	}
}

// --- Event callback tests ---

func TestEmitEvent_NilCallback(t *testing.T) {
	// Should not panic with nil callback.
	emitEvent(nil, SubagentEvent{AgentID: "test", Kind: "spawn"})
}

func TestEmitEvent_CallsCallback(t *testing.T) {
	var mu sync.Mutex
	var received []SubagentEvent

	cb := func(ev SubagentEvent) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, ev)
	}

	emitEvent(cb, SubagentEvent{AgentID: "test-1", Kind: "spawn", PipelineID: "p-1", Mode: "single", Step: 1, Total: 1})

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	ev := received[0]
	if ev.AgentID != "test-1" {
		t.Errorf("AgentID = %q, want 'test-1'", ev.AgentID)
	}
	if ev.Kind != "spawn" {
		t.Errorf("Kind = %q, want 'spawn'", ev.Kind)
	}
	if ev.PipelineID != "p-1" {
		t.Errorf("PipelineID = %q, want 'p-1'", ev.PipelineID)
	}
	if ev.Mode != "single" {
		t.Errorf("Mode = %q, want 'single'", ev.Mode)
	}
	if ev.Step != 1 {
		t.Errorf("Step = %d, want 1", ev.Step)
	}
	if ev.Total != 1 {
		t.Errorf("Total = %d, want 1", ev.Total)
	}
}

// --- expandChainTemplate tests ---

func TestExpandChainTemplate(t *testing.T) {
	tests := []struct {
		name     string
		task     string
		prev     string
		expected string
	}{
		{
			name:     "no placeholders",
			task:     "analyze the code",
			prev:     "some result",
			expected: "analyze the code",
		},
		{
			name:     "previous placeholder",
			task:     "review this: {previous}",
			prev:     "hello world",
			expected: "review this: hello world",
		},
		{
			name:     "previous_json placeholder",
			task:     `embed: "{previous_json}"`,
			prev:     "line1\nline2\t\"quoted\"",
			expected: `embed: "line1\nline2\t\"quoted\""`,
		},
		{
			name:     "both placeholders",
			task:     "text: {previous}, json: {previous_json}",
			prev:     "hello\nworld",
			expected: `text: hello` + "\n" + `world, json: hello\nworld`,
		},
		{
			name:     "empty previous",
			task:     "do {previous} stuff",
			prev:     "",
			expected: "do {previous} stuff",
		},
		{
			name:     "multiple occurrences",
			task:     "{previous} and {previous}",
			prev:     "X",
			expected: "X and X",
		},
		{
			name:     "backslash in previous",
			task:     "{previous_json}",
			prev:     `path\to\file`,
			expected: `path\\to\\file`,
		},
		{
			name:     "carriage return in previous",
			task:     "{previous_json}",
			prev:     "line1\r\nline2",
			expected: `line1\r\nline2`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := expandChainTemplate(tt.task, tt.prev)
			if result != tt.expected {
				t.Errorf("expandChainTemplate(%q, %q) = %q, want %q", tt.task, tt.prev, result, tt.expected)
			}
		})
	}
}

// --- buildSubagentDescription tests ---

func TestBuildSubagentDescription(t *testing.T) {
	cfg := config.Defaults()
	agents := []subagent.AgentConfig{
		{Name: "explore", Description: "Fast codebase exploration", Role: "default"},
		{Name: "task", Description: "Complete coding tasks", Role: "default"},
	}
	orch := subagent.NewOrchestrator(&cfg, "", agents)

	desc := buildSubagentDescription(orch)
	if !strings.Contains(desc, "Single") {
		t.Error("description should mention Single mode")
	}
	if !strings.Contains(desc, "Parallel") {
		t.Error("description should mention Parallel mode")
	}
	if !strings.Contains(desc, "Chain") {
		t.Error("description should mention Chain mode")
	}
	// Agent names should be listed (order may vary).
	if !strings.Contains(desc, "explore") {
		t.Error("description should list 'explore' agent")
	}
	if !strings.Contains(desc, "task") {
		t.Error("description should list 'task' agent")
	}
}

// --- SubagentTools registration test ---

func TestSubagentTools_Registration(t *testing.T) {
	cfg := config.Defaults()
	agents := []subagent.AgentConfig{
		{Name: "explore", Description: "test", Role: "default"},
	}
	orch := subagent.NewOrchestrator(&cfg, "", agents)

	tools, err := SubagentTools(orch, nil)
	if err != nil {
		t.Fatalf("SubagentTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name() != "subagent" {
		t.Errorf("expected tool name 'subagent', got %q", tools[0].Name())
	}
}

// --- resolveContext tests ---

func TestResolveContext_Nil(t *testing.T) {
	ctx := resolveContext(nil)
	if ctx == nil {
		t.Error("expected non-nil context from nil input")
	}
}
