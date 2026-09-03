package server

import (
	"context"
	"log/slog"

	acp "github.com/coder/acp-go-sdk"
)

// Ensure *Agent satisfies the optional AgentLoader interface so the SDK
// dispatches 'session/load' to us. Advertised via AgentCapabilities.LoadSession
// in Initialize.
var _ acp.AgentLoader = (*Agent)(nil)

// LoadSession binds the supplied session id to this agent with the given cwd
// and, when a persisted transcript exists for it, replays that transcript to
// the client before responding — the protocol's contract for session/load, and
// what lets an editor show the conversation it is about to continue. Loading
// is idempotent: repeated loads refresh the session's cwd and replay again.
// See bindSession for how unknown ids are treated with and without a store.
func (a *Agent) LoadSession(ctx context.Context, params acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	sid := string(params.SessionId)
	persisted, err := a.bindSession(ctx, sid, params.Cwd)
	if err != nil {
		return acp.LoadSessionResponse{}, err
	}
	if !persisted {
		return acp.LoadSessionResponse{}, nil
	}
	updater := a.updater(params.SessionId)
	if updater == nil {
		return acp.LoadSessionResponse{}, nil
	}
	if err := a.store().Replay(ctx, sid, updater); err != nil {
		a.log().Log(ctx, slog.LevelError, "acp-server: session replay failed", "session_id", sid, "err", err)
		return acp.LoadSessionResponse{}, err
	}
	return acp.LoadSessionResponse{}, nil
}
