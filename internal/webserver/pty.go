package webserver

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/gorilla/websocket"
)

// WSMessage represents a message sent over the WebSocket.
type WSMessage struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

// PtyBridge manages a PTY process for terminal I/O.
type PtyBridge struct {
	project   string
	sessionID string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	stderr    io.ReadCloser
	pty       *os.File
	mu        sync.Mutex
	done      chan struct{}
}

// NewPtyBridge creates a new PTY bridge for the given project.
func NewPtyBridge(project string) *PtyBridge {
	return &PtyBridge{
		project: project,
		done:    make(chan struct{}),
	}
}

// HandleWebSocket handles bidirectional I/O between WebSocket and PTY.
func (pb *PtyBridge) HandleWebSocket(conn *websocket.Conn, sessionID string) {
	pb.sessionID = sessionID

	// Set read/write deadlines with ping interval
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-pb.done:
				return
			case <-ticker.C:
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	// Set pong handler
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Start PTY process
	if err := pb.startProcess(); err != nil {
		// Send error to client
		msg := WSMessage{Type: "error", Data: err.Error()}
		conn.WriteJSON(msg)
		return
	}
	defer pb.closeProcess()

	// Goroutine to read from PTY and send to WebSocket
	go pb.copyPtyToWS(conn)

	// Goroutine to read from WebSocket and write to PTY
	go pb.copyWSToPty(conn)

	// Wait for disconnect
	<-pb.done
}

// startProcess starts the pi-go process with PTY.
func (pb *PtyBridge) startProcess() error {
	// Find the pi-go binary
	piBin, err := os.Executable()
	if err != nil {
		// Fallback to looking in PATH
		piBin = "pi"
	}

	// Start pi-go with interactive terminal
	cmd := exec.Command(piBin, "run")
	cmd.Dir = pb.project

	// Create PTY
	pty, err := newPty()
	if err != nil {
		return fmt.Errorf("creating PTY: %w", err)
	}
	pb.pty = pty.master

	// Set PTY as process's stdin/stdout/stderr
	cmd.Stdin = pty.master
	cmd.Stdout = pty.master
	cmd.Stderr = pty.master

	// Set process group
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    int(pty.master.Fd()),
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		pty.master.Close()
		pty.slave.Close()
		return fmt.Errorf("starting process: %w", err)
	}

	pb.cmd = cmd
	pb.stdin = pty.master
	pb.stdout = pty.master // Same as stdin for PTY
	pb.stderr = nil

	// Close slave PTY - master is what we use
	pty.slave.Close()

	return nil
}

// closeProcess terminates the PTY process.
func (pb *PtyBridge) closeProcess() {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	if pb.cmd != nil && pb.cmd.Process != nil {
		pb.cmd.Process.Kill()
		pb.cmd.Wait()
		pb.cmd = nil
	}

	if pb.stdin != nil {
		pb.stdin.Close()
		pb.stdin = nil
	}

	if pb.pty != nil {
		pb.pty.Close()
		pb.pty = nil
	}

	close(pb.done)
}

// Close terminates the PTY process.
func (pb *PtyBridge) Close() error {
	pb.closeProcess()
	return nil
}

// copyPtyToWS reads from PTY and writes to WebSocket.
func (pb *PtyBridge) copyPtyToWS(conn *websocket.Conn) {
	buf := make([]byte, 4096)
	for {
		select {
		case <-pb.done:
			return
		default:
		}

		conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, err := pb.stdout.Read(buf)
		if n > 0 {
			msg := WSMessage{Type: "output", Data: string(buf[:n])}
			if err := conn.WriteJSON(msg); err != nil {
				return
			}
		}
		if err != nil {
			if err != io.EOF && !strings.Contains(err.Error(), "use of closed") {
				msg := WSMessage{Type: "close", Data: err.Error()}
				conn.WriteJSON(msg)
			}
			return
		}
	}
}

// copyWSToPty reads from WebSocket and writes to PTY.
func (pb *PtyBridge) copyWSToPty(conn *websocket.Conn) {
	for {
		select {
		case <-pb.done:
			return
		default:
		}

		conn.SetReadDeadline(time.Now().Add(60 * time.Second))

		msgType, reader, err := conn.NextReader()
		if err != nil {
			if !websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				return
			}
			return
		}

		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}

		// Read the full message
		data, err := io.ReadAll(reader)
		if err != nil {
			return
		}

		// Parse message
		var msg WSMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			// Try as raw input if not JSON
			pb.mu.Lock()
			if pb.stdin != nil {
				pb.stdin.Write(data)
			}
			pb.mu.Unlock()
			continue
		}

		switch msg.Type {
		case "input":
			pb.mu.Lock()
			if pb.stdin != nil {
				pb.stdin.Write([]byte(msg.Data))
			}
			pb.mu.Unlock()
		case "resize":
			// Parse WxH format
			parts := strings.Split(msg.Data, "x")
			if len(parts) == 2 {
				w, _ := strconv.Atoi(parts[0])
				h, _ := strconv.Atoi(parts[1])
				pb.resize(w, h)
			}
		case "ping":
			// Respond with pong
			conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"pong"}`))
		}
	}
}

// resize resizes the PTY to the given dimensions.
func (pb *PtyBridge) resize(cols, rows int) {
	// On Unix, we can use ioctl to resize
	if pb.pty == nil {
		return
	}

	// Set window size using TIOCSWINSZ
	ws := &windowSize{
		Rows: uint16(rows),
		Cols: uint16(cols),
		X:    0,
		Y:    0,
	}

	// TIOCSWINSZ = 0x5414 on most systems
	ioctl(uintptr(pb.pty.Fd()), uintptr(0x5414), uintptr(unsafe.Pointer(ws)))
}

// pty represents a pseudo-terminal pair.
type pty struct {
	master *os.File
	slave  *os.File
}

// windowSize represents the size of a terminal window.
type windowSize struct {
	Rows uint16
	Cols uint16
	X    uint16
	Y    uint16
}

// newPty creates a new pseudo-terminal pair.
func newPty() (*pty, error) {
	// Open master PTY
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}

	// Grant access to slave
	if err := grantPty(master); err != nil {
		master.Close()
		return nil, err
	}

	// Unlock slave
	if err := unlockPty(master); err != nil {
		master.Close()
		return nil, err
	}

	// Get slave name
	name, err := ptsname(master)
	if err != nil {
		master.Close()
		return nil, err
	}

	// Open slave PTY
	slave, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		master.Close()
		return nil, err
	}

	return &pty{master: master, slave: slave}, nil
}

// grantPty grants access to the PTY.
func grantPty(f *os.File) error {
	// TIOCGRANT = 0x5419
	return ioctl(uintptr(f.Fd()), 0x5419, 0)
}

// unlockPty unlocks the PTY.
func unlockPty(f *os.File) error {
	// TIOCUNLOCK = 0x5418
	return ioctl(uintptr(f.Fd()), 0x5418, 0)
}

// ptsname returns the name of the slave PTY.
func ptsname(f *os.File) (string, error) {
	var n int
	err := ioctl(uintptr(f.Fd()), uintptr(TIOCGPTN), uintptr(unsafe.Pointer(&n)))
	if err != nil {
		return "", err
	}
	return "/dev/pts/" + strconv.Itoa(n), nil
}

// ioctl performs an ioctl syscall.
func ioctl(fd, req, arg uintptr) error {
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg)
	return err
}

// TIOCGPTN is the ioctl number for getting PTY name.
const TIOCGPTN = 0x80045430
