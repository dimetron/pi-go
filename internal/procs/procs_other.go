//go:build !unix

package procs

import (
	"errors"
	"os"
	"os/exec"
	"time"
)

// setGroup is a no-op off Unix. Windows has an equivalent in job objects, but
// wiring one up is a separate piece of work; until then WaitDelay carries the
// load, which is enough to keep Wait from blocking forever even when a
// grandchild survives.
func setGroup(*exec.Cmd) {}

func terminate(cmd *exec.Cmd, _ time.Duration) error {
	return kill(cmd)
}

func kill(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
