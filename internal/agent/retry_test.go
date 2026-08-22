package agent

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/retry"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

func TestIsTransient(t *testing.T) {
	tests := []struct {
		err       error
		transient bool
	}{
		{nil, false},
		{errors.New("something broke"), false},
		{errors.New("invalid api key"), false},
		{errors.New("429 Too Many Requests"), true},
		{errors.New("rate limit exceeded"), true},
		{errors.New("rate_limit_error"), true},
		{errors.New("500 Internal Server Error"), true},
		{errors.New("502 Bad Gateway"), true},
		{errors.New("503 Service Unavailable"), true},
		{errors.New("504 Gateway Timeout"), true},
		{errors.New("connection reset by peer"), true},
		{errors.New("context deadline exceeded"), true},
		{errors.New("server is overloaded"), true},
		{errors.New("connection refused"), true},
		{fmt.Errorf("wrapped: %w", errors.New("timeout")), true},
	}

	for _, tt := range tests {
		name := "nil"
		if tt.err != nil {
			name = tt.err.Error()
		}
		t.Run(name, func(t *testing.T) {
			if got := isTransient(tt.err); got != tt.transient {
				t.Errorf("isTransient(%v) = %v, want %v", tt.err, got, tt.transient)
			}
		})
	}
}

func TestRetryDelay(t *testing.T) {
	cfg := RetryConfig{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
	}

	if d := retryDelay(cfg, 0); d != 100*time.Millisecond {
		t.Errorf("attempt 0: got %v, want 100ms", d)
	}
	if d := retryDelay(cfg, 1); d != 200*time.Millisecond {
		t.Errorf("attempt 1: got %v, want 200ms", d)
	}
	if d := retryDelay(cfg, 2); d != 400*time.Millisecond {
		t.Errorf("attempt 2: got %v, want 400ms", d)
	}
	if d := retryDelay(cfg, 10); d != 1*time.Second {
		t.Errorf("attempt 10: got %v, want 1s (capped)", d)
	}
}

func newTestEvent(text string) *session.Event {
	return &session.Event{
		LLMResponse: model.LLMResponse{
			Content: genai.NewContentFromText(text, genai.RoleModel),
		},
	}
}

func makeEventSeq(events []*session.Event, err error) func() iter.Seq2[*session.Event, error] {
	return func() iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			for _, ev := range events {
				if !yield(ev, nil) {
					return
				}
			}
			if err != nil {
				yield(nil, err)
			}
		}
	}
}

func TestWithRetrySuccess(t *testing.T) {
	ev := newTestEvent("hello")
	cfg := RetryConfig{MaxRetries: 3, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond}

	var collected []*session.Event
	for ev, err := range WithRetry(cfg, makeEventSeq([]*session.Event{ev}, nil)) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		collected = append(collected, ev)
	}
	if len(collected) != 1 {
		t.Fatalf("expected 1 event, got %d", len(collected))
	}
}

func TestWithRetryTransientThenSuccess(t *testing.T) {
	ev := newTestEvent("hello")
	calls := 0
	cfg := RetryConfig{MaxRetries: 3, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond}

	runFn := func() iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			calls++
			if calls <= 2 {
				yield(nil, errors.New("429 rate limit"))
				return
			}
			yield(ev, nil)
		}
	}

	var collected []*session.Event
	for ev, err := range WithRetry(cfg, runFn) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		collected = append(collected, ev)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
	if len(collected) != 1 {
		t.Errorf("expected 1 event, got %d", len(collected))
	}
}

func TestWithRetryExhausted(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 2, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond}

	runFn := func() iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			yield(nil, errors.New("503 service unavailable"))
		}
	}

	var lastErr error
	for _, err := range WithRetry(cfg, runFn) {
		if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if !strings.Contains(lastErr.Error(), "2 retries") {
		t.Errorf("expected retry count in error, got: %v", lastErr)
	}
}

func TestWithRetryNonTransientNotRetried(t *testing.T) {
	calls := 0
	cfg := RetryConfig{MaxRetries: 3, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond}

	runFn := func() iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			calls++
			yield(nil, errors.New("invalid api key"))
		}
	}

	var lastErr error
	for _, err := range WithRetry(cfg, runFn) {
		if err != nil {
			lastErr = err
		}
	}
	if calls != 1 {
		t.Errorf("non-transient error should not retry, but got %d calls", calls)
	}
	if lastErr == nil || !strings.Contains(lastErr.Error(), "invalid api key") {
		t.Errorf("expected original error, got: %v", lastErr)
	}
}

func TestWithRetryPartialResponseNotRetried(t *testing.T) {
	ev := newTestEvent("partial")
	calls := 0
	cfg := RetryConfig{MaxRetries: 3, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond}

	runFn := func() iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			calls++
			if !yield(ev, nil) {
				return
			}
			yield(nil, errors.New("connection reset"))
		}
	}

	var lastErr error
	var collected []*session.Event
	for ev, err := range WithRetry(cfg, runFn) {
		if err != nil {
			lastErr = err
			continue
		}
		collected = append(collected, ev)
	}
	if calls != 1 {
		t.Errorf("partial response should not retry, got %d calls", calls)
	}
	if lastErr == nil || !strings.Contains(lastErr.Error(), "not retrying") {
		t.Errorf("expected 'not retrying' error, got: %v", lastErr)
	}
	if len(collected) != 1 {
		t.Errorf("expected 1 partial event, got %d", len(collected))
	}
}

// timeoutError implements the Timeout() bool interface.
type timeoutError struct{ msg string }

func (e *timeoutError) Error() string { return e.msg }
func (e *timeoutError) Timeout() bool { return true }

// temporaryError implements the Temporary() bool interface.
type temporaryError struct{ msg string }

func (e *temporaryError) Error() string   { return e.msg }
func (e *temporaryError) Temporary() bool { return true }

// TestIsTransientTimeoutInterface verifies that errors implementing the
// Timeout() bool interface are classified as transient.
func TestIsTransientTimeoutInterface(t *testing.T) {
	err := &timeoutError{msg: "connection timed out"}
	if !isTransient(err) {
		t.Errorf("isTransient(timeoutError) = false, want true")
	}
}

// TestIsTransientTemporaryInterface verifies that errors implementing the
// Temporary() bool interface are classified as transient.
func TestIsTransientTemporaryInterface(t *testing.T) {
	err := &temporaryError{msg: "temporary failure occurred"}
	if !isTransient(err) {
		t.Errorf("isTransient(temporaryError) = false, want true")
	}
}

// TestIsTransientTimeoutInterfaceFalse verifies that a Timeout()==false error
// is not automatically transient (unless the message matches a pattern).
func TestIsTransientTimeoutInterfaceFalse(t *testing.T) {
	err := fmt.Errorf("unique non-matching error xyz123")
	if isTransient(err) {
		t.Errorf("isTransient(non-matching error) = true, want false")
	}
}

// Each replay is announced to the notifier on the context, numbered against
// the budget, so the TUI can show "Retrying in … (n/3)" instead of a silent
// pause.
func TestWithRetryContextNotifies(t *testing.T) {
	ev := newTestEvent("hello")
	calls := 0
	cfg := RetryConfig{MaxRetries: 3, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond}

	runFn := func() iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			calls++
			if calls <= 2 {
				yield(nil, errors.New("read tcp 10.5.50.62:58742->104.18.2.115:443: read: can't assign requested address"))
				return
			}
			yield(ev, nil)
		}
	}

	var notices []retry.Attempt
	ctx := retry.WithNotifier(context.Background(), func(a retry.Attempt) { notices = append(notices, a) })

	for _, err := range WithRetryContext(ctx, cfg, runFn) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
	if len(notices) != 2 {
		t.Fatalf("notifier called %d times, want 2", len(notices))
	}
	for i, n := range notices {
		if n.Attempt != i+1 || n.MaxRetries != 3 || n.Err == nil {
			t.Errorf("notice %d = %+v", i, n)
		}
	}
}

// A canceled context ends the backoff sleep and the retry loop with it: the
// user has walked away, so there is nothing to re-send for.
func TestWithRetryContextCanceledDuringBackoff(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 3, InitialDelay: time.Hour, MaxDelay: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = retry.WithNotifier(ctx, func(retry.Attempt) { cancel() })

	calls := 0
	runFn := func() iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			calls++
			yield(nil, errors.New("503 service unavailable"))
		}
	}

	var got error
	for _, err := range WithRetryContext(ctx, cfg, runFn) {
		got = err
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
	if !errors.Is(got, context.Canceled) {
		t.Errorf("want context.Canceled, got %v", got)
	}
}
