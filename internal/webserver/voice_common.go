package webserver

// Voice plumbing shared by every platform. The feature itself — the Gemini
// Live relay and the PTY-driven agent tools — is compiled only where
// github.com/creack/pty works (voice.go, voice_agent.go, voice_gemini.go,
// all //go:build !windows); voice_windows.go supplies the same handler surface
// as stubs. What lives here is what both halves need: the error shape the page
// reads and the pairing gate in front of every voice endpoint.

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// voiceHTTPError writes one error as JSON. The web UI reads these directly, so
// the message is the user-facing text.
func voiceHTTPError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
}

// voiceAuthorized gates every voice endpoint behind the same pairing token the
// terminal uses, answering 401 and reporting false when it is missing.
//
// This is not defense in depth, it is the actual boundary. `pi serve` binds all
// interfaces, and a voice session can now type into the coding agent — which
// can edit files and run commands. An unauthenticated caller reaching
// POST /api/voice/sessions would therefore be reaching a shell, so voice must
// be exactly as protected as the terminal it drives.
func (s *ServerV2) voiceAuthorized(w http.ResponseWriter, r *http.Request) bool {
	if s.pairingToken(r) {
		return true
	}
	voiceHTTPError(w, http.StatusUnauthorized, fmt.Errorf("pair this browser with the server before using voice"))
	return false
}
