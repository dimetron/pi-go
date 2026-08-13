package tools

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/tool"
)

// TestBashControlTools_Registered checks the two control tools exist under the
// names the bash tool's own description tells the model to call. A mismatch
// here is invisible until a model follows the instruction and gets "no such
// tool".
func TestBashControlTools_Registered(t *testing.T) {
	sup := testSupervisor(t)

	ts, err := BashControlTools(sup)
	if err != nil {
		t.Fatalf("BashControlTools: %v", err)
	}
	if len(ts) != 2 {
		t.Fatalf("got %d tools, want 2", len(ts))
	}

	names := map[string]bool{}
	for _, tool := range ts {
		names[tool.Name()] = true
	}
	for _, want := range []string{"bash_wait", "bash_kill"} {
		if !names[want] {
			t.Errorf("missing tool %q; got %v", want, names)
		}
		if !strings.Contains(bashDescription, want) {
			t.Errorf("bash description should point the model at %q", want)
		}
	}
}

// runNamedTool invokes one of a tool set by name through the same Run path the
// ADK runner uses. Driving the tools this way — rather than calling the
// supervisor directly — is what covers argument decoding, which is where a
// schema mistake would actually bite.
func runNamedTool(t *testing.T, ts []tool.Tool, name string, args map[string]any) (map[string]any, error) {
	t.Helper()
	for _, candidate := range ts {
		if candidate.Name() != name {
			continue
		}
		r, ok := candidate.(runnableTool)
		if !ok {
			t.Fatalf("tool %q is not runnable", name)
		}
		return r.Run(mockToolCtx{Context: context.Background()}, args)
	}
	t.Fatalf("tool %q not found", name)
	return nil, nil
}

// TestBashControlTools_RoundTrip drives a backgrounded command through the two
// tools the way a model would: read, then kill.
func TestBashControlTools_DefaultWaitsForOutput(t *testing.T) {
	sup := fastSupervisor(t)
	sb := testSandbox(t, t.TempDir())
	ts, err := BashControlTools(sup)
	if err != nil {
		t.Fatalf("BashControlTools: %v", err)
	}

	out, err := bashHandler(sb, sup, nil, BashInput{Command: "sleep 0.2; echo default-wait; sleep 30"})
	if err != nil {
		t.Fatalf("bashHandler: %v", err)
	}
	if !out.Running || out.Handle == "" {
		t.Fatalf("expected a backgrounded command, got %+v", out)
	}

	// An omitted wait_ms must use the same blocking-by-default behavior exposed
	// by bash_wait, rather than immediately returning an empty poll result.
	res, err := runNamedTool(t, ts, "bash_wait", map[string]any{"handle": out.Handle})
	if err != nil {
		t.Fatalf("bash_wait: %v", err)
	}
	if !strings.Contains(fmtAny(res["stdout"]), "default-wait") {
		t.Fatalf("bash_wait default wait did not deliver the line: %v", res)
	}
	if running, _ := res["running"].(bool); !running {
		t.Errorf("command should still be running, got %v", res)
	}

	if _, err := runNamedTool(t, ts, "bash_kill", map[string]any{"handle": out.Handle}); err != nil {
		t.Fatalf("bash_kill: %v", err)
	}
}

func TestBashControlTools_RoundTrip(t *testing.T) {
	sup := fastSupervisor(t)
	sb := testSandbox(t, t.TempDir())
	ts, err := BashControlTools(sup)
	if err != nil {
		t.Fatalf("BashControlTools: %v", err)
	}

	out, err := bashHandler(sb, sup, nil, BashInput{Command: "sleep 0.3; echo hello; sleep 30"})
	if err != nil {
		t.Fatalf("bashHandler: %v", err)
	}
	if !out.Running || out.Handle == "" {
		t.Fatalf("expected a backgrounded command, got %+v", out)
	}

	// wait_ms means one call, not a polling loop — the whole reason it exists.
	res, err := runNamedTool(t, ts, "bash_wait", map[string]any{
		"handle":  out.Handle,
		"wait_ms": 3000,
	})
	if err != nil {
		t.Fatalf("bash_wait: %v", err)
	}
	if !strings.Contains(fmtAny(res["stdout"]), "hello") {
		t.Fatalf("bash_wait did not deliver the line: %v", res)
	}
	if running, _ := res["running"].(bool); !running {
		t.Errorf("command should still be running, got %v", res)
	}

	res, err = runNamedTool(t, ts, "bash_kill", map[string]any{"handle": out.Handle})
	if err != nil {
		t.Fatalf("bash_kill: %v", err)
	}
	if running, _ := res["running"].(bool); running {
		t.Error("command still running after bash_kill")
	}
	if fmtAny(res["command"]) == "" {
		t.Error("status should echo the command so the model knows what it stopped")
	}

	// A spent handle must not resolve again.
	if _, err := runNamedTool(t, ts, "bash_wait", map[string]any{"handle": out.Handle}); err == nil {
		t.Error("expected an error reading a killed handle")
	}
}

// TestBashTool_RunEndToEnd invokes the bash tool the way the ADK runner does,
// covering the factory closure and the argument decoding around it.
func TestBashTool_RunEndToEnd(t *testing.T) {
	sb := testSandbox(t, t.TempDir())
	bt, err := newBashTool(sb, testSupervisor(t))
	if err != nil {
		t.Fatalf("newBashTool: %v", err)
	}

	res, err := runNamedTool(t, []tool.Tool{bt}, "bash", map[string]any{
		"command": "echo through-the-runner",
	})
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	if !strings.Contains(fmtAny(res["stdout"]), "through-the-runner") {
		t.Errorf("stdout = %v", res)
	}
}

// TestCoreTools_UsesSuppliedSupervisor confirms the option actually reaches the
// bash tool. Without it the CLI would attach its sink to a supervisor nothing
// runs through, and streaming would silently never appear.
func TestCoreTools_UsesSuppliedSupervisor(t *testing.T) {
	sb := testSandbox(t, t.TempDir())
	sup := testSupervisor(t)

	var started int
	sup.SetSink(func(_, kind, _ string) {
		if kind == "start" {
			started++
		}
	})

	ts, err := CoreTools(sb, WithBashSupervisor(sup))
	if err != nil {
		t.Fatalf("CoreTools: %v", err)
	}

	if _, err := runNamedTool(t, ts, "bash", map[string]any{"command": "echo wired"}); err != nil {
		t.Fatalf("bash: %v", err)
	}
	if started != 1 {
		t.Errorf("supplied supervisor saw %d starts, want 1 — the option did not reach the tool", started)
	}
}

// TestBashControlTools_RejectMissingHandle covers the argument guard in each
// tool closure.
func TestBashControlTools_RejectMissingHandle(t *testing.T) {
	sup := fastSupervisor(t)
	ts, err := BashControlTools(sup)
	if err != nil {
		t.Fatalf("BashControlTools: %v", err)
	}

	for _, name := range []string{"bash_wait", "bash_kill"} {
		if _, err := runNamedTool(t, ts, name, map[string]any{}); err == nil {
			t.Errorf("%s accepted a call with no handle", name)
		}
	}
}

func fmtAny(v any) string {
	s, _ := v.(string)
	return s
}

// TestBashControlTools_EmptyHandleNamesLiveOnes: the model sometimes calls
// these with no handle at all. The error has to be recoverable — it must say
// what is actually running.
func TestBashControlTools_EmptyHandleNamesLiveOnes(t *testing.T) {
	sup := fastSupervisor(t)
	live := run(t, sup, "sleep 30", 10*time.Second)

	if _, err := sup.readOutput("", 0); err == nil || !strings.Contains(err.Error(), live.Handle) {
		t.Errorf("readOutput with no handle should name live handles, got %v", err)
	}
	if _, err := sup.killHandle("nope"); err == nil || !strings.Contains(err.Error(), live.Handle) {
		t.Errorf("killHandle with a bad handle should name live handles, got %v", err)
	}
}

// TestBashHandler_EmptyCommandRejected covers the guard that keeps an empty
// tool call from spawning a shell that does nothing.
func TestBashHandler_EmptyCommandRejected(t *testing.T) {
	sb := testSandbox(t, t.TempDir())
	if _, err := bashHandler(sb, testSupervisor(t), nil, BashInput{}); err == nil {
		t.Error("expected an error for an empty command")
	}
}

// TestBashHandler_IdleTimeoutIsHonored proves the per-call knob works: a
// command well inside the default idle window is backgrounded early when the
// caller asks for it.
func TestBashHandler_IdleTimeoutIsHonored(t *testing.T) {
	sb := testSandbox(t, t.TempDir())
	sup := testSupervisor(t)
	sup.heartbeat = 20 * time.Millisecond

	out, err := bashHandler(sb, sup, nil, BashInput{
		Command:     "sleep 30",
		IdleTimeout: 150, // ms — far below the 90s default
	})
	if err != nil {
		t.Fatalf("bashHandler: %v", err)
	}
	if !out.Running {
		t.Fatalf("idle_timeout was ignored, got %+v", out)
	}
	if _, err := sup.killHandle(out.Handle); err != nil {
		t.Fatalf("killHandle: %v", err)
	}
}

// TestSupervisor_StartFailureIsReported covers the path where the shell never
// runs at all — a working directory that does not exist. It must surface as an
// error, not as a silent empty success.
func TestSupervisor_StartFailureIsReported(t *testing.T) {
	sup := testSupervisor(t)

	_, err := sup.Run(t.Context(), runRequest{
		dir:     "/definitely/not/a/directory",
		command: "echo hi",
		timeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatal("expected an error when the working directory does not exist")
	}
}

// TestSupervisor_IdleTimeoutClampedToTimeout: a caller asking for a 10s budget
// does not want a 90s idle check silently overriding it.
func TestSupervisor_IdleTimeoutClampedToTimeout(t *testing.T) {
	sup := testSupervisor(t)
	sup.heartbeat = 20 * time.Millisecond

	start := time.Now()
	out, err := sup.Run(t.Context(), runRequest{
		dir:         t.TempDir(),
		command:     "sleep 30",
		timeout:     200 * time.Millisecond,
		idleTimeout: time.Hour, // longer than the timeout; must be clamped
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Run took %s; the idle timeout was not clamped to the timeout", elapsed)
	}
	if !out.Running {
		t.Errorf("expected handoff, got %+v", out)
	}
	if _, err := sup.killHandle(out.Handle); err != nil {
		t.Fatalf("killHandle: %v", err)
	}
}

// TestSupervisor_LifetimeLimitKillsBackgroundCommand covers the last-resort
// reaper. Without it, a forgotten background command is precisely the leak this
// change set out to remove.
func TestSupervisor_LifetimeLimitKillsBackgroundCommand(t *testing.T) {
	sup := fastSupervisor(t)
	sup.maxLifetime = 200 * time.Millisecond

	out := run(t, sup, "sleep 30", 10*time.Second)
	if !out.Running {
		t.Fatalf("expected handoff, got %+v", out)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st, err := sup.readOutput(out.Handle, 100*time.Millisecond)
		if err != nil {
			t.Fatalf("readOutput: %v", err)
		}
		if !st.Running {
			return // reaped by the lifetime limit, as intended
		}
	}
	t.Fatal("background command outlived its lifetime limit")
}

// TestSupervisor_EvictionPrefersFinishedCommands: evicting a finished command
// costs nothing, evicting a live one destroys work. The cap must take the free
// one first even when a live command is older.
func TestSupervisor_EvictionPrefersFinishedCommands(t *testing.T) {
	sup := fastSupervisor(t)
	sup.maxProcs = 2

	// Oldest, and still running.
	live := run(t, sup, "sleep 30", 10*time.Second)
	// Younger, but exits almost immediately after being handed off.
	shortLived := run(t, sup, "sleep 0.2", 10*time.Second)

	waitUntilExited(t, sup, shortLived.Handle)

	// A third command forces an eviction.
	third := run(t, sup, "sleep 30", 10*time.Second)

	if _, err := sup.readOutput(live.Handle, 0); err != nil {
		t.Errorf("the live command was evicted in preference to a finished one: %v", err)
	}
	if _, err := sup.readOutput(third.Handle, 0); err != nil {
		t.Errorf("the new command should be registered: %v", err)
	}
}

// waitUntilExited blocks until the handle's command is no longer running,
// without consuming the handle.
func waitUntilExited(t *testing.T, sup *BashSupervisor, handle string) {
	t.Helper()
	p, ok := sup.lookup(handle)
	if !ok {
		t.Fatalf("unknown handle %q", handle)
	}
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		t.Fatalf("command %q never exited", handle)
	}
}

// TestSinkWriter_FlushesLinelessOutput covers the guard against a command that
// never emits a newline — `printf` with no trailing newline, a progress bar
// using carriage returns. Without the flush the UI would show nothing at all.
func TestSinkWriter_FlushesLinelessOutput(t *testing.T) {
	sup := NewBashSupervisor()
	t.Cleanup(sup.KillAll)

	var got []string
	sup.SetSink(func(_, _, content string) { got = append(got, content) })

	p := &bashProc{sup: sup, id: "bg_test", stdout: newStream(1 << 20)}
	w := &sinkWriter{proc: p, stream: p.stdout, kind: "output"}

	w.Write([]byte(strings.Repeat("x", 9000))) // no newline anywhere

	if len(got) == 0 {
		t.Fatal("a command that never emits a newline produced no events at all")
	}
	if len(got[0]) < 8192 {
		t.Errorf("flushed chunk was %d bytes, want the full buffered run", len(got[0]))
	}
}

func TestExitCodeOf(t *testing.T) {
	if got := exitCodeOf(nil); got != 0 {
		t.Errorf("exitCodeOf(nil) = %d, want 0", got)
	}
	// A non-ExitError failure (the process never ran) has no exit status.
	if got := exitCodeOf(errors.New("fork/exec: no such file")); got != -1 {
		t.Errorf("exitCodeOf(plain error) = %d, want -1", got)
	}

	cmd := exec.Command("bash", "-c", "exit 9")
	err := cmd.Run()
	if got := exitCodeOf(err); got != 9 {
		t.Errorf("exitCodeOf(ExitError) = %d, want 9", got)
	}
}

func TestRoundDur(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{1500 * time.Microsecond, "2ms"},
		{2500 * time.Millisecond, "2.5s"},
		{90*time.Second + 4*time.Millisecond, "1m30s"},
	}
	for _, tt := range tests {
		if got := roundDur(tt.in).String(); got != tt.want {
			t.Errorf("roundDur(%v) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func TestClampDuration(t *testing.T) {
	const fallback = 7 * time.Second
	const maxDur = 10 * time.Second

	if got := clampDuration(0, fallback, maxDur); got != fallback {
		t.Errorf("unset input = %v, want the fallback %v", got, fallback)
	}
	if got := clampDuration(-5, fallback, maxDur); got != fallback {
		t.Errorf("negative input = %v, want the fallback %v", got, fallback)
	}
	if got := clampDuration(2000, fallback, maxDur); got != 2*time.Second {
		t.Errorf("2000ms = %v, want 2s", got)
	}
	if got := clampDuration(999999, fallback, maxDur); got != maxDur {
		t.Errorf("oversized input = %v, want the cap %v", got, maxDur)
	}
}

// TestNewStream_DefaultsWhenLimitUnset guards against a zero limit silently
// producing a buffer that discards everything.
func TestNewStream_DefaultsWhenLimitUnset(t *testing.T) {
	for _, limit := range []int{0, -1} {
		s := newStream(limit)
		s.Write([]byte("kept"))
		if got := s.String(); got != "kept" {
			t.Errorf("newStream(%d) dropped its content: %q", limit, got)
		}
	}
}

// TestSupervisorDefaults covers the zero-value fallbacks, which only fire if a
// supervisor is built by hand rather than by the constructor.
func TestSupervisorDefaults(t *testing.T) {
	s := &BashSupervisor{}
	if got := s.heartbeatInterval(); got != heartbeatInterval {
		t.Errorf("heartbeatInterval() = %v, want %v", got, heartbeatInterval)
	}
	if got := s.capacity(); got != maxBackgroundProcs {
		t.Errorf("capacity() = %d, want %d", got, maxBackgroundProcs)
	}
}

// The foreground budget is a turn budget, not a command budget: a command that
// crosses it is handed off with a handle, never killed. Thirty seconds is short
// enough that the pin matters — a silent bump back to two minutes would put the
// old round-trip cost back without failing anything else.
func TestBashDefaultTimeout_IsTheForegroundBudget(t *testing.T) {
	if defaultBashTimeout != 30*time.Second {
		t.Errorf("defaultBashTimeout = %s, want 30s", defaultBashTimeout)
	}
	// The idle check only earns its keep while it can fire before the hard
	// limit, which under defaults it cannot — it exists for callers that raise
	// the timeout for known-long work.
	if defaultIdleTimeout <= defaultBashTimeout {
		t.Errorf("defaultIdleTimeout %s must stay above the foreground budget %s;"+
			" below it the hard limit fires first and the idle check is dead code",
			defaultIdleTimeout, defaultBashTimeout)
	}
}
