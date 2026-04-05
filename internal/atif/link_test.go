package atif

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func makeToolCallEvent(callID, toolName string, args map[string]any) *session.Event {
	ev := &session.Event{}
	ev.Author = "model"
	ev.Timestamp = time.Now()
	ev.Content = &genai.Content{
		Parts: []*genai.Part{
			{Text: "Spawning subagent."},
			{FunctionCall: &genai.FunctionCall{
				ID:   callID,
				Name: toolName,
				Args: args,
			}},
		},
	}
	return ev
}

func makeToolResponseEvent(callID, toolName string, response map[string]any) *session.Event {
	ev := &session.Event{}
	ev.Author = "tool"
	ev.Timestamp = time.Now()
	ev.Content = &genai.Content{
		Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{
				ID:       callID,
				Name:     toolName,
				Response: response,
			}},
		},
	}
	return ev
}

func TestLinkSubagentTrajectories_SingleMode(t *testing.T) {
	dir := t.TempDir()
	parentDir := filepath.Join(dir, "parent-session")
	subDir := filepath.Join(dir, "sub-session-123")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	w := NewWriter(filepath.Join(parentDir, "trajectory.atif.json"), SessionMeta{
		SessionID: "parent-session",
		AgentName: "pi-go",
	})

	// Append a tool call event.
	if err := w.AppendEvent(makeToolCallEvent("call_001", "subagent", map[string]any{
		"agent": "explore", "task": "find files",
	})); err != nil {
		t.Fatal(err)
	}

	// Append the tool response with session_id in the result.
	responseEvent := makeToolResponseEvent("call_001", "subagent", map[string]any{
		"mode": "single",
		"results": []any{
			map[string]any{
				"agent":      "explore",
				"agent_id":   "explore-123",
				"status":     "completed",
				"result":     "found 5 files",
				"duration":   "1.5s",
				"session_id": "sub-session-123",
			},
		},
		"summary": "explore completed in 1.5s",
	})
	if err := w.AppendEvent(responseEvent); err != nil {
		t.Fatal(err)
	}

	// Link subagent trajectories.
	w.LinkSubagentTrajectories(responseEvent, parentDir, dir)

	// Read back and verify subagent_trajectory_ref is set.
	data, err := os.ReadFile(filepath.Join(parentDir, "trajectory.atif.json"))
	if err != nil {
		t.Fatal(err)
	}

	var traj Trajectory
	if err := json.Unmarshal(data, &traj); err != nil {
		t.Fatalf("invalid trajectory JSON: %v", err)
	}

	// Find the observation step with the linked ref.
	var found bool
	for _, step := range traj.Steps {
		if step.Observation == nil {
			continue
		}
		for _, r := range step.Observation.Results {
			if r.SourceCallID == "call_001" && r.SubagentTrajectoryRef != "" {
				found = true
				if filepath.IsAbs(r.SubagentTrajectoryRef) {
					t.Errorf("subagent_trajectory_ref should be relative, got: %s", r.SubagentTrajectoryRef)
				}
				resolved := filepath.Join(parentDir, r.SubagentTrajectoryRef)
				expected := filepath.Join(subDir, "trajectory.atif.json")
				if resolved != expected {
					t.Errorf("resolved path = %s, want %s", resolved, expected)
				}
			}
		}
	}
	if !found {
		t.Error("subagent_trajectory_ref not found in any observation result")
	}
}

func TestLinkSubagentTrajectories_NoSessionID(t *testing.T) {
	dir := t.TempDir()
	parentDir := filepath.Join(dir, "parent")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	w := NewWriter(filepath.Join(parentDir, "trajectory.atif.json"), SessionMeta{
		SessionID: "parent",
		AgentName: "pi-go",
	})

	if err := w.AppendEvent(makeToolCallEvent("call_002", "subagent", map[string]any{
		"agent": "explore", "task": "test",
	})); err != nil {
		t.Fatal(err)
	}

	responseEvent := makeToolResponseEvent("call_002", "subagent", map[string]any{
		"mode": "single",
		"results": []any{
			map[string]any{
				"agent":    "explore",
				"status":   "completed",
				"result":   "done",
				"duration": "1s",
			},
		},
	})
	if err := w.AppendEvent(responseEvent); err != nil {
		t.Fatal(err)
	}

	// Link should be a no-op (no session_id).
	w.LinkSubagentTrajectories(responseEvent, parentDir, dir)

	data, err := os.ReadFile(filepath.Join(parentDir, "trajectory.atif.json"))
	if err != nil {
		t.Fatal(err)
	}

	var traj Trajectory
	if err := json.Unmarshal(data, &traj); err != nil {
		t.Fatal(err)
	}

	for _, step := range traj.Steps {
		if step.Observation == nil {
			continue
		}
		for _, r := range step.Observation.Results {
			if r.SubagentTrajectoryRef != "" {
				t.Errorf("expected no subagent_trajectory_ref, got: %s", r.SubagentTrajectoryRef)
			}
		}
	}
}

func TestLinkSubagentTrajectories_NonSubagentTool(t *testing.T) {
	dir := t.TempDir()
	parentDir := filepath.Join(dir, "parent")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	w := NewWriter(filepath.Join(parentDir, "trajectory.atif.json"), SessionMeta{
		SessionID: "parent",
		AgentName: "pi-go",
	})

	event := makeToolResponseEvent("call_003", "read", map[string]any{
		"content": "file contents",
	})
	if err := w.AppendEvent(event); err != nil {
		t.Fatal(err)
	}

	// Should not panic or modify anything.
	w.LinkSubagentTrajectories(event, parentDir, dir)
}

func TestLinkSubagentTrajectories_NilEvent(t *testing.T) {
	w := NewWriter("/tmp/test.json", SessionMeta{SessionID: "test"})
	// Should not panic.
	w.LinkSubagentTrajectories(nil, "/tmp", "/tmp")
}

func TestLinkSubagentTrajectories_RelativePath(t *testing.T) {
	dir := t.TempDir()
	parentDir := filepath.Join(dir, "parent-sess")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	w := NewWriter(filepath.Join(parentDir, "trajectory.atif.json"), SessionMeta{
		SessionID: "parent-sess",
		AgentName: "pi-go",
	})

	if err := w.AppendEvent(makeToolCallEvent("call_rel", "subagent", map[string]any{})); err != nil {
		t.Fatal(err)
	}

	responseEvent := makeToolResponseEvent("call_rel", "subagent", map[string]any{
		"results": []any{
			map[string]any{"session_id": "sub-sess"},
		},
	})
	if err := w.AppendEvent(responseEvent); err != nil {
		t.Fatal(err)
	}

	w.LinkSubagentTrajectories(responseEvent, parentDir, dir)

	data, err := os.ReadFile(filepath.Join(parentDir, "trajectory.atif.json"))
	if err != nil {
		t.Fatal(err)
	}

	var traj Trajectory
	if err := json.Unmarshal(data, &traj); err != nil {
		t.Fatal(err)
	}

	for _, step := range traj.Steps {
		if step.Observation == nil {
			continue
		}
		for _, r := range step.Observation.Results {
			if r.SubagentTrajectoryRef != "" {
				expected := filepath.Join("..", "sub-sess", "trajectory.atif.json")
				if r.SubagentTrajectoryRef != expected {
					t.Errorf("subagent_trajectory_ref = %q, want %q", r.SubagentTrajectoryRef, expected)
				}
				return
			}
		}
	}
	t.Error("subagent_trajectory_ref not found")
}

func TestExtractSubagentSessionIDs(t *testing.T) {
	tests := []struct {
		name     string
		response any
		want     []string
	}{
		{
			name: "single result with session_id",
			response: map[string]any{
				"results": []any{
					map[string]any{"session_id": "sess-001"},
				},
			},
			want: []string{"sess-001"},
		},
		{
			name: "multiple results",
			response: map[string]any{
				"results": []any{
					map[string]any{"session_id": "sess-001"},
					map[string]any{"session_id": "sess-002"},
				},
			},
			want: []string{"sess-001", "sess-002"},
		},
		{
			name: "no session_id",
			response: map[string]any{
				"results": []any{
					map[string]any{"agent": "explore"},
				},
			},
			want: nil,
		},
		{
			name:     "nil response",
			response: nil,
			want:     nil,
		},
		{
			name:     "invalid type",
			response: "not a map",
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSubagentSessionIDs(tt.response)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestLinkSubagentTrajectories_SubagentOwnTrajectory(t *testing.T) {
	// Verify the subagent's own trajectory.atif.json is a valid standalone ATIF document.
	dir := t.TempDir()
	subDir := filepath.Join(dir, "sub-session")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	subWriter := NewWriter(filepath.Join(subDir, "trajectory.atif.json"), SessionMeta{
		SessionID: "sub-session",
		AgentName: "explore",
		Model:     "claude-sonnet",
	})

	// Simulate subagent events.
	userEv := &session.Event{}
	userEv.Author = "user"
	userEv.Timestamp = time.Now()
	userEv.Content = &genai.Content{Parts: []*genai.Part{{Text: "find all go files"}}}
	if err := subWriter.AppendEvent(userEv); err != nil {
		t.Fatal(err)
	}

	agentEv := &session.Event{}
	agentEv.Author = "model"
	agentEv.Timestamp = time.Now()
	agentEv.Content = &genai.Content{Parts: []*genai.Part{{Text: "Found 42 Go files."}}}
	if err := subWriter.AppendEvent(agentEv); err != nil {
		t.Fatal(err)
	}

	// Read and validate the subagent's ATIF file.
	data, err := os.ReadFile(filepath.Join(subDir, "trajectory.atif.json"))
	if err != nil {
		t.Fatal(err)
	}

	var traj Trajectory
	if err := json.Unmarshal(data, &traj); err != nil {
		t.Fatalf("subagent trajectory is not valid JSON: %v", err)
	}
	if traj.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %q, want %q", traj.SchemaVersion, SchemaVersion)
	}
	if traj.SessionID != "sub-session" {
		t.Errorf("session_id = %q, want %q", traj.SessionID, "sub-session")
	}
	if len(traj.Steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(traj.Steps))
	}
}
