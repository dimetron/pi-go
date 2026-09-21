package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/subagent"
)

func TestWaitForSystemNotice_NilChannelReturnsNoCmd(t *testing.T) {
	if cmd := waitForSystemNotice(nil); cmd != nil {
		t.Error("waitForSystemNotice(nil) returned a command")
	}
}

func TestWaitForSystemNotice_DeliversNotice(t *testing.T) {
	ch := make(chan string, 1)
	ch <- "compaction ran"

	msg := waitForSystemNotice(ch)()

	got, ok := msg.(systemNoticeMsg)
	if !ok {
		t.Fatalf("got %T, want systemNoticeMsg", msg)
	}
	if got.text != "compaction ran" {
		t.Errorf("text = %q, want %q", got.text, "compaction ran")
	}
}

func TestWaitForSystemNotice_ClosedChannelYieldsNil(t *testing.T) {
	ch := make(chan string)
	close(ch)

	// A closed notice channel must not re-arm with a bogus message; nil ends
	// the wait loop.
	if msg := waitForSystemNotice(ch)(); msg != nil {
		t.Errorf("closed channel yielded %#v, want nil", msg)
	}
}

// TestSystemNoticeMsg_LandsInChatTranscript closes the loop on the notice path:
// waitForSystemNotice delivers the message, and updateAgentStream must render it
// into the chat transcript. Together with the LSP hook's guard, this is what
// makes a warning like "language server not installed" visible *in the layout*
// rather than as a raw write over the alternate screen.
func TestSystemNoticeMsg_LandsInChatTranscript(t *testing.T) {
	noticeCh := make(chan string, 1)
	m := &model{
		chatModel: ChatModel{Messages: []message{{role: "user", content: "hi"}}},
		cfg:       Config{SystemNoticeCh: noticeCh},
	}
	noticeCh <- "pi-go: lsp hook: typescript-language-server not found in PATH"

	// Deliver exactly as the TUI does: waitForSystemNotice -> updateAgentStream.
	msg := waitForSystemNotice(noticeCh)()
	if msg == nil {
		t.Fatal("waitForSystemNotice returned nil for a queued notice")
	}
	_, _, handled := m.updateAgentStream(msg)
	if !handled {
		t.Fatal("updateAgentStream did not handle systemNoticeMsg")
	}

	if len(m.chatModel.Messages) != 2 {
		t.Fatalf("chat transcript has %d messages, want 2 (user + notice)",
			len(m.chatModel.Messages))
	}
	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if last.role != "assistant" {
		t.Errorf("notice role = %q, want assistant", last.role)
	}
	if !strings.Contains(last.content, "typescript-language-server not found in PATH") {
		t.Errorf("notice did not reach the transcript: %q", last.content)
	}
}

func TestWaitForSystemNotice_IsATeaCmd(t *testing.T) {
	ch := make(chan string, 1)
	ch <- "x"
	cmd := waitForSystemNotice(ch)
	if cmd == nil {
		t.Fatal("waitForSystemNotice did not return a tea.Cmd")
	}
}

func TestBashEventKind(t *testing.T) {
	tests := []struct {
		kind string
		want string
	}{
		{"output", bashEventPrefix + "output"},
		{"stderr", bashEventPrefix + "stderr"},
		{"exit", bashEventPrefix + "exit"},
		{"", bashEventPrefix},
	}
	for _, tc := range tests {
		if got := BashEventKind(tc.kind); got != tc.want {
			t.Errorf("BashEventKind(%q) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestBashEventKind_IsDistinguishableFromSubagentEvents(t *testing.T) {
	// The whole point of the namespace is that a bash event sharing the
	// subagent channel is never mistaken for subagent activity.
	if strings.HasPrefix("tool_call", bashEventPrefix) {
		t.Fatal("a subagent kind collides with the bash prefix")
	}
	if !strings.HasPrefix(BashEventKind("output"), bashEventPrefix) {
		t.Error("BashEventKind did not apply the prefix")
	}
	if got := strings.TrimPrefix(BashEventKind("output"), bashEventPrefix); got != "output" {
		t.Errorf("round trip gave %q, want %q", got, "output")
	}
}

// --- sidebar agent section ------------------------------------------------

func agentStatus(id, status string, started time.Time) subagent.AgentStatus {
	return subagent.AgentStatus{AgentID: id, Status: status, StartedAt: started}
}

func TestSortAgentsForDisplay(t *testing.T) {
	base := time.Unix(1000, 0)
	agents := []subagent.AgentStatus{
		agentStatus("z-killed", "killed", base),
		agentStatus("b-done", "done", base),
		agentStatus("a-running-late", "running", base.Add(time.Minute)),
		agentStatus("c-failed", "failed", base),
		agentStatus("a-running-early", "running", base),
	}

	sortAgentsForDisplay(agents)

	want := []string{"a-running-early", "a-running-late", "b-done", "c-failed", "z-killed"}
	for i, id := range want {
		if agents[i].AgentID != id {
			t.Errorf("position %d = %q, want %q (order: %v)", i, agents[i].AgentID, id, ids(agents))
		}
	}
}

func TestSortAgentsForDisplay_TiesBreakOnID(t *testing.T) {
	base := time.Unix(1000, 0)
	agents := []subagent.AgentStatus{
		agentStatus("bbb", "running", base),
		agentStatus("aaa", "running", base),
	}

	sortAgentsForDisplay(agents)

	if agents[0].AgentID != "aaa" {
		t.Errorf("order = %v, want it broken on ID", ids(agents))
	}
}

func ids(agents []subagent.AgentStatus) []string {
	out := make([]string, len(agents))
	for i, a := range agents {
		out[i] = a.AgentID
	}
	return out
}

func TestAgentHeadingStyle_ReflectsWorstStatus(t *testing.T) {
	st := testSidebarStyles()
	base := time.Unix(1000, 0)

	tests := []struct {
		name   string
		agents []subagent.AgentStatus
		want   string
	}{
		{
			name:   "all finished is green",
			agents: []subagent.AgentStatus{agentStatus("a", "done", base)},
			want:   st.green.Bold(true).Render("x"),
		},
		{
			name:   "any running outranks finished",
			agents: []subagent.AgentStatus{agentStatus("a", "done", base), agentStatus("b", "running", base)},
			want:   st.peach.Bold(true).Render("x"),
		},
		{
			name: "any failure outranks running",
			agents: []subagent.AgentStatus{
				agentStatus("a", "running", base),
				agentStatus("b", "failed", base),
			},
			want: st.red.Bold(true).Render("x"),
		},
		{
			name:   "no agents is green",
			agents: nil,
			want:   st.green.Bold(true).Render("x"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentHeadingStyle(tc.agents, st).Render("x"); got != tc.want {
				t.Errorf("agentHeadingStyle rendered %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSidebarAgentLines_NoOrchestratorRendersNothing(t *testing.T) {
	got := sidebarAgentLines(SidebarRenderInput{}, 20, testSidebarStyles())
	if got != nil {
		t.Errorf("sidebarAgentLines with no orchestrator = %v, want nil", got)
	}
}
