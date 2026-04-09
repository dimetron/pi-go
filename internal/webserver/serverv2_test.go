package webserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// --- helpers to create a test ServerV2 ---

func newTestServerV2(t *testing.T) *ServerV2 {
	t.Helper()
	return NewServerV2(Config{
		Addr:           "127.0.0.1:0",
		PairingTimeout: 5 * time.Minute,
		Project:        "/tmp/test",
	})
}

// --- isJSONRequest ---

func TestIsJSONRequest_ContentType(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Content-Type", "application/json")
	if !isJSONRequest(r) {
		t.Error("expected true for application/json Content-Type")
	}
}

func TestIsJSONRequest_ContentTypeWithCharset(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	if !isJSONRequest(r) {
		t.Error("expected true for application/json with charset")
	}
}

func TestIsJSONRequest_AcceptHeader(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Accept", "application/json")
	if !isJSONRequest(r) {
		t.Error("expected true for application/json Accept")
	}
}

func TestIsJSONRequest_NoJSON(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Content-Type", "text/html")
	if isJSONRequest(r) {
		t.Error("expected false for text/html")
	}
}

func TestIsJSONRequest_Empty(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	if isJSONRequest(r) {
		t.Error("expected false for no headers")
	}
}

// --- decodeJSON ---

func TestDecodeJSON(t *testing.T) {
	body := strings.NewReader(`{"code":"123456"}`)
	r := httptest.NewRequest("POST", "/", body)

	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		t.Fatalf("decodeJSON failed: %v", err)
	}
	if req.Code != "123456" {
		t.Errorf("expected 123456, got %q", req.Code)
	}
}

func TestDecodeJSON_Invalid(t *testing.T) {
	body := strings.NewReader(`not-json`)
	r := httptest.NewRequest("POST", "/", body)

	var req struct{ Code string }
	if err := decodeJSON(r, &req); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// --- writeJSON ---

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, StatusResponse{Status: PairStatusApproved})

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var resp StatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp.Status != PairStatusApproved {
		t.Errorf("expected approved, got %q", resp.Status)
	}
}

// --- parseSubmittedCode ---

func TestParseSubmittedCode_JSON(t *testing.T) {
	body := strings.NewReader(`{"code":"  654321  "}`)
	r := httptest.NewRequest("POST", "/", body)
	r.Header.Set("Content-Type", "application/json")

	code, err := parseSubmittedCode(r)
	if err != nil {
		t.Fatalf("parseSubmittedCode failed: %v", err)
	}
	if code != "654321" {
		t.Errorf("expected 654321, got %q", code)
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
		t.Errorf("expected 111222, got %q", code)
	}
}

func TestParseSubmittedCode_JSONInvalid(t *testing.T) {
	body := strings.NewReader(`not-json`)
	r := httptest.NewRequest("POST", "/", body)
	r.Header.Set("Content-Type", "application/json")

	_, err := parseSubmittedCode(r)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// --- respondPairSubmitError ---

func TestRespondPairSubmitError_JSON(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()

	respondPairSubmitError(w, r, "bad code")

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "bad code") {
		t.Errorf("body should contain error message, got %q", w.Body.String())
	}
}

func TestRespondPairSubmitError_FormRedirect(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	respondPairSubmitError(w, r, "bad code")

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "/pair?error=") {
		t.Errorf("expected redirect to /pair?error=, got %q", loc)
	}
	if !strings.Contains(loc, "bad+code") && !strings.Contains(loc, "bad%20code") {
		t.Errorf("expected URL-encoded error message in location, got %q", loc)
	}
}

// --- requestOriginAndHost ---

func TestRequestOriginAndHost_EmptyHost(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.Host = ""

	origin, host := requestOriginAndHost(r)
	if host != "pi-go" {
		t.Errorf("expected fallback host pi-go, got %q", host)
	}
	if origin != "http://pi-go" {
		t.Errorf("expected http://pi-go, got %q", origin)
	}
}

// --- ServerV2 handleStatus ---

func TestServerV2_HandleStatus_Pending(t *testing.T) {
	s := newTestServerV2(t)

	_, token, _, err := s.pairingMgr.CreatePair("/tmp/test")
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/api/status?token="+token, nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp StatusResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != PairStatusPending {
		t.Errorf("expected pending, got %q", resp.Status)
	}
}

func TestServerV2_HandleStatus_Missing(t *testing.T) {
	s := newTestServerV2(t)

	r := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestServerV2_HandleStatus_Unknown(t *testing.T) {
	s := newTestServerV2(t)

	r := httptest.NewRequest("GET", "/api/status?token=nonexistent", nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp StatusResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != PairStatusUnknown {
		t.Errorf("expected unknown, got %q", resp.Status)
	}
}

func TestServerV2_HandleStatus_Approved(t *testing.T) {
	s := newTestServerV2(t)

	code, token, _, err := s.pairingMgr.CreatePair("/tmp/test")
	if err != nil {
		t.Fatal(err)
	}
	s.pairingMgr.Approve(code)

	r := httptest.NewRequest("GET", "/api/status?token="+token, nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, r)

	var resp StatusResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != PairStatusApproved {
		t.Errorf("expected approved, got %q", resp.Status)
	}
}

// --- ServerV2 handleCreatePair ---

func TestServerV2_HandleCreatePair_POST(t *testing.T) {
	s := newTestServerV2(t)

	body := strings.NewReader(`{"project":"/tmp/my-proj"}`)
	r := httptest.NewRequest("POST", "/api/pair", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleCreatePair(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp PairResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Code) != 6 {
		t.Errorf("expected 6 digit code, got %q", resp.Code)
	}
	if resp.Token == "" {
		t.Error("token should not be empty")
	}
	if resp.QR == "" {
		t.Error("QR should not be empty")
	}
}

func TestServerV2_HandleCreatePair_GET(t *testing.T) {
	s := newTestServerV2(t)

	r := httptest.NewRequest("GET", "/api/pair", nil)
	w := httptest.NewRecorder()
	s.handleCreatePair(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp PairResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Code) != 6 {
		t.Errorf("expected 6 digit code, got %q", resp.Code)
	}
}

func TestServerV2_HandleCreatePair_DefaultProject(t *testing.T) {
	s := NewServerV2(Config{
		Addr:           "127.0.0.1:0",
		PairingTimeout: 5 * time.Minute,
		Project:        "", // empty project
	})

	r := httptest.NewRequest("GET", "/api/pair", nil)
	w := httptest.NewRecorder()
	s.handleCreatePair(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestServerV2_HandleCreatePair_InvalidJSON(t *testing.T) {
	s := newTestServerV2(t)

	body := strings.NewReader(`not-json`)
	r := httptest.NewRequest("POST", "/api/pair", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleCreatePair(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestServerV2_HandleCreatePair_ReusesActivePair(t *testing.T) {
	s := newTestServerV2(t)

	// First call creates
	r1 := httptest.NewRequest("GET", "/api/pair", nil)
	w1 := httptest.NewRecorder()
	s.handleCreatePair(w1, r1)

	var resp1 PairResponse
	json.NewDecoder(w1.Body).Decode(&resp1)

	// Second call should reuse the same active pair
	r2 := httptest.NewRequest("GET", "/api/pair", nil)
	w2 := httptest.NewRecorder()
	s.handleCreatePair(w2, r2)

	var resp2 PairResponse
	json.NewDecoder(w2.Body).Decode(&resp2)

	if resp1.Code != resp2.Code {
		t.Errorf("expected same code on reuse, got %q and %q", resp1.Code, resp2.Code)
	}
	if resp1.Token != resp2.Token {
		t.Errorf("expected same token on reuse, got %q and %q", resp1.Token, resp2.Token)
	}
}

// --- ServerV2 handlePair ---

func TestServerV2_HandlePair_NoToken(t *testing.T) {
	s := newTestServerV2(t)

	r := httptest.NewRequest("GET", "/pair", nil)
	w := httptest.NewRecorder()
	s.handlePair(w, r)

	// Should serve the pair page (or 404 if file missing, but handler runs)
	// With embedded static files it should work
	if w.Code == 0 {
		t.Error("expected a status code")
	}
}

func TestServerV2_HandlePair_WithApprovedToken(t *testing.T) {
	s := newTestServerV2(t)

	code, token, _, err := s.pairingMgr.CreatePair("/tmp/test")
	if err != nil {
		t.Fatal(err)
	}
	s.pairingMgr.Approve(code)

	r := httptest.NewRequest("GET", "/pair?token="+token, nil)
	w := httptest.NewRecorder()
	s.handlePair(w, r)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("expected redirect to /, got %q", loc)
	}

	var foundCookie bool
	for _, c := range w.Result().Cookies() {
		if c.Name == "pi_token" && c.Value == token {
			foundCookie = true
		}
	}
	if !foundCookie {
		t.Error("expected pi_token cookie")
	}
}

func TestServerV2_HandlePair_WithPendingToken(t *testing.T) {
	s := newTestServerV2(t)

	_, token, _, err := s.pairingMgr.CreatePair("/tmp/test")
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/pair?token="+token, nil)
	w := httptest.NewRecorder()
	s.handlePair(w, r)

	// Pending token should NOT redirect, should serve the pair page
	if w.Code == http.StatusSeeOther {
		t.Error("pending token should not redirect")
	}
}

// --- ServerV2 handleSubmitPairCode ---

func TestServerV2_HandleSubmitPairCode_JSON(t *testing.T) {
	s := newTestServerV2(t)

	code, _, err := s.BootstrapPair("/tmp/test")
	if err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(`{"code":"` + code + `"}`)
	r := httptest.NewRequest("POST", "/api/pair/submit", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	s.handleSubmitPairCode(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var resp StatusResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != PairStatusApproved {
		t.Errorf("expected approved, got %q", resp.Status)
	}

	// Verify cookie was set
	var foundCookie bool
	for _, c := range w.Result().Cookies() {
		if c.Name == "pi_token" {
			foundCookie = true
		}
	}
	if !foundCookie {
		t.Error("expected pi_token cookie")
	}
}

func TestServerV2_HandleSubmitPairCode_Form(t *testing.T) {
	s := newTestServerV2(t)

	code, _, err := s.BootstrapPair("/tmp/test")
	if err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader("code=" + code)
	r := httptest.NewRequest("POST", "/api/pair/submit", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.handleSubmitPairCode(w, r)

	// Form submit should redirect on success
	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("expected redirect to /, got %q", loc)
	}
}

func TestServerV2_HandleSubmitPairCode_EmptyCode(t *testing.T) {
	s := newTestServerV2(t)

	body := strings.NewReader(`{"code":""}`)
	r := httptest.NewRequest("POST", "/api/pair/submit", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	s.handleSubmitPairCode(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestServerV2_HandleSubmitPairCode_WrongCode(t *testing.T) {
	s := newTestServerV2(t)

	_, _, err := s.BootstrapPair("/tmp/test")
	if err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(`{"code":"000000"}`)
	r := httptest.NewRequest("POST", "/api/pair/submit", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	s.handleSubmitPairCode(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestServerV2_HandleSubmitPairCode_WrongCodeForm(t *testing.T) {
	s := newTestServerV2(t)

	_, _, err := s.BootstrapPair("/tmp/test")
	if err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader("code=000000")
	r := httptest.NewRequest("POST", "/api/pair/submit", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.handleSubmitPairCode(w, r)

	// Form should redirect with error
	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "/pair?error=") {
		t.Errorf("expected redirect to /pair?error=, got %q", loc)
	}
}

func TestServerV2_HandleSubmitPairCode_NotPost(t *testing.T) {
	s := newTestServerV2(t)

	r := httptest.NewRequest("GET", "/api/pair/submit", nil)
	w := httptest.NewRecorder()
	s.handleSubmitPairCode(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestServerV2_HandleSubmitPairCode_InvalidJSON(t *testing.T) {
	s := newTestServerV2(t)

	body := strings.NewReader(`not-json`)
	r := httptest.NewRequest("POST", "/api/pair/submit", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	s.handleSubmitPairCode(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- ServerV2 handleIndex ---

func TestServerV2_HandleIndex_Root(t *testing.T) {
	s := newTestServerV2(t)

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, r)

	// With embedded static files, should serve index.html or 200
	if w.Code == http.StatusSeeOther {
		t.Error("ServerV2 handleIndex should not redirect (no auth required)")
	}
}

func TestServerV2_HandleIndex_NotRoot(t *testing.T) {
	s := newTestServerV2(t)

	r := httptest.NewRequest("GET", "/nonexistent", nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- ServerV2 handleWebSocket ---

func TestServerV2_HandleWebSocket_MissingSessionID(t *testing.T) {
	s := newTestServerV2(t)

	r := httptest.NewRequest("GET", "/ws/", nil)
	w := httptest.NewRecorder()
	s.handleWebSocket(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- ServerV2 Addr ---

func TestServerV2_Addr_Default(t *testing.T) {
	s := NewServerV2(Config{
		Addr:           ":9090",
		PairingTimeout: 5 * time.Minute,
	})

	// Before Start, Addr should return cfg.Addr
	if addr := s.Addr(); addr != ":9090" {
		t.Errorf("expected :9090, got %q", addr)
	}
}

func TestServerV2_Addr_AfterListen(t *testing.T) {
	s := NewServerV2(Config{
		Addr:           "127.0.0.1:0",
		PairingTimeout: 5 * time.Minute,
	})
	s.listenAddr = "127.0.0.1:12345"

	if addr := s.Addr(); addr != "127.0.0.1:12345" {
		t.Errorf("expected 127.0.0.1:12345, got %q", addr)
	}
}

// --- ServerV2 SessionManager ---

func TestServerV2_SessionManager(t *testing.T) {
	s := newTestServerV2(t)
	sm := s.SessionManager()
	if sm == nil {
		t.Error("SessionManager should not be nil")
	}
}

// --- ServerV2 Shutdown ---

func TestServerV2_Shutdown(t *testing.T) {
	s := NewServerV2(Config{
		Addr:           "127.0.0.1:0",
		PairingTimeout: 5 * time.Minute,
	})
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

// --- ServerV2 BootstrapPair ---

func TestServerV2_BootstrapPair_EmptyProject(t *testing.T) {
	s := newTestServerV2(t)

	code, token, err := s.BootstrapPair("  ")
	if err != nil {
		t.Fatalf("BootstrapPair: %v", err)
	}
	if len(code) != 6 {
		t.Errorf("expected 6 digit code, got %q", code)
	}
	if token == "" {
		t.Error("token should not be empty")
	}
}

// --- loggingMiddleware ---

func TestLoggingMiddleware_OK(t *testing.T) {
	s := newTestServerV2(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	handler := s.loggingMiddleware(inner)
	r := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestLoggingMiddleware_ClientError(t *testing.T) {
	s := newTestServerV2(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})

	handler := s.loggingMiddleware(inner)
	r := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestLoggingMiddleware_ServerError(t *testing.T) {
	s := newTestServerV2(t)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	handler := s.loggingMiddleware(inner)
	r := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// --- responseCapture ---

func TestResponseCapture_WriteHeader(t *testing.T) {
	w := httptest.NewRecorder()
	rc := &responseCapture{ResponseWriter: w, status: http.StatusOK}
	rc.WriteHeader(http.StatusNotFound)

	if rc.status != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rc.status)
	}
}

func TestResponseCapture_Flush(t *testing.T) {
	w := httptest.NewRecorder()
	rc := &responseCapture{ResponseWriter: w, status: http.StatusOK}
	// httptest.ResponseRecorder implements Flusher
	rc.Flush() // should not panic
}

func TestResponseCapture_Hijack_NotSupported(t *testing.T) {
	w := httptest.NewRecorder()
	rc := &responseCapture{ResponseWriter: w, status: http.StatusOK}
	_, _, err := rc.Hijack()
	if err == nil {
		t.Error("expected error from Hijack on non-hijackable writer")
	}
}

// --- pipeWrapper ---

func TestPipeWrapper_ReadWriteClose(t *testing.T) {
	// Create real pipes
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()

	pw := &pipeWrapper{
		stdin:  stdinW,
		stdout: stdoutR,
		stderr: stderrR,
	}

	// Write to pipe
	go func() {
		pw.Write([]byte("hello"))
	}()

	buf := make([]byte, 5)
	n, err := stdinR.Read(buf)
	if err != nil {
		t.Fatalf("read from stdin: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Errorf("expected hello, got %q", string(buf[:n]))
	}

	// Read from pipe
	go func() {
		stdoutW.Write([]byte("world"))
	}()

	buf = make([]byte, 5)
	n, err = pw.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != "world" {
		t.Errorf("expected world, got %q", string(buf[:n]))
	}

	// Close
	stderrW.Close()
	if err := pw.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	stdinR.Close()
	stdoutW.Close()
}

// --- PtyBridge Alive ---

func TestPtyBridge_Alive_NewBridge(t *testing.T) {
	b := NewPtyBridge("/tmp", "", nil)
	// cmd is nil, done is open, but cmd == nil means not alive
	if b.Alive() {
		t.Error("new bridge with no process should not be alive")
	}
}

func TestPtyBridge_Alive_AfterClose(t *testing.T) {
	b := NewPtyBridge("/tmp", "", nil)
	b.Close()
	if b.Alive() {
		t.Error("closed bridge should not be alive")
	}
}

// --- PtyBridge resize ---

func TestPtyBridge_Resize_NilPtyFile(t *testing.T) {
	b := NewPtyBridge("/tmp", "", nil)
	// Should not panic
	b.resize(80, 24)
}

func TestPtyBridge_Resize_NonOsFile(t *testing.T) {
	// Use a pipeWrapper instead of *os.File
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()

	b := NewPtyBridge("/tmp", "", nil)
	b.ptyFile = &pipeWrapper{stdin: stdinW, stdout: stdoutR, stderr: stderrR}

	// Should not panic - silently ignores non-*os.File
	b.resize(120, 40)

	stdinR.Close()
	stdoutW.Close()
	stderrW.Close()
	b.ptyFile.Close()
}

// --- PtyPool ---

func TestPtyPool_NewPtyPool(t *testing.T) {
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

// --- NewServerV2 defaults ---

func TestNewServerV2_Defaults(t *testing.T) {
	s := NewServerV2(Config{})

	if s.cfg.PairingTimeout != 5*time.Minute {
		t.Errorf("expected 5m default timeout, got %v", s.cfg.PairingTimeout)
	}
	if s.cfg.Addr != ":8080" {
		t.Errorf("expected :8080 default addr, got %q", s.cfg.Addr)
	}
	if s.pairingMgr == nil {
		t.Error("pairingMgr should not be nil")
	}
	if s.sessions == nil {
		t.Error("sessions should not be nil")
	}
	if s.ptyPool == nil {
		t.Error("ptyPool should not be nil")
	}
}

// --- handlers.go Server accessors ---

func TestServer_Mux(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})
	if s.Mux() == nil {
		t.Error("Mux() should not be nil")
	}
}

func TestServer_Addr(t *testing.T) {
	s := NewServer(Config{Addr: ":9999", PairingTimeout: 5 * time.Minute, StaticDir: "."})
	if s.Addr() != ":9999" {
		t.Errorf("expected :9999, got %q", s.Addr())
	}
}

func TestServer_PairingManager(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})
	if s.PairingManager() == nil {
		t.Error("PairingManager should not be nil")
	}
}

func TestServer_GetProject(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	// Before approval
	_, err := s.GetProject("nonexistent")
	if err == nil {
		t.Error("expected error for non-approved token")
	}

	// Create and approve
	code, token, _, _ := s.pairingManager.CreatePair("/tmp/test")
	s.pairingManager.Approve(code)

	proj, err := s.GetProject(token)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if proj != "/tmp/test" {
		t.Errorf("expected /tmp/test, got %q", proj)
	}
}

// --- handlers.go Server.handlePair ---

func TestServer_HandlePair_NoToken(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	r := httptest.NewRequest("GET", "/pair", nil)
	w := httptest.NewRecorder()
	s.handlePair(w, r)

	// Should try to serve pair.html (may 404 since StaticDir is ".")
	if w.Code == http.StatusSeeOther {
		t.Error("no token should not redirect, should serve page")
	}
}

func TestServer_HandlePair_ApprovedToken(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	code, token, _, _ := s.pairingManager.CreatePair("/tmp/test")
	s.pairingManager.Approve(code)

	r := httptest.NewRequest("GET", "/pair?token="+token, nil)
	w := httptest.NewRecorder()
	s.handlePair(w, r)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", w.Code)
	}
}

// --- handlers.go Server.handleCreatePair method not allowed ---

func TestServer_HandleCreatePair_MethodNotAllowed(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	r := httptest.NewRequest("PUT", "/api/pair", nil)
	w := httptest.NewRecorder()
	s.handleCreatePair(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// --- handlers.go Server.handleWebSocket ---

func TestServer_HandleWebSocket_MissingSession(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	r := httptest.NewRequest("GET", "/ws/", nil)
	w := httptest.NewRecorder()
	s.handleWebSocket(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestServer_HandleWebSocket_MissingToken(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	r := httptest.NewRequest("GET", "/ws/test-session", nil)
	w := httptest.NewRecorder()
	s.handleWebSocket(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestServer_HandleWebSocket_InvalidToken(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	r := httptest.NewRequest("GET", "/ws/test-session?token=bad", nil)
	w := httptest.NewRecorder()
	s.handleWebSocket(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// --- handlers.go Server.handleIndex ---

func TestServer_HandleIndex_NotRootPath(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	r := httptest.NewRequest("GET", "/other", nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- handlers.go Server.handleStatus ---

func TestServer_HandleStatus_MissingToken(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	r := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestServer_HandleStatus_Approved(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	code, token, _, _ := s.pairingManager.CreatePair("/tmp/test")
	s.pairingManager.Approve(code)

	r := httptest.NewRequest("GET", "/api/status?token="+token, nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp StatusResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != PairStatusApproved {
		t.Errorf("expected approved, got %q", resp.Status)
	}
}

// --- staticAssetsFileServer / serveStaticPage ---

func TestStaticAssetsFileServer_Embedded(t *testing.T) {
	h := staticAssetsFileServer("")
	if h == nil {
		t.Fatal("should return a handler for embedded assets")
	}
}

func TestStaticAssetsFileServer_Disk(t *testing.T) {
	h := staticAssetsFileServer("/some/dir")
	if h == nil {
		t.Fatal("should return a handler for disk assets")
	}
}

func TestServeStaticPage_Embedded(t *testing.T) {
	r := httptest.NewRequest("GET", "/pair", nil)
	w := httptest.NewRecorder()
	serveStaticPage(w, r, "", "pair.html")

	// The embedded static may or may not have pair.html; just verify no panic
	if w.Code == 0 {
		t.Error("expected a response")
	}
}

func TestServeStaticPage_Disk_Missing(t *testing.T) {
	r := httptest.NewRequest("GET", "/pair", nil)
	w := httptest.NewRecorder()
	serveStaticPage(w, r, "/nonexistent/dir", "pair.html")

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing file, got %d", w.Code)
	}
}

func TestServeStaticPage_Embedded_Missing(t *testing.T) {
	r := httptest.NewRequest("GET", "/missing", nil)
	w := httptest.NewRecorder()
	serveStaticPage(w, r, "", "nonexistent.html")

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- Session CleanupExpired ---

func TestSessionManager_CleanupExpired(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	// Create a session
	_, err := sm.CreateSession("/tmp/test", "token")
	if err != nil {
		t.Fatal(err)
	}

	// CleanupExpired should not remove recent sessions
	sm.CleanupExpired()
	if sm.SessionCount() != 1 {
		t.Errorf("expected 1 session after cleanup, got %d", sm.SessionCount())
	}
}

// --- Full integration test via httptest ---

func TestServerV2_FullPairFlow_HTTPTest(t *testing.T) {
	s := newTestServerV2(t)

	// Step 1: Create pair via POST
	body := bytes.NewBufferString(`{"project":"/tmp/integ"}`)
	r := httptest.NewRequest("POST", "/api/pair", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleCreatePair(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("create pair: expected 200, got %d", w.Code)
	}
	var pr PairResponse
	json.NewDecoder(w.Body).Decode(&pr)

	// Step 2: Check status is pending
	r = httptest.NewRequest("GET", "/api/status?token="+pr.Token, nil)
	w = httptest.NewRecorder()
	s.handleStatus(w, r)
	var sr StatusResponse
	json.NewDecoder(w.Body).Decode(&sr)
	if sr.Status != PairStatusPending {
		t.Errorf("expected pending, got %q", sr.Status)
	}

	// Step 3: Submit correct code via JSON
	body = bytes.NewBufferString(`{"code":"` + pr.Code + `"}`)
	r = httptest.NewRequest("POST", "/api/pair/submit", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	w = httptest.NewRecorder()
	s.handleSubmitPairCode(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("submit pair code: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Step 4: Check status is approved
	r = httptest.NewRequest("GET", "/api/status?token="+pr.Token, nil)
	w = httptest.NewRecorder()
	s.handleStatus(w, r)
	json.NewDecoder(w.Body).Decode(&sr)
	if sr.Status != PairStatusApproved {
		t.Errorf("expected approved, got %q", sr.Status)
	}

	// Step 5: handlePair with approved token should redirect
	r = httptest.NewRequest("GET", "/pair?token="+pr.Token, nil)
	w = httptest.NewRecorder()
	s.handlePair(w, r)
	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", w.Code)
	}
}

// --- PtyBridge copyPtyToWS / copyWSToPty integration tests ---

func TestPtyBridge_CopyPtyToWS(t *testing.T) {
	// Create an io.Pipe to simulate PTY output
	ptyR, ptyW := io.Pipe()

	b := NewPtyBridge("/tmp", "", nil)
	b.ptyFile = &pipeWrapper{stdin: ptyW, stdout: ptyR, stderr: io.NopCloser(strings.NewReader(""))}

	// Set up WebSocket server that runs copyPtyToWS
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		wsDone := make(chan struct{})
		b.copyPtyToWS(conn, wsDone) // blocks until PTY closes
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Write to PTY, then close to end copyPtyToWS
	go func() {
		ptyW.Write([]byte("hello from pty"))
		time.Sleep(50 * time.Millisecond)
		ptyW.Close()
	}()

	// Client reads the message from WebSocket
	var msg WSMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	if msg.Type != "output" {
		t.Errorf("type = %q, want output", msg.Type)
	}
	if msg.Data != "hello from pty" {
		t.Errorf("data = %q, want 'hello from pty'", msg.Data)
	}
}

func TestPtyBridge_CopyWSToPty(t *testing.T) {
	// Create io.Pipe to capture PTY input
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	b := NewPtyBridge("/tmp", "", nil)
	b.ptyFile = &pipeWrapper{stdin: stdinW, stdout: stdoutR, stderr: io.NopCloser(strings.NewReader(""))}

	var received string
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		wsDone := make(chan struct{})
		go func() {
			b.copyWSToPty(conn, wsDone)
			close(wsDone)
		}()

		// Read what the PTY received
		buf := make([]byte, 100)
		n, _ := stdinR.Read(buf)
		mu.Lock()
		received = string(buf[:n])
		mu.Unlock()
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Send input message via WebSocket
	msg := WSMessage{Type: "input", Data: "test input"}
	conn.WriteJSON(msg)

	time.Sleep(300 * time.Millisecond)
	conn.Close()

	mu.Lock()
	defer mu.Unlock()
	if received != "test input" {
		t.Errorf("PTY received %q, want 'test input'", received)
	}

	stdoutW.Close()
	stdinR.Close()
}

func TestPtyBridge_CopyWSToPty_Resize(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	b := NewPtyBridge("/tmp", "", nil)
	b.ptyFile = &pipeWrapper{stdin: stdinW, stdout: stdoutR, stderr: io.NopCloser(strings.NewReader(""))}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		wsDone := make(chan struct{})
		go b.copyWSToPty(conn, wsDone)

		// Wait for client to send
		time.Sleep(300 * time.Millisecond)
		close(wsDone)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Send resize message
	msg := WSMessage{Type: "resize", Data: "120x40"}
	conn.WriteJSON(msg)

	// Send ping message
	msg2 := WSMessage{Type: "ping"}
	conn.WriteJSON(msg2)

	time.Sleep(200 * time.Millisecond)
	conn.Close()

	stdoutW.Close()
	stdinR.Close()
}

func TestPtyBridge_CopyWSToPty_RawData(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	b := NewPtyBridge("/tmp", "", nil)
	b.ptyFile = &pipeWrapper{stdin: stdinW, stdout: stdoutR, stderr: io.NopCloser(strings.NewReader(""))}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		wsDone := make(chan struct{})
		go b.copyWSToPty(conn, wsDone)

		// Read raw data from PTY stdin
		buf := make([]byte, 100)
		n, _ := stdinR.Read(buf)
		if string(buf[:n]) != "raw bytes" {
			t.Errorf("PTY received %q, want 'raw bytes'", string(buf[:n]))
		}
		close(wsDone)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Send raw text (not JSON) — should be written directly to PTY
	conn.WriteMessage(websocket.TextMessage, []byte("raw bytes"))

	time.Sleep(300 * time.Millisecond)
	conn.Close()
	stdoutW.Close()
}

func TestPtyBridge_HandleWebSocket_StartFails(t *testing.T) {
	b := NewPtyBridge("/nonexistent/path/that/doesnt/exist", "", nil)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		b.HandleWebSocket(conn, "test-session")
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Should receive an error message
	var msg WSMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	if msg.Type != "error" {
		t.Errorf("type = %q, want error", msg.Type)
	}
}

func TestPtyBridge_Close_WithPtyFile(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	b := NewPtyBridge("/tmp", "", nil)
	b.ptyFile = &pipeWrapper{stdin: stdinW, stdout: stdoutR, stderr: io.NopCloser(strings.NewReader(""))}

	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// ptyFile should be nil after close
	if b.ptyFile != nil {
		t.Error("ptyFile should be nil after Close")
	}

	stdinR.Close()
	stdoutW.Close()
}

func TestPtyPool_CloseAll_WithBridges(t *testing.T) {
	pool := NewPtyPool(nil)

	// Manually add some bridges
	pool.mu.Lock()
	b1 := NewPtyBridge("/tmp", "", nil)
	b2 := NewPtyBridge("/tmp", "", nil)
	pool.bridges["s1"] = b1
	pool.bridges["s2"] = b2
	pool.mu.Unlock()

	pool.CloseAll()

	pool.mu.Lock()
	defer pool.mu.Unlock()
	if len(pool.bridges) != 0 {
		t.Errorf("expected 0 bridges after CloseAll, got %d", len(pool.bridges))
	}
}

// --- handlers.go Server tests ---

func TestServer_HandleIndex_WithTokenCookie(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	code, token, _, _ := s.pairingManager.CreatePair("/tmp/test")
	s.pairingManager.Approve(code)

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "pi_token", Value: token})
	w := httptest.NewRecorder()
	s.handleIndex(w, r)

	// With approved cookie, should try to serve index.html (not redirect)
	if w.Code == http.StatusSeeOther {
		t.Error("approved token cookie should not redirect to /pair")
	}
}

func TestServer_HandleIndex_NoToken_Redirects(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, r)

	if w.Code != http.StatusSeeOther {
		t.Errorf("no token should redirect, got %d", w.Code)
	}
}

func TestServer_HandleIndex_QueryToken(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	code, token, _, _ := s.pairingManager.CreatePair("/tmp/test")
	s.pairingManager.Approve(code)

	r := httptest.NewRequest("GET", "/?token="+token, nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, r)

	// Approved query token should serve page
	if w.Code == http.StatusSeeOther {
		t.Error("approved query token should not redirect")
	}
}

func TestServer_HandleCreatePair_Valid(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	body := strings.NewReader(`{"project":"/tmp/test"}`)
	r := httptest.NewRequest("POST", "/api/pair", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleCreatePair(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp PairResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Code) != 6 {
		t.Errorf("expected 6 digit code, got %q", resp.Code)
	}
	if resp.QR == "" {
		t.Error("QR should not be empty")
	}
}

func TestServer_HandleCreatePair_EmptyProject(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	body := strings.NewReader(`{"project":""}`)
	r := httptest.NewRequest("POST", "/api/pair", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleCreatePair(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestServer_HandleCreatePair_InvalidJSON(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	body := strings.NewReader(`not json`)
	r := httptest.NewRequest("POST", "/api/pair", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleCreatePair(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestServer_HandleWebSocket_ApprovedToken_GetProject(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	code, token, _, _ := s.pairingManager.CreatePair("/tmp/test")
	s.pairingManager.Approve(code)

	// Without actual WebSocket upgrade, the handler will reach upgrade and fail
	// (which is expected). We're testing that auth passes.
	r := httptest.NewRequest("GET", "/ws/test-session?token="+token, nil)
	w := httptest.NewRecorder()
	s.handleWebSocket(w, r)

	// Should NOT be 401 (auth passed) — upgrade failure gives 400
	if w.Code == http.StatusUnauthorized {
		t.Error("approved token should pass auth check")
	}
}

// --- ServerV2 WebSocket handler ---

func TestServerV2_HandleWebSocket_WithSessionID(t *testing.T) {
	s := newTestServerV2(t)

	// Without WebSocket upgrade, handler reaches upgrade and fails silently
	r := httptest.NewRequest("GET", "/ws/my-session", nil)
	w := httptest.NewRecorder()
	s.handleWebSocket(w, r)

	// Handler runs without panic — that's the test
	_ = w.Code
}

// --- Session edge cases ---

func TestSessionManager_Close_StopsCleanup(t *testing.T) {
	sm := NewSessionManager()

	sm.CreateSession("/tmp/1", "t1")
	sm.CreateSession("/tmp/2", "t2")

	if sm.SessionCount() != 2 {
		t.Errorf("expected 2 sessions, got %d", sm.SessionCount())
	}

	// Close stops the cleanup goroutine (doesn't clear sessions)
	sm.Close()

	// Sessions are still there, just cleanup loop stopped
	if sm.SessionCount() != 2 {
		t.Errorf("expected 2 sessions after Close (just stops goroutine), got %d", sm.SessionCount())
	}
}

// --- AttachWebSocket integration test ---

func TestPtyBridge_AttachWebSocket(t *testing.T) {
	// Create pipes for PTY
	ptyR, ptyW := io.Pipe()

	b := NewPtyBridge("/tmp", "", nil)
	b.ptyFile = &pipeWrapper{stdin: ptyW, stdout: ptyR, stderr: io.NopCloser(strings.NewReader(""))}
	// Simulate cmd being set (so Alive() can work)
	b.cmd = &exec.Cmd{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		b.AttachWebSocket(conn, "test-session")
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Send an input message
	conn.WriteJSON(WSMessage{Type: "input", Data: "test"})

	// Close PTY to end the AttachWebSocket
	time.Sleep(50 * time.Millisecond)
	ptyW.Close()
	ptyR.Close()
	b.closeOnce.Do(func() { close(b.done) })

	time.Sleep(200 * time.Millisecond)
	conn.Close()
}

// --- PtyBridge copyPtyToWS error paths ---

func TestPtyBridge_CopyPtyToWS_DoneClosed(t *testing.T) {
	ptyR, ptyW := io.Pipe()

	b := NewPtyBridge("/tmp", "", nil)
	b.ptyFile = &pipeWrapper{stdin: ptyW, stdout: ptyR, stderr: io.NopCloser(strings.NewReader(""))}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Close done channel before starting — copyPtyToWS should return immediately
		b.closeOnce.Do(func() { close(b.done) })
		wsDone := make(chan struct{})
		b.copyPtyToWS(conn, wsDone)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	time.Sleep(100 * time.Millisecond)
	ptyW.Close()
	ptyR.Close()
}

// --- Hijack support test ---

type hijackableRecorder struct {
	*httptest.ResponseRecorder
}

func (h *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, fmt.Errorf("test hijack")
}

func TestResponseCapture_Hijack_Supported(t *testing.T) {
	w := &hijackableRecorder{httptest.NewRecorder()}
	rc := &responseCapture{ResponseWriter: w, status: http.StatusOK}
	_, _, err := rc.Hijack()
	if err == nil {
		t.Error("expected error from test hijack")
	}
	if err.Error() != "test hijack" {
		t.Errorf("expected 'test hijack' error, got %q", err.Error())
	}
}

// --- WebSocketHandler ws.go coverage ---

func TestWebSocketHandler_HandleWebSocket_NoToken(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()
	wsh := NewWebSocketHandler(sm)

	r := httptest.NewRequest("GET", "/ws/session-1", nil)
	w := httptest.NewRecorder()
	wsh.HandleWebSocket(w, r)

	// Missing token
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestWebSocketHandler_HandleWebSocket_MissingPath(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()
	wsh := NewWebSocketHandler(sm)

	r := httptest.NewRequest("GET", "/ws/", nil)
	w := httptest.NewRecorder()
	wsh.HandleWebSocket(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
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
