package webserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

func TestParseWinsize(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantCols int
		wantRows int
		wantOK   bool
	}{
		{name: "x separator from web ui", in: "120x40", wantCols: 120, wantRows: 40, wantOK: true},
		{name: "comma separator", in: "120,40", wantCols: 120, wantRows: 40, wantOK: true},
		{name: "space separator", in: "120 40", wantCols: 120, wantRows: 40, wantOK: true},
		{name: "surrounding whitespace", in: " 120 x 40 ", wantCols: 120, wantRows: 40, wantOK: true},
		{name: "uppercase separator", in: "120X40", wantCols: 120, wantRows: 40, wantOK: true},
		{name: "clamped high", in: "99999x99999", wantCols: maxTermCols, wantRows: maxTermRows, wantOK: true},
		{name: "clamped low cols", in: "1x40", wantCols: minTermCols, wantRows: 40, wantOK: true},
		{name: "zero rejected", in: "0x0", wantOK: false},
		{name: "negative rejected", in: "-5x-5", wantOK: false},
		{name: "single field", in: "120", wantOK: false},
		{name: "three fields", in: "1x2x3", wantOK: false},
		{name: "non numeric", in: "abcxdef", wantOK: false},
		{name: "empty", in: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cols, rows, ok := parseWinsize(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("parseWinsize(%q) ok = %v, want %v", tt.in, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if cols != tt.wantCols || rows != tt.wantRows {
				t.Errorf("parseWinsize(%q) = %dx%d, want %dx%d", tt.in, cols, rows, tt.wantCols, tt.wantRows)
			}
		})
	}
}

// The browser sends one resize per animation frame while a window is dragged.
// Only geometry changes may reach the child, or the TUI is buried in SIGWINCH.
func TestPtyBridgeResizeDedupes(t *testing.T) {
	pb, ptmx := bridgeWithPTY(t)

	if !pb.Resize(100, 30) {
		t.Fatal("first Resize() = false, want true")
	}
	if pb.Resize(100, 30) {
		t.Error("repeated Resize() = true, want false (no-op)")
	}
	if !pb.Resize(101, 30) {
		t.Error("changed Resize() = false, want true")
	}

	assertPtySize(t, ptmx, 101, 30)
}

func TestPtyBridgeResizeClampsOutOfRange(t *testing.T) {
	pb, ptmx := bridgeWithPTY(t)

	// Values beyond uint16 must not wrap around into a tiny terminal: a PTY
	// narrower than the browser makes the TUI overflow its own frame and the
	// terminal hard-wraps every long line.
	pb.Resize(70000, 70000)

	assertPtySize(t, ptmx, maxTermCols, maxTermRows)
}

// A reattaching browser usually reports the same geometry it had before the
// drop. TIOCSWINSZ raises no SIGWINCH for an unchanged size, so the bridge
// nudges the height first to force the child to repaint.
func TestPtyBridgeRepaintNudgeOnReattach(t *testing.T) {
	pb, ptmx := bridgeWithPTY(t)

	pb.Resize(120, 40)

	// Simulate what AttachWebSocket does for a fresh connection.
	pb.mu.Lock()
	pb.lastCols, pb.lastRows = 0, 0
	pb.pendingRepaint = true
	pb.mu.Unlock()

	pb.resizeFromClient(120, 40)
	assertPtySize(t, ptmx, 120, 40)

	pb.mu.Lock()
	pending := pb.pendingRepaint
	pb.mu.Unlock()
	if pending {
		t.Error("pendingRepaint still set after first client resize")
	}

	// Subsequent same-size resizes are plain no-ops again.
	if pb.Resize(120, 40) {
		t.Error("Resize() after nudge = true, want false")
	}
}

// The web UI sends "WxH" over the wire; the server must actually apply it,
// otherwise the child keeps rendering at 80x24 into a much wider browser.
func TestPtyBridgeResizeOverWebSocket(t *testing.T) {
	pb, ptmx := bridgeWithPTY(t)
	conn := serveBridgeWS(t, pb)

	if err := conn.WriteJSON(WSMessage{Type: "resize", Data: "133x37"}); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	waitFor(t, func() bool {
		size, err := pty.GetsizeFull(ptmx)
		return err == nil && size.Cols == 133 && size.Rows == 37
	}, "pty never resized to 133x37")
}

func TestPtyBridgeIgnoresMalformedResize(t *testing.T) {
	pb, ptmx := bridgeWithPTY(t)
	pb.Resize(100, 30)

	conn := serveBridgeWS(t, pb)

	for _, bad := range []string{"", "abc", "0x0", "120"} {
		if err := conn.WriteJSON(WSMessage{Type: "resize", Data: bad}); err != nil {
			t.Fatalf("WriteJSON(%q): %v", bad, err)
		}
	}

	// Round-trip a ping so the malformed messages are known to be processed.
	if err := conn.WriteJSON(WSMessage{Type: "ping"}); err != nil {
		t.Fatalf("WriteJSON(ping): %v", err)
	}
	readPong(t, conn)

	assertPtySize(t, ptmx, 100, 30)
}

// Without a pong the browser's heartbeat concludes the link is dead and tears
// the terminal down on every heartbeat interval.
func TestPtyBridgeRepliesToClientPing(t *testing.T) {
	pb, _ := bridgeWithPTY(t)
	conn := serveBridgeWS(t, pb)

	if err := conn.WriteJSON(WSMessage{Type: "ping"}); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	readPong(t, conn)
}

// A TUI frame is mostly multi-byte runes — box drawing, braille, escape
// sequences. Reads land wherever the kernel says, so runes get cut in half, and
// encoding/json turns each invalid byte into U+FFFD. The browser then paints
// replacement glyphs where the frame had a rail, which is unrecoverable: the
// bytes are gone by the time xterm sees them.
func TestSplitIncompleteRune(t *testing.T) {
	frame := "│⣿ Context ─ 42% ⣿│"
	raw := []byte(frame)

	for cut := 1; cut < len(raw); cut++ {
		var got []byte

		send, carry := splitIncompleteRune(raw[:cut])
		got = append(got, send...)

		rest := append(append([]byte{}, carry...), raw[cut:]...)
		send, carry = splitIncompleteRune(rest)
		got = append(got, send...)
		got = append(got, carry...)

		if string(got) != frame {
			t.Fatalf("cut at %d: reassembled %q, want %q", cut, got, frame)
		}
		// Every chunk actually put on the wire must survive a JSON round trip
		// unchanged — that is the step that would otherwise substitute U+FFFD.
		if !utf8.Valid(raw[:cut][:len(raw[:cut])-len(carryOf(raw[:cut]))]) {
			t.Fatalf("cut at %d: emitted invalid UTF-8", cut)
		}
	}
}

func carryOf(b []byte) []byte {
	_, carry := splitIncompleteRune(b)
	return carry
}

func TestSplitIncompleteRuneEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		in       []byte
		wantSend int
		wantTail int
	}{
		{name: "empty", in: nil, wantSend: 0, wantTail: 0},
		{name: "pure ascii", in: []byte("hello"), wantSend: 5, wantTail: 0},
		{name: "complete 3-byte rune", in: []byte("│"), wantSend: 3, wantTail: 0},
		{name: "lead byte only", in: []byte{0xE2}, wantSend: 0, wantTail: 1},
		{name: "two of three bytes", in: []byte{0xE2, 0x94}, wantSend: 0, wantTail: 2},
		{name: "ascii then partial", in: []byte{'a', 0xF0, 0x9F}, wantSend: 1, wantTail: 2},
		// An invalid lead byte must be passed through, not held forever waiting
		// for continuation bytes that never come.
		{name: "invalid lead byte", in: []byte{'a', 0xFF}, wantSend: 2, wantTail: 0},
		{name: "stray continuation", in: []byte{0x94, 0x82}, wantSend: 2, wantTail: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			send, tail := splitIncompleteRune(tt.in)
			if len(send) != tt.wantSend || len(tail) != tt.wantTail {
				t.Errorf("splitIncompleteRune(%x) = (%d bytes, %d tail), want (%d, %d)",
					tt.in, len(send), len(tail), tt.wantSend, tt.wantTail)
			}
		})
	}
}

// End to end: a frame streamed through the bridge in awkward chunks must reach
// the browser byte-identical.
func TestPtyOutputSurvivesChunkBoundaries(t *testing.T) {
	frame := "\x1b[38;2;123;166;247m│⣿⣷⣿ pi-go ─────╮\x1b[0m\n"

	pr, pw := io.Pipe()
	pb := NewPtyBridge("/proj", "", "", nil, false, nil)
	pb.ptyFile = struct {
		io.Reader
		io.Writer
		io.Closer
	}{Reader: pr, Writer: io.Discard, Closer: pr}

	wsDone := make(chan struct{})
	readerDone := make(chan struct{})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(readerDone)
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		pb.copyPtyToWS(conn, wsDone)
	}))
	defer ts.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Feed the frame one byte at a time: every rune boundary gets straddled.
	go func() {
		for i := range len(frame) {
			_, _ = pw.Write([]byte{frame[i]})
		}
		_ = pw.Close()
	}()

	var got strings.Builder
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for got.Len() < len(frame) {
		var msg WSMessage
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("ReadJSON after %q: %v", got.String(), err)
		}
		if msg.Type != "output" {
			continue
		}
		got.WriteString(msg.Data)
	}

	if got.String() != frame {
		t.Errorf("streamed frame = %q, want %q", got.String(), frame)
	}
	if strings.ContainsRune(got.String(), utf8.RuneError) {
		t.Error("frame reached the browser with replacement characters")
	}

	close(wsDone)
	_ = conn.Close()
	<-readerDone
}

// bridgeWithPTY returns a bridge backed by a real PTY. The PTY is closed after
// any WebSocket helper has been torn down (cleanups run LIFO), so no goroutine
// is still resizing a closed descriptor.
func bridgeWithPTY(t *testing.T) (*PtyBridge, *os.File) {
	t.Helper()

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("pty.Open() unavailable: %v", err)
	}
	t.Cleanup(func() {
		_ = tty.Close()
		_ = ptmx.Close()
	})

	pb := NewPtyBridge("/proj", "", "", nil, false, nil)
	pb.ptyFile = ptmx
	return pb, ptmx
}

// serveBridgeWS wires a client WebSocket to pb.copyWSToPty and guarantees the
// reader goroutine has exited before the test's remaining cleanups run.
func serveBridgeWS(t *testing.T, pb *PtyBridge) *websocket.Conn {
	t.Helper()

	wsDone := make(chan struct{})
	readerDone := make(chan struct{})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(readerDone)
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		pb.copyWSToPty(conn, wsDone)
	}))

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		ts.Close()
		t.Fatalf("dial: %v", err)
	}

	t.Cleanup(func() {
		_ = conn.Close()
		close(wsDone)
		<-readerDone
		ts.Close()
	})

	return conn
}

func readPong(t *testing.T, conn *websocket.Conn) {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var reply WSMessage
	if err := conn.ReadJSON(&reply); err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}
	if reply.Type != "pong" {
		t.Fatalf("reply type = %q, want pong", reply.Type)
	}
}

func assertPtySize(t *testing.T, ptmx *os.File, cols, rows uint16) {
	t.Helper()

	size, err := pty.GetsizeFull(ptmx)
	if err != nil {
		t.Fatalf("GetsizeFull: %v", err)
	}
	if size.Cols != cols || size.Rows != rows {
		t.Errorf("pty size = %dx%d, want %dx%d", size.Cols, size.Rows, cols, rows)
	}
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}
