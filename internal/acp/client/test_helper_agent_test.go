package client

import (
	"context"
	"os"

	acp "github.com/coder/acp-go-sdk"
)

type helperAgent struct{}

func (helperAgent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

func (helperAgent) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
		AgentInfo:       &acp.Implementation{Name: "helper-agent", Version: "test"},
	}, nil
}

func (helperAgent) NewSession(context.Context, acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	return acp.NewSessionResponse{SessionId: acp.SessionId("session-1")}, nil
}

func (helperAgent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, nil
}

func (helperAgent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	prompt := ""
	if len(params.Prompt) > 0 && params.Prompt[0].Text != nil {
		prompt = params.Prompt[0].Text.Text
	}
	_ = agentConn.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: params.SessionId,
		Update:    acp.UpdateAgentMessageText("echo: " + prompt),
	})
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (helperAgent) Cancel(context.Context, acp.CancelNotification) error {
	return nil
}

func (helperAgent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, nil
}

func (helperAgent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, nil
}

func (helperAgent) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, nil
}

func (helperAgent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}

var agentConn *acp.AgentSideConnection

func runHelperACPAgent() error {
	agentConn = acp.NewAgentSideConnection(helperAgent{}, os.Stdout, os.Stdin)
	<-agentConn.Done()
	return nil
}
