package tools

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/procs"
)

// unreapedProc is a bashProc that has been registered but whose reaping
// goroutine has not run — done is open and exitCode still holds its zero value.
// This is the state killHandle lands in whenever a descendant keeps the
// command's pipes open for as long as the wait delay.
func unreapedProc(t *testing.T, sup *BashSupervisor) *bashProc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	p := &bashProc{
		sup:     sup,
		id:      sup.nextID(),
		command: "sleep forever",
		cmd:     exec.CommandContext(ctx, "true"),
		cancel:  cancel,
		stdout:  newStream(streamCap),
		stderr:  newStream(streamCap),
		started: time.Now(),
		done:    make(chan struct{}),
	}
	sup.mu.Lock()
	sup.procs[p.id] = p
	sup.mu.Unlock()
	return p
}

func TestBashProc_ExitStatusReportsWhetherReaped(t *testing.T) {
	sup := fastSupervisor(t)
	p := unreapedProc(t, sup)

	if code, reaped := p.exitStatus(); reaped {
		t.Errorf("exitStatus() = (%d, true) before reaping, want reaped=false", code)
	}

	p.exitCode = 42
	close(p.done)

	code, reaped := p.exitStatus()
	if !reaped {
		t.Fatal("exitStatus() reports not reaped after done was closed")
	}
	if code != 42 {
		t.Errorf("exitStatus() code = %d, want 42", code)
	}
}

// TestSupervisor_KillUnreapedDoesNotReportCleanExit is the regression guard for
// the bug this fixes: killHandle used to read exitCode unconditionally, so a
// command that had not been reaped within the wait delay was reported as
// "exit 0" — a clean success for a command that was still alive.
func TestSupervisor_KillUnreapedDoesNotReportCleanExit(t *testing.T) {
	sup := fastSupervisor(t)
	p := unreapedProc(t, sup)

	// Keep the wait short; the point is the unreaped branch, not the delay.
	done := make(chan BashStatus, 1)
	go func() {
		st, err := sup.killHandle(p.id)
		if err != nil {
			t.Errorf("killHandle: %v", err)
		}
		done <- st
	}()

	select {
	case st := <-done:
		if st.ExitCode == 0 {
			t.Error("an unreaped command was reported as exit 0")
		}
		if st.ExitCode != -1 {
			t.Errorf("ExitCode = %d, want -1 for an unreaped command", st.ExitCode)
		}
		if !strings.Contains(st.Note, "no exit status is available") {
			t.Errorf("Note = %q, want it to say no exit status is available", st.Note)
		}
	case <-time.After(3 * procs.DefaultWaitDelay):
		t.Fatal("killHandle did not return")
	}
}

func TestSupervisor_KillReapedReportsRealExitCode(t *testing.T) {
	sup := fastSupervisor(t)
	out := run(t, sup, "sleep 30", 120*time.Millisecond)
	if !out.Running {
		t.Fatalf("command was not backgrounded: %+v", out)
	}

	st, err := sup.killHandle(out.Handle)
	if err != nil {
		t.Fatalf("killHandle: %v", err)
	}
	if st.Running {
		t.Error("killed command still reports Running")
	}
	// A killed process reports a signal-derived status, never a clean 0.
	if st.ExitCode == 0 {
		t.Error("killed command reported exit 0")
	}
	if !strings.Contains(st.Note, "Killed") {
		t.Errorf("Note = %q, want it to mention the kill", st.Note)
	}
}

// TestSupervisor_ConcurrentKillAndReadAreRaceFree exercises the two readers of
// a proc's exit state against a live reaping goroutine. It exists to be run
// under -race.
func TestSupervisor_ConcurrentKillAndReadAreRaceFree(t *testing.T) {
	sup := fastSupervisor(t)
	out := run(t, sup, "sleep 30", 100*time.Millisecond)
	if !out.Running {
		t.Fatalf("command was not backgrounded: %+v", out)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = sup.readOutput(out.Handle, 20*time.Millisecond)
	}()
	go func() {
		defer wg.Done()
		_, _ = sup.killHandle(out.Handle)
	}()
	wg.Wait()
}

func TestSupervisor_HandlesListsBackgroundCommandsSorted(t *testing.T) {
	sup := fastSupervisor(t)
	if got := sup.Handles(); len(got) != 0 {
		t.Errorf("fresh supervisor Handles() = %v, want empty", got)
	}

	first := run(t, sup, "sleep 30", 80*time.Millisecond)
	second := run(t, sup, "sleep 30", 80*time.Millisecond)

	got := sup.Handles()
	if len(got) != 2 {
		t.Fatalf("Handles() = %v, want 2 entries", got)
	}
	if got[0] > got[1] {
		t.Errorf("Handles() = %v, want them sorted", got)
	}
	for _, h := range []string{first.Handle, second.Handle} {
		if !contains(got, h) {
			t.Errorf("Handles() = %v, missing %q", got, h)
		}
	}
}

func TestSupervisor_KillUnknownHandleNamesTheLiveOnes(t *testing.T) {
	sup := fastSupervisor(t)
	live := run(t, sup, "sleep 30", 80*time.Millisecond)

	_, err := sup.killHandle("bg_nope")
	if err == nil {
		t.Fatal("killHandle on an unknown handle returned no error")
	}
	if !strings.Contains(err.Error(), live.Handle) {
		t.Errorf("error %q does not name the live handle %q", err, live.Handle)
	}
}

func TestOldestLocked_PrefersFinishedOverLive(t *testing.T) {
	now := time.Now()
	live := &bashProc{id: "live", started: now.Add(-time.Hour), done: make(chan struct{})}

	finishedCh := make(chan struct{})
	close(finishedCh)
	finished := &bashProc{id: "finished", started: now, done: finishedCh}

	// The live one started an hour earlier, but evicting a finished command
	// costs nothing and evicting a live one destroys work.
	got := oldestLocked(map[string]*bashProc{"live": live, "finished": finished})
	if got == nil || got.id != "finished" {
		t.Fatalf("oldestLocked picked %v, want the finished command", got)
	}
}

func TestOldestLocked_FallsBackToOldestLive(t *testing.T) {
	now := time.Now()
	older := &bashProc{id: "older", started: now.Add(-time.Hour), done: make(chan struct{})}
	newer := &bashProc{id: "newer", started: now, done: make(chan struct{})}

	got := oldestLocked(map[string]*bashProc{"newer": newer, "older": older})
	if got == nil || got.id != "older" {
		t.Fatalf("oldestLocked picked %v, want the oldest live command", got)
	}
}

func TestOldestLocked_Empty(t *testing.T) {
	if got := oldestLocked(map[string]*bashProc{}); got != nil {
		t.Errorf("oldestLocked(empty) = %v, want nil", got)
	}
}
