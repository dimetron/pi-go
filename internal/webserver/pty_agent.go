package webserver

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// This file is the seam that lets something other than the browser drive the pi
// process a PTY bridge owns: the voice relay types prompts into the live coding
// session and reads back what it printed.
//
// It drives the SAME session the user is watching rather than spawning a second
// pi, because the whole point of asking by voice is that the answer lands in
// the conversation already on screen — shared history, shared working tree,
// shared approvals. A second process would have none of that.

// Input a voice tool may send. Keys are named rather than raw bytes so the
// model cannot type an arbitrary escape sequence into the terminal.
var ptyNamedKeys = map[string]string{
	"enter":     "\r",
	"return":    "\r",
	"escape":    "\x1b",
	"esc":       "\x1b",
	"tab":       "\t",
	"backspace": "\x7f",
	"space":     " ",
	"up":        "\x1b[A",
	"down":      "\x1b[B",
	"right":     "\x1b[C",
	"left":      "\x1b[D",
	"ctrl-c":    "\x03",
	"ctrl-d":    "\x04",
	"ctrl-l":    "\x0c",
	"y":         "y",
	"n":         "n",
}

// KeyNames lists the keys SendKey accepts, for the tool description and for the
// error a bad name gets back.
func KeyNames() []string {
	names := make([]string, 0, len(ptyNamedKeys))
	for k := range ptyNamedKeys {
		names = append(names, k)
	}
	// Sorted so the tool schema this feeds is byte-stable across restarts.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}

// maxPromptRunes bounds one voice-dictated prompt. A transcription that runs
// away (a stuck microphone in a noisy room) must not paste a novel into the
// agent's input box.
const maxPromptRunes = 4000

// SendPrompt types text into the pi session and submits it, exactly as if the
// user had typed it in the browser terminal.
//
// Control characters are stripped rather than escaped: the argument comes from
// a speech transcript by way of a language model, and the one thing it must not
// be able to do is smuggle an escape sequence or a stray carriage return that
// submits half a sentence.
func (pb *PtyBridge) SendPrompt(text string) error {
	clean := sanitizePromptText(text)
	if clean == "" {
		return fmt.Errorf("the prompt was empty after removing control characters")
	}
	if !pb.Alive() {
		return fmt.Errorf("the pi session is not running")
	}
	if r := []rune(clean); len(r) > maxPromptRunes {
		clean = string(r[:maxPromptRunes])
	}
	if err := pb.write([]byte(clean)); err != nil {
		return err
	}
	// Submit as its own write. Bubble Tea's input reader batches a single read
	// into one paste-like message, and a trailing "\r" in that same batch is
	// not reliably read as the Enter key that submits it.
	time.Sleep(20 * time.Millisecond)
	return pb.write([]byte("\r"))
}

// SendKey sends one named key. This is what answers a permission prompt or
// interrupts a run — the moments where a voice user needs a keystroke, not a
// sentence.
func (pb *PtyBridge) SendKey(name string) error {
	seq, ok := ptyNamedKeys[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return fmt.Errorf("unknown key %q; known keys are %s", name, strings.Join(KeyNames(), ", "))
	}
	if !pb.Alive() {
		return fmt.Errorf("the pi session is not running")
	}
	return pb.write([]byte(seq))
}

// write pushes bytes to the PTY under the lock that guards ptyFile, which Close
// sets to nil.
func (pb *PtyBridge) write(b []byte) error {
	pb.mu.Lock()
	f := pb.ptyFile
	pb.mu.Unlock()
	if f == nil {
		return fmt.Errorf("the pi session is not running")
	}
	if _, err := f.Write(b); err != nil {
		return fmt.Errorf("writing to the pi session: %w", err)
	}
	return nil
}

// sanitizePromptText reduces a transcript to text safe to type into a TUI:
// newlines and tabs become spaces, every other control character is dropped,
// and runs of whitespace collapse.
func sanitizePromptText(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	space := true // leading whitespace is dropped
	for _, r := range text {
		switch {
		case r == '\n', r == '\r', r == '\t', r == ' ':
			if !space {
				b.WriteByte(' ')
				space = true
			}
		case r < 0x20 || r == 0x7f:
			// dropped
		default:
			b.WriteRune(r)
			space = false
		}
	}
	return strings.TrimSpace(b.String())
}

// Screen returns the last n lines of what the pi session has drawn, as plain
// text.
//
// The capture is filled by the PTY→WebSocket pump, so it holds what the browser
// was shown. With no browser attached nothing drains the PTY at all, and this
// returns the screen as of the last attachment — which is the honest answer,
// since the terminal genuinely has not advanced.
func (pb *PtyBridge) Screen(n int) string {
	pb.screenMu.Lock()
	defer pb.screenMu.Unlock()
	if pb.screen == nil {
		return ""
	}
	return pb.screen.snapshot(n)
}

// LastOutput reports when the PTY last produced bytes. Zero means it has not
// produced any since this bridge started.
func (pb *PtyBridge) LastOutput() time.Time {
	pb.screenMu.Lock()
	defer pb.screenMu.Unlock()
	return pb.lastOutput
}

// captureOutput records one chunk of PTY output for later reading.
func (pb *PtyBridge) captureOutput(b []byte) {
	pb.screenMu.Lock()
	defer pb.screenMu.Unlock()
	if pb.screen == nil {
		pb.screen = newScreenBuf(defaultScreenLines)
	}
	pb.screen.feed(b)
	pb.lastOutput = time.Now()
}

// ptyIdlePoll is how often WaitForIdle rechecks. It is short relative to any
// sensible quiet window, so the wait ends promptly once the agent stops.
const ptyIdlePoll = 100 * time.Millisecond

// WaitForIdle blocks until the pi session has produced no output for quiet, or
// until timeout elapses or ctx is done. It reports whether the session actually
// went quiet.
//
// This is what turns "start the agent and hope" into a conversation: the voice
// model calls it after sending a prompt and only speaks once the agent has
// stopped writing, instead of narrating a half-rendered frame.
//
// ctx is the relay's: a browser that closes mid-wait must not leave this
// polling for the rest of the timeout on behalf of a conversation that has
// already ended.
func (pb *PtyBridge) WaitForIdle(ctx context.Context, quiet, timeout time.Duration) bool {
	if quiet <= 0 {
		quiet = time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(ptyIdlePoll)
	defer ticker.Stop()
	for {
		last := pb.LastOutput()
		if !last.IsZero() && time.Since(last) >= quiet {
			return true
		}
		if !pb.Alive() {
			return true // an exited session is as quiet as it will ever get
		}
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return false
		case <-ticker.C:
		}
	}
}

// Get returns a live bridge for a session id without creating one. The voice
// relay uses it to bind to the terminal the browser already opened, and must
// not conjure a second pi process when that terminal is gone.
func (p *PtyPool) Get(sessionID string) (*PtyBridge, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	b, ok := p.bridges[sessionID]
	if !ok || !b.Alive() {
		return nil, false
	}
	return b, true
}
