package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

// PromptHandler runs one ACP prompt turn for a session. The skeleton uses
// EchoPromptHandler; the real pi runtime is plugged in behind this seam by
// later slices.
type PromptHandler func(ctx context.Context, turn PromptTurn) (PromptResult, error)

// PromptTurn is the adapter input handed to a PromptHandler.
type PromptTurn struct {
	SessionID string
	CWD       string
	Prompt    string
	Updater   SessionUpdater
}

// PromptResult is the adapter output returned from a PromptHandler.
type PromptResult struct {
	FinalText  string
	StopReason acp.StopReason
}

// SessionUpdater streams session updates back to the ACP peer. A nil Updater
// silently discards updates so PromptHandlers work both with and without a
// live connection (tests vs. live serve).
type SessionUpdater interface {
	Update(ctx context.Context, update acp.SessionUpdate) error
}

// Agent implements acp.Agent for pi. The zero value is a usable skeleton that
// accepts prompts and echoes them back through EchoPromptHandler.
type Agent struct {
	// AgentInfo advertises pi to the peer during initialize.
	AgentInfo acp.Implementation
	// Handler processes one prompt turn. If nil, EchoPromptHandler is used.
	Handler PromptHandler

	mu       sync.Mutex
	conn     *acp.AgentSideConnection
	sessions map[string]*sessionState
}

type sessionState struct {
	cwd    string
	cancel context.CancelFunc
}

var _ acp.Agent = (*Agent)(nil)

// SetAgentConnection wires the agent-side connection after construction so
// Prompt handlers can stream updates. Called from Serve; tests may skip it.
func (a *Agent) SetAgentConnection(conn *acp.AgentSideConnection) {
	a.mu.Lock()
	a.conn = conn
	a.mu.Unlock()
}

func (a *Agent) info() acp.Implementation {
	if strings.TrimSpace(a.AgentInfo.Name) != "" {
		return a.AgentInfo
	}
	return acp.Implementation{Name: "pi-go", Version: "dev"}
}

func (a *Agent) handler() PromptHandler {
	a.mu.Lock()
	h := a.Handler
	a.mu.Unlock()
	if h == nil {
		return EchoPromptHandler
	}
	return h
}

// Authenticate always succeeds; pi does not require ACP-level authentication.
func (a *Agent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

// Initialize advertises pi's baseline capabilities. EmbeddedContext is on so
// Zed can inline file context; Image defaults to false until zed-09 wires the
// provider gate.
func (a *Agent) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	info := a.info()
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
		AgentInfo:       &info,
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession:        true,
			PromptCapabilities: acp.PromptCapabilities{EmbeddedContext: true},
		},
	}, nil
}

// NewSession registers a new session and returns its identifier.
func (a *Agent) NewSession(_ context.Context, params acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	sid := randomSessionID()
	a.mu.Lock()
	if a.sessions == nil {
		a.sessions = make(map[string]*sessionState)
	}
	a.sessions[sid] = &sessionState{cwd: params.Cwd}
	a.mu.Unlock()
	return acp.NewSessionResponse{SessionId: acp.SessionId(sid)}, nil
}

// Prompt bridges a prompt request through the configured handler. Handlers
// own all session-update emission through turn.Updater; Prompt itself does
// not emit an AgentMessageText summary, avoiding the duplicate-message bug.
// The handler runs under a cancelable context registered on the session so
// an inbound ACP Cancel notification can abort the in-flight turn. When the
// request carries a MessageId it is echoed back as UserMessageId.
func (a *Agent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	sid := string(params.SessionId)
	a.mu.Lock()
	state, ok := a.sessions[sid]
	a.mu.Unlock()
	if !ok {
		return acp.PromptResponse{}, fmt.Errorf("session %s not found", sid)
	}

	promptCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	a.mu.Lock()
	state.cancel = cancel
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		state.cancel = nil
		a.mu.Unlock()
	}()

	updater := a.updater(acp.SessionId(sid))
	turn := PromptTurn{
		SessionID: sid,
		CWD:       state.cwd,
		Prompt:    extractPromptText(params.Prompt),
		Updater:   updater,
	}
	result, err := a.handler()(promptCtx, turn)
	if promptCtx.Err() != nil {
		resp := acp.PromptResponse{StopReason: acp.StopReasonCancelled}
		if params.MessageId != nil {
			mid := *params.MessageId
			resp.UserMessageId = &mid
		}
		return resp, nil
	}
	if err != nil {
		return acp.PromptResponse{}, err
	}
	stop := result.StopReason
	if strings.TrimSpace(string(stop)) == "" {
		stop = acp.StopReasonEndTurn
	}
	resp := acp.PromptResponse{StopReason: stop}
	if params.MessageId != nil {
		mid := *params.MessageId
		resp.UserMessageId = &mid
	}
	return resp, nil
}

// ListSessions is not yet supported; advertise method-not-found so clients
// can detect capability absence.
func (a *Agent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionList)
}

// SetSessionConfigOption is not yet supported.
func (a *Agent) SetSessionConfigOption(context.Context, acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionSetConfigOption)
}

// SetSessionMode is not yet supported; Slice 10 wires mode flows.
func (a *Agent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionSetMode)
}

// EchoPromptHandler is the default PromptHandler used when none is configured.
// It streams a deterministic "echo: <prompt>" reply through the turn's
// updater and returns the same text as PromptResult.FinalText. The stream is
// the sole emission surface so Prompt no longer needs to re-send FinalText.
func EchoPromptHandler(ctx context.Context, turn PromptTurn) (PromptResult, error) {
	reply := "echo: " + turn.Prompt
	if turn.Updater != nil {
		if err := turn.Updater.Update(ctx, acp.UpdateAgentMessageText(reply)); err != nil {
			return PromptResult{}, fmt.Errorf("echo session update: %w", err)
		}
	}
	return PromptResult{
		FinalText:  reply,
		StopReason: acp.StopReasonEndTurn,
	}, nil
}

func extractPromptText(blocks []acp.ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Text != nil && b.Text.Text != "" {
			parts = append(parts, b.Text.Text)
		}
	}
	return strings.Join(parts, "")
}

func (a *Agent) updater(sid acp.SessionId) SessionUpdater {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()
	if conn == nil {
		return nil
	}
	return connectionUpdater{conn: conn, sessionID: sid}
}

type connectionUpdater struct {
	conn      *acp.AgentSideConnection
	sessionID acp.SessionId
}

func (u connectionUpdater) Update(ctx context.Context, update acp.SessionUpdate) error {
	return u.conn.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: u.sessionID,
		Update:    update,
	})
}

func randomSessionID() string {
	var b [12]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return fmt.Sprintf("sess_%d", time.Now().UnixNano())
	}
	return "sess_" + hex.EncodeToString(b[:])
}
