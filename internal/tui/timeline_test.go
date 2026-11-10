package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestTimelineItems_OneRowPerUserTurn(t *testing.T) {
	msgs := []message{
		{role: "user", content: "first question"},
		{role: "assistant", content: "an answer"},
		{role: "tool", content: `{"stdout":"x"}`},
		{role: "user", content: "second question\nwith a second line"},
		{role: "assistant", content: "another answer"},
	}

	items := timelineSearchItems(msgs)
	if len(items) != 2 {
		t.Fatalf("got %d rows, want 2 (one per user turn): %+v", len(items), items)
	}
	if items[0].Text != "1" || items[1].Text != "2" {
		t.Errorf("turn numbering = %q,%q, want 1,2", items[0].Text, items[1].Text)
	}
	if items[0].MessageIdx != 0 || items[1].MessageIdx != 3 {
		t.Errorf("message indices = %d,%d, want 0,3", items[0].MessageIdx, items[1].MessageIdx)
	}
	// The preview is the first line only, so a multi-line prompt does not break
	// the row.
	if strings.Contains(items[1].Description, "\n") {
		t.Errorf("preview kept a newline: %q", items[1].Description)
	}
	if !strings.HasPrefix(items[1].Description, "second question") {
		t.Errorf("preview = %q, want the first line of the prompt", items[1].Description)
	}
}

func TestTimelineItems_NoTurns(t *testing.T) {
	msgs := []message{{role: "assistant", content: "hi"}}
	if items := timelineSearchItems(msgs); len(items) != 0 {
		t.Errorf("got %d rows for a transcript with no user turn", len(items))
	}
}

func TestTimelineCommand_OpensAndToggles(t *testing.T) {
	m := newTestModelFull(t)
	m.chatModel.Messages = []message{
		{role: "user", content: "q1"},
		{role: "assistant", content: "a1"},
		{role: "user", content: "q2"},
	}

	m.handleTimelineCommand()
	if m.searchPopup == nil {
		t.Fatal("/timeline did not open the picker")
	}
	if m.searchPopup.mode != searchModeTimeline {
		t.Errorf("mode = %q, want %q", m.searchPopup.mode, searchModeTimeline)
	}
	if got := len(m.searchPopup.entries); got != 2 {
		t.Errorf("picker has %d rows, want 2", got)
	}

	// A second invocation closes it rather than stacking windows.
	m.handleTimelineCommand()
	if m.searchPopup != nil {
		t.Error("second /timeline left the picker open")
	}
}

func TestTimelineCommand_NoTurnsFlashes(t *testing.T) {
	m := newTestModelFull(t)
	m.chatModel.Messages = nil
	m.handleTimelineCommand()
	if m.searchPopup != nil {
		t.Error("opened an empty timeline picker")
	}
	if m.flash == "" {
		t.Error("expected a flash explaining there are no turns")
	}
}

// buildTranscript renders a conversation through the real render path and
// returns the model with the resulting line offsets recorded, which is what
// /timeline reads.
func buildTranscript(t *testing.T, widths int) *model {
	t.Helper()
	m := newTestModelFull(t)
	m.width = widths
	m.height = 40
	m.chatModel.Width = widths
	var msgs []message
	// Enough turns that the transcript is several viewports tall; with a
	// transcript that already fits, every jump is correctly a no-op and the
	// test could not tell a working jump from a broken one.
	for i := 0; i < 25; i++ {
		msgs = append(msgs,
			message{role: "user", content: strings.Repeat("question ", 3)},
			message{role: "assistant", content: strings.Repeat("answer ", 20)},
		)
	}
	m.chatModel.Messages = msgs
	// One real frame, so the offsets are the ones the renderer produces.
	m.View()
	return m
}

func TestTimelineJump_LandsOnTheChosenTurn(t *testing.T) {
	m := buildTranscript(t, 100)

	if len(m.msgLineOffsets) != len(m.chatModel.Messages)+1 {
		t.Fatalf("offsets has %d entries, want %d", len(m.msgLineOffsets), len(m.chatModel.Messages)+1)
	}
	// Offsets are monotonic and end at the total line count.
	for i := 1; i < len(m.msgLineOffsets); i++ {
		if m.msgLineOffsets[i] < m.msgLineOffsets[i-1] {
			t.Fatalf("offsets go backwards at %d: %v", i, m.msgLineOffsets)
		}
	}

	// Jump to turn 4 (messages 0,2,4,6,8 are user turns).
	target := 6
	m.jumpToMessage(target)

	// The scroll must actually move, and the chosen message must be at or near
	// the top of the viewport rather than merely somewhere on screen.
	available := m.messageViewportHeight()
	if m.chatModel.Scroll <= 0 {
		t.Fatalf("jump left Scroll at %d; the target turn is not at the top", m.chatModel.Scroll)
	}
	_, startLine, _ := clipMessagesToViewport(m.chatModel.RenderMessages(false), available, m.chatModel.Scroll)
	if startLine != m.msgLineOffsets[target] {
		t.Errorf("viewport starts at line %d, want the turn's line %d", startLine, m.msgLineOffsets[target])
	}
}

func TestTimelineJump_FirstTurnScrollsToTop(t *testing.T) {
	// Scroll counts from the bottom, so the first turn sits at the largest real
	// offset. Assert the viewport, not the number: MaxScroll deliberately allows
	// a few rows of over-scroll past the top, and clipMessagesToViewport clamps
	// it, so "top of the transcript is visible" is the property that matters.
	m := buildTranscript(t, 100)
	m.jumpToMessage(0)
	available := m.messageViewportHeight()
	_, startLine, _ := clipMessagesToViewport(m.chatModel.RenderMessages(false), available, m.chatModel.Scroll)
	if startLine != 0 {
		t.Errorf("viewport starts at line %d, want the top (0)", startLine)
	}
	if m.chatModel.Scroll <= 0 {
		t.Errorf("Scroll = %d; the transcript is taller than the viewport, so a jump must scroll", m.chatModel.Scroll)
	}
}

func TestTimelineJump_ClampsToMaxScroll(t *testing.T) {
	// The last turn is the bottom of the transcript, so the requested offset is
	// negative and must clamp to 0 rather than scrolling past the end.
	m := buildTranscript(t, 100)
	last := len(m.chatModel.Messages) - 1
	m.jumpToMessage(last)
	if m.chatModel.Scroll != 0 {
		t.Errorf("jumping to the last turn left Scroll at %d, want 0 (the bottom)", m.chatModel.Scroll)
	}
	if max := m.chatModel.MaxScroll(m.messageViewportHeight()); m.chatModel.Scroll > max {
		t.Errorf("Scroll %d exceeds MaxScroll %d", m.chatModel.Scroll, max)
	}
}

func TestTimelineJump_IgnoresOutOfRangeIndex(t *testing.T) {
	m := buildTranscript(t, 100)
	before := m.chatModel.Scroll
	m.jumpToMessage(-1)
	m.jumpToMessage(len(m.chatModel.Messages) + 5)
	if m.chatModel.Scroll != before {
		t.Errorf("an out-of-range index moved Scroll from %d to %d", before, m.chatModel.Scroll)
	}
}

func TestTimelinePickerEnterJumps(t *testing.T) {
	m := buildTranscript(t, 100)

	m.handleTimelineCommand()
	if m.searchPopup == nil {
		t.Fatal("/timeline did not open the picker")
	}
	// Pick the third turn and accept.
	m.searchPopup.selected = 2
	wantIdx := m.searchPopup.filtered[2].MessageIdx
	if !m.handleSearchPopupKey(tea.Key{Code: tea.KeyEnter}) {
		t.Fatal("Enter was not handled by the timeline picker")
	}
	if m.searchPopup != nil {
		t.Error("Enter left the picker open")
	}
	available := m.messageViewportHeight()
	_, startLine, _ := clipMessagesToViewport(m.chatModel.RenderMessages(false), available, m.chatModel.Scroll)
	if startLine != m.msgLineOffsets[wantIdx] {
		t.Errorf("Enter scrolled to line %d, want message %d's line %d",
			startLine, wantIdx, m.msgLineOffsets[wantIdx])
	}
}

// TestLineOffsets_MapEveryMessageSurvivesCollapse covers the case the offsets
// exist for: renderMessages collapses runs of blank lines, and an offset
// computed before the collapse must still name the right message afterwards.
func TestLineOffsets_MapEveryMessageSurvivesCollapse(t *testing.T) {
	c := &ChatModel{
		Width: 80,
		Messages: []message{
			{role: "user", content: "one"},
			{role: "assistant", content: "two"},
			{role: "user", content: "three"},
			{role: "assistant", content: "four"},
		},
	}
	text, _, offsets := c.renderMessagesWithOffsets(false)
	lines := strings.Split(text, "\n")

	if len(offsets) != len(c.Messages)+1 {
		t.Fatalf("offsets has %d entries, want %d", len(offsets), len(c.Messages)+1)
	}
	if got, want := offsets[len(offsets)-1], len(lines); got != want {
		t.Errorf("final offset = %d, want the line count %d", got, want)
	}
	for i := 0; i < len(c.Messages); i++ {
		if offsets[i] >= len(lines) {
			t.Fatalf("message %d offset %d is past the end (%d lines)", i, offsets[i], len(lines))
		}
		if offsets[i] > offsets[i+1] {
			t.Errorf("offset %d (%d) is after offset %d (%d)", i, offsets[i], i+1, offsets[i+1])
		}
	}
	// The first message must start at the top.
	if offsets[0] != 0 {
		t.Errorf("first message starts at line %d, want 0", offsets[0])
	}
}
