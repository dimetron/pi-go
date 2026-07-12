package tui

import (
	"strings"
	"testing"
)

// agentCardLines returns the gutter lines of a rendered subagent card — the
// output window itself, excluding the "● agent[...]" header.
func agentCardLines(t *testing.T, msg message, width int) []string {
	t.Helper()
	td := &ToolDisplayModel{Width: width}

	var out []string
	for _, line := range strings.Split(td.RenderToolMessage(msg), "\n") {
		if strings.Contains(line, "│ ") {
			out = append(out, line)
		}
	}
	return out
}

// The subagent output window is 7 lines.
func TestAgentOutputWindowIsSevenLines(t *testing.T) {
	if maxAgentOutputLines != 7 {
		t.Fatalf("maxAgentOutputLines = %d, want 7", maxAgentOutputLines)
	}
}

// The bug from the screenshot: a subagent's final analysis arrives as ONE "text"
// event carrying thousands of characters. Capping the number of events caps
// nothing — that single event soft-wraps into a screenful (74 lines, measured)
// and buries the chat.
func TestAgentOutputWindowCapsAHugeSingleEvent(t *testing.T) {
	flood := strings.Repeat(
		"Here is the analysis: the provider package resolves models by prefix. ", 80)

	msg := message{
		role: "tool", tool: "agent", agentType: "pi", agentTitle: "Analyze internal/subagent",
		agentEvents: []agentEv{{kind: "text", content: flood}},
	}

	lines := agentCardLines(t, msg, 100)

	// 7 output lines, plus the note saying output was withheld.
	if len(lines) > 8 {
		t.Fatalf("one huge event rendered %d gutter lines, want 7 plus a note", len(lines))
	}
	if len(lines) == 0 {
		t.Fatal("the window swallowed the output entirely")
	}

	// Clipping 67 of 74 lines with no mark would read as if that were all the
	// agent said.
	if !strings.Contains(strings.Join(lines, "\n"), "earlier output") {
		t.Error("output was clipped with no note that anything was withheld")
	}
}

// Many events must also fit the window, and the newest ones are the ones kept —
// the card is a live progress view, so the latest activity is what matters.
func TestAgentOutputWindowKeepsNewestAcrossManyEvents(t *testing.T) {
	var events []agentEv
	for i := range 40 {
		events = append(events, agentEv{kind: "tool_call", content: "step-" + string(rune('a'+i%26))})
	}
	events = append(events, agentEv{kind: "text", content: "NEWEST-OUTPUT-MARKER"})

	msg := message{
		role: "tool", tool: "agent", agentType: "pi",
		agentEvents: events,
	}

	rendered := (&ToolDisplayModel{Width: 100}).RenderToolMessage(msg)
	lines := agentCardLines(t, msg, 100)

	// The "... N earlier events" note is one of the gutter lines, so the window
	// itself is bounded by maxAgentOutputLines and the note sits above it.
	if len(lines) > maxAgentOutputLines+1 {
		t.Fatalf("card rendered %d gutter lines, want at most %d output lines plus one note",
			len(lines), maxAgentOutputLines)
	}
	if !strings.Contains(rendered, "NEWEST-OUTPUT-MARKER") {
		t.Error("newest event was dropped; the window must keep the latest activity")
	}
	if !strings.Contains(rendered, "earlier events") {
		t.Error("no note that earlier events were hidden")
	}
}

// A short stream is shown in full, with no truncation note.
func TestAgentOutputWindowLeavesShortStreamsAlone(t *testing.T) {
	msg := message{
		role: "tool", tool: "agent", agentType: "pi",
		agentEvents: []agentEv{
			{kind: "tool_call", content: "read server.go"},
			{kind: "tool_result", content: "ok"},
		},
	}

	rendered := (&ToolDisplayModel{Width: 100}).RenderToolMessage(msg)

	if strings.Contains(rendered, "earlier events") {
		t.Error("truncation note shown for a stream that fits")
	}
	if !strings.Contains(rendered, "read server.go") {
		t.Error("a short stream must render in full")
	}
}

// Clipping must not split a multi-byte rune into a replacement character.
func TestTruncateRunesIsUTF8Safe(t *testing.T) {
	s := strings.Repeat("é", 50)
	got := truncateRunes(s, 10)

	if strings.Contains(got, "�") {
		t.Fatalf("truncateRunes split a rune: %q", got)
	}
	if n := len([]rune(got)); n != 10 {
		t.Errorf("got %d runes, want 10", n)
	}
}
