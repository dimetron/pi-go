//go:build windows

package tui

import (
	"errors"
	"os"
	"testing"
	"time"
)

// TestSetNonBlock_ReportsUnsupported pins the contract that makes
// drainTerminalResponses safe on Windows. A console handle cannot be switched
// to non-blocking, so setNonBlock must say so: the drain skips itself only on
// an error, and a stub returning nil sends it into a blocking os.Stdin.Read
// that never returns, hanging pi at startup before anything is printed.
func TestSetNonBlock_ReportsUnsupported(t *testing.T) {
	if err := setNonBlock(os.Stdin); err == nil {
		t.Fatal("setNonBlock returned nil: the drain will proceed to a blocking console read and hang pi at startup")
	} else if !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("setNonBlock error = %v, want errors.ErrUnsupported", err)
	}

	if err := setBlock(os.Stdin); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("setBlock error = %v, want errors.ErrUnsupported", err)
	}
}

// TestDrainTerminalResponses_SkippedOnWindows is the end-to-end guard: whatever
// stdin is, the drain must return promptly rather than block on it. Stdin is
// left as the real handle deliberately, since that is what hung in issue #175.
func TestDrainTerminalResponses_SkippedOnWindows(t *testing.T) {
	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		drainTerminalResponses()
		done <- time.Since(start)
	}()

	select {
	case elapsed := <-done:
		if elapsed > time.Second {
			t.Errorf("drain took %v, want near-instant return", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drainTerminalResponses blocked: regression of the issue #175 startup hang")
	}
}
