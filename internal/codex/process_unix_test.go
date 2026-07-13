//go:build !windows

package codex

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// setPlatformAttrs puts codex in its own process group and installs a Cancel
// hook that kills the whole group. Without it, shell commands codex spawns
// survive cancellation and leak as orphans.
func TestSetPlatformAttrs(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "sleep", "30")
	setPlatformAttrs(cmd)

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("Setpgid not set; codex would share pi's process group and its children would survive")
	}
	if cmd.Cancel == nil {
		t.Fatal("Cancel hook not installed; the process group would never be killed")
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	// The child must lead its own group, not sit in ours.
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("getpgid: %v", err)
	}
	if pgid != cmd.Process.Pid {
		t.Errorf("pgid = %d, want it to equal the child pid %d (its own group)", pgid, cmd.Process.Pid)
	}

	// Cancel kills the group; the process must actually die.
	if err := cmd.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done: // killed, as intended
	case <-time.After(5 * time.Second):
		t.Fatal("process survived Cancel; the group kill did not work")
	}
}
