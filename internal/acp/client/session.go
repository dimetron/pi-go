package client

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"

	shared "github.com/dimetron/pi-go/internal/acp"
)

// RunningSession represents one in-flight ACP turn.
type RunningSession struct {
	cmd    *exec.Cmd
	stdin  io.Closer
	stderr *stderrBuffer
	conn   *acp.ClientSideConnection

	events chan shared.Event
	done   chan struct{}

	closeStdin sync.Once

	mu       sync.Mutex
	result   shared.RunResult
	finished bool
}

func newRunningSession(cmd *exec.Cmd, stdin io.Closer, stderr io.Reader) *RunningSession {
	rs := &RunningSession{
		cmd:    cmd,
		stdin:  stdin,
		stderr: &stderrBuffer{},
		events: make(chan shared.Event, 32),
		done:   make(chan struct{}),
	}
	go rs.stderr.readFrom(stderr)
	return rs
}

func (s *RunningSession) closeStdinOnce() {
	s.closeStdin.Do(func() {
		if s.stdin != nil {
			_ = s.stdin.Close()
		}
	})
}

// Events returns the translated local ACP event stream.
func (s *RunningSession) Events() <-chan shared.Event { return s.events }

// Done is closed when the prompt turn finishes.
func (s *RunningSession) Done() <-chan struct{} { return s.done }

// Cancel terminates the running subprocess.
func (s *RunningSession) Cancel() error {
	s.mu.Lock()
	finished := s.finished
	cmd := s.cmd
	s.mu.Unlock()
	if finished || cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil {
		return fmt.Errorf("kill acp subprocess: %w", err)
	}
	return nil
}

// Wait blocks until the turn completes and returns the final result.
func (s *RunningSession) Wait() shared.RunResult {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result
}

func (s *RunningSession) run(req shared.RunRequest, clientInfo acp.Implementation) {
	defer close(s.done)
	defer close(s.events)
	defer s.closeStdinOnce()

	ctx := context.Background()
	initResp, err := s.conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
		ClientInfo:      &clientInfo,
	})
	if err != nil {
		s.closeStdinOnce()
		s.finish(shared.RunResult{Status: shared.StatusError, Error: fmt.Sprintf("initialize: %v", err)})
		_ = s.waitProcess()
		return
	}

	newSessionResp, err := s.conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        absDir(req.CWD),
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		s.closeStdinOnce()
		s.finish(shared.RunResult{Status: shared.StatusError, Error: fmt.Sprintf("new session: %v", err)})
		_ = s.waitProcess()
		return
	}

	sessionID := string(newSessionResp.SessionId)
	if sessionID == "" {
		sessionID = req.SessionID
	}

	_, _ = initResp, req.SessionID

	promptResp, err := s.conn.Prompt(ctx, acp.PromptRequest{
		SessionId: newSessionResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock(req.Prompt)},
	})
	if err != nil {
		s.closeStdinOnce()
		s.finish(shared.RunResult{Status: shared.StatusError, Error: fmt.Sprintf("prompt: %v", err), SessionID: sessionID})
		_ = s.waitProcess()
		return
	}

	// Signal end-of-stream to the agent so it can exit cleanly; otherwise
	// agents that block on their stdin EOF (e.g. <-agentConn.Done()) would
	// deadlock with our cmd.Wait below.
	s.closeStdinOnce()

	if err := s.waitProcess(); err != nil {
		s.finish(shared.RunResult{Status: shared.StatusError, Error: err.Error(), SessionID: sessionID})
		return
	}

	s.mu.Lock()
	result := s.result
	s.mu.Unlock()
	if strings.TrimSpace(result.Result) == "" {
		result.Result = stopReasonText(promptResp.StopReason)
	}
	result.Status = shared.StatusSuccess
	result.SessionID = sessionID
	s.finish(result)
}

func (s *RunningSession) handleUpdate(notification acp.SessionNotification) {
	sessionID := string(notification.SessionId)
	update := notification.Update

	s.mu.Lock()
	if s.result.SessionID == "" {
		s.result.SessionID = sessionID
	}
	s.mu.Unlock()

	if chunk := update.AgentMessageChunk; chunk != nil {
		text := contentBlockText(chunk.Content)
		if strings.TrimSpace(text) != "" {
			s.emit(shared.Event{Type: shared.EventTypeMessage, Content: text, SessionID: sessionID})
			s.appendResult(text)
		}
	}
	if thought := update.AgentThoughtChunk; thought != nil {
		text := contentBlockText(thought.Content)
		if strings.TrimSpace(text) != "" {
			s.emit(shared.Event{Type: shared.EventTypeProgress, Content: text, SessionID: sessionID})
		}
	}
	if toolCall := update.ToolCall; toolCall != nil {
		if title := strings.TrimSpace(toolCall.Title); title != "" {
			s.emit(shared.Event{Type: shared.EventTypeTool, Content: title, SessionID: sessionID})
		}
	}
	if toolUpdate := update.ToolCallUpdate; toolUpdate != nil {
		if title := toolUpdate.Title; title != nil && strings.TrimSpace(*title) != "" {
			s.emit(shared.Event{Type: shared.EventTypeTool, Content: *title, SessionID: sessionID})
		}
	}
}

func (s *RunningSession) appendResult(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.result.Result += text
}

func (s *RunningSession) emit(event shared.Event) {
	select {
	case s.events <- event:
	default:
	}
}

func (s *RunningSession) finish(result shared.RunResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		if result.Status == shared.StatusSuccess && strings.TrimSpace(s.result.Result) == "" {
			s.result.Result = result.Result
		}
		if s.result.SessionID == "" {
			s.result.SessionID = result.SessionID
		}
		return
	}
	if strings.TrimSpace(result.Error) == "" {
		result.Error = strings.TrimSpace(s.stderr.String())
	}
	s.result = result
	s.finished = true
}

func (s *RunningSession) waitProcess() error {
	err := s.cmd.Wait()
	if err == nil {
		return nil
	}
	stderr := strings.TrimSpace(s.stderr.String())
	if stderr == "" {
		return fmt.Errorf("acp subprocess: %w", err)
	}
	return fmt.Errorf("acp subprocess: %w: %s", err, stderr)
}

func contentBlockText(block acp.ContentBlock) string {
	if block.Text != nil {
		return block.Text.Text
	}
	if block.ResourceLink != nil {
		return block.ResourceLink.Uri
	}
	return ""
}

func stopReasonText(reason acp.StopReason) string {
	if strings.TrimSpace(string(reason)) == "" {
		return ""
	}
	return string(reason)
}
