package server

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aevent"
	acp "github.com/coder/acp-go-sdk"

	"github.com/dimetron/pi-go/internal/acp/server/adapter"
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
		// The executor settles the open artifact when the turn ends; the
		// closing frame is part of the protocol, not an afterthought.
		if err := u.closeTextArtifacts(context.Background()); err != nil {
			t.Errorf("closeTextArtifacts: %v", err)
		}
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
			ToolCallId: "call-1", Status: statusPtr(acp.ToolCallStatusCompleted),
			RawOutput: map[string]any{"exit_code": 0},
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
		_ = u.closeTextArtifacts(ctx)
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

// TestStreamFramesMatchKagentContract pins the wire sequence against the run
// kagent captured from its own ADK runtime and documented in
// ui/src/api/chat/a2aGrpcChatClient.ts:
//
//	artifactUpdate  id=A  "alpha"              (no append)
//	artifactUpdate  id=A  " beta"              append: true
//	artifactUpdate  id=A  " gamma"             append: true
//	artifactUpdate  id=A  "alpha beta gamma"   lastChunk: true
//
// The deltas are what make the UI stream token by token; the closing frame is
// what settles the message and leaves one part in the stored task.
func TestStreamFramesMatchKagentContract(t *testing.T) {
	ctx := context.Background()
	events, _ := collect(t, func(u *a2aUpdater) {
		for _, chunk := range []string{"alpha", " beta", " gamma"} {
			if err := u.Update(ctx, textChunk(chunk)); err != nil {
				t.Errorf("Update: %v", err)
			}
		}
	})

	if len(events) != 4 {
		t.Fatalf("frames = %d, want 4 (3 chunks + closing replacement)", len(events))
	}

	type frame struct {
		text      string
		append    bool
		lastChunk bool
	}
	want := []frame{
		{"alpha", false, false},
		{" beta", true, false},
		{" gamma", true, false},
		{"alpha beta gamma", false, true},
	}

	var id a2a.ArtifactID
	for i, ev := range events {
		au, ok := ev.(*a2a.TaskArtifactUpdateEvent)
		if !ok {
			t.Fatalf("frame %d is %T, want *a2a.TaskArtifactUpdateEvent", i, ev)
		}
		if i == 0 {
			id = au.Artifact.ID
		} else if au.Artifact.ID != id {
			t.Errorf("frame %d artifact id = %q, want every frame to share %q", i, au.Artifact.ID, id)
		}
		got := frame{au.Artifact.Parts[0].Text(), au.Append, au.LastChunk}
		if got != want[i] {
			t.Errorf("frame %d = %+v, want %+v", i, got, want[i])
		}
	}
}

// TestThinkingFramesCarryThoughtMeta keeps the thought markers on every
// frame, including the closing replacement, so a client that classifies parts
// by metadata does not show the last one as an ordinary reply.
func TestThinkingFramesCarryThoughtMeta(t *testing.T) {
	ctx := context.Background()
	events, _ := collect(t, func(u *a2aUpdater) {
		_ = u.Update(ctx, thoughtChunk("weighing "))
		_ = u.Update(ctx, thoughtChunk("options"))
	})

	if len(events) != 3 {
		t.Fatalf("frames = %d, want 3 (2 chunks + closing replacement)", len(events))
	}
	for i, ev := range events {
		au := ev.(*a2a.TaskArtifactUpdateEvent)
		for _, key := range []string{kagentThoughtKey, adkMetaThoughtKey} {
			if v, ok := au.Artifact.Parts[0].Meta()[key].(bool); !ok || !v {
				t.Errorf("frame %d is missing %s=true", i, key)
			}
		}
	}
	last := events[2].(*a2a.TaskArtifactUpdateEvent)
	if got := last.Artifact.Parts[0].Text(); got != "weighing options" {
		t.Errorf("closing thinking text = %q, want %q", got, "weighing options")
	}
	if !last.LastChunk || last.Append {
		t.Errorf("closing frame append=%v lastChunk=%v, want append=false lastChunk=true",
			last.Append, last.LastChunk)
	}
}

// TestToolPartsMatchKagentShape pins the parts kagent's Claude and Codex
// harnesses emit (kagent go/api/a2a/tool_activity.go): the UI renders a data
// part as a tool card only when kagent.dev/a2a/part-type names it, and reads
// the card header from the tool name and its body from args / response.result.
func TestToolPartsMatchKagentShape(t *testing.T) {
	ctx := context.Background()
	events, _ := collect(t, func(u *a2aUpdater) {
		_ = u.Update(ctx, acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
			ToolCallId: "call-1", Title: "git status -s",
			RawInput: map[string]any{"command": "git status -s"},
			Meta:     map[string]any{adapter.ToolNameMetaKey: "bash"},
		}})
		_ = u.Update(ctx, acp.SessionUpdate{ToolCallUpdate: &acp.SessionToolCallUpdate{
			ToolCallId: "call-1", Status: statusPtr(acp.ToolCallStatusCompleted),
			RawOutput: map[string]any{"exit_code": 0},
		}})
	})

	if len(events) != 2 {
		t.Fatalf("frames = %d, want 2", len(events))
	}

	for i, want := range []string{partTypeFunctionCall, partTypeFunctionResponse} {
		ev := events[i].(*a2a.TaskArtifactUpdateEvent)
		part := ev.Artifact.Parts[0]
		for _, key := range []string{kagentPartTypeKey, adkMetaTypeKey} {
			if got, _ := part.Meta()[key].(string); got != want {
				t.Errorf("frame %d %s = %q, want %q", i, key, got, want)
			}
		}
		if !ev.LastChunk || ev.Append {
			t.Errorf("frame %d append=%v lastChunk=%v, want a complete artifact", i, ev.Append, ev.LastChunk)
		}
		if _, ok := ev.Artifact.Meta()[kagentTimelineKey].(string); !ok {
			t.Errorf("frame %d artifact has no %s", i, kagentTimelineKey)
		}
	}

	call := events[0].(*a2a.TaskArtifactUpdateEvent).Artifact.Parts[0].Data().(map[string]any)
	if call["id"] != "call-1" || call["name"] != "bash" {
		t.Errorf("call id/name = %v/%v, want call-1/bash", call["id"], call["name"])
	}
	if args, _ := call["args"].(map[string]any); args["command"] != "git status -s" {
		t.Errorf("call args = %v, want the raw input", call["args"])
	}

	resp := events[1].(*a2a.TaskArtifactUpdateEvent).Artifact.Parts[0].Data().(map[string]any)
	if resp["id"] != "call-1" || resp["name"] != "bash" {
		t.Errorf("response id/name = %v/%v, want the call's", resp["id"], resp["name"])
	}
	response, _ := resp["response"].(map[string]any)
	result, _ := response["result"].(map[string]any)
	if result["exit_code"] != float64(0) {
		t.Errorf("response = %v, want {result: raw output}", resp["response"])
	}
	if _, has := response["isError"]; has {
		t.Error("a completed call must not be marked isError")
	}
}

// TestToolNameFallsBackToTitle keeps a start without the tool-name meta — an
// ACP producer other than pi-go's adapter — named by its display title.
func TestToolNameFallsBackToTitle(t *testing.T) {
	ctx := context.Background()
	events, _ := collect(t, func(u *a2aUpdater) {
		_ = u.Update(ctx, acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
			ToolCallId: "call-1", Title: "Read example.go",
		}})
	})
	if len(events) != 1 {
		t.Fatalf("frames = %d, want 1", len(events))
	}
	data := events[0].(*a2a.TaskArtifactUpdateEvent).Artifact.Parts[0].Data().(map[string]any)
	if data["name"] != "Read example.go" {
		t.Errorf("name = %v, want the title", data["name"])
	}
}

// TestNonTerminalToolUpdateIsNotAResult pins the subagent case: the adapter
// re-sends a running subagent's card with each nested call. Those updates
// carry no terminal status and must not render as results — previously each
// became a nameless result card and the real result lost its name.
func TestNonTerminalToolUpdateIsNotAResult(t *testing.T) {
	ctx := context.Background()
	events, _ := collect(t, func(u *a2aUpdater) {
		_ = u.Update(ctx, acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
			ToolCallId: "call-1", Title: "subagent: explore",
			Meta: map[string]any{adapter.ToolNameMetaKey: "subagent"},
		}})
		_ = u.Update(ctx, acp.SessionUpdate{ToolCallUpdate: &acp.SessionToolCallUpdate{
			ToolCallId: "call-1", Content: []acp.ToolCallContent{{
				Content: &acp.ToolCallContentContent{Type: "content", Content: acp.TextBlock("▶ read(go.mod)")},
			}},
		}})
		_ = u.Update(ctx, acp.SessionUpdate{ToolCallUpdate: &acp.SessionToolCallUpdate{
			ToolCallId: "call-1", Status: statusPtr(acp.ToolCallStatusInProgress),
		}})
		_ = u.Update(ctx, acp.SessionUpdate{ToolCallUpdate: &acp.SessionToolCallUpdate{
			ToolCallId: "call-1", Status: statusPtr(acp.ToolCallStatusCompleted),
			RawOutput: "found it",
		}})
	})

	if len(events) != 2 {
		t.Fatalf("frames = %d, want 2 (call, one result)", len(events))
	}
	resp := events[1].(*a2a.TaskArtifactUpdateEvent).Artifact.Parts[0].Data().(map[string]any)
	if resp["name"] != "subagent" {
		t.Errorf("result name = %v, want the call's name", resp["name"])
	}
}

// TestFailedToolResultIsMarked keeps a failed call distinguishable from one
// that returned nothing: kagent's card reads response.isError.
func TestFailedToolResultIsMarked(t *testing.T) {
	ctx := context.Background()
	events, _ := collect(t, func(u *a2aUpdater) {
		_ = u.Update(ctx, acp.SessionUpdate{ToolCallUpdate: &acp.SessionToolCallUpdate{
			ToolCallId: "call-1", Status: statusPtr(acp.ToolCallStatusFailed),
			RawOutput: map[string]any{"error": "exit status 1"},
		}})
	})
	if len(events) != 1 {
		t.Fatalf("frames = %d, want 1", len(events))
	}
	resp := events[0].(*a2a.TaskArtifactUpdateEvent).Artifact.Parts[0].Data().(map[string]any)
	response, _ := resp["response"].(map[string]any)
	if response["isError"] != true {
		t.Errorf("response = %v, want isError=true", response)
	}
}

// TestToolPayloadsAreJSONValues pins the conversion: a tool result can be a Go
// struct, and the gRPC path converts data parts to a protobuf Struct, which
// rejects anything but maps, slices and scalars.
func TestToolPayloadsAreJSONValues(t *testing.T) {
	type out struct {
		Stdout string `json:"stdout"`
	}
	ctx := context.Background()
	events, _ := collect(t, func(u *a2aUpdater) {
		_ = u.Update(ctx, acp.SessionUpdate{ToolCallUpdate: &acp.SessionToolCallUpdate{
			ToolCallId: "call-1", Status: statusPtr(acp.ToolCallStatusCompleted),
			RawOutput: out{Stdout: "ok"},
		}})
	})
	resp := events[0].(*a2a.TaskArtifactUpdateEvent).Artifact.Parts[0].Data().(map[string]any)
	result, ok := resp["response"].(map[string]any)["result"].(map[string]any)
	if !ok || result["stdout"] != "ok" {
		t.Errorf("result = %#v, want map[stdout:ok]", resp["response"])
	}
}

// TestStoredTaskHasCompleteTimeline pins the reload order: the kagent UI sorts
// a stored task by timeline position only when every artifact has one, and a
// text artifact's closing replacement overwrites the metadata its first frame
// set — so that frame has to carry the position again.
func TestStoredTaskHasCompleteTimeline(t *testing.T) {
	ctx := context.Background()
	events, ec := collect(t, func(u *a2aUpdater) {
		_ = u.Update(ctx, thoughtChunk("thinking"))
		_ = u.Update(ctx, textChunk("checking"))
		_ = u.Update(ctx, acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{ToolCallId: "call-1", Title: "ls"}})
		_ = u.Update(ctx, acp.SessionUpdate{ToolCallUpdate: &acp.SessionToolCallUpdate{
			ToolCallId: "call-1", Status: statusPtr(acp.ToolCallStatusCompleted),
		}})
		_ = u.Update(ctx, textChunk("done"))
	})

	task := applyToTask(t, ec.TaskID, events)
	if len(task.Artifacts) != 5 {
		t.Fatalf("artifacts = %d, want 5", len(task.Artifacts))
	}
	positions := make([]string, len(task.Artifacts))
	for i, art := range task.Artifacts {
		p, ok := art.Meta()[kagentTimelineKey].(string)
		if !ok {
			t.Fatalf("artifact %d has no %s", i, kagentTimelineKey)
		}
		positions[i] = p
	}
	// Emission order: thought, text (both opened before the tool), call,
	// result, then the post-tool text.
	if !slices.IsSorted(positions) {
		t.Errorf("positions out of order: %v", positions)
	}
}

func statusPtr(s acp.ToolCallStatus) *acp.ToolCallStatus { return &s }

// blockedUpdater returns an updater nobody is draining, with its event buffer
// already full, so the next emit blocks. Paired with a canceled context that
// is how a send fails in practice: the client stopped pulling and the run
// context was canceled under it.
func blockedUpdater(t *testing.T) (*a2aUpdater, context.Context) {
	t.Helper()
	u := newA2AUpdater(context.Background(), newExecCtx("hello"))
	for len(u.events) < cap(u.events) {
		u.events <- a2a.NewArtifactEvent(u.execCtx, a2a.NewTextPart("filler"))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return u, ctx
}

// TestUpdateIgnoresUnknownAndEmpty covers the update kinds the translator has
// no mapping for, and a chunk with no text: both are dropped rather than
// emitted as an empty artifact, which a2a-go rejects.
func TestUpdateIgnoresUnknownAndEmpty(t *testing.T) {
	ctx := context.Background()
	events, _ := collect(t, func(u *a2aUpdater) {
		if err := u.Update(ctx, acp.SessionUpdate{}); err != nil {
			t.Errorf("unknown update: %v", err)
		}
		if err := u.Update(ctx, textChunk("")); err != nil {
			t.Errorf("empty chunk: %v", err)
		}
		if err := u.Update(ctx, acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{}}); err != nil {
			t.Errorf("chunk without content: %v", err)
		}
	})
	if len(events) != 0 {
		t.Fatalf("events = %d, want none", len(events))
	}
}

// TestEmitErrorsPropagate covers the send-failure paths: when nobody is
// draining and the run context is canceled, every emit fails, and each caller
// must surface that rather than carry on producing events nobody reads.
func TestEmitErrorsPropagate(t *testing.T) {
	t.Run("text chunk", func(t *testing.T) {
		u, ctx := blockedUpdater(t)
		if err := u.Update(ctx, textChunk("hi")); err == nil {
			t.Fatal("want an error when the consumer is gone")
		}
	})

	t.Run("closing frame", func(t *testing.T) {
		u := newA2AUpdater(context.Background(), newExecCtx("hello"))
		if err := u.Update(context.Background(), textChunk("hi")); err != nil {
			t.Fatalf("Update: %v", err)
		}
		for len(u.events) < cap(u.events) {
			u.events <- a2a.NewArtifactEvent(u.execCtx, a2a.NewTextPart("filler"))
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := u.closeTextArtifacts(ctx); err == nil {
			t.Fatal("want the closing frame to surface the cancellation")
		}
	})

	t.Run("tool start", func(t *testing.T) {
		u, ctx := blockedUpdater(t)
		err := u.Update(ctx, acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
			ToolCallId: "call-1", Title: "bash ls",
		}})
		if err == nil {
			t.Fatal("want an error when the consumer is gone")
		}
	})

	t.Run("tool end", func(t *testing.T) {
		u, ctx := blockedUpdater(t)
		err := u.Update(ctx, acp.SessionUpdate{ToolCallUpdate: &acp.SessionToolCallUpdate{
			ToolCallId: "call-1", Status: statusPtr(acp.ToolCallStatusCompleted),
		}})
		if err == nil {
			t.Fatal("want an error when the consumer is gone")
		}
	})

	// A tool boundary closes the open text artifact first, so a failure there
	// must abort the tool event rather than be swallowed.
	t.Run("tool boundary close failure", func(t *testing.T) {
		u := newA2AUpdater(context.Background(), newExecCtx("hello"))
		if err := u.Update(context.Background(), textChunk("hi")); err != nil {
			t.Fatalf("Update: %v", err)
		}
		for len(u.events) < cap(u.events) {
			u.events <- a2a.NewArtifactEvent(u.execCtx, a2a.NewTextPart("filler"))
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := u.Update(ctx, acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
			ToolCallId: "call-1", Title: "bash ls",
		}})
		if err == nil {
			t.Fatal("want the close failure to abort the tool event")
		}
	})
}

// TestToolNameFallsBackWhenUntitled keeps an untitled ACP tool call from
// rendering as a nameless card; the response carries the same fallback.
func TestToolNameFallsBackWhenUntitled(t *testing.T) {
	ctx := context.Background()
	events, _ := collect(t, func(u *a2aUpdater) {
		_ = u.Update(ctx, acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
			ToolCallId: "call-1",
		}})
		// A result whose call was never seen also falls back.
		_ = u.Update(ctx, acp.SessionUpdate{ToolCallUpdate: &acp.SessionToolCallUpdate{
			ToolCallId: "unknown-call", Status: statusPtr(acp.ToolCallStatusCompleted),
		}})
	})

	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	for i, ev := range events {
		data, ok := ev.(*a2a.TaskArtifactUpdateEvent).Artifact.Parts[0].Data().(map[string]any)
		if !ok {
			t.Fatalf("event %d part is not a data part", i)
		}
		if data["name"] != "tool" {
			t.Errorf("event %d name = %v, want the %q fallback", i, data["name"], "tool")
		}
	}
}

// TestCloseIsIdempotent keeps a second close from emitting a duplicate frame,
// which matters because the executor closes at the end of every turn and a
// tool boundary may have closed already.
func TestCloseIsIdempotent(t *testing.T) {
	ctx := context.Background()
	events, _ := collect(t, func(u *a2aUpdater) {
		_ = u.Update(ctx, textChunk("done"))
		if err := u.closeTextArtifacts(ctx); err != nil {
			t.Errorf("first close: %v", err)
		}
		if err := u.closeTextArtifacts(ctx); err != nil {
			t.Errorf("second close: %v", err)
		}
	})
	// chunk + one closing frame; the collect helper's own close adds nothing.
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (chunk + one closing frame)", len(events))
	}
}
