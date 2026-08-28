//go:build !windows

package diagram

import (
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

// getTerminalWidth returns the current terminal width in columns.
// Falls back to $COLUMNS env var, then 80 as a safe default.
func getTerminalWidth() int {
	type winsize struct {
		Row, Col, Xpixel, Ypixel uint16
	}
	var ws winsize
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL,
		os.Stdout.Fd(),
		syscall.TIOCGWINSZ,
		uintptr(unsafe.Pointer(&ws)),
	)
	if err == 0 && ws.Col > 0 {
		return int(ws.Col)
	}
	if cols := os.Getenv("COLUMNS"); cols != "" {
		if n, err := strconv.Atoi(cols); err == nil && n > 0 {
			return n
		}
	}
	return 80
}
