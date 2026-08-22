package codex

import (
	"strings"
	"testing"
)

// This file pins the behavior that (*Session).handleItem encoded as one large
// switch before it was split into emitMessageItem, emitReasoningItem and
// emitToolItem. Every row is a branch the original switch had, so a change in
// which event a given item shape produces shows up here.

// itemSession returns a Session whose emit() lands in a buffered channel, plus
// a drain that reports the events one handleItem call produced. emit is a
// non-blocking send, so the buffer has to be larger than any single call needs.
func itemSession() (*Session, func() []Event) {
	s := &Session{threadID: "thread-1", events: make(chan Event, 8)}
	return s, func() []Event {
		var got []Event
		for {
			select {
			case ev := <-s.events:
				got = append(got, ev)
			default:
				return got
			}
		}
	}
}

func exitCode(n int) *int { return &n }

func TestHandleItemEmitsPerItemShape(t *testing.T) {
	tests := []struct {
		name      string
		item      Item
		completed bool
		wantEvent []Event // the full sequence, in order
		wantText  string  // what was accumulated into the run result
	}{
		// agentMessage: emitted on both started and completed, accumulated only
		// on completed — counting both would duplicate the answer.
		{
			name:      "agent message started emits but does not accumulate",
			item:      Item{Type: ItemAgentMessage, Text: "hello"},
			completed: false,
			wantEvent: []Event{{Type: EventTypeMessage, Content: "hello", SessionID: "thread-1"}},
			wantText:  "",
		},
		{
			name:      "agent message completed emits and accumulates",
			item:      Item{Type: ItemAgentMessage, Text: "hello"},
			completed: true,
			wantEvent: []Event{{Type: EventTypeMessage, Content: "hello", SessionID: "thread-1"}},
			wantText:  "hello",
		},
		{
			name:      "blank agent message is dropped entirely",
			item:      Item{Type: ItemAgentMessage, Text: "   \n\t "},
			completed: true,
			wantEvent: nil,
			wantText:  "",
		},
		{
			name:      "agent message keeps its surrounding whitespace when emitted",
			item:      Item{Type: ItemAgentMessage, Text: "  padded  "},
			completed: true,
			wantEvent: []Event{{Type: EventTypeMessage, Content: "  padded  ", SessionID: "thread-1"}},
			wantText:  "  padded  ",
		},

		// exitedReviewMode: the same shape, reading Review rather than Text.
		{
			name:      "review text is emitted and accumulated on completion",
			item:      Item{Type: ItemExitedReviewMode, Review: "looks fine"},
			completed: true,
			wantEvent: []Event{{Type: EventTypeMessage, Content: "looks fine", SessionID: "thread-1"}},
			wantText:  "looks fine",
		},
		{
			name:      "review text started does not accumulate",
			item:      Item{Type: ItemExitedReviewMode, Review: "looks fine"},
			completed: false,
			wantEvent: []Event{{Type: EventTypeMessage, Content: "looks fine", SessionID: "thread-1"}},
			wantText:  "",
		},
		{
			name:      "blank review is dropped",
			item:      Item{Type: ItemExitedReviewMode, Review: " "},
			completed: true,
			wantEvent: nil,
			wantText:  "",
		},
		{
			// exitedReviewMode reads Review, never Text — a stray Text field
			// must not leak into the transcript.
			name:      "review ignores the Text field",
			item:      Item{Type: ItemExitedReviewMode, Text: "not this", Review: "this"},
			completed: true,
			wantEvent: []Event{{Type: EventTypeMessage, Content: "this", SessionID: "thread-1"}},
			wantText:  "this",
		},

		// reasoning: progress, completed only, and never accumulated.
		{
			name:      "reasoning summary is joined and emitted as progress",
			item:      Item{Type: ItemReasoning, Summary: []string{"first", "second"}},
			completed: true,
			wantEvent: []Event{{Type: EventTypeProgress, Content: "first\nsecond", SessionID: "thread-1"}},
			wantText:  "",
		},
		{
			name:      "reasoning started emits nothing",
			item:      Item{Type: ItemReasoning, Summary: []string{"first"}},
			completed: false,
			wantEvent: nil,
			wantText:  "",
		},
		{
			name:      "all-blank reasoning summary is dropped",
			item:      Item{Type: ItemReasoning, Summary: []string{"", "  ", "\n"}},
			completed: true,
			wantEvent: nil,
			wantText:  "",
		},
		{
			name:      "empty reasoning summary is dropped",
			item:      Item{Type: ItemReasoning},
			completed: true,
			wantEvent: nil,
			wantText:  "",
		},

		// commandExecution: three distinct completed phrasings.
		{
			name:      "command started names the command",
			item:      Item{Type: ItemCommandExecution, Command: "go test ./..."},
			completed: false,
			wantEvent: []Event{{Type: EventTypeTool, Content: "Running: go test ./...", SessionID: "thread-1"}},
		},
		{
			name:      "command completed with an exit code reports it",
			item:      Item{Type: ItemCommandExecution, Command: "go test", ExitCode: exitCode(2)},
			completed: true,
			wantEvent: []Event{{Type: EventTypeTool, Content: "Command completed (exit 2)", SessionID: "thread-1"}},
		},
		{
			name:      "command completed with exit zero still reports the code",
			item:      Item{Type: ItemCommandExecution, Command: "go test", ExitCode: exitCode(0)},
			completed: true,
			wantEvent: []Event{{Type: EventTypeTool, Content: "Command completed (exit 0)", SessionID: "thread-1"}},
		},
		{
			name:      "command completed without an exit code omits it",
			item:      Item{Type: ItemCommandExecution, Command: "go test"},
			completed: true,
			wantEvent: []Event{{Type: EventTypeTool, Content: "Command completed", SessionID: "thread-1"}},
		},

		// fileChange: the started line counts the changes.
		{
			name:      "file change started counts the paths",
			item:      Item{Type: ItemFileChange, Changes: []FileChange{{Path: "a.go"}, {Path: "b.go"}}},
			completed: false,
			wantEvent: []Event{{Type: EventTypeTool, Content: "Applying 2 file changes", SessionID: "thread-1"}},
		},
		{
			name:      "file change started with no paths still reports a count",
			item:      Item{Type: ItemFileChange},
			completed: false,
			wantEvent: []Event{{Type: EventTypeTool, Content: "Applying 0 file changes", SessionID: "thread-1"}},
		},
		{
			name:      "file change completed drops the count",
			item:      Item{Type: ItemFileChange, Changes: []FileChange{{Path: "a.go"}}},
			completed: true,
			wantEvent: []Event{{Type: EventTypeTool, Content: "File changes completed", SessionID: "thread-1"}},
		},

		// mcpToolCall / dynamicToolCall share one case: the server prefix is
		// added only when the item carries one.
		{
			name:      "mcp tool call started is prefixed by its server",
			item:      Item{Type: ItemMCPToolCall, Server: "fs", Tool: "read"},
			completed: false,
			wantEvent: []Event{{Type: EventTypeTool, Content: "Calling fs/read", SessionID: "thread-1"}},
		},
		{
			name:      "mcp tool call completed is prefixed by its server",
			item:      Item{Type: ItemMCPToolCall, Server: "fs", Tool: "read"},
			completed: true,
			wantEvent: []Event{{Type: EventTypeTool, Content: "Called fs/read", SessionID: "thread-1"}},
		},
		{
			name:      "mcp tool call without a server is unprefixed",
			item:      Item{Type: ItemMCPToolCall, Tool: "read"},
			completed: true,
			wantEvent: []Event{{Type: EventTypeTool, Content: "Called read", SessionID: "thread-1"}},
		},
		{
			name:      "dynamic tool call takes the same path",
			item:      Item{Type: ItemDynamicToolCall, Tool: "shell"},
			completed: false,
			wantEvent: []Event{{Type: EventTypeTool, Content: "Calling shell", SessionID: "thread-1"}},
		},
		{
			name:      "dynamic tool call honors a server prefix too",
			item:      Item{Type: ItemDynamicToolCall, Server: "local", Tool: "shell"},
			completed: true,
			wantEvent: []Event{{Type: EventTypeTool, Content: "Called local/shell", SessionID: "thread-1"}},
		},

		// webSearch is the one tool item with no completion line at all.
		{
			name:      "web search started names the query",
			item:      Item{Type: ItemWebSearch, Query: "golang cyclomatic"},
			completed: false,
			wantEvent: []Event{{Type: EventTypeTool, Content: "Web search: golang cyclomatic", SessionID: "thread-1"}},
		},
		{
			name:      "web search completed emits nothing",
			item:      Item{Type: ItemWebSearch, Query: "golang cyclomatic"},
			completed: true,
			wantEvent: nil,
		},

		// An item type pi-go does not render must stay silent, not fall through
		// into the tool phrasing.
		{
			name:      "unknown item type is ignored",
			item:      Item{Type: "somethingCodexAddedLater", Text: "x", Command: "y"},
			completed: true,
			wantEvent: nil,
		},
		{
			name:      "empty item type is ignored",
			item:      Item{},
			completed: false,
			wantEvent: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, drain := itemSession()
			var text strings.Builder

			s.handleItem(tt.item, tt.completed, &text)

			got := drain()
			if len(got) != len(tt.wantEvent) {
				t.Fatalf("emitted %d events %+v, want %d %+v", len(got), got, len(tt.wantEvent), tt.wantEvent)
			}
			for i := range got {
				if got[i] != tt.wantEvent[i] {
					t.Errorf("event %d = %+v, want %+v", i, got[i], tt.wantEvent[i])
				}
			}
			if text.String() != tt.wantText {
				t.Errorf("accumulated text = %q, want %q", text.String(), tt.wantText)
			}
		})
	}
}

// The session id rides on every event handleItem emits; a helper that dropped
// it would leave the caller unable to attribute the event.
func TestHandleItemStampsTheThreadID(t *testing.T) {
	s := &Session{threadID: "thread-xyz", events: make(chan Event, 4)}
	var text strings.Builder

	s.handleItem(Item{Type: ItemAgentMessage, Text: "a"}, true, &text)
	s.handleItem(Item{Type: ItemReasoning, Summary: []string{"b"}}, true, &text)
	s.handleItem(Item{Type: ItemWebSearch, Query: "c"}, false, &text)
	close(s.events)

	n := 0
	for ev := range s.events {
		n++
		if ev.SessionID != "thread-xyz" {
			t.Errorf("event %+v carries SessionID %q, want thread-xyz", ev, ev.SessionID)
		}
	}
	if n != 3 {
		t.Errorf("got %d events, want 3", n)
	}
}

// Accumulation is additive across the items of one turn: the builder handleItem
// is handed is the turn's answer, not a per-item scratch buffer.
func TestHandleItemAccumulatesAcrossItems(t *testing.T) {
	s, drain := itemSession()
	var text strings.Builder

	s.handleItem(Item{Type: ItemAgentMessage, Text: "part one. "}, true, &text)
	// Not completed, so this one is shown but not counted.
	s.handleItem(Item{Type: ItemAgentMessage, Text: "draft"}, false, &text)
	s.handleItem(Item{Type: ItemExitedReviewMode, Review: "part two."}, true, &text)
	// Tool and reasoning items never contribute to the answer.
	s.handleItem(Item{Type: ItemCommandExecution, Command: "ls"}, true, &text)
	s.handleItem(Item{Type: ItemReasoning, Summary: []string{"thinking"}}, true, &text)

	if got, want := text.String(), "part one. part two."; got != want {
		t.Errorf("accumulated text = %q, want %q", got, want)
	}
	if got := len(drain()); got != 5 {
		t.Errorf("emitted %d events, want 5", got)
	}
}
