//go:build unix

package procs

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// setGroup makes the child the leader of a new process group, so its own PID
// doubles as the group ID. Every process it forks inherits that group unless it
// deliberately leaves, which is what makes a single kill reach the whole tree.
func setGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// terminate asks the group to exit, then insists.
//
// SIGTERM first: a shell gets to run its EXIT trap, a compiler gets to unlink
// its temporary files, and a test binary gets to flush. SIGKILL follows after
// grace for anything that ignored it — including the case that matters most
// here, a descendant blocked in a syscall that will never return on its own.
//
// The SIGKILL is scheduled rather than awaited because Cancel runs on Wait's
// own goroutine: blocking here would delay exactly the return we are trying to
// guarantee.
func terminate(cmd *exec.Cmd, grace time.Duration) error {
	if err := signalGroup(cmd, syscall.SIGTERM); err != nil {
		return err
	}
	time.AfterFunc(grace, func() {
		_ = signalGroup(cmd, syscall.SIGKILL)
	})
	return nil
}

func kill(cmd *exec.Cmd) error {
	return signalGroup(cmd, syscall.SIGKILL)
}

// signalGroup sends sig to the child's process group, falling back to the lone
// child when no group was established.
//
// The negative-PID form of kill(2) addresses a group, and getting the sign
// wrong here is unusually costly: pi runs as a normal user process, so
// kill(-pid) against our own group would take down the session. The Setpgid
// check is what rules that out — without it there is no guarantee the child
// leads a group of its own, and -pid could name the group pi itself is in.
func signalGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return nil
	}

	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid {
		// Resolve the group rather than assuming it equals the PID. They are
		// the same for a group leader, but a child that called setpgid itself
		// has moved on, and signaling a stale group would miss it entirely.
		pgid, err := syscall.Getpgid(pid)
		if err == nil && pgid > 1 {
			if err := syscall.Kill(-pgid, sig); err == nil || errors.Is(err, syscall.ESRCH) {
				return nil
			}
		}
	}

	// No group, or the group signal failed: reach the direct child at least.
	if err := cmd.Process.Signal(sig); err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	return nil
}
