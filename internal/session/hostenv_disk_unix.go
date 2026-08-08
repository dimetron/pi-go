//go:build unix

package session

import "syscall"

// diskStats reports total and available bytes for the filesystem holding path.
//
// Bavail is used rather than Bfree: Bfree counts blocks reserved for root,
// which an ordinary pi process cannot touch, so it overstates what is actually
// writable.
func diskStats(path string) (total, free uint64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	//nolint:gosec,unconvert // Bsize is int64 on some platforms and uint32 on others.
	bsize := uint64(st.Bsize)
	//nolint:gosec,unconvert // Blocks/Bavail likewise vary in width across Unixes.
	return uint64(st.Blocks) * bsize, uint64(st.Bavail) * bsize
}
