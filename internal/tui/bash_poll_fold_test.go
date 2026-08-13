package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// pollResult builds the JSON a bash_wait poll of a still-running command
// returns, so a test drives the same content the supervisor sends.
func pollResult(handle, stdout, elapsed, idle string) string {
	return fmt.Sprintf(
		`{"handle":%q,"command":"cd wiki && make daily-all","running":true,"stdout":%q,`+
			`"elapsed":%q,"idle":%q,"timeout":"3.6s","idle_timeout":"1s"}`,
		handle, stdout, elapsed, idle,
	)
}

// poll drives one full call/result round trip for a bash_wait poll.
func poll(m *model, id, handle, stdout, elapsed, idle string) {
	m.handleAgentToolCall(agentToolCallMsg{
		id: id, name: "bash_wait", args: map[string]any{"handle": handle},
	})
	m.handleAgentToolResult(agentToolResultMsg{
		id: id, name: "bash_wait", content: pollResult(handle, stdout, elapsed, idle),
	})
}

// A command that goes quiet is polled every few seconds, and every poll used to
// append its own card — the transcript filled with a dozen near-identical
// "◉ bash_wait(bg_4: make daily-all)" blocks whose only difference was the
// elapsed counter, pushing everything the user cared about off the screen.
// Repeat polls of the same handle refresh one card in place instead.
func TestBashPoll_RepeatedPollsRefreshOneCard(t *testing.T) {
	m := newHandlerModel()

	poll(m, "call_1", "bg_4", "", "27.1s", "7.7s")
	poll(m, "call_2", "bg_4", "", "29.5s", "10.2s")
	poll(m, "call_3", "bg_4", "compiling", "32.1s", "0.1s")

	if got := len(m.chatModel.Messages); got != 1 {
		t.Fatalf("3 polls of one handle produced %d cards, want 1", got)
	}
	card := m.chatModel.Messages[0]
	if card.pollCount != 3 {
		t.Errorf("pollCount = %d, want 3", card.pollCount)
	}
	if card.toolID != "call_3" {
		t.Errorf("card toolID = %q, want the newest call call_3", card.toolID)
	}
	if !strings.Contains(card.content, "compiling") || !strings.Contains(card.content, "32.1s elapsed") {
		t.Errorf("card does not show the newest poll: %q", card.content)
	}
	if card.pendingRefresh {
		t.Error("pendingRefresh still set after the result arrived")
	}
}

// The window from the previous poll has to stay on screen while the next poll
// runs. Blanking the card for the seconds a poll takes would make a live
// command look like a card that lost its output.
func TestBashPoll_KeepsPreviousWindowWhileRefreshing(t *testing.T) {
	m := newHandlerModel()
	poll(m, "call_1", "bg_4", "building", "5s", "1s")

	m.handleAgentToolCall(agentToolCallMsg{
		id: "call_2", name: "bash_wait", args: map[string]any{"handle": "bg_4"},
	})

	card := m.chatModel.Messages[0]
	if !strings.Contains(card.content, "building") {
		t.Errorf("previous window lost while the next poll is in flight: %q", card.content)
	}
	if !card.pendingRefresh {
		t.Error("refreshing card not marked pendingRefresh")
	}
	if !card.toolPending() {
		t.Error("refreshing card must count as pending so its bullet keeps blinking")
	}
}

// Folding only ever touches the newest card. A poll that follows something the
// model said is a separate beat in the transcript, and pulling its output up
// into an older card would reorder the scrollback under the reader.
func TestBashPoll_TextBetweenPollsStartsANewCard(t *testing.T) {
	m := newHandlerModel()
	poll(m, "call_1", "bg_4", "", "5s", "5s")

	m.handleAgentText(agentTextMsg{text: "still waiting on the build"})
	poll(m, "call_2", "bg_4", "", "10s", "10s")

	if got := len(m.chatModel.Messages); got != 3 {
		t.Fatalf("got %d messages, want poll + text + poll", got)
	}
	if m.chatModel.Messages[2].tool != "bash_wait" || m.chatModel.Messages[2].pollCount != 1 {
		t.Errorf("poll after text did not get its own fresh card: %+v", m.chatModel.Messages[2])
	}
}

// Two backgrounded commands polled in the same turn must keep separate cards:
// merging them would show one command's output under the other's header.
func TestBashPoll_DifferentHandlesKeepSeparateCards(t *testing.T) {
	m := newHandlerModel()

	poll(m, "call_1", "bg_1", "build line", "5s", "1s")
	poll(m, "call_2", "bg_2", "test line", "5s", "1s")
	poll(m, "call_3", "bg_1", "build line 2", "9s", "1s")

	if got := len(m.chatModel.Messages); got != 3 {
		t.Fatalf("got %d cards, want one per poll (no handle may fold into another)", got)
	}
	if !strings.Contains(m.chatModel.Messages[1].content, "test line") {
		t.Errorf("bg_2 card content = %q", m.chatModel.Messages[1].content)
	}
	if !strings.Contains(m.chatModel.Messages[2].content, "build line 2") {
		t.Errorf("second bg_1 poll card content = %q", m.chatModel.Messages[2].content)
	}
}

// The parallel case, with both calls issued before either result lands: the
// results must still reach their own cards. A folded card carries a fresh
// toolID, so this is the guard that folding did not break ID matching.
func TestBashPoll_ParallelPollsNotTransposed(t *testing.T) {
	m := newHandlerModel()

	m.handleAgentToolCall(agentToolCallMsg{
		id: "call_1", name: "bash_wait", args: map[string]any{"handle": "bg_1"},
	})
	m.handleAgentToolCall(agentToolCallMsg{
		id: "call_2", name: "bash_wait", args: map[string]any{"handle": "bg_2"},
	})
	m.handleAgentToolResult(agentToolResultMsg{
		id: "call_2", name: "bash_wait", content: pollResult("bg_2", "test line", "5s", "1s"),
	})
	m.handleAgentToolResult(agentToolResultMsg{
		id: "call_1", name: "bash_wait", content: pollResult("bg_1", "build line", "5s", "1s"),
	})

	if !strings.Contains(m.chatModel.Messages[0].content, "build line") {
		t.Errorf("bg_1 card got %q, want the build output", m.chatModel.Messages[0].content)
	}
	if !strings.Contains(m.chatModel.Messages[1].content, "test line") {
		t.Errorf("bg_2 card got %q, want the test output", m.chatModel.Messages[1].content)
	}
}

// bash_kill ends a command, so it is never a repeat of anything and always
// deserves its own card — the poll before it and the kill are different events.
func TestBashPoll_KillIsNotFoldedIntoAPoll(t *testing.T) {
	m := newHandlerModel()
	poll(m, "call_1", "bg_4", "", "5s", "5s")

	m.handleAgentToolCall(agentToolCallMsg{
		id: "call_2", name: "bash_kill", args: map[string]any{"handle": "bg_4"},
	})

	if got := len(m.chatModel.Messages); got != 2 {
		t.Fatalf("got %d cards, want the poll and the kill to stay separate", got)
	}
	if m.chatModel.Messages[1].tool != "bash_kill" {
		t.Errorf("second card = %q, want bash_kill", m.chatModel.Messages[1].tool)
	}
}

// Folding is bash_wait-only. Repeated subagent calls each spawn a real child
// with its own event stream, so every call keeps its own card — collapsing them
// would hide one agent's work behind another's.
func TestBashPoll_SubagentCardsAreNeverFolded(t *testing.T) {
	m := newHandlerModel()

	for i, id := range []string{"call_1", "call_2"} {
		m.handleAgentToolCall(agentToolCallMsg{
			id: id, name: "agent",
			args: map[string]any{"type": "explore", "prompt": fmt.Sprintf("search %d", i)},
		})
	}

	if got := len(m.chatModel.Messages); got != 2 {
		t.Fatalf("got %d subagent cards, want one per call", got)
	}
}

// A parallel subagent call fans out into one card per child; each card must
// stay independent so the two agents' streams never share a window.
func TestSubagentCards_ParallelFanOutKeepsOneCardPerChild(t *testing.T) {
	m := newHandlerModel()

	m.handleAgentToolCall(agentToolCallMsg{
		id: "call_1", name: "agent",
		args: map[string]any{"tasks": []any{
			map[string]any{"agent": "claude", "task": "review the diff"},
			map[string]any{"agent": "explore", "task": "map the package"},
		}},
	})

	if got := len(m.chatModel.Messages); got != 2 {
		t.Fatalf("parallel call produced %d cards, want one per child", got)
	}
	if got := m.chatModel.Messages[0].agentType; got != "claude" {
		t.Errorf("first card agentType = %q, want claude", got)
	}
	if got := m.chatModel.Messages[1].agentType; got != "explore" {
		t.Errorf("second card agentType = %q, want explore", got)
	}
}

// A `bash` card names its command; a poll of that command should too. It used
// to manage that only by finding the bash card that started the handle, so a
// poll in a restored session — or after the bash:start binding was lost — read
// "bash_wait(bg_4)" and never said what was running. The poll result carries
// the command (BashStatus.Command), which is the source that always exists.
func TestBashPoll_HeaderNamesCommandFromTheResult(t *testing.T) {
	m := newHandlerModel() // no bash card in the transcript to cross-reference

	poll(m, "call_1", "bg_4", "", "27.1s", "7.7s")

	if got, want := m.chatModel.Messages[0].toolIn, "bg_4: cd wiki && make daily-all"; got != want {
		t.Errorf("poll card header = %q, want %q", got, want)
	}
}

// bash_kill recovers the command the same way, so the card that reports a
// command's death says which command died.
func TestBashKill_HeaderNamesCommandFromTheResult(t *testing.T) {
	m := newHandlerModel()

	m.handleAgentToolCall(agentToolCallMsg{
		id: "call_1", name: "bash_kill", args: map[string]any{"handle": "bg_4"},
	})
	m.handleAgentToolResult(agentToolResultMsg{
		id: "call_1", name: "bash_kill",
		content: `{"handle":"bg_4","command":"make daily-all","running":false,"exit_code":-1,"stdout":""}`,
	})

	if got, want := m.chatModel.Messages[0].toolIn, "bg_4: make daily-all"; got != want {
		t.Errorf("kill card header = %q, want %q", got, want)
	}
}

// A header is one line by construction. A backgrounded heredoc or multi-line
// pipeline must be collapsed, or its newlines write straight into the card and
// knock the rail out of its column.
func TestBashPoll_HeaderCollapsesMultiLineCommand(t *testing.T) {
	m := newHandlerModel()

	m.handleAgentToolCall(agentToolCallMsg{
		id: "call_1", name: "bash_wait", args: map[string]any{"handle": "bg_1"},
	})
	m.handleAgentToolResult(agentToolResultMsg{
		id: "call_1", name: "bash_wait",
		content: `{"handle":"bg_1","command":"cat <<EOF\n  line one\n  line two\nEOF","running":true,"stdout":""}`,
	})

	got := m.chatModel.Messages[0].toolIn
	if strings.ContainsAny(got, "\n\t") {
		t.Errorf("header carries raw newlines: %q", got)
	}
	if want := "bg_1: cat <<EOF line one line two EOF"; got != want {
		t.Errorf("header = %q, want %q", got, want)
	}
}

// A result that names no command must leave the header the call built — an
// older supervisor, or a transcript replayed from a session written before
// BashStatus carried the command, should not blank the handle out.
func TestBashPoll_HeaderKeptWhenResultNamesNoCommand(t *testing.T) {
	m := newHandlerModel()
	m.chatModel.Messages = []message{
		{role: "tool", tool: "bash", toolIn: "sleep 10", agentID: "bg_1"},
	}

	m.handleAgentToolCall(agentToolCallMsg{
		id: "call_1", name: "bash_wait", args: map[string]any{"handle": "bg_1"},
	})
	m.handleAgentToolResult(agentToolResultMsg{
		id: "call_1", name: "bash_wait", content: `{"handle":"bg_1","running":true,"stdout":""}`,
	})

	if got, want := m.chatModel.Messages[1].toolIn, "bg_1: sleep 10"; got != want {
		t.Errorf("header = %q, want the one built at call time %q", got, want)
	}
}

// The tally is the only trace the folded-away polls leave. Without it a card
// showing "32.1s elapsed" looks like a single slow poll rather than the tenth
// check on a command the agent has been watching for half a minute.
func TestRenderRegularTool_ShowsPollTally(t *testing.T) {
	td := &ToolDisplayModel{Width: 100, BlinkOn: true}
	card := message{
		role: "tool", tool: "bash_wait", toolIn: "bg_4: make daily-all",
		content: "⏳ 32.1s elapsed", pollCount: 7,
	}

	got := lipgloss.NewStyle().Render(td.RenderToolMessage(card))

	if !strings.Contains(got, "×7") {
		t.Errorf("expected the ×7 poll tally in %q", got)
	}
	td.CompactTools = true
	if got := td.RenderToolMessage(card); !strings.Contains(got, "×7") {
		t.Errorf("expected the ×7 poll tally in the compact card %q", got)
	}
}

// A card called once must not carry a tally — "×1" on every tool card would be
// noise on the common case.
func TestRenderRegularTool_NoTallyForASingleCall(t *testing.T) {
	td := &ToolDisplayModel{Width: 100, BlinkOn: true}
	card := message{role: "tool", tool: "bash", toolIn: "make build", content: "ok", pollCount: 1}

	if got := td.RenderToolMessage(card); strings.Contains(got, "×") {
		t.Errorf("single-call card carries a tally: %q", got)
	}
}

// The render cache keys on the message's fields, so a fold that changes only
// the counter or the pending flag must still produce a different key —
// otherwise the refreshed card would redraw from the previous poll's cache.
func TestRenderKey_ChangesWhenAPollFoldsIn(t *testing.T) {
	base := message{role: "tool", tool: "bash_wait", toolIn: "bg_4: make daily-all", content: "⏳ 5s"}
	key := func(m message) uint64 { return m.renderKey(100, false, false, false, 0, false) }

	folded := base
	folded.pollCount = 2
	if key(base) == key(folded) {
		t.Error("a bumped poll count did not change the render key")
	}

	refreshing := base
	refreshing.pendingRefresh = true
	if key(base) == key(refreshing) {
		t.Error("pendingRefresh did not change the render key")
	}
}

// A session restored from before the rename replays the old tool name in its
// events. The display still has to recognize it as a poll — header, window
// formatter and fold all key off the name — or an old transcript degrades into
// bare unknown-tool cards with no command and no window. Same reason the
// display answers to both "grep" and "ripgrep".
func TestBashPoll_LegacyNameIsStillRecognized(t *testing.T) {
	m := newHandlerModel()

	for _, id := range []string{"call_1", "call_2"} {
		m.handleAgentToolCall(agentToolCallMsg{
			id: id, name: "bash_output", args: map[string]any{"handle": "bg_4"},
		})
		m.handleAgentToolResult(agentToolResultMsg{
			id: id, name: "bash_output", content: pollResult("bg_4", "", "5s", "5s"),
		})
	}

	if got := len(m.chatModel.Messages); got != 1 {
		t.Fatalf("legacy-named polls produced %d cards, want them folded into 1", got)
	}
	card := m.chatModel.Messages[0]
	if card.pollCount != 2 {
		t.Errorf("pollCount = %d, want 2", card.pollCount)
	}
	if want := "bg_4: cd wiki && make daily-all"; card.toolIn != want {
		t.Errorf("legacy card header = %q, want %q", card.toolIn, want)
	}
	if got := toolCallSummary("bash_output", map[string]any{"handle": "bg_1"}); got != "bg_1" {
		t.Errorf("legacy toolCallSummary = %q, want bg_1", got)
	}
}
