//go:build e2e
// +build e2e

// E2E tests exercising the pi-acp-mock binary and the client event-pump
// contracts. Guarded by the `e2e` build tag so regular `go test ./...` runs
// stay fast; run with `go test -tags=e2e ./internal/acp/client/...`.
package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/acp"
)

// mockACPSession is an in-memory acpSession for exercising pump semantics
// without starting a real subprocess.
type mockACPSession struct {
	events chan acp.Event
	done   chan struct{}
	result acp.RunResult
}

func (s *mockACPSession) Events() <-chan acp.Event { return s.events }
func (s *mockACPSession) Done() <-chan struct{}    { return s.done }
func (s *mockACPSession) Cancel() error            { return nil }
func (s *mockACPSession) Wait() acp.RunResult {
	<-s.done
	return s.result
}

// TestMockACPSessionDeliversEventAndResult confirms a session's Events()
// channel delivers queued events and Wait() returns the recorded result
// after done closes.
func TestMockACPSessionDeliversEventAndResult(t *testing.T) {
	mock := &mockACPSession{
		events: make(chan acp.Event, 1),
		done:   make(chan struct{}),
		result: acp.RunResult{Status: acp.StatusSuccess, Result: "mock", SessionID: "s1"},
	}
	mock.events <- acp.Event{Type: acp.EventTypeMessage, Content: "mock", SessionID: "s1"}
	close(mock.events)
	close(mock.done)

	select {
	case ev := <-mock.Events():
		if ev.Content != "mock" {
			t.Errorf("content = %q, want %q", ev.Content, "mock")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
	if got := mock.Wait(); got.Status != acp.StatusSuccess {
		t.Errorf("status = %q, want %q", got.Status, acp.StatusSuccess)
	}
}

// TestPIACPMockBinaryStarts builds and launches the pi-acp-mock binary,
// sends a single initialize frame, and verifies the startup banner lands
// on stderr. Uses t.TempDir for the binary output so stray artifacts
// don't leak into the repo.
func TestPIACPMockBinaryStarts(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "pi-acp-mock")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/pi-acp-mock")
	build.Dir = filepath.Join("..", "..", "..") // run from repo root
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("cannot build pi-acp-mock: %v\n%s", err, out)
	}

	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(), "PI_MOCK_RESPONSE=hi", "PI_MOCK_DELAY_MS=0")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start mock: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	const initReq = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientInfo":{"name":"test","version":"1.0"}}}` + "\n"
	if _, err := stdin.Write([]byte(initReq)); err != nil {
		t.Fatalf("write init: %v", err)
	}
	_ = stdin.Close()

	buf := make([]byte, 4096)
	deadline := time.Now().Add(2 * time.Second)
	var banner strings.Builder
	for time.Now().Before(deadline) {
		n, err := stderr.Read(buf)
		if n > 0 {
			banner.Write(buf[:n])
			if strings.Contains(banner.String(), "starting") {
				return
			}
		}
		if err != nil {
			break
		}
	}
	t.Errorf("startup banner not seen on stderr; got %q", banner.String())
}
