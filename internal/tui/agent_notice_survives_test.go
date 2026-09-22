package tui

import (
	"strings"
	"testing"
)

// A system notice shares role "assistant" with the reply. Appending it without
// closing the block makes the next streamed text delta absorb it — the notice is
// overwritten and never rendered, which is what the LSP hook's warnings did on
// every .rs write. The repaint the notice triggers already happens; this is what
// made it paint nothing.
func TestSystemNotice_SurvivesNextTextDelta(t *testing.T) {
	noticeCh := make(chan string, 1)
	m := &model{
		chatModel: ChatModel{Messages: []message{{role: "user", content: "fix main.rs"}}},
		cfg:       Config{SystemNoticeCh: noticeCh},
	}

	// The model is mid-reply when the tool call runs and the hook raises a notice.
	m.handleAgentText(agentTextMsg{text: "I will fix "})
	noticeCh <- "lsp hook: format /tmp/p/src/main.rs: file not found"
	if _, _, handled := m.updateAgentStream(waitForSystemNotice(noticeCh)()); !handled {
		t.Fatal("updateAgentStream did not handle systemNoticeMsg")
	}

	// The model then streams the rest of the reply.
	m.handleAgentText(agentTextMsg{text: "the borrow error."})

	msgs := m.chatModel.Messages
	var sawNotice bool
	for _, mm := range msgs {
		if strings.Contains(mm.content, "file not found") {
			sawNotice = true
			if !mm.closed() {
				t.Errorf("notice is not a closed block; the next delta will absorb it: %+v", mm)
			}
		}
	}
	if !sawNotice {
		t.Fatalf("notice was absorbed by the next text delta; messages: %+v", msgs)
	}

	// The reply must resume as its own block, carrying the full sentence and
	// none of the notice's text.
	last := msgs[len(msgs)-1]
	if last.content != "the borrow error." {
		t.Errorf("reply block = %q, want %q", last.content, "the borrow error.")
	}
	if strings.Contains(last.content, "lsp hook") {
		t.Errorf("notice text leaked into the reply block: %q", last.content)
	}
}

// The accumulator holds the block that just closed. Left in place, the next
// delta appends to it and replays the closed block inside the new one.
func TestAppendNotice_ClosesAndResetsAccumulator(t *testing.T) {
	cm := ChatModel{Messages: make([]message, 0), Streaming: "leftover"}
	cm.AppendNotice("compaction ran")
	if cm.Streaming != "" {
		t.Errorf("Streaming = %q, want empty", cm.Streaming)
	}
	if len(cm.Messages) != 1 || !cm.Messages[0].closed() {
		t.Errorf("notice message = %+v, want one closed block", cm.Messages)
	}
}

// A notice is not a warning: benign notices ("compaction ran", "update
// available") must keep the plain reply styling rather than gaining a ⚠ prefix,
// and must not be rendered in the warning color.
func TestNotice_RendersAsPlainReplyNotWarning(t *testing.T) {
	cm := ChatModel{Width: 80, Messages: make([]message, 0)}
	cm.AppendNotice("compaction ran")

	got := cm.RenderMessages(false)
	if strings.Contains(got, "⚠") {
		t.Errorf("notice rendered with a warning bullet:\n%s", got)
	}
	if strings.Contains(got, "Σ") {
		t.Errorf("notice rendered as a meta note:\n%s", got)
	}
	if !strings.Contains(got, "compaction ran") {
		t.Errorf("notice text missing from the render:\n%s", got)
	}
}
