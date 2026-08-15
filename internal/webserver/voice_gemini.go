package webserver

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dimetron/pi-go/internal/voicegemini"
)

// The Gemini Live transport is a server-side WebSocket relay.
//
// The browser connects to GET /api/voice/gemini/ws?session=<id> (a session it
// created via POST /api/voice/sessions); the server dials the Live API with the
// long-lived GEMINI_API_KEY and pumps frames both ways. The key never reaches
// the page in any form.
//
// Browser-bound frames:
//   - binary: raw little-endian PCM16 output audio at 24kHz, ready to play
//   - text (JSON): {type: ready | transcript_user_delta |
//     transcript_assistant_delta | interrupted | turn_complete | go_away |
//     tool_cancel | error, text?, message?}
//
// Server-bound frames:
//   - binary: raw PCM16 mic audio at 16kHz; the relay base64-wraps it into a
//     realtimeInput message. Text frames are ignored — the browser has no say
//     in session config, which the server locked at setup.

// wsWriter serializes writes to one websocket connection: both pumps may write
// concurrently, and gorilla forbids concurrent writers.
type wsWriter struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (w *wsWriter) writeJSON(v any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteJSON(v)
}

func (w *wsWriter) writeBinary(b []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteMessage(websocket.BinaryMessage, b)
}

// maxVoiceCloseText bounds how much of a provider close reason is echoed to the
// browser.
const maxVoiceCloseText = 400

// handleGeminiVoiceWS relays one live session between the browser and the
// Gemini Live API.
func (s *ServerV2) handleGeminiVoiceWS(w http.ResponseWriter, r *http.Request) {
	if !s.voiceEnabled() {
		voiceHTTPError(w, http.StatusServiceUnavailable,
			fmt.Errorf("voice is not configured — set GEMINI_API_KEY and run `pi serve --voice`"))
		return
	}
	sessionID := r.URL.Query().Get("session")
	vs, ok := s.voiceStore.get(sessionID)
	if !ok {
		voiceHTTPError(w, http.StatusNotFound, fmt.Errorf("unknown or expired voice session"))
		return
	}
	if vs.claimed() {
		voiceHTTPError(w, http.StatusConflict, fmt.Errorf("this voice session already has a relay"))
		return
	}

	up := &websocket.Upgrader{CheckOrigin: s.voiceOriginOK}
	browser, err := up.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote the response
	}
	defer browser.Close() //nolint:errcheck // best-effort close on a relay teardown

	// This socket IS the session's life. A phone that locks (or a tab that
	// dies) drops it without a close frame and without the browser's DELETE, so
	// releasing the slot here rather than at the TTL is what keeps the next
	// start from hitting the concurrency cap naming a session that no longer
	// exists anywhere.
	defer func() {
		s.voiceStore.delete(vs.ID)
		s.log.Info("voice relay ended", "session", vs.ID,
			"lived", time.Since(vs.CreatedAt).Round(time.Second).String())
	}()

	s.log.Info("voice browser connected", "session", vs.ID, "model", vs.Model)
	bw := &wsWriter{conn: browser}

	// Dial the provider. The URL carries the key; never log it.
	creator := s.voiceGemini.WithModelSelection(vs.Model)
	provider, resp, err := websocket.DefaultDialer.Dial(creator.DialURL(), nil)
	// On a failed handshake gorilla hands back the HTTP response and leaves its
	// body to the caller; on success resp carries no body to drain.
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close() //nolint:errcheck // draining a failed handshake response
	}
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		s.log.Error("voice provider dial failed", "session", vs.ID, "status", status, "err", err)
		_ = bw.writeJSON(map[string]any{
			"type":    "error",
			"message": fmt.Sprintf("gemini dial failed (status %d)", status),
		})
		return
	}
	defer provider.Close() //nolint:errcheck // best-effort close on a relay teardown
	pw := &wsWriter{conn: provider}

	// The server owns the session setup: model, instructions, transcription and
	// tools. The model comes from the session (the browser picked it from the
	// server's list at create time and it was validated there), so two tabs can
	// run different models at once.
	if err := pw.writeJSON(creator.SetupMessage()); err != nil {
		s.log.Error("voice setup send failed", "session", vs.ID, "err", err)
		_ = bw.writeJSON(map[string]any{"type": "error", "message": geminiCloseMessage(err)})
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	// A delete has to stop this relay, not merely drop its store entry: both
	// read loops sit in a blocking ReadMessage/ReadJSON that only a closed
	// socket unblocks.
	vs.setCancel(func() {
		cancel()
		_ = browser.Close()
		_ = provider.Close()
	})

	// browser → provider: mic audio.
	go func() {
		// A vanished browser has to end the WHOLE relay. Canceling the context
		// cannot do it on its own — the loop below sits in provider.ReadJSON,
		// which blocks outside any context — so closing the provider socket is
		// what unblocks it and frees the slot in milliseconds instead of at the
		// TTL.
		//
		// This needs the dead peer to actually surface as a read error (a close
		// frame, FIN or RST). A half-open TCP connection — a phone that locks
		// and never sends anything again — is only noticed when the provider
		// itself times out. Add a read deadline plus a ping ticker here if that
		// idle window ever needs to be bounded.
		defer func() {
			cancel()
			_ = provider.Close()
		}()
		for {
			kind, data, err := browser.ReadMessage()
			if err != nil {
				return
			}
			if kind != websocket.BinaryMessage || len(data) == 0 {
				continue
			}
			msg := voicegemini.RealtimeAudioMessage(base64.StdEncoding.EncodeToString(data))
			if err := pw.writeJSON(msg); err != nil {
				return
			}
		}
	}()

	// provider → browser: audio, transcripts, tool calls.
	for {
		select {
		case <-ctx.Done():
			s.log.Info("voice relay closed", "session", vs.ID)
			return
		default:
		}
		var sm voicegemini.ServerMessage
		if err := provider.ReadJSON(&sm); err != nil {
			s.log.Info("voice provider read ended", "session", vs.ID, "err", err)
			_ = bw.writeJSON(map[string]any{"type": "error", "message": geminiCloseMessage(err)})
			return
		}
		switch {
		case sm.SetupComplete != nil:
			s.log.Info("voice setup complete", "session", vs.ID, "model", creator.Model)
			_ = bw.writeJSON(map[string]any{"type": "ready"})
		case sm.ServerContent != nil:
			relayGeminiContent(bw, sm.ServerContent)
		case sm.ToolCall != nil:
			// This build declares no function declarations at setup, so a
			// toolCall here means the model invented one. Answering it keeps the
			// turn moving instead of leaving the provider waiting on a response
			// that will never come.
			s.answerUnsupportedToolCalls(pw, vs.ID, sm.ToolCall)
		case sm.ToolCancel != nil:
			_ = bw.writeJSON(map[string]any{"type": "tool_cancel", "ids": sm.ToolCancel.IDs})
		case sm.GoAway != nil:
			s.log.Info("voice goAway", "session", vs.ID, "timeLeft", sm.GoAway.TimeLeft)
			_ = bw.writeJSON(map[string]any{"type": "go_away"})
		}
	}
}

// relayGeminiContent forwards one serverContent to the browser: output audio as
// binary frames, everything else as small JSON events.
func relayGeminiContent(bw *wsWriter, sc *voicegemini.ServerContent) {
	for _, b64 := range sc.AudioParts() {
		if pcm, err := base64.StdEncoding.DecodeString(b64); err == nil && len(pcm) > 0 {
			_ = bw.writeBinary(pcm)
		}
	}
	if sc.InputTranscr != nil && sc.InputTranscr.Text != "" {
		_ = bw.writeJSON(map[string]any{"type": "transcript_user_delta", "text": sc.InputTranscr.Text})
	}
	if sc.OutputTranscr != nil && sc.OutputTranscr.Text != "" {
		_ = bw.writeJSON(map[string]any{"type": "transcript_assistant_delta", "text": sc.OutputTranscr.Text})
	}
	if sc.Interrupted {
		_ = bw.writeJSON(map[string]any{"type": "interrupted"})
	}
	if sc.TurnComplete {
		_ = bw.writeJSON(map[string]any{"type": "turn_complete"})
	}
}

// answerUnsupportedToolCalls replies to every call in one toolCall with an
// error response. A provider left waiting on a function response stalls the
// conversation, so an explicit refusal is strictly better than silence.
func (s *ServerV2) answerUnsupportedToolCalls(pw *wsWriter, sessionID string, tc *voicegemini.ToolCall) {
	responses := make([]voicegemini.FunctionResponse, 0, len(tc.FunctionCalls))
	for _, fc := range tc.FunctionCalls {
		s.log.Warn("voice tool call declined", "session", sessionID, "tool", fc.Name)
		responses = append(responses, voicegemini.FunctionResponse{
			ID:       fc.ID,
			Name:     fc.Name,
			Response: map[string]any{"error": "this voice session exposes no tools"},
		})
	}
	if len(responses) > 0 {
		_ = pw.writeJSON(voicegemini.ToolResponseMessage(responses))
	}
}

// geminiCloseMessage turns a provider read/write error into something the
// browser can act on. When Gemini closes the socket it explains why in the
// close frame (a rejected setup payload names the offending field), and that
// sentence is worth far more to whoever is debugging than a close code.
func geminiCloseMessage(err error) string {
	var ce *websocket.CloseError
	if errors.As(err, &ce) && ce.Text != "" {
		return fmt.Sprintf("gemini closed the session (code %d): %s", ce.Code, truncateVoice(ce.Text, maxVoiceCloseText))
	}
	return "provider connection closed"
}

// truncateVoice shortens s to at most n runes, marking that it was cut.
func truncateVoice(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// voiceOriginOK accepts same-origin upgrades and, when the server is explicitly
// running insecure, anything. A browser always sends Origin on a WebSocket
// dial, so an absent one is a non-browser client and is allowed — the session
// id is the credential that matters here.
func (s *ServerV2) voiceOriginOK(r *http.Request) bool {
	if s.cfg.Insecure {
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	_, host := requestOriginAndHost(r)
	return strings.EqualFold(u.Host, host)
}
