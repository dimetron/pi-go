package subagent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	sharedacp "github.com/dimetron/pi-go/internal/acp"
)

func TestTerminalStatus(t *testing.T) {
	t.Parallel()

	// A timeout also surfaces as a signal kill, so it must be classified as a
	// timeout even when wrapped alongside one.
	killed := exec.Command("false").Run() // *exec.ExitError, unsuccessful

	tests := []struct {
		name    string
		waitErr error
		want    string
	}{
		{"clean exit", nil, "completed"},
		{"timeout sentinel", ErrSubagentTimeout, "timeout"},
		{"wrapped timeout", fmt.Errorf("pi subagent: %w", ErrSubagentTimeout), "timeout"},
		{"signal kill", killed, "killed"},
		{"signal-worded error", errors.New("signal: killed"), "killed"},
		{"plain failure", errors.New("boom"), "failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := terminalStatus(tt.waitErr); got != tt.want {
				t.Errorf("terminalStatus(%v) = %q, want %q", tt.waitErr, got, tt.want)
			}
		})
	}
}

func TestSpawnFailure(t *testing.T) {
	t.Parallel()

	cfg := TimeoutConfig{Absolute: 90 * time.Second, Inactivity: 30 * time.Second}
	waitErr := errors.New("exit status 2")

	tests := []struct {
		name         string
		timedOutIdle bool
		procCtxErr   error
		waitErr      error
		stderr       string
		wantNil      bool
		wantTimeout  bool     // errors.Is(err, ErrSubagentTimeout)
		wantContains []string // substrings the message must carry
	}{
		{
			name:    "clean exit",
			wantNil: true,
		},
		{
			// A clean exit wins over stderr chatter: warnings are not failures.
			name:    "stderr without wait error",
			stderr:  "warning: something",
			wantNil: true,
		},
		{
			name:         "inactivity beats everything",
			timedOutIdle: true,
			procCtxErr:   context.DeadlineExceeded,
			waitErr:      waitErr,
			stderr:       "noise",
			wantTimeout:  true,
			wantContains: []string{"produced no output for 30s", timeoutHint},
		},
		{
			name:         "absolute deadline",
			procCtxErr:   context.DeadlineExceeded,
			waitErr:      waitErr,
			wantTimeout:  true,
			wantContains: []string{"exceeded its 1m30s time limit", timeoutHint},
		},
		{
			// Cancellation is not a deadline, so it falls through to the exit status.
			name:         "canceled context falls through",
			procCtxErr:   context.Canceled,
			waitErr:      waitErr,
			wantContains: []string{"pi process failed", "exit status 2"},
		},
		{
			name:         "failure with stderr",
			waitErr:      waitErr,
			stderr:       "panic: nil map",
			wantContains: []string{"pi process failed", "exit status 2", "panic: nil map"},
		},
		{
			name:         "failure without stderr",
			waitErr:      waitErr,
			wantContains: []string{"pi process failed", "exit status 2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := spawnFailure(cfg, tt.timedOutIdle, tt.procCtxErr, tt.waitErr, tt.stderr)
			if tt.wantNil {
				if err != nil {
					t.Fatalf("spawnFailure() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("spawnFailure() = nil, want error")
			}
			if got := errors.Is(err, ErrSubagentTimeout); got != tt.wantTimeout {
				t.Errorf("errors.Is(err, ErrSubagentTimeout) = %v, want %v (err=%v)", got, tt.wantTimeout, err)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("spawnFailure() = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestJSONEventToEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   jsonEvent
		want Event
	}{
		{
			name: "text delta carries delta, not content",
			in:   jsonEvent{Type: "text_delta", Delta: "hi", Content: "ignored"},
			want: Event{Type: "text_delta", Content: "hi"},
		},
		{
			name: "tool call carries name and input",
			in:   jsonEvent{Type: "tool_call", ToolName: "grep", ToolInput: map[string]any{"q": "x"}},
			want: Event{Type: "tool_call", Content: "grep", ToolArgs: map[string]any{"q": "x"}},
		},
		{
			name: "tool result carries content",
			in:   jsonEvent{Type: "tool_result", Content: "{}", Delta: "ignored"},
			want: Event{Type: "tool_result", Content: "{}"},
		},
		{
			name: "message start carries session id only",
			in:   jsonEvent{Type: "message_start", SessionID: "s1", Content: "ignored"},
			want: Event{Type: "message_start", SessionID: "s1"},
		},
		{
			name: "message end is bare",
			in:   jsonEvent{Type: "message_end", Content: "ignored", SessionID: "ignored"},
			want: Event{Type: "message_end"},
		},
		{
			name: "unknown type concatenates delta and content",
			in:   jsonEvent{Type: "thinking", Delta: "a", Content: "b"},
			want: Event{Type: "thinking", Content: "ab"},
		},
		{
			name: "empty type stays empty",
			in:   jsonEvent{},
			want: Event{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, tt.in.toEvent()); diff != "" {
				t.Errorf("toEvent() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestStderrCaptureBounded(t *testing.T) {
	t.Parallel()

	c := &stderrCapture{}
	line := strings.Repeat("x", 1024)
	// Writes stop once the buffer is at or over the cap, so the total settles
	// just past maxStderrCapture rather than exactly on it.
	for range (maxStderrCapture / len(line)) + 10 {
		c.writeLine(line)
	}
	got := len(c.String())
	if got < maxStderrCapture {
		t.Errorf("captured %d bytes, want at least %d", got, maxStderrCapture)
	}
	if got > maxStderrCapture+len(line)+1 {
		t.Errorf("captured %d bytes, want no more than %d", got, maxStderrCapture+len(line)+1)
	}
}

func TestStderrCaptureTrimsAndSeparates(t *testing.T) {
	t.Parallel()

	c := &stderrCapture{}
	if got := c.String(); got != "" {
		t.Errorf("empty capture = %q, want %q", got, "")
	}
	c.writeLine("first")
	c.writeLine("second")
	if diff := cmp.Diff("first\nsecond", c.String()); diff != "" {
		t.Errorf("String() mismatch (-want +got):\n%s", diff)
	}
}

func TestDrainStderr(t *testing.T) {
	t.Parallel()

	capture, done := drainStderr(strings.NewReader("one\ntwo\n"))
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("drainStderr did not signal EOF")
	}
	if diff := cmp.Diff("one\ntwo", capture.String()); diff != "" {
		t.Errorf("capture mismatch (-want +got):\n%s", diff)
	}
}

func TestScanLines(t *testing.T) {
	t.Parallel()

	var got []string
	for line := range scanLines(strings.NewReader("a\n\nb\n")) {
		got = append(got, line)
	}
	// Blank lines survive the scan; readEvents is what skips them.
	if diff := cmp.Diff([]string{"a", "", "b"}, got); diff != "" {
		t.Errorf("scanLines mismatch (-want +got):\n%s", diff)
	}
}

func TestProcessReadEvents(t *testing.T) {
	t.Parallel()

	lines := make(chan string, 8)
	for _, l := range []string{
		`{"type":"text_delta","delta":"he"}`,
		"", // skipped
		`not json at all`,
		`{"type":"text_delta","delta":"llo"}`,
		`{"type":"message_end"}`,
	} {
		lines <- l
	}
	close(lines)

	proc := &Process{events: make(chan Event, 64)}
	result, timedOut := proc.readEvents(lines, func() {}, time.Minute)
	close(proc.events)

	if timedOut {
		t.Error("timedOutIdle = true, want false")
	}
	if diff := cmp.Diff("hello", result); diff != "" {
		t.Errorf("result mismatch (-want +got):\n%s", diff)
	}

	var got []Event
	for ev := range proc.events {
		got = append(got, ev)
	}
	want := []Event{
		{Type: "text_delta", Content: "he"},
		{Type: "text_delta", Content: "not json at all"},
		{Type: "text_delta", Content: "llo"},
		{Type: "message_end"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("events mismatch (-want +got):\n%s", diff)
	}
}

func TestProcessReadEventsInactivity(t *testing.T) {
	t.Parallel()

	// Never closed and never written to: only the inactivity timer can end this.
	lines := make(chan string)
	proc := &Process{events: make(chan Event, 8)}

	canceled := make(chan struct{})
	result, timedOut := proc.readEvents(lines, func() { close(canceled); close(lines) }, time.Millisecond)

	if !timedOut {
		t.Error("timedOutIdle = false, want true")
	}
	if result != "" {
		t.Errorf("result = %q, want empty", result)
	}
	select {
	case <-canceled:
	default:
		t.Error("readEvents did not cancel the process group on inactivity")
	}
}

func TestOrchestratorSubagentEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		repoRoot string
		env      []string
		workDir  string
		want     []string
	}{
		{
			name:    "no worktree manager leaves env untouched",
			env:     []string{"A=1"},
			workDir: "/wt",
			want:    []string{"A=1"},
		},
		{
			name:     "adds sandbox and worktree roots",
			repoRoot: "/repo",
			env:      []string{"A=1"},
			workDir:  "/wt",
			want:     []string{"A=1", "PI_SANDBOX_ROOT=/repo", "PI_WORKTREE_ROOT=/wt"},
		},
		{
			name:     "empty work dir omits the worktree root",
			repoRoot: "/repo",
			env:      []string{"A=1"},
			want:     []string{"A=1", "PI_SANDBOX_ROOT=/repo"},
		},
		{
			name:     "nil env still gets the roots",
			repoRoot: "/repo",
			workDir:  "/wt",
			want:     []string{"PI_SANDBOX_ROOT=/repo", "PI_WORKTREE_ROOT=/wt"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			orch := NewOrchestrator(testConfig(), tt.repoRoot, nil)
			got := orch.subagentEnv(tt.env, tt.workDir)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("subagentEnv mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestOrchestratorSubagentEnvDoesNotAliasInput guards the copy in subagentEnv:
// the caller's slice must not gain the sandbox roots when it has spare capacity.
func TestOrchestratorSubagentEnvDoesNotAliasInput(t *testing.T) {
	t.Parallel()

	orch := NewOrchestrator(testConfig(), "/repo", nil)
	env := make([]string, 1, 8)
	env[0] = "A=1"

	orch.subagentEnv(env, "/wt")

	if diff := cmp.Diff([]string{"A=1"}, env); diff != "" {
		t.Errorf("caller env was mutated (-want +got):\n%s", diff)
	}
}

func TestOrchestratorPrepareWorkDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		repoRoot     string
		input        SpawnInput
		wantDir      string
		wantWorktree bool
	}{
		{
			name:         "explicit work dir wins and disables the worktree",
			repoRoot:     "/repo",
			input:        SpawnInput{WorkDir: "/elsewhere", Agent: AgentConfig{Worktree: true}},
			wantDir:      "/elsewhere",
			wantWorktree: false,
		},
		{
			name:         "no worktree falls back to the repo root",
			repoRoot:     "/repo",
			input:        SpawnInput{Agent: AgentConfig{Worktree: false}},
			wantDir:      "/repo",
			wantWorktree: false,
		},
		{
			name:         "worktree wanted but no manager configured",
			repoRoot:     "",
			input:        SpawnInput{Agent: AgentConfig{Worktree: true}},
			wantDir:      "",
			wantWorktree: true,
		},
		{
			name:         "input override beats the agent default",
			repoRoot:     "/repo",
			input:        SpawnInput{Agent: AgentConfig{Worktree: true}, Worktree: new(false)},
			wantDir:      "/repo",
			wantWorktree: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			orch := NewOrchestrator(testConfig(), tt.repoRoot, nil)
			dir, useWorktree, err := orch.prepareWorkDir("agent-1", tt.input)
			if err != nil {
				t.Fatalf("prepareWorkDir: %v", err)
			}
			if dir != tt.wantDir {
				t.Errorf("workDir = %q, want %q", dir, tt.wantDir)
			}
			if useWorktree != tt.wantWorktree {
				t.Errorf("useWorktree = %v, want %v", useWorktree, tt.wantWorktree)
			}
		})
	}
}

// TestOrchestratorPrepareWorkDirCreatesWorktree covers the branch that actually
// touches git, and the error branch below it.
func TestOrchestratorPrepareWorkDirCreatesWorktree(t *testing.T) {
	repo := initTestRepo(t)
	orch := NewOrchestrator(testConfig(), repo, nil)
	defer orch.Shutdown()

	dir, useWorktree, err := orch.prepareWorkDir("agent-1", SpawnInput{Agent: AgentConfig{Worktree: true}})
	if err != nil {
		t.Fatalf("prepareWorkDir: %v", err)
	}
	if !useWorktree {
		t.Error("useWorktree = false, want true")
	}
	if dir == repo || dir == "" {
		t.Errorf("workDir = %q, want a worktree path under %q", dir, repo)
	}
	if orch.worktree.Active() != 1 {
		t.Errorf("active worktrees = %d, want 1", orch.worktree.Active())
	}
}

func TestOrchestratorPrepareWorkDirCreateError(t *testing.T) {
	t.Parallel()

	// A repo root that is not a git repository makes `git worktree add` fail.
	orch := NewOrchestrator(testConfig(), t.TempDir(), nil)

	_, useWorktree, err := orch.prepareWorkDir("agent-1", SpawnInput{Agent: AgentConfig{Worktree: true}})
	if err == nil {
		t.Fatal("prepareWorkDir() = nil error, want a worktree creation failure")
	}
	if !strings.Contains(err.Error(), "creating worktree") {
		t.Errorf("error = %q, want it to mention creating worktree", err)
	}
	// Reported even on failure, so the caller knows to run worktree cleanup.
	if !useWorktree {
		t.Error("useWorktree = false, want true even on error")
	}
}

func TestOrchestratorAwaitOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status string // "" means the agent is not tracked at all
		events []Event
		want   runOutcome
	}{
		{
			name:   "stream closes with no terminal event",
			status: "completed",
			events: []Event{{Type: "text_delta"}},
			want:   outcomeSilent,
		},
		{
			name:   "empty stream",
			status: "completed",
			want:   outcomeSilent,
		},
		{
			name:   "message_end on a completed agent",
			status: "completed",
			events: []Event{{Type: "text_delta"}, {Type: "message_end"}},
			want:   outcomeOK,
		},
		{
			name:   "error event on a failed agent",
			status: "failed",
			events: []Event{{Type: "error"}},
			want:   outcomeCrashed,
		},
		{
			name:   "message_end on a killed agent",
			status: "killed",
			events: []Event{{Type: "message_end"}},
			want:   outcomeCrashed,
		},
		{
			name:   "timeout is not a crash",
			status: "timeout",
			events: []Event{{Type: "message_end"}},
			want:   outcomeOK,
		},
		{
			name:   "untracked agent reads as OK",
			status: "",
			events: []Event{{Type: "message_end"}},
			want:   outcomeOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			orch := NewOrchestrator(testConfig(), "", nil)
			const agentID = "agent-1"
			if tt.status != "" {
				orch.SetStatusForTest(agentID, tt.status)
			}

			events := make(chan Event, len(tt.events)+1)
			for _, ev := range tt.events {
				events <- ev
			}
			close(events)

			if got := orch.awaitOutcome(events, agentID); got != tt.want {
				t.Errorf("awaitOutcome() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSpawnerBuildCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    SpawnOpts
		wantDir string
		wantEnv string // an entry the environment must carry, "" to skip
	}{
		{
			name:    "work dir is applied",
			opts:    SpawnOpts{Prompt: "p", WorkDir: "/tmp"},
			wantDir: "/tmp",
		},
		{
			name:    "empty work dir is left unset",
			opts:    SpawnOpts{Prompt: "p"},
			wantDir: "",
		},
		{
			name:    "extra env is appended to the filtered base env",
			opts:    SpawnOpts{Prompt: "p", Env: []string{"PI_TEST_MARKER=1"}},
			wantEnv: "PI_TEST_MARKER=1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Spawner{PiBinary: "/bin/true"}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			cmd := s.buildCommand(ctx, tt.opts)
			if cmd.Dir != tt.wantDir {
				t.Errorf("cmd.Dir = %q, want %q", cmd.Dir, tt.wantDir)
			}
			if cmd.WaitDelay != 3*time.Second {
				t.Errorf("cmd.WaitDelay = %v, want 3s", cmd.WaitDelay)
			}
			if len(cmd.Env) == 0 {
				t.Fatal("cmd.Env is empty, want the filtered base env")
			}
			if tt.wantEnv != "" && !slices.Contains(cmd.Env, tt.wantEnv) {
				t.Errorf("cmd.Env is missing %q", tt.wantEnv)
			}
		})
	}
}

func TestStartPiProcess(t *testing.T) {
	t.Parallel()

	t.Run("missing binary fails to start", func(t *testing.T) {
		t.Parallel()
		cmd := exec.Command("/nonexistent/pi-binary")
		_, _, err := startPiProcess(cmd)
		if err == nil {
			t.Fatal("startPiProcess() = nil error, want a start failure")
		}
		if !strings.Contains(err.Error(), "starting pi process") {
			t.Errorf("error = %q, want it to mention starting pi process", err)
		}
	})

	t.Run("pipes are wired for a real command", func(t *testing.T) {
		t.Parallel()
		cmd := exec.Command("/bin/echo", "hi")
		stdout, stderr, err := startPiProcess(cmd)
		if err != nil {
			t.Fatalf("startPiProcess: %v", err)
		}
		if stdout == nil || stderr == nil {
			t.Fatal("startPiProcess returned a nil pipe")
		}
		var got []string
		for line := range scanLines(stdout) {
			got = append(got, line)
		}
		capture, done := drainStderr(stderr)
		<-done
		if err := cmd.Wait(); err != nil {
			t.Fatalf("cmd.Wait: %v", err)
		}
		if diff := cmp.Diff([]string{"hi"}, got); diff != "" {
			t.Errorf("stdout mismatch (-want +got):\n%s", diff)
		}
		if capture.String() != "" {
			t.Errorf("stderr = %q, want empty", capture.String())
		}
	})
}

// TestOrchestratorSpawnWithRetryStopsOnFirstSuccess pins the retry loop against
// spurious re-spawns: a terminal event on an agent that is not in a crash state
// ends the loop, so a healthy run costs exactly one spawn even with a retry
// budget available.
func TestOrchestratorSpawnWithRetryStopsOnFirstSuccess(t *testing.T) {
	var starts atomic.Int32
	prev := startACPSessionFn
	startACPSessionFn = func(_ context.Context, _, _ string, _ SpawnOpts) (acpSession, error) {
		starts.Add(1)
		sess := newFakeACPSession()
		go sess.finish(sharedacp.RunResult{Status: sharedacp.StatusSuccess, Result: "ok", SessionID: "s"})
		return sess, nil
	}
	t.Cleanup(func() { startACPSessionFn = prev })

	orch := NewOrchestrator(testConfig(), "", nil)
	defer orch.Shutdown()

	events, agentID, err := orch.SpawnWithRetry(t.Context(), SpawnInput{
		Agent:      AgentConfig{Name: "claude", Role: "smol"},
		Prompt:     "hi",
		MaxRetries: 1,
	})
	if err != nil {
		t.Fatalf("SpawnWithRetry: %v", err)
	}
	if agentID == "" {
		t.Error("agentID is empty, want the spawned agent's id")
	}
	if events == nil {
		t.Fatal("events channel is nil, want the spawned agent's channel")
	}
	if got := starts.Load(); got != 1 {
		t.Errorf("spawn attempts = %d, want 1 (no retry after a clean run)", got)
	}

	// Drain so the forwarding goroutine can finish.
	for range events { //nolint:revive // drain
	}
}
