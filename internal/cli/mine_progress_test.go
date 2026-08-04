package cli

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// TestMineProgressDoIsSerialized is the regression test for the data race: the
// progress callbacks mutate shared counters and are invoked from every embed
// worker. Run with -race; the counter assertion catches lost updates even
// without it.
func TestMineProgressDoIsSerialized(t *testing.T) {
	var buf bytes.Buffer
	p := newMineProgress(&buf)

	const goroutines, perGoroutine = 16, 50
	counter := 0

	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perGoroutine {
				p.do(func() string {
					counter++
					return "bar"
				})
			}
		}()
	}
	wg.Wait()

	if want := goroutines * perGoroutine; counter != want {
		t.Errorf("counter = %d, want %d (updates were lost to a race)", counter, want)
	}
}

// TestMineProgressLogsDoNotCorruptBar covers the display half of the bug. The
// bar leaves the cursor mid-line with no newline, so a log record written
// straight to the same terminal welds itself onto the bar:
//
//	... 1024/6282 chunks  17s2026/08/04 21:05:39 WARN mine: embedding batch failed
//
// captureLogs must erase the line first and redraw after.
func TestMineProgressLogsDoNotCorruptBar(t *testing.T) {
	var buf bytes.Buffer
	p := newMineProgress(&buf)
	// bytes.Buffer is not a terminal, so force the tty path on to exercise the
	// escape sequences this test is about.
	p.tty = true

	var logBuf bytes.Buffer
	restore := func() func() {
		prev := slog.Default()
		slog.SetDefault(slog.New(&progressAwareHandler{
			Handler: slog.NewTextHandler(&logBuf, nil),
			p:       p,
		}))
		return func() { slog.SetDefault(prev) }
	}()
	defer restore()

	p.do(func() string { return "PROGRESS-BAR" })
	slog.Warn("embedding batch failed")

	out := buf.String()

	// The record must be preceded by an erase, and the bar restored after it.
	if !strings.Contains(out, "\r\x1b[K") {
		t.Errorf("expected an erase sequence around the log record, got %q", out)
	}
	if got := strings.Count(out, "PROGRESS-BAR"); got != 2 {
		t.Errorf("bar drawn %d times, want 2 (once initially, once redrawn under the log)", got)
	}
	if !strings.Contains(logBuf.String(), "embedding batch failed") {
		t.Errorf("log record was swallowed: %q", logBuf.String())
	}
}

// TestMineProgressNonTTYWritesNoEscapes keeps redirected output clean: a bar
// redrawn with \r into a pipe or a log file is unreadable noise.
func TestMineProgressNonTTYWritesNoEscapes(t *testing.T) {
	var buf bytes.Buffer
	p := newMineProgress(&buf) // bytes.Buffer is never a terminal

	p.do(func() string { return "bar" })
	if buf.Len() != 0 {
		t.Errorf("non-tty should suppress the live bar, wrote %q", buf.String())
	}

	p.finish("done")
	out := buf.String()
	if strings.ContainsAny(out, "\r\x1b") {
		t.Errorf("non-tty output must contain no escapes, got %q", out)
	}
	if !strings.Contains(out, "done") {
		t.Errorf("final line missing from non-tty output: %q", out)
	}
}

// TestMineProgressFinishTerminatesLine ensures output after the bar starts on
// its own line rather than being appended to it.
func TestMineProgressFinishTerminatesLine(t *testing.T) {
	var buf bytes.Buffer
	p := newMineProgress(&buf)
	p.tty = true

	p.do(func() string { return "bar" })
	p.finish("done")

	if !strings.HasSuffix(buf.String(), "\n") {
		t.Errorf("finish must terminate the live line, got %q", buf.String())
	}
}

// TestProgressAwareHandlerSurvivesWithAttrs guards the easy mistake: embedding
// slog.Handler gives a default WithAttrs that returns the *inner* handler, so
// the first logger.With(...) call would silently drop the interleaving fix.
func TestProgressAwareHandlerSurvivesWithAttrs(t *testing.T) {
	var buf bytes.Buffer
	p := newMineProgress(&buf)

	h := &progressAwareHandler{Handler: slog.NewTextHandler(&bytes.Buffer{}, nil), p: p}

	if _, ok := h.WithAttrs([]slog.Attr{slog.String("k", "v")}).(*progressAwareHandler); !ok {
		t.Error("WithAttrs must return a *progressAwareHandler, not the bare inner handler")
	}
	if _, ok := h.WithGroup("g").(*progressAwareHandler); !ok {
		t.Error("WithGroup must return a *progressAwareHandler, not the bare inner handler")
	}
}
