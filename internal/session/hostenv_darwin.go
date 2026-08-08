//go:build darwin

package session

import (
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// memoryStats reports total and available physical memory.
//
// "Available" on macOS is not a single number the kernel hands out. The honest
// approximation is free + inactive + speculative pages: inactive pages are
// clean and reclaimable on demand, and counting only genuinely free pages makes
// a healthy machine look like it is seconds from death — a 32GB box in normal
// use routinely reports well under 1GB truly free.
func memoryStats() (total, available uint64) {
	if n, err := unix.SysctlUint64("hw.memsize"); err == nil {
		total = n
	}

	// vm_stat is the only reliable way to get the page breakdown without cgo.
	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return total, 0
	}

	pageSize := uint64(4096)
	var free, inactive, speculative uint64
	for _, line := range strings.Split(string(out), "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.Contains(name, "page size of") {
			// Header line: "Mach Virtual Memory Statistics: (page size of 16384 bytes)"
			continue
		}
		digits := strings.Trim(strings.TrimSpace(value), ".")
		n, convErr := strconv.ParseUint(digits, 10, 64)
		if convErr != nil {
			continue
		}
		switch strings.TrimSpace(name) {
		case "Pages free":
			free = n
		case "Pages inactive":
			inactive = n
		case "Pages speculative":
			speculative = n
		}
	}

	// The header carries the real page size; parse it rather than assuming.
	if i := strings.Index(string(out), "page size of "); i >= 0 {
		rest := string(out)[i+len("page size of "):]
		if fields := strings.Fields(rest); len(fields) > 0 {
			if n, convErr := strconv.ParseUint(fields[0], 10, 64); convErr == nil && n > 0 {
				pageSize = n
			}
		}
	}

	return total, (free + inactive + speculative) * pageSize
}
