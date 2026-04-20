package webserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// --- handlers.go small getters (Mux/Addr/PairingManager/GetProject) ---

func TestServer_Mux(t *testing.T) {
	s := NewServer(Config{Addr: ":0"})
	if s.Mux() == nil {
		t.Fatal("Mux should not return nil")
	}
}

func TestServer_Addr(t *testing.T) {
	s := NewServer(Config{Addr: ":1234"})
	if got := s.Addr(); got != ":1234" {
		t.Errorf("expected :1234, got %q", got)
	}
}

func TestServer_PairingManagerGetter(t *testing.T) {
	s := NewServer(Config{})
	if s.PairingManager() == nil {
		t.Fatal("PairingManager should not be nil")
	}
}

func TestServer_GetProject_Approved(t *testing.T) {
	s := NewServer(Config{})
	code, token, _, err := s.pairingManager.CreatePair("/tmp/gp-project")
	if err != nil {
		t.Fatalf("CreatePair: %v", err)
	}
	if _, err := s.pairingManager.Approve(code); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	proj, err := s.GetProject(token)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if proj != "/tmp/gp-project" {
		t.Errorf("expected /tmp/gp-project, got %q", proj)
	}
}

func TestServer_GetProject_Unknown(t *testing.T) {
	s := NewServer(Config{})
	if _, err := s.GetProject("does-not-exist"); err == nil {
		t.Error("expected error for unknown token")
	}
}

// --- handlers.go (legacy Server) handleWebSocket auth branches ---

func TestServer_HandleWebSocket_MissingToken(t *testing.T) {
	s := NewServer(Config{})
	r := httptest.NewRequest("GET", "/ws/sess-1", nil)
	w := httptest.NewRecorder()
	s.handleWebSocket(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestServer_HandleWebSocket_InvalidToken(t *testing.T) {
	s := NewServer(Config{})
	r := httptest.NewRequest("GET", "/ws/sess-1?token=bad", nil)
	w := httptest.NewRecorder()
	s.handleWebSocket(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestServer_HandleWebSocket_ApprovedTokenUpgradeFails(t *testing.T) {
	// Approved token passes auth; the upgrade then fails under the plain
	// httptest.NewRecorder which is not hijackable. The handler must not panic.
	s := NewServer(Config{})
	code, token, _, err := s.pairingManager.CreatePair("/tmp/ws-auth")
	if err != nil {
		t.Fatalf("CreatePair: %v", err)
	}
	if _, err := s.pairingManager.Approve(code); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	r := httptest.NewRequest("GET", "/ws/sess-1?token="+token, nil)
	w := httptest.NewRecorder()
	s.handleWebSocket(w, r)
	// No assertion on code — just exercise the path and prove it doesn't panic.
}

// --- ServerV2.Start + Shutdown ---

func TestServerV2_StartAndShutdown(t *testing.T) {
	s := NewServerV2(Config{Addr: "127.0.0.1:0", PairingTimeout: time.Minute})
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if s.Addr() == "" {
		t.Error("Addr should be populated after Start")
	}
	if !strings.HasPrefix(s.Addr(), "127.0.0.1:") {
		t.Errorf("unexpected listen addr: %q", s.Addr())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestServerV2_Start_BindError(t *testing.T) {
	// Garbled address to force a net.Listen error.
	s := NewServerV2(Config{Addr: "invalid:::address"})
	if err := s.Start(); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
		t.Fatal("expected error for invalid address")
	}
}

// --- responseCapture.Hijack ---

type noHijackRecorder struct{ *httptest.ResponseRecorder }

func TestResponseCapture_Hijack_NotSupported(t *testing.T) {
	rc := &responseCapture{ResponseWriter: &noHijackRecorder{httptest.NewRecorder()}}
	_, _, err := rc.Hijack()
	if err == nil {
		t.Fatal("expected error when underlying writer does not support hijack")
	}
	if !strings.Contains(err.Error(), "does not implement") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- BootstrapPair empty/whitespace project defaults to "." ---

func TestBootstrapPair_EmptyProject(t *testing.T) {
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	code, token, err := s.BootstrapPair("   ")
	if err != nil {
		t.Fatalf("BootstrapPair: %v", err)
	}
	if code == "" || token == "" {
		t.Fatal("expected code and token")
	}
	if s.activeProject != "." {
		t.Errorf("expected project '.', got %q", s.activeProject)
	}
}

func TestBootstrapPair_ReusesPending(t *testing.T) {
	// Calling BootstrapPair twice while the first is still pending should
	// reuse the same code/token (covers the getOrCreateActivePair branch
	// that rebuilds the QR for an existing pending pair).
	s := newTestServerV2(t)
	defer s.Shutdown(t.Context())
	code1, token1, err := s.BootstrapPair("/tmp/reuse")
	if err != nil {
		t.Fatalf("BootstrapPair 1: %v", err)
	}
	code2, token2, err := s.BootstrapPair("/tmp/reuse")
	if err != nil {
		t.Fatalf("BootstrapPair 2: %v", err)
	}
	if code1 != code2 || token1 != token2 {
		t.Errorf("expected reuse: (%s,%s) vs (%s,%s)", code1, token1, code2, token2)
	}
}

// --- BuildPairQRCode / GenerateQRCode ---

func TestBuildPairQRCode_Valid(t *testing.T) {
	data, err := BuildPairQRCode("123456", "tok-xyz", "pi-go", "http://pi-go/pair")
	if err != nil {
		t.Fatalf("BuildPairQRCode: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("BuildPairQRCode returned empty data")
	}
	if string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Errorf("expected PNG signature")
	}
}

func TestBuildPairQRCode_MinimalArgs(t *testing.T) {
	data, err := BuildPairQRCode("", "", "", "")
	if err != nil {
		t.Fatalf("BuildPairQRCode: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty PNG")
	}
}

func TestGenerateQRCode_EmptyError(t *testing.T) {
	// The skip2/go-qrcode library rejects empty input — covers the
	// error-wrapping branch in GenerateQRCode.
	_, err := GenerateQRCode("")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
	if !strings.Contains(err.Error(), "encode QR PNG") {
		t.Errorf("expected wrap 'encode QR PNG', got %v", err)
	}
}

// --- PairingManager.CreatePairWithContext: default host fallback ---

func TestCreatePairWithContext_EmptyHostDefault(t *testing.T) {
	pm := NewPairingManager(5 * time.Minute)
	code, token, qr, err := pm.CreatePairWithContext("/tmp/proj", "   ", "")
	if err != nil {
		t.Fatalf("CreatePairWithContext: %v", err)
	}
	if len(code) != 6 || token == "" || len(qr) == 0 {
		t.Fatalf("unexpected values: code=%q token=%q qrlen=%d", code, token, len(qr))
	}
}

// --- PtyPool.GetOrCreate: Start failure propagates ---

func TestPtyPool_GetOrCreate_StartError(t *testing.T) {
	pool := NewPtyPool(nil)
	defer pool.CloseAll()

	// A nonexistent project directory makes cmd.Start fail, so GetOrCreate
	// should return an error (exercising the error branch at pty.go:407).
	b, err := pool.GetOrCreate("sess-err", "/nonexistent/path/pigo-test", "", "", nil, false)
	if err == nil {
		_ = b.Close()
		t.Fatal("expected error for nonexistent project dir")
	}
}

// --- WebSocketHandler: CheckOrigin callback ---

func TestWebSocketHandler_CheckOrigin(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()
	h := NewWebSocketHandler(sm)

	if h.upgrader.CheckOrigin == nil {
		t.Fatal("expected non-nil CheckOrigin")
	}
	r := httptest.NewRequest("GET", "/ws/test", nil)
	r.Header.Set("Origin", "http://evil.example.com")
	if !h.upgrader.CheckOrigin(r) {
		t.Error("expected CheckOrigin to return true for any origin")
	}
}

// --- writeJSON / decodeJSON helpers ---

func TestServerV2_WriteJSON_ContentType(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, map[string]string{"hello": "world"})
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %q", ct)
	}
	var got map[string]string
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["hello"] != "world" {
		t.Errorf("unexpected body: %v", got)
	}
}

func TestDecodeJSON_Err(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader("not json"))
	var v struct {
		A string `json:"a"`
	}
	if err := decodeJSON(r, &v); err == nil {
		t.Fatal("expected error for non-JSON body")
	}
}

// --- PtyBridge.copyPtyToWS: WriteJSON error path (conn closed early) ---

func TestPtyBridge_CopyPtyToWS_WSCloseTriggersExit(t *testing.T) {
	pr, pw := io.Pipe()
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
		// Signal wsDone immediately — forces copyPtyToWS to return on its first
		// select check (covers the `<-wsDone` case before any read).
		close(wsDone)
		b.copyPtyToWS(conn, wsDone)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()
	_ = pw.Close()
	_ = pr.Close()
}

// --- PtyBridge.copyWSToPty: wsDone early exit ---

func TestPtyBridge_CopyWSToPty_WsDoneEarlyExit(t *testing.T) {
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
		close(wsDone)
		// Should return immediately because wsDone is closed.
		b.copyWSToPty(conn, wsDone)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()
}

// --- PtyBridge.copyWSToPty: done channel (PTY exit) triggers exit ---

func TestPtyBridge_CopyWSToPty_PtyDoneEarlyExit(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()
	b := NewPtyBridge("/tmp", "", "", nil, false, nil)
	b.ptyFile = struct {
		io.Reader
		io.Writer
		io.Closer
	}{Reader: pr, Writer: pw, Closer: pr}
	// Pre-close the bridge done so copyWSToPty bails on the <-pb.done case.
	_ = b.Close()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer conn.Close()
		wsDone := make(chan struct{})
		b.copyWSToPty(conn, wsDone)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()
}

// --- PtyBridge.copyPtyToWS: done channel triggers exit ---

func TestPtyBridge_CopyPtyToWS_PtyDoneExit(t *testing.T) {
	pr, pw := io.Pipe()
	b := NewPtyBridge("/tmp", "", "", nil, false, nil)
	b.ptyFile = struct {
		io.Reader
		io.Writer
		io.Closer
	}{Reader: pr, Writer: pw, Closer: pr}
	_ = b.Close()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer conn.Close()
		wsDone := make(chan struct{})
		b.copyPtyToWS(conn, wsDone)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()
	_ = pw.Close()
	_ = pr.Close()
}

// --- PtyBridge.AttachWebSocket: clean attach with synthetic ptyFile ---
//
// Uses an io.Pipe for the PTY side so copyPtyToWS gets EOF after the write
// pipe is closed. Closing the WebSocket causes copyWSToPty to return. No
// os/exec process is started, so there's no cmd.Wait goroutine racing with
// sessionID access.
func TestPtyBridge_AttachWebSocket_Clean(t *testing.T) {
	pr, pw := io.Pipe()
	b := NewPtyBridge("/tmp", "", "", nil, false, nil)
	b.ptyFile = struct {
		io.Reader
		io.Writer
		io.Closer
	}{Reader: pr, Writer: pw, Closer: pr}

	attachReturned := make(chan struct{})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		defer conn.Close()
		b.AttachWebSocket(conn, "attach-clean")
		close(attachReturned)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Feed a message so copyPtyToWS writes at least one JSON message out.
	go func() {
		_, _ = pw.Write([]byte("pty out"))
		_ = pw.Close()
	}()

	// Read the output message.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg WSMessage
	_ = conn.ReadJSON(&msg)

	// Close the WS — copyWSToPty's ReadMessage returns, closeWS runs, the
	// PTY side is already at EOF so copyPtyToWS has also exited.
	_ = conn.Close()

	select {
	case <-attachReturned:
	case <-time.After(3 * time.Second):
		t.Fatal("AttachWebSocket did not return in time")
	}
}

// --- PtyBridge.startProcess / startProcessWithPipes: successful start ---
//
// Launches a short-lived process that exits cleanly, exercising the
// happy-path for both startProcess (via pty) and the pipe fallback.
// We point the bridge at /bin/sh (via os.Executable) but supply a nonexistent
// binary so cmd.Start() fails through the fallback path.

// --- PtyBridge.Close: exercises the `pb.conn != nil` branch ---
//
// We set up a bridge with a real websocket.Conn assigned before calling
// Close, and use an errgroup to ensure the upgrade finishes and the conn
// is assigned on this same goroutine. This exercises the conn-close branch
// at pty.go:361 without any concurrent goroutine writing to pb.conn.
func TestPtyBridge_Close_WithConn(t *testing.T) {
	pr, pw := io.Pipe()
	b := NewPtyBridge("/tmp", "", "", nil, false, nil)
	b.ptyFile = struct {
		io.Reader
		io.Writer
		io.Closer
	}{Reader: pr, Writer: pw, Closer: pr}

	connCh := make(chan *websocket.Conn, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		connCh <- conn
		// Hold the handler until the client disconnects.
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	// Receive the server-side conn and assign it on *this* goroutine so
	// that the subsequent Close sees a well-defined state.
	serverConn := <-connCh
	b.conn = serverConn

	if err := b.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if b.conn != nil {
		t.Error("conn should be nil after Close")
	}
}

// --- CreatePair defaults host to pi-go ---

func TestCreatePair_DefaultsHost(t *testing.T) {
	pm := NewPairingManager(time.Minute)
	code, token, qr, err := pm.CreatePair("/tmp/cp-def")
	if err != nil {
		t.Fatalf("CreatePair: %v", err)
	}
	if len(code) != 6 || token == "" || len(qr) == 0 {
		t.Fatal("bad values")
	}
}
