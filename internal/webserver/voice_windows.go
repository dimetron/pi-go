//go:build windows

package webserver

// Browser voice is compiled out on Windows.
//
// The feature is not a chat next to the terminal: its tools type prompts into
// the live pi process and read its screen back through a PTY bridge, and
// github.com/creack/pty has no Windows implementation — every call returns
// ErrUnsupported, and the relay's tests hang in the serve/PTY path on the
// Windows CI job. Rather than ship a voice session that connects and then
// cannot touch the agent, the handlers below keep the routes server.go
// registers and answer with one clear reason, so the page hides the control
// and `pi serve --voice` fails at startup (see internal/cli/serve_voice_windows.go).

import (
	"context"
	"errors"
	"net/http"

	"github.com/dimetron/pi-go/internal/voicegemini"
)

// errVoiceUnsupported is the one reason every voice surface reports on
// Windows. The page renders it verbatim.
var errVoiceUnsupported = errors.New("voice is not supported on Windows")

// voiceStore keeps the ServerV2 field compiling; there are never any sessions
// to hold.
type voiceStore struct{}

func newVoiceStore() *voiceStore { return &voiceStore{} }

// EnableVoice always fails: there is no PTY to drive, so there is nothing to
// enable. Failing here rather than at the first microphone byte keeps the
// contract of the real implementation — a voice that cannot work is a boot
// error the operator reads in the terminal.
func (s *ServerV2) EnableVoice(context.Context, string, ...voicegemini.Option) error {
	return errVoiceUnsupported
}

// handleVoiceConfig reports voice as disabled with the platform reason, using
// the same shape the page reads on every other OS.
func (s *ServerV2) handleVoiceConfig(w http.ResponseWriter, r *http.Request) {
	if !s.voiceAuthorized(w, r) {
		return
	}
	writeJSON(w, map[string]any{
		"enabled": false,
		"reason":  errVoiceUnsupported.Error(),
	})
}

func (s *ServerV2) handleCreateVoiceSession(w http.ResponseWriter, r *http.Request) {
	s.voiceUnsupported(w, r)
}

func (s *ServerV2) handleDeleteVoiceSession(w http.ResponseWriter, r *http.Request) {
	s.voiceUnsupported(w, r)
}

func (s *ServerV2) handleGeminiVoiceWS(w http.ResponseWriter, r *http.Request) {
	s.voiceUnsupported(w, r)
}

// voiceUnsupported answers 503 behind the pairing gate, the status the page
// already treats as "voice is off" when a server runs without --voice.
func (s *ServerV2) voiceUnsupported(w http.ResponseWriter, r *http.Request) {
	if !s.voiceAuthorized(w, r) {
		return
	}
	voiceHTTPError(w, http.StatusServiceUnavailable, errVoiceUnsupported)
}
