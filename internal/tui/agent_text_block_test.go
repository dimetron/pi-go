package tui

import (
	"strings"
	"testing"
)

// newTextBlockModel builds the minimal model handleAgentText/handleAgentThinking
// need: a chat transcript already holding the user turn plus the empty assistant
// placeholder that handleUserInput appends.
func newTextBlockModel() *model {
	m := &model{}
	m.chatModel.Messages = []message{
		{role: "user", content: "run agy ask to say hi and see what wrong"},
		{role: "assistant", content: ""},
	}
	return m
}

func assistantBlocks(m *model) []string {
	var out []string
	for _, msg := range m.chatModel.Messages {
		if msg.role == "assistant" {
			out = append(out, msg.content)
		}
	}
	return out
}

// TestAgentText_NewBlockAfterToolCallIsNotCumulative replays the exact sequence
// from the broken agy session: text, tool call, text, tool call, text. Each
// assistant block must hold only its own text. Before the fix the shared
// Streaming accumulator was never reset at a block boundary, so block 2 rendered
// block 1's text glued to its own ("...say hi.agy ran cleanly") and block 3
// repeated both again.
func TestAgentText_NewBlockAfterToolCallIsNotCumulative(t *testing.T) {
	m := newTextBlockModel()

	m.handleAgentText(agentTextMsg{text: "I'll spawn the agy subagent and ask it to say hi."})
	m.handleAgentToolCall(agentToolCallMsg{name: "subagent", id: "t1", args: map[string]any{"type": "agy"}})
	m.handleAgentText(agentTextMsg{text: "agy ran cleanly — 7.6s, returned a greeting."})
	m.handleAgentToolCall(agentToolCallMsg{name: "bash", id: "t2", args: map[string]any{"cmd": "ls"}})
	m.handleAgentText(agentTextMsg{text: "Nothing is wrong. The agy subagent works end-to-end."})

	got := assistantBlocks(m)
	want := []string{
		"I'll spawn the agy subagent and ask it to say hi.",
		"agy ran cleanly — 7.6s, returned a greeting.",
		"Nothing is wrong. The agy subagent works end-to-end.",
	}
	if len(got) != len(want) {
		t.Fatalf("assistant block count = %d, want %d\ngot: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("assistant block %d:\n got  %q\n want %q", i, got[i], want[i])
		}
	}
}

// TestAgentText_DeltasWithinOneBlockStillAccumulate guards the other direction:
// consecutive deltas with no tool call between them are one streamed message and
// must still concatenate.
func TestAgentText_DeltasWithinOneBlockStillAccumulate(t *testing.T) {
	m := newTextBlockModel()

	m.handleAgentText(agentTextMsg{text: "Hello"})
	m.handleAgentText(agentTextMsg{text: ", "})
	m.handleAgentText(agentTextMsg{text: "world."})

	got := assistantBlocks(m)
	if len(got) != 1 || got[0] != "Hello, world." {
		t.Fatalf("assistant blocks = %q, want one block %q", got, "Hello, world.")
	}
}

// TestAgentThinking_NewBlockAfterToolCallIsNotCumulative covers the same
// accumulator bug on the thinking path, where a tool call between two reasoning
// blocks likewise opens a new message.
func TestAgentThinking_NewBlockAfterToolCallIsNotCumulative(t *testing.T) {
	m := newTextBlockModel()
	// Drop the empty assistant placeholder so the first thinking delta appends
	// its own message, as it does when a turn opens with reasoning.
	m.chatModel.Messages = m.chatModel.Messages[:1]

	m.handleAgentThinking(agentThinkingMsg{text: "First I check the log."})
	m.handleAgentToolCall(agentToolCallMsg{name: "bash", id: "t1", args: map[string]any{"cmd": "ls"}})
	m.handleAgentThinking(agentThinkingMsg{text: "Now I read the file."})

	var got []string
	for _, msg := range m.chatModel.Messages {
		if msg.role == "thinking" {
			got = append(got, msg.content)
		}
	}
	want := []string{"First I check the log.", "Now I read the file."}
	if len(got) != len(want) {
		t.Fatalf("thinking block count = %d, want %d\ngot: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("thinking block %d:\n got  %q\n want %q", i, got[i], want[i])
		}
	}
}

// TestAgentText_TraceLogTracksCurrentBlock checks the trace pane does not
// inherit the cumulative text either.
func TestAgentText_TraceLogTracksCurrentBlock(t *testing.T) {
	m := newTextBlockModel()

	m.handleAgentText(agentTextMsg{text: "first"})
	m.handleAgentToolCall(agentToolCallMsg{name: "bash", id: "t1", args: map[string]any{"cmd": "ls"}})
	m.handleAgentText(agentTextMsg{text: "second"})

	var last string
	for _, e := range m.chatModel.TraceLog {
		if e.kind == "llm" {
			last = e.detail
		}
	}
	if strings.Contains(last, "first") {
		t.Errorf("trailing llm trace detail = %q, must not carry the previous block's text", last)
	}
}

// TestAgentText_AfterToolThenThinkingStartsClean covers the path where a
// reasoning block sits between the tool call and the answer. handleAgentText
// converts the trailing thinking message into the shell for that answer, so the
// text accumulator must restart there as well.
func TestAgentText_AfterToolThenThinkingStartsClean(t *testing.T) {
	m := newTextBlockModel()

	m.handleAgentText(agentTextMsg{text: "Let me check the log."})
	m.handleAgentToolCall(agentToolCallMsg{name: "bash", id: "t1", args: map[string]any{"cmd": "ls"}})
	m.handleAgentThinking(agentThinkingMsg{text: "The log looks clean."})
	m.handleAgentText(agentTextMsg{text: "Nothing is wrong."})

	got := assistantBlocks(m)
	want := []string{"Let me check the log.", "Nothing is wrong."}
	if len(got) != len(want) {
		t.Fatalf("assistant block count = %d, want %d\ngot: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("assistant block %d:\n got  %q\n want %q", i, got[i], want[i])
		}
	}
}
