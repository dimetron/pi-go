package webserver

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// --- helpers to create a test ServerV2 ---

func newTestServerV2(t *testing.T) *ServerV2 {
	t.Helper()
	return NewServerV2(Config{Addr: ":0", PairingTimeout: time.Minute, Logger: slog.Default()})
}

func decodeBody[T any](t *testing.T, r *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return v
}

// --- request helpers ---

func TestIsJSONRequest_ContentType(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Content-Type", "application/json")
	if !isJSONRequest(r) {
		t.Error("expected true for application/json Content-Type")
	}
}

func TestIsJSONRequest_AcceptHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept", "application/json")
	if !isJSONRequest(r) {
		t.Error("expected true for application/json Accept")
	}
}

func TestIsJSONRequest_False(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if isJSONRequest(r) {
		t.Error("expected false without JSON headers")
	}
}

// --- parseSubmittedCode / request parsing ---

type approvedResp struct {
	Approved bool   `json:"approved"`
	Status   string `json:"status"`
}

type pairCreateResp struct {
	Code  string `json:"code"`
	Token string `json:"token"`
	QR    string `json:"qr"`
}

func TestParseSubmittedCode_JSON(t *testing.T) {
	body := strings.NewReader(`{"code":"  654321  "}`)
	r := httptest.NewRequest("POST", "/", body)
	r.Header.Set("Content-Type", "application/json")
	code, err := parseSubmittedCode(r)
	if err != nil {
		t.Fatalf("parseSubmittedCode failed: %v", err)
	}
	if code != "654321" {
		t.Errorf("expected trimmed 654321, got %q", code)
	}
}

func TestParseSubmittedCode_Form(t *testing.T) {
	body := strings.NewReader("code=  111222  ")
	r := httptest.NewRequest("POST", "/", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	code, err := parseSubmittedCode(r)
	if err != nil {
		t.Fatalf("parseSubmittedCode failed: %v", err)
	}
	if code != "111222" {
		t.Errorf("expected trimmed 111222, got %q", code)
	}
}

func TestParseSubmittedCode_InvalidJSON(t *testing.T) {
	body := strings.NewReader(`{invalid}`)
	r := httptest.NewRequest("POST", "/", body)
	r.Header.Set("Content-Type", "application/json")
	_, err := parseSubmittedCode(r)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestRespondPairSubmitError_JSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Accept", "application/json")
	respondPairSubmitError(w, r, "bad code")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "bad code") {
		t.Fatalf("expected body to contain error, got %q", w.Body.String())
	}
}

func TestRespondPairSubmitError_HTML(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", nil)
	respondPairSubmitError(w, r, "bad code")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "/pair?error=") {
		t.Fatalf("expected redirect to pair error, got %q", loc)
	}
}

// --- request origin/host helpers ---

func TestRequestOriginAndHost_DefaultHTTP(t *testing.T) {
	r := httptest.NewRequest("GET", "http://pi-go.local/pair", nil)
	origin, host := requestOriginAndHost(r)
	if origin != "http://pi-go.local" {
		t.Errorf("expected http://pi-go.local, got %q", origin)
	}
	if host != "pi-go.local" {
		t.Errorf("expected pi-go.local, got %q", host)
	}
}

func TestRequestOriginAndHost_Forwarded(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost/pair", nil)
	r.Header.Set("X-Forwarded-Host", "pi-go")
	r.Header.Set("X-Forwarded-Proto", "https")
	origin, host := requestOriginAndHost(r)
	if origin != "https://pi-go" {
		t.Errorf("expected https://pi-go, got %q", origin)
	}
	if host != "pi-go" {
		t.Errorf("expected pi-go, got %q", host)
	}
}

func TestRequestOriginAndHost_EmptyHost(t *testing.T) {
	r := httptest.NewRequest("GET", "/pair", nil)
	r.Host = ""
	origin, host := requestOriginAndHost(r)
	if origin != "http://pi-go" {
		t.Errorf("expected http://pi-go, got %q", origin)
	}
	if host != "pi-go" {
		t.Errorf("expected pi-go, got %q", host)
	}
}

// --- ServerV2 status / pairing endpoints ---

func TestServerV2_HandleStatus_Pending(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	code, token, err := s.BootstrapPair("/tmp/test")
	if err != nil {
		t.Fatalf("BootstrapPair: %v", err)
	}
	if code == "" || token == "" {
		t.Fatalf("expected code and token")
	}

	r := httptest.NewRequest("GET", "/api/status?token="+url.QueryEscape(token), nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := decodeBody[approvedResp](t, w)
	if resp.Status != "pending" {
		t.Errorf("expected pending, got %q", resp.Status)
	}
}

func TestServerV2_HandleStatus_Missing(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	r := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestServerV2_HandleStatus_Unknown(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	r := httptest.NewRequest("GET", "/api/status?token=does-not-exist", nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := decodeBody[approvedResp](t, w)
	if resp.Status != "unknown" {
		t.Errorf("expected unknown, got %q", resp.Status)
	}
}

func TestServerV2_HandleStatus_Approved(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	code, token, err := s.BootstrapPair("/tmp/test")
	if err != nil {
		t.Fatalf("BootstrapPair: %v", err)
	}
	if _, err := s.pairingMgr.Approve(code); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	r := httptest.NewRequest("GET", "/api/status?token="+url.QueryEscape(token), nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := decodeBody[approvedResp](t, w)
	if resp.Status != "approved" {
		t.Errorf("expected approved, got %q", resp.Status)
	}
}

func TestServerV2_HandleCreatePair_POST(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	body := strings.NewReader(`{"project":"/tmp/project"}`)
	r := httptest.NewRequest("POST", "/api/pair", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleCreatePair(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := decodeBody[pairCreateResp](t, w)
	if len(resp.Code) != 6 {
		t.Errorf("expected 6 digit code, got %q", resp.Code)
	}
	if resp.Token == "" || resp.QR == "" {
		t.Errorf("expected token and qr to be set")
	}
}

func TestServerV2_HandleCreatePair_GET(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	r := httptest.NewRequest("GET", "/api/pair", nil)
	w := httptest.NewRecorder()
	s.handleCreatePair(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := decodeBody[pairCreateResp](t, w)
	if len(resp.Code) != 6 {
		t.Errorf("expected 6 digit code, got %q", resp.Code)
	}
}

func TestServerV2_HandleCreatePair_InvalidJSON(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	body := strings.NewReader(`{invalid}`)
	r := httptest.NewRequest("POST", "/api/pair", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleCreatePair(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestServerV2_HandleCreatePair_ReusesActivePair(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	r1 := httptest.NewRequest("GET", "/api/pair", nil)
	w1 := httptest.NewRecorder()
	s.handleCreatePair(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w1.Code)
	}
	resp1 := decodeBody[pairCreateResp](t, w1)

	// Second call should reuse the same active pair
	r2 := httptest.NewRequest("GET", "/api/pair", nil)
	w2 := httptest.NewRecorder()
	s.handleCreatePair(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}
	resp2 := decodeBody[pairCreateResp](t, w2)

	if resp1.Code != resp2.Code || resp1.Token != resp2.Token {
		t.Fatalf("expected active pair reuse, got %+v vs %+v", resp1, resp2)
	}
}

func TestServerV2_HandlePair_NoToken(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	r := httptest.NewRequest("GET", "/pair", nil)
	w := httptest.NewRecorder()
	s.handlePair(w, r)
	if w.Code != http.StatusOK {
		// With embedded static files it should work
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestServerV2_HandlePair_ApprovedRedirect(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	code, token, err := s.BootstrapPair("/tmp/test")
	if err != nil {
		t.Fatalf("BootstrapPair: %v", err)
	}
	if _, err := s.pairingMgr.Approve(code); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	_ = token

	r := httptest.NewRequest("GET", "/pair?token="+url.QueryEscape(token), nil)
	w := httptest.NewRecorder()
	s.handlePair(w, r)
	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("expected redirect to /, got %q", loc)
	}
	found := false
	for _, c := range w.Result().Cookies() {
		if c.Name == "pi_token" && c.Value == token {
			found = true
		}
	}
	if !found {
		t.Error("expected pi_token cookie")
	}
}

func TestServerV2_HandlePair_PendingNoRedirect(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	_, token, err := s.BootstrapPair("/tmp/test")
	if err != nil {
		t.Fatalf("BootstrapPair: %v", err)
	}
	r := httptest.NewRequest("GET", "/pair?token="+url.QueryEscape(token), nil)
	w := httptest.NewRecorder()
	s.handlePair(w, r)
	if w.Code == http.StatusSeeOther {
		t.Error("pending token should not redirect")
	}
}

func TestServerV2_HandleSubmitPairCode_JSON(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	code, _, err := s.BootstrapPair("/tmp/test")
	if err != nil {
		t.Fatalf("BootstrapPair: %v", err)
	}
	body := strings.NewReader(`{"code":"` + code + `"}`)
	r := httptest.NewRequest("POST", "/api/pair/submit", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleSubmitPairCode(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := decodeBody[approvedResp](t, w)
	if resp.Status != "approved" {
		t.Errorf("expected approved, got %q", resp.Status)
	}
}

func TestServerV2_HandleSubmitPairCode_HTML(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	code, _, err := s.BootstrapPair("/tmp/test")
	if err != nil {
		t.Fatalf("BootstrapPair: %v", err)
	}
	body := strings.NewReader("code=" + url.QueryEscape(code))
	r := httptest.NewRequest("POST", "/api/pair/submit", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.handleSubmitPairCode(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("expected redirect to /, got %q", loc)
	}
}

func TestServerV2_HandleSubmitPairCode_InvalidJSON(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	body := strings.NewReader(`{invalid}`)
	r := httptest.NewRequest("POST", "/api/pair/submit", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleSubmitPairCode(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestServerV2_HandleSubmitPairCode_InvalidCode(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	body := strings.NewReader(`{"code":"000000"}`)
	r := httptest.NewRequest("POST", "/api/pair/submit", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleSubmitPairCode(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestServerV2_HandleSubmitPairCode_InvalidCode_HTML(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	body := strings.NewReader("code=000000")
	r := httptest.NewRequest("POST", "/api/pair/submit", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.handleSubmitPairCode(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "/pair?error=") {
		t.Fatalf("expected redirect to pair error, got %q", loc)
	}
}

// --- routing / misc server behavior ---

func TestServerV2_HandleIndex_Root(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestServerV2_HandleIndex_NotRoot(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	r := httptest.NewRequest("GET", "/nonexistent", nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestServerV2_HandleWebSocket_MissingSessionID(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	r := httptest.NewRequest("GET", "/ws/", nil)
	w := httptest.NewRecorder()
	s.handleWebSocket(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestServerV2_Addr_Default(t *testing.T) {
	s := NewServerV2(Config{Addr: ":9999"})
	// Before Start, Addr should return cfg.Addr
	if s.Addr() != ":9999" {
		t.Errorf("expected :9999, got %q", s.Addr())
	}
}

func TestServerV2_Addr_AfterListen(t *testing.T) {
	s := NewServerV2(Config{Addr: "127.0.0.1:12345"})
	s.listenAddr = "127.0.0.1:12345"
	if addr := s.Addr(); addr != "127.0.0.1:12345" {
		t.Errorf("expected 127.0.0.1:12345, got %q", addr)
	}
}

func TestServerV2_SessionManager(t *testing.T) {
	s := newTestServerV2(t)
	if s.SessionManager() == nil {
		t.Error("SessionManager should not be nil")
	}
}

func TestServerV2_BootstrapPair(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	code, token, err := s.BootstrapPair("/tmp/test")
	if err != nil {
		t.Fatalf("BootstrapPair: %v", err)
	}
	if len(code) != 6 {
		t.Errorf("expected 6 digit code, got %q", code)
	}
	if token == "" {
		t.Error("expected token")
	}
}

// --- logging middleware / response capture ---

func TestLoggingMiddleware_OK(t *testing.T) {
	s := newTestServerV2(t)
	h := s.loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestLoggingMiddleware_ClientError(t *testing.T) {
	s := newTestServerV2(t)
	h := s.loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	r := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestLoggingMiddleware_ServerError(t *testing.T) {
	s := newTestServerV2(t)
	h := s.loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	r := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestResponseCapture_WriteHeader(t *testing.T) {
	rr := httptest.NewRecorder()
	rc := &responseCapture{ResponseWriter: rr, status: http.StatusOK}
	rc.WriteHeader(http.StatusNotFound)
	if rc.status != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rc.status)
	}
}

func TestResponseCapture_Flush(t *testing.T) {
	// httptest.ResponseRecorder implements Flusher
	rr := httptest.NewRecorder()
	rc := &responseCapture{ResponseWriter: rr}
	rc.Flush()
}

// --- pty / pipe wrapper behavior ---

func TestPipeWrapper_ReadWriteClose(t *testing.T) {
	pr, pw := io.Pipe()
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stderr: %v", err)
	}
	defer stderrR.Close()
	defer stderrW.Close()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stdin: %v", err)
	}
	defer stdinR.Close()
	defer stdinW.Close()

	wrapper := &pipeWrapper{stdin: stdinW, stdout: pr, stderr: stderrR}

	go func() {
		_, _ = pw.Write([]byte("hello"))
		_ = pw.Close()
	}()
	buf := make([]byte, 5)
	n, err := wrapper.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("Read: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Errorf("expected hello, got %q", string(buf[:n]))
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		b := make([]byte, 5)
		_, _ = stdinR.Read(b)
		if string(b) != "world" {
			t.Errorf("expected world, got %q", string(b))
		}
	}()
	if _, err := wrapper.Write([]byte("world")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	<-done
	_ = wrapper.Close()
}

func TestPtyBridge_Alive_NewBridge(t *testing.T) {
	b := NewPtyBridge("/tmp", "", "", nil, false, nil)
	if b.Alive() {
		t.Error("new bridge without cmd should not be alive")
	}
}

func TestPtyBridge_Alive_AfterClose(t *testing.T) {
	b := NewPtyBridge("/tmp", "", "", nil, false, nil)
	_ = b.Close()
	if b.Alive() {
		t.Error("closed bridge should not be alive")
	}
}

func TestPtyBridge_Resize_NilPtyFile(t *testing.T) {
	b := NewPtyBridge("/tmp", "", "", nil, false, nil)
	b.Resize(80, 24)
}

func TestPtyBridge_Resize_NonOsFile(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()
	b := NewPtyBridge("/tmp", "", "", nil, false, nil)
	b.ptyFile = struct {
		io.Reader
		io.Writer
		io.Closer
	}{Reader: pr, Writer: pw, Closer: pr}
	// Should not panic - silently ignores non-*os.File
	b.Resize(80, 24)
}

func TestNewPtyPool(t *testing.T) {
	pool := NewPtyPool(nil)
	if pool == nil {
		t.Fatal("NewPtyPool should not return nil")
	}
	if pool.bridges == nil {
		t.Error("bridges map should be initialized")
	}
}

func TestPtyPool_CloseAll_Empty(t *testing.T) {
	pool := NewPtyPool(nil)
	pool.CloseAll() // should not panic on empty pool
}

func TestNewServerV2_Defaults(t *testing.T) {
	s := NewServerV2(Config{})
	if s.Addr() != ":8080" {
		t.Errorf("expected :8080, got %q", s.Addr())
	}
	if s.pairingMgr == nil || s.sessions == nil || s.ptyPool == nil {
		t.Fatal("expected managers to be initialized")
	}
}

func TestServer_PairingManager(t *testing.T) {
	s := newTestServerV2(t)
	if s.PairingManager() == nil {
		t.Error("PairingManager should not be nil")
	}
}

func TestServerV2_CreateSession_Approved(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	code, token, err := s.BootstrapPair("/tmp/test")
	if err != nil {
		t.Fatalf("BootstrapPair: %v", err)
	}
	if _, err := s.pairingMgr.Approve(code); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	sess, err := s.SessionManager().CreateSession("/tmp/test", token)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.Project != "/tmp/test" {
		t.Errorf("expected /tmp/test, got %q", sess.Project)
	}
}

// --- handlers.go Server (legacy server) ---

func TestServer_HandlePair_NoToken(t *testing.T) {
	s := NewServer(Config{})
	r := httptest.NewRequest("GET", "/pair", nil)
	w := httptest.NewRecorder()
	s.handlePair(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestServer_HandlePair_ApprovedRedirect(t *testing.T) {
	s := NewServer(Config{})
	code, token, _, err := s.pairingManager.CreatePair("/tmp/test")
	if err != nil {
		t.Fatalf("CreatePair: %v", err)
	}
	if _, err := s.pairingManager.Approve(code); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	r := httptest.NewRequest("GET", "/pair?token="+url.QueryEscape(token), nil)
	w := httptest.NewRecorder()
	s.handlePair(w, r)
	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("expected redirect to /, got %q", loc)
	}
}

func TestServer_HandlePair_PendingNoRedirect(t *testing.T) {
	s := NewServer(Config{})
	_, token, _, err := s.pairingManager.CreatePair("/tmp/test")
	if err != nil {
		t.Fatalf("CreatePair: %v", err)
	}
	r := httptest.NewRequest("GET", "/pair?token="+url.QueryEscape(token), nil)
	w := httptest.NewRecorder()
	s.handlePair(w, r)
	if w.Code == http.StatusSeeOther {
		t.Error("pending token should not redirect")
	}
}

func TestServer_HandleCreatePair_MethodNotAllowed(t *testing.T) {
	s := NewServer(Config{})
	r := httptest.NewRequest("PUT", "/api/pair", nil)
	w := httptest.NewRecorder()
	s.handleCreatePair(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// --- handlers.go Server.handleWebSocket ---

func TestServer_HandleWebSocket_MissingSessionID(t *testing.T) {
	s := NewServer(Config{})
	r := httptest.NewRequest("GET", "/ws/", nil)
	w := httptest.NewRecorder()
	s.handleWebSocket(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestServer_HandleIndex_Root(t *testing.T) {
	s := NewServer(Config{})
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, r)
	// No token -> redirect to /pair
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/pair" {
		t.Errorf("expected redirect to /pair, got %q", loc)
	}
}

func TestServer_HandleIndex_NotFound(t *testing.T) {
	s := NewServer(Config{})
	r := httptest.NewRequest("GET", "/other", nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestServer_HandleStatus_MissingToken(t *testing.T) {
	s := NewServer(Config{})
	r := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestServer_HandleStatus_Approved(t *testing.T) {
	s := NewServer(Config{})
	code, token, _, err := s.pairingManager.CreatePair("/tmp/test")
	if err != nil {
		t.Fatalf("CreatePair: %v", err)
	}
	if _, err := s.pairingManager.Approve(code); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	r := httptest.NewRequest("GET", "/api/status?token="+url.QueryEscape(token), nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp approvedResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "approved" {
		t.Errorf("expected approved, got %q", resp.Status)
	}
}

// --- static assets ---

func TestStaticAssetsFileServer_Disk(t *testing.T) {
	h := staticAssetsFileServer(filepath.Join("testdata", "web-static"))
	if h == nil {
		t.Fatal("should return a handler for disk assets")
	}
}

func TestServeStaticPage_Embedded(t *testing.T) {
	r := httptest.NewRequest("GET", "/pair", nil)
	w := httptest.NewRecorder()
	serveStaticPage(w, r, "", "pair.html")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestServeStaticPage_Disk_Missing(t *testing.T) {
	r := httptest.NewRequest("GET", "/pair", nil)
	w := httptest.NewRecorder()
	serveStaticPage(w, r, filepath.Join("testdata", "missing-dir"), "pair.html")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestServeStaticPage_EmbeddedMissing(t *testing.T) {
	r := httptest.NewRequest("GET", "/missing", nil)
	w := httptest.NewRecorder()
	serveStaticPage(w, r, "", "nonexistent.html")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- session manager ---

func TestSessionManager_CleanupExpired(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()
	_, err := sm.CreateSession("/tmp/test", "token")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// CleanupExpired should not remove recent sessions
	sm.CleanupExpired()
	if len(sm.sessions) == 0 {
		t.Fatal("expected session to remain")
	}
}

// --- Full integration test via httptest ---

func TestServerV2_FullPairingFlow(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	h := s.loggingMiddleware(http.NewServeMux())
	_ = h

	body := strings.NewReader(`{"project":"/tmp/project"}`)
	r := httptest.NewRequest("POST", "/api/pair", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleCreatePair(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	pr := decodeBody[pairCreateResp](t, w)

	statusReq := httptest.NewRequest("GET", "/api/status?token="+url.QueryEscape(pr.Token), nil)
	statusW := httptest.NewRecorder()
	s.handleStatus(statusW, statusReq)
	sr := decodeBody[approvedResp](t, statusW)
	if sr.Status != "pending" {
		t.Errorf("expected pending, got %q", sr.Status)
	}

	submitBody := strings.NewReader(`{"code":"` + pr.Code + `"}`)
	submitReq := httptest.NewRequest("POST", "/api/pair/submit", submitBody)
	submitReq.Header.Set("Content-Type", "application/json")
	submitW := httptest.NewRecorder()
	s.handleSubmitPairCode(submitW, submitReq)
	if submitW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", submitW.Code)
	}
	appr := decodeBody[approvedResp](t, submitW)
	if appr.Status != "approved" {
		t.Errorf("expected approved, got %q", appr.Status)
	}

	pairReq := httptest.NewRequest("GET", "/pair?token="+url.QueryEscape(pr.Token), nil)
	pairW := httptest.NewRecorder()
	s.handlePair(pairW, pairReq)
	if pairW.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", pairW.Code)
	}
}

func TestPtyBridge_CopyPtyToWS(t *testing.T) {
	// Create an io.Pipe to simulate PTY output
	pr, pw := io.Pipe()
	defer pw.Close()
	b := NewPtyBridge("/tmp", "", "", nil, false, nil)
	b.ptyFile = struct {
		io.Reader
		io.Writer
		io.Closer
	}{Reader: pr, Writer: pw, Closer: pr}

	// Set up WebSocket server that runs copyPtyToWS
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer conn.Close()
		wsDone := make(chan struct{})
		go b.copyPtyToWS(conn, wsDone)
		<-time.After(200 * time.Millisecond)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Write to PTY, then close to end copyPtyToWS
	go func() {
		_, _ = pw.Write([]byte("hello"))
		_ = pw.Close()
	}()

	var msg WSMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}
	if msg.Type != "output" {
		t.Errorf("type = %q, want output", msg.Type)
	}
	if msg.Data != "hello" {
		t.Errorf("data = %q, want hello", msg.Data)
	}
}

func TestPtyBridge_CopyWSToPty(t *testing.T) {
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer stdinR.Close()
	defer stdinW.Close()

	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()
	b := NewPtyBridge("/tmp", "", "", nil, false, nil)
	b.ptyFile = struct {
		io.Reader
		io.Writer
		io.Closer
	}{Reader: pr, Writer: stdinW, Closer: pr}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer conn.Close()
		wsDone := make(chan struct{})
		go b.copyWSToPty(conn, wsDone)
		<-time.After(300 * time.Millisecond)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	msg := WSMessage{Type: "input", Data: "test input"}
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	buf := make([]byte, len("test input"))
	if _, err := io.ReadFull(stdinR, buf); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(buf) != "test input" {
		t.Errorf("got %q, want test input", string(buf))
	}
}

func TestPtyBridge_CopyWSToPty_Resize(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()
	b := NewPtyBridge("/tmp", "", "", nil, false, nil)
	b.ptyFile = struct {
		io.Reader
		io.Writer
		io.Closer
	}{Reader: pr, Writer: pw, Closer: pr}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer conn.Close()
		wsDone := make(chan struct{})
		go b.copyWSToPty(conn, wsDone)
		<-time.After(200 * time.Millisecond)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	msg := WSMessage{Type: "resize", Data: "120,40"}
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
}

func TestPtyBridge_CopyWSToPty_RawData(t *testing.T) {
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer stdinR.Close()
	defer stdinW.Close()

	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()
	b := NewPtyBridge("/tmp", "", "", nil, false, nil)
	b.ptyFile = struct {
		io.Reader
		io.Writer
		io.Closer
	}{Reader: pr, Writer: stdinW, Closer: pr}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer conn.Close()
		wsDone := make(chan struct{})
		go b.copyWSToPty(conn, wsDone)
		<-time.After(300 * time.Millisecond)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte("raw input")); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	buf := make([]byte, len("raw input"))
	if _, err := io.ReadFull(stdinR, buf); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(buf) != "raw input" {
		t.Errorf("got %q, want raw input", string(buf))
	}
}

func TestPtyBridge_Start_NonexistentDir(t *testing.T) {
	b := NewPtyBridge("/nonexistent/path/that/doesnt/exist", "", "", nil, false, nil)
	if err := b.Start(); err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}

func TestPtyBridge_Start_ErrorSentToWS(t *testing.T) {
	b := NewPtyBridge("/nonexistent/path/that/doesnt/exist", "", "", nil, false, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer conn.Close()
		b.HandleWebSocket(conn, "session-err")
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	var msg WSMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}
	if msg.Type != "error" {
		t.Errorf("type = %q, want error", msg.Type)
	}
}

func TestPtyBridge_Close_WithPtyFile(t *testing.T) {
	pr, pw := io.Pipe()
	b := NewPtyBridge("/tmp", "", "", nil, false, nil)
	b.ptyFile = struct {
		io.Reader
		io.Writer
		io.Closer
	}{Reader: pr, Writer: pw, Closer: pr}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if b.ptyFile != nil {
		t.Error("ptyFile should be nil after Close")
	}
}

func TestPtyPool_CloseAll_WithBridges(t *testing.T) {
	pool := NewPtyPool(nil)
	b1 := NewPtyBridge("/tmp", "", "", nil, false, nil)
	b2 := NewPtyBridge("/tmp", "", "", nil, false, nil)
	pool.bridges["1"] = b1
	pool.bridges["2"] = b2
	pool.CloseAll()
	if len(pool.bridges) != 0 {
		t.Fatalf("expected empty pool, got %d", len(pool.bridges))
	}
}

func TestServer_HandlePair_QueryToken_Approved(t *testing.T) {
	s := NewServer(Config{})
	code, token, _, err := s.pairingManager.CreatePair("/tmp/test")
	if err != nil {
		t.Fatalf("CreatePair: %v", err)
	}
	if _, err := s.pairingManager.Approve(code); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	r := httptest.NewRequest("GET", "/?token="+url.QueryEscape(token), nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, r)
	if w.Code == http.StatusSeeOther {
		t.Errorf("no token should redirect, got %d", w.Code)
	}
}

func TestServer_HandleIndex_QueryToken(t *testing.T) {
	s := NewServer(Config{})
	code, token, _, err := s.pairingManager.CreatePair("/tmp/test")
	if err != nil {
		t.Fatalf("CreatePair: %v", err)
	}
	if _, err := s.pairingManager.Approve(code); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	r := httptest.NewRequest("GET", "/?token="+url.QueryEscape(token), nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, r)
	if w.Code == http.StatusSeeOther {
		t.Error("approved query token should not redirect")
	}
}

func TestServer_HandleCreatePair_Valid(t *testing.T) {
	s := NewServer(Config{})
	body := strings.NewReader(`{"project":"/tmp/test"}`)
	r := httptest.NewRequest("POST", "/api/pair", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleCreatePair(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := decodeBody[pairCreateResp](t, w)
	if len(resp.Code) != 6 {
		t.Errorf("expected 6 digit code, got %q", resp.Code)
	}
}

func TestServer_HandleCreatePair_EmptyProject(t *testing.T) {
	s := NewServer(Config{})
	body := strings.NewReader(`{"project":""}`)
	r := httptest.NewRequest("POST", "/api/pair", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleCreatePair(w, r)
	// Empty project defaults to "." so the handler still succeeds.
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestServer_HandleCreatePair_InvalidJSON(t *testing.T) {
	s := NewServer(Config{})
	body := strings.NewReader(`{invalid}`)
	r := httptest.NewRequest("POST", "/api/pair", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleCreatePair(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestServer_ValidateAuth_Approved(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	code, token, err := s.BootstrapPair("/tmp/test")
	if err != nil {
		t.Fatalf("BootstrapPair: %v", err)
	}
	if _, err := s.pairingMgr.Approve(code); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !s.pairingMgr.IsApproved(token) {
		t.Error("approved token should pass auth check")
	}
}

func TestServer_HandleWebSocket_NoPanicOnUpgradeFail(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	r := httptest.NewRequest("GET", "/ws/session-1", nil)
	w := httptest.NewRecorder()
	s.handleWebSocket(w, r)
	// Handler runs without panic — that's the test
}

func TestPtyBridge_AttachWebSocket(t *testing.T) {
	b := NewPtyBridge("/tmp", "", "", nil, false, nil)
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()
	b.ptyFile = struct {
		io.Reader
		io.Writer
		io.Closer
	}{Reader: pr, Writer: pw, Closer: pr}
	// Simulate cmd being set (so Alive() can work)
	b.cmd = &exec.Cmd{}
	_ = pw
}

// --- PtyBridge copyPtyToWS error paths ---

type hijackableRecorder struct{ *httptest.ResponseRecorder }

func (h *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("test hijack")
}

func TestServerV2_HandleWebSocket_UpgradeFail(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	w := &hijackableRecorder{httptest.NewRecorder()}
	r := httptest.NewRequest("GET", "/ws/session-1", nil)
	s.handleWebSocket(w, r)
	if w.Code == http.StatusSwitchingProtocols {
		t.Error("expected error from test hijack")
	}
}

func TestWebSocketHandler_HandleWebSocket_WithToken(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()
	wsh := NewWebSocketHandler(sm)

	// With token but no actual WS upgrade — reaches upgrade which fails
	r := httptest.NewRequest("GET", "/ws/session-1?token=test-token", nil)
	w := httptest.NewRecorder()
	wsh.HandleWebSocket(w, r)

	// Upgrade fails but auth passed — that's what we're testing
	if w.Code == http.StatusUnauthorized {
		t.Error("with token should pass auth check")
	}
}
