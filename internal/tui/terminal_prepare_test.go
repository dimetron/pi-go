package tui

import (
	"os"
	"strings"
	"testing"
)

// captureStdout redirects os.Stdout through a pipe for the duration of fn and
// returns whatever was written. The pipe is not a terminal, which is itself the
// condition the first test asserts on.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 1024)
		for {
			n, readErr := r.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
			}
			if readErr != nil {
				break
			}
		}
		done <- sb.String()
	}()

	fn()
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// Escapes must never reach a non-terminal stdout: under a pipe, a redirect, or
// a test harness they would be captured as output rather than interpreted.
func TestPrepareTerminal_SilentWhenNotATerminal(t *testing.T) {
	got := captureStdout(t, prepareTerminal)
	if got != "" {
		t.Errorf("wrote %q to a non-terminal stdout, want nothing", got)
	}
}

// The startup sequence must be a full RIS — anything less leaves the first
// frame corrupt in terminals (JCEF-based: IntelliJ and similar) where a
// previous program parked the font-renderer in a non-default state. Pin the
// exact byte so a future "let me just add a soft reset to be polite" edit
// cannot quietly regress this.
func TestPrepareTerminal_EmitsFullRIS(t *testing.T) {
	if terminalResetSequence != "\x1bc" {
		t.Errorf("terminalResetSequence = %q, want %q (full RIS)", terminalResetSequence, "\x1bc")
	}
}

// Calling it twice must be harmless — Run may be re-entered by tests, and a
// restart path calls through the same entry point.
func TestPrepareTerminal_Idempotent(t *testing.T) {
	first := captureStdout(t, prepareTerminal)
	second := captureStdout(t, prepareTerminal)
	if first != second {
		t.Errorf("not idempotent: %q then %q", first, second)
	}
}
