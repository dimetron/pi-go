package server

import (
	"context"
	"errors"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

func TestAgentCancelAbortsInflightPrompt(t *testing.T) {
	started := make(chan struct{})
	a := &Agent{
		Handler: func(ctx context.Context, _ PromptTurn) (PromptResult, error) {
			close(started)
			<-ctx.Done()
			return PromptResult{}, ctx.Err()
		},
	}
	sess, err := a.NewSession(context.Background(), acp.NewSessionRequest{
		Cwd:        "/tmp",
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	type promptOut struct {
		resp acp.PromptResponse
		err  error
	}
	out := make(chan promptOut, 1)
	go func() {
		resp, err := a.Prompt(context.Background(), acp.PromptRequest{
			SessionId: sess.SessionId,
			Prompt:    []acp.ContentBlock{acp.TextBlock("slow")},
		})
		out <- promptOut{resp, err}
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler never started")
	}

	if err := a.Cancel(context.Background(), acp.CancelNotification{SessionId: sess.SessionId}); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}

	select {
	case got := <-out:
		if got.err != nil {
			t.Fatalf("Prompt() err = %v, want nil cancel success", got.err)
		}
		if got.resp.StopReason != acp.StopReasonCancelled {
			t.Fatalf("StopReason = %q, want %q", got.resp.StopReason, acp.StopReasonCancelled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Prompt did not return after Cancel")
	}
}

func TestAgentCancelUnknownSessionIsNoOp(t *testing.T) {
	a := &Agent{}
	if err := a.Cancel(context.Background(), acp.CancelNotification{SessionId: acp.SessionId("nope")}); err != nil {
		t.Fatalf("Cancel(unknown) err = %v, want nil", err)
	}
}

func TestAgentCancelOverRPC(t *testing.T) {
	started := make(chan struct{})
	a := &Agent{
		Handler: func(ctx context.Context, _ PromptTurn) (PromptResult, error) {
			close(started)
			<-ctx.Done()
			return PromptResult{}, ctx.Err()
		},
	}

	clientRW, agentRW := pipePair()
	defer clientRW.Close()
	defer wireAgent(t, a, agentRW)()

	clientConn := acp.NewClientSideConnection(&capturingClient{}, clientRW, clientRW)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := clientConn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
	}); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	sess, err := clientConn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        "/tmp",
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	type promptOut struct {
		resp acp.PromptResponse
		err  error
	}
	out := make(chan promptOut, 1)
	go func() {
		resp, err := clientConn.Prompt(ctx, acp.PromptRequest{
			SessionId: sess.SessionId,
			Prompt:    []acp.ContentBlock{acp.TextBlock("slow")},
		})
		out <- promptOut{resp, err}
	}()

	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("handler never started")
	}

	if err := clientConn.Cancel(ctx, acp.CancelNotification{SessionId: sess.SessionId}); err != nil {
		t.Fatalf("Cancel() over RPC error = %v", err)
	}

	select {
	case got := <-out:
		if got.err != nil {
			if errors.Is(got.err, context.Canceled) {
				t.Fatalf("Prompt returned context.Canceled; expected %q stop-reason reply", acp.StopReasonCancelled)
			}
			t.Fatalf("Prompt() err = %v, want nil", got.err)
		}
		if got.resp.StopReason != acp.StopReasonCancelled {
			t.Fatalf("StopReason = %q, want %q", got.resp.StopReason, acp.StopReasonCancelled)
		}
	case <-ctx.Done():
		t.Fatal("Prompt did not return after RPC Cancel")
	}
}
