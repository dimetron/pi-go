package adapter

import (
	"context"
	"errors"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func TestToolKindMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want acp.ToolKind
	}{
		{"read", acp.ToolKindRead},
		{"ls", acp.ToolKindRead},
		{"grep", acp.ToolKindSearch},
		{"find", acp.ToolKindSearch},
		{"glob", acp.ToolKindSearch},
		{"edit", acp.ToolKindEdit},
		{"write", acp.ToolKindEdit},
		{"bash", acp.ToolKindExecute},
		{"shell", acp.ToolKindExecute},
		{"subagent", acp.ToolKindThink},
		{"agent", acp.ToolKindThink},
		{"unheard-of", acp.ToolKindOther},
		{"", acp.ToolKindOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := toolKind(tc.name); got != tc.want {
				t.Fatalf("toolKind(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestLocationFromArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"path key", map[string]any{"path": "/a/b.go"}, "/a/b.go"},
		{"file_path key", map[string]any{"file_path": "/c/d.go"}, "/c/d.go"},
		{"path wins over file_path", map[string]any{"path": "/p", "file_path": "/f"}, "/p"},
		{"missing", map[string]any{"other": 1}, ""},
		{"non-string", map[string]any{"path": 7}, ""},
		{"empty string", map[string]any{"path": ""}, ""},
		{"nil map", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := locationFromArgs(tc.args); got != tc.want {
				t.Fatalf("locationFromArgs = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOnToolStartAndEndSuccess(t *testing.T) {
	up := &fakeUpdater{}
	s := New(up)

	id, err := s.OnToolStart(context.Background(), "read", map[string]any{"path": "/tmp/x.txt"})
	if err != nil {
		t.Fatalf("OnToolStart: %v", err)
	}
	if id == "" {
		t.Fatal("OnToolStart returned empty id")
	}
	if err := s.OnToolEnd(context.Background(), id, map[string]any{"bytes": 42}, nil); err != nil {
		t.Fatalf("OnToolEnd: %v", err)
	}

	if got, want := len(up.updates), 2; got != want {
		t.Fatalf("updates = %d, want %d", got, want)
	}
	start := up.updates[0].ToolCall
	if start == nil {
		t.Fatalf("updates[0] is not a ToolCall: %+v", up.updates[0])
	}
	if start.Kind != acp.ToolKindRead {
		t.Fatalf("start kind = %q, want %q", start.Kind, acp.ToolKindRead)
	}
	if start.Status != acp.ToolCallStatusInProgress {
		t.Fatalf("start status = %q, want %q", start.Status, acp.ToolCallStatusInProgress)
	}
	if len(start.Locations) != 1 || start.Locations[0].Path != "/tmp/x.txt" {
		t.Fatalf("start locations = %+v, want single path /tmp/x.txt", start.Locations)
	}
	if string(start.ToolCallId) != id {
		t.Fatalf("start id = %q, want %q", start.ToolCallId, id)
	}

	end := up.updates[1].ToolCallUpdate
	if end == nil {
		t.Fatalf("updates[1] is not a ToolCallUpdate: %+v", up.updates[1])
	}
	if end.Status == nil || *end.Status != acp.ToolCallStatusCompleted {
		t.Fatalf("end status = %+v, want completed", end.Status)
	}
	if end.RawOutput == nil {
		t.Fatal("end rawOutput is nil, want non-nil result")
	}
}

func TestOnToolEndFailureSetsFailedStatusAndErrorOutput(t *testing.T) {
	up := &fakeUpdater{}
	s := New(up)

	id, err := s.OnToolStart(context.Background(), "bash", map[string]any{"cmd": "exit 1"})
	if err != nil {
		t.Fatalf("OnToolStart: %v", err)
	}
	if err := s.OnToolEnd(context.Background(), id, nil, errors.New("boom")); err != nil {
		t.Fatalf("OnToolEnd: %v", err)
	}

	end := up.updates[1].ToolCallUpdate
	if end == nil {
		t.Fatalf("updates[1] is not a ToolCallUpdate: %+v", up.updates[1])
	}
	if end.Status == nil || *end.Status != acp.ToolCallStatusFailed {
		t.Fatalf("end status = %+v, want failed", end.Status)
	}
	m, ok := end.RawOutput.(map[string]any)
	if !ok {
		t.Fatalf("end rawOutput = %T, want map[string]any", end.RawOutput)
	}
	if m["error"] != "boom" {
		t.Fatalf("end rawOutput[error] = %v, want %q", m["error"], "boom")
	}
}

func TestOnToolEndUnknownIDIsNoOp(t *testing.T) {
	up := &fakeUpdater{}
	s := New(up)
	if err := s.OnToolEnd(context.Background(), "nope", nil, nil); err != nil {
		t.Fatalf("OnToolEnd: %v", err)
	}
	if len(up.updates) != 0 {
		t.Fatalf("unexpected updates: %+v", up.updates)
	}
}

func TestSubagentWrapsInnerToolCalls(t *testing.T) {
	up := &fakeUpdater{}
	s := New(up)
	ctx := context.Background()

	// Parent sub-agent dispatch — becomes the active think card.
	parentID, err := s.OnToolStart(ctx, "subagent", map[string]any{"agent": "explore"})
	if err != nil {
		t.Fatalf("OnToolStart parent: %v", err)
	}

	// Two inner tool calls while the sub-agent is active.
	childA, err := s.OnToolStart(ctx, "read", map[string]any{"path": "a.go"})
	if err != nil {
		t.Fatalf("OnToolStart child a: %v", err)
	}
	if err := s.OnToolEnd(ctx, childA, "ok", nil); err != nil {
		t.Fatalf("OnToolEnd child a: %v", err)
	}
	childB, err := s.OnToolStart(ctx, "grep", map[string]any{"pattern": "foo"})
	if err != nil {
		t.Fatalf("OnToolStart child b: %v", err)
	}
	if err := s.OnToolEnd(ctx, childB, nil, errors.New("not found")); err != nil {
		t.Fatalf("OnToolEnd child b: %v", err)
	}

	// Parent closes, which clears the subagentID and emits the final update.
	if err := s.OnToolEnd(ctx, parentID, "done", nil); err != nil {
		t.Fatalf("OnToolEnd parent: %v", err)
	}

	// Count top-level StartToolCall updates: exactly one (the parent).
	var topStarts int
	for _, u := range up.updates {
		if u.ToolCall != nil {
			topStarts++
		}
	}
	if topStarts != 1 {
		t.Fatalf("top-level StartToolCall count = %d, want 1", topStarts)
	}

	// The parent's kind is Think.
	if up.updates[0].ToolCall.Kind != acp.ToolKindThink {
		t.Fatalf("parent kind = %q, want %q", up.updates[0].ToolCall.Kind, acp.ToolKindThink)
	}

	// The last update targets the parent, with status completed.
	last := up.updates[len(up.updates)-1].ToolCallUpdate
	if last == nil || string(last.ToolCallId) != parentID {
		t.Fatalf("last update = %+v, want ToolCallUpdate for parent %q", last, parentID)
	}
	if last.Status == nil || *last.Status != acp.ToolCallStatusCompleted {
		t.Fatalf("parent final status = %+v, want completed", last.Status)
	}

	// sub-agent id cleared so later tools are top-level again.
	if s.subagentID != "" {
		t.Fatalf("subagentID = %q, want empty after parent end", s.subagentID)
	}

	// After the parent closes, a new tool start is top-level, not nested.
	if _, err := s.OnToolStart(ctx, "edit", map[string]any{"file_path": "x.go"}); err != nil {
		t.Fatalf("OnToolStart after parent: %v", err)
	}
	newStart := up.updates[len(up.updates)-1].ToolCall
	if newStart == nil {
		t.Fatalf("expected top-level ToolCall after parent, got %+v", up.updates[len(up.updates)-1])
	}
	if newStart.Kind != acp.ToolKindEdit {
		t.Fatalf("new start kind = %q, want %q", newStart.Kind, acp.ToolKindEdit)
	}
}

func TestSubagentInnerUpdatesAppendToParentContent(t *testing.T) {
	up := &fakeUpdater{}
	s := New(up)
	ctx := context.Background()

	parentID, err := s.OnToolStart(ctx, "subagent", nil)
	if err != nil {
		t.Fatalf("parent start: %v", err)
	}
	childA, _ := s.OnToolStart(ctx, "read", map[string]any{"path": "a"})
	_ = s.OnToolEnd(ctx, childA, "x", nil)
	childB, _ := s.OnToolStart(ctx, "read", map[string]any{"path": "b"})
	_ = s.OnToolEnd(ctx, childB, "y", errors.New("fail"))

	// Each nested start and nested end appends exactly one line to the
	// parent's content and emits an UpdateToolCall(parent, WithUpdateContent).
	// After two starts + two ends, the latest parent update's content has
	// four entries in insertion order.
	var lastContent []acp.ToolCallContent
	for _, u := range up.updates {
		if u.ToolCallUpdate != nil && string(u.ToolCallUpdate.ToolCallId) == parentID && len(u.ToolCallUpdate.Content) > 0 {
			lastContent = u.ToolCallUpdate.Content
		}
	}
	if got, want := len(lastContent), 4; got != want {
		t.Fatalf("parent content entries = %d, want %d", got, want)
	}
	want := []string{"▶ read", "✓ read", "▶ read", "✗ read"}
	for i, w := range want {
		cb := lastContent[i].Content
		if cb == nil || cb.Content.Text == nil || cb.Content.Text.Text != w {
			t.Fatalf("content[%d] = %+v, want text %q", i, lastContent[i], w)
		}
	}
}

func TestToolCallNilUpdaterStillTracksState(t *testing.T) {
	s := New(nil)
	id, err := s.OnToolStart(context.Background(), "read", map[string]any{"path": "/p"})
	if err != nil {
		t.Fatalf("OnToolStart: %v", err)
	}
	if err := s.OnToolEnd(context.Background(), id, "ok", nil); err != nil {
		t.Fatalf("OnToolEnd: %v", err)
	}
	if _, still := s.toolCalls[id]; still {
		t.Fatalf("toolCalls still has %q after end", id)
	}
}

func TestOnToolStartPropagatesUpdaterError(t *testing.T) {
	sentinel := errors.New("send failed")
	up := &fakeUpdater{err: sentinel}
	s := New(up)

	_, err := s.OnToolStart(context.Background(), "read", map[string]any{"path": "/p"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapping %v", err, sentinel)
	}
}
