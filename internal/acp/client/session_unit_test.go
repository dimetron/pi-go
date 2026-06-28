package client

import (
	"io"
	"os/exec"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	shared "github.com/dimetron/pi-go/internal/acp"
)

// newTestSession returns a minimal RunningSession backed by a no-op command and
// a pipe for stderr. The background streamStderr goroutine is cleaned up via
// t.Cleanup so no goroutines leak between tests.
func newTestSession(t *testing.T) *RunningSession {
	t.Helper()

	cmd := exec.Command("true")
	pr, pw := io.Pipe()
	t.Cleanup(func() {
		_ = pr.Close()
		_ = pw.Close()
	})

	session := newRunningSession(cmd, pw, pr)
	t.Cleanup(func() {
		_ = pw.Close()
		select {
		case <-session.stderrDone:
		case <-time.After(time.Second):
		}
	})
	return session
}

func TestSessionDoneChannel(t *testing.T) {
	s := newTestSession(t)
	if s.Done() == nil {
		t.Fatal("Done() returned nil channel")
	}
	select {
	case <-s.Done():
		t.Fatal("Done() channel closed before the turn finished")
	default:
	}
}

func TestRunnerClientInfoUsesConfigured(t *testing.T) {
	custom := acp.Implementation{Name: "custom-client", Version: "9.9"}
	r := Runner{ClientInfo: custom}
	if got := r.clientInfo(); got.Name != custom.Name || got.Version != custom.Version {
		t.Errorf("clientInfo() = %+v, want %+v", got, custom)
	}

	// Empty name falls back to the pi-go default.
	def := Runner{}.clientInfo()
	if def.Name != "pi-go" {
		t.Errorf("default clientInfo name = %q, want %q", def.Name, "pi-go")
	}
}

func TestContentBlockText(t *testing.T) {
	tests := []struct {
		name  string
		block acp.ContentBlock
		want  string
	}{
		{name: "text block", block: acp.TextBlock("hello world"), want: "hello world"},
		{name: "resource link returns uri", block: acp.ResourceLinkBlock("doc", "file:///tmp/doc.md"), want: "file:///tmp/doc.md"},
		{name: "empty block returns empty", block: acp.ContentBlock{}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := contentBlockText(tt.block); got != tt.want {
				t.Errorf("contentBlockText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHandleUpdateAgentMessageChunk(t *testing.T) {
	s := newTestSession(t)

	s.handleUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("sess-1"),
		Update: acp.SessionUpdate{
			AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
				Content: acp.TextBlock("part one "),
			},
		},
	})
	s.handleUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("sess-1"),
		Update: acp.SessionUpdate{
			AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
				Content: acp.TextBlock("part two"),
			},
		},
	})

	s.mu.Lock()
	gotResult := s.result.Result
	gotSession := s.result.SessionID
	s.mu.Unlock()

	if gotResult != "part one part two" {
		t.Errorf("accumulated result = %q, want %q", gotResult, "part one part two")
	}
	if gotSession != "sess-1" {
		t.Errorf("result session id = %q, want %q", gotSession, "sess-1")
	}
}

func TestHandleUpdateThoughtAndToolEvents(t *testing.T) {
	s := newTestSession(t)

	// Thought chunk emits a progress event without changing the result text.
	s.handleUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("sess-2"),
		Update: acp.SessionUpdate{
			AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
				Content: acp.TextBlock("thinking..."),
			},
		},
	})

	// New tool call.
	s.handleUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("sess-2"),
		Update: acp.SessionUpdate{
			ToolCall: &acp.SessionUpdateToolCall{
				ToolCallId: "call-1",
				Title:      "Read file",
			},
		},
	})

	// Tool call update with a new title.
	newTitle := "Read file (done)"
	s.handleUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("sess-2"),
		Update: acp.SessionUpdate{
			ToolCallUpdate: &acp.SessionToolCallUpdate{
				ToolCallId: "call-1",
				Title:      &newTitle,
			},
		},
	})

	s.mu.Lock()
	gotResult := s.result.Result
	s.mu.Unlock()
	if gotResult != "" {
		t.Errorf("result should be empty after thought/tool updates, got %q", gotResult)
	}
}

func TestFinishIdempotentMergesResult(t *testing.T) {
	s := newTestSession(t)

	// First finish records an error result and marks the session finished.
	s.finish(shared.RunResult{Status: shared.StatusError, Error: "boom"})

	s.mu.Lock()
	if !s.finished {
		s.mu.Unlock()
		t.Fatal("session should be finished after first finish()")
	}
	s.mu.Unlock()

	// Second finish on an already-finished session should merge a non-empty
	// success result and a session ID without overwriting the error status.
	s.finish(shared.RunResult{Status: shared.StatusSuccess, Result: "late text", SessionID: "sess-late"})

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.result.Status != shared.StatusError {
		t.Errorf("status = %q, want %q (must not be overwritten)", s.result.Status, shared.StatusError)
	}
	if s.result.Result != "late text" {
		t.Errorf("result = %q, want merged %q", s.result.Result, "late text")
	}
	if s.result.SessionID != "sess-late" {
		t.Errorf("session id = %q, want merged %q", s.result.SessionID, "sess-late")
	}
}
