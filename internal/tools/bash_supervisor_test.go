package tools

import (
	"context"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// fastSupervisor is tuned so stall handling happens in milliseconds. The
// thresholds are the only thing scaled down; every code path under test is the
// production one.
func fastSupervisor(t *testing.T) *BashSupervisor {
	t.Helper()
	sup := NewBashSupervisor()
	sup.idleTimeout = 150 * time.Millisecond
	sup.heartbeat = 25 * time.Millisecond
	t.Cleanup(sup.KillAll)
	return sup
}

// allowSlowShellStartup widens the idle threshold on Windows for tests whose
// command must NOT be backgrounded. Starting Git-for-Windows bash alone can
// take longer than fastSupervisor's 150ms, so a command was judged idle before
// it had printed anything (seen in CI: Elapsed 154ms, Idle 150ms+). Tests that
// expect a handoff keep the short threshold, so the code path is unchanged.
func allowSlowShellStartup(sup *BashSupervisor) {
	if runtime.GOOS == "windows" {
		sup.idleTimeout = 2 * time.Second
	}
}

// bashWorkDir returns a working directory for a backgrounded shell command.
//
// It deliberately does not use t.TempDir. A killed `bash -c` leaves its
// grandchildren running on Windows -- procs.setGroup is a Unix-only job/process
// group facility, see internal/procs/procs_other.go -- and Windows will not
// remove a directory that a live process has as its working directory. That
// would fail t.TempDir's own cleanup and, with it, tests that otherwise passed.
// Removal here is best effort.
func bashWorkDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pi-bash-cwd-")
	if err != nil {
		t.Fatalf("creating shell working directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func run(t *testing.T, sup *BashSupervisor, command string, timeout time.Duration) BashOutput {
	t.Helper()
	out, err := sup.Run(context.Background(), runRequest{
		dir:     bashWorkDir(t),
		command: command,
		timeout: timeout,
	})
	if err != nil {
		t.Fatalf("Run(%q): %v", command, err)
	}
	return out
}

// TestSupervisor_SilentCommandIsBackgrounded is the case that wedged two
// sessions: a command that produces nothing and never finishes. It must come
// back promptly with a handle, still running.
func TestSupervisor_SilentCommandIsBackgrounded(t *testing.T) {
	sup := fastSupervisor(t)

	out := run(t, sup, "sleep 30", 10*time.Second)

	if !out.Running {
		t.Fatalf("expected the silent command to be backgrounded, got %+v", out)
	}
	if out.Handle == "" {
		t.Fatal("expected a handle")
	}
	if out.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1 while running", out.ExitCode)
	}
	// The note has to be actionable: a model that cannot tell "produced nothing"
	// from "produced something slowly" cannot decide whether to kill it.
	if !strings.Contains(out.Note, "no output at all") {
		t.Errorf("Note should call out the total absence of output, got %q", out.Note)
	}

	st, err := sup.killHandle(out.Handle)
	if err != nil {
		t.Fatalf("killHandle: %v", err)
	}
	if st.Running {
		t.Error("still running after kill")
	}
}

// TestSupervisor_ChattyCommandIsNotBackgrounded is the guard against an overly
// eager idle check. A command that keeps talking is making progress, however
// long it takes, and must be left alone.
func TestSupervisor_ChattyCommandIsNotBackgrounded(t *testing.T) {
	sup := fastSupervisor(t)
	allowSlowShellStartup(sup)

	// Runs for ~500ms, well past the 150ms idle threshold, but never silent
	// for longer than 50ms.
	out := run(t, sup, "for i in $(seq 1 10); do echo tick-$i; sleep 0.05; done", 30*time.Second)

	if out.Running {
		t.Fatalf("a command producing steady output must not be backgrounded: %+v", out)
	}
	if out.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", out.ExitCode)
	}
	if !strings.Contains(out.Stdout, "tick-10") {
		t.Errorf("stdout missing the last line: %q", out.Stdout)
	}
}

// TestSupervisor_BackgroundOutputIsIncremental verifies that reads pick up
// where the previous one stopped, and that the final read carries the exit
// status.
func TestSupervisor_BackgroundOutputIsIncremental(t *testing.T) {
	sup := fastSupervisor(t)

	// Silent long enough to be handed off, then produces output and exits.
	out := run(t, sup, "sleep 0.4; echo first; sleep 0.2; echo second", 10*time.Second)
	if !out.Running {
		t.Fatalf("expected handoff, got %+v", out)
	}

	first := readUntil(t, sup, out.Handle, "first")
	if strings.Contains(first, "second") {
		t.Fatalf("first read raced ahead of the command: %q", first)
	}

	// The second read must not repeat what the first already returned.
	second := readUntil(t, sup, out.Handle, "second")
	if strings.Contains(second, "first") {
		t.Errorf("second read repeated already-delivered output: %q", second)
	}
}

// readUntil polls a handle until want appears, returning the read that carried
// it. It fails the test rather than looping forever.
func readUntil(t *testing.T, sup *BashSupervisor, handle, want string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st, err := sup.readOutput(handle, 200*time.Millisecond)
		if err != nil {
			t.Fatalf("readOutput: %v", err)
		}
		if strings.Contains(st.Stdout, want) {
			return st.Stdout
		}
		if !st.Running {
			t.Fatalf("command exited before producing %q", want)
		}
	}
	t.Fatalf("timed out waiting for %q", want)
	return ""
}

// TestSupervisor_FinalReadReportsExitAndSpendsHandle covers the end of a
// backgrounded command's life: the exit code is delivered once, and the handle
// is then gone so finished commands cannot pile up.
func TestSupervisor_FinalReadReportsExitAndSpendsHandle(t *testing.T) {
	sup := fastSupervisor(t)

	out := run(t, sup, "sleep 0.3; echo done; exit 5", 10*time.Second)
	if !out.Running {
		t.Fatalf("expected handoff, got %+v", out)
	}

	// Reads are incremental, so the output and the exit status may arrive in
	// different reads: whichever read observes the last write delivers the
	// bytes, and the exit code lands on the first read taken after the process
	// is reaped. What must hold is that nothing is lost across the sequence.
	var (
		final     BashStatus
		collected strings.Builder
	)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st, err := sup.readOutput(out.Handle, 500*time.Millisecond)
		if err != nil {
			t.Fatalf("readOutput: %v", err)
		}
		collected.WriteString(st.Stdout)
		if !st.Running {
			final = st
			break
		}
	}
	if final.Handle == "" {
		t.Fatal("command never reported completion")
	}
	if final.ExitCode != 5 {
		t.Errorf("ExitCode = %d, want 5", final.ExitCode)
	}
	if !strings.Contains(collected.String(), "done") {
		t.Errorf("output was lost across the read sequence: %q", collected.String())
	}
	if _, err := sup.readOutput(out.Handle, 0); err == nil {
		t.Error("a spent handle should no longer resolve")
	}
}

// TestSupervisor_ForegroundCancellationKillsCommand covers Esc during a turn:
// a command still in the foreground dies with its whole tree and is not
// silently promoted to the background.
func TestSupervisor_ForegroundCancellationKillsCommand(t *testing.T) {
	sup := fastSupervisor(t)
	sup.idleTimeout = time.Hour // keep it foreground until we cancel

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := sup.Run(ctx, runRequest{dir: bashWorkDir(t), command: "sleep 30 | cat", timeout: time.Hour})
	if err == nil {
		t.Fatal("expected a cancellation error")
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("Run blocked for %s after cancellation", elapsed)
	}
	if got := sup.Handles(); len(got) != 0 {
		t.Errorf("canceled command should not be backgrounded, got handles %v", got)
	}
}

// TestSupervisor_KillAllStopsEverything covers session shutdown. A
// backgrounded command has no other owner, so anything left here is a leak of
// exactly the kind this change removes.
func TestSupervisor_KillAllStopsEverything(t *testing.T) {
	sup := fastSupervisor(t)

	var handles []string
	for range 3 {
		out := run(t, sup, "sleep 30", 10*time.Second)
		if !out.Running {
			t.Fatalf("expected handoff, got %+v", out)
		}
		handles = append(handles, out.Handle)
	}
	if got := len(sup.Handles()); got != 3 {
		t.Fatalf("Handles() = %d, want 3", got)
	}

	sup.KillAll()

	if got := sup.Handles(); len(got) != 0 {
		t.Errorf("Handles() = %v, want empty after KillAll", got)
	}
	for _, h := range handles {
		if _, err := sup.readOutput(h, 0); err == nil {
			t.Errorf("handle %s still resolves after KillAll", h)
		}
	}
}

// TestSupervisor_CapEvictsOldest keeps a confused model from accumulating
// background commands without bound.
func TestSupervisor_CapEvictsOldest(t *testing.T) {
	sup := fastSupervisor(t)
	sup.maxProcs = 2

	first := run(t, sup, "sleep 30", 10*time.Second)
	second := run(t, sup, "sleep 30", 10*time.Second)
	third := run(t, sup, "sleep 30", 10*time.Second)

	handles := sup.Handles()
	if len(handles) != 2 {
		t.Fatalf("Handles() = %v, want 2 entries", handles)
	}
	if _, err := sup.readOutput(first.Handle, 0); err == nil {
		t.Error("oldest handle should have been evicted")
	}
	for _, h := range []string{second.Handle, third.Handle} {
		if _, err := sup.readOutput(h, 0); err != nil {
			t.Errorf("handle %s should have survived eviction: %v", h, err)
		}
	}
}

// TestSupervisor_SinkStreamsOutput covers the live view: the UI has to receive
// output while the command runs, not after it finishes. Without that, a stall
// is indistinguishable from a hang.
func TestSupervisor_SinkStreamsOutput(t *testing.T) {
	sup := fastSupervisor(t)
	allowSlowShellStartup(sup)

	var mu sync.Mutex
	kinds := map[string][]string{}
	sup.SetSink(func(_, kind, content string) {
		mu.Lock()
		kinds[kind] = append(kinds[kind], content)
		mu.Unlock()
	})

	out := run(t, sup, "echo alpha; echo beta >&2; exit 0", 10*time.Second)
	if out.ExitCode != 0 {
		t.Fatalf("ExitCode = %d", out.ExitCode)
	}

	mu.Lock()
	defer mu.Unlock()
	if !contains(kinds["output"], "alpha") {
		t.Errorf("stdout was not streamed: %v", kinds["output"])
	}
	if !contains(kinds["stderr"], "beta") {
		t.Errorf("stderr was not streamed: %v", kinds["stderr"])
	}
	if len(kinds["start"]) != 1 {
		t.Errorf("expected exactly one start event, got %v", kinds["start"])
	}
	if len(kinds["exit"]) != 1 {
		t.Errorf("expected exactly one exit event, got %v", kinds["exit"])
	}
}

// TestSupervisor_SinkReportsStall is the timer hook: silence must be visible
// while it is happening.
func TestSupervisor_SinkReportsStall(t *testing.T) {
	sup := fastSupervisor(t)

	var mu sync.Mutex
	var heartbeats, stalls int
	sup.SetSink(func(_, kind, _ string) {
		mu.Lock()
		switch kind {
		case "heartbeat":
			heartbeats++
		case "stall":
			stalls++
		}
		mu.Unlock()
	})

	out := run(t, sup, "sleep 30", 10*time.Second)
	if !out.Running {
		t.Fatalf("expected handoff, got %+v", out)
	}

	mu.Lock()
	defer mu.Unlock()
	if heartbeats == 0 {
		t.Error("no heartbeat reached the sink; a stall would be invisible")
	}
	if stalls != 1 {
		t.Errorf("stall events = %d, want 1", stalls)
	}
}

// TestSupervisor_UnknownHandleNamesTheLiveOnes: an error the model can recover
// from beats one it can only apologize for.
func TestSupervisor_UnknownHandleNamesTheLiveOnes(t *testing.T) {
	sup := fastSupervisor(t)
	live := run(t, sup, "sleep 30", 10*time.Second)

	_, err := sup.readOutput("bg_nope", 0)
	if err == nil {
		t.Fatal("expected an error for an unknown handle")
	}
	if !strings.Contains(err.Error(), live.Handle) {
		t.Errorf("error should list live handles, got %q", err)
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}
