package server

import (
	"context"
	"errors"
	"iter"
	"slices"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	adksession "google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// recordingUpdater captures every session update in order.
type recordingUpdater struct {
	updates []acp.SessionUpdate
	failAt  int // 1-based index of the update to fail on; 0 never fails
}

func (u *recordingUpdater) Update(_ context.Context, update acp.SessionUpdate) error {
	u.updates = append(u.updates, update)
	if u.failAt > 0 && len(u.updates) == u.failAt {
		return errors.New("peer gone")
	}
	return nil
}

func eventsOf(evs ...*adksession.Event) iter.Seq[*adksession.Event] {
	return slices.Values(evs)
}

func userTextEvent(text string) *adksession.Event {
	ev := &adksession.Event{Author: "user", Timestamp: time.Now()}
	ev.Content = genai.NewContentFromText(text, genai.RoleUser)
	return ev
}

func modelEvent(parts ...*genai.Part) *adksession.Event {
	ev := &adksession.Event{Author: "pi", Timestamp: time.Now()}
	ev.Content = &genai.Content{Role: genai.RoleModel, Parts: parts}
	return ev
}

func TestReplayEventsMapsTranscriptToUpdates(t *testing.T) {
	t.Parallel()

	call := &genai.FunctionCall{ID: "fc_1", Name: "read", Args: map[string]any{"path": "main.go"}}
	result := &genai.FunctionResponse{ID: "fc_1", Name: "read", Response: map[string]any{"content": "package main"}}
	partial := modelEvent(genai.NewPartFromText("delta"))
	partial.Partial = true

	u := &recordingUpdater{}
	err := replayEvents(context.Background(), eventsOf(
		userTextEvent("open main.go"),
		partial,
		modelEvent(&genai.Part{Text: "let me look", Thought: true}),
		modelEvent(&genai.Part{FunctionCall: call}),
		&adksession.Event{Author: "user", Content: &genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{{FunctionResponse: result}}}},
		modelEvent(genai.NewPartFromText("it is a main package")),
		nil,
		&adksession.Event{Author: "pi"},
	), u)
	if err != nil {
		t.Fatalf("replayEvents() error = %v", err)
	}

	if len(u.updates) != 5 {
		t.Fatalf("got %d updates, want 5: %+v", len(u.updates), u.updates)
	}
	if got := u.updates[0].UserMessageChunk; got == nil || got.Content.Text == nil || got.Content.Text.Text != "open main.go" {
		t.Errorf("update[0] = %+v, want user message chunk", u.updates[0])
	}
	if got := u.updates[1].AgentThoughtChunk; got == nil || got.Content.Text.Text != "let me look" {
		t.Errorf("update[1] = %+v, want thought chunk", u.updates[1])
	}
	tc := u.updates[2].ToolCall
	if tc == nil {
		t.Fatalf("update[2] = %+v, want tool_call", u.updates[2])
	}
	if tc.ToolCallId != "fc_1" || tc.Kind != acp.ToolKindRead || tc.Status != acp.ToolCallStatusCompleted {
		t.Errorf("tool_call = id %q kind %q status %q", tc.ToolCallId, tc.Kind, tc.Status)
	}
	if tc.Title != "read: main.go" {
		t.Errorf("tool_call title = %q, want enriched with the path", tc.Title)
	}
	tu := u.updates[3].ToolCallUpdate
	if tu == nil || tu.ToolCallId != "fc_1" || tu.Status == nil || *tu.Status != acp.ToolCallStatusCompleted {
		t.Fatalf("update[3] = %+v, want completed tool_call_update for fc_1", u.updates[3])
	}
	if out, ok := tu.RawOutput.(map[string]any); !ok || out["content"] != "package main" {
		t.Errorf("tool_call_update rawOutput = %#v, want the response map", tu.RawOutput)
	}
	if got := u.updates[4].AgentMessageChunk; got == nil || got.Content.Text.Text != "it is a main package" {
		t.Errorf("update[4] = %+v, want agent message chunk", u.updates[4])
	}
}

func TestReplayEventsPairsResponsesWithoutIDsByName(t *testing.T) {
	t.Parallel()

	u := &recordingUpdater{}
	err := replayEvents(context.Background(), eventsOf(
		modelEvent(&genai.Part{FunctionCall: &genai.FunctionCall{Name: "bash", Args: map[string]any{"command": "ls"}}}),
		modelEvent(&genai.Part{FunctionCall: &genai.FunctionCall{Name: "bash", Args: map[string]any{"command": "pwd"}}}),
		&adksession.Event{Content: &genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{Name: "bash", Response: map[string]any{"stdout": "/"}}},
			{FunctionResponse: &genai.FunctionResponse{Name: "never-called"}},
		}}},
	), u)
	if err != nil {
		t.Fatalf("replayEvents() error = %v", err)
	}
	if len(u.updates) != 3 {
		t.Fatalf("got %d updates, want 2 calls + 1 paired result: %+v", len(u.updates), u.updates)
	}
	first, second := u.updates[0].ToolCall, u.updates[1].ToolCall
	if first.ToolCallId == second.ToolCallId {
		t.Fatalf("generated ids collide: %q", first.ToolCallId)
	}
	if first.Kind != acp.ToolKindExecute {
		t.Errorf("bash kind = %q, want execute", first.Kind)
	}
	if got := u.updates[2].ToolCallUpdate; got == nil || got.ToolCallId != second.ToolCallId {
		t.Errorf("result paired with %v, want the most recent bash call %q", u.updates[2], second.ToolCallId)
	}
}

func TestReplayEventsStopsAtFirstUpdateError(t *testing.T) {
	t.Parallel()

	u := &recordingUpdater{failAt: 2}
	err := replayEvents(context.Background(), eventsOf(
		userTextEvent("one"),
		modelEvent(genai.NewPartFromText("two")),
		modelEvent(genai.NewPartFromText("three")),
	), u)
	if err == nil {
		t.Fatal("replayEvents() error = nil, want the updater's failure")
	}
	if len(u.updates) != 2 {
		t.Errorf("replay continued past the failure: %d updates", len(u.updates))
	}
}

func TestReplayEventsSkipsEmptyParts(t *testing.T) {
	t.Parallel()

	u := &recordingUpdater{}
	err := replayEvents(context.Background(), eventsOf(
		modelEvent(nil, &genai.Part{Text: ""}, &genai.Part{Text: "", Thought: true}, genai.NewPartFromText("kept")),
	), u)
	if err != nil {
		t.Fatalf("replayEvents() error = %v", err)
	}
	if len(u.updates) != 1 {
		t.Fatalf("got %d updates, want only the non-empty part: %+v", len(u.updates), u.updates)
	}
	if got := u.updates[0].AgentMessageChunk; got == nil || got.Content.Text.Text != "kept" {
		t.Errorf("update = %+v, want the \"kept\" chunk", u.updates[0])
	}
}
