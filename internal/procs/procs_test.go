//go:build unix

package procs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestIsolate_KillsGrandchildAndReturnsPromptly reproduces the failure this
// package exists for.
//
// The command backgrounds a child and then waits. The child inherits the
// stdout pipe, so under plain exec.CommandContext two things go wrong at once:
// cancellation signals only the shell, leaving the child running and reparented
// to init, and Wait then blocks forever because that child still holds the pipe
// open. Two pi sessions hung for over an hour this way with orphaned `find`
// processes still burning CPU.
//
// Both halves are asserted here: Wait must return quickly, and the grandchild
// must be gone.
func TestIsolate_KillsGrandchildAndReturnsPromptly(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	// `sleep` inherits stdout; `wait` keeps the shell alive alongside it.
	cmd := CommandContext(ctx, "bash", "-c",
		"sleep 60 & echo $! > "+pidFile+"; wait")

	// A non-*os.File writer is what makes os/exec allocate a pipe and copy
	// through a goroutine — which is the code path that blocks. Handing it an
	// *os.File would let exec pass the fd straight through and the bug would
	// not reproduce.
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	childPID := waitForPIDFile(t, pidFile)

	start := time.Now()
	_ = cmd.Wait()
	elapsed := time.Since(start)

	// Generous bound: the point is "returns", not "returns in exactly N ms".
	// The old behavior did not return at all.
	if elapsed > 10*time.Second {
		t.Fatalf("Wait blocked for %s after cancellation; the pipe is still held open", elapsed)
	}

	if alive(childPID) {
		t.Errorf("grandchild %d survived cancellation", childPID)
	}
}

// TestIsolate_NormalExitIsUnaffected guards the common case: the overwhelming
// majority of commands finish long before anything here fires, and must be
// reported exactly as before.
func TestIsolate_NormalExitIsUnaffected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := CommandContext(ctx, "bash", "-c", "echo hi; exit 3")
	var out bytes.Buffer
	cmd.Stdout = &out

	err := cmd.Run()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %v", err)
	}
	if got := exitErr.ExitCode(); got != 3 {
		t.Errorf("exit code = %d, want 3", got)
	}
	if strings.TrimSpace(out.String()) != "hi" {
		t.Errorf("stdout = %q, want %q", out.String(), "hi")
	}
}

// TestKill_TerminatesGroup covers the explicit-kill path used when a
// backgrounded command is stopped on request rather than by cancellation.
func TestKill_TerminatesGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")

	cmd := CommandContext(context.Background(), "bash", "-c", "sleep 60 & echo $! > "+pidFile+"; wait")
	var out bytes.Buffer
	cmd.Stdout = &out

	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	childPID := waitForPIDFile(t, pidFile)

	if err := Kill(cmd); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	_ = cmd.Wait()

	if alive(childPID) {
		t.Errorf("grandchild %d survived Kill", childPID)
	}
}

// TestKill_AfterExitIsHarmless: killing a reaped process must not error, and
// must not signal whatever inherited its PID.
func TestKill_AfterExitIsHarmless(t *testing.T) {
	cmd := CommandContext(context.Background(), "bash", "-c", "true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if err := Kill(cmd); err != nil {
		t.Errorf("Kill after exit: %v", err)
	}
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child never wrote its pid to %s", path)
	return 0
}

// alive reports whether pid still exists, allowing a moment for the kernel to
// reap it. Signal 0 performs the permission and existence checks without
// delivering anything.
func alive(pid int) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}
