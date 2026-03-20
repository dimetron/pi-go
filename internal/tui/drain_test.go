package tui

import (
	"os"
	"testing"
	"time"
)

func TestDrainTerminalResponses_ConsumesPending(t *testing.T) {
	// Create a pipe and write fake terminal responses into it.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	garbage := "\x1b[14;1R\x1b[?2026;2$y"
	if _, err := w.WriteString(garbage); err != nil {
		t.Fatal(err)
	}
	// Close write end so reads see EOF after the data.
	w.Close()

	origStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	start := time.Now()
	drainTerminalResponses()
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("drain took too long: %v", elapsed)
	}
}

func TestDrainTerminalResponses_EmptyStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	// Close write end so drain sees EOF immediately.
	w.Close()

	origStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	start := time.Now()
	drainTerminalResponses()
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("drain took too long on empty stdin: %v", elapsed)
	}
}
