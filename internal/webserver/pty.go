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
	baseURL   string
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

// NewPtyBridge creates a new PTY bridge for the given project, model, and base URL.
func NewPtyBridge(project, model, baseURL string, logger *slog.Logger) *PtyBridge {
	if logger == nil {
		logger = slog.Default()
	}
	return &PtyBridge{
		project: project,
		model:   model,
		baseURL: baseURL,
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
	if pb.baseURL != "" {
		args = append(args, "--url", pb.baseURL)
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
	if pb.baseURL != "" {
		args = append(args, "--url", pb.baseURL)
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
		pb.log.Info("pipe process exited", "session", pb.sessionID, "err", err)
		pb.closeOnce.Do(func() { close(pb.done) })
	}()

	pb.log.Info("pty started", "session", pb.sessionID, "pid", cmd.Process.Pid, "mode", "pipes")
	return nil
}

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
			msg := WSMessage{Type: "output", Data: string(buf[:n])}
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if werr := conn.WriteJSON(msg); werr != nil {
				return
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
			parts := strings.Split(msg.Data, ",")
			if len(parts) == 2 {
				cols, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
				rows, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
				pb.Resize(cols, rows)
			}
		}
	}
}

// Resize updates the PTY size. No-op when running in pipe fallback mode.
func (pb *PtyBridge) Resize(cols, rows int) {
	f, ok := pb.ptyFile.(*os.File)
	if !ok || f == nil {
		return
	}
	_ = pty.Setsize(f, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
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
func (p *PtyPool) GetOrCreate(sessionID, project, model, baseURL string) (*PtyBridge, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if b, ok := p.bridges[sessionID]; ok && b.Alive() {
		p.log.Info("pty reused", "session", sessionID)
		return b, nil
	}

	b := NewPtyBridge(project, model, baseURL, p.log)
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
