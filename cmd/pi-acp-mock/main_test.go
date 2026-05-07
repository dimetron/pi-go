package main

import (
	"context"
	"strings"
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
