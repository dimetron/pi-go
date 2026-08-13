package tui

import (
	"strings"
	"testing"
)

func bashCard(command string) message {
	return message{role: "tool", tool: "bash", toolIn: command}
}

// TestHandleBashEvent_BindsByCommand is the guard against interleaving. The
// model routinely issues several bash calls in one turn; binding to "the most
// recent bash card" would pour two commands' output into one card and leave the
// other blank.
func TestHandleBashEvent_BindsByCommand(t *testing.T) {
	m := &model{}
	m.chatModel.Messages = []message{
		bashCard("go build ./..."),
		bashCard("go test ./..."),
	}

	m.handleBashEvent(agentSubEventMsg{agentID: "bg_2", kind: "bash:start", content: "go test ./..."})
	m.handleBashEvent(agentSubEventMsg{agentID: "bg_1", kind: "bash:start", content: "go build ./..."})

	if got := m.chatModel.Messages[0].agentID; got != "bg_1" {
		t.Errorf("first card bound to %q, want bg_1", got)
	}
	if got := m.chatModel.Messages[1].agentID; got != "bg_2" {
		t.Errorf("second card bound to %q, want bg_2", got)
	}

	m.handleBashEvent(agentSubEventMsg{agentID: "bg_1", kind: "bash:output", content: "build line"})
	m.handleBashEvent(agentSubEventMsg{agentID: "bg_2", kind: "bash:output", content: "test line"})

	if len(m.chatModel.Messages[0].agentEvents) != 1 ||
		m.chatModel.Messages[0].agentEvents[0].content != "build line" {
		t.Errorf("first card events = %+v", m.chatModel.Messages[0].agentEvents)
	}
	if len(m.chatModel.Messages[1].agentEvents) != 1 ||
		m.chatModel.Messages[1].agentEvents[0].content != "test line" {
		t.Errorf("second card events = %+v", m.chatModel.Messages[1].agentEvents)
	}
}

// TestHandleBashEvent_IgnoresFinishedCards: a card that already has its result
// must not be reopened by a late event from a reused card slot.
func TestHandleBashEvent_IgnoresFinishedCards(t *testing.T) {
	m := &model{}
	finished := bashCard("echo done")
	finished.content = "done"
	m.chatModel.Messages = []message{finished, bashCard("echo done")}

	m.handleBashEvent(agentSubEventMsg{agentID: "bg_1", kind: "bash:start", content: "echo done"})

	if m.chatModel.Messages[0].agentID != "" {
		t.Error("a completed card was rebound to a new command")
	}
	if m.chatModel.Messages[1].agentID != "bg_1" {
		t.Errorf("pending card not bound, got %q", m.chatModel.Messages[1].agentID)
	}
}

// TestHandleBashEvent_CapsRetainedEvents keeps a chatty command from growing
// the message unboundedly behind a five-line window.
func TestHandleBashEvent_CapsRetainedEvents(t *testing.T) {
	m := &model{}
	m.chatModel.Messages = []message{bashCard("noisy")}
	m.handleBashEvent(agentSubEventMsg{agentID: "bg_1", kind: "bash:start", content: "noisy"})

	for range maxLiveBashEvents * 3 {
		m.handleBashEvent(agentSubEventMsg{agentID: "bg_1", kind: "bash:output", content: "line"})
	}

	if got := len(m.chatModel.Messages[0].agentEvents); got != maxLiveBashEvents {
		t.Errorf("retained %d events, want the cap of %d", got, maxLiveBashEvents)
	}
}

// TestToolCallSummary_BashControl shows the handle in the card header of a
// bash_wait/bash_kill poll. Without a case for these tools the header fell
// through to the empty summary and rendered as a bare "◉ bash_wait" with no
// clue which command was being polled.
func TestToolCallSummary_BashControl(t *testing.T) {
	if got := toolCallSummary("bash_wait", map[string]any{"handle": "bg_1"}); got != "bg_1" {
		t.Errorf("bash_wait summary = %q, want bg_1", got)
	}
	if got := toolCallSummary("bash_kill", map[string]any{"handle": "bg_2"}); got != "bg_2" {
		t.Errorf("bash_kill summary = %q, want bg_2", got)
	}
}

// TestHandleAgentToolCall_BashControlHeaderFoldsCommand verifies that a poll of
// a backgrounded command names the command in its card header. The bash card
// bound to the handle carries the command (handleBashEvent stamps agentID), and
// the poll card should read "bash_wait(bg_1: sleep 10 ...)" rather than a
// bare "◉ bash_wait".
func TestHandleAgentToolCall_BashControlHeaderFoldsCommand(t *testing.T) {
	m := newHandlerModel()
	m.chatModel.Messages = []message{
		{role: "tool", tool: "bash", toolIn: `sleep 10 && echo "done"`, agentID: "bg_1"},
	}

	m.handleAgentToolCall(agentToolCallMsg{
		id: "call_poll", name: "bash_wait", args: map[string]any{"handle": "bg_1"},
	})

	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if want := `bg_1: sleep 10 && echo "done"`; last.toolIn != want {
		t.Errorf("bash_wait card toolIn = %q, want %q", last.toolIn, want)
	}
}

// TestHandleAgentToolCall_BashControlHeaderFallsBackToHandle: when no bash card
// is bound to the handle (restored transcript, supervisor forgotten the
// command), the poll card still shows the handle.
func TestHandleAgentToolCall_BashControlHeaderFallsBackToHandle(t *testing.T) {
	m := newHandlerModel()

	m.handleAgentToolCall(agentToolCallMsg{
		id: "call_poll", name: "bash_kill", args: map[string]any{"handle": "bg_9"},
	})

	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if last.toolIn != "bg_9" {
		t.Errorf("bash_kill card toolIn = %q, want bg_9", last.toolIn)
	}
}

// TestHandleAgentToolResult_BindsBashHandleFromResult covers the recovery path
// for a lost bash:start binding: when the start event arrives before the card
// exists (separate buffered channel), the card never gets its agentID stamped.
// The bash result carries the handle, so it is the reliable place to bind —
// a later bash_wait/bash_kill card then still finds the command.
func TestHandleAgentToolResult_BindsBashHandleFromResult(t *testing.T) {
	m := newHandlerModel()
	m.chatModel.Messages = []message{
		{role: "tool", tool: "bash", toolIn: "sleep 10", toolID: "call_1"},
	}

	m.handleAgentToolResult(agentToolResultMsg{
		id: "call_1", name: "bash",
		content: `{"handle":"bg_1","running":true,"exit_code":-1}`,
	})

	if got := m.chatModel.Messages[0].agentID; got != "bg_1" {
		t.Errorf("bash card agentID = %q, want bg_1 (bound from result)", got)
	}

	// A foreground result (no handle) must not stamp anything.
	m2 := newHandlerModel()
	m2.chatModel.Messages = []message{
		{role: "tool", tool: "bash", toolIn: "echo hi", toolID: "call_2"},
	}
	m2.handleAgentToolResult(agentToolResultMsg{id: "call_2", name: "bash", content: `{"exit_code":0,"stdout":"hi"}`})
	if got := m2.chatModel.Messages[0].agentID; got != "" {
		t.Errorf("foreground bash card agentID = %q, want empty", got)
	}
}

// TestBashHandleFromResult covers the handle extractor directly.
func TestBashHandleFromResult(t *testing.T) {
	if got := bashHandleFromResult(`{"handle":"bg_3","running":true}`); got != "bg_3" {
		t.Errorf("bashHandleFromResult = %q, want bg_3", got)
	}
	if got := bashHandleFromResult(`{"exit_code":0,"stdout":"hi"}`); got != "" {
		t.Errorf("bashHandleFromResult = %q, want empty for foreground result", got)
	}
	if got := bashHandleFromResult("not json"); got != "" {
		t.Errorf("bashHandleFromResult = %q, want empty for non-JSON", got)
	}
}

// TestRenderLiveOutput_ShowsStallState is the reason the stream exists: a
// command that prints nothing must still show that it is alive and stuck,
// rather than rendering as an empty card indistinguishable from a hang.
func TestRenderLiveOutput_ShowsStallState(t *testing.T) {
	td := &ToolDisplayModel{Width: 100}
	msg := bashCard("find / -name '*.go'")
	msg.agentEvents = []agentEv{
		{kind: "heartbeat", content: "5s elapsed, 5s without output"},
		{kind: "heartbeat", content: "30s elapsed, 30s without output"},
		{kind: "stall", content: "no output for 30s"},
	}

	out := td.RenderToolMessage(msg)

	if !strings.Contains(out, "no output for 30s") {
		t.Errorf("live card should show the current stall state, got:\n%s", out)
	}
	// Only the latest state line belongs on screen; earlier heartbeats are
	// stale by definition and would push the output window off the card.
	if strings.Contains(out, "5s elapsed, 5s without output") {
		t.Errorf("stale heartbeat should have been superseded, got:\n%s", out)
	}
}

// TestRenderLiveOutput_ResultReplacesStream confirms the live window is a
// progress indicator, not a second copy of the output.
func TestRenderLiveOutput_ResultReplacesStream(t *testing.T) {
	td := &ToolDisplayModel{Width: 100}
	msg := bashCard("echo hi")
	msg.agentEvents = []agentEv{{kind: "output", content: "streamed-line"}}
	msg.content = "final-output"

	out := td.RenderToolMessage(msg)

	if !strings.Contains(out, "final-output") {
		t.Errorf("finished card should show the result, got:\n%s", out)
	}
	if strings.Contains(out, "streamed-line") {
		t.Errorf("finished card should drop the live stream, got:\n%s", out)
	}
}
