package client

import (
	"context"
	"os"
	"sync/atomic"

	acp "github.com/coder/acp-go-sdk"
)

// helperAgent is the in-process ACP agent the test helper subprocess runs.
// The connection it answers on is published through conn once
// NewAgentSideConnection has returned: that constructor starts the receive
// goroutine that calls Prompt, so writing a plain package-level variable
// after it returns races with Prompt reading it. ready is closed after the
// store so Prompt can block until the connection exists.
type helperAgent struct {
	conn  atomic.Pointer[acp.AgentSideConnection]
	ready chan struct{}
}

func (*helperAgent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

func (*helperAgent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}

func (*helperAgent) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
		AgentInfo:       &acp.Implementation{Name: "helper-agent", Version: "test"},
	}, nil
}

func (*helperAgent) NewSession(context.Context, acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	return acp.NewSessionResponse{SessionId: acp.SessionId("session-1")}, nil
}

func (*helperAgent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, nil
}

func (h *helperAgent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	prompt := ""
	if len(params.Prompt) > 0 && params.Prompt[0].Text != nil {
		prompt = params.Prompt[0].Text.Text
	}
	<-h.ready
	_ = h.conn.Load().SessionUpdate(ctx, acp.SessionNotification{
		SessionId: params.SessionId,
		Update:    acp.UpdateAgentMessageText("echo: " + prompt),
	})
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (*helperAgent) Cancel(context.Context, acp.CancelNotification) error {
	return nil
}

func (*helperAgent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, nil
}

func (*helperAgent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, nil
}

func (*helperAgent) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, nil
}

func (*helperAgent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}

func runHelperACPAgent() error {
	h := &helperAgent{ready: make(chan struct{})}
	conn := acp.NewAgentSideConnection(h, os.Stdout, os.Stdin)
	h.conn.Store(conn)
	close(h.ready)
	<-conn.Done()
	return nil
}
