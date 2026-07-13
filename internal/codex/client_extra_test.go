package codex

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// A codex binary that does not exist must fail at NewClient with a clear error,
// not hang or panic waiting on a handshake that will never happen.
func TestNewClient_MissingBinaryFails(t *testing.T) {
	_, err := NewClient(context.Background(), ClientOpts{
		Command: []string{"pi-codex-does-not-exist-xyz", "app-server"},
	})
	if err == nil {
		t.Fatal("expected an error for a missing codex binary")
	}
}

// A binary that exits immediately never completes the handshake; NewClient must
// return rather than block forever on the initialize response.
func TestNewClient_BinaryExitsImmediately(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no /usr/bin/true")
	}
	_, err := NewClient(t.Context(), ClientOpts{Command: []string{"true"}})
	if err == nil {
		t.Fatal("expected an error when the app-server exits before handshaking")
	}
}

// writeMessage must refuse to write after close rather than writing to a closed
// pipe, and must report an unmarshalable payload instead of panicking.
func TestWriteMessage_ErrorPaths(t *testing.T) {
	t.Run("unmarshalable payload", func(t *testing.T) {
		fs := newFakeServer(t)
		err := fs.client.writeMessage(map[string]any{"bad": make(chan int)})
		if err == nil {
			t.Fatal("expected a marshal error")
		}
		if !strings.Contains(err.Error(), "marshal") {
			t.Errorf("error = %v, want it to mention marshal", err)
		}
	})

	t.Run("after close", func(t *testing.T) {
		fs := newFakeServer(t)
		if err := fs.client.close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		err := fs.client.writeMessage(map[string]any{"ok": true})
		if err == nil {
			t.Fatal("expected an error writing to a closed client")
		}
		if !strings.Contains(err.Error(), "closed") {
			t.Errorf("error = %v, want it to mention the client being closed", err)
		}
	})
}
