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

func TestDetectMode_SingleWithOnlyAgent(t *testing.T) {
	// Lenient: single mode should work with just agent (task might be empty)
	// This helps recover from LLM mistakes where task isn't provided
	input := SubagentInput{Agent: "explore"}
	if mode := detectMode(input); mode != "single" {
		t.Errorf("detectMode = %q, want 'single' (lenient with only agent)", mode)
	}
}

func TestDetectMode_SingleWithOnlyTask(t *testing.T) {
	// Lenient: single mode should work with just task (agent might be empty)
	// This helps recover from LLM mistakes where agent isn't provided
	input := SubagentInput{Task: "some task"}
	if mode := detectMode(input); mode != "single" {
		t.Errorf("detectMode = %q, want 'single' (lenient with only task)", mode)
	}
}

// --- Single mode tests ---

func TestSubagentSingleMode_UnknownAgent(t *testing.T) {
	agents := []subagent.AgentConfig{
		{Name: "explore", Description: "test", Role: "default"},
	}
	orch := subagent.NewOrchestrator(new(config.Defaults()), "", agents)

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
	orch := subagent.NewOrchestrator(new(config.Defaults()), "", nil)

	input := SubagentInput{} // empty — no mode
	_, err := subagentHandler(nil, orch, input, nil)
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

// --- Parallel mode tests ---

func TestSubagentParallelMode_UnknownAgent(t *testing.T) {
	agents := []subagent.AgentConfig{
		{Name: "explore", Description: "test", Role: "default"},
	}
	orch := subagent.NewOrchestrator(new(config.Defaults()), "", agents)

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
	orch := subagent.NewOrchestrator(new(config.Defaults()), "", nil)

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
	orch := subagent.NewOrchestrator(new(config.Defaults()), "", nil)

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
	agents := []subagent.AgentConfig{
		{Name: "explore", Description: "test", Role: "default"},
	}
	orch := subagent.NewOrchestrator(new(config.Defaults()), "", agents)

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
	orch := subagent.NewOrchestrator(new(config.Defaults()), "", nil)

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
	orch := subagent.NewOrchestrator(new(config.Defaults()), "", nil)

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
	agents := []subagent.AgentConfig{
		{Name: "explore", Description: "Fast codebase exploration", Role: "default"},
		{Name: "task", Description: "Complete coding tasks", Role: "default"},
	}
	orch := subagent.NewOrchestrator(new(config.Defaults()), "", agents)

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
	agents := []subagent.AgentConfig{
		{Name: "explore", Description: "test", Role: "default"},
	}
	orch := subagent.NewOrchestrator(new(config.Defaults()), "", agents)

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

// --- consumeAgentEvents tests ---

func TestConsumeAgentEvents_TextOnly(t *testing.T) {
	ch := make(chan subagent.Event, 3)
	ch <- subagent.Event{Type: "text_delta", Content: "Hello "}
	ch <- subagent.Event{Type: "text_delta", Content: "world"}
	close(ch)

	text, status, errMsg := consumeAgentEvents(ch)
	if status != "completed" {
		t.Errorf("status = %q, want completed", status)
	}
	if errMsg != "" {
		t.Errorf("errMsg = %q, want empty", errMsg)
	}
	if text != "Hello world" {
		t.Errorf("text = %q, want %q", text, "Hello world")
	}
}

func TestConsumeAgentEvents_WithError(t *testing.T) {
	ch := make(chan subagent.Event, 3)
	ch <- subagent.Event{Type: "text_delta", Content: "partial"}
	ch <- subagent.Event{Type: "error", Error: "timeout"}
	close(ch)

	_, status, errMsg := consumeAgentEvents(ch)
	if status != "failed" {
		t.Errorf("status = %q, want failed", status)
	}
	if errMsg != "timeout" {
		t.Errorf("errMsg = %q, want timeout", errMsg)
	}
}

func TestConsumeAgentEvents_Empty(t *testing.T) {
	ch := make(chan subagent.Event)
	close(ch)

	text, status, errMsg := consumeAgentEvents(ch)
	if status != "completed" {
		t.Errorf("status = %q, want completed", status)
	}
	if text != "" {
		t.Errorf("text = %q, want empty", text)
	}
	if errMsg != "" {
		t.Errorf("errMsg = %q, want empty", errMsg)
	}
}

// --- buildParallelSummary tests ---

func TestBuildParallelSummary_AllCompleted(t *testing.T) {
	results := []AgentResult{
		{Status: "completed"},
		{Status: "completed"},
		{Status: "completed"},
	}
	got := buildParallelSummary(results, 3, "5s")
	if got != "parallel: 3/3 completed in 5s" {
		t.Errorf("got %q", got)
	}
}

func TestBuildParallelSummary_WithFailures(t *testing.T) {
	results := []AgentResult{
		{Status: "completed"},
		{Status: "failed"},
		{Status: "completed"},
	}
	got := buildParallelSummary(results, 3, "10s")
	if !strings.Contains(got, "2/3 completed") {
		t.Errorf("missing completed count in %q", got)
	}
	if !strings.Contains(got, "1 failed") {
		t.Errorf("missing failed count in %q", got)
	}
}

// --- buildChainSummary tests ---

func TestBuildChainSummary_AllCompleted(t *testing.T) {
	results := []AgentResult{
		{Status: "completed"},
		{Status: "completed"},
	}
	got := buildChainSummary(results, 2, "3s")
	if got != "chain: 2/2 steps completed in 3s" {
		t.Errorf("got %q", got)
	}
}

func TestBuildChainSummary_StoppedEarly(t *testing.T) {
	results := []AgentResult{
		{Status: "completed"},
		{Status: "failed"},
	}
	got := buildChainSummary(results, 4, "7s")
	if !strings.Contains(got, "stopped at step 2/4") {
		t.Errorf("got %q", got)
	}
}

// --- NewSubagentTool tests ---

func TestNewSubagentTool(t *testing.T) {
	agents := []subagent.AgentConfig{
		{Name: "explore", Description: "test", Role: "default"},
	}
	orch := subagent.NewOrchestrator(new(config.Defaults()), "", agents)

	tool, err := NewSubagentTool(orch, nil)
	if err != nil {
		t.Fatalf("NewSubagentTool() error = %v", err)
	}
	if tool == nil {
		t.Fatal("NewSubagentTool() returned nil tool")
	}
	if tool.Name() != "subagent" {
		t.Errorf("Name() = %q, want 'subagent'", tool.Name())
	}
}

// --- expandChainTemplate edge cases ---

func TestExpandChainTemplate_NewlinesAndSpecialChars(t *testing.T) {
	tests := []struct {
		name     string
		task     string
		prev     string
		expected string
	}{
		{
			name:     "tab character",
			task:     "data: {previous}",
			prev:     "col1\tcol2",
			expected: "data: col1\tcol2",
		},
		{
			name:     "quote in json",
			task:     `{json: "{previous_json}"}`,
			prev:     `say "hello"`,
			expected: `{json: "say \"hello\""}`,
		},
		{
			name:     "unicode placeholder",
			task:     "result: {previous}",
			prev:     "日本語",
			expected: "result: 日本語",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := expandChainTemplate(tt.task, tt.prev)
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

// --- detectMode edge cases ---

func TestDetectMode_ParallelOverridesSingle(t *testing.T) {
	// When both agent/task and tasks are provided, tasks should win
	input := SubagentInput{
		Agent: "explore",
		Task:  "some task",
		Tasks: []TaskItem{{Agent: "task", Task: "b"}},
	}
	if mode := detectMode(input); mode != "parallel" {
		t.Errorf("detectMode = %q, want 'parallel'", mode)
	}
}

func TestDetectMode_ChainOverridesBoth(t *testing.T) {
	input := SubagentInput{
		Agent: "explore",
		Task:  "some task",
		Tasks: []TaskItem{{Agent: "task", Task: "b"}},
		Chain: []ChainItem{{Agent: "task", Task: "c"}},
	}
	if mode := detectMode(input); mode != "chain" {
		t.Errorf("detectMode = %q, want 'chain'", mode)
	}
}

// --- SubagentEvent tests ---

func TestSubagentEvent(t *testing.T) {
	ev := SubagentEvent{
		AgentID:    "agent-123",
		Kind:       "spawn",
		Content:    "explore",
		PipelineID: "pipe-456",
		Mode:       "single",
		Step:       1,
		Total:      1,
	}

	if ev.AgentID != "agent-123" {
		t.Errorf("AgentID = %q", ev.AgentID)
	}
	if ev.Kind != "spawn" {
		t.Errorf("Kind = %q", ev.Kind)
	}
	if ev.PipelineID != "pipe-456" {
		t.Errorf("PipelineID = %q", ev.PipelineID)
	}
	if ev.Mode != "single" {
		t.Errorf("Mode = %q", ev.Mode)
	}
	if ev.Step != 1 {
		t.Errorf("Step = %d", ev.Step)
	}
	if ev.Total != 1 {
		t.Errorf("Total = %d", ev.Total)
	}
}

func TestSubagentOutput(t *testing.T) {
	output := SubagentOutput{
		Mode: "single",
		Results: []AgentResult{
			{
				Agent:    "explore",
				AgentID:  "id-1",
				Status:   "completed",
				Result:   "analysis complete",
				Duration: "5s",
			},
		},
		Summary: "explore completed in 5s",
	}

	if output.Mode != "single" {
		t.Errorf("Mode = %q", output.Mode)
	}
	if len(output.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(output.Results))
	}
	if output.Results[0].Status != "completed" {
		t.Errorf("Status = %q", output.Results[0].Status)
	}
	if output.Results[0].Result != "analysis complete" {
		t.Errorf("Result = %q", output.Results[0].Result)
	}
}

func TestAgentResult(t *testing.T) {
	result := AgentResult{
		Agent:     "task",
		AgentID:   "id-2",
		Status:    "failed",
		Result:    "",
		Error:     "connection refused",
		Duration:  "1s",
		SessionID: "session-123",
	}

	if result.Agent != "task" {
		t.Errorf("Agent = %q", result.Agent)
	}
	if result.Status != "failed" {
		t.Errorf("Status = %q", result.Status)
	}
	if result.Error != "connection refused" {
		t.Errorf("Error = %q", result.Error)
	}
	if result.SessionID != "session-123" {
		t.Errorf("SessionID = %q", result.SessionID)
	}
}

// --- subagentHandler error mode tests ---

func TestSubagentHandler_AmbiguousInput(t *testing.T) {
	orch := subagent.NewOrchestrator(new(config.Defaults()), "", nil)

	// Both agent AND tasks provided - should be parallel mode
	input := SubagentInput{
		Agent: "someagent",
		Tasks: []TaskItem{{Agent: "a", Task: "b"}},
	}
	_, err := subagentHandler(nil, orch, input, nil)
	// This should error for unknown agent "someagent" (in parallel validation)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
