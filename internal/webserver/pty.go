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

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

// WSMessage represents a message sent over the WebSocket.
type WSMessage struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

// PtyBridge manages a long-lived PTY process that survives WebSocket reconnects.
type PtyBridge struct {
	project   string
	sessionID string
	model     string
	log       *slog.Logger
	cmd       *exec.Cmd
	ptyFile   io.ReadWriteCloser

	mu   sync.Mutex
	conn *websocket.Conn // current attached WebSocket (nil when detached)

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

// NewPtyBridge creates a new PTY bridge for the given project and model.
func NewPtyBridge(project, model string, logger *slog.Logger) *PtyBridge {
	if logger == nil {
		logger = slog.Default()
	}
	return &PtyBridge{
		project: project,
		model:   model,
		log:     logger,
		done:    make(chan struct{}),
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
	pb.sessionID = sessionID

	// Register connection.
	pb.mu.Lock()
	pb.conn = conn
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
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
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

// startProcess starts the pi-go TUI process with PTY.
func (pb *PtyBridge) startProcess() error {
	piBin, err := os.Executable()
	if err != nil {
		piBin = "pi"
	}

	args := []string{}
	if pb.model != "" {
		args = append(args, "--model", pb.model)
	}
	cmd := exec.Command(piBin, args...)
	cmd.Dir = pb.project
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"CLICOLOR_FORCE=1",
		"TERMENV=truecolor",
	)

	sz := &pty.Winsize{Rows: 24, Cols: 80}
	ptyFile, err := pty.StartWithSize(cmd, sz)
	if err != nil {
		return pb.startProcessWithPipes()
	}

	pb.cmd = cmd
	pb.ptyFile = ptyFile

	go func() {
		err := pb.cmd.Wait()
		pb.log.Info("pty process exited", "session", pb.sessionID, "err", err)
		pb.closeOnce.Do(func() { close(pb.done) })
	}()

	pb.log.Info("pty started", "session", pb.sessionID, "pid", cmd.Process.Pid, "mode", "pty")
	return nil
}

// startProcessWithPipes starts the pi-go TUI process with regular pipes (fallback).
func (pb *PtyBridge) startProcessWithPipes() error {
	piBin, err := os.Executable()
	if err != nil {
		piBin = "pi"
	}

	args := []string{}
	if pb.model != "" {
		args = append(args, "--model", pb.model)
	}
	cmd := exec.Command(piBin, args...)
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
		pb.log.Info("pty process exited", "session", pb.sessionID, "err", err, "mode", "pipes")
		pb.closeOnce.Do(func() { close(pb.done) })
	}()

	pb.log.Info("pty started", "session", pb.sessionID, "pid", cmd.Process.Pid, "mode", "pipes")
	return nil
}

// Close kills the child process and releases the PTY fd.
func (pb *PtyBridge) Close() error {
	pb.closeOnce.Do(func() { close(pb.done) })

	pb.mu.Lock()
	defer pb.mu.Unlock()

	if pb.cmd != nil && pb.cmd.Process != nil {
		_ = pb.cmd.Process.Kill()
		_ = pb.cmd.Wait()
		pb.cmd = nil
	}

	if pb.ptyFile != nil {
		pb.ptyFile.Close()
		pb.ptyFile = nil
	}
	return nil
}

// copyPtyToWS reads from PTY and writes to WebSocket.
func (pb *PtyBridge) copyPtyToWS(conn *websocket.Conn, wsDone <-chan struct{}) {
	buf := make([]byte, 4096)
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
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			msg := WSMessage{Type: "output", Data: string(buf[:n])}
			if writeErr := conn.WriteJSON(msg); writeErr != nil {
				pb.log.Warn("ws write failed", "session", pb.sessionID, "err", writeErr)
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "use of closed") {
				pb.log.Warn("pty read error", "session", pb.sessionID, "err", err)
				msg := WSMessage{Type: "close", Data: err.Error()}
				_ = conn.WriteJSON(msg)
			}
			return
		}
	}
}

// copyWSToPty reads from WebSocket and writes to PTY.
func (pb *PtyBridge) copyWSToPty(conn *websocket.Conn, wsDone <-chan struct{}) {
	for {
		select {
		case <-wsDone:
			return
		case <-pb.done:
			return
		default:
		}

		msgType, reader, err := conn.NextReader()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				pb.log.Warn("ws read error", "session", pb.sessionID, "err", err)
			}
			return
		}

		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}

		data, err := io.ReadAll(reader)
		if err != nil {
			return
		}

		var msg WSMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			pb.mu.Lock()
			if pb.ptyFile != nil {
				_, _ = pb.ptyFile.Write(data)
			}
			pb.mu.Unlock()
			continue
		}

		switch msg.Type {
		case "input":
			pb.mu.Lock()
			if pb.ptyFile != nil {
				_, _ = pb.ptyFile.Write([]byte(msg.Data))
			}
			pb.mu.Unlock()
		case "resize":
			parts := strings.Split(msg.Data, "x")
			if len(parts) == 2 {
				w, _ := strconv.Atoi(parts[0])
				h, _ := strconv.Atoi(parts[1])
				pb.resize(w, h)
			}
		case "ping":
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"pong"}`))
		}
	}
}

// resize resizes the PTY to the given dimensions.
func (pb *PtyBridge) resize(cols, rows int) {
	if pb.ptyFile == nil {
		return
	}
	if ptyFile, ok := pb.ptyFile.(*os.File); ok {
		_ = pty.Setsize(ptyFile, &pty.Winsize{
			Rows: uint16(rows),
			Cols: uint16(cols),
		})
	}
}

// PtyPool manages long-lived PTY bridges keyed by session ID.
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
func (p *PtyPool) GetOrCreate(sessionID, project, model string) (*PtyBridge, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if b, ok := p.bridges[sessionID]; ok && b.Alive() {
		p.log.Info("pty reused", "session", sessionID)
		return b, nil
	}

	b := NewPtyBridge(project, model, p.log)
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
		b.Close()
		delete(p.bridges, id)
	}
}
