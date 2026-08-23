// Package notice carries short, user-facing messages raised while pi-go starts
// up or reconfigures itself — a skipped MCP server, a rerouted docs source, a
// blocked skill, an OAuth re-login.
//
// These are not diagnostics, and os.Stderr is the wrong place for them once a
// front end owns the terminal: the TUI paints its frame with direct cursor
// control, so a stray write lands in the middle of the layout and stays there
// until the next full repaint, and the ACP server interleaves them with its
// protocol stream. Routing them through a sink lets the front end render them
// where the user is already looking.
//
// The default sink writes to os.Stderr, which is correct for the
// non-interactive CLI: nothing else owns the terminal, and a warning that
// vanished entirely would be worse than one that scrolls past.
package notice

import (
	"fmt"
	"os"
	"sync"
)

var (
	mu   sync.RWMutex
	sink func(string)
)

// SetSink routes notices to fn instead of os.Stderr, returning the sink it
// replaced so a caller can restore it — tests rely on that, and so does a
// front end that exits before the process does. Passing nil restores the
// os.Stderr default.
//
// fn is called from whichever goroutine raises the notice, including ones deep
// inside a lazily-connecting MCP toolset, so it must be safe for concurrent
// use and must not block: a sink that stalls stalls the agent turn behind it.
func SetSink(fn func(string)) func(string) {
	mu.Lock()
	defer mu.Unlock()
	prev := sink
	sink = fn
	return prev
}

// Notifyf formats a notice and delivers it to the installed sink. The message
// carries no trailing newline — a sink renders it as a single item, and the
// stderr fallback supplies the newline itself.
func Notifyf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	mu.RLock()
	fn := sink
	mu.RUnlock()
	if fn != nil {
		fn(msg)
		return
	}
	fmt.Fprintf(os.Stderr, "pi-go: %s\n", msg)
}
