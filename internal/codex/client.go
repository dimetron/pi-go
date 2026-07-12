package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// BinaryName is the expected name of the Codex CLI binary.
const BinaryName = "codex"

// rpcTimeout caps a single request/response round trip against a hung
// app-server. Mirrors the ACP runners: long enough for a cold start, short
// enough to surface a missing binary or a failed login quickly.
const rpcTimeout = 60 * time.Second

// DefaultBinaryPaths lists common installation locations for the codex CLI.
var DefaultBinaryPaths = []string{
	"codex",
	".local/bin/codex",
	"/usr/local/bin/codex",
	"/usr/bin/codex",
	"/opt/homebrew/bin/codex",
}

// DefaultArgs puts the codex CLI into app-server (JSON-RPC over stdio) mode.
var DefaultArgs = []string{"app-server"}

// EnvCodexCmd overrides the codex command. Format: "binary arg1 arg2 ..." or
// a bare "binary", in which case DefaultArgs are appended.
const EnvCodexCmd = "PI_CODEX_CMD"

// notifBufferSize bounds the notification channel. Sends block (rather than
// drop) when it fills, because dropping a turn/completed would hang the
// session; the session drains it continuously, so blocking is bounded.
const notifBufferSize = 256

// stderrBufferSize bounds the buffered stderr line channel. Unlike
// notifications, stderr lines are best-effort: they are always accumulated in
// full for the final result, so a drop only costs live visibility.
const stderrBufferSize = 256

// ClientOpts configures the app-server subprocess.
type ClientOpts struct {
	CWD     string   // working directory of the subprocess
	Env     []string // full environment for the subprocess (not merged with os.Environ)
	Command []string // command override; when set, used verbatim (tests)
}

// Client wraps a `codex app-server` subprocess speaking JSON-RPC 2.0 over
// newline-delimited JSON on stdin/stdout.
//
// A single reader goroutine owns stdout: it routes responses to the pending
// request that carries the same ID and forwards notifications to the channel
// returned by notifications(). A second goroutine drains stderr, and a third
// reaps the process once both are done.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *stderrBuffer

	notifCh  chan JSONRPCNotification
	stderrCh chan string

	// closing is closed by close() to unblock a reader that is parked on a
	// notification send after the session has stopped consuming.
	closing chan struct{}
	// exited is closed once the subprocess has been reaped.
	exited chan struct{}

	mu       sync.Mutex
	nextID   int
	pending  map[int]chan JSONRPCResponse
	closed   bool
	waitErr  error
	reaped   bool
	stdinOne sync.Once
}

// NewClient spawns `codex app-server`, performs the initialize handshake and
// sends the initialized notification. The returned Client is ready for
// thread/start.
func NewClient(ctx context.Context, opts ClientOpts) (*Client, error) {
	binary, args, err := resolveCommand(opts.Command)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	if opts.CWD != "" {
		cmd.Dir = opts.CWD
	}
	if len(opts.Env) > 0 {
		cmd.Env = opts.Env
	}
	cmd.WaitDelay = 3 * time.Second
	// Start codex in its own process group and kill the whole group on
	// cancel/timeout, so shell commands it spawns don't outlive it.
	setPlatformAttrs(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("codex stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("codex stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("codex stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s app-server: %w", BinaryName, err)
	}

	c := newClient(cmd, stdin, stdout, stderr)

	if err := c.handshake(ctx); err != nil {
		_ = c.close()
		return nil, err
	}
	return c, nil
}

// newClient wires a Client around already-open streams and starts its reader,
// stderr-drain and reaper goroutines. cmd may be nil, which makes the client
// stream-only (used by tests).
func newClient(cmd *exec.Cmd, stdin io.WriteCloser, stdout, stderr io.Reader) *Client {
	c := &Client{
		cmd:      cmd,
		stdin:    stdin,
		stderr:   &stderrBuffer{},
		notifCh:  make(chan JSONRPCNotification, notifBufferSize),
		stderrCh: make(chan string, stderrBufferSize),
		closing:  make(chan struct{}),
		exited:   make(chan struct{}),
		pending:  make(map[int]chan JSONRPCResponse),
	}

	readerDone := make(chan struct{})
	stderrDone := make(chan struct{})

	go func() {
		defer close(readerDone)
		c.readLoop(stdout)
	}()
	go func() {
		defer close(stderrDone)
		c.drainStderr(stderr)
	}()
	go func() {
		defer close(c.exited)
		// Reap only after both pipes hit EOF: cmd.Wait closes them, which
		// would otherwise race the readers and truncate the last messages.
		<-readerDone
		<-stderrDone
		// Both stderr writers (drainStderr and readLoop, which routes
		// unparseable stdout here) are done, so this is the one safe place to
		// close the channel.
		close(c.stderrCh)
		if cmd == nil {
			return
		}
		err := cmd.Wait()
		c.mu.Lock()
		c.waitErr = err
		c.reaped = true
		c.mu.Unlock()
	}()

	return c
}

// handshake performs initialize + initialized. The delta notifications are
// opted out of: pi-go renders completed items, so per-token deltas would only
// flood the notification channel.
func (c *Client) handshake(ctx context.Context) error {
	raw, err := c.request(ctx, MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Title: "pi-go", Name: "pi-go", Version: "dev"},
		Capabilities: InitializeCaps{
			OptOutNotificationMethods: []string{
				"item/agentMessage/delta",
				"item/reasoning/summaryTextDelta",
				"item/reasoning/summaryPartAdded",
				"item/reasoning/textDelta",
			},
		},
	})
	if err != nil {
		return fmt.Errorf("codex initialize: %w", err)
	}
	var resp InitializeResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("codex initialize response: %w", err)
	}
	if err := c.notify(MethodInitialized, map[string]any{}); err != nil {
		return fmt.Errorf("codex initialized: %w", err)
	}
	return nil
}

// request sends a JSON-RPC request and waits for the matching response. It
// returns early if ctx is done, the app-server exits, or rpcTimeout elapses.
func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("codex client is closed")
	}
	c.nextID++
	id := c.nextID
	replyCh := make(chan JSONRPCResponse, 1)
	c.pending[id] = replyCh
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	req := JSONRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	if err := c.writeMessage(req); err != nil {
		return nil, fmt.Errorf("codex %s: %w", method, err)
	}

	select {
	case resp := <-replyCh:
		if resp.Error != nil {
			return nil, fmt.Errorf("codex %s: %w", method, resp.Error)
		}
		return resp.Result, nil
	case <-c.exited:
		return nil, fmt.Errorf("codex %s: app-server exited: %w", method, c.exitError())
	case <-ctx.Done():
		return nil, fmt.Errorf("codex %s: %w", method, ctx.Err())
	}
}

// requestNoWait sends a JSON-RPC request and does not wait for its response.
//
// It exists for turn/interrupt: Cancel runs with the orchestrator's lock held,
// so it must not block for up to rpcTimeout waiting on an app-server that is
// about to be killed anyway. The reply, if it ever arrives, matches no pending
// request and is dropped.
func (c *Client) requestNoWait(method string, params any) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("codex client is closed")
	}
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	if err := c.writeMessage(JSONRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return fmt.Errorf("codex %s: %w", method, err)
	}
	return nil
}

// notify sends a JSON-RPC notification; no response is expected.
func (c *Client) notify(method string, params any) error {
	return c.writeMessage(JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  mustRaw(params),
	})
}

func mustRaw(params any) json.RawMessage {
	data, err := json.Marshal(params)
	if err != nil {
		return json.RawMessage("null")
	}
	return data
}

// writeMessage marshals msg and writes it as one JSONL line.
func (c *Client) writeMessage(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("codex client is closed")
	}
	if _, err := c.stdin.Write(data); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// notifications returns the stream of server-initiated notifications. It is
// closed when the app-server's stdout hits EOF.
func (c *Client) notifications() <-chan JSONRPCNotification { return c.notifCh }

// stderrLines returns the stream of stderr lines. It is closed when stderr
// hits EOF.
func (c *Client) stderrLines() <-chan string { return c.stderrCh }

// stderrText returns everything the app-server has written to stderr so far.
func (c *Client) stderrText() string { return strings.TrimSpace(c.stderr.String()) }

// exitError reports why the subprocess exited, or nil if it exited cleanly or
// has not been reaped yet.
func (c *Client) exitError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.waitErr
}

// close terminates the subprocess and releases the pending requests. It is
// safe to call more than once.
func (c *Client) close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	cmd := c.cmd
	c.mu.Unlock()

	close(c.closing)
	c.stdinOne.Do(func() {
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
	})

	if cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Kill(); err != nil && !errIsProcessDone(err) {
			return fmt.Errorf("kill %s app-server: %w", BinaryName, err)
		}
	}
	return nil
}

func errIsProcessDone(err error) bool {
	return errors.Is(err, os.ErrProcessDone)
}

// readLoop consumes stdout line by line, routing each JSON-RPC message:
//   - id + method → a server-initiated request; answered with "method not
//     found" since pi-go exposes no callable methods to codex
//   - id only     → a response; handed to the waiting request
//   - method only → a notification; forwarded to the session
func (c *Client) readLoop(stdout io.Reader) {
	defer close(c.notifCh)
	defer c.failPending()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 256*1024), maxLineBytes)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			// Not JSON-RPC — codex occasionally logs to stdout. Surface it as
			// diagnostic output rather than dropping it silently.
			c.emitStderr("codex: unparseable stdout: " + string(line))
			continue
		}

		switch {
		case msg.ID != nil && msg.Method != "":
			c.rejectServerRequest(*msg.ID, msg.Method)
		case msg.ID != nil:
			c.routeResponse(JSONRPCResponse{
				JSONRPC: msg.JSONRPC,
				ID:      *msg.ID,
				Result:  msg.Result,
				Error:   msg.Error,
			})
		case msg.Method != "":
			select {
			case c.notifCh <- JSONRPCNotification{JSONRPC: msg.JSONRPC, Method: msg.Method, Params: msg.Params}:
			case <-c.closing:
				return
			}
		}
	}
}

// maxLineBytes caps a single JSONL line. Codex items can carry large file
// diffs, so this is generous compared to bufio's 64KB default.
const maxLineBytes = 8 * 1024 * 1024

// routeResponse hands a response to the request waiting on its ID. Unknown IDs
// (a late reply to an abandoned request) are dropped.
func (c *Client) routeResponse(resp JSONRPCResponse) {
	c.mu.Lock()
	ch, ok := c.pending[resp.ID]
	c.mu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- resp:
	default:
	}
}

// rejectServerRequest answers a server-initiated request with a JSON-RPC
// "method not found". pi-go declares no capabilities that would make codex
// call back into it, so any such request is a protocol surprise; replying
// keeps the app-server from blocking on us.
func (c *Client) rejectServerRequest(id int, method string) {
	_ = c.writeMessage(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: -32601, Message: "method not supported by pi-go: " + method},
	})
}

// failPending unblocks every in-flight request once the stream is dead.
func (c *Client) failPending() {
	c.mu.Lock()
	pending := c.pending
	c.pending = make(map[int]chan JSONRPCResponse)
	c.mu.Unlock()

	for id, ch := range pending {
		select {
		case ch <- JSONRPCResponse{ID: id, Error: &RPCError{Code: -32000, Message: "codex app-server closed the connection"}}:
		default:
		}
	}
}

// drainStderr forwards stderr lines live and retains them all for the final
// result. It does not close stderrCh — readLoop writes there too, so the
// channel is closed by the reaper once both are finished.
func (c *Client) drainStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = c.stderr.Write([]byte(line + "\n"))
		if strings.TrimSpace(line) == "" {
			continue
		}
		c.sendStderr(line)
	}
}

// emitStderr records a synthetic diagnostic line as if codex had written it to
// stderr, so it reaches both the live stream and the final result.
func (c *Client) emitStderr(line string) {
	_, _ = c.stderr.Write([]byte(line + "\n"))
	c.sendStderr(line)
}

// sendStderr is best-effort: the full text is retained in c.stderr regardless.
func (c *Client) sendStderr(line string) {
	select {
	case c.stderrCh <- line:
	default:
	}
}

// stderrBuffer is a concurrency-safe accumulator for subprocess stderr.
type stderrBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *stderrBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *stderrBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// resolveCommand determines the binary and args for the app-server subprocess,
// honoring (in order) an explicit command override, the PI_CODEX_CMD env var,
// and finally DefaultBinaryPaths.
func resolveCommand(override []string) (string, []string, error) {
	if len(override) > 0 {
		return override[0], append([]string(nil), override[1:]...), nil
	}

	if envCmd := strings.TrimSpace(os.Getenv(EnvCodexCmd)); envCmd != "" {
		parts := strings.Fields(envCmd)
		binary := parts[0]
		args := parts[1:]
		if len(args) == 0 {
			args = append([]string(nil), DefaultArgs...)
		}
		return binary, args, nil
	}

	binary, err := findBinary(DefaultBinaryPaths)
	if err != nil {
		return "", nil, err
	}
	return binary, append([]string(nil), DefaultArgs...), nil
}

// findBinary returns the first existing entry in paths, resolving bare names
// via PATH and absolute/relative paths via stat.
func findBinary(paths []string) (string, error) {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if filepath.IsAbs(path) || strings.HasPrefix(path, ".") {
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
			continue
		}
		if fullPath, err := exec.LookPath(path); err == nil {
			return fullPath, nil
		}
	}
	return "", fmt.Errorf("%s not found in PATH or default locations", BinaryName)
}
