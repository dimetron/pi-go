//go:build windows

package diagram

import (
	"os"
	"strconv"
)

// getTerminalWidth returns the terminal width.
// On Windows, falls back to $COLUMNS env var, then 80.
func getTerminalWidth() int {
	if cols := os.Getenv("COLUMNS"); cols != "" {
		if n, err := strconv.Atoi(cols); err == nil && n > 0 {
			return n
		}
	}
	return 80
}
