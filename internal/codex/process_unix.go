//go:build !windows

package codex

import (
	"os/exec"

	"github.com/dimetron/pi-go/internal/procs"
)

// setPlatformAttrs configures Unix process group management so the
// `codex app-server` subprocess and all its children (e.g. shell commands it
// spawns) can be killed together.
//
// This delegates to internal/procs rather than hand-rolling
// kill(-cmd.Process.Pid, SIGKILL), which assumed the group ID equals the
// child's PID and never checked that the PID was still ours to signal. See
// internal/procs/procs_unix.go for why signaling a stale PID is dangerous.
func setPlatformAttrs(cmd *exec.Cmd) {
	procs.Isolate(cmd)
}
