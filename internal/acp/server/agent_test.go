package server

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
)

type mockUpdater struct {
	err error
}

func (m *mockUpdater) Update(context.Context, acp.SessionUpdate) error {
	return m.err
}

type capturingClient struct {
	messages atomic.Pointer[[]string]

	mu                    sync.Mutex
	availableCommandsByID map[string][]acp.AvailableCommand
	availableCommandsSeen atomic.Int32
	failCommandsCount     atomic.Int32
}

func (c *capturingClient) append(text string) {
	for {
		cur := c.messages.Load()
		var next []string
		if cur != nil {
			next = append(next, *cur...)
		}
		next = append(next, text)
		if c.messages.CompareAndSwap(cur, &next) {
			return
		}
	}
}

func (c *capturingClient) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	if n.Update.AgentMessageChunk != nil {
		blk := n.Update.AgentMessageChunk.Content
		if blk.Text != nil {
			c.append(blk.Text.Text)
		}
	}
	if n.Update.AvailableCommandsUpdate != nil {
		c.availableCommandsSeen.Add(1)
		for {
			cur := c.failCommandsCount.Load()
			if cur <= 0 {
				break
			}
			if c.failCommandsCount.CompareAndSwap(cur, cur-1) {
				return errors.New("inject available_commands_update failure")
			}
		}
		cmds := append([]acp.AvailableCommand(nil), n.Update.AvailableCommandsUpdate.AvailableCommands...)
		c.mu.Lock()
		if c.availableCommandsByID == nil {
			c.availableCommandsByID = make(map[string][]acp.AvailableCommand)
		}
		c.availableCommandsByID[string(n.SessionId)] = cmds
		c.mu.Unlock()
	}
	return nil
}

func (c *capturingClient) availableCommands(sessionID string) ([]acp.AvailableCommand, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cmds, ok := c.availableCommandsByID[sessionID]
	if !ok {
		return nil, false
	}
	return append([]acp.AvailableCommand(nil), cmds...), true
}

func waitForAvailableCommands(t *testing.T, c *capturingClient, sessionID string) []acp.AvailableCommand {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cmds, ok := c.availableCommands(sessionID); ok {
			return cmds
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for available commands for session %s", sessionID)
	return nil
}

func hasAvailableCommand(cmds []acp.AvailableCommand, name string) bool {
	for _, cmd := range cmds {
		if cmd.Name == name {
			return true
		}
	}
	return false
}

func (c *capturingClient) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, errors.New("requestPermission not used in skeleton tests")
}

func (c *capturingClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, errors.New("readTextFile not used in skeleton tests")
}

func (c *capturingClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, errors.New("writeTextFile not used in skeleton tests")
}

func (c *capturingClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, errors.New("createTerminal not used in skeleton tests")
}

func (c *capturingClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, errors.New("killTerminal not used in skeleton tests")
}

func (c *capturingClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, errors.New("releaseTerminal not used in skeleton tests")
}

func (c *capturingClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, errors.New("terminalOutput not used in skeleton tests")
}

func (c *capturingClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, errors.New("waitForTerminalExit not used in skeleton tests")
}

// pipePair returns two io.ReadWriteClosers cross-wired by a pair of io.Pipe
// endpoints, simulating a bidirectional stdio transport between an ACP client
// and an ACP agent.
func pipePair() (clientRW, agentRW *pipeRW) {
	c2aR, c2aW := io.Pipe() // client -> agent
	a2cR, a2cW := io.Pipe() // agent -> client
	clientRW = &pipeRW{r: a2cR, w: c2aW}
	agentRW = &pipeRW{r: c2aR, w: a2cW}
	return
}

type pipeRW struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (p *pipeRW) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeRW) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeRW) Close() error {
	_ = p.r.Close()
	return p.w.Close()
}

// wireAgent wires a new agent-side connection around the supplied Agent using
// the given agent-side pipe, and returns a cleanup func.
func wireAgent(t *testing.T, a *Agent, rw *pipeRW) func() {
	t.Helper()
	conn := acp.NewAgentSideConnection(a, rw, rw)
	a.SetAgentConnection(conn)
	return func() { _ = rw.Close() }
}

func TestAgentInitializeAdvertisesPi(t *testing.T) {
	clientRW, agentRW := pipePair()
	defer clientRW.Close()

	a := &Agent{AgentInfo: acp.Implementation{Name: "pi-test", Version: "1.2.3"}}
	defer wireAgent(t, a, agentRW)()

	clientConn := acp.NewClientSideConnection(&capturingClient{}, clientRW, clientRW)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := clientConn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
		ClientInfo:      &acp.Implementation{Name: "pi-test-client"},
	})
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if resp.AgentInfo == nil {
		t.Fatalf("AgentInfo = nil, want pi info")
	}
	if resp.AgentInfo.Name != "pi-test" || resp.AgentInfo.Version != "1.2.3" {
		t.Fatalf("AgentInfo = %+v, want {Name:pi-test Version:1.2.3}", resp.AgentInfo)
	}
	if int(resp.ProtocolVersion) != acp.ProtocolVersionNumber {
		t.Fatalf("ProtocolVersion = %d, want %d", resp.ProtocolVersion, acp.ProtocolVersionNumber)
	}
	if !resp.AgentCapabilities.LoadSession {
		t.Fatalf("LoadSession = false, want true")
	}
	if !resp.AgentCapabilities.PromptCapabilities.EmbeddedContext {
		t.Fatalf("PromptCapabilities.EmbeddedContext = false, want true so Zed can inline file context")
	}
}

func TestAgentNewSessionAndPromptFlow(t *testing.T) {
	if raceEnabled {
		t.Skip("acp-go-sdk ClientSideConnection has a constructor/read loop race under -race in pipe-based tests")
	}

	clientRW, agentRW := pipePair()
	defer clientRW.Close()

	a := &Agent{}
	defer wireAgent(t, a, agentRW)()

	captures := &capturingClient{}
	clientConn := acp.NewClientSideConnection(captures, clientRW, clientRW)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := clientConn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
	}); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	sessResp, err := clientConn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        "/tmp",
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if strings.TrimSpace(string(sessResp.SessionId)) == "" {
		t.Fatalf("empty SessionId")
	}

	promptResp, err := clientConn.Prompt(ctx, acp.PromptRequest{
		SessionId: sessResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello pi")},
	})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if promptResp.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want %q", promptResp.StopReason, acp.StopReasonEndTurn)
	}

	msgs := captures.messages.Load()
	if msgs == nil || len(*msgs) == 0 {
		t.Fatalf("no session updates captured")
	}
	if len(*msgs) != 1 {
		t.Fatalf("captured %d message chunks, want exactly 1 (dedup regression): %q", len(*msgs), *msgs)
	}
	got := strings.TrimSpace(strings.Join(*msgs, ""))
	if got != "echo: hello pi" {
		t.Fatalf("captured message = %q, want %q", got, "echo: hello pi")
	}
}

func TestAgentLoadSessionSendsAvailableCommandsUpdate(t *testing.T) {
	clientRW, agentRW := pipePair()
	defer clientRW.Close()

	a := &Agent{
		Skills: []extension.Skill{{Name: "review", Description: "Review code"}},
	}
	defer wireAgent(t, a, agentRW)()

	captures := &capturingClient{}
	clientConn := acp.NewClientSideConnection(captures, clientRW, clientRW)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := clientConn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
	}); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	const sid = "sess_load_commands"
	if _, err := clientConn.LoadSession(ctx, acp.LoadSessionRequest{
		SessionId:  acp.SessionId(sid),
		Cwd:        "/tmp/load-commands",
		McpServers: []acp.McpServer{},
	}); err != nil {
		t.Fatalf("LoadSession() error = %v", err)
	}

	cmds := waitForAvailableCommands(t, captures, sid)
	if !hasAvailableCommand(cmds, "help") {
		t.Fatalf("available commands = %+v, want help to be advertised", cmds)
	}
	if !hasAvailableCommand(cmds, "review") {
		t.Fatalf("available commands = %+v, want review skill to be advertised", cmds)
	}
}

func TestAgentSendAvailableCommandsCanRetryAfterFailure(t *testing.T) {
	a := &Agent{
		Skills: []extension.Skill{{Name: "review", Description: "Review code"}},
	}
	sessResp, err := a.NewSession(context.Background(), acp.NewSessionRequest{
		Cwd:        "/tmp/retry-commands",
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		state := a.sessions[string(sessResp.SessionId)]
		pending := state != nil && state.commandsPending
		a.mu.Unlock()
		if !pending {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.mu.Lock()
	state := a.sessions[string(sessResp.SessionId)]
	initiallySent := state != nil && state.commandsSent
	a.mu.Unlock()
	if initiallySent {
		t.Fatal("expected commands to remain unsent when no connection exists")
	}

	clientRW, agentRW := pipePair()
	defer clientRW.Close()
	defer wireAgent(t, a, agentRW)()

	captures := &capturingClient{}
	clientConn := acp.NewClientSideConnection(captures, clientRW, clientRW)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := clientConn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
	}); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	a.sendAvailableCommands(string(sessResp.SessionId))

	cmds := waitForAvailableCommands(t, captures, string(sessResp.SessionId))
	if !hasAvailableCommand(cmds, "review") {
		t.Fatalf("available commands after retry = %+v, want review skill", cmds)
	}
}

// TestAgentPromptEchoesMessageId asserts the agent echoes PromptRequest.MessageId
// as PromptResponse.UserMessageId so Zed can correlate prompts with replies.
func TestAgentPromptEchoesMessageId(t *testing.T) {
	a := &Agent{}
	sessResp, err := a.NewSession(context.Background(), acp.NewSessionRequest{
		Cwd:        "/tmp",
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	msgID := "7b4d6e2a-1f3c-4c2a-9b0d-0e1a2b3c4d5e"
	resp, err := a.Prompt(context.Background(), acp.PromptRequest{
		SessionId: sessResp.SessionId,
		MessageId: &msgID,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hi")},
	})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if resp.UserMessageId == nil {
		t.Fatalf("UserMessageId = nil, want %q", msgID)
	}
	if *resp.UserMessageId != msgID {
		t.Fatalf("UserMessageId = %q, want %q", *resp.UserMessageId, msgID)
	}
}

// TestAgentPromptOmitsUserMessageIdWhenAbsent guards against fabricating an id
// when the client didn't supply one.
func TestAgentPromptOmitsUserMessageIdWhenAbsent(t *testing.T) {
	a := &Agent{}
	sessResp, err := a.NewSession(context.Background(), acp.NewSessionRequest{
		Cwd:        "/tmp",
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	resp, err := a.Prompt(context.Background(), acp.PromptRequest{
		SessionId: sessResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hi")},
	})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if resp.UserMessageId != nil {
		t.Fatalf("UserMessageId = %q, want nil when client omits MessageId", *resp.UserMessageId)
	}
}

func TestAgentAuthenticate(t *testing.T) {
	a := &Agent{}
	_, err := a.Authenticate(context.Background(), acp.AuthenticateRequest{})
	if err != nil {
		t.Fatalf("Authenticate() error = %v, want nil", err)
	}
}

func TestEchoPromptHandlerUpdaterError(t *testing.T) {
	wantErr := errors.New("updater failed")
	_, err := EchoPromptHandler(context.Background(), PromptTurn{
		Prompt:  "hello",
		Updater: &mockUpdater{err: wantErr},
	})
	if err == nil {
		t.Fatal("expected error from EchoPromptHandler when updater fails")
	}
	if !strings.Contains(err.Error(), "echo session update") {
		t.Fatalf("error = %v, want it to mention 'echo session update'", err)
	}
}

func TestAgentPrompt_PanicRecovery(t *testing.T) {
	a := &Agent{
		Handler: func(context.Context, PromptTurn) (PromptResult, error) {
			panic("oops handler panicked")
		},
	}
	sessResp, err := a.NewSession(context.Background(), acp.NewSessionRequest{
		Cwd:        "/tmp",
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	_, err = a.Prompt(context.Background(), acp.PromptRequest{
		SessionId: sessResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("x")},
	})
	if err == nil {
		t.Fatal("expected error after handler panic")
	}
}

func TestAvailableCommandsForCWD_WithResolver(t *testing.T) {
	wantCWD := "/some/path"
	var sawCWD string
	wantCmds := []acp.AvailableCommand{
		{Name: "my-cmd", Description: "my custom command"},
	}
	a := &Agent{
		AvailableCommandsResolver: func(cwd string) []acp.AvailableCommand {
			sawCWD = cwd
			return wantCmds
		},
	}
	cmds := a.availableCommandsForCWD(wantCWD)
	if sawCWD != wantCWD {
		t.Fatalf("resolver received cwd = %q, want %q", sawCWD, wantCWD)
	}
	if len(cmds) != len(wantCmds) || cmds[0].Name != wantCmds[0].Name {
		t.Fatalf("availableCommandsForCWD() = %+v, want %+v", cmds, wantCmds)
	}
}

func TestAgentPromptUnknownSessionErrors(t *testing.T) {
	a := &Agent{}
	_, err := a.Prompt(context.Background(), acp.PromptRequest{
		SessionId: acp.SessionId("does-not-exist"),
		Prompt:    []acp.ContentBlock{acp.TextBlock("x")},
	})
	if err == nil {
		t.Fatal("expected error for unknown session")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("error = %q, want it to mention session id", err.Error())
	}
}

func TestAgentCustomHandlerRuns(t *testing.T) {
	var called atomic.Int32
	a := &Agent{
		Handler: func(_ context.Context, turn PromptTurn) (PromptResult, error) {
			called.Add(1)
			if turn.Prompt != "ping" {
				return PromptResult{}, errors.New("unexpected prompt")
			}
			return PromptResult{FinalText: "pong", StopReason: acp.StopReasonEndTurn}, nil
		},
	}
	// NewSession without a connection: updater is nil, which the handler path tolerates.
	sessResp, err := a.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp", McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	resp, err := a.Prompt(context.Background(), acp.PromptRequest{
		SessionId: sessResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("ping")},
	})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if resp.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want end_turn", resp.StopReason)
	}
	if called.Load() != 1 {
		t.Fatalf("handler called %d times, want 1", called.Load())
	}
}

func TestAgentUnsupportedMethodsReturnMethodNotFound(t *testing.T) {
	a := &Agent{}

	if _, err := a.ListSessions(context.Background(), acp.ListSessionsRequest{}); !isMethodNotFound(err) {
		t.Fatalf("ListSessions err = %v, want method-not-found", err)
	}
	if _, err := a.SetSessionConfigOption(context.Background(), acp.SetSessionConfigOptionRequest{}); !isMethodNotFound(err) {
		t.Fatalf("SetSessionConfigOption err = %v, want method-not-found", err)
	}
	if _, err := a.SetSessionMode(context.Background(), acp.SetSessionModeRequest{}); !isMethodNotFound(err) {
		t.Fatalf("SetSessionMode err = %v, want method-not-found", err)
	}
}

func TestNewPromptHandlerModelOverrideUsesRealConfig(t *testing.T) {
	h := NewPromptHandler(RuntimeConfig{
		Model: "minimax-m2.7:cloud",
		LoadConfig: func() (config.Config, error) {
			return config.Config{Roles: map[string]config.RoleConfig{
				"default": {Model: "gpt-5.4"},
			}}, nil
		},
	})
	res, err := h(context.Background(), PromptTurn{Prompt: "hello", CWD: t.TempDir()})
	if err != nil {
		if strings.Contains(err.Error(), "echo:") {
			t.Fatalf("handler fell back to echo stub: %v", err)
		}
		return
	}
	if strings.Contains(res.FinalText, "echo: hello") {
		t.Fatalf("handler returned echo stub output %q; want real runtime path", res.FinalText)
	}
}

func isMethodNotFound(err error) bool {
	var re *acp.RequestError
	if errors.As(err, &re) {
		return re.Code == -32601
	}
	return false
}

func TestServeRejectsMissingConfig(t *testing.T) {
	if err := Serve(context.Background(), ServeConfig{}); err == nil {
		t.Fatal("expected error when agent is missing")
	}
	if err := Serve(context.Background(), ServeConfig{Agent: &Agent{}}); err == nil {
		t.Fatal("expected error when streams are missing")
	}
}
