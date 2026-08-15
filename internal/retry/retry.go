// Package retry classifies provider failures as retryable or terminal and
// paces the re-attempts.
//
// It exists because "is this worth retrying?" has to be answered identically in
// two places that cannot import each other: internal/provider, which retries a
// single failed HTTP request, and internal/agent, which retries a whole run.
//
// The classification is deliberately message-based. Providers hand pi-go their
// failures as opaque strings — an SSE stream error is a JSON body, not a typed
// error — so pattern matching on the text is the only signal available at both
// layers.
package retry

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config controls how many times a failed attempt is repeated and how long the
// pauses between attempts grow.
type Config struct {
	// MaxRetries is the number of retries after the initial attempt.
	MaxRetries int

	// InitialDelay is the pause before the first retry.
	InitialDelay time.Duration

	// MaxDelay caps the exponential backoff, and also caps any delay the
	// server itself asked for.
	MaxDelay time.Duration
}

// DefaultConfig returns the shared defaults.
//
// MaxDelay is a minute because the limits that produce a retryable 429 are
// per-minute windows: a shorter cap would guarantee the retry lands inside the
// same exhausted window it is waiting out.
func DefaultConfig() Config {
	return Config{
		MaxRetries:   5,
		InitialDelay: 1 * time.Second,
		MaxDelay:     60 * time.Second,
	}
}

// terminalPatterns are failures no amount of waiting will clear: the request,
// the credentials, or the account is wrong.
//
// These are matched *before* transientPatterns, because the most common failure
// in pi-go's own session history is a 429 that is not a rate limit at all —
// "you have reached your weekly usage limit" and "You exceeded your current
// quota" both arrive as 429 Too Many Requests. Classifying those by status code
// alone spends the full retry budget sleeping before reporting a failure the
// user has to fix by upgrading a plan.
var terminalPatterns = []string{
	// Quota and plan exhaustion, mostly wearing a 429.
	"usage limit",
	"upgrade for higher limits",
	"insufficient_quota",
	"exceeded your current quota",
	"check your plan and billing",
	"quota exceeded",

	// Authentication and authorization.
	"unauthorized",
	"forbidden",
	"invalid_api_key",
	"invalid api key",
	"incorrect api key",
	"token_expired",
	"insufficient permissions",
	"autherror",

	// Malformed or unroutable requests.
	"bad request",
	"invalid_request_error",
	"not found",

	// The sandbox denying the dial. Retrying re-denies it.
	"operation not permitted",
}

// transientPatterns are failures that a later identical request may survive.
var transientPatterns = []string{
	// Rate limiting proper — reached only if no terminal pattern matched.
	"429",
	"rate limit",
	"rate_limit",
	"too many requests",

	// Upstream faults.
	"500",
	"502",
	"503",
	"504",
	"internal server error",
	"server_error",
	"bad gateway",
	"service unavailable",
	"gateway timeout",
	"overloaded",
	"temporary failure",

	// Connection-level faults.
	"connection reset",
	"connection refused",
	"timeout",
	"timed out",
	"deadline exceeded",

	// A stream that died mid-flight. An HTTP/2 reset arrives as
	// "stream error: stream ID 9; INTERNAL_ERROR; received from peer", and a
	// truncated SSE body as "unexpected EOF" or "unexpected end of JSON
	// input" — none of which match any of the patterns above.
	"stream error:",
	"internal_error",
	"unexpected eof",
	"unexpected end of json input",
}

// IsTerminal reports whether err describes a failure that will recur
// identically however long the caller waits.
func IsTerminal(err error) bool {
	if err == nil {
		return false
	}
	// A server-supplied retry window means the failure clears when the window
	// reopens, so it is not terminal even when the prose otherwise reads like
	// quota exhaustion (see IsTransient for why that happens).
	if _, ok := ServerDelay(err); ok {
		return false
	}
	return containsAny(strings.ToLower(err.Error()), terminalPatterns)
}

// IsTransient reports whether err is worth retrying.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())

	// A server-supplied retry window wins over everything else. Providers only
	// name a window ("retry in 59s", "retryDelay:59s") for failures that clear
	// — per-minute token limits, throttling — never for the quota/plan
	// exhaustion that arrives with terminal prose. This beats the terminal
	// patterns because Gemini's per-minute token limit reuses that prose
	// verbatim ("You exceeded your current quota", "quota exceeded", "check
	// your plan and billing") while still telling us exactly when its window
	// reopens.
	if _, ok := ServerDelay(err); ok {
		return true
	}

	// Terminal wins on a tie: a quota 429 matches both lists.
	if containsAny(msg, terminalPatterns) {
		return false
	}
	if containsAny(msg, transientPatterns) {
		return true
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

func containsAny(msg string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// durationHintRe matches a Go-parseable duration after a retry hint, covering
// the "9.422s" and "6m0.339s" forms OpenAI uses and the "59.440629838s" form
// Gemini uses.
var durationHintRe = regexp.MustCompile(
	`(?i)(?:try again in|retry[- ]after:?|retry in|retrydelay:?)\s*((?:[0-9]+h)?(?:[0-9]+m)?[0-9]+(?:\.[0-9]+)?(?:ms|s|m|h))`)

// numberHintRe matches a bare number followed by an optional spelled-out unit,
// covering "retry after 30 seconds" and a raw Retry-After value.
var numberHintRe = regexp.MustCompile(
	`(?i)(?:try again in|retry[- ]after:?|retry in|retrydelay:?)\s*([0-9]+(?:\.[0-9]+)?)\s*(milliseconds?|ms|seconds?|secs?|minutes?|mins?)?`)

// ServerDelay extracts the wait the provider asked for, if it named one.
//
// A rate-limit body usually carries the exact figure ("Please try again in
// 9.422s" from OpenAI, "Please retry in 59.440629838s" or a
// "retryDelay:59s" detail from Gemini), and honoring it is the difference
// between one well-timed retry and a backoff schedule that keeps landing
// inside the same exhausted window.
func ServerDelay(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}
	msg := err.Error()

	if m := durationHintRe.FindStringSubmatch(msg); m != nil {
		if d, parseErr := time.ParseDuration(m[1]); parseErr == nil && d > 0 {
			return d, true
		}
	}

	m := numberHintRe.FindStringSubmatch(msg)
	if m == nil {
		return 0, false
	}
	n, parseErr := strconv.ParseFloat(m[1], 64)
	if parseErr != nil || n <= 0 {
		return 0, false
	}
	// A bare number means seconds: that is what the Retry-After header carries.
	unit := time.Second
	switch strings.ToLower(m[2]) {
	case "ms", "millisecond", "milliseconds":
		unit = time.Millisecond
	case "m", "min", "mins", "minute", "minutes":
		unit = time.Minute
	}
	return time.Duration(n * float64(unit)), true
}

// Delay returns how long to wait before the given retry attempt (0-based).
//
// A server-supplied hint wins over the computed backoff, since the server knows
// when its window reopens and we do not. Both are clamped to cfg.MaxDelay so a
// provider cannot park a turn indefinitely.
func Delay(cfg Config, attempt int, err error) time.Duration {
	maxDelay := cfg.MaxDelay
	if maxDelay <= 0 {
		maxDelay = DefaultConfig().MaxDelay
	}

	if hinted, ok := ServerDelay(err); ok {
		return min(hinted, maxDelay)
	}

	initial := cfg.InitialDelay
	if initial <= 0 {
		initial = DefaultConfig().InitialDelay
	}
	backoff := float64(initial) * math.Pow(2, float64(attempt))
	if backoff > float64(maxDelay) {
		return maxDelay
	}
	return time.Duration(backoff)
}
