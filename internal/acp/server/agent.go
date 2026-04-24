package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"go.opentelemetry.io/otel/attribute"
	adksession "google.golang.org/adk/session"

	"github.com/dimetron/pi-go/internal/acp/server/adapter"
	piagent "github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/otel"
	"github.com/dimetron/pi-go/internal/subagent"
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
	// AvailableCommandsResolver resolves slash commands for a session cwd.
	// If nil, the agent falls back to the static Skills/Subagents slices.
	AvailableCommandsResolver func(cwd string) []acp.AvailableCommand
	// Skills are the loaded skill definitions used to populate AvailableCommands.
	Skills []extension.Skill
	// Subagents are the loaded sub-agent definitions used to populate AvailableCommands.
	Subagents []subagent.AgentConfig
	// Logger is used for diagnostic output. If nil, a discard logger is used.
	Logger *slog.Logger
	// SessionService lists persisted pi sessions for ACP session/list. Optional.
	SessionService adksession.Service

	mu       sync.Mutex
	conn     *acp.AgentSideConnection
	sessions map[string]*sessionState
}

type sessionState struct {
	cwd             string
	cancel          context.CancelFunc
	commandsSent    bool
	commandsPending bool
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

func (a *Agent) log() *slog.Logger {
	a.mu.Lock()
	l := a.Logger
	a.mu.Unlock()
	if l == nil {
		return slog.New(slog.DiscardHandler)
	}
	return l
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
func (a *Agent) Initialize(_ context.Context, _ acp.InitializeRequest) (acp.InitializeResponse, error) {
	info := a.info()
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
		AgentInfo:       &info,
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession:         true,
			PromptCapabilities:  acp.PromptCapabilities{EmbeddedContext: true},
			SessionCapabilities: acp.SessionCapabilities{List: &acp.SessionListCapabilities{}},
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

	a.log().Log(context.Background(), slog.LevelDebug,
		"acp-server: new session",
		"session_id", sid,
		"cwd", params.Cwd,
		"pid", pid(),
	)

	// Send AvailableCommandsUpdate to the new session so Zed shows the
	// slash-command list (clear, compact, help, skills, subagents).
	go a.sendAvailableCommands(sid)

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

	// Retry advertising slash commands on later turns if the initial session
	// notification failed or the session was loaded before a connection existed.
	go a.sendAvailableCommands(sid)

	promptCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	promptCtx, span := otel.Tracer("acp-server").Start(promptCtx, "acp.Prompt")
	span.SetAttributes(
		attribute.String("session.id", sid),
		attribute.String("session.cwd", state.cwd),
		attribute.Int("prompt.len", len(extractPromptText(params.Prompt))),
	)
	defer span.End()

	a.mu.Lock()
	state.cancel = cancel
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		state.cancel = nil
		a.mu.Unlock()
	}()

	log := a.log()
	log.Log(ctx, slog.LevelDebug,
		"acp-server: prompt start",
		"session_id", sid,
		"prompt_len", len(params.Prompt),
		"message_id", params.MessageId != nil,
	)

	updater := a.updater(acp.SessionId(sid))
	turn := PromptTurn{
		SessionID: sid,
		CWD:       state.cwd,
		Prompt:    extractPromptText(params.Prompt),
		Updater:   updater,
	}

	var panicked atomic.Bool
	result, err := func() (PromptResult, error) {
		defer func() {
			if r := recover(); r != nil {
				panicked.Store(true)
				log.Log(ctx, slog.LevelError,
					"acp-server: prompt panic",
					"session_id", sid,
					"panic", r,
					"stack", string(debug.Stack()),
				)
			}
		}()
		return a.handler()(promptCtx, turn)
	}()

	if panicked.Load() {
		return acp.PromptResponse{}, fmt.Errorf("handler panicked")
	}

	log.Log(ctx, slog.LevelDebug,
		"acp-server: prompt done",
		"session_id", sid,
		"err", err,
		"stop_reason", result.StopReason,
		"final_text_len", len(result.FinalText),
	)

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

// ListSessions returns persisted pi sessions visible to the ACP client.
func (a *Agent) ListSessions(ctx context.Context, params acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	if a.SessionService == nil {
		return acp.ListSessionsResponse{Sessions: []acp.SessionInfo{}}, nil
	}
	resp, err := a.SessionService.List(ctx, &adksession.ListRequest{
		AppName: piagent.AppName,
		UserID:  piagent.DefaultUserID,
	})
	if err != nil {
		return acp.ListSessionsResponse{}, fmt.Errorf("listing sessions: %w", err)
	}

	sessions := make([]acp.SessionInfo, 0, len(resp.Sessions))
	for _, sess := range resp.Sessions {
		cwd := sessionCWD(sess)
		if params.Cwd != nil && cwd != *params.Cwd {
			continue
		}
		updated := ""
		if !sess.LastUpdateTime().IsZero() {
			updated = sess.LastUpdateTime().Format(time.RFC3339Nano)
		}
		info := acp.SessionInfo{
			SessionId: acp.SessionId(sess.ID()),
			Cwd:       cwd,
		}
		if updated != "" {
			info.UpdatedAt = &updated
		}
		title := sess.ID()
		info.Title = &title
		sessions = append(sessions, info)
	}
	return acp.ListSessionsResponse{Sessions: sessions}, nil
}

func sessionCWD(sess adksession.Session) string {
	if val, err := sess.State().Get("cwd"); err == nil {
		if cwd, ok := val.(string); ok && strings.TrimSpace(cwd) != "" {
			return cwd
		}
	}
	if wd, err := getwd(); err == nil {
		return wd
	}
	return "/"
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
		switch {
		case b.Text != nil && b.Text.Text != "":
			parts = append(parts, b.Text.Text)
		case b.Resource != nil:
			parts = append(parts, formatEmbeddedResource(b.Resource))
		case b.ResourceLink != nil:
			parts = append(parts, "[Reference: "+b.ResourceLink.Uri+"]")
		}
	}
	return strings.Join(parts, "\n")
}

// formatEmbeddedResource converts an ACP embedded-resource block (produced when
// the user @-mentions a file in Zed) into a text snippet the LLM can read.
// TextResourceContents carries the actual file text; BlobResourceContents is
// binary and only the URI is surfaced.
func formatEmbeddedResource(r *acp.ContentBlockResource) string {
	if r.Resource.TextResourceContents != nil {
		rc := r.Resource.TextResourceContents
		return fmt.Sprintf("[File: %s]\n```\n%s\n```", rc.Uri, rc.Text)
	}
	if r.Resource.BlobResourceContents != nil {
		return "[Binary file: " + r.Resource.BlobResourceContents.Uri + "]"
	}
	return ""
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

func (a *Agent) availableCommandsForCWD(cwd string) []acp.AvailableCommand {
	a.mu.Lock()
	resolver := a.AvailableCommandsResolver
	skills := append([]extension.Skill(nil), a.Skills...)
	subagents := append([]subagent.AgentConfig(nil), a.Subagents...)
	a.mu.Unlock()

	if resolver != nil {
		return resolver(cwd)
	}
	return adapter.BuildAvailableCommands(skills, subagents)
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

// sendAvailableCommands sends the list of available slash commands to the
// newly created session. It is called from NewSession after the session state
// is initialized. If commands were already sent to this session, it is a no-op.
func (a *Agent) sendAvailableCommands(sid string) {
	a.mu.Lock()
	state, ok := a.sessions[sid]
	if !ok {
		a.mu.Unlock()
		return
	}
	if state.commandsSent || state.commandsPending {
		a.mu.Unlock()
		return
	}
	state.commandsPending = true
	cwd := state.cwd
	a.mu.Unlock()

	success := false
	defer func() {
		a.mu.Lock()
		if st, ok := a.sessions[sid]; ok {
			st.commandsPending = false
			if success {
				st.commandsSent = true
			}
		}
		a.mu.Unlock()
	}()

	updater := a.updater(acp.SessionId(sid))
	if updater == nil {
		return
	}
	cmds := a.availableCommandsForCWD(cwd)
	if len(cmds) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := updater.Update(ctx, acp.SessionUpdate{
		AvailableCommandsUpdate: &acp.SessionAvailableCommandsUpdate{
			AvailableCommands: cmds,
		},
	}); err != nil {
		return
	}
	success = true
}

func randomSessionID() string {
	var b [12]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return fmt.Sprintf("sess_%d", time.Now().UnixNano())
	}
	return "sess_" + hex.EncodeToString(b[:])
}

var pid = func() int { return 0 } // replaced at init

func init() {
	pid = func() int { return syscall.Getpid() }
}
