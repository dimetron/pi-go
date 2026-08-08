// Package procs runs child processes so that canceling them actually stops
// them.
//
// The default exec.CommandContext contract is weaker than it looks. On
// cancellation it signals only the direct child, and it waits for the
// stdout/stderr pipes to reach EOF. A shell command like
//
//	find / -name '*.go' | grep foo
//
// forks grandchildren that inherit those pipes. Killing the shell leaves them
// running, reparented to init, still holding the write end — so Wait blocks
// forever and the caller hangs long past its own timeout. That is not
// theoretical: it wedged two pi sessions for over an hour, with the orphaned
// find processes still burning CPU.
//
// Isolate closes both halves of that gap: the child leads its own process
// group so cancellation reaches every descendant, and WaitDelay caps how long
// Wait will wait on pipes that something is still holding open.
package procs

import (
	"context"
	"os/exec"
	"time"
)

// DefaultWaitDelay bounds how long Wait may block after cancellation before
// the runtime force-closes the child's pipes and gives up on them.
//
// It is a backstop, not the main mechanism — group termination should already
// have reaped every writer. It exists for the cases group termination cannot
// reach: a process stopped in the kernel, a descendant that escaped into a new
// session, or a platform without process groups at all.
const DefaultWaitDelay = 5 * time.Second

// DefaultKillGrace is how long a canceled group has to exit on SIGTERM before
// it is sent SIGKILL. Long enough for a shell to run its traps and for a
// compiler to remove its temporary files; short enough that no caller notices.
const DefaultKillGrace = 2 * time.Second

// CommandContext builds an isolated command. Prefer it over calling
// exec.CommandContext and Isolate separately: Isolate installs a Cancel func,
// and os/exec rejects that at Start time on a command built without a context,
// so the two-step form is easy to get wrong in a way that only fails at
// runtime.
func CommandContext(ctx context.Context, name string, arg ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, arg...)
	Isolate(cmd)
	return cmd
}

// Isolate prepares cmd so that canceling its context terminates the whole
// process tree it spawns, and so that Wait cannot block indefinitely
// afterwards.
//
// cmd must have been created by exec.CommandContext. Isolate sets Cancel, and
// os/exec refuses to start a command that has one without a context; use
// CommandContext to avoid the trap entirely.
//
// Call it after building cmd and before starting it. Isolate sets SysProcAttr,
// Cancel and WaitDelay; a caller that overwrites any of those afterwards gets
// the old broken behavior back.
//
// On platforms without process groups it still sets WaitDelay, which alone is
// enough to prevent the indefinite hang, and falls back to killing the direct
// child.
func Isolate(cmd *exec.Cmd) {
	IsolateWithDelay(cmd, DefaultWaitDelay, DefaultKillGrace)
}

// IsolateWithDelay is Isolate with explicit timings. waitDelay bounds the wait
// on lingering pipes; killGrace is how long the group has to honor SIGTERM
// before SIGKILL follows.
func IsolateWithDelay(cmd *exec.Cmd, waitDelay, killGrace time.Duration) {
	setGroup(cmd)
	cmd.WaitDelay = waitDelay
	cmd.Cancel = func() error {
		return terminate(cmd, killGrace)
	}
}

// Kill terminates cmd's process group immediately, without the SIGTERM grace
// period. It is safe to call on a process that has already exited, and safe to
// call more than once.
//
// Use it to stop a process that is not tied to a cancellable context — a
// backgrounded command being killed on request, for instance.
func Kill(cmd *exec.Cmd) error {
	return kill(cmd)
}
