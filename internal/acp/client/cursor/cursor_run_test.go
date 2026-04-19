package cursor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	shared "github.com/dimetron/pi-go/internal/acp"
)

type mockClientForRun struct{}

func (m *mockClientForRun) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, nil
}

func (m *mockClientForRun) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, nil
}

func (m *mockClientForRun) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, nil
}

func (m *mockClientForRun) SessionUpdate(context.Context, acp.SessionNotification) error {
	return nil
}

func (m *mockClientForRun) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, nil
}

func (m *mockClientForRun) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, nil
}

func (m *mockClientForRun) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, nil
}

func (m *mockClientForRun) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, nil
}

func (m *mockClientForRun) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, nil
}

func runSessionWithScript(t *testing.T, script string) *RunningSession {
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "test.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cmd := exec.Command("/bin/bash", scriptPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start command: %v", err)
	}
	defer cmd.Process.Kill()

	client := &mockClientForRun{}
	conn := acp.NewClientSideConnection(client, stdin, stdout)

	return &RunningSession{
		cmd:        cmd,
		stdin:      stdin,
		stderr:     &stderrBuffer{},
		conn:       conn,
		events:     make(chan shared.Event, 32),
		done:       make(chan struct{}),
		toolFilter: shared.NewToolCallTitleFilter(func(title string) {}),
	}
}

func TestRunningSessionRunWithPromptError(t *testing.T) {
	script := `#!/bin/bash
printf '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentInfo":{"name":"test","version":"1.0"}}}\n'
printf '{"jsonrpc":"2.0","id":2,"result":{"sessionId":"test-session"}}\n'
printf '{"jsonrpc":"2.0","id":3,"error":{"code":-32600,"message":"prompt error"}}\n'
exit 1
`
	session := runSessionWithScript(t, script)
	go session.run(RunRequest{Prompt: "test"}, acp.Implementation{Name: "test", Version: "1.0"})

	select {
	case <-session.done:
	case <-time.After(2 * time.Second):
		t.Error("session did not complete in time")
	}

	session.mu.Lock()
	result := session.result
	session.mu.Unlock()

	if result.Status != shared.StatusError && result.Error == "" {
		t.Errorf("expected error status or message, got status=%s, error=%q", result.Status, result.Error)
	}
}

func TestRunningSessionRunWithSessionError(t *testing.T) {
	script := `#!/bin/bash
printf '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentInfo":{"name":"test","version":"1.0"}}}\n'
printf '{"jsonrpc":"2.0","id":2,"error":{"code":-32603,"message":"session error"}}\n'
exit 1
`
	session := runSessionWithScript(t, script)
	go session.run(RunRequest{Prompt: "test"}, acp.Implementation{Name: "test", Version: "1.0"})

	select {
	case <-session.done:
	case <-time.After(2 * time.Second):
		t.Error("session did not complete in time")
	}

	session.mu.Lock()
	result := session.result
	session.mu.Unlock()

	if result.Status != shared.StatusError {
		t.Errorf("expected StatusError, got %v", result.Status)
	}
}

func TestRunningSessionRunWithInitError(t *testing.T) {
	script := `#!/bin/bash
printf '{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"init error"}}\n'
exit 1
`
	session := runSessionWithScript(t, script)
	go session.run(RunRequest{Prompt: "test"}, acp.Implementation{Name: "test", Version: "1.0"})

	select {
	case <-session.done:
	case <-time.After(2 * time.Second):
		t.Error("session did not complete in time")
	}

	session.mu.Lock()
	result := session.result
	session.mu.Unlock()

	if result.Status != shared.StatusError {
		t.Errorf("expected StatusError, got %v", result.Status)
	}
}

func TestRunningSessionRunWithProcessExitError(t *testing.T) {
	script := `#!/bin/bash
printf '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentInfo":{"name":"test","version":"1.0"}}}\n'
printf '{"jsonrpc":"2.0","id":2,"result":{"sessionId":"test-session"}}\n'
printf '{"jsonrpc":"2.0","id":3,"result":{"stopReason":"end_turn"}}\n'
exit 1
`
	session := runSessionWithScript(t, script)
	go session.run(RunRequest{Prompt: "test"}, acp.Implementation{Name: "test", Version: "1.0"})

	select {
	case <-session.done:
	case <-time.After(2 * time.Second):
		t.Error("session did not complete in time")
	}

	session.mu.Lock()
	result := session.result
	session.mu.Unlock()

	// Script exits with error but we should still get success if prompt succeeded.
	t.Logf("status=%s, error=%q, result=%q", result.Status, result.Error, result.Result)
}

func TestCursorSessionEmitDrop(t *testing.T) {
	session := &RunningSession{
		events: make(chan shared.Event, 1),
		done:   make(chan struct{}),
	}
	session.events <- shared.Event{Type: "existing"}
	session.emit(shared.Event{Type: shared.EventTypeMessage, Content: "dropped"})

	select {
	case ev := <-session.events:
		if ev.Type != "existing" {
			t.Errorf("existing event evicted; got %q", ev.Type)
		}
	default:
		t.Fatal("expected buffered event")
	}
}

func TestCursorSessionAppendResult(t *testing.T) {
	session := &RunningSession{
		toolFilter: shared.NewToolCallTitleFilter(func(string) {}),
	}
	session.appendResult("Hello ")
	session.appendResult("World")
	if session.result.Result != "Hello World" {
		t.Errorf("result = %q, want %q", session.result.Result, "Hello World")
	}
}

func TestCursorHandleUpdateAllKinds(t *testing.T) {
	session := &RunningSession{
		events:     make(chan shared.Event, 32),
		done:       make(chan struct{}),
		toolFilter: shared.NewToolCallTitleFilter(func(string) {}),
	}

	// AgentMessageChunk → appends to result and emits message event.
	session.handleUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("s-agent"),
		Update: acp.SessionUpdate{
			AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.TextBlock("hi")},
		},
	})
	if session.result.Result != "hi" {
		t.Errorf("result after message = %q, want %q", session.result.Result, "hi")
	}

	// AgentThoughtChunk → emits progress event only, does not extend result.
	session.handleUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("s-agent"),
		Update: acp.SessionUpdate{
			AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{Content: acp.TextBlock("thinking")},
		},
	})
	if session.result.Result != "hi" {
		t.Errorf("thought must not affect result; got %q", session.result.Result)
	}

	// ToolCall + ToolCallUpdate exercise the filter path.
	session.handleUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("s-agent"),
		Update: acp.SessionUpdate{
			ToolCall: &acp.SessionUpdateToolCall{
				ToolCallId: acp.ToolCallId("t1"),
				Title:      "Read: foo",
			},
		},
	})
	title := "Read: foo done"
	session.handleUpdate(acp.SessionNotification{
		SessionId: acp.SessionId("s-agent"),
		Update: acp.SessionUpdate{
			ToolCallUpdate: &acp.SessionToolCallUpdate{
				ToolCallId: acp.ToolCallId("t1"),
				Title:      &title,
			},
		},
	})
	if session.curSession != "s-agent" {
		t.Errorf("curSession = %q, want %q", session.curSession, "s-agent")
	}
}

func TestCursorCallbackClientUnsupportedMethods(t *testing.T) {
	c := &callbackClient{session: &RunningSession{
		events:     make(chan shared.Event, 32),
		done:       make(chan struct{}),
		toolFilter: shared.NewToolCallTitleFilter(func(string) {}),
	}}
	ctx := context.Background()

	if _, err := c.ReadTextFile(ctx, acp.ReadTextFileRequest{}); err == nil {
		t.Error("ReadTextFile should return error")
	}
	if _, err := c.WriteTextFile(ctx, acp.WriteTextFileRequest{}); err == nil {
		t.Error("WriteTextFile should return error")
	}
	if _, err := c.CreateTerminal(ctx, acp.CreateTerminalRequest{}); err == nil {
		t.Error("CreateTerminal should return error")
	}
	if _, err := c.KillTerminal(ctx, acp.KillTerminalRequest{}); err == nil {
		t.Error("KillTerminal should return error")
	}
	if _, err := c.TerminalOutput(ctx, acp.TerminalOutputRequest{}); err == nil {
		t.Error("TerminalOutput should return error")
	}
	if _, err := c.ReleaseTerminal(ctx, acp.ReleaseTerminalRequest{}); err == nil {
		t.Error("ReleaseTerminal should return error")
	}
	if _, err := c.WaitForTerminalExit(ctx, acp.WaitForTerminalExitRequest{}); err == nil {
		t.Error("WaitForTerminalExit should return error")
	}

	// RequestPermission auto-approves — should not return error.
	if _, err := c.RequestPermission(ctx, acp.RequestPermissionRequest{}); err != nil {
		t.Errorf("RequestPermission error = %v", err)
	}

	// SessionUpdate delegates to handleUpdate and returns nil.
	if err := c.SessionUpdate(ctx, acp.SessionNotification{SessionId: acp.SessionId("s1")}); err != nil {
		t.Errorf("SessionUpdate error = %v", err)
	}
}

func TestCursorEventsAndDoneChannels(t *testing.T) {
	session := &RunningSession{
		events: make(chan shared.Event, 1),
		done:   make(chan struct{}),
	}
	if session.Events() == nil {
		t.Error("Events() must not return nil")
	}
	if session.Done() == nil {
		t.Error("Done() must not return nil")
	}
}

func TestCursorWaitReturnsResult(t *testing.T) {
	done := make(chan struct{})
	session := &RunningSession{
		done:   done,
		result: shared.RunResult{Status: shared.StatusSuccess, Result: "ok"},
	}
	close(done)
	if got := session.Wait(); got.Result != "ok" {
		t.Errorf("Wait() result = %q, want %q", got.Result, "ok")
	}
}
