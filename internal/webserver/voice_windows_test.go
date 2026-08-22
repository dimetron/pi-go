//go:build windows

package webserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The Windows stub must keep the page's contract: /api/voice/config answers
// 200 with enabled=false and a reason, every other voice endpoint answers 503
// with the same reason, and nothing is reachable without the pairing token.

func decodeVoiceBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestVoiceWindows_ConfigReportsUnsupported(t *testing.T) {
	s := testServer(t)
	rec := httptest.NewRecorder()
	s.handleVoiceConfig(rec, voiceReq(t, s, http.MethodGet, "/api/voice/config", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decodeVoiceBody(t, rec)
	if body["enabled"] != false {
		t.Errorf("enabled = %v, want false", body["enabled"])
	}
	if reason, _ := body["reason"].(string); !strings.Contains(reason, "Windows") {
		t.Errorf("reason = %q, want it to name the platform", reason)
	}
}

func TestVoiceWindows_EndpointsAnswer503(t *testing.T) {
	s := testServer(t)
	handlers := map[string]struct {
		method, target string
		h              http.HandlerFunc
	}{
		"create": {http.MethodPost, "/api/voice/sessions", s.handleCreateVoiceSession},
		"delete": {http.MethodDelete, "/api/voice/sessions/abc", s.handleDeleteVoiceSession},
		"ws":     {http.MethodGet, "/api/voice/gemini/ws?session=abc", s.handleGeminiVoiceWS},
	}
	for name, tc := range handlers {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.h(rec, voiceReq(t, s, tc.method, tc.target, nil))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", rec.Code)
			}
			body := decodeVoiceBody(t, rec)
			if msg, _ := body["error"].(string); msg != errVoiceUnsupported.Error() {
				t.Errorf("error = %q, want %q", msg, errVoiceUnsupported.Error())
			}
		})
	}
}

func TestVoiceWindows_RequiresPairing(t *testing.T) {
	s := testServer(t)
	for name, h := range map[string]http.HandlerFunc{
		"config": s.handleVoiceConfig,
		"create": s.handleCreateVoiceSession,
		"delete": s.handleDeleteVoiceSession,
		"ws":     s.handleGeminiVoiceWS,
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h(rec, httptest.NewRequest(http.MethodGet, "/api/voice/x", nil))
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 without a pairing token", rec.Code)
			}
		})
	}
}

func TestVoiceWindows_EnableVoiceFails(t *testing.T) {
	s := testServer(t)
	err := s.EnableVoice(context.Background(), "AIzaSyTestKeyLongEnough")
	if err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("EnableVoice() = %v, want the Windows reason", err)
	}
	if s.voiceGemini != nil {
		t.Error("EnableVoice left a creator behind on a platform that cannot use it")
	}
}
