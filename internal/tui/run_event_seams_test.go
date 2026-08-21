package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/subagent"
)

func TestToolMayTickChecklist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tool string
		want bool
	}{
		// Only the tools that write files can have ticked a checkbox.
		{name: "write", tool: "write", want: true},
		{name: "edit", tool: "edit", want: true},
		{name: "read does not", tool: "read", want: false},
		{name: "bash does not", tool: "bash", want: false},
		{name: "grep does not", tool: "grep", want: false},
		{name: "no active tool", tool: "", want: false},
		// The check is exact, not a prefix or substring match.
		{name: "write_file is a different tool", tool: "write_file", want: false},
		{name: "case is significant", tool: "Write", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := toolMayTickChecklist(tt.tool); got != tt.want {
				t.Errorf("toolMayTickChecklist(%q) = %v, want %v", tt.tool, got, tt.want)
			}
		})
	}
}

func TestLastMessageMatching(t *testing.T) {
	t.Parallel()

	msgs := []message{
		{role: "assistant", content: "first"},
		{role: "tool", tool: "bash", content: "done"},
		{role: "assistant", content: "second"},
		{role: "tool", tool: "read", content: ""},
	}

	tests := []struct {
		name string
		pred func(*message) bool
		want string // matched message's content; "\x00" means no match expected
	}{
		{
			// The newest match wins: an older card has already been closed
			// out and must not be rewritten by a later event.
			name: "picks the newest assistant, not the oldest",
			pred: func(m *message) bool { return m.role == "assistant" },
			want: "second",
		},
		{
			name: "an open tool card is found by its empty content",
			pred: func(m *message) bool { return m.role == "tool" && m.content == "" },
			want: "",
		},
		{
			name: "no match returns nil",
			pred: func(m *message) bool { return m.role == "system" },
			want: "\x00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := lastMessageMatching(msgs, tt.pred)
			if tt.want == "\x00" {
				if got != nil {
					t.Errorf("lastMessageMatching() = %+v, want nil", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("lastMessageMatching() = nil, want the message with content %q", tt.want)
			}
			if got.content != tt.want {
				t.Errorf("matched content = %q, want %q", got.content, tt.want)
			}
		})
	}
}

// The returned pointer has to alias the slice — the streaming handlers update
// the transcript through it.
func TestLastMessageMatchingAliasesTheSlice(t *testing.T) {
	t.Parallel()

	msgs := []message{{role: "assistant", content: "before"}}
	got := lastMessageMatching(msgs, func(m *message) bool { return m.role == "assistant" })
	if got == nil {
		t.Fatal("expected a match")
	}
	got.content = "after"

	if msgs[0].content != "after" {
		t.Errorf("writing through the pointer did not reach the slice: %q", msgs[0].content)
	}
}

func TestLastMessageMatchingEmpty(t *testing.T) {
	t.Parallel()

	if got := lastMessageMatching(nil, func(*message) bool { return true }); got != nil {
		t.Errorf("lastMessageMatching(nil) = %+v, want nil", *got)
	}
}

// A checklist refresh is a disk read on a live worktree, so it is driven here
// through a real worktree rather than a nil orchestrator. This is the branch
// that had no coverage before: that a write refreshes the plan view, and that
// a read leaves it alone.
func TestApplyRunToolResultRefreshesChecklistAfterWrites(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		wantDone bool
	}{
		{name: "write refreshes", tool: "write", wantDone: true},
		{name: "edit refreshes", tool: "edit", wantDone: true},
		{name: "read leaves the stale view alone", tool: "read", wantDone: false},
		{name: "bash leaves the stale view alone", tool: "bash", wantDone: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := initRunTestRepo(t)
			orch := subagent.NewOrchestrator(&config.Config{}, repo, nil)
			t.Cleanup(orch.Shutdown)

			const agentID = "task-checklist-1"
			wtPath, err := orch.Worktree().Create(agentID)
			if err != nil {
				t.Fatalf("creating worktree: %v", err)
			}

			// The agent has ticked the box on disk; the model still holds the
			// pre-write view.
			writePlanMD(t, wtPath, "my-spec", "- [x] Step 1: parse the config\n")

			m := &model{
				width: 120,
				cfg:   Config{Orchestrator: orch},
				chatModel: ChatModel{Messages: []message{
					{role: "tool", tool: tt.tool, content: ""},
				}},
				statusModel: StatusModel{ActiveTool: tt.tool},
				run: &runState{
					specName:        "my-spec",
					agentID:         agentID,
					worktreeAgentID: agentID,
					checklist:       []ChecklistStep{{Title: "Step 1: parse the config", Done: false}},
				},
			}

			m.applyRunToolResult(subagent.Event{Type: "tool_result", Content: "wrote 1 line"})

			if len(m.run.checklist) != 1 {
				t.Fatalf("checklist length = %d, want 1", len(m.run.checklist))
			}
			if got := m.run.checklist[0].Done; got != tt.wantDone {
				t.Errorf("after a %q result, checklist[0].Done = %v, want %v", tt.tool, got, tt.wantDone)
			}
		})
	}
}

// writePlanMD lays down specs/<spec>/plan.md inside a worktree.
func writePlanMD(t *testing.T, dir, specName, body string) {
	t.Helper()
	specDir := filepath.Join(dir, "specs", specName)
	if err := os.MkdirAll(specDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specDir, "plan.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRecordRunStreamingTrace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		existing    []traceEntry
		streaming   string
		delta       string
		wantEntries int
		wantDetail  string
	}{
		{
			// Nothing open: the delta opens a new entry and is its own detail.
			name:        "opens the first entry",
			streaming:   "hello",
			delta:       "hello",
			wantEntries: 1,
			wantDetail:  "hello",
		},
		{
			// An open llm entry absorbs the accumulated text, not the delta.
			name:        "extends an open llm entry with the accumulation",
			existing:    []traceEntry{{kind: "llm", detail: "hello"}},
			streaming:   "hello world",
			delta:       " world",
			wantEntries: 1,
			wantDetail:  "hello world",
		},
		{
			// A tool call closed the turn, so the next delta starts a fresh
			// entry carrying only what arrived after the interruption.
			name:        "starts a new entry after a tool call",
			existing:    []traceEntry{{kind: "llm", detail: "hello"}, {kind: "tool_call", summary: ">>> read"}},
			streaming:   "hello resumed",
			delta:       " resumed",
			wantEntries: 3,
			wantDetail:  " resumed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := &model{chatModel: ChatModel{TraceLog: tt.existing, Streaming: tt.streaming}}
			m.recordRunStreamingTrace(tt.delta)

			if got := len(m.chatModel.TraceLog); got != tt.wantEntries {
				t.Fatalf("trace has %d entries, want %d", got, tt.wantEntries)
			}
			last := m.chatModel.TraceLog[len(m.chatModel.TraceLog)-1]
			if last.kind != "llm" {
				t.Errorf("last entry kind = %q, want %q", last.kind, "llm")
			}
			if last.detail != tt.wantDetail {
				t.Errorf("last entry detail = %q, want %q", last.detail, tt.wantDetail)
			}
		})
	}
}

func TestFeedMatrixFromRunEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		event      subagent.Event
		wantActive bool
	}{
		{name: "content drives the rain", event: subagent.Event{Type: "text_delta", Content: "tokens"}, wantActive: true},
		// A content-free event still counts: a quiet stream must not look
		// like a stalled one.
		{name: "type stands in for missing content", event: subagent.Event{Type: "message_end"}, wantActive: true},
		{name: "an empty event feeds nothing", event: subagent.Event{}, wantActive: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := &model{width: 120}
			m.feedMatrixFromRunEvent(tt.event)

			if got := m.matrix.active; got != tt.wantActive {
				t.Errorf("matrix active = %v, want %v", got, tt.wantActive)
			}
			if tt.wantActive && m.matrix.seed == 0 {
				t.Error("matrix seed was not advanced by the feed")
			}
		})
	}
}
