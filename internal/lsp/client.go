package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultRequestTimeout is the maximum time to wait for a response.
const DefaultRequestTimeout = 30 * time.Second

// Client is a JSON-RPC 2.0 client that communicates with an LSP server
// over stdio using Content-Length framing.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	nextID  atomic.Int64
	writeMu sync.Mutex // protects stdin writes
	pendMu  sync.Mutex // protects pending map
	pending map[int]chan *Response
	closed  atomic.Bool
	done    chan struct{} // closed when reader exits

	// NotificationHandler is called for server-initiated notifications.
	// It is called from the reader goroutine; it must not block.
	NotificationHandler func(method string, params json.RawMessage)
}

// NewClient starts an LSP server subprocess and returns a client connected
// to it via stdio. The caller must call Close() when done.
func NewClient(command string, args ...string) (*Client, error) {
	cmd := exec.Command(command, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", command, err)
	}

	c := &Client{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		pending: make(map[int]chan *Response),
		done:    make(chan struct{}),
	}

	go c.readLoop()

	return c, nil
}

// Request sends a JSON-RPC request and waits for the response.
// The context controls the timeout; if no deadline is set, DefaultRequestTimeout is used.
func (c *Client) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("client closed")
	}

	id := int(c.nextID.Add(1))

	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshaling params: %w", err)
		}
		rawParams = b
	}

	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  rawParams,
	}

	ch := make(chan *Response, 1)

	// Register pending response channel first.
	c.pendMu.Lock()
	c.pending[id] = ch
	c.pendMu.Unlock()

	// Write under write lock (separate from pending lock to avoid deadlock).
	if err := c.writeMessage(req); err != nil {
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
		return nil, fmt.Errorf("writing request: %w", err)
	}

	// Apply timeout if context has no deadline.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultRequestTimeout)
		defer cancel()
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-ctx.Done():
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
		return nil, fmt.Errorf("request %s (id=%d): %w", method, id, ctx.Err())
	case <-c.done:
		return nil, fmt.Errorf("server exited while waiting for response to %s", method)
	}
}

// Notify sends a JSON-RPC notification (no response expected).
func (c *Client) Notify(method string, params any) error {
	if c.closed.Load() {
		return fmt.Errorf("client closed")
	}

	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshaling params: %w", err)
		}
		rawParams = b
	}

	notif := Notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  rawParams,
	}

	return c.writeMessage(notif)
}

// Close sends shutdown and exit to the server, then kills the process.
func (c *Client) Close() error {
	if c.closed.Swap(true) {
		return nil // already closed
	}

	// Try graceful shutdown (best-effort, short timeout).
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, _ = c.Request(ctx, "shutdown", nil)
	cancel()

	_ = c.Notify("exit", nil)
	_ = c.stdin.Close()

	// Wait briefly for process exit, then force kill.
	waitDone := make(chan error, 1)
	go func() { waitDone <- c.cmd.Wait() }()

	select {
	case err := <-waitDone:
		select {
		case <-c.done:
		default:
			close(c.done)
		}
		return err
	case <-time.After(5 * time.Second):
		_ = c.cmd.Process.Kill()
		<-waitDone
		select {
		case <-c.done:
		default:
			close(c.done)
		}
		return nil
	}
}

// writeMessage encodes a message with Content-Length framing and writes to stdin.
func (c *Client) writeMessage(msg any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	// Combine header and body into a single write for atomicity.
	frame := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = io.WriteString(c.stdin, frame)
	return err
}

// readLoop reads Content-Length framed JSON-RPC messages from stdout.
func (c *Client) readLoop() {
	defer func() {
		// Fail all pending requests.
		c.pendMu.Lock()
		for id, ch := range c.pending {
			ch <- &Response{
				JSONRPC: "2.0",
				ID:      &id,
				Error:   &ResponseError{Code: -1, Message: "server exited"},
			}
			delete(c.pending, id)
		}
		c.pendMu.Unlock()

		select {
		case <-c.done:
		default:
			close(c.done)
		}
	}()

	reader := bufio.NewReader(c.stdout)
	for {
		// Read headers until empty line.
		contentLength := -1
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break // end of headers
			}
			if val, ok := strings.CutPrefix(line, "Content-Length: "); ok {
				n, err := strconv.Atoi(val)
				if err == nil {
					contentLength = n
				}
			}
		}

		if contentLength < 0 {
			continue
		}

		// Read body.
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(reader, body); err != nil {
			return
		}

		c.handleMessage(body)
	}
}

// handleMessage dispatches a raw JSON-RPC message to the appropriate handler.
func (c *Client) handleMessage(body []byte) {
	// Peek at the message to determine if it's a response or notification.
	var msg struct {
		ID     *int            `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		return
	}

	if msg.Method != "" && msg.ID == nil {
		// Server notification.
		if c.NotificationHandler != nil {
			c.NotificationHandler(msg.Method, msg.Params)
		}
		return
	}

	if msg.ID != nil {
		// Response to a request.
		var resp Response
		if err := json.Unmarshal(body, &resp); err != nil {
			return
		}
		c.pendMu.Lock()
		ch, ok := c.pending[*resp.ID]
		if ok {
			delete(c.pending, *resp.ID)
		}
		c.pendMu.Unlock()
		if ok {
			ch <- &resp
		}
	}
}
