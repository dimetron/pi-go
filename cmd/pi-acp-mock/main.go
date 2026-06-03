// Package main is a mock ACP server for testing pi-go's ACP integration.
// It implements the minimal ACP agent surface and responds to prompts with
// configurable fake output for closed-loop E2E testing.
//
// Usage:
//
//	PI_MOCK_RESPONSE="hello world" PI_MOCK_DELAY_MS=100 ./pi-acp-mock
//
// Environment variables:
//
//	PI_MOCK_RESPONSE - Text to respond with (default: "Mock ACP response").
//	                   If the text contains "{{prompt}}" it is replaced with
//	                   the actual prompt received.
//	PI_MOCK_DELAY_MS - Thinking delay in milliseconds before reply (default: 0).
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

type mockAgent struct {
	responseText string
	delay        time.Duration
	conn         *acp.AgentSideConnection
}

func (m *mockAgent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

func (m *mockAgent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}

func (m *mockAgent) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion:   acp.ProtocolVersion(acp.ProtocolVersionNumber),
		AgentInfo:         &acp.Implementation{Name: "pi-acp-mock", Version: "1.0"},
		AgentCapabilities: acp.AgentCapabilities{LoadSession: true},
	}, nil
}

func (m *mockAgent) NewSession(_ context.Context, _ acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	return acp.NewSessionResponse{
		SessionId: acp.SessionId(fmt.Sprintf("mock-%d", os.Getpid())),
	}, nil
}

func (m *mockAgent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, nil
}

func (m *mockAgent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	promptText := extractText(params.Prompt)

	if m.delay > 0 {
		select {
		case <-ctx.Done():
			return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, ctx.Err()
		case <-time.After(m.delay):
		}
	}

	log.Printf("received prompt: %s", truncate(promptText, 100))

	response := strings.ReplaceAll(m.responseText, "{{prompt}}", promptText)

	if m.conn != nil {
		_ = m.conn.SessionUpdate(ctx, acp.SessionNotification{
			SessionId: params.SessionId,
			Update:    acp.UpdateAgentMessageText(response),
		})
	}

	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (m *mockAgent) Cancel(context.Context, acp.CancelNotification) error {
	return nil
}

func (m *mockAgent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, nil
}

func (m *mockAgent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, nil
}

func (m *mockAgent) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, nil
}

func (m *mockAgent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}

func main() {
	log.SetOutput(os.Stderr)
	log.SetPrefix("[pi-acp-mock] ")

	responseText := getEnv("PI_MOCK_RESPONSE", "Mock ACP response")
	delay := parseDelay(os.Getenv("PI_MOCK_DELAY_MS"))

	log.Printf("starting: response=%q delay=%s", truncate(responseText, 50), delay)

	agent := &mockAgent{responseText: responseText, delay: delay}
	conn := acp.NewAgentSideConnection(agent, os.Stdout, os.Stdin)
	agent.conn = conn

	<-conn.Done()
	log.Println("stopped")
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func parseDelay(raw string) time.Duration {
	if raw == "" {
		return 0
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms < 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

func extractText(blocks []acp.ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Text != nil {
			parts = append(parts, b.Text.Text)
		}
	}
	return strings.Join(parts, " ")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
