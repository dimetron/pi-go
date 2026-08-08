package cli

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a bytes.Buffer safe for the heartbeat goroutine to write to
// while the test reads it.
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

func TestFormatETA(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{"sub-second rounds up to 1s, never 0s", 200 * time.Millisecond, "1s"},
		{"seconds", 45 * time.Second, "45s"},
		{"just under a minute", 59 * time.Second, "59s"},
		{"a minute is minutes and padded seconds", 60 * time.Second, "1m00s"},
		{"minutes and seconds", 80 * time.Second, "1m20s"},
		{"just under an hour", 59*time.Minute + 59*time.Second, "59m59s"},
		{"an hour is hours and padded minutes", time.Hour, "1h00m"},
		{"hours and minutes", 2*time.Hour + 5*time.Minute, "2h05m"},
		{"zero still reads as 1s", 0, "1s"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatETA(tc.in); got != tc.want {
				t.Errorf("formatETA(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestClipRight(t *testing.T) {
	tests := []struct {
		name string
		in   string
		w    int
		want string
	}{
		{"fits untouched", "abc", 10, "abc"},
		{"exact fit untouched", "abc", 3, "abc"},
		{"clipped from the right", "abcdef", 3, "abc"},
		{"zero width is empty", "abc", 0, ""},
		{"negative width is empty", "abc", -1, ""},
		{"empty input", "", 5, ""},
		// The bar glyphs are three bytes each, so a byte-based clip would cut a
		// glyph in half and emit a replacement character.
		{"clips on a rune boundary, not a byte one", "███", 2, "██"},
		{"a single wide rune that does not fit yields empty", "日", 1, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := clipRight(tc.in, tc.w)
			if got != tc.want {
				t.Errorf("clipRight(%q, %d) = %q, want %q", tc.in, tc.w, got, tc.want)
			}
			if w := dispWidth(got); tc.w > 0 && w > tc.w {
				t.Errorf("clipRight(%q, %d) = %q with width %d, exceeds max", tc.in, tc.w, got, w)
			}
			if strings.ContainsRune(got, '�') {
				t.Errorf("clipRight split a rune and produced a replacement character: %q", got)
			}
		})
	}
}

func TestClipLeft(t *testing.T) {
	tests := []struct {
		name string
		in   string
		w    int
		want string
	}{
		{"fits untouched", "abc", 10, "abc"},
		{"keeps the tail", "abcdef", 3, "def"},
		{"zero width is empty", "abc", 0, ""},
		{"a single wide rune that does not fit yields empty", "日", 1, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := clipLeft(tc.in, tc.w); got != tc.want {
				t.Errorf("clipLeft(%q, %d) = %q, want %q", tc.in, tc.w, got, tc.want)
			}
		})
	}
}

func TestTerminalWidth_NonFileFallsBackTo80(t *testing.T) {
	if got := terminalWidth(&bytes.Buffer{}); got != 80 {
		t.Errorf("terminalWidth(non-file) = %d, want the 80-column fallback", got)
	}
}

func TestTerminalWidth_NonTTYFileFallsBackTo80(t *testing.T) {
	// A regular file is an *os.File but has no window size, so GetSize errors
	// and the fallback must still apply.
	f, err := os.CreateTemp(t.TempDir(), "width")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer f.Close()

	if got := terminalWidth(f); got != 80 {
		t.Errorf("terminalWidth(regular file) = %d, want the 80-column fallback", got)
	}
}

func TestIsTerminal(t *testing.T) {
	if isTerminal(&bytes.Buffer{}) {
		t.Error("a bytes.Buffer is not a terminal")
	}

	f, err := os.CreateTemp(t.TempDir(), "tty")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Error("a regular file is not a character device")
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()
	if !isTerminal(devNull) {
		t.Errorf("%s is a character device and should report as one", os.DevNull)
	}
}

func TestIsTerminal_ClosedFileReportsFalse(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "closed")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Stat on a closed descriptor errors; that must read as "not a terminal"
	// rather than panicking.
	if isTerminal(f) {
		t.Error("a closed file reported as a terminal")
	}
}

func TestMineProgress_StatusAnnouncesIndeterminateWork(t *testing.T) {
	var buf bytes.Buffer
	p := newMineProgress(&buf)
	p.tty = true // drawLocked only emits to a terminal

	p.status("scan", "walking the tree")

	got := buf.String()
	for _, want := range []string{"scan", "walking the tree"} {
		if !strings.Contains(got, want) {
			t.Errorf("status output %q missing %q", got, want)
		}
	}
	// An indeterminate step has no countable units, so there is no honest bar
	// or percentage to draw — only the step and how long it has run.
	if strings.Contains(got, "%") {
		t.Errorf("indeterminate status showed a percentage: %q", got)
	}
}

func TestMineProgress_NonTTYRecordsTheLineWithoutDrawingIt(t *testing.T) {
	var buf bytes.Buffer
	p := newMineProgress(&buf) // a buffer is never a TTY

	p.status("scan", "walking the tree")

	if buf.Len() != 0 {
		t.Errorf("non-TTY writer received %q, want nothing drawn", buf.String())
	}
	// The line is still retained so a later log record can replay it.
	if !strings.Contains(p.last, "walking the tree") {
		t.Errorf("retained line = %q, want it to name the step", p.last)
	}
}

func TestMineProgress_HeartbeatIsANoOpWithoutATTY(t *testing.T) {
	var buf bytes.Buffer
	p := newMineProgress(&buf) // a buffer is never a TTY

	p.startHeartbeat()
	if p.beat != nil {
		t.Error("startHeartbeat armed a ticker for a non-TTY writer")
	}

	// stopHeartbeat must tolerate never having been started.
	p.stopHeartbeat()
}

func TestMineProgress_StopHeartbeatIsIdempotent(t *testing.T) {
	var buf bytes.Buffer
	p := newMineProgress(&buf)
	p.tty = true // force the animated path without needing a real terminal

	p.startHeartbeat()
	if p.beat == nil {
		t.Fatal("startHeartbeat did not arm a ticker on a TTY")
	}

	p.stopHeartbeat()
	if p.beat != nil {
		t.Error("stopHeartbeat left the channel armed")
	}
	p.stopHeartbeat() // must not panic or block on a second call
}

func TestMineProgress_HeartbeatRedrawsActiveLine(t *testing.T) {
	var buf syncBuffer
	p := newMineProgress(&buf)
	p.tty = true

	p.show("embed", "internal/cli/cli.go", 10, 100, "chunks")
	p.startHeartbeat()
	t.Cleanup(p.stopHeartbeat)

	deadline := time.After(5 * time.Second)
	for {
		if strings.Count(buf.String(), "internal/cli/cli.go") >= 2 {
			return // redrawn at least once by the heartbeat
		}
		select {
		case <-deadline:
			t.Fatalf("heartbeat never redrew the line; output: %q", buf.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestProgressAwareHandler_WithGroup(t *testing.T) {
	var buf bytes.Buffer
	p := newMineProgress(&buf)
	base := slog.NewTextHandler(&buf, nil)
	h := &progressAwareHandler{Handler: base, p: p}

	// An empty group name is a documented no-op in slog and must not rewrap.
	if got := h.WithGroup(""); got != slog.Handler(h) {
		t.Error("WithGroup(\"\") should return the same handler")
	}

	grouped := h.WithGroup("mine")
	if _, ok := grouped.(*progressAwareHandler); !ok {
		// Unwrapping here would lose the bar-interleaving protection.
		t.Fatalf("WithGroup returned %T, want it to stay wrapped", grouped)
	}
}
