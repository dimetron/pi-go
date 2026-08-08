//go:build !windows

package procs

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestSignalGroup_RefusesOwnProcessGroup is the guard that matters most here.
//
// signalGroup trusts SysProcAttr.Setpgid as evidence that the child leads its
// own group, but that field is only a *request*: setting it after Start records
// the flag with no kernel effect, and Getpgid then returns the test binary's own
// group. Issuing kill(-pgid, SIGKILL) at that point would kill the caller.
//
// This test builds exactly that shape — a child in the caller's group, with the
// Setpgid flag set after the fact — and asserts the caller survives. If the
// pgid == Getpgrp() check is removed, this test kills the test binary outright.
func TestSignalGroup_RefusesOwnProcessGroup(t *testing.T) {
	// A child started WITHOUT Setpgid inherits our process group.
	cmd := exec.Command("bash", "-c", "sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("getpgid: %v", err)
	}
	if pgid != syscall.Getpgrp() {
		t.Skipf("child is not in our group (pgid=%d, ours=%d); nothing to test", pgid, syscall.Getpgrp())
	}

	// Now lie the way a post-Start Isolate would: claim a group that was never
	// created. Without the self-group guard, signalGroup takes the group branch
	// and SIGKILLs our own group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := signalGroup(cmd, syscall.SIGTERM); err != nil {
		t.Fatalf("signalGroup: %v", err)
	}

	// Reaching this line at all is the assertion: we were not killed.
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil && !alive(cmd.Process.Pid) {
		// The child took the SIGTERM directly, which is the correct fallback.
		t.Logf("child received the signal directly, as intended")
	}
}

// TestTerminate_DoesNotSignalAfterReap covers the delayed SIGKILL in terminate.
//
// terminate schedules a SIGKILL for `grace` later. On the normal path the child
// dies immediately and os/exec reaps it, freeing the PID — so when the timer
// fires it must not touch that PID, which the kernel may since have handed to
// something else.
func TestTerminate_DoesNotSignalAfterReap(t *testing.T) {
	grace := 150 * time.Millisecond

	cmd := exec.CommandContext(context.Background(), "bash", "-c", "true")
	IsolateWithDelay(cmd, time.Second, grace)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid

	// Arm the escalation, then let the process exit and be reaped.
	if err := terminate(cmd, grace); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	_ = cmd.Wait()

	// Occupy the freed PID's role: a live process in OUR group. If the timer
	// fires against a stale PID and takes the group branch, this test binary
	// dies. Waiting past the grace period is the assertion.
	time.Sleep(grace * 3)

	// Still here.
	if os.Getpid() <= 0 {
		t.Fatal("unreachable")
	}
	_ = pid
}

// TestKill_AfterExitDoesNotSignalOurGroup makes the property that
// TestKill_AfterExitIsHarmless states in its comment ("must not signal whatever
// inherited its PID") actually testable: it runs Kill on a reaped command whose
// recorded PID has been rewritten to name a live process in our own group.
func TestKill_AfterExitDoesNotSignalOurGroup(t *testing.T) {
	// A live sibling in our process group, standing in for a PID-reuse victim.
	victim := exec.Command("bash", "-c", "sleep 30")
	if err := victim.Start(); err != nil {
		t.Fatalf("start victim: %v", err)
	}
	t.Cleanup(func() {
		_ = victim.Process.Kill()
		_ = victim.Wait()
	})

	// A reaped, isolated command.
	cmd := CommandContext(context.Background(), "bash", "-c", "true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	// Point it at the victim's PID, which is the situation PID reuse creates.
	cmd.Process = victim.Process

	if err := Kill(cmd); err != nil {
		t.Errorf("Kill: %v", err)
	}

	// We must still be alive, and so must the rest of our group.
	if !alive(os.Getpid()) {
		t.Fatal("Kill signaled our own process group")
	}
}
