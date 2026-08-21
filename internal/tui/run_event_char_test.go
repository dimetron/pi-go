package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/subagent"
)

// runEventCase drives a sequence of subagent events through handleRunAgentEvent
// and pins the model state they produce. handleRunAgentEvent renders nothing —
// its whole output is the mutation it performs — so the state snapshot is the
// equivalent of a rendered golden here.
type runEventCase struct {
	name   string
	build  func() *model
	events []subagent.Event
}

// newRunEventModel is the baseline model the cases start from: a live run with
// one empty assistant placeholder, which is what the stream arrives into.
func newRunEventModel(t *testing.T, msgs []message, run *runState) *model {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &model{
		ctx:       ctx,
		cancel:    cancel,
		width:     120,
		height:    40,
		chatModel: ChatModel{Messages: msgs},
		run:       run,
		running:   true,
	}
}

func runEventCases(t *testing.T) []runEventCase {
	t.Helper()

	assistantPlaceholder := func() []message {
		return []message{{role: "assistant", content: ""}}
	}
	liveRun := func() *runState {
		return &runState{specName: "test-spec", agentID: "task-1", phase: "running"}
	}

	return []runEventCase{
		{
			name:   "text_delta accumulates into the last assistant message",
			build:  func() *model { return newRunEventModel(t, assistantPlaceholder(), liveRun()) },
			events: []subagent.Event{{Type: "text_delta", Content: "Hello "}, {Type: "text_delta", Content: "world"}},
		},
		{
			// No assistant message to land in: the reverse scan finds nothing
			// and the delta survives only in Streaming.
			name:   "text_delta with no assistant message to update",
			build:  func() *model { return newRunEventModel(t, []message{{role: "user", content: "go"}}, liveRun()) },
			events: []subagent.Event{{Type: "text_delta", Content: "orphan"}},
		},
		{
			// A tool_call pushes a non-llm entry onto the trace, so the next
			// delta appends a fresh llm entry instead of extending the last.
			name:  "text_delta appends a new trace entry after a tool_call",
			build: func() *model { return newRunEventModel(t, assistantPlaceholder(), liveRun()) },
			events: []subagent.Event{
				{Type: "text_delta", Content: "first"},
				{Type: "tool_call", Content: "read"},
				{Type: "text_delta", Content: "second"},
			},
		},
		{
			name:  "tool_call arg shapes",
			build: func() *model { return newRunEventModel(t, assistantPlaceholder(), liveRun()) },
			events: []subagent.Event{
				{Type: "tool_call", Content: "read", ToolArgs: map[string]any{"file_path": "internal/tui/run.go"}},
				{Type: "tool_call", Content: "bash", ToolArgs: map[string]any{"command": "go test ./..."}},
				{Type: "tool_call", Content: "grep"},
				// A non-map ToolArgs must not panic and must leave toolIn empty.
				{Type: "tool_call", Content: "edit", ToolArgs: "not-a-map"},
			},
		},
		{
			// Several assistant turns are on screen. The delta belongs to the
			// newest one; writing to an older card would rewrite a turn the
			// user has already read.
			name: "text_delta updates the newest of several assistant messages",
			build: func() *model {
				return newRunEventModel(t, []message{
					{role: "assistant", content: "older turn"},
					{role: "tool", tool: "bash", content: "done"},
					{role: "assistant", content: ""},
				}, liveRun())
			},
			events: []subagent.Event{{Type: "text_delta", Content: "newest"}},
		},
		{
			// Two tool cards are still open. The result closes the newest.
			name: "tool_result fills the newest of several open tool cards",
			build: func() *model {
				return newRunEventModel(t, []message{
					{role: "tool", tool: "read", content: ""},
					{role: "tool", tool: "bash", content: ""},
				}, liveRun())
			},
			events: []subagent.Event{{Type: "tool_result", Content: "second result"}},
		},
		{
			name:  "tool_result fills the last empty tool message",
			build: func() *model { return newRunEventModel(t, assistantPlaceholder(), liveRun()) },
			events: []subagent.Event{
				{Type: "tool_call", Content: "bash"},
				{Type: "tool_result", Content: `{"exit_code": 0, "stdout": "ok"}`},
			},
		},
		{
			// Already-filled tool messages are skipped; with none empty the
			// result lands nowhere but still clears the active tool.
			name: "tool_result with no empty tool message to fill",
			build: func() *model {
				return newRunEventModel(t, []message{{role: "tool", tool: "bash", content: "done"}}, liveRun())
			},
			events: []subagent.Event{{Type: "tool_result", Content: "orphan result"}},
		},
		{
			// write and edit trigger a checklist refresh; other tools do not.
			// With no orchestrator the refresh is a no-op, so this pins the
			// surrounding state rather than the read itself.
			name:  "tool_result after a write",
			build: func() *model { return newRunEventModel(t, assistantPlaceholder(), liveRun()) },
			events: []subagent.Event{
				{Type: "tool_call", Content: "write"},
				{Type: "tool_result", Content: "wrote 12 lines"},
			},
		},
		{
			name:  "tool_result after a write with no live run",
			build: func() *model { return newRunEventModel(t, assistantPlaceholder(), nil) },
			events: []subagent.Event{
				{Type: "tool_call", Content: "write"},
				{Type: "tool_result", Content: "wrote 12 lines"},
			},
		},
		{
			name:  "message_start opens a fresh assistant placeholder",
			build: func() *model { return newRunEventModel(t, assistantPlaceholder(), liveRun()) },
			events: []subagent.Event{
				{Type: "text_delta", Content: "leftover"},
				{Type: "message_start"},
			},
		},
		{
			name:  "message_end clears the streaming accumulator",
			build: func() *model { return newRunEventModel(t, assistantPlaceholder(), liveRun()) },
			events: []subagent.Event{
				{Type: "text_delta", Content: "leftover"},
				{Type: "message_end"},
			},
		},
		{
			name:   "error appends a message and a trace entry",
			build:  func() *model { return newRunEventModel(t, assistantPlaceholder(), liveRun()) },
			events: []subagent.Event{{Type: "error", Error: "connection reset by peer"}},
		},
		{
			// An unrecognized type must change nothing except the matrix feed.
			name:   "unknown event type is inert",
			build:  func() *model { return newRunEventModel(t, assistantPlaceholder(), liveRun()) },
			events: []subagent.Event{{Type: "heartbeat", Content: "tick"}},
		},
		{
			// Empty content falls back to feeding the type name, so the rain
			// still reacts to a content-free event.
			name:   "matrix falls back to the event type when content is empty",
			build:  func() *model { return newRunEventModel(t, assistantPlaceholder(), liveRun()) },
			events: []subagent.Event{{Type: "message_end"}},
		},
		{
			// Neither content nor type: nothing is fed at all.
			name:   "matrix is not fed when content and type are both empty",
			build:  func() *model { return newRunEventModel(t, assistantPlaceholder(), liveRun()) },
			events: []subagent.Event{{}},
		},
	}
}

// snapshotRunEventState serializes everything handleRunAgentEvent can touch,
// leaving out wall-clock fields (traceEntry.time, statusModel.ToolStart) which
// are recorded only as set/unset so the snapshot stays reproducible.
func snapshotRunEventState(m *model) string {
	var b strings.Builder

	fmt.Fprintf(&b, "streaming=%q scroll=%d activeTool=%q toolStartSet=%v\n",
		m.chatModel.Streaming, m.chatModel.Scroll, m.statusModel.ActiveTool,
		!m.statusModel.ToolStart.IsZero())
	fmt.Fprintf(&b, "matrix active=%v seed=%d\n", m.matrix.active, m.matrix.seed)

	b.WriteString("messages:\n")
	for i, msg := range m.chatModel.Messages {
		fmt.Fprintf(&b, "  [%d] role=%q tool=%q toolIn=%q content=%q\n",
			i, msg.role, msg.tool, msg.toolIn, msg.content)
	}

	b.WriteString("trace:\n")
	for i, e := range m.chatModel.TraceLog {
		fmt.Fprintf(&b, "  [%d] kind=%q summary=%q detail=%q timeSet=%v\n",
			i, e.kind, e.summary, e.detail, !e.time.IsZero())
	}

	if m.run == nil {
		b.WriteString("run=nil\n")
		return b.String()
	}
	fmt.Fprintf(&b, "run phase=%q checklist=%d\n", m.run.phase, len(m.run.checklist))
	return b.String()
}

// TestHandleRunAgentEventStateGolden pins the model mutation each event kind
// performs. It is the characterization net for the handler split: the handler
// produces no text, so its state is the thing that must not drift.
func TestHandleRunAgentEventStateGolden(t *testing.T) {
	t.Parallel()

	for _, tc := range runEventCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := tc.build()
			for _, ev := range tc.events {
				m.handleRunAgentEvent(runAgentEventMsg{event: ev, agentID: "task-1"})
			}
			assertRunGolden(t, "run_events", tc.name, snapshotRunEventState(m))
		})
	}
}
