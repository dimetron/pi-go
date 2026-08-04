package cli

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
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

// newTestBar returns a printer with a fixed width and a settled ETA baseline.
func newTestBar(t *testing.T, width int) *mineProgress {
	t.Helper()
	p := newMineProgress(&bytes.Buffer{})
	p.width, p.tty = width, true
	p.stage = "embed"
	p.stageStart = time.Now().Add(-30 * time.Second)
	p.stageBase = 0
	return p
}

// TestLineFitsTerminalWidth is the regression test for the byte-vs-column bug.
// The bar glyphs and spinner are 3 bytes each, so measuring with len() made the
// line look ~45 columns wider than it is. Two symptoms followed: the filename
// field computed as negative and was dropped from every frame, and clipping
// sliced a glyph in half and emitted U+FFFD.
//
// It also matters that the line never exceeds the width: it is redrawn with \r,
// which only returns to the start of the current line, so a wrapped line smears
// down the terminal instead of updating in place.
func TestLineFitsTerminalWidth(t *testing.T) {
	for _, width := range []int{200, 120, 100, 80, 60, 40, 20, 10} {
		p := newTestBar(t, width)
		line := p.lineLocked("embed", "internal/palace/embedder_ollama.go", 3891, 6282, "chunks")

		if got := dispWidth(line); got > width-1 {
			t.Errorf("width=%d: line is %d columns, must be <= %d: %q", width, got, width-1, line)
		}
		if strings.ContainsRune(line, '�') {
			t.Errorf("width=%d: line contains a broken rune: %q", width, line)
		}
	}
}

// TestLineShowsFilenameWhenItFits is the other half of that bug: a line that
// fits the width is not enough if the field a user actually watches is gone.
func TestLineShowsFilenameWhenItFits(t *testing.T) {
	p := newTestBar(t, 100)
	line := p.lineLocked("embed", "internal/palace/embedder_ollama.go", 3891, 6282, "chunks")

	if !strings.Contains(line, "embedder_ollama.go") {
		t.Errorf("filename missing from a 100-column line: %q", line)
	}
	if !strings.Contains(line, "left") {
		t.Errorf("ETA missing from a 100-column line: %q", line)
	}
}

// TestElideLeftKeepsBasename: paths share long prefixes and differ at the end,
// so trimming from the right would leave every line looking identical.
func TestElideLeftKeepsBasename(t *testing.T) {
	const path = "internal/palace/embedder_ollama.go"

	// Wide enough for "..." + the basename (21 columns): the basename survives.
	got := elideLeft(path, 24)
	if !strings.HasSuffix(got, "embedder_ollama.go") {
		t.Errorf("elideLeft dropped the basename: %q", got)
	}

	// Every width must stay within budget and keep the tail, even when the
	// field is too narrow for the whole basename.
	for _, w := range []int{40, 24, 20, 12, 4, 3, 1} {
		got := elideLeft(path, w)
		if dispWidth(got) > w {
			t.Errorf("elideLeft(%d) = %d columns, want <= %d: %q", w, dispWidth(got), w, got)
		}
		if !strings.HasSuffix(path, strings.TrimPrefix(got, "...")) {
			t.Errorf("elideLeft(%d) = %q, which is not a tail of the path", w, got)
		}
	}
}

// TestETAWithholdsEarlyEstimate: an ETA extrapolated from the first couple of
// batches swings wildly and reads as a broken display. Nothing is better.
func TestETAWithholdsEarlyEstimate(t *testing.T) {
	p := newMineProgress(&bytes.Buffer{})
	p.stage, p.stageStart, p.stageBase = "embed", time.Now(), 0

	if got := p.etaLocked(2, 6282); got != "" {
		t.Errorf("ETA should be withheld on the first samples, got %q", got)
	}

	p.stageStart = time.Now().Add(-30 * time.Second)
	if got := p.etaLocked(3000, 6282); got == "" {
		t.Error("ETA should appear once the stage has a settled rate")
	}
	// A finished stage has nothing left to estimate.
	if got := p.etaLocked(6282, 6282); got != "" {
		t.Errorf("ETA should be empty at completion, got %q", got)
	}
}

// TestETAResetsOnStageChange: scan, embed and insert run at very different
// rates, so carrying a rate across the boundary predicts a wrong finish time.
func TestETAResetsOnStageChange(t *testing.T) {
	p := newTestBar(t, 120)
	p.lineLocked("embed", "a.go", 3000, 6282, "chunks")

	p.lineLocked("insert", "a.go", 3000, 6282, "chunks")
	if p.stage != "insert" {
		t.Fatalf("stage = %q, want insert", p.stage)
	}
	if p.stageBase != 3000 {
		t.Errorf("stageBase = %d, want 3000 (baseline must rebase on stage change)", p.stageBase)
	}
	if time.Since(p.stageStart) > time.Second {
		t.Error("stageStart must reset when the stage changes")
	}
}

func TestCompactCount(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{
		{0, "0"}, {999, "999"}, {1000, "1.0k"}, {6282, "6.3k"}, {21074, "21k"},
	} {
		if got := compactCount(tc.in); got != tc.want {
			t.Errorf("compactCount(%d) = %q, want %q", tc.in, got, tc.want)
		}
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
