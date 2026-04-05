package atif

import (
	"encoding/json"
	"testing"
)

func TestSchemaVersion(t *testing.T) {
	if SchemaVersion != "ATIF-v1.6" {
		t.Fatalf("expected ATIF-v1.6, got %s", SchemaVersion)
	}
}

func TestTrajectoryJSON(t *testing.T) {
	traj := Trajectory{
		SchemaVersion: SchemaVersion,
		SessionID:     "test-session-123",
		Agent: AgentInfo{
			Name:      "pi-go",
			ModelName: "claude-sonnet-4-20250514",
			Extra:     map[string]any{"work_dir": "/tmp/test"},
		},
		Steps: []Step{
			{
				StepID:    1,
				Timestamp: "2026-04-05T10:30:00.000Z",
				Source:    "user",
				Message:   "Fix the failing test",
			},
			{
				StepID:    2,
				Timestamp: "2026-04-05T10:30:02.500Z",
				Source:    "agent",
				Message:   "Let me read the test file.",
				ToolCalls: []ToolCall{
					{
						ToolCallID:   "call_001",
						FunctionName: "read",
						Arguments:    map[string]any{"path": "auth_test.go"},
					},
				},
			},
			{
				StepID:    3,
				Timestamp: "2026-04-05T10:30:03.100Z",
				Source:    "system",
				Message:   "",
				Observation: &Observation{
					Results: []ObservationResult{
						{
							SourceCallID: "call_001",
							Content:      "package auth\n\nimport \"testing\"\n",
						},
					},
				},
			},
		},
	}

	data, err := json.MarshalIndent(traj, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Round-trip: unmarshal back
	var got Trajectory
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version: got %s, want %s", got.SchemaVersion, SchemaVersion)
	}
	if got.SessionID != "test-session-123" {
		t.Errorf("session_id: got %s", got.SessionID)
	}
	if got.Agent.Name != "pi-go" {
		t.Errorf("agent.name: got %s", got.Agent.Name)
	}
	if got.Agent.ModelName != "claude-sonnet-4-20250514" {
		t.Errorf("agent.model_name: got %s", got.Agent.ModelName)
	}
	if len(got.Steps) != 3 {
		t.Fatalf("steps: got %d, want 3", len(got.Steps))
	}

	// Verify step 1 (user message)
	if got.Steps[0].Source != "user" {
		t.Errorf("step 1 source: got %s", got.Steps[0].Source)
	}
	msg, ok := got.Steps[0].Message.(string)
	if !ok {
		t.Fatalf("step 1 message: expected string, got %T", got.Steps[0].Message)
	}
	if msg != "Fix the failing test" {
		t.Errorf("step 1 message: got %s", msg)
	}

	// Verify step 2 (agent with tool calls)
	if got.Steps[1].Source != "agent" {
		t.Errorf("step 2 source: got %s", got.Steps[1].Source)
	}
	if len(got.Steps[1].ToolCalls) != 1 {
		t.Fatalf("step 2 tool_calls: got %d", len(got.Steps[1].ToolCalls))
	}
	tc := got.Steps[1].ToolCalls[0]
	if tc.ToolCallID != "call_001" {
		t.Errorf("tool_call_id: got %s", tc.ToolCallID)
	}
	if tc.FunctionName != "read" {
		t.Errorf("function_name: got %s", tc.FunctionName)
	}

	// Verify step 3 (observation)
	if got.Steps[2].Observation == nil {
		t.Fatal("step 3: observation is nil")
	}
	if len(got.Steps[2].Observation.Results) != 1 {
		t.Fatalf("observation results: got %d", len(got.Steps[2].Observation.Results))
	}
	if got.Steps[2].Observation.Results[0].SourceCallID != "call_001" {
		t.Errorf("source_call_id: got %s", got.Steps[2].Observation.Results[0].SourceCallID)
	}
}

func TestOmitemptyFields(t *testing.T) {
	// Minimal trajectory - optional fields should be omitted
	traj := Trajectory{
		SchemaVersion: SchemaVersion,
		SessionID:     "minimal",
		Agent:         AgentInfo{Name: "test"},
		Steps:         []Step{},
	}

	data, err := json.Marshal(traj)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	// These optional fields should NOT be present
	for _, key := range []string{"notes", "final_metrics", "continued_trajectory_ref", "extra"} {
		if _, ok := raw[key]; ok {
			t.Errorf("expected %s to be omitted, but it was present", key)
		}
	}

	// Required fields MUST be present
	for _, key := range []string{"schema_version", "session_id", "agent", "steps"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("expected required field %s to be present", key)
		}
	}
}

func TestStepOmitemptyFields(t *testing.T) {
	step := Step{
		StepID:  1,
		Source:  "user",
		Message: "hello",
	}

	data, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	// These optional fields should NOT be present
	for _, key := range []string{
		"timestamp", "model_name", "reasoning_effort", "reasoning_content",
		"tool_calls", "observation", "is_copied_context", "metrics", "extra",
	} {
		if _, ok := raw[key]; ok {
			t.Errorf("expected %s to be omitted, but it was present", key)
		}
	}

	// Required fields
	for _, key := range []string{"step_id", "source", "message"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("expected required field %s to be present", key)
		}
	}
}

func TestMessageAsContentPartArray(t *testing.T) {
	step := Step{
		StepID: 1,
		Source: "agent",
		Message: []ContentPart{
			{Type: "text", Text: "Here is the image:"},
			{Type: "image_url", ImageURL: "https://example.com/img.png"},
		},
	}

	data, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}

	var got Step
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	parts, ok := got.Message.([]any)
	if !ok {
		t.Fatalf("expected message to be array, got %T", got.Message)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
}

func TestSubagentTrajectoryRef(t *testing.T) {
	obs := ObservationResult{
		SourceCallID:          "call_sub",
		Content:               "subagent result",
		SubagentTrajectoryRef: "../sub-session/trajectory.atif.json",
	}

	data, err := json.Marshal(obs)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	if raw["subagent_trajectory_ref"] != "../sub-session/trajectory.atif.json" {
		t.Errorf("subagent_trajectory_ref: got %v", raw["subagent_trajectory_ref"])
	}

	// Without ref, it should be omitted
	obs2 := ObservationResult{
		SourceCallID: "call_1",
		Content:      "result",
	}
	data2, _ := json.Marshal(obs2)
	var raw2 map[string]any
	json.Unmarshal(data2, &raw2)
	if _, ok := raw2["subagent_trajectory_ref"]; ok {
		t.Error("subagent_trajectory_ref should be omitted when empty")
	}
}

func TestMetricsJSON(t *testing.T) {
	m := Metrics{
		PromptTokens:     100,
		CompletionTokens: 50,
		CostUSD:          0.003,
	}

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}

	var got Metrics
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	if got.PromptTokens != 100 {
		t.Errorf("prompt_tokens: got %d", got.PromptTokens)
	}
	if got.CompletionTokens != 50 {
		t.Errorf("completion_tokens: got %d", got.CompletionTokens)
	}
	if got.CostUSD != 0.003 {
		t.Errorf("cost_usd: got %f", got.CostUSD)
	}

	// Zero metrics should omit fields
	empty := Metrics{}
	data2, _ := json.Marshal(empty)
	var raw map[string]any
	json.Unmarshal(data2, &raw)
	for _, key := range []string{"prompt_tokens", "completion_tokens", "cached_tokens", "cost_usd", "extra"} {
		if _, ok := raw[key]; ok {
			t.Errorf("expected %s to be omitted for zero metrics", key)
		}
	}
}

func TestJSONFieldNames(t *testing.T) {
	// Verify snake_case JSON field names match ATIF spec exactly
	traj := Trajectory{
		SchemaVersion: "ATIF-v1.6",
		SessionID:     "s1",
		Agent: AgentInfo{
			Name:      "test",
			Version:   "1.0",
			ModelName: "gpt-4",
		},
		Steps: []Step{
			{
				StepID:    1,
				Source:    "agent",
				Message:   "text",
				ModelName: "gpt-4",
				ToolCalls: []ToolCall{
					{
						ToolCallID:   "tc1",
						FunctionName: "bash",
						Arguments:    map[string]any{"cmd": "ls"},
					},
				},
			},
		},
	}

	data, err := json.Marshal(traj)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	json.Unmarshal(data, &raw)

	// Check top-level field names
	expectedTopLevel := []string{"schema_version", "session_id", "agent", "steps"}
	for _, key := range expectedTopLevel {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing top-level field %q in JSON", key)
		}
	}

	// Check agent field names
	agent := raw["agent"].(map[string]any)
	if _, ok := agent["model_name"]; !ok {
		t.Error("missing agent.model_name")
	}

	// Check step field names
	steps := raw["steps"].([]any)
	step := steps[0].(map[string]any)
	if _, ok := step["step_id"]; !ok {
		t.Error("missing step.step_id")
	}
	if _, ok := step["model_name"]; !ok {
		t.Error("missing step.model_name")
	}

	// Check tool_call field names
	tcs := step["tool_calls"].([]any)
	tc := tcs[0].(map[string]any)
	if _, ok := tc["tool_call_id"]; !ok {
		t.Error("missing tool_call.tool_call_id")
	}
	if _, ok := tc["function_name"]; !ok {
		t.Error("missing tool_call.function_name")
	}
}
