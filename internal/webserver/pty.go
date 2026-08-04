package webserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

// WSMessage represents a message sent over the WebSocket.
type WSMessage struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

// Terminal geometry bounds. A browser can propose nonsense (0 columns while the
// tab is hidden, five-digit values from a broken fit) and the PTY winsize fields
// are uint16, so every client-supplied size is clamped into this range.
const (
	minTermCols = 2
	maxTermCols = 1000
	minTermRows = 1
	maxTermRows = 1000

	defaultTermCols = 80
	defaultTermRows = 24

	wsWriteTimeout = 10 * time.Second
)

// PtyBridge manages a long-lived PTY process that survives WebSocket reconnects.
type PtyBridge struct {
	project   string
	sessionID string
	model     string
	baseURL   string
	headers   []string
	insecure  bool
	log       *slog.Logger
	cmd       *exec.Cmd
	ptyFile   io.ReadWriteCloser

	mu   sync.Mutex
	conn *websocket.Conn // current attached WebSocket (nil when detached)

	// Last geometry pushed to the PTY, used to drop the duplicate resizes a
	// browser emits while a window is being dragged.
	lastCols, lastRows int
	// Set on every attach: the first resize from a freshly (re)connected client
	// must force a full redraw even when the geometry did not change.
	pendingRepaint bool

	// writeMu serializes WebSocket writes. Gorilla allows only one concurrent
	// writer and output frames, pongs and keep-alive pings come from three
	// different goroutines.
	writeMu sync.Mutex

	done      chan struct{} // closed when PTY process exits
	closeOnce sync.Once
}

// pipeWrapper wraps stdin/stdout/stderr pipes to implement io.ReadWriteCloser
type pipeWrapper struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

func (pw *pipeWrapper) Read(p []byte) (n int, err error) {
	return pw.stdout.Read(p)
}

func (pw *pipeWrapper) Write(p []byte) (n int, err error) {
	return pw.stdin.Write(p)
}

func (pw *pipeWrapper) Close() error {
	pw.stdin.Close()
	pw.stdout.Close()
	pw.stderr.Close()
	return nil
}

// NewPtyBridge creates a new PTY bridge for the given project, model, base URL,
// optional extra HTTP headers (key=value), and insecure TLS toggle. All of these
// are forwarded to the spawned pi subprocess via command-line flags.
func NewPtyBridge(project, model, baseURL string, headers []string, insecure bool, logger *slog.Logger) *PtyBridge {
	if logger == nil {
		logger = slog.Default()
	}
	return &PtyBridge{
		project:  project,
		model:    model,
		baseURL:  baseURL,
		headers:  headers,
		insecure: insecure,
		log:      logger,
		done:     make(chan struct{}),
	}
}

// Start launches the PTY child process. Must be called before AttachWebSocket.
func (pb *PtyBridge) Start() error {
	return pb.startProcess()
}

// Alive reports whether the PTY process is still running.
func (pb *PtyBridge) Alive() bool {
	select {
	case <-pb.done:
		return false
	default:
		return pb.cmd != nil && pb.cmd.Process != nil
	}
}

// AttachWebSocket connects a WebSocket to this PTY bridge.
// Blocks until the WebSocket disconnects or the PTY process exits.
// After return the PTY is still alive; call Close() to kill it.
func (pb *PtyBridge) AttachWebSocket(conn *websocket.Conn, sessionID string) {
	// Register connection and session id atomically. Forget the last geometry so
	// the size the reattaching client reports is always pushed through, and arm
	// the repaint nudge that gets it a fresh frame instead of the stale one left
	// in its scrollback.
	pb.mu.Lock()
	pb.sessionID = sessionID
	pb.conn = conn
	pb.lastCols, pb.lastRows = 0, 0
	pb.pendingRepaint = true
	pb.mu.Unlock()

	// Detach on exit.
	defer func() {
		pb.mu.Lock()
		if pb.conn == conn {
			pb.conn = nil
		}
		pb.mu.Unlock()
	}()

	// Per-connection done channel — closed when this WS session ends,
	// NOT when the PTY exits.
	wsDone := make(chan struct{})
	wsOnce := sync.Once{}
	closeWS := func() { wsOnce.Do(func() { close(wsDone) }) }

	// Keep-alive pings.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-wsDone:
				return
			case <-pb.done:
				closeWS()
				return
			case <-ticker.C:
				if err := pb.writeControl(conn, websocket.PingMessage); err != nil {
					closeWS()
					return
				}
			}
		}
	}()

	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})
	_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))

	var wg sync.WaitGroup
	wg.Add(2)

	// PTY → WebSocket
	go func() {
		defer wg.Done()
		pb.copyPtyToWS(conn, wsDone)
		closeWS()
	}()

	// WebSocket → PTY
	go func() {
		defer wg.Done()
		pb.copyWSToPty(conn, wsDone)
		closeWS()
	}()

	wg.Wait()
}

// HandleWebSocket is a convenience wrapper: Start + AttachWebSocket + Close.
// Used when session persistence is not needed.
func (pb *PtyBridge) HandleWebSocket(conn *websocket.Conn, sessionID string) {
	if err := pb.Start(); err != nil {
		pb.log.Error("pty start failed", "session", sessionID, "err", err)
		msg := WSMessage{Type: "error", Data: err.Error()}
		_ = conn.WriteJSON(msg)
		return
	}
	pb.AttachWebSocket(conn, sessionID)
	pb.Close()
}

// childArgs builds the CLI args passed through to the spawned pi subprocess.
func (pb *PtyBridge) childArgs() []string {
	args := []string{}
	if pb.model != "" {
		args = append(args, "--model", pb.model)
	}
	if pb.baseURL != "" {
		args = append(args, "--url", pb.baseURL)
	}
	for _, h := range pb.headers {
		args = append(args, "--header", h)
	}
	if pb.insecure {
		args = append(args, "--insecure")
	}
	return args
}

// startProcess starts the pi-go TUI process with PTY.
func (pb *PtyBridge) startProcess() error {
	piBin, err := os.Executable()
	if err != nil {
		piBin = "pi"
	}

	cmd := exec.Command(piBin, pb.childArgs()...)
	cmd.Dir = pb.project
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"CLICOLOR_FORCE=1",
		"TERMENV=truecolor",
	)

	sz := &pty.Winsize{Rows: defaultTermRows, Cols: defaultTermCols}
	ptyFile, err := pty.StartWithSize(cmd, sz)
	if err != nil {
		return pb.startProcessWithPipes()
	}

	pb.cmd = cmd
	pb.ptyFile = ptyFile

	go func() {
		err := pb.cmd.Wait()
		pb.mu.Lock()
		sid := pb.sessionID
		pb.mu.Unlock()
		pb.log.Info("pty process exited", "session", sid, "err", err)
		pb.closeOnce.Do(func() { close(pb.done) })
	}()

	pb.mu.Lock()
	sid := pb.sessionID
	pb.mu.Unlock()
	pb.log.Info("pty started", "session", sid, "pid", cmd.Process.Pid, "mode", "pty")
	return nil
}

// startProcessWithPipes starts the pi-go TUI process with regular pipes (fallback).
func (pb *PtyBridge) startProcessWithPipes() error {
	piBin, err := os.Executable()
	if err != nil {
		piBin = "pi"
	}

	cmd := exec.Command(piBin, pb.childArgs()...)
	cmd.Dir = pb.project
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"CLICOLOR_FORCE=1",
		"TERMENV=truecolor",
	)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("creating stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("creating stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return fmt.Errorf("creating stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		stderr.Close()
		return fmt.Errorf("starting process: %w", err)
	}

	pb.cmd = cmd
	pb.ptyFile = &pipeWrapper{stdin: stdin, stdout: stdout, stderr: stderr}

	go func() {
		err := pb.cmd.Wait()
		pb.log.Info("pipe process exited", "session", pb.sessionID, "err", err)
		pb.closeOnce.Do(func() { close(pb.done) })
	}()

	pb.log.Info("pty started", "session", pb.sessionID, "pid", cmd.Process.Pid, "mode", "pipes")
	return nil
}

func (pb *PtyBridge) copyPtyToWS(conn *websocket.Conn, wsDone <-chan struct{}) {
	// A full-screen TUI repaint is tens of kilobytes of escape sequences. A
	// small buffer chops it across many frames, which xterm.js renders one at a
	// time — the visible tearing during redraws in web mode.
	buf := make([]byte, 32*1024)

	// Holds the first bytes of a multi-byte rune that a read cut in half; see
	// splitIncompleteRune.
	var carry []byte

	for {
		select {
		case <-wsDone:
			return
		case <-pb.done:
			return
		default:
		}

		n, err := pb.ptyFile.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if len(carry) > 0 {
				chunk = append(carry, chunk...)
			}

			send, tail := splitIncompleteRune(chunk)
			// Copy: tail points into buf, which the next Read overwrites.
			carry = append([]byte(nil), tail...)
			if len(send) > 0 {
				msg := WSMessage{Type: "output", Data: string(send)}
				if werr := pb.writeJSON(conn, msg); werr != nil {
					return
				}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				pb.log.Debug("pty read ended", "err", err)
			}
			return
		}
	}
}

// splitIncompleteRune splits b just before a trailing multi-byte UTF-8 sequence
// that has not fully arrived, returning the part safe to send and the bytes to
// prepend to the next read.
//
// This matters because the payload is JSON. encoding/json replaces every
// invalid UTF-8 byte with U+FFFD, so a rune straddling a read boundary does not
// merely arrive late — it arrives permanently mangled, and the browser paints
// replacement glyphs where the TUI drew box-drawing or braille. The same cut
// through an escape sequence's bytes turns the rest of the sequence into
// literal text on screen.
func splitIncompleteRune(b []byte) (send, tail []byte) {
	// A rune is at most 4 bytes, so only the last 3 can be a partial prefix.
	for i := 1; i <= 3 && i <= len(b); i++ {
		c := b[len(b)-i]
		if !utf8.RuneStart(c) {
			continue // continuation byte — keep walking back to the lead byte
		}
		if utf8SeqLen(c) > i {
			return b[:len(b)-i], b[len(b)-i:]
		}
		return b, nil
	}
	return b, nil
}

// utf8SeqLen returns how many bytes the rune starting with lead byte c occupies.
// An invalid lead byte reports 1 so it is passed through rather than held back
// forever waiting for continuation bytes that will never come.
func utf8SeqLen(c byte) int {
	switch {
	case c < 0x80:
		return 1
	case c&0xE0 == 0xC0:
		return 2
	case c&0xF0 == 0xE0:
		return 3
	case c&0xF8 == 0xF0:
		return 4
	default:
		return 1
	}
}

func (pb *PtyBridge) copyWSToPty(conn *websocket.Conn, wsDone <-chan struct{}) {
	for {
		select {
		case <-wsDone:
			return
		case <-pb.done:
			return
		default:
		}

		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var msg WSMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			// Legacy raw input support.
			_, _ = pb.ptyFile.Write(data)
			continue
		}

		switch msg.Type {
		case "input":
			_, _ = pb.ptyFile.Write([]byte(msg.Data))
		case "resize":
			cols, rows, ok := parseWinsize(msg.Data)
			if !ok {
				pb.log.Debug("ignoring malformed resize", "data", msg.Data)
				continue
			}
			pb.resizeFromClient(cols, rows)
		case "ping":
			// The browser drives its own heartbeat on top of the protocol-level
			// ping: without this reply it assumes the link is dead and tears the
			// terminal down every heartbeat interval.
			_ = pb.writeJSON(conn, WSMessage{Type: "pong"})
		}
	}
}

// parseWinsize parses a client geometry string. Both "120x40" (what the web UI
// sends) and "120,40" are accepted, and the result is clamped to a size the PTY
// can actually hold. ok is false when the payload carries no usable numbers.
func parseWinsize(data string) (cols, rows int, ok bool) {
	fields := strings.FieldsFunc(data, func(r rune) bool {
		return r == 'x' || r == 'X' || r == ',' || r == ';' || r == ' ' || r == '\t'
	})
	if len(fields) != 2 {
		return 0, 0, false
	}

	cols, colErr := strconv.Atoi(strings.TrimSpace(fields[0]))
	rows, rowErr := strconv.Atoi(strings.TrimSpace(fields[1]))
	if colErr != nil || rowErr != nil || cols <= 0 || rows <= 0 {
		return 0, 0, false
	}

	return clamp(cols, minTermCols, maxTermCols), clamp(rows, minTermRows, maxTermRows), true
}

func clamp(v, lo, hi int) int {
	return min(max(v, lo), hi)
}

// resizeFromClient applies a browser-driven resize. The first one after each
// attach is preceded by a one-row nudge: TIOCSWINSZ raises no SIGWINCH when the
// geometry is unchanged, so a reconnecting client that fits to the same size
// would otherwise sit on the stale frame until the next keystroke.
func (pb *PtyBridge) resizeFromClient(cols, rows int) {
	pb.mu.Lock()
	nudge := pb.pendingRepaint
	pb.pendingRepaint = false
	pb.mu.Unlock()

	if nudge && rows > minTermRows {
		pb.setSize(cols, rows-1)
	}
	pb.Resize(cols, rows)
}

// Resize updates the PTY size, clamping out-of-range geometry. No-op when
// running in pipe fallback mode or when the size is unchanged. Reports whether
// a new size reached the PTY.
func (pb *PtyBridge) Resize(cols, rows int) bool {
	return pb.setSize(clamp(cols, minTermCols, maxTermCols), clamp(rows, minTermRows, maxTermRows))
}

func (pb *PtyBridge) setSize(cols, rows int) bool {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	if cols == pb.lastCols && rows == pb.lastRows {
		return false
	}

	f, ok := pb.ptyFile.(*os.File)
	if !ok || f == nil {
		return false
	}
	if err := pty.Setsize(f, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)}); err != nil {
		pb.log.Debug("pty resize failed", "cols", cols, "rows", rows, "err", err)
		return false
	}

	pb.lastCols, pb.lastRows = cols, rows
	return true
}

// writeJSON sends a message to the browser under the shared write lock.
func (pb *PtyBridge) writeJSON(conn *websocket.Conn, msg WSMessage) error {
	pb.writeMu.Lock()
	defer pb.writeMu.Unlock()

	_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return conn.WriteJSON(msg)
}

// writeControl sends a control frame under the shared write lock.
func (pb *PtyBridge) writeControl(conn *websocket.Conn, messageType int) error {
	pb.writeMu.Lock()
	defer pb.writeMu.Unlock()

	_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return conn.WriteMessage(messageType, nil)
}

// Close terminates the PTY bridge and underlying process.
func (pb *PtyBridge) Close() error {
	var retErr error
	pb.closeOnce.Do(func() {
		close(pb.done)
		if pb.conn != nil {
			_ = pb.conn.Close()
			pb.conn = nil
		}
		if pb.ptyFile != nil {
			if err := pb.ptyFile.Close(); err != nil && retErr == nil {
				retErr = err
			}
			pb.ptyFile = nil
		}
		if pb.cmd != nil && pb.cmd.Process != nil {
			if err := pb.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) && retErr == nil {
				retErr = err
			}
		}
	})
	return retErr
}

// PtyPool manages PTY bridges keyed by session ID.
type PtyPool struct {
	mu      sync.Mutex
	bridges map[string]*PtyBridge
	log     *slog.Logger
}

// NewPtyPool creates a new PTY pool.
func NewPtyPool(logger *slog.Logger) *PtyPool {
	if logger == nil {
		logger = slog.Default()
	}
	return &PtyPool{bridges: make(map[string]*PtyBridge), log: logger}
}

// GetOrCreate returns an existing live PTY bridge for the session,
// or creates and starts a new one.
func (p *PtyPool) GetOrCreate(sessionID, project, model, baseURL string, headers []string, insecure bool) (*PtyBridge, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if b, ok := p.bridges[sessionID]; ok && b.Alive() {
		p.log.Info("pty reused", "session", sessionID)
		return b, nil
	}

	b := NewPtyBridge(project, model, baseURL, headers, insecure, p.log)
	if err := b.Start(); err != nil {
		return nil, err
	}
	p.bridges[sessionID] = b

	go func() {
		<-b.done
		p.mu.Lock()
		if p.bridges[sessionID] == b {
			delete(p.bridges, sessionID)
			p.log.Info("pty removed from pool", "session", sessionID)
		}
		p.mu.Unlock()
	}()

	return b, nil
}

// CloseAll terminates all PTY bridges in the pool.
func (p *PtyPool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, b := range p.bridges {
		_ = b.Close()
		delete(p.bridges, id)
	}
}
