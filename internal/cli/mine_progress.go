package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
)

// mineProgress owns the single live terminal line that `pi memory mine` redraws
// with \r, and is the only thing allowed to write to it.
//
// It exists because two independent writers were fighting over that line. The
// progress callbacks redraw it from the embed worker pool, while slog writes
// warnings from the same goroutines with no idea a bar is on screen — and
// because the bar leaves the cursor mid-line with no trailing newline, a log
// record lands welded onto it:
//
//	[█████░░░]  internal/cli/x.go  1024/6282 chunks  17s2026/08/04 21:05:39 WARN mine: ...
//
// Every write now goes through mu, and log records erase the bar before writing
// and restore it after, so the two share the terminal instead of corrupting it.
//
// The lock is not reentrant: code running inside do() must not log, or it will
// deadlock against the handler installed by captureLogs.
type mineProgress struct {
	mu   sync.Mutex
	out  io.Writer
	tty  bool
	last string // last bar drawn, replayed after a log record interrupts it
}

// newMineProgress returns a printer for out. When out is not a terminal the bar
// is suppressed entirely rather than spraying \r and \x1b[K into a pipe or a
// log file; progress on a non-tty is noise, and the final summary still prints.
func newMineProgress(out io.Writer) *mineProgress {
	return &mineProgress{out: out, tty: isTerminal(out)}
}

// isTerminal reports whether w is a character device.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// do runs fn under the progress lock and draws the line it returns, if any.
//
// The callbacks mutate shared counters (file totals, spinner index, current
// phase) and are invoked from every embed worker, so they need the lock for
// their own sake as much as for the terminal's — this was a data race before,
// not only a display glitch.
func (p *mineProgress) do(fn func() string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if line := fn(); line != "" {
		p.drawLocked(line)
	}
}

// drawLocked redraws the live line. Caller must hold mu.
func (p *mineProgress) drawLocked(line string) {
	p.last = line
	if !p.tty {
		return
	}
	fmt.Fprintf(p.out, "\r%s\x1b[K", line)
}

// finish replaces the live line with a final one and terminates it, ending the
// region the bar owns so ordinary output can follow.
func (p *mineProgress) finish(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.last = ""
	if p.tty {
		fmt.Fprintf(p.out, "\r%s\x1b[K\n", line)
		return
	}
	fmt.Fprintln(p.out, line)
}

// captureLogs routes slog through a handler that clears the progress line
// before each record and redraws it after, and returns a function restoring the
// previous default logger.
//
// Wrapping the global default is deliberate: the writers that corrupt the bar
// are inside internal/palace, which logs through slog.Default() and should not
// have to know a CLI progress bar exists.
func (p *mineProgress) captureLogs() func() {
	prev := slog.Default()
	slog.SetDefault(slog.New(&progressAwareHandler{Handler: prev.Handler(), p: p}))
	return func() { slog.SetDefault(prev) }
}

// progressAwareHandler serializes log records against the progress line.
type progressAwareHandler struct {
	slog.Handler
	p *mineProgress
}

func (h *progressAwareHandler) Handle(ctx context.Context, r slog.Record) error {
	h.p.mu.Lock()
	defer h.p.mu.Unlock()

	// Erase the bar so the record starts at column zero on a clean line.
	if h.p.tty && h.p.last != "" {
		fmt.Fprint(h.p.out, "\r\x1b[K")
	}
	err := h.Handler.Handle(ctx, r)
	// Put the bar back underneath the record just written.
	if h.p.tty && h.p.last != "" {
		fmt.Fprintf(h.p.out, "\r%s\x1b[K", h.p.last)
	}
	return err
}

// WithAttrs and WithGroup must rewrap, or slog silently unwraps back to the
// bare handler the first time a caller adds an attribute and the interleaving
// protection disappears.
func (h *progressAwareHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &progressAwareHandler{Handler: h.Handler.WithAttrs(attrs), p: h.p}
}

func (h *progressAwareHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &progressAwareHandler{Handler: h.Handler.WithGroup(name), p: h.p}
}
