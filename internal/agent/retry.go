package agent

import (
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
	return func(yield func(*session.Event, error) bool) {
		for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
			var transientErr error
			hadEvents := false

			for ev, err := range runFn() {
				if err != nil && isTransient(err) {
					transientErr = err
					break
				}
				if !yield(ev, err) {
					return
				}
				if ev != nil {
					hadEvents = true
				}
			}

			if transientErr == nil {
				return
			}

			if hadEvents {
				// Already yielded partial results, cannot retry safely.
				_ = yield(nil, fmt.Errorf("transient error after partial response (not retrying): %w", transientErr))
				return
			}

			if attempt < cfg.MaxRetries {
				time.Sleep(retry.Delay(cfg, attempt, transientErr))
				continue
			}

			// Exhausted retries.
			_ = yield(nil, fmt.Errorf("transient error after %d retries: %w", cfg.MaxRetries, transientErr))
		}
	}
}
