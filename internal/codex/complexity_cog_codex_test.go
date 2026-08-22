package codex

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// loop's select arms were flattened into two helpers, startedResult and
// exitedResult. Each arm decides one thing: whether the turn is over, and with
// what RunResult. These tests pin every outcome of both helpers directly,
// which the session-level tests could only reach through timing.

// cogLoopSession returns a session wired to a fake app-server but with no loop
// goroutine running, so the helpers can be called directly without racing the
// session's own owner goroutine.
func cogLoopSession(t *testing.T, threadID string) (*Session, *fakeServer) {
	t.Helper()
	fs := newFakeServer(t)
	return newSession(fs.client, threadID), fs
}

func TestStartedResultOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		start      startResult
		text       string
		lastErr    string
		wantDone   bool
		wantStatus string
		wantError  string
		wantStop   string
		wantResult string
	}{
		{
			name:       "a failed turn/start ends the turn with the start error",
			start:      startResult{err: errStartFailed},
			text:       "partial answer",
			wantDone:   true,
			wantStatus: StatusError,
			wantError:  "turn/start rejected",
			wantResult: "partial answer",
		},
		{
			name:     "a turn that is still in progress keeps the loop running",
			start:    startResult{turn: &Turn{ID: "t1", Status: TurnInProgress}},
			text:     "so far",
			wantDone: false,
		},
		{
			name:     "a start response with no turn keeps the loop running",
			start:    startResult{},
			wantDone: false,
		},
		{
			name:       "a start response that is already completed ends the turn",
			start:      startResult{turn: &Turn{ID: "t1", Status: TurnCompleted}},
			text:       "the answer",
			wantDone:   true,
			wantStatus: StatusSuccess,
			wantStop:   TurnCompleted,
			wantResult: "the answer",
		},
		{
			name:       "a start response that already failed ends the turn with the turn error",
			start:      startResult{turn: &Turn{ID: "t1", Status: TurnFailed, Error: "model unavailable"}},
			wantDone:   true,
			wantStatus: StatusError,
			wantError:  "model unavailable",
			wantStop:   TurnFailed,
		},
		{
			name:       "a failed turn with no error of its own falls back to the last reported error",
			start:      startResult{turn: &Turn{ID: "t1", Status: TurnFailed}},
			lastErr:    "rate limited",
			wantDone:   true,
			wantStatus: StatusError,
			wantError:  "rate limited",
			wantStop:   TurnFailed,
		},
		{
			name:       "an interrupted start response ends the turn with a synthesized reason",
			start:      startResult{turn: &Turn{ID: "t1", Status: TurnInterrupted}},
			wantDone:   true,
			wantStatus: StatusError,
			wantError:  "codex turn interrupted",
			wantStop:   TurnInterrupted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess, _ := cogLoopSession(t, "thr_1")

			done, got := sess.startedResult(tt.start, tt.text, tt.lastErr)
			if done != tt.wantDone {
				t.Fatalf("done = %v, want %v", done, tt.wantDone)
			}
			if !tt.wantDone {
				if got != (RunResult{}) {
					t.Errorf("RunResult = %+v, want the zero value while the turn runs", got)
				}
				return
			}
			if got.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", got.Status, tt.wantStatus)
			}
			if got.Error != tt.wantError {
				t.Errorf("Error = %q, want %q", got.Error, tt.wantError)
			}
			if got.StopReason != tt.wantStop {
				t.Errorf("StopReason = %q, want %q", got.StopReason, tt.wantStop)
			}
			if got.Result != tt.wantResult {
				t.Errorf("Result = %q, want %q", got.Result, tt.wantResult)
			}
		})
	}
}

// errStartFailed stands in for whatever turn/start rejection the app-server
// reports; only its message reaches the RunResult.
var errStartFailed = cogStartError("turn/start rejected")

type cogStartError string

func (e cogStartError) Error() string { return string(e) }

// TestStartedResultKeepsSessionIDOnlyForTerminalTurns pins a pre-existing
// asymmetry the flattening preserves: the start-error result carries no
// session ID, while a result built from a terminal turn does.
func TestStartedResultKeepsSessionIDOnlyForTerminalTurns(t *testing.T) {
	sess, _ := cogLoopSession(t, "thr_9")

	_, startErr := sess.startedResult(startResult{err: errStartFailed}, "", "")
	if startErr.SessionID != "" {
		t.Errorf("start-error SessionID = %q, want empty", startErr.SessionID)
	}

	_, completed := sess.startedResult(startResult{turn: &Turn{Status: TurnCompleted}}, "", "")
	if completed.SessionID != "thr_9" {
		t.Errorf("completed SessionID = %q, want %q", completed.SessionID, "thr_9")
	}
}

// TestExitedResultCrashesWhenNothingIsBuffered pins the plain crash path: the
// subprocess is gone and nothing terminal was queued behind the exit signal.
func TestExitedResultCrashesWhenNothingIsBuffered(t *testing.T) {
	sess, _ := cogLoopSession(t, "thr_1")

	var text strings.Builder
	text.WriteString("half an answer")
	lastErr := ""

	got := sess.exitedResult(&text, &lastErr)

	if got.Status != StatusError {
		t.Errorf("Status = %q, want %q", got.Status, StatusError)
	}
	if got.Error != "codex app-server exited before the turn completed" {
		t.Errorf("Error = %q, want the plain crash message", got.Error)
	}
	if got.Result != "half an answer" {
		t.Errorf("Result = %q, want the text accumulated so far", got.Result)
	}
	if got.SessionID != "thr_1" {
		t.Errorf("SessionID = %q, want %q", got.SessionID, "thr_1")
	}
}

// TestExitedResultDrainsACompletionQueuedBehindTheExit pins the reason the
// drain exists: a turn that finished just as the process went away must be
// reported as that turn's outcome, not as a crash.
func TestExitedResultDrainsACompletionQueuedBehindTheExit(t *testing.T) {
	sess, fs := cogLoopSession(t, "thr_1")

	cogQueueNotification(t, fs, NotifyTurnCompleted, TurnCompletedParams{
		ThreadID: "thr_1",
		Turn:     Turn{ID: "turn_1", Status: TurnCompleted},
	})

	var text strings.Builder
	text.WriteString("the answer")
	lastErr := ""

	got := sess.exitedResult(&text, &lastErr)

	if got.Status != StatusSuccess {
		t.Errorf("Status = %q, want %q", got.Status, StatusSuccess)
	}
	if got.StopReason != TurnCompleted {
		t.Errorf("StopReason = %q, want %q", got.StopReason, TurnCompleted)
	}
	if got.Result != "the answer" {
		t.Errorf("Result = %q, want %q", got.Result, "the answer")
	}
	if got.Error != "" {
		t.Errorf("Error = %q, want empty", got.Error)
	}
}

// TestExitedResultFoldsADrainedErrorIntoTheCrashMessage pins the other half of
// the drain: a non-terminal error notification queued behind the exit does not
// end the turn, but it does explain the crash.
func TestExitedResultFoldsADrainedErrorIntoTheCrashMessage(t *testing.T) {
	sess, fs := cogLoopSession(t, "thr_1")

	cogQueueNotification(t, fs, NotifyError, ErrorParams{
		Error: RPCError{Message: "usage limit reached"},
	})

	var text strings.Builder
	lastErr := ""

	got := sess.exitedResult(&text, &lastErr)

	if lastErr != "usage limit reached" {
		t.Errorf("lastErr = %q, want it updated to the drained error", lastErr)
	}
	want := "codex app-server exited before the turn completed: usage limit reached"
	if got.Error != want {
		t.Errorf("Error = %q, want %q", got.Error, want)
	}
	if got.Status != StatusError {
		t.Errorf("Status = %q, want %q", got.Status, StatusError)
	}
}

// TestExitedResultIgnoresAChildThreadCompletion pins that the drain applies the
// same thread filter the live path does: a child thread finishing is progress,
// not the end of our turn.
func TestExitedResultIgnoresAChildThreadCompletion(t *testing.T) {
	sess, fs := cogLoopSession(t, "thr_parent")

	cogQueueNotification(t, fs, NotifyTurnCompleted, TurnCompletedParams{
		ThreadID: "thr_child",
		Turn:     Turn{ID: "turn_child", Status: TurnCompleted},
	})

	var text strings.Builder
	lastErr := ""

	got := sess.exitedResult(&text, &lastErr)

	if got.Status != StatusError {
		t.Errorf("Status = %q, want %q — a child completion must not end the turn", got.Status, StatusError)
	}
	if got.Error != "codex app-server exited before the turn completed" {
		t.Errorf("Error = %q, want the plain crash message", got.Error)
	}
}

// cogQueueNotification places a notification directly on the client's
// notification channel. Writing to the channel rather than through the fake
// server's pipe keeps the test free of any race with the reader goroutine: the
// notification is buffered before the drain starts, every time.
func cogQueueNotification(t *testing.T, fs *fakeServer, method string, params any) {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal %s params: %v", method, err)
	}
	fs.client.notifCh <- JSONRPCNotification{Method: method, Params: raw}
}

// TestSessionTerminalStartResponseFinishesTheTurn drives the whole loop: a
// turn/start response that already reports a terminal status must finish the
// session, because no turn/completed notification will ever follow it. This
// case runs unchanged against the pre-refactor loop.
func TestSessionTerminalStartResponseFinishesTheTurn(t *testing.T) {
	fs := newFakeServer(t)

	type startedSession struct {
		sess *Session
		err  error
	}
	ch := make(chan startedSession, 1)
	go func() {
		sess, err := startSession(t.Context(), fs.client, SessionOpts{Prompt: "go"})
		ch <- startedSession{sess, err}
	}()

	threadReq := fs.readRequest()
	fs.respond(*threadReq.ID, ThreadStartResponse{Thread: Thread{ID: "thr_1"}})

	res := <-ch
	if res.err != nil {
		t.Fatalf("startSession: %v", res.err)
	}
	t.Cleanup(func() { _ = fs.client.close() })

	turnReq := fs.readRequest()
	fs.respond(*turnReq.ID, TurnStartResponse{Turn: Turn{
		ID:     "turn_1",
		Status: TurnFailed,
		Error:  "model unavailable",
	}})

	collect(t, res.sess)

	select {
	case <-res.sess.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("session did not finish on an already-terminal start response")
	}

	result := res.sess.Wait()
	if result.Status != StatusError {
		t.Errorf("Status = %q, want %q", result.Status, StatusError)
	}
	if result.StopReason != TurnFailed {
		t.Errorf("StopReason = %q, want %q", result.StopReason, TurnFailed)
	}
	if result.Error != "model unavailable" {
		t.Errorf("Error = %q, want %q", result.Error, "model unavailable")
	}
	if result.SessionID != "thr_1" {
		t.Errorf("SessionID = %q, want %q", result.SessionID, "thr_1")
	}
}
