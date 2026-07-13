package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

// handshake must send initialize, then follow the response with an initialized
// notification. Skipping the notification leaves codex app-server waiting and
// the first turn hangs.
func TestHandshake_InitializeThenInitializedNotification(t *testing.T) {
	fs := newFakeServer(t)

	errCh := make(chan error, 1)
	go func() { errCh <- fs.client.handshake(context.Background()) }()

	req := fs.readRequest()
	if req.Method != MethodInitialize {
		t.Fatalf("first message = %q, want %q", req.Method, MethodInitialize)
	}
	if req.ID == nil {
		t.Error("initialize must be a request (with an id), not a notification")
	}

	// The client opts out of high-frequency delta notifications; losing that
	// would flood the event stream.
	var params InitializeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("unmarshal initialize params: %v", err)
	}
	if params.ClientInfo.Name != "pi-go" {
		t.Errorf("ClientInfo.Name = %q, want pi-go", params.ClientInfo.Name)
	}
	if len(params.Capabilities.OptOutNotificationMethods) == 0 {
		t.Error("expected OptOutNotificationMethods to be requested")
	}

	fs.respond(*req.ID, InitializeResponse{})

	// Now the initialized notification: no id, and handshake must not return
	// until it has been sent.
	note := fs.readRequest()
	if note.Method != MethodInitialized {
		t.Fatalf("second message = %q, want %q", note.Method, MethodInitialized)
	}
	if note.ID != nil {
		t.Errorf("initialized must be a notification (no id), got id=%v", note.ID)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("handshake: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handshake did not return")
	}
}

// An RPC error from initialize must surface, not be swallowed.
func TestHandshake_InitializeErrorSurfaces(t *testing.T) {
	fs := newFakeServer(t)

	errCh := make(chan error, 1)
	go func() { errCh <- fs.client.handshake(context.Background()) }()

	req := fs.readRequest()
	fs.respondError(*req.ID, -32000, "boom")

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected an error when initialize fails")
		}
		if !strings.Contains(err.Error(), "codex initialize") {
			t.Errorf("error = %v, want it to mention codex initialize", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handshake did not return")
	}
}

// A malformed initialize response must be reported, not treated as success.
func TestHandshake_MalformedResponse(t *testing.T) {
	fs := newFakeServer(t)

	errCh := make(chan error, 1)
	go func() { errCh <- fs.client.handshake(context.Background()) }()

	req := fs.readRequest()
	fs.writeRaw(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":"not-an-object"}`, *req.ID))

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected an error for a malformed initialize response")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handshake did not return")
	}
}

// mustRaw is the "params" escape hatch: it must never panic, and an unmarshalable
// value has to degrade to JSON null rather than corrupt the message.
func TestMustRaw(t *testing.T) {
	got := mustRaw(map[string]any{"a": 1})
	if string(got) != `{"a":1}` {
		t.Errorf("mustRaw(map) = %s, want {\"a\":1}", got)
	}

	// A channel cannot be marshaled.
	if got := mustRaw(make(chan int)); string(got) != "null" {
		t.Errorf("mustRaw(unmarshalable) = %s, want null", got)
	}
	// NaN cannot be marshaled either.
	if got := mustRaw(math.NaN()); string(got) != "null" {
		t.Errorf("mustRaw(NaN) = %s, want null", got)
	}
}

// errIsProcessDone distinguishes "already exited" from a real kill failure, so
// shutting down a codex process that has already died is not reported as an error.
func TestErrIsProcessDone(t *testing.T) {
	if !errIsProcessDone(os.ErrProcessDone) {
		t.Error("errIsProcessDone(os.ErrProcessDone) = false, want true")
	}
	if !errIsProcessDone(wrapped{os.ErrProcessDone}) {
		t.Error("errIsProcessDone must unwrap")
	}
	if errIsProcessDone(errors.New("permission denied")) {
		t.Error("errIsProcessDone(other) = true, want false")
	}
	if errIsProcessDone(nil) {
		t.Error("errIsProcessDone(nil) = true, want false")
	}
}

type wrapped struct{ err error }

func (w wrapped) Error() string { return "wrapped: " + w.err.Error() }
func (w wrapped) Unwrap() error { return w.err }
