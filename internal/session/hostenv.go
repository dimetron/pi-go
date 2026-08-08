package session

import "runtime"

// HostEnv is a snapshot of the machine at session start.
//
// It exists to answer one question after the fact: when a process died with
// nothing but "signal: killed" to show for it, was the machine out of something?
// A SIGKILL from an OOM killer, a timeout, and a manual kill are indistinguishable
// at the point the error is read, and by the time anyone investigates the memory
// pressure that caused it is long gone. Recording the numbers up front turns a
// guess into a check.
//
// Every field is best-effort: a platform that will not report one leaves it zero
// rather than failing session creation over diagnostics.
type HostEnv struct {
	// GOOS/GOARCH and CPU count, for reading the numbers below in proportion.
	OS   string `json:"os,omitempty"`
	Arch string `json:"arch,omitempty"`
	CPUs int    `json:"cpus,omitempty"`

	// TotalMemoryBytes is physical RAM; AvailableMemoryBytes is what was free
	// at session start. The ratio is what matters — "68MB free of 32GB" is the
	// shape of a machine about to start killing things.
	TotalMemoryBytes     uint64 `json:"totalMemoryBytes,omitempty"`
	AvailableMemoryBytes uint64 `json:"availableMemoryBytes,omitempty"`

	// Disk figures are for the session directory's filesystem, which is where
	// transcripts, the palace DB and any worktrees land.
	DiskTotalBytes uint64 `json:"diskTotalBytes,omitempty"`
	DiskFreeBytes  uint64 `json:"diskFreeBytes,omitempty"`
}

// captureHostEnv snapshots the machine. path selects the filesystem to report
// on; pass the session directory.
func captureHostEnv(path string) HostEnv {
	env := HostEnv{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
		CPUs: runtime.NumCPU(),
	}
	env.TotalMemoryBytes, env.AvailableMemoryBytes = memoryStats()
	env.DiskTotalBytes, env.DiskFreeBytes = diskStats(path)
	return env
}
