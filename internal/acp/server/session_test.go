package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestServe_ContextCancelled(t *testing.T) {
	t.Parallel()

	pr, _ := io.Pipe()
	_, pw := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := Serve(ctx, ServeConfig{
		Agent: &Agent{},
		In:    pr,
		Out:   pw,
	})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestServe_PeerDisconnects(t *testing.T) {
	t.Parallel()

	// Pipe for Out: Serve writes here, we discard.
	_, outPw, outOk := makePipePair()
	defer outOk()

	// Pipe for In: we close the write end to simulate peer disconnect.
	inPr, inPw := io.Pipe()

	// Create a context that won't cancel during the test.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- Serve(ctx, ServeConfig{
			Agent: &Agent{},
			In:    inPr,
			Out:   outPw,
		})
	}()

	// Close the peer's write end to simulate disconnect.
	inPw.Close()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Serve did not return after peer disconnect")
	}
}

func TestServe_WithLogger(t *testing.T) {
	t.Parallel()

	pr, _ := io.Pipe()
	_, pw := io.Pipe()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so Serve returns quickly

	_ = Serve(ctx, ServeConfig{
		Agent:  &Agent{},
		In:     pr,
		Out:    pw,
		Logger: logger,
	})

	if buf.Len() == 0 {
		t.Fatal("logger was not called")
	}
}

// makePipePair returns a connected pair of pipes. The reader-side pipe
// reads from r, writes to w, and closer closes both. The writer-side pipe
// reads from r, writes to w. Caller must call the closer when done.
func makePipePair() (r io.Reader, w io.Writer, closeFn func()) {
	pr, pw := io.Pipe()
	return pr, pw, func() { pw.Close() }
}

// syncBuffer is a bytes.Buffer safe for the concurrent writes the SDK's
// receive goroutine makes while the test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestServeRoutesSDKDiagnosticsToLogger pins that the SDK's transport-level
// diagnostics reach the configured logger. Serve already logs through the
// Agent's logger, but the "failed to parse incoming message" and "connection
// closed" records are emitted by the SDK connection, which falls back to
// slog.Default() — stderr — when no logger is installed on it. That fallback
// is why a client sending malformed input left no trace in the err log.
func TestServeRoutesSDKDiagnosticsToLogger(t *testing.T) {
	t.Parallel()

	var logged syncBuffer
	logger := slog.New(slog.NewTextHandler(&logged, nil))

	inPr, inPw := io.Pipe()
	defer inPw.Close()

	// Out is never written to: a line the SDK cannot parse produces a log
	// record, not a response.
	_, outPw, outClose := makePipePair()
	defer outClose()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- Serve(ctx, ServeConfig{
			Agent:  &Agent{},
			In:     inPr,
			Out:    outPw,
			Logger: logger,
		})
	}()

	// Retry rather than write once: SetLogger runs just after the SDK's
	// receive goroutine starts, so the very first line could still be read
	// before the logger is installed. Each unparseable line is reported
	// independently, so a later attempt is as good as the first.
	const want = "failed to parse incoming message"
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(logged.String(), want) {
		if _, err := io.WriteString(inPw, "not json\n"); err != nil {
			t.Fatalf("write malformed line: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	<-errCh

	if got := logged.String(); !strings.Contains(got, want) {
		t.Errorf("SDK parse diagnostic did not reach the configured logger; got %q", got)
	}
}
