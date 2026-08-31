package server

import (
	"context"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aevent"
	acp "github.com/coder/acp-go-sdk"
)

// applyToTask folds the updater's events into a task the way a2a-go's task
// manager does, so assertions run against what a client actually stores.
func applyToTask(t *testing.T, execCtxTaskID a2a.TaskID, events []a2a.Event) *a2a.Task {
	t.Helper()
	task := &a2a.Task{ID: execCtxTaskID, ContextID: "ctx-1"}
	for _, ev := range events {
		au, ok := ev.(*a2a.TaskArtifactUpdateEvent)
		if !ok {
			continue
		}
		updated, err := a2aevent.ApplyArtifactUpdate(task, au)
		if err != nil {
			t.Fatalf("ApplyArtifactUpdate: %v", err)
		}
		task = updated
	}
	return task
}

// collect drains the updater's channel after fn has run.
func collect(t *testing.T, fn func(u *a2aUpdater)) ([]a2a.Event, *a2asrvExecCtx) {
	t.Helper()
	execCtx := newExecCtx("hello")
	u := newA2AUpdater(context.Background(), execCtx)
	go func() {
		fn(u)
		close(u.events)
	}()
	var events []a2a.Event
	for ev := range u.events {
		events = append(events, ev)
	}
	return events, &a2asrvExecCtx{TaskID: execCtx.TaskID}
}

type a2asrvExecCtx struct{ TaskID a2a.TaskID }

func textChunk(s string) acp.SessionUpdate {
	return acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
		Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: s}},
	}}
}

func thoughtChunk(s string) acp.SessionUpdate {
	return acp.SessionUpdate{AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
		Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: s}},
	}}
}

// TestStreamedTextCoalescesIntoOnePart is the regression test for the kagent
// UI rendering one reply as a column of fragments. A2A append semantics
// concatenate parts, not text, so streaming each chunk as its own part left
// the stored task holding one part per token.
func TestStreamedTextCoalescesIntoOnePart(t *testing.T) {
	ctx := context.Background()
	events, ec := collect(t, func(u *a2aUpdater) {
		for _, chunk := range []string{"Here ", "are ", "the ", "tools."} {
			if err := u.Update(ctx, textChunk(chunk)); err != nil {
				t.Errorf("Update: %v", err)
			}
		}
	})

	task := applyToTask(t, ec.TaskID, events)
	if len(task.Artifacts) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(task.Artifacts))
	}
	if got := len(task.Artifacts[0].Parts); got != 1 {
		t.Fatalf("parts in the text artifact = %d, want 1 (one bubble, not one per chunk)", got)
	}
	if got := task.Artifacts[0].Parts[0].Text(); got != "Here are the tools." {
		t.Errorf("text = %q, want the chunks joined", got)
	}
}

// TestThinkingCoalescesSeparately keeps thinking in its own artifact so the UI
// can render it apart from the reply.
func TestThinkingCoalescesSeparately(t *testing.T) {
	ctx := context.Background()
	events, ec := collect(t, func(u *a2aUpdater) {
		_ = u.Update(ctx, thoughtChunk("let me "))
		_ = u.Update(ctx, thoughtChunk("check"))
		_ = u.Update(ctx, textChunk("done"))
	})

	task := applyToTask(t, ec.TaskID, events)
	if len(task.Artifacts) != 2 {
		t.Fatalf("artifacts = %d, want 2 (thinking + reply)", len(task.Artifacts))
	}
	for _, art := range task.Artifacts {
		if len(art.Parts) != 1 {
			t.Errorf("artifact %s has %d parts, want 1", art.ID, len(art.Parts))
		}
	}
	if got := task.Artifacts[0].Parts[0].Text(); got != "let me check" {
		t.Errorf("thinking = %q, want %q", got, "let me check")
	}
	if got := task.Artifacts[1].Parts[0].Text(); got != "done" {
		t.Errorf("reply = %q, want %q", got, "done")
	}
}

// TestTextAfterToolCallStartsNewArtifact pins the ordering: text produced
// after a tool call must not grow the artifact opened before it, or the reply
// renders above the tool card instead of below it.
func TestTextAfterToolCallStartsNewArtifact(t *testing.T) {
	ctx := context.Background()
	events, ec := collect(t, func(u *a2aUpdater) {
		_ = u.Update(ctx, textChunk("checking now"))
		_ = u.Update(ctx, acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
			ToolCallId: "call-1", Title: "bash git status -s",
		}})
		_ = u.Update(ctx, acp.SessionUpdate{ToolCallUpdate: &acp.SessionToolCallUpdate{
			ToolCallId: "call-1", RawOutput: map[string]any{"exit_code": 0},
		}})
		_ = u.Update(ctx, textChunk("all clean"))
	})

	task := applyToTask(t, ec.TaskID, events)
	if len(task.Artifacts) != 4 {
		t.Fatalf("artifacts = %d, want 4 (text, call, response, text)", len(task.Artifacts))
	}
	if got := task.Artifacts[0].Parts[0].Text(); got != "checking now" {
		t.Errorf("first artifact = %q, want the pre-tool text", got)
	}
	if got := task.Artifacts[3].Parts[0].Text(); got != "all clean" {
		t.Errorf("last artifact = %q, want the post-tool text in its own artifact", got)
	}
	for i, art := range task.Artifacts {
		if len(art.Parts) != 1 {
			t.Errorf("artifact %d has %d parts, want 1", i, len(art.Parts))
		}
	}
}

// TestStreamedTextSuppressesFinalArtifact keeps the executor from appending a
// duplicate final artifact once text has already been streamed, even though
// tool boundaries clear the open artifact ID.
func TestStreamedTextSuppressesFinalArtifact(t *testing.T) {
	ctx := context.Background()
	u := newA2AUpdater(ctx, newExecCtx("hello"))
	go func() {
		_ = u.Update(ctx, textChunk("partial"))
		_ = u.Update(ctx, acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
			ToolCallId: "call-1", Title: "bash ls",
		}})
		close(u.events)
	}()
	for range u.events { //nolint:revive // draining
	}

	if !u.streamedText() {
		t.Error("streamedText() = false after a tool call cleared the artifact ID; " +
			"the executor would emit a duplicate final artifact")
	}
}

// TestBashOutputLinesCoalesce reproduces the reported symptom directly: the
// bash tool streams stdout a line at a time, which rendered as one bubble per
// line in the kagent UI.
func TestBashOutputLinesCoalesce(t *testing.T) {
	ctx := context.Background()
	lines := []string{
		"go -> /usr/local/go/bin/go\n",
		"pip -> /usr/bin/pip\n",
		"git -> /usr/bin/git\n",
	}
	events, ec := collect(t, func(u *a2aUpdater) {
		for _, line := range lines {
			_ = u.Update(ctx, textChunk(line))
		}
	})

	task := applyToTask(t, ec.TaskID, events)
	if len(task.Artifacts) != 1 || len(task.Artifacts[0].Parts) != 1 {
		t.Fatalf("got %d artifacts with %d parts, want 1 artifact with 1 part",
			len(task.Artifacts), len(task.Artifacts[0].Parts))
	}
	if got := task.Artifacts[0].Parts[0].Text(); got != strings.Join(lines, "") {
		t.Errorf("text = %q, want the lines joined into one part", got)
	}
}
