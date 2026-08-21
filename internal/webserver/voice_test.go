package webserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dimetron/pi-go/internal/voicegemini"
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

// withVoice returns a server whose voice transport is configured without
// touching the network — EnableVoice would verify against Google.
func withVoice(t *testing.T, s *ServerV2) *ServerV2 {
	t.Helper()
	s.voiceGemini = voicegemini.New("AIzaSyTestKeyLongEnough")
	return s
}

func TestVoiceConfigDisabled(t *testing.T) {
	s := testServer(t)
	rec := httptest.NewRecorder()
	s.handleVoiceConfig(rec, voiceReq(t, s, http.MethodGet, "/api/voice/config", nil))

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["enabled"] != false {
		t.Errorf("enabled = %v, want false", body["enabled"])
	}
	// The page renders this reason, so it has to say what to do.
	if reason, _ := body["reason"].(string); !strings.Contains(reason, "GEMINI_API_KEY") {
		t.Errorf("reason = %q, want it to name the missing key", reason)
	}
}

func TestVoiceConfigEnabled(t *testing.T) {
	s := withVoice(t, testServer(t))
	rec := httptest.NewRecorder()
	s.handleVoiceConfig(rec, voiceReq(t, s, http.MethodGet, "/api/voice/config", nil))

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["enabled"] != true {
		t.Fatalf("enabled = %v, want true", body["enabled"])
	}
	if body["model"] != voicegemini.DefaultModel {
		t.Errorf("model = %v, want %q", body["model"], voicegemini.DefaultModel)
	}
	// The page builds its AudioContexts from these; a drift between the two
	// sides is silent distortion, not an error.
	if body["inputRate"] != float64(voicegemini.InputSampleRate) {
		t.Errorf("inputRate = %v, want %d", body["inputRate"], voicegemini.InputSampleRate)
	}
	if body["outputRate"] != float64(voicegemini.OutputSampleRate) {
		t.Errorf("outputRate = %v, want %d", body["outputRate"], voicegemini.OutputSampleRate)
	}
}

func TestCreateVoiceSessionRequiresVoice(t *testing.T) {
	s := testServer(t)
	rec := httptest.NewRecorder()
	s.handleCreateVoiceSession(rec, voiceReq(t, s, http.MethodPost, "/api/voice/sessions", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestCreateVoiceSession(t *testing.T) {
	s := withVoice(t, testServer(t))
	rec := httptest.NewRecorder()
	req := voiceReq(t, s, http.MethodPost, "/api/voice/sessions", strings.NewReader(`{}`))
	s.handleCreateVoiceSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	var body struct {
		ID       string         `json:"id"`
		Model    string         `json:"model"`
		Realtime map[string]any `json:"realtime"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ID == "" {
		t.Fatal("id is empty")
	}
	if body.Realtime["ws"] != "/api/voice/gemini/ws" {
		t.Errorf("realtime.ws = %v", body.Realtime["ws"])
	}
	// The relay authenticates by this id, so it has to be in the store.
	if _, ok := s.voiceStore.get(body.ID); !ok {
		t.Error("the created session is not in the store")
	}
}

// The API key must never reach the browser in any form — that is the whole
// reason this transport relays instead of handing out a token.
func TestCreateVoiceSessionLeaksNoCredential(t *testing.T) {
	s := testServer(t)
	s.voiceGemini = voicegemini.New("AIzaSyVerySecretKeyValue123")

	rec := httptest.NewRecorder()
	s.handleCreateVoiceSession(rec, voiceReq(t, s, http.MethodPost, "/api/voice/sessions", strings.NewReader(`{}`)))

	if strings.Contains(rec.Body.String(), "AIzaSy") {
		t.Fatalf("the session response leaked a credential: %s", rec.Body)
	}
}

// An unknown model is a preference the server declines, not an error: the
// allowlist is what decides, and a stale tab must still get a session.
func TestCreateVoiceSessionIgnoresUnknownModel(t *testing.T) {
	s := withVoice(t, testServer(t))
	rec := httptest.NewRecorder()
	req := voiceReq(t, s, http.MethodPost, "/api/voice/sessions", strings.NewReader(`{"model":"gemini-made-up"}`))
	s.handleCreateVoiceSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	var body struct {
		Model string `json:"model"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body.Model != voicegemini.DefaultModel {
		t.Errorf("model = %q, want the default", body.Model)
	}
}

func TestCreateVoiceSessionCap(t *testing.T) {
	s := withVoice(t, testServer(t))
	for i := 0; i < maxVoiceSessions; i++ {
		if _, err := s.voiceStore.create(voicegemini.DefaultModel, ""); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	rec := httptest.NewRecorder()
	s.handleCreateVoiceSession(rec, voiceReq(t, s, http.MethodPost, "/api/voice/sessions", strings.NewReader(`{}`)))
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

func TestDeleteVoiceSession(t *testing.T) {
	s := withVoice(t, testServer(t))
	vs, err := s.voiceStore.create(voicegemini.DefaultModel, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	rec := httptest.NewRecorder()
	req := voiceReq(t, s, http.MethodDelete, "/api/voice/sessions/"+vs.ID, nil)
	s.handleDeleteVoiceSession(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if _, ok := s.voiceStore.get(vs.ID); ok {
		t.Error("the session survived its delete")
	}
}

func TestDeleteVoiceSessionRunsTheRelayTeardown(t *testing.T) {
	st := newVoiceStore()
	vs, err := st.create(voicegemini.DefaultModel, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	stopped := make(chan struct{})
	vs.setCancel(func() { close(stopped) })

	st.delete(vs.ID)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("delete did not run the relay teardown; both read pumps would stay blocked")
	}
}

func TestVoiceStoreExpiry(t *testing.T) {
	st := newVoiceStore()
	vs, err := st.create(voicegemini.DefaultModel, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	vs.ExpiresAt = time.Now().Add(-time.Second)

	if _, ok := st.get(vs.ID); ok {
		t.Fatal("an expired session must read as absent")
	}
	// Expiry has to free the slot too, or the cap would fill with dead entries.
	for i := 0; i < maxVoiceSessions; i++ {
		if _, err := st.create(voicegemini.DefaultModel, ""); err != nil {
			t.Fatalf("create %d after expiry: %v", i, err)
		}
	}
}

// Two dials with one id must not both pump audio into one provider connection.
func TestVoiceSessionClaimIsOneShot(t *testing.T) {
	vs := &voiceSession{ID: "vs-test"}
	if vs.claimed() {
		t.Fatal("the first claim must succeed")
	}
	if !vs.claimed() {
		t.Fatal("the second claim must be refused")
	}
}

func TestVoiceSessionIDsAreUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := randomVoiceID()
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestGeminiVoiceWSRejectsUnknownSession(t *testing.T) {
	s := withVoice(t, testServer(t))
	rec := httptest.NewRecorder()
	req := voiceReq(t, s, http.MethodGet, "/api/voice/gemini/ws?session=vs-nope", nil)
	s.handleGeminiVoiceWS(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestGeminiVoiceWSRequiresVoice(t *testing.T) {
	s := testServer(t)
	rec := httptest.NewRecorder()
	s.handleGeminiVoiceWS(rec, voiceReq(t, s, http.MethodGet, "/api/voice/gemini/ws?session=x", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestGeminiVoiceWSRejectsASecondRelay(t *testing.T) {
	s := withVoice(t, testServer(t))
	vs, err := s.voiceStore.create(voicegemini.DefaultModel, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	vs.claimed() // the first relay

	rec := httptest.NewRecorder()
	req := voiceReq(t, s, http.MethodGet, "/api/voice/gemini/ws?session="+vs.ID, nil)
	s.handleGeminiVoiceWS(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

func TestVoiceOriginOK(t *testing.T) {
	tests := []struct {
		name     string
		insecure bool
		origin   string
		host     string
		want     bool
	}{
		{name: "same origin", origin: "http://localhost:8765", host: "localhost:8765", want: true},
		{name: "cross origin", origin: "http://evil.test", host: "localhost:8765", want: false},
		// A browser always sends Origin on a WebSocket dial, so an absent one is
		// a non-browser client; the session id is the credential that matters.
		{name: "absent origin", origin: "", host: "localhost:8765", want: true},
		{name: "insecure allows anything", insecure: true, origin: "http://evil.test", host: "localhost:8765", want: true},
		{name: "unparseable origin", origin: "://nope", host: "localhost:8765", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewServerV2(Config{Addr: "127.0.0.1:0", Insecure: tt.insecure})
			req := voiceReq(t, s, http.MethodGet, "/api/voice/gemini/ws", nil)
			req.Host = tt.host
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if got := s.voiceOriginOK(req); got != tt.want {
				t.Errorf("voiceOriginOK() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGeminiCloseMessage(t *testing.T) {
	// The provider names the offending setup field in the close frame, which is
	// worth far more than the code alone.
	err := &websocket.CloseError{
		Code: 1007,
		Text: `Invalid JSON payload received. Unknown name "additionalProperties"`,
	}
	got := geminiCloseMessage(err)
	if !strings.Contains(got, "1007") || !strings.Contains(got, "additionalProperties") {
		t.Errorf("geminiCloseMessage() = %q, want the code and the provider's words", got)
	}

	if got := geminiCloseMessage(plainError{}); got != "provider connection closed" {
		t.Errorf("geminiCloseMessage(plain) = %q", got)
	}
}

func TestTruncateVoice(t *testing.T) {
	if got := truncateVoice("hello", 10); got != "hello" {
		t.Errorf("truncateVoice() = %q, want it unchanged", got)
	}
	if got := truncateVoice("hello world", 5); got != "hello…" {
		t.Errorf("truncateVoice() = %q", got)
	}
	// Multi-byte text must not be cut mid-rune.
	if got := truncateVoice("привіт", 3); got != "при…" {
		t.Errorf("truncateVoice() = %q, want a rune-safe cut", got)
	}
}

type plainError struct{}

func (plainError) Error() string { return "boom" }
