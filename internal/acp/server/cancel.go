package server

import (
	"context"

	acp "github.com/coder/acp-go-sdk"
)

// Cancel routes an ACP cancel notification to the in-flight Prompt for the
// session, if any, by canceling the context passed to its PromptHandler. If
// no prompt is running for the id, the notification is silently dropped —
// ACP cancellation is best-effort per spec.
func (a *Agent) Cancel(_ context.Context, params acp.CancelNotification) error {
	sid := string(params.SessionId)
	a.mu.Lock()
	var cancel context.CancelFunc
	if st, ok := a.sessions[sid]; ok {
		cancel = st.cancel
		st.cancel = nil
	}
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}
