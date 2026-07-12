//go:build !windows

package codex

import (
	"os/exec"
	"syscall"
)

// setPlatformAttrs configures Unix process group management so the
// `codex app-server` subprocess and all its children (e.g. shell commands it
// spawns) can be killed together.
func setPlatformAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
