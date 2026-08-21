//go:build windows

package tui

import (
	"errors"
	"os"
)

// Windows has no equivalent of syscall.SetNonblock for a console handle: the
// console is read through ReadConsole/ReadFile, which block until the user
// presses Enter, and there is no fcntl(O_NONBLOCK) to switch that off.
//
// So these report failure rather than succeeding silently. That matters
// because drainTerminalResponses treats a setNonBlock error as "cannot drain,
// skip it" and returns early. A stub returning nil instead promised a
// non-blocking stdin it had not delivered, so the drain went on to call
// os.Stdin.Read on a still-blocking console handle. Its 50ms deadline is
// checked only between reads, never during one, so the read parked forever and
// pi hung — at startup with nothing yet printed, because prepareTerminal drains
// before it writes anything. See TestSetNonBlock_ReportsUnsupported.
//
// Skipping the drain on Windows is the right trade: it exists only to swallow
// unix terminal query replies (CPR, DECRQM) that would otherwise be parsed as
// keystrokes, so forgoing it costs at most a few stray characters in the input
// box, against a hard hang.
func setNonBlock(_ *os.File) error { return errors.ErrUnsupported }
func setBlock(_ *os.File) error    { return errors.ErrUnsupported }
