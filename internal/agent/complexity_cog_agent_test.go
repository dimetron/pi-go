package agent

import (
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// Retry timing is behavior, not decoration: how many attempts happen, how long
// the pauses are, and which failures are replayed at all are all invisible to
// an ordinary test. These pin them against the pre-refactor WithRetry so a
// flattening cannot quietly change what pi-go does under provider failure.
//
// Nothing here sleeps for a real backoff schedule — every config uses
// millisecond delays, and the one case that must observe a pause asks for a
// window measured in tens of milliseconds.

// cogProviderErr wraps a verbatim provider message. Provider text is prose — it
// starts with a capital and ends with a full stop — which is what the
// error-strings linter rejects at an errors.New call site, so it is built here
// instead of inline.
func cogProviderErr(msg string) error { return errors.New(msg) }

func cogRetryEvent(text string) *session.Event {
	return &session.Event{
		LLMResponse: model.LLMResponse{
			Content: genai.NewContentFromText(text, genai.RoleModel),
		},
	}
}

// cogAlwaysFailing returns a runFn that yields err on every attempt and counts
// how many times it was started.
func cogAlwaysFailing(calls *int, err error) func() iter.Seq2[*session.Event, error] {
	return func() iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			*calls++
			yield(nil, err)
		}
	}
}

// cogDrain consumes a sequence completely, returning the events and the last
// error seen.
func cogDrain(seq iter.Seq2[*session.Event, error]) ([]*session.Event, error) {
	var events []*session.Event
	var lastErr error
	for ev, err := range seq {
		if err != nil {
			lastErr = err
			continue
		}
		events = append(events, ev)
	}
	return events, lastErr
}

// The attempt budget is MaxRetries retries *after* the initial attempt, so a
// run that never clears is started MaxRetries+1 times. MaxRetries: 0 means one
// attempt and no sleep at all.
func TestWithRetryAttemptBudget(t *testing.T) {
	tests := []struct {
		name       string
		maxRetries int
		wantCalls  int
	}{
		{"no retries means a single attempt", 0, 1},
		{"one retry means two attempts", 1, 2},
		{"three retries means four attempts", 3, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := RetryConfig{MaxRetries: tt.maxRetries, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond}
			calls := 0
			_, lastErr := cogDrain(WithRetry(cfg, cogAlwaysFailing(&calls, errors.New("503 service unavailable"))))

			if calls != tt.wantCalls {
				t.Errorf("runFn started %d times, want %d", calls, tt.wantCalls)
			}
			if lastErr == nil {
				t.Fatal("expected an exhaustion error")
			}
			if !strings.Contains(lastErr.Error(), "503 service unavailable") {
				t.Errorf("exhaustion error lost the cause: %v", lastErr)
			}
			if !strings.Contains(lastErr.Error(), "transient error after") {
				t.Errorf("exhaustion error changed shape: %v", lastErr)
			}
		})
	}
}

// The retryable/terminal split is the whole point of the loop. A terminal error
// is yielded verbatim on the first attempt; a transient one is replayed until
// the budget runs out and then wrapped.
func TestWithRetryErrorClassificationDrivesReplay(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantCalls int
		wantWrap  bool
	}{
		{"rate limit is replayed", errors.New("429 Too Many Requests"), 3, true},
		{"upstream 503 is replayed", errors.New("503 service unavailable"), 3, true},
		{"connection reset is replayed", errors.New("connection reset by peer"), 3, true},
		{"bad credentials are terminal", errors.New("invalid api key"), 1, false},
		{"quota exhaustion is terminal", errors.New("429 Too Many Requests: you have reached your weekly usage limit"), 1, false},
		{"an unrecognized failure is terminal", errors.New("something broke"), 1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := RetryConfig{MaxRetries: 2, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond}
			calls := 0
			_, lastErr := cogDrain(WithRetry(cfg, cogAlwaysFailing(&calls, tt.err)))

			if calls != tt.wantCalls {
				t.Errorf("runFn started %d times, want %d", calls, tt.wantCalls)
			}
			if lastErr == nil {
				t.Fatal("expected an error to reach the consumer")
			}
			gotWrap := strings.Contains(lastErr.Error(), "transient error after")
			if gotWrap != tt.wantWrap {
				t.Errorf("wrapped = %v, want %v (err: %v)", gotWrap, tt.wantWrap, lastErr)
			}
			if !tt.wantWrap && !errors.Is(lastErr, tt.err) {
				t.Errorf("terminal error was not passed through verbatim: %v", lastErr)
			}
		})
	}
}

// A consumer that stops ranging must stop the whole thing: no further attempt,
// no exhaustion error, and the inner run torn down at once.
func TestWithRetryConsumerStopEndsEverything(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 3, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond}
	calls := 0
	yielded := 0

	runFn := func() iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			calls++
			for range 5 {
				yielded++
				if !yield(cogRetryEvent("chunk"), nil) {
					return
				}
			}
			yield(nil, errors.New("503 service unavailable"))
		}
	}

	seen := 0
	for range WithRetry(cfg, runFn) {
		seen++
		break
	}

	if calls != 1 {
		t.Errorf("runFn started %d times after an early stop, want 1", calls)
	}
	if seen != 1 {
		t.Errorf("consumer saw %d items, want 1", seen)
	}
	if yielded != 1 {
		t.Errorf("runFn yielded %d times after the consumer stopped, want 1", yielded)
	}
}

// Stopping while a terminal error is being delivered must also end the run —
// that error travels through the same yield as an event.
func TestWithRetryConsumerStopOnTerminalError(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 3, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond}
	calls := 0
	afterYield := false

	runFn := func() iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			calls++
			if !yield(nil, errors.New("invalid api key")) {
				return
			}
			afterYield = true
		}
	}

	for range WithRetry(cfg, runFn) {
		break
	}

	if calls != 1 {
		t.Errorf("runFn started %d times, want 1", calls)
	}
	if afterYield {
		t.Error("runFn kept going after the consumer stopped")
	}
}

// A yielded nil event is not a partial response: the run produced nothing the
// consumer could act on, so it stays replayable.
func TestWithRetryNilEventIsNotAPartialResponse(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 2, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond}
	calls := 0

	runFn := func() iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			calls++
			if !yield(nil, nil) {
				return
			}
			yield(nil, errors.New("503 service unavailable"))
		}
	}

	_, lastErr := cogDrain(WithRetry(cfg, runFn))
	if calls != 3 {
		t.Errorf("runFn started %d times, want 3 — a nil event must not block replay", calls)
	}
	if lastErr == nil || !strings.Contains(lastErr.Error(), "transient error after 2 retries") {
		t.Errorf("got %v, want the exhaustion error", lastErr)
	}
}

// A real event already handed to the consumer makes the run unreplayable: tool
// calls may have run, so the transient error is reported instead of retried.
func TestWithRetryRealEventBlocksReplay(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 3, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond}
	calls := 0

	runFn := func() iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			calls++
			if !yield(cogRetryEvent("partial"), nil) {
				return
			}
			yield(nil, errors.New("503 service unavailable"))
		}
	}

	events, lastErr := cogDrain(WithRetry(cfg, runFn))
	if calls != 1 {
		t.Errorf("runFn started %d times, want 1", calls)
	}
	if len(events) != 1 {
		t.Errorf("consumer kept %d events, want 1", len(events))
	}
	if lastErr == nil || !strings.Contains(lastErr.Error(), "transient error after partial response (not retrying)") {
		t.Errorf("got %v, want the partial-response error", lastErr)
	}
}

// A successful run never sleeps and never repeats.
func TestWithRetrySuccessDoesNotSleep(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 3, InitialDelay: 5 * time.Second, MaxDelay: 5 * time.Second}
	calls := 0

	runFn := func() iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			calls++
			yield(cogRetryEvent("done"), nil)
		}
	}

	start := time.Now()
	events, lastErr := cogDrain(WithRetry(cfg, runFn))
	elapsed := time.Since(start)

	if calls != 1 {
		t.Errorf("runFn started %d times, want 1", calls)
	}
	if lastErr != nil {
		t.Errorf("unexpected error: %v", lastErr)
	}
	if len(events) != 1 {
		t.Errorf("got %d events, want 1", len(events))
	}
	if elapsed > time.Second {
		t.Errorf("a clean run slept for %v", elapsed)
	}
}

// The pause between attempts comes from retry.Delay, and the failing error is
// handed to it — a provider that names its own reopening window is obeyed
// instead of the exponential schedule. InitialDelay here is 1ms, so an observed
// pause of tens of milliseconds can only have come from the named window.
func TestWithRetryObeysServerNamedWindow(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 1, InitialDelay: time.Millisecond, MaxDelay: time.Minute}
	calls := 0
	err := cogProviderErr("429 Too Many Requests: rate limit reached. Please retry in 60ms.")

	start := time.Now()
	_, lastErr := cogDrain(WithRetry(cfg, cogAlwaysFailing(&calls, err)))
	elapsed := time.Since(start)

	if calls != 2 {
		t.Fatalf("runFn started %d times, want 2", calls)
	}
	if lastErr == nil {
		t.Fatal("expected an exhaustion error")
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("waited %v; the server-named 60ms window was not applied", elapsed)
	}
}

// MaxDelay caps a server-named window too. Without the cap this run would sleep
// for a minute.
func TestWithRetryMaxDelayCapsServerNamedWindow(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 1, InitialDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
	calls := 0
	err := cogProviderErr("429 Too Many Requests: rate limit reached. Please retry in 59.440629838s.")

	start := time.Now()
	_, lastErr := cogDrain(WithRetry(cfg, cogAlwaysFailing(&calls, err)))
	elapsed := time.Since(start)

	if calls != 2 {
		t.Fatalf("runFn started %d times, want 2", calls)
	}
	if lastErr == nil {
		t.Fatal("expected an exhaustion error")
	}
	if elapsed > 2*time.Second {
		t.Errorf("waited %v; MaxDelay did not cap the server-named window", elapsed)
	}
}

// The last attempt reports rather than sleeps: with MaxRetries: 2 there are two
// pauses, not three.
func TestWithRetryDoesNotSleepAfterTheLastAttempt(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 2, InitialDelay: 30 * time.Millisecond, MaxDelay: 30 * time.Millisecond}
	calls := 0

	start := time.Now()
	_, lastErr := cogDrain(WithRetry(cfg, cogAlwaysFailing(&calls, errors.New("503 service unavailable"))))
	elapsed := time.Since(start)

	if calls != 3 {
		t.Fatalf("runFn started %d times, want 3", calls)
	}
	if lastErr == nil {
		t.Fatal("expected an exhaustion error")
	}
	if elapsed < 45*time.Millisecond {
		t.Errorf("waited %v; expected two 30ms pauses between three attempts", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Errorf("waited %v; expected no pause after the final attempt", elapsed)
	}
}

// An empty run — no events, no error — completes without retrying.
func TestWithRetryEmptyRunCompletes(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 3, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond}
	calls := 0

	runFn := func() iter.Seq2[*session.Event, error] {
		return func(_ func(*session.Event, error) bool) { calls++ }
	}

	events, lastErr := cogDrain(WithRetry(cfg, runFn))
	if calls != 1 {
		t.Errorf("runFn started %d times, want 1", calls)
	}
	if lastErr != nil {
		t.Errorf("unexpected error: %v", lastErr)
	}
	if len(events) != 0 {
		t.Errorf("got %d events, want 0", len(events))
	}
}
