package client

import (
	"bytes"
	"log/slog"
	"os"
	"testing"
)

// TestRunnerLoggerNeverNil pins the guarantee that makes the TUI safe: a Runner
// with no Logger must still hand the SDK a real logger. The SDK falls back to
// slog.Default() when its logger is unset, and slog.Default() writes to stderr
// — the terminal the TUI draws on.
func TestRunnerLoggerNeverNil(t *testing.T) {
	if got := (Runner{}).logger(); got == nil {
		t.Fatal("Runner{}.logger() = nil; the SDK would fall back to slog.Default() and write to stderr")
	}
}

// TestRunnerLoggerDiscardsByDefault checks the default logger actually swallows
// records rather than merely being non-nil.
func TestRunnerLoggerDiscardsByDefault(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()

	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	(Runner{}).logger().Info("connection closed", "cause", "peer connection closed")

	w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if buf.Len() != 0 {
		t.Errorf("default logger wrote %q to stderr; it must discard", buf.String())
	}
}

// TestRunnerLoggerHonoursExplicit keeps the override working, so a caller that
// wants SDK diagnostics in the session log still gets them.
func TestRunnerLoggerHonoursExplicit(t *testing.T) {
	var buf bytes.Buffer
	want := slog.New(slog.NewTextHandler(&buf, nil))
	if got := (Runner{Logger: want}).logger(); got != want {
		t.Error("an explicit Logger must be used as-is")
	}
	(Runner{Logger: want}).logger().Info("hello")
	if buf.Len() == 0 {
		t.Error("explicit logger received no records")
	}
}
