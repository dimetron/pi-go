package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Result statuses, mirroring internal/acp so the subagent dispatcher can treat
// codex and ACP results the same way.
const (
	StatusSuccess = "success"
	StatusError   = "error"
)

// Event types emitted by a Session.
const (
	EventTypeMessage  = "message"  // agent text output
	EventTypeProgress = "progress" // reasoning / thinking
	EventTypeTool     = "tool"     // command execution, file changes, tool calls
	EventTypeStderr   = "stderr"   // one line of app-server stderr
	EventTypeError    = "error"    // server-reported error
)

// Event is one streamed update from a codex turn.
type Event struct {
	Type      string `json:"type"`
	Content   string `json:"content,omitempty"`
	Error     string `json:"error,omitempty"`
	SessionID string `json:"session_id,omitempty"` // codex thread ID
}

// RunResult is the terminal outcome of a codex turn.
type RunResult struct {
	Status     string `json:"status"`
	Result     string `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
	SessionID  string `json:"session_id,omitempty"`  // codex thread ID
	Stderr     string `json:"stderr,omitempty"`      // captured app-server stderr
	StopReason string `json:"stop_reason,omitempty"` // turn status: completed/interrupted/failed
}

// eventBufferSize bounds the event channel. Sends are best-effort — see emit.
const eventBufferSize = 256

// SessionOpts configures a single codex turn.
type SessionOpts struct {
	CWD     string   // working directory for the thread
	Prompt  string   // task prompt (ignored when Review is set; review has no prompt)
	Sandbox string   // SandboxReadOnly or SandboxWorkspaceWrite
	Env     []string // full environment for the app-server subprocess
	Review  bool     // run review/start instead of turn/start
	Command []string // command override; used verbatim (tests)
}

// Session runs one turn (or review) against a `codex app-server` subprocess
// and streams the result back as Events.
//
// All events are emitted by a single goroutine (loop), which also owns the
// terminal transition, so the events channel is closed exactly once and never
// written to afterwards.
type Session struct {
	client   *Client
	threadID string

	events chan Event
	done   chan struct{}

	mu     sync.Mutex
	turnID string
	result RunResult
}

// NewSession spawns the app-server, opens a thread and starts the turn.
//
// The notification handler is running before turn/start is sent: codex emits
// turn/started (and can emit the first items) as soon as the request lands, so
// a handler started afterwards would miss them.
func NewSession(ctx context.Context, opts SessionOpts) (*Session, error) {
	if strings.TrimSpace(opts.Prompt) == "" && !opts.Review {
		return nil, fmt.Errorf("prompt is required")
	}
	sandbox := opts.Sandbox
	if sandbox == "" {
		sandbox = SandboxReadOnly
	}

	client, err := NewClient(ctx, ClientOpts{CWD: opts.CWD, Env: opts.Env, Command: opts.Command})
	if err != nil {
		return nil, err
	}
	opts.Sandbox = sandbox
	return startSession(ctx, client, opts)
}

// startSession opens the thread on an initialized client and starts the turn.
// Split out from NewSession so the turn lifecycle can be driven against a fake
// app-server without spawning a subprocess.
func startSession(ctx context.Context, client *Client, opts SessionOpts) (*Session, error) {
	raw, err := client.request(ctx, MethodThreadStart, ThreadStartParams{
		CWD:            opts.CWD,
		ApprovalPolicy: ApprovalNever,
		Sandbox:        opts.Sandbox,
		ServiceName:    "pi-go-codex-subagent",
		Ephemeral:      true,
	})
	if err != nil {
		_ = client.close()
		return nil, err
	}
	var threadResp ThreadStartResponse
	if err := json.Unmarshal(raw, &threadResp); err != nil {
		_ = client.close()
		return nil, fmt.Errorf("codex thread/start response: %w", err)
	}
	if threadResp.Thread.ID == "" {
		_ = client.close()
		return nil, fmt.Errorf("codex thread/start returned no thread id")
	}

	s := newSession(client, threadResp.Thread.ID)

	// Handler first, turn/start second — see the doc comment.
	started := make(chan startResult, 1)
	go func() { started <- s.startTurn(ctx, opts) }()
	go s.loop(ctx, started)

	return s, nil
}

// startResult is what startTurn reports back to loop: either an error, or the
// turn returned by turn/start (which may already carry a terminal status —
// see isTerminalTurnStatus).
type startResult struct {
	err  error
	turn *Turn
}

func newSession(client *Client, threadID string) *Session {
	return &Session{
		client:   client,
		threadID: threadID,
		events:   make(chan Event, eventBufferSize),
		done:     make(chan struct{}),
	}
}

// startTurn issues turn/start (or review/start) and records the turn ID that
// Cancel needs to interrupt it. The returned startResult carries the turn as
// reported by the start response, so the caller can detect a turn that is
// already terminal (completed/failed/interrupted) without waiting on a
// turn/completed notification that will never arrive.
func (s *Session) startTurn(ctx context.Context, opts SessionOpts) startResult {
	if opts.Review {
		raw, err := s.client.request(ctx, MethodReviewStart, ReviewStartParams{
			ThreadID: s.threadID,
			Delivery: ReviewDeliveryInline,
			Target:   ReviewTarget{Type: ReviewTargetUncommitted},
		})
		if err != nil {
			return startResult{err: err}
		}
		var resp ReviewStartResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return startResult{err: fmt.Errorf("codex review/start response: %w", err)}
		}
		s.setTurnID(resp.Turn.ID)
		return startResult{turn: &resp.Turn}
	}

	raw, err := s.client.request(ctx, MethodTurnStart, TurnStartParams{
		ThreadID: s.threadID,
		Input:    []UserInput{{Type: "text", Text: opts.Prompt, TextElements: []any{}}},
	})
	if err != nil {
		return startResult{err: err}
	}
	var resp TurnStartResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return startResult{err: fmt.Errorf("codex turn/start response: %w", err)}
	}
	s.setTurnID(resp.Turn.ID)
	return startResult{turn: &resp.Turn}
}

// isTerminalTurnStatus reports whether a turn status means the turn is over
// and no further turn/completed notification should be expected for it.
func isTerminalTurnStatus(status string) bool {
	switch status {
	case TurnCompleted, TurnFailed, TurnInterrupted:
		return true
	default:
		return false
	}
}

// Events returns the streaming event channel. It is closed when the turn ends.
func (s *Session) Events() <-chan Event { return s.events }

// Done is closed when the turn ends.
func (s *Session) Done() <-chan struct{} { return s.done }

// Wait blocks until the turn ends and returns its result.
func (s *Session) Wait() RunResult {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result
}

// Cancel interrupts the running turn and terminates the app-server.
//
// The interrupt is sent first so codex gets a chance to unwind a running
// command, but Cancel does not wait for it to be acknowledged: the caller may
// hold a lock, and the kill that follows is the real backstop. The loop
// observes the client closing and finishes the session.
func (s *Session) Cancel() error {
	select {
	case <-s.done:
		return nil
	default:
	}

	if turnID := s.TurnID(); turnID != "" {
		_ = s.client.requestNoWait(MethodTurnInterrupt, TurnInterruptParams{
			ThreadID: s.threadID,
			TurnID:   turnID,
		})
	}
	return s.client.close()
}

// TurnID returns the ID of the in-flight turn, or "" before turn/start returns.
func (s *Session) TurnID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turnID
}

func (s *Session) setTurnID(id string) {
	s.mu.Lock()
	s.turnID = id
	s.mu.Unlock()
}

// loop is the session's single owner goroutine: it translates notifications
// into events, accumulates the final answer, and performs the one terminal
// transition. Every exit path funnels through finish.
func (s *Session) loop(ctx context.Context, started <-chan startResult) {
	var text strings.Builder
	var lastErr string

	notifs := s.client.notifications()
	stderrLines := s.client.stderrLines()

	for {
		select {
		case sr := <-started:
			// turn/start failed — nothing will ever complete this turn.
			if sr.err != nil {
				s.finish(RunResult{Status: StatusError, Error: sr.err.Error(), Result: text.String()})
				return
			}
			// The start response may already report a terminal status (the
			// app-server protocol allows this); if so, no turn/completed
			// notification will follow and we must not wait for one.
			if sr.turn != nil && isTerminalTurnStatus(sr.turn.Status) {
				s.finish(s.completedResult(*sr.turn, text.String(), lastErr))
				return
			}

		case line, ok := <-stderrLines:
			if !ok {
				stderrLines = nil
				continue
			}
			s.emit(Event{Type: EventTypeStderr, Content: line, SessionID: s.threadID})

		case notif, ok := <-notifs:
			if !ok {
				// stdout closed: the app-server is going away without having
				// completed the turn. Don't conclude anything yet — wait for
				// the exited signal, which is only raised once stderr has been
				// drained too, so the crash report carries the reason.
				notifs = nil
				continue
			}
			done, result := s.handleNotification(notif, &text, &lastErr)
			if done {
				s.finish(result)
				return
			}

		case <-s.client.closing:
			// Cancel. Don't wait for the killed subprocess to hit EOF — the
			// caller wants the session gone now.
			s.finish(RunResult{
				Status:     StatusError,
				Error:      "codex turn canceled",
				Result:     text.String(),
				SessionID:  s.threadID,
				StopReason: TurnInterrupted,
			})
			return

		case <-s.client.exited:
			// The subprocess is gone. Drain what is still buffered before
			// concluding it crashed: a turn/completed (or the error that
			// explains the exit) may already be queued behind this signal.
			if done, result := s.drainRemaining(&text, &lastErr); done {
				s.finish(result)
				return
			}
			s.finish(s.crashResult(text.String(), lastErr))
			return

		case <-ctx.Done():
			s.finish(RunResult{
				Status:    StatusError,
				Error:     ctx.Err().Error(),
				Result:    text.String(),
				SessionID: s.threadID,
				Stderr:    s.client.stderrText(),
			})
			return
		}
	}
}

// drainRemaining consumes what is still buffered on the notification and
// stderr channels after the subprocess exits. It reports whether a terminal
// turn/completed was among them, so a turn that finished just as the process
// went away is not misreported as a crash.
func (s *Session) drainRemaining(text *strings.Builder, lastErr *string) (bool, RunResult) {
	notifs := s.client.notifications()
	stderrLines := s.client.stderrLines()
	for {
		select {
		case line, ok := <-stderrLines:
			if !ok {
				stderrLines = nil
				continue
			}
			s.emit(Event{Type: EventTypeStderr, Content: line, SessionID: s.threadID})
		case notif, ok := <-notifs:
			if !ok {
				return false, RunResult{}
			}
			if done, result := s.handleNotification(notif, text, lastErr); done {
				return true, result
			}
		default:
			return false, RunResult{}
		}
	}
}

// handleNotification translates one notification into events. It reports
// whether the notification ends the turn, and if so, with what result.
func (s *Session) handleNotification(notif JSONRPCNotification, text *strings.Builder, lastErr *string) (bool, RunResult) {
	switch notif.Method {
	case NotifyTurnCompleted:
		var p TurnCompletedParams
		if err := json.Unmarshal(notif.Params, &p); err != nil {
			return false, RunResult{}
		}
		// Codex spins up child threads for collab/subagent work. Only the
		// completion of *our* thread ends the session; a child's completion is
		// just progress.
		if p.ThreadID != s.threadID {
			s.emit(Event{Type: EventTypeProgress, Content: "codex subagent thread completed", SessionID: s.threadID})
			return false, RunResult{}
		}
		return true, s.completedResult(p.Turn, text.String(), *lastErr)

	case NotifyError:
		var p ErrorParams
		if err := json.Unmarshal(notif.Params, &p); err != nil {
			return false, RunResult{}
		}
		if msg := strings.TrimSpace(p.Error.Message); msg != "" {
			*lastErr = msg
			s.emit(Event{Type: EventTypeError, Error: msg, SessionID: s.threadID})
		}
		return false, RunResult{}

	case NotifyItemStarted, NotifyItemCompleted:
		var p ItemParams
		if err := json.Unmarshal(notif.Params, &p); err != nil {
			return false, RunResult{}
		}
		// Codex spins up child threads for collab/subagent work (see the
		// turn/completed guard above). Items from those child threads must
		// not be streamed as parent output either.
		if p.ThreadID != s.threadID {
			return false, RunResult{}
		}
		s.handleItem(p.Item, notif.Method == NotifyItemCompleted, text)
		return false, RunResult{}

	default:
		// thread/started, turn/started and anything else the app-server adds
		// later carry no content pi-go renders.
		return false, RunResult{}
	}
}

// handleItem emits the events for one thread item. Agent text is accumulated
// into the result only on completion — item/started for an agentMessage repeats
// the same text, and counting both would duplicate the answer.
func (s *Session) handleItem(item Item, completed bool, text *strings.Builder) {
	switch item.Type {
	case ItemAgentMessage:
		if strings.TrimSpace(item.Text) == "" {
			return
		}
		s.emit(Event{Type: EventTypeMessage, Content: item.Text, SessionID: s.threadID})
		if completed {
			text.WriteString(item.Text)
		}

	case ItemExitedReviewMode:
		if strings.TrimSpace(item.Review) == "" {
			return
		}
		s.emit(Event{Type: EventTypeMessage, Content: item.Review, SessionID: s.threadID})
		if completed {
			text.WriteString(item.Review)
		}

	case ItemReasoning:
		if !completed {
			return
		}
		if summary := strings.TrimSpace(strings.Join(item.Summary, "\n")); summary != "" {
			s.emit(Event{Type: EventTypeProgress, Content: summary, SessionID: s.threadID})
		}

	case ItemCommandExecution:
		if !completed {
			s.emitTool("Running: " + item.Command)
			return
		}
		if item.ExitCode != nil {
			s.emitTool(fmt.Sprintf("Command completed (exit %d)", *item.ExitCode))
			return
		}
		s.emitTool("Command completed")

	case ItemFileChange:
		if !completed {
			s.emitTool(fmt.Sprintf("Applying %d file changes", len(item.Changes)))
			return
		}
		s.emitTool("File changes completed")

	case ItemMCPToolCall, ItemDynamicToolCall:
		name := item.Tool
		if item.Server != "" {
			name = item.Server + "/" + item.Tool
		}
		if !completed {
			s.emitTool("Calling " + name)
			return
		}
		s.emitTool("Called " + name)

	case ItemWebSearch:
		if !completed {
			s.emitTool("Web search: " + item.Query)
		}
	}
}

func (s *Session) emitTool(content string) {
	s.emit(Event{Type: EventTypeTool, Content: content, SessionID: s.threadID})
}

// completedResult maps a finished turn onto a RunResult.
func (s *Session) completedResult(turn Turn, text, lastErr string) RunResult {
	result := RunResult{
		Result:     text,
		SessionID:  s.threadID,
		Stderr:     s.client.stderrText(),
		StopReason: turn.Status,
	}
	if turn.Status == TurnCompleted {
		result.Status = StatusSuccess
		return result
	}

	result.Status = StatusError
	result.Error = cmpOr(strings.TrimSpace(turn.Error), lastErr, "codex turn "+turn.Status)
	return result
}

// crashResult describes an app-server that exited before completing the turn.
func (s *Session) crashResult(text, lastErr string) RunResult {
	stderr := s.client.stderrText()
	msg := "codex app-server exited before the turn completed"
	if err := s.client.exitError(); err != nil {
		msg = "codex app-server exited: " + err.Error()
	}
	if lastErr != "" {
		msg = msg + ": " + lastErr
	}
	return RunResult{
		Status:    StatusError,
		Error:     msg,
		Result:    text,
		SessionID: s.threadID,
		Stderr:    stderr,
	}
}

// cmpOr returns the first non-empty string. (cmp.Or is generic over comparable
// zero values, which is exactly this, but spelling it out keeps the intent
// obvious at the call site.)
func cmpOr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// emit delivers an event without blocking the loop. Delivery is best-effort:
// if the consumer has fallen behind, the event is dropped rather than stalling
// the app-server's notification stream. The final result is unaffected —
// answer text is accumulated in loop and full stderr in the client.
func (s *Session) emit(ev Event) {
	select {
	case s.events <- ev:
	default:
	}
}

// finish performs the single terminal transition: record the result, close the
// event stream, unblock Wait, and tear down the subprocess. Only loop calls it,
// so events is closed exactly once and never after an emit.
func (s *Session) finish(result RunResult) {
	if result.SessionID == "" {
		result.SessionID = s.threadID
	}
	if result.Stderr == "" {
		result.Stderr = s.client.stderrText()
	}

	s.mu.Lock()
	s.result = result
	s.mu.Unlock()

	close(s.events)
	close(s.done)
	_ = s.client.close()
}
