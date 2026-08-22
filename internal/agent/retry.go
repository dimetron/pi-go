package agent

import (
	"context"
	"fmt"
	"iter"
	"time"

	"google.golang.org/adk/v2/session"

	"github.com/dimetron/pi-go/internal/retry"
)

// RetryConfig controls retry behavior for transient LLM errors.
type RetryConfig = retry.Config

// DefaultRetryConfig returns sensible defaults for retry behavior.
func DefaultRetryConfig() RetryConfig {
	return retry.DefaultConfig()
}

// isTransient returns true if the error is likely transient and worth retrying.
// Classification lives in internal/retry so that internal/provider, which
// cannot import this package, applies exactly the same rules.
func isTransient(err error) bool {
	return retry.IsTransient(err)
}

// retryDelay calculates the delay before retry attempt n (0-based).
func retryDelay(cfg RetryConfig, attempt int) time.Duration {
	return retry.Delay(cfg, attempt, nil)
}

// WithRetry wraps an agent run function with retry logic for transient errors.
// If the iterator yields a transient error, it sleeps and retries the entire run.
// Non-transient errors are yielded immediately without retry.
//
// This is the coarse net. It can only replay a run that produced nothing, so it
// does not help once a turn is underway — by the time a mid-turn request fails,
// tool calls have already been yielded and re-running them is not safe. The
// finer net is in internal/provider, which retries the single failed HTTP
// request without disturbing the turn around it.
func WithRetry(cfg RetryConfig, runFn func() iter.Seq2[*session.Event, error]) iter.Seq2[*session.Event, error] {
	return WithRetryContext(context.Background(), cfg, runFn)
}

// WithRetryContext is WithRetry with a context: each retry is announced to the
// retry.Notifier carried on ctx (see retry.WithNotifier), so the front-end can
// show that it is waiting on a retry rather than on the model, and the backoff
// sleep ends early if ctx is canceled.
func WithRetryContext(ctx context.Context, cfg RetryConfig, runFn func() iter.Seq2[*session.Event, error]) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		retryLoop(ctx, cfg, runFn, yield)
	}
}

// retryLoop runs runFn up to cfg.MaxRetries+1 times, sleeping between attempts.
// It returns as soon as an attempt finishes without a transient error — which
// includes the case where the consumer stopped ranging, since runAttempt stops
// with no transient error then. Each retry is announced to the retry.Notifier
// on ctx before the pause, and a canceled ctx ends the pause and the loop.
func retryLoop(ctx context.Context, cfg RetryConfig, runFn func() iter.Seq2[*session.Event, error], yield func(*session.Event, error) bool) {
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		hadEvents, transientErr := runAttempt(runFn, yield)

		if transientErr == nil {
			return
		}

		if hadEvents {
			// Already yielded partial results, cannot retry safely.
			_ = yield(nil, fmt.Errorf("transient error after partial response (not retrying): %w", transientErr))
			return
		}

		if attempt < cfg.MaxRetries {
			delay := retry.Delay(cfg, attempt, transientErr)
			retry.Notify(ctx, retry.Attempt{
				Attempt:    attempt + 1,
				MaxRetries: cfg.MaxRetries,
				Delay:      delay,
				Err:        transientErr,
			})
			if !sleepContext(ctx, delay) {
				_ = yield(nil, ctx.Err())
				return
			}
			continue
		}

		// Exhausted retries.
		_ = yield(nil, fmt.Errorf("transient error after %d retries: %w", cfg.MaxRetries, transientErr))
	}
}

// runAttempt forwards one run to the consumer. It reports the transient error
// that cut the run short, if any, and whether a real event reached the consumer
// — which is what makes a run unreplayable. A consumer that stops ranging ends
// the attempt with no transient error, so the caller stops too.
func runAttempt(runFn func() iter.Seq2[*session.Event, error], yield func(*session.Event, error) bool) (hadEvents bool, transientErr error) {
	for ev, err := range runFn() {
		if err != nil && isTransient(err) {
			return hadEvents, err
		}
		if !yield(ev, err) {
			return hadEvents, nil
		}
		if ev != nil {
			hadEvents = true
		}
	}
	return hadEvents, nil
}

// sleepContext waits for d, reporting false if ctx was canceled first.
func sleepContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
