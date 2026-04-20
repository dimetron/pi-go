package client

import (
	"bufio"
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
	// stderrDone closes once streamStderr returns so waitProcess can synchronize
	// with the drain goroutine before reading stderr.
	stderrDone chan struct{}

	closeStdin sync.Once

	mu         sync.Mutex
	result     shared.RunResult
	finished   bool
	toolFilter *shared.ToolCallTitleFilter
	curSession string
}

func newRunningSession(cmd *exec.Cmd, stdin io.Closer, stderr io.Reader) *RunningSession {
	rs := &RunningSession{
		cmd:        cmd,
		stdin:      stdin,
		stderr:     &stderrBuffer{},
		events:     make(chan shared.Event, 32),
		done:       make(chan struct{}),
		stderrDone: make(chan struct{}),
	}
	rs.toolFilter = shared.NewToolCallTitleFilter(func(title string) {
		rs.mu.Lock()
		sid := rs.curSession
		rs.mu.Unlock()
		rs.emit(shared.Event{Type: shared.EventTypeTool, Content: title, SessionID: sid})
	})
	go rs.streamStderr(stderr)
	return rs
}

func (s *RunningSession) streamStderr(r io.Reader) {
	defer close(s.stderrDone)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = s.stderr.Write([]byte(line + "\n"))
		if strings.TrimSpace(line) == "" {
			continue
		}
		s.mu.Lock()
		sid := s.curSession
		s.mu.Unlock()
		s.emit(shared.Event{Type: shared.EventTypeStderr, Content: line, SessionID: sid})
	}
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

// RunACPFlow executes the shared ACP initialize/new-session/prompt lifecycle
// against an already-started client connection, delegating state/result details
// back to the caller.
func RunACPFlow(
	ctx context.Context,
	conn *acp.ClientSideConnection,
	req shared.RunRequest,
	clientInfo acp.Implementation,
	handleSessionID func(string),
	handleUpdate func(acp.SessionNotification),
	finish func(shared.RunResult),
	waitProcess func() error,
	closeStdin func(),
	readResult func() shared.RunResult,
) {
	initCtx := ctx
	initCancel := func() {}
	if req.RPCTimeout > 0 {
		initCtx, initCancel = context.WithTimeout(ctx, req.RPCTimeout)
	}
	initResp, err := conn.Initialize(initCtx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
		ClientInfo:      &clientInfo,
	})
	initCancel()
	if err != nil {
		closeStdin()
		finish(shared.RunResult{Status: shared.StatusError, Error: fmt.Sprintf("initialize: %v", err)})
		_ = waitProcess()
		return
	}

	newCtx := ctx
	newCancel := func() {}
	if req.RPCTimeout > 0 {
		newCtx, newCancel = context.WithTimeout(ctx, req.RPCTimeout)
	}
	newSessionResp, err := conn.NewSession(newCtx, acp.NewSessionRequest{
		Cwd:        absDir(req.CWD),
		McpServers: []acp.McpServer{},
	})
	newCancel()
	if err != nil {
		closeStdin()
		finish(shared.RunResult{Status: shared.StatusError, Error: fmt.Sprintf("new session: %v", err)})
		_ = waitProcess()
		return
	}

	sessionID := string(newSessionResp.SessionId)
	if sessionID == "" {
		sessionID = req.SessionID
	}
	if handleSessionID != nil {
		handleSessionID(sessionID)
	}

	_, _ = initResp, handleUpdate

	promptResp, err := conn.Prompt(ctx, acp.PromptRequest{
		SessionId: newSessionResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock(req.Prompt)},
	})
	if err != nil {
		closeStdin()
		finish(shared.RunResult{Status: shared.StatusError, Error: fmt.Sprintf("prompt: %v", err), SessionID: sessionID})
		_ = waitProcess()
		return
	}

	closeStdin()

	if err := waitProcess(); err != nil {
		finish(shared.RunResult{Status: shared.StatusError, Error: err.Error(), SessionID: sessionID})
		return
	}

	result := readResult()
	result.Status = shared.StatusSuccess
	result.SessionID = sessionID
	if stop := promptResp.StopReason; strings.TrimSpace(string(stop)) != "" {
		result.StopReason = string(stop)
	}
	finish(result)
}

func (s *RunningSession) run(req shared.RunRequest, clientInfo acp.Implementation) {
	defer close(s.done)
	defer close(s.events)
	defer s.closeStdinOnce()

	RunACPFlow(
		context.Background(),
		s.conn,
		req,
		clientInfo,
		func(sessionID string) {
			s.mu.Lock()
			s.curSession = sessionID
			s.mu.Unlock()
		},
		s.handleUpdate,
		s.finish,
		s.waitProcess,
		s.closeStdinOnce,
		func() shared.RunResult {
			s.mu.Lock()
			defer s.mu.Unlock()
			return s.result
		},
	)
}

func (s *RunningSession) handleUpdate(notification acp.SessionNotification) {
	sessionID := string(notification.SessionId)
	update := notification.Update

	s.mu.Lock()
	if s.result.SessionID == "" {
		s.result.SessionID = sessionID
	}
	if sessionID != "" {
		s.curSession = sessionID
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
		s.toolFilter.OnToolCall(
			string(toolCall.ToolCallId),
			shared.EnrichToolCallTitle(toolCall.Title, toolCall.RawInput),
		)
	}
	if toolUpdate := update.ToolCallUpdate; toolUpdate != nil {
		title := ""
		if toolUpdate.Title != nil {
			title = *toolUpdate.Title
		}
		s.toolFilter.OnToolCallUpdate(
			string(toolUpdate.ToolCallId),
			shared.EnrichToolCallTitle(title, toolUpdate.RawInput),
		)
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
	if s.result.Stderr == "" {
		s.result.Stderr = strings.TrimSpace(s.stderr.String())
	}
	if strings.TrimSpace(result.Error) == "" {
		result.Error = strings.TrimSpace(s.stderr.String())
	}
	s.result = result
	s.finished = true
}

func (s *RunningSession) waitProcess() error {
	<-s.stderrDone
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
