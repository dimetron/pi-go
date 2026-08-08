//go:build linux

package session

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// memoryStats reports total and available physical memory from /proc/meminfo.
//
// MemAvailable is used rather than MemFree because it is the kernel's own
// estimate of what a new allocation could actually get, accounting for
// reclaimable page cache. MemFree alone understates it badly on any machine
// that has been up for a while.
func memoryStats() (total, available uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, value, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		kb, convErr := strconv.ParseUint(fields[0], 10, 64)
		if convErr != nil {
			continue
		}
		switch name {
		case "MemTotal":
			total = kb * 1024
		case "MemAvailable":
			available = kb * 1024
		}
	}
	return total, available
}
