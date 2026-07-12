package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeServer is an in-process stand-in for `codex app-server`: it reads JSONL
// requests from the client's stdin and writes JSONL responses/notifications
// back on the client's stdout, so the whole Client and Session can be exercised
// without the codex binary.
type fakeServer struct {
	t       *testing.T
	client  *Client
	toSrv   *bufio.Scanner // what the client wrote
	fromSrv io.WriteCloser // what the server writes back
	stderrW io.WriteCloser
}

// newFakeServer wires a Client to a fake server over two os.Pipe pairs and
// returns both halves. No handshake is performed; use newHandshakenServer for
// a client that has already initialized.
func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()

	stdinR, stdinW, err := os.Pipe() // client → server
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	stdoutR, stdoutW, err := os.Pipe() // server → client
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	stderrR, stderrW, err := os.Pipe() // server stderr → client
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	c := newClient(nil, stdinW, stdoutR, stderrR)
	fs := &fakeServer{
		t:       t,
		client:  c,
		toSrv:   bufio.NewScanner(stdinR),
		fromSrv: stdoutW,
		stderrW: stderrW,
	}
	t.Cleanup(func() {
		_ = c.close()
		_ = stdoutW.Close()
		_ = stderrW.Close()
		_ = stdinR.Close()
	})
	return fs
}

// readRequest blocks until the client sends one message and returns it decoded.
func (f *fakeServer) readRequest() rpcMessage {
	f.t.Helper()
	for f.toSrv.Scan() {
		line := f.toSrv.Bytes()
		if strings.TrimSpace(string(line)) == "" {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			f.t.Fatalf("client wrote non-JSON: %q", line)
		}
		return msg
	}
	f.t.Fatal("client sent nothing")
	return rpcMessage{}
}

// respond replies to the request with the given ID.
func (f *fakeServer) respond(id int, result any) {
	f.t.Helper()
	f.writeLine(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

// respondError replies to the request with the given ID with a JSON-RPC error.
func (f *fakeServer) respondError(id int, code int, message string) {
	f.t.Helper()
	f.writeLine(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
}

// notify sends a server notification.
func (f *fakeServer) notify(method string, params any) {
	f.t.Helper()
	f.writeLine(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (f *fakeServer) writeLine(msg any) {
	f.t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		f.t.Fatalf("marshal: %v", err)
	}
	if _, err := f.fromSrv.Write(append(data, '\n')); err != nil {
		f.t.Fatalf("write: %v", err)
	}
}

func (f *fakeServer) writeRaw(line string) {
	f.t.Helper()
	if _, err := f.fromSrv.Write([]byte(line + "\n")); err != nil {
		f.t.Fatalf("write: %v", err)
	}
}

func (f *fakeServer) writeStderr(line string) {
	f.t.Helper()
	if _, err := f.stderrW.Write([]byte(line + "\n")); err != nil {
		f.t.Fatalf("write stderr: %v", err)
	}
}

// exit closes the server's side of stdout/stderr, which is what the client sees
// when `codex app-server` dies.
func (f *fakeServer) exit() {
	f.t.Helper()
	_ = f.fromSrv.Close()
	_ = f.stderrW.Close()
}

func TestClientRequest_RoutesResponseByID(t *testing.T) {
	fs := newFakeServer(t)

	type reply struct {
		raw json.RawMessage
		err error
	}
	replies := make(chan reply, 1)
	go func() {
		raw, err := fs.client.request(t.Context(), MethodThreadStart, ThreadStartParams{CWD: "/tmp"})
		replies <- reply{raw, err}
	}()

	req := fs.readRequest()
	if req.Method != MethodThreadStart {
		t.Fatalf("method = %q, want %q", req.Method, MethodThreadStart)
	}
	if req.ID == nil {
		t.Fatal("request has no id")
	}
	if req.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", req.JSONRPC)
	}
	var params ThreadStartParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.CWD != "/tmp" {
		t.Errorf("cwd = %q, want /tmp", params.CWD)
	}

	// A notification and an unrelated response must not satisfy the request.
	fs.notify(NotifyTurnStarted, TurnStartedParams{ThreadID: "thr_1"})
	fs.respond(*req.ID+99, map[string]any{"thread": map[string]any{"id": "wrong"}})
	fs.respond(*req.ID, ThreadStartResponse{Thread: Thread{ID: "thr_1"}})

	select {
	case got := <-replies:
		if got.err != nil {
			t.Fatalf("request: %v", got.err)
		}
		var resp ThreadStartResponse
		if err := json.Unmarshal(got.raw, &resp); err != nil {
			t.Fatalf("decode result: %v", err)
		}
		if resp.Thread.ID != "thr_1" {
			t.Errorf("thread id = %q, want thr_1", resp.Thread.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request did not return")
	}
}

func TestClientRequest_ReturnsRPCError(t *testing.T) {
	fs := newFakeServer(t)

	errs := make(chan error, 1)
	go func() {
		_, err := fs.client.request(t.Context(), MethodTurnStart, TurnStartParams{})
		errs <- err
	}()

	req := fs.readRequest()
	fs.respondError(*req.ID, -32000, "not logged in")

	select {
	case err := <-errs:
		if err == nil || !strings.Contains(err.Error(), "not logged in") {
			t.Fatalf("err = %v, want it to carry the RPC error message", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request did not return")
	}
}

func TestClientRequest_FailsWhenServerExits(t *testing.T) {
	fs := newFakeServer(t)

	errs := make(chan error, 1)
	go func() {
		_, err := fs.client.request(t.Context(), MethodTurnStart, TurnStartParams{})
		errs <- err
	}()

	fs.readRequest()
	fs.exit()

	select {
	case err := <-errs:
		if err == nil {
			t.Fatal("expected an error when the app-server exits mid-request")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request did not return after the server exited")
	}
}

func TestClientNotifications_ParsedAndForwarded(t *testing.T) {
	fs := newFakeServer(t)

	fs.notify(NotifyItemCompleted, ItemParams{
		ThreadID: "thr_1",
		TurnID:   "turn_1",
		Item:     Item{Type: ItemAgentMessage, Text: "hi", Phase: PhaseFinalAnswer},
	})

	select {
	case notif := <-fs.client.notifications():
		if notif.Method != NotifyItemCompleted {
			t.Fatalf("method = %q, want %q", notif.Method, NotifyItemCompleted)
		}
		var p ItemParams
		if err := json.Unmarshal(notif.Params, &p); err != nil {
			t.Fatalf("decode params: %v", err)
		}
		if p.Item.Type != ItemAgentMessage || p.Item.Text != "hi" || p.ThreadID != "thr_1" {
			t.Errorf("item = %+v, want an agentMessage 'hi' on thr_1", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no notification delivered")
	}
}

func TestClientReadLoop_RejectsServerRequests(t *testing.T) {
	fs := newFakeServer(t)

	// A server-initiated request (id + method). pi-go exposes no methods, so it
	// must answer rather than leave the app-server hanging.
	fs.writeLine(map[string]any{"jsonrpc": "2.0", "id": 7, "method": "applyPatchApproval", "params": map[string]any{}})

	resp := fs.readRequest()
	if resp.ID == nil || *resp.ID != 7 {
		t.Fatalf("response id = %v, want 7", resp.ID)
	}
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("error = %+v, want a -32601 method-not-found", resp.Error)
	}
}

func TestClientReadLoop_NonJSONStdoutBecomesStderr(t *testing.T) {
	fs := newFakeServer(t)

	fs.writeRaw("panic: codex exploded")

	select {
	case line := <-fs.client.stderrLines():
		if !strings.Contains(line, "codex exploded") {
			t.Errorf("stderr line = %q, want it to carry the unparseable stdout", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unparseable stdout was dropped")
	}
	if !strings.Contains(fs.client.stderrText(), "codex exploded") {
		t.Errorf("stderrText = %q, want it to retain the unparseable stdout", fs.client.stderrText())
	}
}

func TestClientStderr_StreamedAndAccumulated(t *testing.T) {
	fs := newFakeServer(t)

	fs.writeStderr("warning: no auth found")

	select {
	case line := <-fs.client.stderrLines():
		if line != "warning: no auth found" {
			t.Errorf("line = %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no stderr line delivered")
	}

	// Give the accumulator a moment; the write above already reached the buffer
	// before the channel send, so this is not racy.
	if !strings.Contains(fs.client.stderrText(), "no auth found") {
		t.Errorf("stderrText = %q, want the line retained for the final result", fs.client.stderrText())
	}
}

func TestClientClose_IsIdempotentAndRejectsRequests(t *testing.T) {
	fs := newFakeServer(t)

	if err := fs.client.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := fs.client.close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := fs.client.request(context.Background(), MethodTurnStart, nil); err == nil {
		t.Error("expected request on a closed client to fail")
	}
}

func TestResolveCommand(t *testing.T) {
	codexPath := writeFakeBinary(t, "codex")

	t.Run("override wins", func(t *testing.T) {
		t.Setenv(EnvCodexCmd, "/should/not/be/used")
		binary, args, err := resolveCommand([]string{"/bin/echo", "hello"})
		if err != nil {
			t.Fatalf("resolveCommand: %v", err)
		}
		if binary != "/bin/echo" {
			t.Errorf("binary = %q, want /bin/echo", binary)
		}
		if len(args) != 1 || args[0] != "hello" {
			t.Errorf("args = %v, want [hello] verbatim", args)
		}
	})

	t.Run("PI_CODEX_CMD bare binary gets default args", func(t *testing.T) {
		t.Setenv(EnvCodexCmd, "/custom/codex")
		binary, args, err := resolveCommand(nil)
		if err != nil {
			t.Fatalf("resolveCommand: %v", err)
		}
		if binary != "/custom/codex" {
			t.Errorf("binary = %q, want /custom/codex", binary)
		}
		if len(args) != 1 || args[0] != "app-server" {
			t.Errorf("args = %v, want [app-server]", args)
		}
	})

	t.Run("PI_CODEX_CMD with args used verbatim", func(t *testing.T) {
		t.Setenv(EnvCodexCmd, "/custom/codex serve --json")
		binary, args, err := resolveCommand(nil)
		if err != nil {
			t.Fatalf("resolveCommand: %v", err)
		}
		if binary != "/custom/codex" {
			t.Errorf("binary = %q", binary)
		}
		if strings.Join(args, " ") != "serve --json" {
			t.Errorf("args = %v, want [serve --json]", args)
		}
	})

	t.Run("falls back to PATH lookup", func(t *testing.T) {
		t.Setenv(EnvCodexCmd, "")
		t.Setenv("PATH", filepath.Dir(codexPath))
		binary, args, err := resolveCommand(nil)
		if err != nil {
			t.Fatalf("resolveCommand: %v", err)
		}
		if binary != codexPath {
			t.Errorf("binary = %q, want %q", binary, codexPath)
		}
		if len(args) != 1 || args[0] != "app-server" {
			t.Errorf("args = %v, want [app-server]", args)
		}
	})
}

func TestFindBinary_NotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := findBinary([]string{"codex", "/nonexistent/codex"})
	if err == nil {
		t.Fatal("expected an error when codex is not installed")
	}
	if !strings.Contains(err.Error(), "codex not found in PATH or default locations") {
		t.Errorf("err = %q, want the documented not-found message", err)
	}
}

func TestFindBinary_AbsolutePath(t *testing.T) {
	path := writeFakeBinary(t, "codex")
	got, err := findBinary([]string{"/nonexistent/codex", path})
	if err != nil {
		t.Fatalf("findBinary: %v", err)
	}
	if got != path {
		t.Errorf("got = %q, want %q", got, path)
	}
}

// writeFakeBinary creates an executable file named name in a temp dir and
// returns its path.
func writeFakeBinary(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return path
}
