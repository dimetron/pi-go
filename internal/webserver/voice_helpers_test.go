package webserver

// Request helpers for the voice endpoints. Untagged on purpose: the Windows
// stub tests (voice_windows_test.go) need to reach the same handlers through
// the same pairing gate as the real ones.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testServer(t *testing.T) *ServerV2 {
	t.Helper()
	return NewServerV2(Config{Addr: "127.0.0.1:0"})
}

// voiceToken pairs a browser with s and returns the approved token. Voice
// endpoints are gated on it, so every test that reaches one has to hold it —
// which is the point: a voice session can type into the coding agent.
func voiceToken(t *testing.T, s *ServerV2) string {
	t.Helper()
	code, _, err := s.BootstrapPair(".")
	if err != nil {
		t.Fatalf("BootstrapPair() = %v", err)
	}
	token, err := s.pairingMgr.Approve(code)
	if err != nil {
		t.Fatalf("Approve() = %v", err)
	}
	return token
}

// voiceReq builds a request to a voice endpoint carrying an approved token.
func voiceReq(t *testing.T, s *ServerV2, method, target string, body io.Reader) *http.Request {
	t.Helper()
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	return httptest.NewRequest(method, target+sep+"token="+voiceToken(t, s), body)
}
