package server

import (
	"context"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

func TestAgentInitializeAdvertisesLoadSession(t *testing.T) {
	a := &Agent{}
	resp, err := a.Initialize(context.Background(), acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
	})
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if !resp.AgentCapabilities.LoadSession {
		t.Fatalf("LoadSession capability = false, want true")
	}
}

func TestAgentLoadSessionCreatesUnknownSession(t *testing.T) {
	a := &Agent{}
	const sid = "sess_loaded_unknown"
	if _, err := a.LoadSession(context.Background(), acp.LoadSessionRequest{
		SessionId:  acp.SessionId(sid),
		Cwd:        "/tmp/loaded",
		McpServers: []acp.McpServer{},
	}); err != nil {
		t.Fatalf("LoadSession() error = %v", err)
	}

	resp, err := a.Prompt(context.Background(), acp.PromptRequest{
		SessionId: acp.SessionId(sid),
		Prompt:    []acp.ContentBlock{acp.TextBlock("hi")},
	})
	if err != nil {
		t.Fatalf("Prompt() after load error = %v", err)
	}
	if resp.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want end_turn", resp.StopReason)
	}
}

func TestAgentLoadSessionRefreshesCwd(t *testing.T) {
	a := &Agent{}
	sess, err := a.NewSession(context.Background(), acp.NewSessionRequest{
		Cwd:        "/tmp/original",
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	captured := make(chan string, 1)
	a.Handler = func(_ context.Context, turn PromptTurn) (PromptResult, error) {
		captured <- turn.CWD
		return PromptResult{FinalText: "ok", StopReason: acp.StopReasonEndTurn}, nil
	}

	if _, err := a.LoadSession(context.Background(), acp.LoadSessionRequest{
		SessionId:  sess.SessionId,
		Cwd:        "/tmp/refreshed",
		McpServers: []acp.McpServer{},
	}); err != nil {
		t.Fatalf("LoadSession() error = %v", err)
	}

	if _, err := a.Prompt(context.Background(), acp.PromptRequest{
		SessionId: sess.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hi")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	select {
	case got := <-captured:
		if got != "/tmp/refreshed" {
			t.Fatalf("turn.CWD = %q, want /tmp/refreshed", got)
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not run")
	}
}

func TestAgentLoadSessionOverRPC(t *testing.T) {
	clientRW, agentRW := pipePair()
	defer clientRW.Close()

	a := &Agent{}
	defer wireAgent(t, a, agentRW)()

	clientConn := acp.NewClientSideConnection(&capturingClient{}, clientRW, clientRW)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	initResp, err := clientConn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
	})
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if !initResp.AgentCapabilities.LoadSession {
		t.Fatalf("server did not advertise LoadSession over the wire")
	}

	const sid = "sess_rpc_loaded"
	if _, err := clientConn.LoadSession(ctx, acp.LoadSessionRequest{
		SessionId:  acp.SessionId(sid),
		Cwd:        "/tmp/rpc",
		McpServers: []acp.McpServer{},
	}); err != nil {
		t.Fatalf("LoadSession() error = %v", err)
	}

	promptResp, err := clientConn.Prompt(ctx, acp.PromptRequest{
		SessionId: acp.SessionId(sid),
		Prompt:    []acp.ContentBlock{acp.TextBlock("rpc-load")},
	})
	if err != nil {
		t.Fatalf("Prompt() after RPC load error = %v", err)
	}
	if promptResp.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want end_turn", promptResp.StopReason)
	}

	a.mu.Lock()
	st, ok := a.sessions[sid]
	a.mu.Unlock()
	if !ok {
		t.Fatal("session not registered after LoadSession")
	}
	if !strings.EqualFold(st.cwd, "/tmp/rpc") {
		t.Fatalf("session cwd = %q, want /tmp/rpc", st.cwd)
	}
}
