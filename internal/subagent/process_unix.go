//go:build !windows

package subagent

import (
	"os/exec"
	"syscall"
)

// setPlatformAttrs configures Unix process group management so the
// subagent and all its children can be killed together.
func setPlatformAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
