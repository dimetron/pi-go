package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

const (
	// barCells is the width of the bar itself. Narrow enough to leave room for
	// a filename on an 80-column terminal, which is the case that was wrapping.
	barCells = 20

	// minItemWidth is the smallest filename field worth drawing. Below this the
	// item is dropped rather than elided into meaninglessness.
	minItemWidth = 12

	// etaMinSamples and etaMinElapsed gate the estimate. The first few batches
	// are unrepresentative — model load, cold caches — and an ETA extrapolated
	// from them swings wildly, which reads as a broken display rather than an
	// estimate.
	etaMinSamples = 8
	etaMinElapsed = 2 * time.Second
)

// spinnerFrames is the braille spinner shared by every progress line.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

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
	mu    sync.Mutex
	out   io.Writer
	tty   bool
	width int
	last  string // last bar drawn, replayed after a log record interrupts it

	spinner int

	// Per-stage rate baseline for the ETA. Reset on every stage change: scan,
	// embed and insert run at wildly different rates, so an average taken across
	// a stage boundary predicts a finish time that is wrong in both directions.
	stage      string
	stageStart time.Time
	stageBase  int // units already done when this stage began
}

// newMineProgress returns a printer for out. When out is not a terminal the bar
// is suppressed entirely rather than spraying \r and \x1b[K into a pipe or a
// log file; progress on a non-tty is noise, and the final summary still prints.
func newMineProgress(out io.Writer) *mineProgress {
	return &mineProgress{out: out, tty: isTerminal(out), width: terminalWidth(out)}
}

// terminalWidth reports the usable width of out, defaulting to 80.
//
// Width matters more than cosmetics here: the line is redrawn with \r, and \r
// only returns to the start of the *current* line. A line wider than the
// terminal wraps, so the next redraw erases only its tail and leaves the
// wrapped remainder on screen — the bar smears down the terminal instead of
// updating in place. Every line this file emits is therefore clipped to fit.
func terminalWidth(out io.Writer) int {
	const fallback = 80
	f, ok := out.(*os.File)
	if !ok {
		return fallback
	}
	w, _, err := term.GetSize(int(f.Fd()))
	if err != nil || w <= 0 {
		return fallback
	}
	return w
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

// show renders one progress update: a bar, the item being worked on, and an
// estimated time to finish. It is the only line format `pi memory mine` draws.
//
//	⠹ embed  [██████████░░░░░░░░░░]  52%  3.2k/6.3k chunks  internal/cli/cli.go  ~1m20s left
//
// Everything is budgeted against the terminal width, and the item — the part a
// user actually watches to see progress is real — absorbs whatever is left.
// When even that will not fit, fields drop from the right rather than letting
// the line wrap.
func (p *mineProgress) show(stage, item string, done, total int, unit string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.drawLocked(p.lineLocked(stage, item, done, total, unit))
}

// lineLocked builds the progress line. Caller must hold mu.
func (p *mineProgress) lineLocked(stage, item string, done, total int, unit string) string {
	if stage != p.stage {
		p.stage, p.stageStart, p.stageBase = stage, time.Now(), done
	}
	p.spinner = (p.spinner + 1) % len(spinnerFrames)

	pct := 0
	if total > 0 {
		pct = min(done*100/total, 100)
	}

	// Fixed-width left portion, so the bar does not jitter as counts grow.
	head := fmt.Sprintf("%s %-6s [%s] %3d%%  %s/%s %s",
		spinnerFrames[p.spinner], stage,
		renderBar(pct, barCells),
		pct,
		compactCount(done), compactCount(total), unit)

	tail := ""
	if eta := p.etaLocked(done, total); eta != "" {
		tail = "  " + eta
	}

	// The item gets the remainder. Reserve one column so a full-width line
	// never triggers the terminal's own wrap.
	room := p.width - 1 - dispWidth(head) - dispWidth(tail)
	if room < minItemWidth {
		// No room for the item; drop it, then the ETA, before wrapping.
		if p.width-1 >= dispWidth(head)+dispWidth(tail) {
			return head + tail
		}
		if p.width-1 >= dispWidth(head) {
			return head
		}
		return clipRight(head, p.width-1)
	}
	return head + "  " + padRight(elideLeft(item, room-2), room-2) + tail
}

// etaLocked estimates remaining time from the current stage's observed rate.
// Caller must hold mu. Returns "" until there is enough signal to be useful —
// a confident-looking estimate from two samples is worse than none.
func (p *mineProgress) etaLocked(done, total int) string {
	elapsed := time.Since(p.stageStart)
	progressed := done - p.stageBase
	remaining := total - done

	if progressed < etaMinSamples || remaining <= 0 || elapsed < etaMinElapsed {
		return ""
	}
	rate := float64(progressed) / elapsed.Seconds() // units/sec
	if rate <= 0 {
		return ""
	}
	return "~" + formatETA(time.Duration(float64(remaining)/rate)*time.Second) + " left"
}

// renderBar draws a pct-filled bar of the given cell count.
func renderBar(pct, cells int) string {
	filled := pct * cells / 100
	filled = max(min(filled, cells), 0)
	return strings.Repeat("█", filled) + strings.Repeat("░", cells-filled)
}

// compactCount shortens large counts so the header stays a stable width:
// 6282 -> 6.3k. Mining routinely runs to tens of thousands of chunks.
func compactCount(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 10000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return strconv.Itoa(n/1000) + "k"
	}
}

// formatETA renders a duration as the coarsest useful unit. Sub-second
// precision on an estimate implies a confidence it does not have.
func formatETA(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(int(d.Seconds()), 1))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// Every width calculation below is in display columns, never bytes. The bar
// glyphs (█ ░) and the braille spinner are three bytes each, so len() overstates
// the line by ~45 columns — enough that the filename field was computed as
// negative and silently dropped from every frame, and enough that a byte-sliced
// clip cut a glyph in half and emitted a replacement character.
func dispWidth(s string) int { return ansi.StringWidth(s) }

// elideLeft trims from the left, keeping the tail. Paths share long prefixes
// and differ at the end, so the basename is the informative part.
func elideLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if dispWidth(s) <= w {
		return s
	}
	if w <= 3 {
		return clipLeft(s, w)
	}
	return "..." + clipLeft(s, w-3)
}

// clipLeft keeps the last w columns, on a rune boundary.
func clipLeft(s string, w int) string {
	r := []rune(s)
	for i := range r {
		if dispWidth(string(r[i:])) <= w {
			return string(r[i:])
		}
	}
	return ""
}

// clipRight keeps the first w columns, on a rune boundary.
func clipRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if dispWidth(s) <= w {
		return s
	}
	r := []rune(s)
	for i := len(r); i > 0; i-- {
		if dispWidth(string(r[:i])) <= w {
			return string(r[:i])
		}
	}
	return ""
}

func padRight(s string, w int) string {
	if n := dispWidth(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
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
