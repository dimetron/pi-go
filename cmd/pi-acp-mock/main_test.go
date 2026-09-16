package main

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

func TestGetEnv(t *testing.T) {
	t.Setenv("PI_TEST_ENV", "configured")
	if got := getEnv("PI_TEST_ENV", "default"); got != "configured" {
		t.Fatalf("getEnv configured = %q", got)
	}
	if got := getEnv("PI_TEST_MISSING", "default"); got != "default" {
		t.Fatalf("getEnv default = %q", got)
	}
}

func TestParseDelay(t *testing.T) {
	tests := []struct {
		raw  string
		want time.Duration
	}{
		{"", 0},
		{"bad", 0},
		{"-1", 0},
		{"25", 25 * time.Millisecond},
	}
	for _, tt := range tests {
		if got := parseDelay(tt.raw); got != tt.want {
			t.Fatalf("parseDelay(%q) = %s, want %s", tt.raw, got, tt.want)
		}
	}
}

func TestExtractTextAndTruncate(t *testing.T) {
	prompt := []acp.ContentBlock{
		acp.TextBlock("hello"),
		acp.TextBlock("world"),
	}
	if got := extractText(prompt); got != "hello world" {
		t.Fatalf("extractText = %q", got)
	}
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("truncate short = %q", got)
	}
	if got := truncate("abcdefghijklmnopqrstuvwxyz", 5); got != "abcde..." {
		t.Fatalf("truncate long = %q", got)
	}
}

func TestMockAgentBasics(t *testing.T) {
	agent := &mockAgent{responseText: "reply to {{prompt}}"}
	if _, err := agent.Authenticate(context.Background(), acp.AuthenticateRequest{}); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	init, err := agent.Initialize(context.Background(), acp.InitializeRequest{})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if init.AgentInfo == nil || init.AgentInfo.Name != "pi-acp-mock" {
		t.Fatalf("unexpected agent info: %#v", init.AgentInfo)
	}
	sess, err := agent.NewSession(context.Background(), acp.NewSessionRequest{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if !strings.HasPrefix(string(sess.SessionId), "mock-") {
		t.Fatalf("unexpected session id %q", sess.SessionId)
	}
	resp, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: sess.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("test prompt")},
	})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if resp.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("StopReason = %q", resp.StopReason)
	}
	if err := agent.Cancel(context.Background(), acp.CancelNotification{}); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if _, err := agent.ResumeSession(context.Background(), acp.ResumeSessionRequest{SessionId: sess.SessionId}); err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}
	if _, err := agent.CloseSession(context.Background(), acp.CloseSessionRequest{SessionId: sess.SessionId}); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if _, err := agent.ListSessions(context.Background(), acp.ListSessionsRequest{}); err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if _, err := agent.SetSessionConfigOption(context.Background(), acp.SetSessionConfigOptionRequest{}); err != nil {
		t.Fatalf("SetSessionConfigOption: %v", err)
	}
	if _, err := agent.SetSessionMode(context.Background(), acp.SetSessionModeRequest{}); err != nil {
		t.Fatalf("SetSessionMode: %v", err)
	}
}

func TestMockAgentPromptCancelledDuringDelay(t *testing.T) {
	agent := &mockAgent{responseText: "unused", delay: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resp, err := agent.Prompt(ctx, acp.PromptRequest{})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if resp.StopReason != acp.StopReasonCancelled {
		t.Fatalf("StopReason = %q", resp.StopReason)
	}
}

// newAgentFromEnv is main's configuration step. Defaults must apply when the
// knobs are unset, and both knobs must be honored when they are.
func TestNewAgentFromEnv(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		t.Setenv("PI_MOCK_RESPONSE", "")
		t.Setenv("PI_MOCK_DELAY_MS", "")

		a := newAgentFromEnv()
		if a.responseText != "Mock ACP response" {
			t.Errorf("responseText = %q, want the default", a.responseText)
		}
		if a.delay != 0 {
			t.Errorf("delay = %s, want 0 when unset", a.delay)
		}
	})

	t.Run("configured", func(t *testing.T) {
		t.Setenv("PI_MOCK_RESPONSE", "hello")
		t.Setenv("PI_MOCK_DELAY_MS", "250")

		a := newAgentFromEnv()
		if a.responseText != "hello" {
			t.Errorf("responseText = %q, want hello", a.responseText)
		}
		if a.delay != 250*time.Millisecond {
			t.Errorf("delay = %s, want 250ms", a.delay)
		}
	})
}

// Logout is part of the ACP agent surface; a client calling it must get a clean
// response rather than an unimplemented error.
func TestMockAgent_Logout(t *testing.T) {
	a := &mockAgent{}
	if _, err := a.Logout(context.Background(), acp.LogoutRequest{}); err != nil {
		t.Errorf("Logout: %v", err)
	}
	if _, err := a.Authenticate(context.Background(), acp.AuthenticateRequest{}); err != nil {
		t.Errorf("Authenticate: %v", err)
	}
}

// A prompt canceled while the mock is sleeping must report StopReasonCancelled
// and surface the context error, not deliver its canned response anyway.
func TestMockAgent_PromptCancelledDuringDelay(t *testing.T) {
	a := &mockAgent{responseText: "should not be delivered", delay: 10 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled

	resp, err := a.Prompt(ctx, acp.PromptRequest{})
	if err == nil {
		t.Fatal("expected the context error to surface")
	}
	if resp.StopReason != acp.StopReasonCancelled {
		t.Errorf("StopReason = %v, want %v", resp.StopReason, acp.StopReasonCancelled)
	}
}

// serve must return when the peer closes the connection — an EOF on stdin ends
// the session. If it did not, the mock would hang and wedge any test using it.
func TestServe_ReturnsWhenPeerCloses(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		serve(io.Discard, strings.NewReader("")) // immediate EOF
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after the peer closed the connection")
	}
}

// recordingConn captures SessionUpdate notifications so tests can assert on
// the emitted update stream without a real transport.
type recordingConn struct {
	mu       sync.Mutex
	sessions []acp.SessionNotification
}

func (r *recordingConn) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions = append(r.sessions, n)
	return nil
}

func (r *recordingConn) count(kind string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.sessions {
		switch kind {
		case "tool_call":
			if s.Update.ToolCall != nil {
				n++
			}
		case "tool_call_update":
			if s.Update.ToolCallUpdate != nil {
				n++
			}
		case "thought":
			if s.Update.AgentThoughtChunk != nil {
				n++
			}
		}
	}
	return n
}

func TestMockAgent_ToolLifecycle(t *testing.T) {
	agent := &mockAgent{responseText: "done", emitTools: true, sessions: map[acp.SessionId]*mockSession{}}
	rec := &recordingConn{}
	agent.conn = rec

	sess, err := agent.NewSession(context.Background(), acp.NewSessionRequest{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: sess.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("run tools")},
	}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if got := rec.count("tool_call"); got != 1 {
		t.Errorf("tool_call notifications = %d, want 1", got)
	}
	if got := rec.count("tool_call_update"); got < 2 {
		t.Errorf("tool_call_update notifications = %d, want >= 2 (in_progress + completed)", got)
	}
	last := rec.sessions[len(rec.sessions)-2] // completed tool update
	if last.Update.ToolCallUpdate == nil || last.Update.ToolCallUpdate.Status == nil {
		t.Fatalf("last tool update missing status")
	}
	if *last.Update.ToolCallUpdate.Status != acp.ToolCallStatusCompleted {
		t.Errorf("tool status = %q, want completed", *last.Update.ToolCallUpdate.Status)
	}
	if agent.sessions == nil {
		t.Fatalf("sessions map not initialized")
	}
}

func TestMockAgent_LoadSessionReplays(t *testing.T) {
	agent := &mockAgent{responseText: "answer", sessions: map[acp.SessionId]*mockSession{}}
	rec := &recordingConn{}
	agent.conn = rec

	sess, err := agent.NewSession(context.Background(), acp.NewSessionRequest{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: sess.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	before := len(rec.sessions)

	// A second load replays the same transcript; both are complete replays.
	if _, err := agent.LoadSession(context.Background(), acp.LoadSessionRequest{SessionId: sess.SessionId}); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	replayed := len(rec.sessions) - before
	if replayed != 2 { // one user chunk + one agent chunk
		t.Errorf("replayed updates = %d, want 2", replayed)
	}

	if _, err := agent.ListSessions(context.Background(), acp.ListSessionsRequest{}); err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	list := func() acp.ListSessionsResponse {
		resp, err := agent.ListSessions(context.Background(), acp.ListSessionsRequest{})
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		return resp
	}()
	if len(list.Sessions) != 1 {
		t.Errorf("listed sessions = %d, want 1", len(list.Sessions))
	}
	if list.Sessions[0].Title == nil || *list.Sessions[0].Title != "hello" {
		t.Errorf("session title = %v, want \"hello\"", list.Sessions[0].Title)
	}
}

func TestMockAgent_AvailableCommands(t *testing.T) {
	agent := &mockAgent{responseText: "ok", emitCommands: true, sessions: map[acp.SessionId]*mockSession{}}
	rec := &recordingConn{}
	agent.conn = rec
	if _, err := agent.NewSession(context.Background(), acp.NewSessionRequest{}); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	found := false
	for _, s := range rec.sessions {
		if s.Update.AvailableCommandsUpdate != nil && len(s.Update.AvailableCommandsUpdate.AvailableCommands) == 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("available_commands_update not emitted on session/new")
	}
}
