package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
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
