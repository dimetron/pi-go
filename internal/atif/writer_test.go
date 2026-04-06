package atif

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func newTestEvent(author string, text string) *session.Event {
	ev := &session.Event{}
	ev.Author = author
	ev.Timestamp = time.Date(2026, 4, 5, 10, 30, 0, 0, time.UTC)
	ev.Content = &genai.Content{
		Parts: []*genai.Part{{Text: text}},
	}
	return ev
}

func TestNewWriter(t *testing.T) {
	w := NewWriter("/tmp/test.json", SessionMeta{
		SessionID: "sess-1",
		AgentName: "pi-go",
		Model:     "claude-sonnet",
		WorkDir:   "/home/user/project",
	})

	if w.trajectory.SchemaVersion != SchemaVersion {
		t.Errorf("schema version = %q, want %q", w.trajectory.SchemaVersion, SchemaVersion)
	}
	if w.trajectory.SessionID != "sess-1" {
		t.Errorf("session id = %q, want %q", w.trajectory.SessionID, "sess-1")
	}
	if w.trajectory.Agent.Name != "pi-go" {
		t.Errorf("agent name = %q, want %q", w.trajectory.Agent.Name, "pi-go")
	}
	if w.trajectory.Agent.ModelName != "claude-sonnet" {
		t.Errorf("model = %q, want %q", w.trajectory.Agent.ModelName, "claude-sonnet")
	}
	if w.trajectory.Agent.Extra["work_dir"] != "/home/user/project" {
		t.Errorf("work_dir = %v, want %q", w.trajectory.Agent.Extra["work_dir"], "/home/user/project")
	}
	if w.stepCounter != 1 {
		t.Errorf("initial step counter = %d, want 1", w.stepCounter)
	}
}

func TestNewWriterNoWorkDir(t *testing.T) {
	w := NewWriter("/tmp/test.json", SessionMeta{
		SessionID: "sess-1",
		AgentName: "pi-go",
	})
	if w.trajectory.Agent.Extra != nil {
		t.Errorf("extra should be nil when no work_dir, got %v", w.trajectory.Agent.Extra)
	}
}

func TestWriterAppendEvent(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	w := NewWriter(fp, SessionMeta{SessionID: "sess-1", AgentName: "pi-go"})

	err := w.AppendEvent(newTestEvent("user", "hello"))
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	// File should exist and be valid JSON.
	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	var traj Trajectory
	if err := json.Unmarshal(data, &traj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(traj.Steps) != 1 {
		t.Fatalf("steps count = %d, want 1", len(traj.Steps))
	}
	if traj.Steps[0].StepID != 1 {
		t.Errorf("step_id = %d, want 1", traj.Steps[0].StepID)
	}
	if traj.Steps[0].Source != "user" {
		t.Errorf("source = %q, want %q", traj.Steps[0].Source, "user")
	}
	if traj.Steps[0].Message != "hello" {
		t.Errorf("message = %v, want %q", traj.Steps[0].Message, "hello")
	}
}

func TestWriterIncrementalStepIDs(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	w := NewWriter(fp, SessionMeta{SessionID: "sess-1", AgentName: "pi-go"})

	events := []*session.Event{
		newTestEvent("user", "first"),
		newTestEvent("model", "second"),
		newTestEvent("user", "third"),
	}

	for _, ev := range events {
		if err := w.AppendEvent(ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	var traj Trajectory
	if err := json.Unmarshal(data, &traj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(traj.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(traj.Steps))
	}
	for i, step := range traj.Steps {
		want := i + 1
		if step.StepID != want {
			t.Errorf("step[%d].step_id = %d, want %d", i, step.StepID, want)
		}
	}
}

func TestWriterFileIsValidJSONAfterEachAppend(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	w := NewWriter(fp, SessionMeta{SessionID: "sess-1", AgentName: "pi-go"})

	for i := 0; i < 5; i++ {
		if err := w.AppendEvent(newTestEvent("user", "msg")); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		data, err := os.ReadFile(fp)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if !json.Valid(data) {
			t.Fatalf("invalid JSON after append %d", i)
		}
	}
}

func TestWriterSkipsEmptyEvent(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	w := NewWriter(fp, SessionMeta{SessionID: "sess-1", AgentName: "pi-go"})

	// nil event
	if err := w.AppendEvent(nil); err != nil {
		t.Fatalf("nil event: %v", err)
	}

	// event with no content
	if err := w.AppendEvent(&session.Event{Author: "user"}); err != nil {
		t.Fatalf("no content: %v", err)
	}

	// File should not exist since no steps were written.
	if _, err := os.Stat(fp); !os.IsNotExist(err) {
		t.Error("file should not exist for empty events")
	}

	if w.StepCount() != 0 {
		t.Errorf("step count = %d, want 0", w.StepCount())
	}
}

func TestWriterStepCount(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	w := NewWriter(fp, SessionMeta{SessionID: "sess-1", AgentName: "pi-go"})

	if w.StepCount() != 0 {
		t.Errorf("initial step count = %d, want 0", w.StepCount())
	}

	w.AppendEvent(newTestEvent("user", "hello"))
	if w.StepCount() != 1 {
		t.Errorf("step count = %d, want 1", w.StepCount())
	}

	w.AppendEvent(newTestEvent("model", "world"))
	if w.StepCount() != 2 {
		t.Errorf("step count = %d, want 2", w.StepCount())
	}
}

func TestWriterSetSubagentRef(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	w := NewWriter(fp, SessionMeta{SessionID: "sess-1", AgentName: "pi-go"})

	// Add an event with a function response (observation).
	ev := &session.Event{}
	ev.Author = "model"
	ev.Timestamp = time.Date(2026, 4, 5, 10, 30, 0, 0, time.UTC)
	ev.Content = &genai.Content{
		Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{
				ID:       "call-42",
				Name:     "spawn",
				Response: map[string]any{"result": "ok"},
			}},
		},
	}
	if err := w.AppendEvent(ev); err != nil {
		t.Fatalf("append: %v", err)
	}

	w.SetSubagentRef("call-42", "../sub-sess/trajectory.atif.json")

	// Flush and verify.
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	data, _ := os.ReadFile(fp)
	var traj Trajectory
	json.Unmarshal(data, &traj)

	if len(traj.Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(traj.Steps))
	}
	obs := traj.Steps[0].Observation
	if obs == nil || len(obs.Results) != 1 {
		t.Fatal("expected observation with 1 result")
	}
	if obs.Results[0].SubagentTrajectoryRef != "../sub-sess/trajectory.atif.json" {
		t.Errorf("ref = %q, want %q", obs.Results[0].SubagentTrajectoryRef, "../sub-sess/trajectory.atif.json")
	}
}

func TestWriterSetSubagentRefNotFound(t *testing.T) {
	w := NewWriter("/tmp/test.json", SessionMeta{SessionID: "sess-1", AgentName: "pi-go"})
	// Should not panic on missing call ID.
	w.SetSubagentRef("nonexistent", "path")
}

func TestWriterCloseEmpty(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	w := NewWriter(fp, SessionMeta{SessionID: "sess-1", AgentName: "pi-go"})

	// Close with no events should not create a file.
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(fp); !os.IsNotExist(err) {
		t.Error("file should not exist for empty trajectory")
	}
}

func TestWriterCloseFlushes(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	w := NewWriter(fp, SessionMeta{SessionID: "sess-1", AgentName: "pi-go"})

	w.AppendEvent(newTestEvent("user", "hello"))
	os.Remove(fp) // remove the file written by AppendEvent

	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// File should be recreated by Close.
	if _, err := os.Stat(fp); err != nil {
		t.Errorf("file should exist after Close: %v", err)
	}
}

func TestWriterConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	w := NewWriter(fp, SessionMeta{SessionID: "sess-1", AgentName: "pi-go"})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.AppendEvent(newTestEvent("user", "concurrent"))
		}()
	}
	wg.Wait()

	if w.StepCount() != 10 {
		t.Errorf("step count = %d, want 10", w.StepCount())
	}

	// File should be valid JSON.
	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var traj Trajectory
	if err := json.Unmarshal(data, &traj); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(traj.Steps) != 10 {
		t.Errorf("steps in file = %d, want 10", len(traj.Steps))
	}
}

func TestWriterAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	w := NewWriter(fp, SessionMeta{SessionID: "sess-1", AgentName: "pi-go"})

	w.AppendEvent(newTestEvent("user", "first"))

	// Verify no temp files are left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "trajectory.atif.json" {
			t.Errorf("unexpected file: %s", e.Name())
		}
	}
}

func TestWriterSchemaCompliance(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	w := NewWriter(fp, SessionMeta{
		SessionID: "sess-1",
		AgentName: "pi-go",
		Model:     "claude-sonnet",
		WorkDir:   "/project",
	})

	w.AppendEvent(newTestEvent("user", "hello"))
	w.AppendEvent(newTestEvent("model", "hi there"))

	data, _ := os.ReadFile(fp)
	var raw map[string]any
	json.Unmarshal(data, &raw)

	// Check required top-level fields.
	required := []string{"schema_version", "session_id", "agent", "steps"}
	for _, key := range required {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing required field %q", key)
		}
	}

	if raw["schema_version"] != SchemaVersion {
		t.Errorf("schema_version = %v, want %q", raw["schema_version"], SchemaVersion)
	}
}

func TestWriterToolCallEvent(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "trajectory.atif.json")
	w := NewWriter(fp, SessionMeta{SessionID: "sess-1", AgentName: "pi-go"})

	ev := &session.Event{}
	ev.Author = "model"
	ev.Timestamp = time.Date(2026, 4, 5, 10, 30, 0, 0, time.UTC)
	ev.Content = &genai.Content{
		Parts: []*genai.Part{
			{Text: "Let me read that file."},
			{FunctionCall: &genai.FunctionCall{
				ID:   "call-1",
				Name: "read",
				Args: map[string]any{"path": "main.go"},
			}},
		},
	}

	w.AppendEvent(ev)

	data, _ := os.ReadFile(fp)
	var traj Trajectory
	json.Unmarshal(data, &traj)

	if len(traj.Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(traj.Steps))
	}
	step := traj.Steps[0]
	if step.Source != "agent" {
		t.Errorf("source = %q, want %q", step.Source, "agent")
	}
	if len(step.ToolCalls) != 1 {
		t.Fatalf("tool_calls = %d, want 1", len(step.ToolCalls))
	}
	if step.ToolCalls[0].FunctionName != "read" {
		t.Errorf("function_name = %q, want %q", step.ToolCalls[0].FunctionName, "read")
	}
}
