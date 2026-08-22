//go:build unix

package procs

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestSignalGroup_NilAndUnstartedAreNoOps: terminate runs on Wait's goroutine
// and must never panic or error on a command that never started, or the
// cancellation path itself becomes the failure.
func TestSignalGroup_NilAndUnstartedAreNoOps(t *testing.T) {
	if err := signalGroup(nil, syscall.SIGKILL); err != nil {
		t.Errorf("signalGroup(nil) = %v, want nil", err)
	}

	unstarted := exec.Command("bash", "-c", "true") // never started: Process is nil
	if err := signalGroup(unstarted, syscall.SIGKILL); err != nil {
		t.Errorf("signalGroup(unstarted) = %v, want nil", err)
	}
	if err := Kill(unstarted); err != nil {
		t.Errorf("Kill(unstarted) = %v, want nil", err)
	}
}

// TestTerminate_UnstartedCommandDoesNotEscalate covers the timer callback's
// cmd.Process == nil return at procs_unix.go:52: terminate on a command that
// never started must schedule a SIGKILL that finds no process and no-ops,
// leaving the test runner alive.
func TestTerminate_UnstartedCommandDoesNotEscalate(t *testing.T) {
	cmd := exec.Command("bash", "-c", "true") // never started: Process is nil
	if err := terminate(cmd, 20*time.Millisecond); err != nil {
		t.Fatalf("terminate(unstarted) = %v, want nil", err)
	}
	// Wait past the grace so the AfterFunc timer fires with cmd.Process == nil.
	time.Sleep(50 * time.Millisecond)
	if err := syscall.Kill(syscall.Getpid(), 0); err != nil {
		t.Fatalf("the test process was signaled by a stale timer: %v", err)
	}
}

// TestSignalGroup_WithoutSetpgidSignalsTheChildOnly is the safety property that
// matters most in this file.
//
// The negative-PID form of kill(2) addresses a process group, and pi runs as an
// ordinary user process — so kill(-pid) without a group of our own could name
// the group pi itself is in and take down the session. When Setpgid was not
// applied, the code must fall back to signaling the direct child and nothing
// else.
func TestSignalGroup_WithoutSetpgidSignalsTheChildOnly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", "sleep 30")
	// Deliberately no setGroup: this is the no-process-group path.
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid

	if err := signalGroup(cmd, syscall.SIGKILL); err != nil {
		t.Fatalf("signalGroup: %v", err)
	}
	_ = cmd.Wait()

	if alive(pid) {
		t.Errorf("child %d survived the fallback signal", pid)
	}
	// The test process must still be here — the whole point of the guard.
	if err := syscall.Kill(syscall.Getpid(), 0); err != nil {
		t.Fatalf("the test process was signaled by a group kill: %v", err)
	}
}

// TestTerminate_EscalatesToKill covers the SIGTERM-then-SIGKILL path against a
// process that ignores SIGTERM. Group termination has to be able to insist,
// otherwise a shell trapping TERM would leak exactly as before.
func TestTerminate_EscalatesToKill(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// trap '' TERM makes the shell immune to SIGTERM.
	cmd := CommandContext(ctx, "bash", "-c", "trap '' TERM; sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid

	if err := terminate(cmd, 100*time.Millisecond); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	_ = cmd.Wait()

	if alive(pid) {
		t.Errorf("process %d ignored SIGTERM and was never escalated to SIGKILL", pid)
	}
}

// TestIsolateWithDelay_AppliesTimings checks the explicit-timing entry point
// wires WaitDelay through, since that is the backstop against the original
// indefinite hang.
func TestIsolateWithDelay_AppliesTimings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", "true")
	IsolateWithDelay(cmd, 3*time.Second, time.Second)

	if cmd.WaitDelay != 3*time.Second {
		t.Errorf("WaitDelay = %v, want 3s", cmd.WaitDelay)
	}
	if cmd.Cancel == nil {
		t.Error("Cancel was not installed")
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Error("Setpgid was not applied")
	}
}

// TestIsolate_PreservesExistingSysProcAttr: a caller that set its own
// attributes (credentials, for instance) must keep them.
func TestIsolate_PreservesExistingSysProcAttr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", "true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Foreground: false, Noctty: true}
	Isolate(cmd)

	if !cmd.SysProcAttr.Setpgid {
		t.Error("Setpgid was not applied on top of existing attributes")
	}
	if !cmd.SysProcAttr.Noctty {
		t.Error("Isolate clobbered a caller-set attribute")
	}
}
