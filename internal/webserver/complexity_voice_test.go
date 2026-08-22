//go:build !windows

package webserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/voicegemini"
)

// This file pins the branch structure of the voice functions the complexity
// refactor reshaped (the CSI half lives in complexity_web_test.go):
//
//   - the four voice tool bodies lifted out of (*ServerV2).executeVoiceTool.
//   - (*ServerV2).voiceRelayTarget, the guard block lifted out of
//     handleGeminiVoiceWS.

// ---------------------------------------------------------------------------
// Voice tool bodies
// ---------------------------------------------------------------------------

// Every tool body decodes its own arguments, and every one of them turns a
// malformed payload into a tool failure rather than an error — a provider left
// waiting on a function response stalls the whole conversation.
func TestVoiceToolsRejectMalformedArgs(t *testing.T) {
	s := withVoice(t, testServer(t))
	testBridge(t, s, "term-1")
	vs := &voiceSession{ID: "vs-1", Terminal: "term-1"}

	// Each tool's argument struct has a differently typed field, so one bad
	// payload per tool: a string where a number belongs and vice versa.
	tests := []struct {
		tool string
		args string
	}{
		{voiceToolSendPrompt, `{"prompt": 42}`},
		{voiceToolSendPrompt, `{`},
		{voiceToolReadScreen, `{"lines": "many"}`},
		{voiceToolReadScreen, `[]`},
		{voiceToolWait, `{"seconds": "a while"}`},
		{voiceToolWait, `"nope"`},
		{voiceToolSendKey, `{"key": ["enter"]}`},
		{voiceToolSendKey, `{"key":}`},
	}

	for _, tt := range tests {
		t.Run(tt.tool+" "+tt.args, func(t *testing.T) {
			res := s.executeVoiceTool(t.Context(), vs, voicegemini.FunctionCall{
				Name: tt.tool,
				Args: json.RawMessage(tt.args),
			})
			if _, failed := res.Response["error"]; !failed {
				t.Fatalf("%s with args %s returned %+v, want an error response", tt.tool, tt.args, res.Response)
			}
			if !strings.HasPrefix(res.Summary, tt.tool+" failed: ") {
				t.Errorf("summary = %q, want it to name the failing tool", res.Summary)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Relay admission
// ---------------------------------------------------------------------------

// voiceRelayTarget is the guard block lifted out of handleGeminiVoiceWS. Each
// rejection has its own status, and the caller relies on the boolean rather
// than on the response having been written.
func TestVoiceRelayTargetRejections(t *testing.T) {
	// relayTarget runs one admission attempt against s for the given session id
	// and reports the session, whether it was admitted, and the status written.
	relayTarget := func(t *testing.T, s *ServerV2, id string) (*voiceSession, bool, int) {
		t.Helper()
		rec := httptest.NewRecorder()
		r := voiceReq(t, s, http.MethodGet, "/api/voice/ws?session="+id, nil)
		vs, ok := s.voiceRelayTarget(rec, r)
		return vs, ok, rec.Code
	}

	t.Run("voice not configured", func(t *testing.T) {
		// A server started without --voice: paired, but with no transport.
		s := testServer(t)
		_, ok, code := relayTarget(t, s, "anything")
		if ok {
			t.Fatal("admitted a request while voice was disabled")
		}
		if code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want %d", code, http.StatusServiceUnavailable)
		}
	})

	t.Run("no session id", func(t *testing.T) {
		s := withVoice(t, testServer(t))
		_, ok, code := relayTarget(t, s, "")
		if ok {
			t.Fatal("admitted a request with no session id")
		}
		if code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", code, http.StatusNotFound)
		}
	})

	t.Run("unknown session id", func(t *testing.T) {
		s := withVoice(t, testServer(t))
		_, ok, code := relayTarget(t, s, "never-created")
		if ok {
			t.Fatal("admitted an unknown session id")
		}
		if code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", code, http.StatusNotFound)
		}
	})

	t.Run("unpaired browser", func(t *testing.T) {
		s := withVoice(t, testServer(t))
		rec := httptest.NewRecorder()
		// No token at all: the relay re-checks the pairing rather than
		// inheriting trust from the POST that created the session.
		r := httptest.NewRequest(http.MethodGet, "/api/voice/ws?session=x", nil)
		if _, ok := s.voiceRelayTarget(rec, r); ok {
			t.Fatal("admitted an unpaired browser")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("first admission wins, second is refused", func(t *testing.T) {
		s := withVoice(t, testServer(t))
		created, err := s.voiceStore.create("", "term-1")
		if err != nil {
			t.Fatalf("create() = %v", err)
		}

		vs, ok, code := relayTarget(t, s, created.ID)
		if !ok {
			t.Fatalf("first admission was refused with status %d", code)
		}
		if vs != created {
			t.Errorf("admitted %+v, want the stored session %+v", vs, created)
		}

		// claimed() is one-shot, so the second relay for the same session is a
		// conflict — that is what keeps two tabs off one coding agent.
		if _, ok, code := relayTarget(t, s, created.ID); ok {
			t.Fatal("admitted a second relay for the same session")
		} else if code != http.StatusConflict {
			t.Errorf("status = %d, want %d", code, http.StatusConflict)
		}
	})
}
