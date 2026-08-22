//go:build windows

package session

import "golang.org/x/sys/windows"

// diskStats reports total and available bytes for the volume holding path.
//
// The "available to caller" figure is used rather than the volume's total free
// space, for the same reason the Unix implementation prefers Bavail over Bfree:
// a disk quota can put free space out of this process's reach, and reporting
// space pi cannot actually use would mislead whoever reads meta.json.
func diskStats(path string) (total, free uint64) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0
	}
	var availableToCaller, totalBytes, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &availableToCaller, &totalBytes, &totalFree); err != nil {
		return 0, 0
	}
	return totalBytes, availableToCaller
}
