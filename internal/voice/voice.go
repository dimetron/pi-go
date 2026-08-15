// Package voice holds the provider-neutral contracts for pi's live voice
// sessions in the web server.
//
// The interface and session type live here rather than in internal/webserver so
// a provider adapter (internal/voicegemini) can implement them without an
// import cycle: webserver imports voice and the adapter; the adapter imports
// only voice.
package voice

import (
	"context"
	"time"
)

// SessionCreator creates provider-scoped realtime sessions.
//
// The production implementation talks to Gemini Live (internal/voicegemini);
// tests use a fake so HTTP tests never need credentials or network access. The
// interface is the seam that keeps provider transport details out of the relay
// and session logic.
type SessionCreator interface {
	Create(ctx context.Context, threadID string) (Session, error)
}

// Session is the provider-approved short-lived connection data the browser
// needs to establish a live session. Realtime is the public map returned
// verbatim to the browser; it must never contain a long-lived credential.
type Session struct {
	SessionID string
	ExpiresAt time.Time
	Realtime  map[string]any
}
