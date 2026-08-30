package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A warning raised mid-turn used to be indistinguishable from an open reply
// block: both carry role "assistant", so the next text delta overwrote the
// notice and inherited its styling. The notice must survive and the resumed
// reply must land in a block of its own.
func TestHandleAgentText_WarningClosesTheBlock(t *testing.T) {
	m := &model{}
	m.chatModel = ChatModel{Messages: make([]message, 0)}

	m.handleAgentText(agentTextMsg{text: "first block."})
	m.handleAgentWarning(agentWarningMsg{text: "Loop detected — resuming (attempt 1 of 2)."})
	m.handleAgentText(agentTextMsg{text: "resumed reply."})

	msgs := m.chatModel.Messages
	if len(msgs) != 3 {
		t.Fatalf("want 3 blocks (reply, warning, reply), got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].content != "first block." || msgs[0].closed() {
		t.Errorf("first block altered: %+v", msgs[0])
	}
	if !msgs[1].isWarning {
		t.Errorf("block 1 should be the warning, got %+v", msgs[1])
	}
	if msgs[1].content != "Loop detected — resuming (attempt 1 of 2)." {
		t.Errorf("warning text was overwritten: %q", msgs[1].content)
	}
	if msgs[2].isWarning {
		t.Error("resumed reply inherited the warning style")
	}
	if msgs[2].content != "resumed reply." {
		t.Errorf("resumed reply replayed the earlier block: %q", msgs[2].content)
	}
}

// Errors and meta notes share the warning's role and the same exposure.
func TestHandleAgentText_ErrorAndMetaCloseTheBlock(t *testing.T) {
	for _, tc := range []struct {
		name   string
		append func(*ChatModel)
	}{
		{"error", func(c *ChatModel) { c.AppendError("Error: boom") }},
		{"meta", func(c *ChatModel) { c.AppendMeta("Σ 100 tokens") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &model{}
			m.chatModel = ChatModel{Messages: make([]message, 0)}
			m.handleAgentText(agentTextMsg{text: "reply."})
			tc.append(&m.chatModel)
			m.handleAgentText(agentTextMsg{text: "more."})

			msgs := m.chatModel.Messages
			if len(msgs) != 3 {
				t.Fatalf("want 3 blocks, got %d: %+v", len(msgs), msgs)
			}
			if msgs[2].closed() || msgs[2].content != "more." {
				t.Errorf("text after a closed block: %+v", msgs[2])
			}
		})
	}
}

// The accumulator holds the block that just closed. Left in place, the next
// delta appends to it and replays the closed block inside the new one.
func TestAppendNotice_ResetsStreamingAccumulator(t *testing.T) {
	for _, tc := range []struct {
		name   string
		append func(*ChatModel)
	}{
		{"warning", func(c *ChatModel) { c.AppendWarning("careful") }},
		{"error", func(c *ChatModel) { c.AppendError("boom") }},
		{"meta", func(c *ChatModel) { c.AppendMeta("Σ 1") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cm := ChatModel{Messages: make([]message, 0), Streaming: "leftover"}
			tc.append(&cm)
			if cm.Streaming != "" {
				t.Errorf("Streaming = %q, want empty", cm.Streaming)
			}
		})
	}
}

// An unwrapped notice runs past the right edge, where the terminal clips the
// line and takes its SGR reset with it, leaving the styling open for every line
// drawn afterwards. Wrapping keeps each line — and its reset — inside the pane.
func TestNoticeBody_WrapsAndClosesEveryLine(t *testing.T) {
	cm := ChatModel{Width: 40}
	long := strings.Repeat("warning text that keeps going ", 6)

	for _, tc := range []struct {
		name string
		out  string
	}{
		{"warning", cm.assistantWarningBody(long, darkPalette)},
		{"error", cm.assistantErrorBody(long, darkPalette)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := strings.Split(tc.out, "\n")
			if len(lines) < 2 {
				t.Fatalf("notice was not wrapped: %q", tc.out)
			}
			for i, ln := range lines {
				if w := ansi.StringWidth(ln); w > cm.Width {
					t.Errorf("line %d is %d cols wide, pane is %d: %q", i, w, cm.Width, ln)
				}
				// lipgloss closes with the implicit form, \x1b[m; accept both.
				if strings.Contains(ln, "\x1b[") &&
					!strings.HasSuffix(ln, "\x1b[m") && !strings.HasSuffix(ln, "\x1b[0m") {
					t.Errorf("line %d leaves its styling open: %q", i, ln)
				}
			}
		})
	}
}
