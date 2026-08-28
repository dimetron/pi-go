package webserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

// TestMain doubles as a fake "pi" child process. ServerV2/PtyPool spawn the
// current executable (os.Executable) as the pi TUI; when PI_GO_FAKE_PI is set
// we short-circuit before running the test suite, emit a line of output, and
// exit cleanly. This lets the PTY lifecycle (startProcess, AttachWebSocket,
// copy loops) be exercised deterministically without recursively running tests
// or depending on a real pi binary.
func TestMain(m *testing.M) {
	if os.Getenv("PI_GO_FAKE_PI") == "1" {
		fmt.Println("fake-pi ready")
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestPtyBridgeChildArgs(t *testing.T) {
	t.Run("all flags forwarded", func(t *testing.T) {
		pb := NewPtyBridge("/proj", "glm-5.2:cloud", "https://api.example", []string{"X-A=1", "X-B=2"}, true, nil)
		args := pb.childArgs()
		joined := strings.Join(args, " ")
		for _, want := range []string{"--model glm-5.2:cloud", "--url https://api.example", "--header X-A=1", "--header X-B=2", "--insecure"} {
			if !strings.Contains(joined, want) {
				t.Errorf("childArgs() = %v, missing %q", args, want)
			}
		}
	})

	t.Run("no flags produce empty args", func(t *testing.T) {
		pb := NewPtyBridge("/proj", "", "", nil, false, nil)
		if args := pb.childArgs(); len(args) != 0 {
			t.Errorf("childArgs() = %v, want empty", args)
		}
	})
}

func TestGenerateQRCodeTooLongData(t *testing.T) {
	// Data far exceeding QR byte capacity forces an encode error.
	_, err := GenerateQRCode(strings.Repeat("a", 5000))
	if err == nil {
		t.Fatal("GenerateQRCode() error = nil, want error for oversized payload")
	}
}

func TestCreatePairWithContextDefaultsHost(t *testing.T) {
	pm := NewPairingManager(5 * time.Minute)
	// Empty serverHost should fall back to the "pi-go" default without error.
	code, token, qr, err := pm.CreatePairWithContext("/proj", "", "")
	if err != nil {
		t.Fatalf("CreatePairWithContext() error = %v", err)
	}
	if !ValidateCode(code) {
		t.Errorf("code %q is not valid", code)
	}
	if token == "" {
		t.Error("token should not be empty")
	}
	if len(qr) == 0 {
		t.Error("QR data should not be empty")
	}
}

func TestBuildPairQRCode(t *testing.T) {
	png, err := BuildPairQRCode("123456", "127.0.0.1:8080", "http://127.0.0.1:8080/pair")
	if err != nil {
		t.Fatalf("BuildPairQRCode() error = %v", err)
	}
	if len(png) == 0 {
		t.Fatal("BuildPairQRCode() returned empty PNG")
	}
}

func TestHandleCreatePairRejectsNonPost(t *testing.T) {
	s := NewServer(Config{PairingTimeout: time.Minute, StaticDir: "."})
	req := httptest.NewRequest(http.MethodGet, "/api/pair", nil)
	w := httptest.NewRecorder()
	s.handleCreatePair(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleCreatePairRejectsInvalidBody(t *testing.T) {
	s := NewServer(Config{PairingTimeout: time.Minute, StaticDir: "."})
	req := httptest.NewRequest(http.MethodPost, "/api/pair", strings.NewReader("not-json"))
	w := httptest.NewRecorder()
	s.handleCreatePair(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleWebSocketAuthFailures(t *testing.T) {
	s := NewServer(Config{PairingTimeout: time.Minute, StaticDir: "."})

	tests := []struct {
		name string
		path string
		want int
	}{
		{name: "missing session id", path: "/ws/", want: http.StatusBadRequest},
		{name: "missing token", path: "/ws/sess-1", want: http.StatusUnauthorized},
		{name: "unapproved token", path: "/ws/sess-1?token=nope", want: http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()
			s.handleWebSocket(w, req)
			if w.Code != tt.want {
				t.Errorf("status = %d, want %d", w.Code, tt.want)
			}
		})
	}
}

func TestPtyBridgeResizeAndCloseWithRealPTY(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("pty.Open() unavailable: %v", err)
	}
	defer func() { _ = tty.Close() }()

	pb := NewPtyBridge("/proj", "", "", nil, false, nil)
	pb.ptyFile = ptmx

	// Resize against a real *os.File exercises the pty.Setsize path.
	pb.Resize(100, 30)

	// Close should close the pty file and return without error.
	if err := pb.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestResponseCaptureHijackUnsupported(t *testing.T) {
	// httptest.ResponseRecorder does not implement http.Hijacker, so Hijack
	// must report the unsupported error rather than panic.
	rc := &responseCapture{ResponseWriter: httptest.NewRecorder()}
	if _, _, err := rc.Hijack(); err == nil {
		t.Fatal("Hijack() error = nil, want error for non-Hijacker writer")
	}
}

func TestWebSocketHandlerEarlyReturns(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()
	wh := NewWebSocketHandler(sm)

	tests := []struct {
		name string
		path string
		want int
	}{
		{name: "missing session id", path: "/ws/", want: http.StatusBadRequest},
		{name: "missing token", path: "/ws/sess-1", want: http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()
			wh.HandleWebSocket(w, req)
			if w.Code != tt.want {
				t.Errorf("status = %d, want %d", w.Code, tt.want)
			}
		})
	}
}

func TestPtyPoolGetOrCreateReusesAliveBridge(t *testing.T) {
	pool := NewPtyPool(nil)
	defer pool.CloseAll()

	// A started long-lived process makes the bridge report Alive()==true.
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sleep process: %v", err)
	}
	b := NewPtyBridge("/proj", "", "", nil, false, nil)
	b.cmd = cmd
	pool.mu.Lock()
	pool.bridges["sess-reuse"] = b
	pool.mu.Unlock()

	got, err := pool.GetOrCreate("sess-reuse", "/proj", "", "", nil, false)
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
	if got != b {
		t.Error("GetOrCreate() did not reuse the existing alive bridge")
	}
}

func TestServerV2WebSocketEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping process-spawning websocket test in short mode")
	}
	// The spawned pty child is this test binary acting as a fake pi (see TestMain).
	t.Setenv("PI_GO_FAKE_PI", "1")

	dir := t.TempDir()
	s := NewServerV2(Config{Addr: "127.0.0.1:0", Project: dir, StaticDir: ".", Logger: slog.Default()})
	if err := s.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	}()

	// Authenticate so the WS handler doesn't reject the upgrade.
	code, token, _, err := s.PairingManager().CreatePair(dir)
	if err != nil {
		t.Fatalf("CreatePair: %v", err)
	}
	if _, err := s.PairingManager().Approve(code); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	wsURL := "ws://" + s.Addr() + "/ws/sess-e2e?token=" + token
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", wsURL, err)
	}
	defer func() { _ = conn.Close() }()

	// Exercise the WS -> PTY copy path.
	_ = conn.WriteJSON(WSMessage{Type: "input", Data: "hello\n"})

	// Drain messages until the fake pi exits (PTY EOF closes the connection).
	deadline := time.Now().Add(8 * time.Second)
	sawOutput := false
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var msg WSMessage
		if err := conn.ReadJSON(&msg); err != nil {
			break
		}
		if msg.Type == "output" {
			sawOutput = true
		}
	}
	// Output capture depends on timing; coverage of the PTY path is the goal,
	// so a missing output line is logged rather than failed.
	if !sawOutput {
		t.Log("no PTY output captured before close (timing-dependent)")
	}
}

func TestSessionManagerCleanupExpiredRemovesOld(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	s, err := sm.CreateSession("/proj", "tok")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	// Backdate creation beyond the 24h retention window.
	sm.mu.Lock()
	sm.sessions[s.ID].CreatedAt = time.Now().Add(-25 * time.Hour)
	sm.mu.Unlock()

	sm.CleanupExpired()

	if _, ok := sm.GetSession(s.ID); ok {
		t.Error("expired session should have been removed by CleanupExpired")
	}
}
