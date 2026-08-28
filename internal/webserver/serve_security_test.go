package webserver

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The default bind decides who can reach an endpoint that hands out a shell.
// Anything but loopback here means every host on the network can talk to it
// before a single line of auth code runs.
func TestDefaultAddrIsLoopback(t *testing.T) {
	host, _, err := net.SplitHostPort(DefaultAddr)
	if err != nil {
		t.Fatalf("DefaultAddr %q is not host:port: %v", DefaultAddr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		t.Errorf("DefaultAddr = %q, want a loopback bind", DefaultAddr)
	}
}

// --- the pair response must not carry the token ---

// /api/pair has no authentication, so whatever it returns is public. The token
// approves a pairing and then authenticates /ws/, which spawns the agent on a
// PTY: returning it here is handing out the session.
func TestHandleCreatePair_NoTokenInResponse(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			s := newTestServerV2(t)
			defer s.Shutdown(t.Context())

			var body *strings.Reader
			if method == http.MethodPost {
				body = strings.NewReader(`{"project":"/tmp/leak"}`)
			} else {
				body = strings.NewReader("")
			}
			r := httptest.NewRequest(method, "/api/pair", body)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.handleCreatePair(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", w.Code)
			}

			raw := w.Body.String()
			var fields map[string]any
			if err := json.Unmarshal([]byte(raw), &fields); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if _, ok := fields["token"]; ok {
				t.Errorf("response carries a token field: %s", raw)
			}

			// The server still holds a token for this pair — it just did not
			// travel. Prove it, so the test cannot pass on a broken pair.
			s.mu.Lock()
			token := s.activePairToken
			s.mu.Unlock()
			if token == "" {
				t.Fatal("no active token on the server; the pair never formed")
			}
			if strings.Contains(raw, token) {
				t.Errorf("token %q appears in the response body", token)
			}

			// The QR is part of the same public response, so the payload it
			// encodes must be clean too.
			qr, _ := fields["qr"].(string)
			if qr == "" {
				t.Fatal("qr missing from response")
			}
			png, err := base64.StdEncoding.DecodeString(qr)
			if err != nil {
				t.Fatalf("qr is not base64: %v", err)
			}
			if strings.Contains(string(png), token) {
				t.Error("token is embedded in the QR image payload")
			}
		})
	}
}

// The QR payload itself, decoded rather than scanned: no token key at all.
func TestBuildQRPayload_HasNoToken(t *testing.T) {
	payload, err := buildQRPayload("123456", "127.0.0.1:8765", "http://127.0.0.1:8765/pair")
	if err != nil {
		t.Fatalf("buildQRPayload: %v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := decoded["token"]; ok {
		t.Errorf("QR payload carries a token: %v", decoded)
	}
	if decoded["code"] != "123456" {
		t.Errorf("code = %q, want 123456", decoded["code"])
	}
}

// --- Origin validation ---

func TestCheckSameOrigin(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		fwdFor string
		origin string
		want   bool
	}{
		{name: "same origin", host: "127.0.0.1:8765", origin: "http://127.0.0.1:8765", want: true},
		{name: "same host different scheme", host: "127.0.0.1:8765", origin: "https://127.0.0.1:8765", want: true},
		{name: "absent origin", host: "127.0.0.1:8765", want: true},
		{name: "cross origin", host: "127.0.0.1:8765", origin: "http://evil.example.com", want: false},
		{name: "same name different port", host: "127.0.0.1:8765", origin: "http://127.0.0.1:9999", want: false},
		{name: "null origin", host: "127.0.0.1:8765", origin: "null", want: false},
		{name: "unparseable origin", host: "127.0.0.1:8765", origin: "://nope", want: false},
		{name: "forwarded host matches", host: "internal:8765", fwdFor: "pi.example.com", origin: "https://pi.example.com", want: true},
		{name: "forwarded host mismatch", host: "internal:8765", fwdFor: "pi.example.com", origin: "https://evil.example.com", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/ws/session", nil)
			r.Host = tt.host
			if tt.fwdFor != "" {
				r.Header.Set("X-Forwarded-Host", tt.fwdFor)
			}
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			if got := checkSameOrigin(r); got != tt.want {
				t.Errorf("checkSameOrigin() = %v, want %v", got, tt.want)
			}
		})
	}
}

// End to end over a real listener: an approved token is not enough if the
// upgrade is offered by a page on another origin.
func TestHandleWebSocket_RejectsCrossOriginUpgrade(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())

	code, token, err := s.BootstrapPair(t.TempDir())
	if err != nil {
		t.Fatalf("BootstrapPair: %v", err)
	}
	if _, err := s.pairingMgr.Approve(code); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(s.handleWebSocket))
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "http://")

	get := func(origin string) int {
		req, err := http.NewRequestWithContext(t.Context(), "GET",
			ts.URL+"/ws/sec-test?token="+url.QueryEscape(token), nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		// A real upgrade handshake, so the request reaches CheckOrigin.
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			t.Fatalf("round trip: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if got := get("http://evil.example.com"); got != http.StatusForbidden {
		t.Errorf("cross-origin upgrade status = %d, want 403", got)
	}
	// Same-origin and no-Origin requests get past CheckOrigin; the handshake
	// then succeeds (101) or the PTY fails, but it is never a 403.
	if got := get("http://" + host); got == http.StatusForbidden {
		t.Error("same-origin upgrade was rejected")
	}
	if got := get(""); got == http.StatusForbidden {
		t.Error("upgrade with no Origin header was rejected")
	}
}

// --- cookie policy ---

func TestPairCookiesAreSameSiteStrict(t *testing.T) {
	assertStrict := func(t *testing.T, w *httptest.ResponseRecorder) {
		t.Helper()
		for _, c := range w.Result().Cookies() {
			if c.Name != "pi_token" {
				continue
			}
			if c.SameSite != http.SameSiteStrictMode {
				t.Errorf("pi_token SameSite = %v, want Strict", c.SameSite)
			}
			if !c.HttpOnly {
				t.Error("pi_token should stay HttpOnly")
			}
			return
		}
		t.Fatal("no pi_token cookie was set")
	}

	t.Run("submit", func(t *testing.T) {
		s := newTestServerV2(t)
		defer s.Shutdown(t.Context())
		code, _, err := s.BootstrapPair("/tmp/cookie")
		if err != nil {
			t.Fatalf("BootstrapPair: %v", err)
		}
		r := httptest.NewRequest("POST", "/api/pair/submit", strings.NewReader(`{"code":"`+code+`"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handleSubmitPairCode(w, r)
		assertStrict(t, w)
	})

	t.Run("pair redirect", func(t *testing.T) {
		s := newTestServerV2(t)
		defer s.Shutdown(t.Context())
		code, token, err := s.BootstrapPair("/tmp/cookie")
		if err != nil {
			t.Fatalf("BootstrapPair: %v", err)
		}
		if _, err := s.pairingMgr.Approve(code); err != nil {
			t.Fatalf("Approve: %v", err)
		}
		r := httptest.NewRequest("GET", "/pair?token="+url.QueryEscape(token), nil)
		w := httptest.NewRecorder()
		s.handlePair(w, r)
		assertStrict(t, w)
	})
}

// --- pairing code attempt limiting ---

// A 6-digit code is only worth something if guesses are expensive. After the
// budget is spent every pending pair is gone and further attempts are refused,
// including the one that would have worked.
func TestApprove_LocksOutAfterRepeatedBadCodes(t *testing.T) {
	pm := NewPairingManager(5 * time.Minute)
	code, _, _, err := pm.CreatePair("/tmp/lockout")
	if err != nil {
		t.Fatalf("CreatePair: %v", err)
	}

	for i := 1; i < pm.maxFailures; i++ {
		if _, err := pm.Approve("000000"); err == nil {
			t.Fatalf("attempt %d: wrong code was approved", i)
		} else if errors.Is(err, ErrTooManyAttempts) {
			t.Fatalf("attempt %d: locked out early", i)
		}
	}

	// The attempt that spends the budget reports the lockout, not a plain
	// rejection.
	if _, err := pm.Approve("000000"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("final attempt error = %v, want ErrTooManyAttempts", err)
	}

	// The code being ground against is destroyed, not merely throttled.
	if _, err := pm.Approve(code); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("approve during lockout = %v, want ErrTooManyAttempts", err)
	}
}

// The lockout is a window, not a permanent brick: an operator who fat-fingers
// the code gets another go.
func TestApprove_LockoutExpires(t *testing.T) {
	pm := NewPairingManager(5 * time.Minute)
	pm.lockout = 10 * time.Millisecond

	for i := 0; i < pm.maxFailures; i++ {
		if _, err := pm.Approve("000000"); err == nil {
			t.Fatalf("attempt %d: wrong code was approved", i)
		}
	}

	code, token, _, err := pm.CreatePair("/tmp/after-lockout")
	if err != nil {
		t.Fatalf("CreatePair: %v", err)
	}
	if _, err := pm.Approve(code); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("still inside the window: err = %v, want ErrTooManyAttempts", err)
	}

	time.Sleep(20 * time.Millisecond)

	got, err := pm.Approve(code)
	if err != nil {
		t.Fatalf("approve after lockout: %v", err)
	}
	if got != token {
		t.Errorf("approved token = %q, want %q", got, token)
	}
}

// A successful pairing clears the budget, so yesterday's typos cannot combine
// with today's to lock a working session out.
func TestApprove_SuccessResetsFailureBudget(t *testing.T) {
	pm := NewPairingManager(5 * time.Minute)

	for i := 0; i < pm.maxFailures-1; i++ {
		if _, err := pm.Approve("000000"); err == nil {
			t.Fatalf("attempt %d: wrong code was approved", i)
		}
	}

	code, _, _, err := pm.CreatePair("/tmp/reset")
	if err != nil {
		t.Fatalf("CreatePair: %v", err)
	}
	if _, err := pm.Approve(code); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	// One more wrong code must be an ordinary rejection, not a lockout.
	if _, err := pm.Approve("000000"); errors.Is(err, ErrTooManyAttempts) {
		t.Error("failure budget survived a successful approval")
	}
}

// Minting a fresh pair must not hand the attacker a fresh budget: that is the
// obvious way to defeat attempt limiting.
func TestApprove_NewPairDoesNotResetFailureBudget(t *testing.T) {
	pm := NewPairingManager(5 * time.Minute)

	for i := 0; i < pm.maxFailures-1; i++ {
		if _, err := pm.Approve("000000"); err == nil {
			t.Fatalf("attempt %d: wrong code was approved", i)
		}
	}
	if _, _, _, err := pm.CreatePair("/tmp/no-reset"); err != nil {
		t.Fatalf("CreatePair: %v", err)
	}
	if _, err := pm.Approve("000000"); !errors.Is(err, ErrTooManyAttempts) {
		t.Errorf("err = %v, want ErrTooManyAttempts — CreatePair reset the budget", err)
	}
}

// The HTTP layer surfaces the lockout as a rejection rather than a 500 or a
// stack trace.
func TestHandleSubmitPairCode_LockoutRejects(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	code, _, err := s.BootstrapPair("/tmp/http-lockout")
	if err != nil {
		t.Fatalf("BootstrapPair: %v", err)
	}

	submit := func(c string) int {
		r := httptest.NewRequest("POST", "/api/pair/submit", strings.NewReader(`{"code":"`+c+`"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handleSubmitPairCode(w, r)
		return w.Code
	}

	for i := 0; i < s.pairingMgr.maxFailures; i++ {
		if got := submit("000000"); got != http.StatusBadRequest {
			t.Fatalf("attempt %d status = %d, want 400", i, got)
		}
	}
	if got := submit(code); got != http.StatusBadRequest {
		t.Errorf("status after lockout = %d, want 400", got)
	}
}
