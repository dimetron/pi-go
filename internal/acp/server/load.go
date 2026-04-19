package server

import (
	"context"

	acp "github.com/coder/acp-go-sdk"
)

// Ensure *Agent satisfies the optional AgentLoader interface so the SDK
// dispatches 'session/load' to us. Advertised via AgentCapabilities.LoadSession
// in Initialize.
var _ acp.AgentLoader = (*Agent)(nil)

// LoadSession binds the supplied session id to this agent with the given cwd.
// If the id is unknown (e.g. the client is resuming a persisted session id
// against a fresh agent process), a new state record is created so subsequent
// Prompt calls succeed. Loading is idempotent — repeated loads simply refresh
// the session's cwd.
func (a *Agent) LoadSession(_ context.Context, params acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	sid := string(params.SessionId)
	a.mu.Lock()
	if a.sessions == nil {
		a.sessions = make(map[string]*sessionState)
	}
	st, ok := a.sessions[sid]
	if !ok {
		st = &sessionState{}
		a.sessions[sid] = st
	}
	st.cwd = params.Cwd
	a.mu.Unlock()
	return acp.LoadSessionResponse{}, nil
}
