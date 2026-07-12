package codex

import (
	"encoding/json"
	"testing"
	"time"
)

// startFakeSession spins up a Session against the fake app-server: it answers
// thread/start with threadID, then answers the turn/start (or review/start)
// the session sends. It returns the session and the fake server so the test can
// push notifications at it.
func startFakeSession(t *testing.T, opts SessionOpts, threadID, turnID string) (*Session, *fakeServer) {
	t.Helper()

	fs := newFakeServer(t)

	type started struct {
		sess *Session
		err  error
	}
	ch := make(chan started, 1)
	go func() {
		sess, err := startSession(t.Context(), fs.client, opts)
		ch <- started{sess, err}
	}()

	req := fs.readRequest()
	if req.Method != MethodThreadStart {
		t.Fatalf("first request = %q, want %q", req.Method, MethodThreadStart)
	}
	fs.respond(*req.ID, ThreadStartResponse{Thread: Thread{ID: threadID}})

	res := <-ch
	if res.err != nil {
		t.Fatalf("startSession: %v", res.err)
	}

	// The turn request is sent by the session's own goroutine, concurrently
	// with the notification handler that is already running.
	turnReq := fs.readRequest()
	wantMethod := MethodTurnStart
	if opts.Review {
		wantMethod = MethodReviewStart
	}
	if turnReq.Method != wantMethod {
		t.Fatalf("turn request = %q, want %q", turnReq.Method, wantMethod)
	}
	fs.respond(*turnReq.ID, TurnStartResponse{Turn: Turn{ID: turnID, Status: TurnInProgress}})

	// The response is routed asynchronously; wait until the session has recorded
	// the turn so tests never race the start they just completed.
	waitFor(t, func() bool { return res.sess.TurnID() == turnID }, "turn id to be recorded")

	t.Cleanup(func() { _ = fs.client.close() })
	return res.sess, fs
}

// waitFor polls cond until it holds or the test times out.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// collect drains the session's events until the channel closes.
func collect(t *testing.T, sess *Session) []Event {
	t.Helper()
	var events []Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-sess.Events():
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-timeout:
			t.Fatal("timed out draining session events")
		}
	}
}

func TestSession_TranslatesItemsAndCompletes(t *testing.T) {
	sess, fs := startFakeSession(t, SessionOpts{Prompt: "fix the bug", Sandbox: SandboxWorkspaceWrite}, "thr_1", "turn_1")

	fs.notify(NotifyTurnStarted, TurnStartedParams{ThreadID: "thr_1", Turn: Turn{ID: "turn_1", Status: TurnInProgress}})
	fs.notify(NotifyItemCompleted, ItemParams{ThreadID: "thr_1", Item: Item{
		Type: ItemReasoning, Summary: []string{"thinking about it"},
	}})
	fs.notify(NotifyItemStarted, ItemParams{ThreadID: "thr_1", Item: Item{
		Type: ItemCommandExecution, Command: "go build ./...",
	}})
	exit := 0
	fs.notify(NotifyItemCompleted, ItemParams{ThreadID: "thr_1", Item: Item{
		Type: ItemCommandExecution, Command: "go build ./...", ExitCode: &exit,
	}})
	fs.notify(NotifyItemStarted, ItemParams{ThreadID: "thr_1", Item: Item{
		Type: ItemFileChange, Changes: []FileChange{{Path: "main.go"}},
	}})
	fs.notify(NotifyItemCompleted, ItemParams{ThreadID: "thr_1", Item: Item{
		Type: ItemAgentMessage, Text: "fixed it", Phase: PhaseFinalAnswer,
	}})
	fs.notify(NotifyTurnCompleted, TurnCompletedParams{
		ThreadID: "thr_1",
		Turn:     Turn{ID: "turn_1", Status: TurnCompleted},
	})

	events := collect(t, sess)

	wantTypes := []string{
		EventTypeProgress, // reasoning
		EventTypeTool,     // command started
		EventTypeTool,     // command completed
		EventTypeTool,     // file change started
		EventTypeMessage,  // agent message
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("event count = %d, want %d: %+v", len(events), len(wantTypes), events)
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Errorf("event[%d].Type = %q, want %q", i, events[i].Type, want)
		}
		if events[i].SessionID != "thr_1" {
			t.Errorf("event[%d].SessionID = %q, want thr_1", i, events[i].SessionID)
		}
	}
	if events[1].Content != "Running: go build ./..." {
		t.Errorf("command start content = %q", events[1].Content)
	}
	if events[2].Content != "Command completed (exit 0)" {
		t.Errorf("command end content = %q", events[2].Content)
	}
	if events[3].Content != "Applying 1 file changes" {
		t.Errorf("file change content = %q", events[3].Content)
	}

	result := sess.Wait()
	if result.Status != StatusSuccess {
		t.Errorf("status = %q, want %q (error: %q)", result.Status, StatusSuccess, result.Error)
	}
	if result.Result != "fixed it" {
		t.Errorf("result = %q, want the final agent message", result.Result)
	}
	if result.StopReason != TurnCompleted {
		t.Errorf("stopReason = %q, want %q", result.StopReason, TurnCompleted)
	}
	if result.SessionID != "thr_1" {
		t.Errorf("sessionID = %q, want thr_1", result.SessionID)
	}
}

// TestSession_IgnoresChildThreadCompletion covers the thread-ID filter: codex
// spawns collab/subagent threads, and their turn/completed must not tear down
// the outer session.
func TestSession_IgnoresChildThreadCompletion(t *testing.T) {
	sess, fs := startFakeSession(t, SessionOpts{Prompt: "do work"}, "thr_1", "turn_1")

	fs.notify(NotifyTurnCompleted, TurnCompletedParams{
		ThreadID: "thr_child",
		Turn:     Turn{ID: "turn_child", Status: TurnCompleted},
	})

	select {
	case <-sess.Done():
		t.Fatal("a child thread's turn/completed terminated the outer session")
	case <-time.After(150 * time.Millisecond):
	}

	fs.notify(NotifyItemCompleted, ItemParams{ThreadID: "thr_1", Item: Item{
		Type: ItemAgentMessage, Text: "done", Phase: PhaseFinalAnswer,
	}})
	fs.notify(NotifyTurnCompleted, TurnCompletedParams{
		ThreadID: "thr_1",
		Turn:     Turn{ID: "turn_1", Status: TurnCompleted},
	})

	collect(t, sess)
	result := sess.Wait()
	if result.Status != StatusSuccess || result.Result != "done" {
		t.Errorf("result = %+v, want a successful 'done' from the outer thread", result)
	}
}

func TestSession_ErrorNotificationEmitsErrorEvent(t *testing.T) {
	sess, fs := startFakeSession(t, SessionOpts{Prompt: "go"}, "thr_1", "turn_1")

	fs.notify(NotifyError, ErrorParams{Error: RPCError{Code: 1, Message: "rate limited"}})
	fs.notify(NotifyTurnCompleted, TurnCompletedParams{
		ThreadID: "thr_1",
		Turn:     Turn{ID: "turn_1", Status: TurnFailed},
	})

	events := collect(t, sess)
	var sawError bool
	for _, ev := range events {
		if ev.Type == EventTypeError && ev.Error == "rate limited" {
			sawError = true
		}
	}
	if !sawError {
		t.Errorf("events = %+v, want an error event carrying the server message", events)
	}

	result := sess.Wait()
	if result.Status != StatusError {
		t.Fatalf("status = %q, want %q", result.Status, StatusError)
	}
	if result.Error != "rate limited" {
		t.Errorf("error = %q, want the last server error", result.Error)
	}
	if result.StopReason != TurnFailed {
		t.Errorf("stopReason = %q, want %q", result.StopReason, TurnFailed)
	}
}

func TestSession_CrashBeforeCompletion(t *testing.T) {
	sess, fs := startFakeSession(t, SessionOpts{Prompt: "go"}, "thr_1", "turn_1")

	fs.writeStderr("fatal: codex app-server panicked")
	fs.exit()

	collect(t, sess)

	select {
	case <-sess.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("session did not finish after the app-server exited")
	}

	result := sess.Wait()
	if result.Status != StatusError {
		t.Fatalf("status = %q, want %q", result.Status, StatusError)
	}
	if result.Error == "" {
		t.Error("expected an error explaining the early exit")
	}
	if result.Stderr == "" {
		t.Errorf("expected stderr to be reported, got %q", result.Stderr)
	}
}

func TestSession_CancelSendsTurnInterrupt(t *testing.T) {
	sess, fs := startFakeSession(t, SessionOpts{Prompt: "go"}, "thr_1", "turn_1")

	go func() { _ = sess.Cancel() }()

	req := fs.readRequest()
	if req.Method != MethodTurnInterrupt {
		t.Fatalf("method = %q, want %q", req.Method, MethodTurnInterrupt)
	}
	var params TurnInterruptParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.ThreadID != "thr_1" || params.TurnID != "turn_1" {
		t.Errorf("interrupt params = %+v, want thread thr_1 / turn turn_1", params)
	}

	// Cancel kills the subprocess, which ends the session even if the
	// app-server never answers the interrupt.
	select {
	case <-sess.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Cancel did not terminate the session")
	}
}

func TestSession_ReviewUsesReviewStart(t *testing.T) {
	sess, fs := startFakeSession(t, SessionOpts{Sandbox: SandboxReadOnly, Review: true}, "thr_r", "turn_r")

	fs.notify(NotifyItemCompleted, ItemParams{ThreadID: "thr_r", Item: Item{
		Type: ItemExitedReviewMode, Review: "LGTM, one nit on line 12",
	}})
	fs.notify(NotifyTurnCompleted, TurnCompletedParams{
		ThreadID: "thr_r",
		Turn:     Turn{ID: "turn_r", Status: TurnCompleted},
	})

	events := collect(t, sess)
	if len(events) != 1 || events[0].Type != EventTypeMessage {
		t.Fatalf("events = %+v, want a single message event with the review", events)
	}

	result := sess.Wait()
	if result.Status != StatusSuccess {
		t.Fatalf("status = %q, want success", result.Status)
	}
	if result.Result != "LGTM, one nit on line 12" {
		t.Errorf("result = %q, want the review text", result.Result)
	}
}

func TestNewSession_RequiresPrompt(t *testing.T) {
	if _, err := NewSession(t.Context(), SessionOpts{}); err == nil {
		t.Fatal("expected an error when no prompt is given for a turn")
	}
}

func TestNewSession_BinaryNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv(EnvCodexCmd, "")
	// The default paths are absolute, so PATH alone cannot hide a codex that is
	// actually installed on the machine running the tests.
	prev := DefaultBinaryPaths
	DefaultBinaryPaths = []string{"codex", "/nonexistent/codex"}
	t.Cleanup(func() { DefaultBinaryPaths = prev })

	_, err := NewSession(t.Context(), SessionOpts{Prompt: "hi"})
	if err == nil {
		t.Fatal("expected an error when codex is not installed")
	}
	if got := err.Error(); got != "codex not found in PATH or default locations" {
		t.Errorf("err = %q, want the documented not-found message", got)
	}
}
