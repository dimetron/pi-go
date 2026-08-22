package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/subagent"
)

// fakeRunner is a scriptable compressorRunner for adapter tests.
type fakeRunner struct {
	agent     subagent.AgentConfig
	lookupErr error
	events    []subagent.Event
	spawnErr  error
	spawnIn   subagent.SpawnInput
}

func (f *fakeRunner) LookupAgent(name string) (subagent.AgentConfig, error) {
	return f.agent, f.lookupErr
}

func (f *fakeRunner) Spawn(ctx context.Context, input subagent.SpawnInput) (<-chan subagent.Event, string, error) {
	f.spawnIn = input
	if f.spawnErr != nil {
		return nil, "", f.spawnErr
	}
	ch := make(chan subagent.Event, len(f.events))
	for _, ev := range f.events {
		ch <- ev
	}
	close(ch)
	return ch, "fake-agent", nil
}

// streamEvents builds a fake that emits the given events, then closes.
func newStreamingFake(events ...subagent.Event) *fakeRunner {
	return &fakeRunner{events: events, agent: subagent.AgentConfig{Name: "memory-compressor"}}
}

// newFake is a short alias for newStreamingFake.
func newFake(events ...subagent.Event) *fakeRunner {
	return newStreamingFake(events...)
}

// TestCompressor_CompressObservation_Success: streamed text_delta events are
// accumulated, JSON is parsed, and raw metadata is preserved.
func TestCompressor_CompressObservation_Success(t *testing.T) {
	raw := RawObservation{
		SessionID: "sess-1",
		Project:   "/proj",
		ToolName:  "Read",
		Timestamp: time.Date(2026, 3, 20, 1, 2, 3, 0, time.UTC),
	}
	// Split a fenced JSON response across deltas with an unrelated event between.
	fake := newStreamingFake(
		subagent.Event{Type: "text_delta", Content: "```json\n{\"title\": \"Read main.go\","},
		subagent.Event{Type: "tool_call", Content: "read"},
		subagent.Event{Type: "text_delta", Content: " \"type\": \"discovery\", \"text\": \"explored\"}"},
		subagent.Event{Type: "message_end"},
	)
	c := &SubagentCompressor{runner: fake}

	obs, err := c.CompressObservation(context.Background(), raw)
	if err != nil {
		t.Fatalf("CompressObservation: %v", err)
	}
	if obs.Title != "Read main.go" {
		t.Errorf("title = %q, want %q", obs.Title, "Read main.go")
	}
	if obs.Type != TypeDiscovery {
		t.Errorf("type = %q, want discovery", obs.Type)
	}
	if obs.SessionID != "sess-1" {
		t.Errorf("sessionID = %q, want sess-1", obs.SessionID)
	}
	if obs.Project != "/proj" {
		t.Errorf("project = %q, want /proj", obs.Project)
	}
	if obs.ToolName != "Read" {
		t.Errorf("toolName = %q, want Read", obs.ToolName)
	}
	if !obs.CreatedAt.Equal(raw.Timestamp) {
		t.Errorf("createdAt = %v, want %v", obs.CreatedAt, raw.Timestamp)
	}
	// SpawnInput carries the prompt.
	if fake.spawnIn.Prompt == "" {
		t.Error("spawn prompt is empty")
	}
}

// TestCompressor_CompressObservation_LookupFailure
func TestCompressor_CompressObservation_LookupFailure(t *testing.T) {
	fake := &fakeRunner{lookupErr: errors.New("no agent")}
	c := &SubagentCompressor{runner: fake}
	_, err := c.CompressObservation(context.Background(), RawObservation{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "finding memory-compressor agent") {
		t.Errorf("error should have lookup context: %v", err)
	}
}

// TestCompressor_CompressObservation_SpawnFailure
func TestCompressor_CompressObservation_SpawnFailure(t *testing.T) {
	fake := &fakeRunner{spawnErr: errors.New("spawn boom")}
	c := &SubagentCompressor{runner: fake}
	_, err := c.CompressObservation(context.Background(), RawObservation{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "spawning memory-compressor") {
		t.Errorf("error should have spawn context: %v", err)
	}
}

// TestCompressor_CompressObservation_ErrorEvent
func TestCompressor_CompressObservation_ErrorEvent(t *testing.T) {
	fake := newFake(
		subagent.Event{Type: "text_delta", Content: "partial"},
		subagent.Event{Type: "error", Error: "model refused"},
	)
	c := &SubagentCompressor{runner: fake}
	_, err := c.CompressObservation(context.Background(), RawObservation{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "memory-compressor error") {
		t.Errorf("error should carry event context: %v", err)
	}
}

// TestCompressor_CompressObservation_EmptyStream: a closed stream with no text
// yields the "empty response" parse error.
func TestCompressor_CompressObservation_EmptyStream(t *testing.T) {
	c := &SubagentCompressor{runner: newFake()}
	_, err := c.CompressObservation(context.Background(), RawObservation{})
	if err == nil {
		t.Fatal("expected error for an empty event stream")
	}
	if !contains(err.Error(), "parsing compressor response") {
		t.Errorf("error should wrap parse context: %v", err)
	}
}

// TestCompressor_CompressObservation_MalformedJSON: accumulated text is not JSON.
func TestCompressor_CompressObservation_MalformedJSON(t *testing.T) {
	c := &SubagentCompressor{runner: newFake(
		subagent.Event{Type: "text_delta", Content: "this is not json"},
	)}
	_, err := c.CompressObservation(context.Background(), RawObservation{})
	if err == nil {
		t.Fatal("expected error for malformed accumulated JSON")
	}
	if !contains(err.Error(), "parsing compressor response") {
		t.Errorf("error should wrap parse context: %v", err)
	}
}

// TestCompressor_SummarizeSession_Success
func TestCompressor_SummarizeSession_Success(t *testing.T) {
	observations := []*Observation{
		{Title: "read a.go", Type: TypeDiscovery, Text: "explored", SourceFiles: []string{"/p/a.go"}},
		{Title: "fixed bug", Type: TypeBugfix, Text: "nil guard"},
	}
	// Stream a fenced JSON response across deltas with a tool event between.
	fake := newFake(
		subagent.Event{Type: "text_delta", Content: "```json\n{\"request\":\"fix x\",\"investigated\":\"read\""},
		subagent.Event{Type: "tool_call", Content: "edit"},
		subagent.Event{Type: "text_delta", Content: `,"learned":"nil check","completed":"guarded","next_steps":"test"}`},
		subagent.Event{Type: "text_delta", Content: "\n```"},
	)
	c := &SubagentCompressor{runner: fake}

	sum, err := c.SummarizeSession(context.Background(), "sess-1", "/proj", observations)
	if err != nil {
		t.Fatalf("SummarizeSession: %v", err)
	}
	if sum.Request != "fix x" {
		t.Errorf("request = %q, want %q", sum.Request, "fix x")
	}
	if sum.SessionID != "sess-1" || sum.Project != "/proj" {
		t.Errorf("metadata = %q/%q, want sess-1//proj", sum.SessionID, sum.Project)
	}
	// Prompt includes every observation's title and source files.
	p := fake.spawnIn.Prompt
	if !contains(p, "read a.go") {
		t.Errorf("prompt should include observation titles: %q", p)
	}
	if !contains(p, "a.go") {
		t.Errorf("prompt should include source files: %q", p)
	}
	// CreatedAt is near now.
	if age := time.Since(sum.CreatedAt); age < 0 || age > time.Second {
		t.Errorf("createdAt = %v, expected within the last second", sum.CreatedAt)
	}
}

// TestCompressor_SummarizeSession_LookupFailure
func TestCompressor_SummarizeSession_LookupFailure(t *testing.T) {
	fake := &fakeRunner{lookupErr: errors.New("no agent")}
	c := &SubagentCompressor{runner: fake}
	_, err := c.SummarizeSession(context.Background(), "s", "/p", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "finding memory-compressor agent") {
		t.Errorf("error should have lookup context: %v", err)
	}
}

// TestCompressor_SummarizeSession_SpawnFailure
func TestCompressor_SummarizeSession_SpawnFailure(t *testing.T) {
	fake := &fakeRunner{spawnErr: errors.New("boom")}
	c := &SubagentCompressor{runner: fake}
	_, err := c.SummarizeSession(context.Background(), "s", "/p", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "spawning memory-compressor for summary") {
		t.Errorf("error should have summary spawn context: %v", err)
	}
}

// TestCompressor_SummarizeSession_ErrorEvent
func TestCompressor_SummarizeSession_ErrorEvent(t *testing.T) {
	fake := newFake(subagent.Event{Type: "error", Error: "timeout"})
	c := &SubagentCompressor{runner: fake}
	_, err := c.SummarizeSession(context.Background(), "s", "/p", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "memory-compressor summary error") {
		t.Errorf("error should carry summary error context: %v", err)
	}
}

// TestCompressor_SummarizeSession_EmptyStream
func TestCompressor_SummarizeSession_EmptyStream(t *testing.T) {
	c := &SubagentCompressor{runner: newFake()}
	_, err := c.SummarizeSession(context.Background(), "s", "/p", nil)
	if err == nil {
		t.Fatal("expected error for empty stream")
	}
	if !contains(err.Error(), "empty summary response") {
		t.Errorf("error should mention empty summary: %v", err)
	}
}
