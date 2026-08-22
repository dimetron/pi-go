package lsp

import (
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"
)

// readLoop is a goroutine: every one of its exits has to close c.done and fail
// the pending requests, or a caller waits forever. These tests drive it through
// a pipe so each framing decision and each exit path can be observed directly,
// and they are pinned against the pre-refactor loop.

// cogReaderClient wires a bare Client to a pipe and starts its reader. Only the
// fields readLoop touches are populated: cmd and stdin stay nil so a test that
// strays outside the reader fails loudly.
func cogReaderClient(t *testing.T) (*Client, *io.PipeWriter) {
	t.Helper()
	pr, pw := io.Pipe()
	c := &Client{
		stdout:  pr,
		pending: make(map[int]chan *Response),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	t.Cleanup(func() { _ = pw.Close() })
	return c, pw
}

// cogPend registers a pending response channel the way Request does.
func cogPend(c *Client, id int) chan *Response {
	ch := make(chan *Response, 1)
	c.pendMu.Lock()
	c.pending[id] = ch
	c.pendMu.Unlock()
	return ch
}

func cogFrame(body string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

func cogAwaitResponse(t *testing.T, ch chan *Response) *Response {
	t.Helper()
	select {
	case resp := <-ch:
		return resp
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a response")
		return nil
	}
}

func cogAwaitDone(t *testing.T, c *Client) {
	t.Helper()
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
		t.Fatal("readLoop did not close done")
	}
}

// The framing cases. Each writes a stream to the reader and expects the id of
// the single response that survives it; a header set the reader rejects must
// drop its frame without disturbing the frame that follows.
func TestReadLoopFraming(t *testing.T) {
	body1 := `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`

	tests := []struct {
		name   string
		stream string
	}{
		{
			name:   "a plain frame",
			stream: cogFrame(body1),
		},
		{
			name:   "unknown headers are ignored",
			stream: fmt.Sprintf("Content-Type: application/vscode-jsonrpc; charset=utf-8\r\nContent-Length: %d\r\n\r\n%s", len(body1), body1),
		},
		{
			name:   "bare LF line endings are accepted",
			stream: fmt.Sprintf("Content-Length: %d\n\n%s", len(body1), body1),
		},
		{
			name: "a frame with no Content-Length is dropped",
			// An empty header block ends the headers immediately, leaving no
			// length; the reader skips to the next frame.
			stream: "\r\n" + cogFrame(body1),
		},
		{
			name:   "an unparsable Content-Length is ignored and drops the frame",
			stream: "Content-Length: not-a-number\r\n\r\n" + cogFrame(body1),
		},
		{
			name: "a repeated Content-Length keeps the last valid value",
			// The first value is wrong; the second is right, and the frame is
			// read with it.
			stream: fmt.Sprintf("Content-Length: 9999\r\nContent-Length: %d\r\n\r\n%s", len(body1), body1),
		},
		{
			name: "a trailing unparsable Content-Length does not clear the good one",
			stream: fmt.Sprintf("Content-Length: %d\r\nContent-Length: xyz\r\n\r\n%s",
				len(body1), body1),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, pw := cogReaderClient(t)
			ch := cogPend(c, 1)

			go func() { _, _ = io.WriteString(pw, tt.stream) }()

			resp := cogAwaitResponse(t, ch)
			if resp.ID == nil || *resp.ID != 1 {
				t.Fatalf("response ID = %v, want 1", resp.ID)
			}
			if resp.Error != nil {
				t.Errorf("unexpected error: %v", resp.Error)
			}
			if string(resp.Result) != `{"ok":true}` {
				t.Errorf("result = %s, want {\"ok\":true}", resp.Result)
			}
		})
	}
}

// Back-to-back frames on one stream are all delivered, in order.
func TestReadLoopConsecutiveFrames(t *testing.T) {
	c, pw := cogReaderClient(t)
	chans := map[int]chan *Response{}
	for id := 1; id <= 3; id++ {
		chans[id] = cogPend(c, id)
	}

	go func() {
		for id := 1; id <= 3; id++ {
			_, _ = io.WriteString(pw, cogFrame(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%d}`, id, id*10)))
		}
	}()

	for id := 1; id <= 3; id++ {
		resp := cogAwaitResponse(t, chans[id])
		if string(resp.Result) != fmt.Sprintf("%d", id*10) {
			t.Errorf("id %d: result = %s, want %d", id, resp.Result, id*10)
		}
	}
}

// A server notification reaches the handler from inside the reader goroutine.
func TestReadLoopDeliversNotification(t *testing.T) {
	c, pw := cogReaderClient(t)

	type got struct {
		method string
		params string
	}
	seen := make(chan got, 1)
	c.NotificationHandler = func(method string, params json.RawMessage) {
		seen <- got{method: method, params: string(params)}
	}

	body := `{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{"uri":"file:///a.go"}}`
	go func() { _, _ = io.WriteString(pw, cogFrame(body)) }()

	select {
	case g := <-seen:
		if g.method != "textDocument/publishDiagnostics" {
			t.Errorf("method = %q", g.method)
		}
		if g.params != `{"uri":"file:///a.go"}` {
			t.Errorf("params = %s", g.params)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notification never arrived")
	}
}

// Every exit path has to do the same two things: answer the in-flight requests
// with "server exited" and close done.
func TestReadLoopExitPaths(t *testing.T) {
	tests := []struct {
		name string
		// write produces the stream, then ends it. Returning without closing
		// is not an option — the reader only exits on a read failure.
		write func(pw *io.PipeWriter)
	}{
		{
			name: "the stream ends between frames",
			write: func(pw *io.PipeWriter) {
				_ = pw.Close()
			},
		},
		{
			name: "the stream ends mid-header",
			write: func(pw *io.PipeWriter) {
				_, _ = io.WriteString(pw, "Content-Length: 4")
				_ = pw.Close()
			},
		},
		{
			name: "the stream ends before the body arrives",
			write: func(pw *io.PipeWriter) {
				_, _ = io.WriteString(pw, "Content-Length: 40\r\n\r\n")
				_ = pw.Close()
			},
		},
		{
			name: "the body is truncated",
			write: func(pw *io.PipeWriter) {
				_, _ = io.WriteString(pw, "Content-Length: 40\r\n\r\n{\"id\":1}")
				_ = pw.Close()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, pw := cogReaderClient(t)
			ch := cogPend(c, 7)

			go tt.write(pw)

			resp := cogAwaitResponse(t, ch)
			if resp.Error == nil {
				t.Fatalf("got %+v, want a server-exited error", resp)
			}
			if resp.Error.Code != -1 || resp.Error.Message != "server exited" {
				t.Errorf("error = %+v, want code -1 \"server exited\"", resp.Error)
			}
			if resp.ID == nil || *resp.ID != 7 {
				t.Errorf("error response ID = %v, want 7", resp.ID)
			}
			if resp.JSONRPC != "2.0" {
				t.Errorf("error response JSONRPC = %q, want \"2.0\"", resp.JSONRPC)
			}

			cogAwaitDone(t, c)

			c.pendMu.Lock()
			left := len(c.pending)
			c.pendMu.Unlock()
			if left != 0 {
				t.Errorf("%d pending entries left behind", left)
			}
		})
	}
}

// Every waiter is answered, not just the first, and each gets its own ID.
func TestReadLoopFailsAllPendingOnExit(t *testing.T) {
	c, pw := cogReaderClient(t)

	chans := map[int]chan *Response{}
	for id := 1; id <= 5; id++ {
		chans[id] = cogPend(c, id)
	}

	_ = pw.Close()
	cogAwaitDone(t, c)

	for id, ch := range chans {
		resp := cogAwaitResponse(t, ch)
		if resp.ID == nil || *resp.ID != id {
			t.Errorf("waiter %d got ID %v", id, resp.ID)
		}
		if resp.Error == nil || resp.Error.Message != "server exited" {
			t.Errorf("waiter %d got %+v, want the server-exited error", id, resp.Error)
		}
	}
}

// A response nobody is waiting for is discarded rather than blocking the
// reader, and the reader keeps going.
func TestReadLoopUnmatchedResponseDoesNotStall(t *testing.T) {
	c, pw := cogReaderClient(t)
	ch := cogPend(c, 2)

	go func() {
		_, _ = io.WriteString(pw, cogFrame(`{"jsonrpc":"2.0","id":99,"result":1}`))
		_, _ = io.WriteString(pw, cogFrame(`{"jsonrpc":"2.0","id":2,"result":2}`))
	}()

	resp := cogAwaitResponse(t, ch)
	if string(resp.Result) != "2" {
		t.Errorf("result = %s, want 2", resp.Result)
	}
}

// Malformed JSON inside a correctly framed body is dropped without killing the
// reader.
func TestReadLoopSurvivesMalformedBody(t *testing.T) {
	c, pw := cogReaderClient(t)
	ch := cogPend(c, 3)

	go func() {
		_, _ = io.WriteString(pw, cogFrame(`{not json`))
		_, _ = io.WriteString(pw, cogFrame(`{"jsonrpc":"2.0","id":3,"result":"after"}`))
	}()

	resp := cogAwaitResponse(t, ch)
	if string(resp.Result) != `"after"` {
		t.Errorf("result = %s, want \"after\"", resp.Result)
	}
}

// A zero-length body is a valid frame; it decodes to an empty message that is
// neither a response nor a notification, and the reader moves on.
func TestReadLoopZeroLengthBody(t *testing.T) {
	c, pw := cogReaderClient(t)
	ch := cogPend(c, 4)

	go func() {
		_, _ = io.WriteString(pw, "Content-Length: 0\r\n\r\n")
		_, _ = io.WriteString(pw, cogFrame(`{"jsonrpc":"2.0","id":4,"result":"ok"}`))
	}()

	resp := cogAwaitResponse(t, ch)
	if string(resp.Result) != `"ok"` {
		t.Errorf("result = %s, want \"ok\"", resp.Result)
	}
}
