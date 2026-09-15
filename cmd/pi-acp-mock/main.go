// Package main is a mock ACP server for testing pi-go's ACP integration and
// client tooling (VS Code extension, editor plugins). It implements a small
// but complete ACP agent surface — including tool_call lifecycles, thought
// chunks, available commands, and an in-memory session store with load replay
// — so closed-loop tests can exercise rich client rendering without an LLM.
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
//	PI_MOCK_TOOLS    - When "1", each prompt emits a tool_call lifecycle
//	                   (pending → in_progress → completed with text content).
//	PI_MOCK_TOOLS_FAIL - When "1" (takes precedence over PI_MOCK_TOOLS), the
//	                   tool call ends in status=failed instead.
//	PI_MOCK_THOUGHTS - When "1", each prompt emits an agent_thought_chunk
//	                   before the reply.
//	PI_MOCK_COMMANDS - When "1", new sessions advertise available commands
//	                   (clear, compact, help) via available_commands_update.
//	PI_MOCK_ECHO_RESOURCE - When "1", resource blocks in the prompt are echoed
//	                   back inside the reply (to verify @file mentions).
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

type mockSession struct {
	id        acp.SessionId
	cwd       string
	createdAt time.Time
	// transcript records what happened in order, replayed by LoadSession.
	transcript []mockChunk
}

type mockChunk struct {
	role    string // "user" | "agent"
	text    string
	thought bool
}

// sessionUpdater is the slice of AgentSideConnection the mock uses; an
// interface so tests can inject a recorder.
type sessionUpdater interface {
	SessionUpdate(ctx context.Context, n acp.SessionNotification) error
}

type mockAgent struct {
	responseText   string
	delay          time.Duration
	emitTools      bool
	failTools      bool
	emitThoughts   bool
	emitCommands   bool
	echoResources  bool
	conn           sessionUpdater
	mu             sync.Mutex
	sessions       map[acp.SessionId]*mockSession
	nextToolCallID int
}

func (m *mockAgent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

func (m *mockAgent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}

func (m *mockAgent) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
		AgentInfo:       &acp.Implementation{Name: "pi-acp-mock", Version: "1.1"},
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession: true,
			SessionCapabilities: acp.SessionCapabilities{
				List: &acp.SessionListCapabilities{},
			},
			PromptCapabilities: acp.PromptCapabilities{EmbeddedContext: true},
		},
	}, nil
}

func (m *mockAgent) NewSession(_ context.Context, params acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	s := &mockSession{
		id:        acp.SessionId(fmt.Sprintf("mock-%d", os.Getpid())),
		cwd:       params.Cwd,
		createdAt: time.Now(),
	}
	m.mu.Lock()
	if m.sessions == nil {
		m.sessions = map[acp.SessionId]*mockSession{}
	}
	s.id = acp.SessionId(fmt.Sprintf("mock-%d-%d", os.Getpid(), len(m.sessions)))
	m.sessions[s.id] = s
	m.mu.Unlock()

	// Advertise commands for the new session when asked to.
	if m.emitCommands && m.conn != nil {
		_ = m.conn.SessionUpdate(context.Background(), acp.SessionNotification{
			SessionId: s.id,
			Update: acp.SessionUpdate{AvailableCommandsUpdate: &acp.SessionAvailableCommandsUpdate{
				AvailableCommands: []acp.AvailableCommand{
					{Name: "clear", Description: "Clear the session"},
					{Name: "compact", Description: "Compact the session history"},
					{Name: "help", Description: "Show help"},
				},
			}},
		})
	}
	return acp.NewSessionResponse{SessionId: s.id}, nil
}

func (m *mockAgent) ResumeSession(_ context.Context, _ acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, nil
}

func (m *mockAgent) LoadSession(ctx context.Context, params acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	m.mu.Lock()
	s := m.sessions[params.SessionId]
	chunks := append([]mockChunk(nil), s.transcript...)
	m.mu.Unlock()
	if s == nil {
		return acp.LoadSessionResponse{}, fmt.Errorf("unknown session %s", params.SessionId)
	}
	// Replay the recorded transcript as user/agent message chunks.
	for _, c := range chunks {
		if m.conn == nil {
			continue
		}
		var upd acp.SessionUpdate
		if c.role == "user" {
			upd = acp.UpdateUserMessageText(c.text)
		} else {
			upd = acp.UpdateAgentMessageText(c.text)
		}
		_ = m.conn.SessionUpdate(ctx, acp.SessionNotification{
			SessionId: params.SessionId,
			Update:    upd,
		})
	}
	return acp.LoadSessionResponse{}, nil
}

func (m *mockAgent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	promptText := extractText(params.Prompt)
	resourceEcho := extractResources(params.Prompt, m.echoResources)

	if m.delay > 0 {
		select {
		case <-ctx.Done():
			return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, ctx.Err()
		case <-time.After(m.delay):
		}
	}

	log.Printf("received prompt: %s", truncate(promptText, 100))

	response := strings.ReplaceAll(m.responseText, "{{prompt}}", promptText)
	if resourceEcho != "" {
		response = resourceEcho + "\n\n" + response
	}

	m.mu.Lock()
	m.nextToolCallID++
	toolCallID := acp.ToolCallId(fmt.Sprintf("tool-%d", m.nextToolCallID))
	s := m.sessions[params.SessionId]
	m.mu.Unlock()

	if m.conn != nil {
		ctx := context.Background()
		if m.emitThoughts {
			_ = m.conn.SessionUpdate(ctx, acp.SessionNotification{
				SessionId: params.SessionId,
				Update:    acp.UpdateAgentThoughtText("thinking about: " + truncate(promptText, 60)),
			})
		}
		if m.emitTools || m.failTools {
			m.emitToolLifecycle(ctx, params.SessionId, toolCallID, m.failTools)
		}
		_ = m.conn.SessionUpdate(ctx, acp.SessionNotification{
			SessionId: params.SessionId,
			Update:    acp.UpdateAgentMessageText(response),
		})
	}

	m.mu.Lock()
	if s != nil {
		if resourceEcho != "" {
			s.transcript = append(s.transcript, mockChunk{role: "user", text: resourceEcho})
		}
		s.transcript = append(s.transcript, mockChunk{role: "user", text: promptText})
		if m.emitThoughts {
			s.transcript = append(s.transcript, mockChunk{role: "agent", thought: true, text: "thinking about: " + truncate(promptText, 60)})
		}
		s.transcript = append(s.transcript, mockChunk{role: "agent", text: response})
	}
	m.mu.Unlock()

	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

// emitToolLifecycle walks one tool call through pending → in_progress →
// terminal status, attaching content on the final update.
func (m *mockAgent) emitToolLifecycle(ctx context.Context, session acp.SessionId, id acp.ToolCallId, fail bool) {
	if m.conn == nil {
		return
	}
	send := func(upd acp.SessionUpdate) {
		_ = m.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: session, Update: upd})
	}
	send(acp.StartToolCall(id, "Read example.go",
		acp.WithStartKind(acp.ToolKindRead),
		acp.WithStartStatus(acp.ToolCallStatusPending),
		acp.WithStartRawInput(map[string]any{"path": "example.go"}),
	))
	send(acp.UpdateToolCall(id,
		acp.WithUpdateStatus(acp.ToolCallStatusInProgress),
	))
	if fail {
		send(acp.UpdateToolCall(id,
			acp.WithUpdateStatus(acp.ToolCallStatusFailed),
			acp.WithUpdateContent([]acp.ToolCallContent{{Content: &acp.ToolCallContentContent{
				Type:    "content",
				Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: "file not found"}},
			}}}),
		))
		return
	}
	send(acp.UpdateToolCall(id,
		acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
		acp.WithUpdateContent([]acp.ToolCallContent{{Content: &acp.ToolCallContentContent{
			Type:    "content",
			Content: acp.ContentBlock{Text: &acp.ContentBlockText{Text: "package main\n\nfunc main() {}"}},
		}}}),
		acp.WithUpdateRawOutput(map[string]any{"text": "package main"}),
	))
}

func (m *mockAgent) Cancel(context.Context, acp.CancelNotification) error {
	return nil
}

func (m *mockAgent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, nil
}

func (m *mockAgent) ListSessions(_ context.Context, _ acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	m.mu.Lock()
	sessions := make([]*mockSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.Unlock()
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].createdAt.After(sessions[j].createdAt) })
	out := make([]acp.SessionInfo, 0, len(sessions))
	for _, s := range sessions {
		title := s.firstUserLine()
		info := acp.SessionInfo{
			SessionId: s.id,
			Cwd:       s.cwd,
		}
		if title != "" {
			info.Title = acp.Ptr(title)
		}
		updated := s.createdAt.Format(time.RFC3339)
		info.UpdatedAt = acp.Ptr(updated)
		out = append(out, info)
	}
	return acp.ListSessionsResponse{Sessions: out}, nil
}

func (s *mockSession) firstUserLine() string {
	for _, c := range s.transcript {
		if c.role == "user" {
			line := strings.SplitN(strings.TrimSpace(c.text), "\n", 2)[0]
			if len(line) > 60 {
				return line[:60]
			}
			return line
		}
	}
	return ""
}

func (m *mockAgent) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, nil
}

func (m *mockAgent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}

// newAgentFromEnv builds the mock agent from its environment knobs. Split out of
// main so the configuration can be exercised without opening a connection and
// blocking on it forever.
func newAgentFromEnv() *mockAgent {
	return &mockAgent{
		responseText:  getEnv("PI_MOCK_RESPONSE", "Mock ACP response"),
		delay:         parseDelay(os.Getenv("PI_MOCK_DELAY_MS")),
		emitTools:     getEnv("PI_MOCK_TOOLS", "") == "1",
		failTools:     getEnv("PI_MOCK_TOOLS_FAIL", "") == "1",
		emitThoughts:  getEnv("PI_MOCK_THOUGHTS", "") == "1",
		emitCommands:  getEnv("PI_MOCK_COMMANDS", "") == "1",
		echoResources: getEnv("PI_MOCK_ECHO_RESOURCE", "") == "1",
		sessions:      map[acp.SessionId]*mockSession{},
	}
}

func main() {
	log.SetOutput(os.Stderr)
	log.SetPrefix("[pi-acp-mock] ")
	serve(os.Stdout, os.Stdin)
}

// serve runs the mock agent over the given streams until the peer closes the
// connection. Taking the streams as parameters (rather than reaching for
// os.Stdout/os.Stdin) lets the shutdown path be exercised with a pipe.
func serve(out io.Writer, in io.Reader) {
	agent := newAgentFromEnv()
	log.Printf("starting: response=%q delay=%s", truncate(agent.responseText, 50), agent.delay)

	conn := acp.NewAgentSideConnection(agent, out, in)
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

// extractResources renders embedded resource blocks as "[file] uri" lines;
// empty unless echoResources is on.
func extractResources(blocks []acp.ContentBlock, enabled bool) string {
	if !enabled {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Resource == nil {
			continue
		}
		if rc := b.Resource.Resource.TextResourceContents; rc != nil {
			parts = append(parts, fmt.Sprintf("[file] %s (%d bytes)", rc.Uri, len(rc.Text)))
		} else {
			parts = append(parts, fmt.Sprintf("[file] %s", b.Resource.Resource.BlobResourceContents.Uri))
		}
	}
	return strings.Join(parts, "\n")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
