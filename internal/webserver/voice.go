package webserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dimetron/pi-go/internal/voicegemini"
)

// Voice sessions are created over REST and then consumed by exactly one
// WebSocket relay.
//
// The split exists because a browser cannot set headers on a WebSocket dial, so
// the relay authenticates by an opaque session id in the query string. Creating
// the session over REST is what makes that id short-lived and single-use
// instead of a bearer token living in page state.

// voiceSessionTTL bounds an unclaimed session. A session that is never dialed
// (the user grants the mic and then closes the tab) must not hold a slot until
// the process restarts.
const voiceSessionTTL = 5 * time.Minute

// maxVoiceSessions caps concurrent live relays. Each one holds a provider
// WebSocket and two audio pumps, and the practical limit for a local dev server
// is the operator's own ears — this exists to bound a runaway client, not to
// ration real use.
const maxVoiceSessions = 4

// voiceSession is one browser-to-provider relay slot.
type voiceSession struct {
	ID        string
	Model     string
	CreatedAt time.Time
	ExpiresAt time.Time

	mu     sync.Mutex
	claim  bool   // a relay has taken this session
	cancel func() // set by the relay; unblocks both read pumps
}

// claimed reports whether a relay already owns this session, marking it claimed
// when it does not. Two dials with the same id must not both pump audio into
// one provider connection.
func (vs *voiceSession) claimed() bool {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if vs.claim {
		return true
	}
	vs.claim = true
	return false
}

// setCancel records the relay's teardown hook.
func (vs *voiceSession) setCancel(f func()) {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	vs.cancel = f
}

// stop runs the relay's teardown hook if one is registered.
func (vs *voiceSession) stop() {
	vs.mu.Lock()
	f := vs.cancel
	vs.mu.Unlock()
	if f != nil {
		f()
	}
}

// voiceStore holds live voice sessions by id.
type voiceStore struct {
	mu       sync.Mutex
	sessions map[string]*voiceSession
}

func newVoiceStore() *voiceStore {
	return &voiceStore{sessions: make(map[string]*voiceSession)}
}

// create registers a new session, first evicting anything expired. It fails
// when the server is already at maxVoiceSessions, because silently replacing a
// live conversation would be worse than refusing a new one.
func (st *voiceStore) create(model string) (*voiceSession, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	for id, vs := range st.sessions {
		if now.After(vs.ExpiresAt) {
			delete(st.sessions, id)
		}
	}
	if len(st.sessions) >= maxVoiceSessions {
		return nil, fmt.Errorf("too many live voice sessions (%d); close one and retry", len(st.sessions))
	}
	vs := &voiceSession{
		ID:        randomVoiceID(),
		Model:     model,
		CreatedAt: now,
		ExpiresAt: now.Add(voiceSessionTTL),
	}
	st.sessions[vs.ID] = vs
	return vs, nil
}

// get returns a live session by id. An expired session reads as absent.
func (st *voiceStore) get(id string) (*voiceSession, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	vs, ok := st.sessions[id]
	if !ok {
		return nil, false
	}
	if time.Now().After(vs.ExpiresAt) {
		delete(st.sessions, id)
		return nil, false
	}
	return vs, true
}

// delete removes a session, stopping its relay if one is running.
func (st *voiceStore) delete(id string) {
	st.mu.Lock()
	vs, ok := st.sessions[id]
	delete(st.sessions, id)
	st.mu.Unlock()
	if ok {
		vs.stop()
	}
}

// randomVoiceID returns an opaque session id.
func randomVoiceID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is not a condition a web handler can sensibly
		// continue past with a guessable id, so fall back to something that is
		// still unique per process-nanosecond rather than to a constant.
		return fmt.Sprintf("vs-%d", time.Now().UnixNano())
	}
	return "vs-" + hex.EncodeToString(b[:])
}

// voiceEnabled reports whether this build has a configured Gemini creator.
func (s *ServerV2) voiceEnabled() bool { return s.voiceGemini != nil }

// handleVoiceConfig reports whether voice is available and what the browser may
// choose, so the page can render (or hide) the control without guessing.
func (s *ServerV2) handleVoiceConfig(w http.ResponseWriter, _ *http.Request) {
	if !s.voiceEnabled() {
		writeJSON(w, map[string]any{
			"enabled": false,
			"reason":  "voice is not configured — set GEMINI_API_KEY and run `pi serve --voice`",
		})
		return
	}
	writeJSON(w, map[string]any{
		"enabled":    true,
		"models":     s.voiceGemini.Models(),
		"model":      s.voiceGemini.Model,
		"inputRate":  voicegemini.InputSampleRate,
		"outputRate": voicegemini.OutputSampleRate,
	})
}

// handleCreateVoiceSession mints a relay slot for one browser.
func (s *ServerV2) handleCreateVoiceSession(w http.ResponseWriter, r *http.Request) {
	if !s.voiceEnabled() {
		voiceHTTPError(w, http.StatusServiceUnavailable,
			fmt.Errorf("voice is not configured — set GEMINI_API_KEY and run `pi serve --voice`"))
		return
	}

	// A model the browser did not name, or named badly, resolves to the
	// server's default rather than failing: the selection is a preference, and
	// WithModelSelection is what enforces the allowlist.
	var body struct {
		Model string `json:"model"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body)
	}
	creator := s.voiceGemini.WithModelSelection(strings.TrimSpace(body.Model))

	vs, err := s.voiceStore.create(creator.Model)
	if err != nil {
		voiceHTTPError(w, http.StatusConflict, err)
		return
	}

	sess, err := creator.Create(r.Context(), vs.ID)
	if err != nil {
		s.voiceStore.delete(vs.ID)
		voiceHTTPError(w, http.StatusInternalServerError, err)
		return
	}

	s.log.Info("voice session created", "session", vs.ID, "model", creator.Model)
	writeJSON(w, map[string]any{
		"id":        vs.ID,
		"model":     creator.Model,
		"expiresAt": vs.ExpiresAt.UTC().Format(time.RFC3339),
		"realtime":  sess.Realtime,
	})
}

// handleDeleteVoiceSession ends a session from the page (a deliberate "stop"),
// which is the tidy path; the relay's own teardown covers every other way a
// session ends.
func (s *ServerV2) handleDeleteVoiceSession(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/voice/sessions/")
	if id == "" {
		voiceHTTPError(w, http.StatusBadRequest, fmt.Errorf("missing session id"))
		return
	}
	s.voiceStore.delete(id)
	w.WriteHeader(http.StatusNoContent)
}

// voiceHTTPError writes one error as JSON. The web UI reads these directly, so
// the message is the user-facing text.
func voiceHTTPError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
}

// EnableVoice configures the Gemini Live transport and verifies the key and
// model before the first microphone byte.
//
// Verification is deliberately at enable time rather than at first dial:
// a wrong key or a non-Live model is a boot error the operator sees in the
// terminal, not a dead microphone the user discovers mid-sentence.
func (s *ServerV2) EnableVoice(ctx context.Context, apiKey string, opts ...voicegemini.Option) error {
	c := voicegemini.New(apiKey, opts...)
	if err := c.Verify(ctx); err != nil {
		return err
	}
	s.voiceGemini = c
	s.log.Info("voice enabled", "model", c.Model, "key", voicegemini.MaskKey(apiKey))
	return nil
}
