//go:build !windows

package subagent

import (
	"os/exec"

	"github.com/dimetron/pi-go/internal/procs"
)

// setPlatformAttrs configures Unix process group management so the
// subagent and all its children can be killed together.
//
// This delegates to internal/procs rather than hand-rolling
// kill(-cmd.Process.Pid, SIGKILL). The hand-rolled form assumed the group ID
// equals the child's PID, never checked that the PID was still ours to signal,
// and went straight to SIGKILL — so a subagent stopped by a timeout was denied
// the chance to flush its stderr, and the error arrived with no diagnostic text
// attached. procs resolves the group properly, refuses to signal pi's own
// group, and sends SIGTERM before SIGKILL.
func setPlatformAttrs(cmd *exec.Cmd) {
	procs.Isolate(cmd)
}
