//go:build !windows

package webserver

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// The screen reducer's whole job is to make pi's own TUI readable. Every other
// test in screen_test.go feeds it sequences a real terminal *would* emit; this
// one feeds it the ones pi actually emits, by running the real binary on a real
// PTY.
//
// It is opt-in because it needs a built binary and a few seconds of wall clock:
//
//	PI_VOICE_LIVE_PTY=./pi go test ./internal/webserver/ -run TestScreenAgainstRealPi -v
//
// Set PI_VOICE_LIVE_PTY to the pi binary to exercise. What it proves is the two
// things unit tests cannot: that a frame pi draws survives the reducer as
// legible text, and that typing a prompt followed by "\r" is what submits it.
func TestScreenAgainstRealPi(t *testing.T) {
	bin := os.Getenv("PI_VOICE_LIVE_PTY")
	if bin == "" {
		t.Skip("set PI_VOICE_LIVE_PTY=<path to pi> to run the live PTY check")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("PI_VOICE_LIVE_PTY=%q: %v", bin, err)
	}

	cmd := exec.Command(bin)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor", "CLICOLOR_FORCE=1")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 100})
	if err != nil {
		t.Fatalf("starting pi on a pty: %v", err)
	}
	defer func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	screen := newScreenBuf(defaultScreenLines)
	var mu sync.Mutex
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				mu.Lock()
				screen.feed(buf[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	snapshot := func() string {
		mu.Lock()
		defer mu.Unlock()
		return screen.snapshot(60)
	}

	// Wait for the TUI to finish starting. Waiting for a non-empty screen is not
	// enough — pi draws a first frame within milliseconds and then keeps
	// repainting as its deferred initialisation lands, and a prompt typed into
	// one of those frames is wiped by the next. Settling on a stable screen is
	// the same condition pi_wait_for_agent uses in production.
	waitUntil(t, 90*time.Second, func() bool {
		screen := snapshot()
		return strings.TrimSpace(screen) != "" && !strings.Contains(screen, "waiting for response")
	})
	if !settle(t, 3*time.Second, 60*time.Second, snapshot) {
		t.Fatalf("pi never stopped repainting:\n%s", snapshot())
	}
	first := snapshot()
	t.Logf("startup screen:\n%s", first)

	if strings.Contains(first, "\x1b") {
		t.Errorf("escape bytes survived into the snapshot:\n%q", first)
	}
	// A frame that reduced correctly carries no leftovers. Counting letters was
	// tried first and was the wrong measure: pi's chrome is mostly box-drawing,
	// so a perfectly reduced frame scores low. What actually distinguishes a
	// mangled frame is the debris a half-parsed sequence leaves behind — the
	// parameters and final byte of a CSI printed as literal text.
	for _, debris := range []string{"[0m", "[1m", "[2K", "[?25", ";1H", "[39m"} {
		if strings.Contains(first, debris) {
			t.Errorf("escape-sequence debris %q survived into the snapshot:\n%s", debris, first)
		}
	}

	// Typing a prompt and pressing Enter is exactly what SendPrompt does. What
	// this checks is that the two writes land as "text, then submit" rather
	// than as one paste pi leaves sitting in its input box.
	const prompt = "zzuniquephrasezz"
	if _, err := ptmx.Write([]byte(prompt)); err != nil {
		t.Fatalf("typing: %v", err)
	}
	waitUntil(t, 10*time.Second, func() bool { return strings.Contains(snapshot(), prompt) })
	if !strings.Contains(snapshot(), prompt) {
		t.Fatalf("the typed prompt never appeared on screen:\n%s", snapshot())
	}
	typed := snapshot()

	time.Sleep(50 * time.Millisecond)
	if _, err := ptmx.Write([]byte("\r")); err != nil {
		t.Fatalf("submitting: %v", err)
	}

	// After submission the input box is empty again and the prompt has moved
	// into the conversation, so the screen must differ from the typed state.
	changed := waitUntil(t, 15*time.Second, func() bool { return snapshot() != typed })
	t.Logf("screen after submit:\n%s", snapshot())
	if !changed {
		t.Error("the screen did not change after Enter — the prompt was not submitted")
	}
}

// settle waits until snapshot stops changing for quiet, or timeout elapses.
func settle(t *testing.T, quiet, timeout time.Duration, snapshot func() string) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := snapshot()
	stableSince := time.Now()
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if now := snapshot(); now != last {
			last = now
			stableSince = time.Now()
			continue
		}
		if time.Since(stableSince) >= quiet {
			return true
		}
	}
	return false
}

// waitUntil polls cond until it holds or the timeout elapses.
func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cond()
}
