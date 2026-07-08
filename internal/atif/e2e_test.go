//go:build e2e

package atif

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// TestE2EFullConversation simulates a realistic multi-turn conversation:
// user message → agent response with tool calls → tool results → agent follow-up →
// user message → agent final response, then validates the ATIF output end-to-end.
func TestE2EFullConversation(t *testing.T) {
	dir := t.TempDir()
	parentDir := filepath.Join(dir, "session-e2e")
	subDir := filepath.Join(dir, "sub-session-abc")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	fp := filepath.Join(parentDir, "trajectory.atif.json")
	w := NewWriter(fp, SessionMeta{
		SessionID: "session-e2e",
		AgentName: "pi-go",
		Model:     "claude-sonnet-4-6",
		WorkDir:   "/home/user/project",
	})

	ts := time.Date(2026, 4, 5, 14, 0, 0, 0, time.UTC)

	// Step 1: User sends a message.
	ev1 := &session.Event{}
	ev1.Author = "user"
	ev1.Timestamp = ts
	ev1.Content = &genai.Content{Parts: []*genai.Part{
		{Text: "Read main.go and explain what it does"},
	}}
	mustAppend(t, w, ev1)

	// Step 2: Agent responds with tool call.
	ts = ts.Add(time.Second)
	ev2 := &session.Event{}
	ev2.Author = "model"
	ev2.Timestamp = ts
	ev2.Content = &genai.Content{Parts: []*genai.Part{
		{Text: "I'll read that file for you."},
		{FunctionCall: &genai.FunctionCall{
			ID:   "call_read_1",
			Name: "read",
			Args: map[string]any{"path": "main.go"},
		}},
	}}
	mustAppend(t, w, ev2)

	// Step 3: Tool result.
	ts = ts.Add(time.Second)
	ev3 := &session.Event{}
	ev3.Author = "tool"
	ev3.Timestamp = ts
	ev3.Content = &genai.Content{Parts: []*genai.Part{
		{FunctionResponse: &genai.FunctionResponse{
			ID:       "call_read_1",
			Name:     "read",
			Response: map[string]any{"content": "package main\n\nfunc main() { fmt.Println(\"hello\") }"},
		}},
	}}
	mustAppend(t, w, ev3)

	// Step 4: Agent explains and invokes subagent.
	ts = ts.Add(time.Second)
	ev4 := &session.Event{}
	ev4.Author = "model"
	ev4.Timestamp = ts
	ev4.Content = &genai.Content{Parts: []*genai.Part{
		{Text: "This is a simple hello-world program. Let me explore for related tests."},
		{FunctionCall: &genai.FunctionCall{
			ID:   "call_sub_1",
			Name: "subagent",
			Args: map[string]any{"agent": "explore", "task": "find test files"},
		}},
	}}
	mustAppend(t, w, ev4)

	// Step 5: Subagent tool result with session_id.
	ts = ts.Add(2 * time.Second)
	ev5 := &session.Event{}
	ev5.Author = "tool"
	ev5.Timestamp = ts
	ev5.Content = &genai.Content{Parts: []*genai.Part{
		{FunctionResponse: &genai.FunctionResponse{
			ID:   "call_sub_1",
			Name: "subagent",
			Response: map[string]any{
				"mode": "single",
				"results": []any{
					map[string]any{
						"agent":      "explore",
						"status":     "completed",
						"result":     "found main_test.go",
						"session_id": "sub-session-abc",
					},
				},
			},
		}},
	}}
	mustAppend(t, w, ev5)

	// Link subagent trajectory.
	w.LinkSubagentTrajectories(ev5, parentDir, dir)

	// Step 6: User asks follow-up.
	ts = ts.Add(time.Second)
	ev6 := &session.Event{}
	ev6.Author = "user"
	ev6.Timestamp = ts
	ev6.Content = &genai.Content{Parts: []*genai.Part{
		{Text: "Thanks, can you also run the tests?"},
	}}
	mustAppend(t, w, ev6)

	// Step 7: Agent final response.
	ts = ts.Add(time.Second)
	ev7 := &session.Event{}
	ev7.Author = "model"
	ev7.Timestamp = ts
	ev7.Content = &genai.Content{Parts: []*genai.Part{
		{Text: "All tests pass. The project has 100% coverage on main.go."},
	}}
	mustAppend(t, w, ev7)

	// Also create the subagent's own ATIF file.
	subWriter := NewWriter(filepath.Join(subDir, "trajectory.atif.json"), SessionMeta{
		SessionID: "sub-session-abc",
		AgentName: "explore",
		Model:     "claude-sonnet-4-6",
	})
	subEv1 := &session.Event{}
	subEv1.Author = "user"
	subEv1.Timestamp = ts
	subEv1.Content = &genai.Content{Parts: []*genai.Part{{Text: "find test files"}}}
	mustAppend(t, subWriter, subEv1)
	subEv2 := &session.Event{}
	subEv2.Author = "model"
	subEv2.Timestamp = ts
	subEv2.Content = &genai.Content{Parts: []*genai.Part{{Text: "found main_test.go"}}}
	mustAppend(t, subWriter, subEv2)

	// --- VALIDATION ---

	// Read back and parse.
	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("read trajectory: %v", err)
	}
	if !json.Valid(data) {
		t.Fatal("trajectory file is not valid JSON")
	}

	var traj Trajectory
	if err := json.Unmarshal(data, &traj); err != nil {
		t.Fatalf("unmarshal trajectory: %v", err)
	}

	// 1. Required top-level fields.
	t.Run("RequiredFields", func(t *testing.T) {
		if traj.SchemaVersion != SchemaVersion {
			t.Errorf("schema_version = %q, want %q", traj.SchemaVersion, SchemaVersion)
		}
		if traj.SessionID != "session-e2e" {
			t.Errorf("session_id = %q, want %q", traj.SessionID, "session-e2e")
		}
		if traj.Agent.Name != "pi-go" {
			t.Errorf("agent.name = %q, want %q", traj.Agent.Name, "pi-go")
		}
		if traj.Agent.ModelName != "claude-sonnet-4-6" {
			t.Errorf("agent.model_name = %q, want %q", traj.Agent.ModelName, "claude-sonnet-4-6")
		}
		if len(traj.Steps) == 0 {
			t.Fatal("steps should not be empty")
		}
	})

	// 2. Step count: 7 events should produce 7 steps.
	t.Run("StepCount", func(t *testing.T) {
		if len(traj.Steps) != 7 {
			t.Fatalf("steps = %d, want 7", len(traj.Steps))
		}
	})

	// 3. Sequential step_id ordering starting from 1.
	t.Run("StepIDOrdering", func(t *testing.T) {
		for i, step := range traj.Steps {
			want := i + 1
			if step.StepID != want {
				t.Errorf("step[%d].step_id = %d, want %d", i, step.StepID, want)
			}
		}
	})

	// 4. Source values are only "user", "agent", or "system".
	t.Run("ValidSources", func(t *testing.T) {
		validSources := map[string]bool{"user": true, "agent": true, "system": true}
		for i, step := range traj.Steps {
			if !validSources[step.Source] {
				t.Errorf("step[%d].source = %q, not a valid ATIF source", i, step.Source)
			}
		}
	})

	// 5. Expected source sequence.
	t.Run("SourceSequence", func(t *testing.T) {
		wantSources := []string{"user", "agent", "system", "agent", "system", "user", "agent"}
		for i, step := range traj.Steps {
			if step.Source != wantSources[i] {
				t.Errorf("step[%d].source = %q, want %q", i, step.Source, wantSources[i])
			}
		}
	})

	// 6. Timestamps present and non-empty on all steps.
	t.Run("Timestamps", func(t *testing.T) {
		for i, step := range traj.Steps {
			if step.Timestamp == "" {
				t.Errorf("step[%d].timestamp is empty", i)
			}
			// Verify parseable as RFC3339.
			if _, err := time.Parse(time.RFC3339Nano, step.Timestamp); err != nil {
				t.Errorf("step[%d].timestamp %q is not valid RFC3339: %v", i, step.Timestamp, err)
			}
		}
	})

	// 7. Tool call present in step 2 (agent with read tool).
	t.Run("ToolCallPresent", func(t *testing.T) {
		step := traj.Steps[1] // step_id 2
		if len(step.ToolCalls) != 1 {
			t.Fatalf("step 2 tool_calls = %d, want 1", len(step.ToolCalls))
		}
		tc := step.ToolCalls[0]
		if tc.ToolCallID != "call_read_1" {
			t.Errorf("tool_call_id = %q, want %q", tc.ToolCallID, "call_read_1")
		}
		if tc.FunctionName != "read" {
			t.Errorf("function_name = %q, want %q", tc.FunctionName, "read")
		}
	})

	// 8. Observation present in step 3 (tool result for read).
	t.Run("ObservationPresent", func(t *testing.T) {
		step := traj.Steps[2] // step_id 3
		if step.Observation == nil {
			t.Fatal("step 3 should have observation")
		}
		if len(step.Observation.Results) != 1 {
			t.Fatalf("step 3 observation.results = %d, want 1", len(step.Observation.Results))
		}
	})

	// 9. Tool call ↔ observation linkage: every source_call_id matches a prior tool_call_id.
	t.Run("ToolCallObservationLinkage", func(t *testing.T) {
		toolCallIDs := make(map[string]bool)
		for _, step := range traj.Steps {
			// Collect tool_call_ids.
			for _, tc := range step.ToolCalls {
				toolCallIDs[tc.ToolCallID] = true
			}
			// Verify observation references.
			if step.Observation != nil {
				for _, r := range step.Observation.Results {
					if !toolCallIDs[r.SourceCallID] {
						t.Errorf("source_call_id %q not found in prior tool_call_ids", r.SourceCallID)
					}
				}
			}
		}
	})

	// 10. Subagent trajectory ref is set and relative.
	t.Run("SubagentTrajectoryRef", func(t *testing.T) {
		step := traj.Steps[4] // step_id 5, subagent result
		if step.Observation == nil {
			t.Fatal("step 5 should have observation")
		}
		found := false
		for _, r := range step.Observation.Results {
			if r.SubagentTrajectoryRef != "" {
				found = true
				if filepath.IsAbs(r.SubagentTrajectoryRef) {
					t.Errorf("subagent_trajectory_ref should be relative, got: %s", r.SubagentTrajectoryRef)
				}
				// Verify the path resolves to the subagent dir.
				resolved := filepath.Join(parentDir, r.SubagentTrajectoryRef)
				expected := filepath.Join(subDir, "trajectory.atif.json")
				if resolved != expected {
					t.Errorf("resolved = %s, want %s", resolved, expected)
				}
			}
		}
		if !found {
			t.Error("subagent_trajectory_ref not found in step 5")
		}
	})

	// 11. Subagent's own ATIF is a valid standalone document.
	t.Run("SubagentStandaloneValid", func(t *testing.T) {
		subData, err := os.ReadFile(filepath.Join(subDir, "trajectory.atif.json"))
		if err != nil {
			t.Fatal(err)
		}
		var subTraj Trajectory
		if err := json.Unmarshal(subData, &subTraj); err != nil {
			t.Fatalf("subagent trajectory invalid: %v", err)
		}
		if subTraj.SchemaVersion != SchemaVersion {
			t.Errorf("subagent schema_version = %q", subTraj.SchemaVersion)
		}
		if subTraj.SessionID != "sub-session-abc" {
			t.Errorf("subagent session_id = %q", subTraj.SessionID)
		}
		if len(subTraj.Steps) != 2 {
			t.Errorf("subagent steps = %d, want 2", len(subTraj.Steps))
		}
	})

	// 12. File size sanity check.
	t.Run("FileSizeSanity", func(t *testing.T) {
		info, err := os.Stat(fp)
		if err != nil {
			t.Fatal(err)
		}
		// 7 steps with tool calls should be > 500 bytes and < 50KB.
		if info.Size() < 500 {
			t.Errorf("file too small: %d bytes", info.Size())
		}
		if info.Size() > 50*1024 {
			t.Errorf("file too large: %d bytes", info.Size())
		}
	})

	// 13. Raw JSON field names match ATIF spec.
	t.Run("JSONFieldNames", func(t *testing.T) {
		var raw map[string]any
		json.Unmarshal(data, &raw)

		requiredFields := []string{"schema_version", "session_id", "agent", "steps"}
		for _, f := range requiredFields {
			if _, ok := raw[f]; !ok {
				t.Errorf("missing top-level field %q", f)
			}
		}

		// Check step field names.
		steps := raw["steps"].([]any)
		step0 := steps[0].(map[string]any)
		for _, f := range []string{"step_id", "timestamp", "source", "message"} {
			if _, ok := step0[f]; !ok {
				t.Errorf("missing step field %q", f)
			}
		}
	})

	// 14. Messages are non-empty for content-bearing steps.
	t.Run("MessagesNonEmpty", func(t *testing.T) {
		contentSteps := []int{0, 1, 3, 5, 6} // steps with text content
		for _, idx := range contentSteps {
			msg := traj.Steps[idx].Message
			if msg == nil || msg == "" {
				t.Errorf("step[%d].message should not be empty", idx)
			}
		}
	})
}

// TestE2EMalformedEventResilience ensures malformed events don't crash the pipeline.
func TestE2EMalformedEventResilience(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	w := NewWriter(fp, SessionMeta{SessionID: "sess-malformed", AgentName: "pi-go"})

	// Good event first.
	mustAppend(t, w, newTestEvent("user", "hello"))

	// nil event.
	if err := w.AppendEvent(nil); err != nil {
		t.Fatalf("nil event should not error: %v", err)
	}

	// Event with nil content.
	if err := w.AppendEvent(&session.Event{Author: "model"}); err != nil {
		t.Fatalf("nil content should not error: %v", err)
	}

	// Event with empty parts.
	ev := &session.Event{}
	ev.Author = "model"
	ev.Timestamp = time.Now()
	ev.Content = &genai.Content{Parts: []*genai.Part{}}
	if err := w.AppendEvent(ev); err != nil {
		t.Fatalf("empty parts should not error: %v", err)
	}

	// Event with only thought parts (should be filtered).
	evThought := &session.Event{}
	evThought.Author = "model"
	evThought.Timestamp = time.Now()
	evThought.Content = &genai.Content{Parts: []*genai.Part{
		{Text: "thinking...", Thought: true},
	}}
	if err := w.AppendEvent(evThought); err != nil {
		t.Fatalf("thought-only event should not error: %v", err)
	}

	// Event with nil FunctionCall args.
	evNilArgs := &session.Event{}
	evNilArgs.Author = "model"
	evNilArgs.Timestamp = time.Now()
	evNilArgs.Content = &genai.Content{Parts: []*genai.Part{
		{FunctionCall: &genai.FunctionCall{
			ID:   "call_nil",
			Name: "test",
			Args: nil,
		}},
	}}
	mustAppend(t, w, evNilArgs)

	// Good event after all the malformed ones.
	mustAppend(t, w, newTestEvent("model", "response"))

	// Verify file is valid and has exactly 3 steps (hello, nil-args tool call, response).
	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatal("file is not valid JSON after malformed events")
	}
	var traj Trajectory
	if err := json.Unmarshal(data, &traj); err != nil {
		t.Fatal(err)
	}
	if len(traj.Steps) != 3 {
		t.Errorf("steps = %d, want 3 (good + nil-args-tool + good)", len(traj.Steps))
	}
	// Step IDs should be sequential.
	for i, step := range traj.Steps {
		if step.StepID != i+1 {
			t.Errorf("step[%d].step_id = %d, want %d", i, step.StepID, i+1)
		}
	}
}

// TestE2ESessionResumeEndToEnd simulates creating a session, persisting events,
// then loading them into a fresh writer to verify resume produces identical output.
func TestE2ESessionResumeEndToEnd(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	meta := SessionMeta{
		SessionID: "sess-resume",
		AgentName: "pi-go",
		Model:     "claude-sonnet-4-6",
	}

	// Phase 1: Original session with 4 events.
	w1 := NewWriter(fp, meta)
	originalEvents := []*session.Event{
		newTestEvent("user", "first question"),
		newTestEvent("model", "first answer"),
		newTestEvent("user", "second question"),
		newTestEvent("model", "second answer"),
	}
	for _, ev := range originalEvents {
		mustAppend(t, w1, ev)
	}

	// Phase 2: Simulate resume by creating a new writer and replaying events.
	w2 := NewWriter(fp, meta)
	for _, ev := range originalEvents {
		if err := w2.AppendEvent(ev); err != nil {
			t.Fatalf("replay event: %v", err)
		}
	}

	// Phase 3: Add new events after resume.
	newEvents := []*session.Event{
		newTestEvent("user", "third question"),
		newTestEvent("model", "third answer"),
	}
	for _, ev := range newEvents {
		mustAppend(t, w2, ev)
	}

	// Validate.
	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatal(err)
	}
	var traj Trajectory
	if err := json.Unmarshal(data, &traj); err != nil {
		t.Fatal(err)
	}

	// Should have 6 steps total with sequential IDs.
	if len(traj.Steps) != 6 {
		t.Fatalf("steps = %d, want 6", len(traj.Steps))
	}
	for i, step := range traj.Steps {
		if step.StepID != i+1 {
			t.Errorf("step[%d].step_id = %d, want %d", i, step.StepID, i+1)
		}
	}

	// Verify alternating sources.
	for i, step := range traj.Steps {
		want := "user"
		if i%2 == 1 {
			want = "agent"
		}
		if step.Source != want {
			t.Errorf("step[%d].source = %q, want %q", i, step.Source, want)
		}
	}
}

func mustAppend(t *testing.T, w *Writer, ev *session.Event) {
	t.Helper()
	if err := w.AppendEvent(ev); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
}
