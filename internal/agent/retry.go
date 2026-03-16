package agent

import (
	"errors"
	"fmt"
	"iter"
	"math"
	"strings"
	"time"

	"google.golang.org/adk/session"
)

// RetryConfig controls retry behavior for transient LLM errors.
type RetryConfig struct {
	// MaxRetries is the maximum number of retry attempts (default 3).
	MaxRetries int

	// InitialDelay is the base delay before the first retry (default 1s).
	InitialDelay time.Duration

	// MaxDelay caps the exponential backoff delay (default 30s).
	MaxDelay time.Duration
}

// DefaultRetryConfig returns sensible defaults for retry behavior.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:   3,
		InitialDelay: 1 * time.Second,
		MaxDelay:     30 * time.Second,
	}
}

// isTransient returns true if the error is likely transient and worth retrying.
// This covers rate limits (429), server errors (5xx), timeouts, and connection resets.
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()

	transientPatterns := []string{
		"429",
		"rate limit",
		"rate_limit",
		"too many requests",
		"500",
		"502",
		"503",
		"504",
		"internal server error",
		"bad gateway",
		"service unavailable",
		"gateway timeout",
		"connection reset",
		"connection refused",
		"timeout",
		"deadline exceeded",
		"temporary failure",
		"overloaded",
	}

	lower := strings.ToLower(msg)
	for _, pattern := range transientPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}

	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return true
	}

	var tempErr interface{ Temporary() bool }
	if errors.As(err, &tempErr) && tempErr.Temporary() {
		return true
	}

	return false
}

// retryDelay calculates the delay for attempt n using exponential backoff.
func retryDelay(cfg RetryConfig, attempt int) time.Duration {
	delay := float64(cfg.InitialDelay) * math.Pow(2, float64(attempt))
	if delay > float64(cfg.MaxDelay) {
		delay = float64(cfg.MaxDelay)
	}
	return time.Duration(delay)
}

// WithRetry wraps an agent run function with retry logic for transient errors.
// If the iterator yields a transient error, it sleeps and retries the entire run.
// Non-transient errors are yielded immediately without retry.
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
				delay := retryDelay(cfg, attempt)
				time.Sleep(delay)
				continue
			}

			// Exhausted retries.
			_ = yield(nil, fmt.Errorf("transient error after %d retries: %w", cfg.MaxRetries, transientErr))
		}
	}
}
