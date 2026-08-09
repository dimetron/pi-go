package retry

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// providerErr wraps a verbatim provider message. Provider text is prose — it
// starts with a capital and ends with a full stop — which is exactly what the
// error-strings linter rejects at an errors.New call site, so it is built here
// instead of inline.
func providerErr(msg string) error { return errors.New(msg) }

// The messages below are verbatim from ~/.pi-go/sessions/*/events.jsonl, so the
// classifier is tested against the failures pi-go actually records rather than
// invented ones.
func TestIsTransient(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unrecognized", errors.New("something broke"), false},

		// Retryable: rate limiting with a reopening window.
		{"tpm rate limit", errors.New(`received error while streaming: {"type":"tokens","code":"rate_limit_exceeded","message":"Rate limit reached for gpt-5.6-luna on tokens per min (TPM): Limit 200000, Used 105953, Requested 125455. Please try again in 9.422s."}`), true},
		{"bare 429", errors.New("429 Too Many Requests"), true},
		{"rate limit prose", errors.New("rate limit exceeded"), true},
		{"rate_limit code", errors.New("rate_limit_error"), true},

		// Retryable: upstream faults.
		{"500", errors.New("500 Internal Server Error"), true},
		{"502", errors.New("502 Bad Gateway"), true},
		{"503", errors.New("503 Service Unavailable"), true},
		{"504", errors.New("504 Gateway Timeout"), true},
		{"ref'd 500", errors.New("Internal Server Error (ref: ed5c545e-ddee-4abf-a1f4-90ab10fdbcc2)"), true},
		{"overloaded model", errors.New("503 Service Unavailable: model 'minimax-m3' is temporarily overloaded, please retry shortly or try a different model"), true},
		{"sse server_error", errors.New(`received error while streaming: {"type":"server_error","code":"server_error","message":"An error occurred while processing your request. You can retry your request"}`), true},

		// Retryable: connection and stream faults.
		{"connection reset", errors.New("connection reset by peer"), true},
		{"connection refused", errors.New("connection refused"), true},
		{"deadline", errors.New("context deadline exceeded"), true},
		{"wrapped timeout", fmt.Errorf("wrapped: %w", errors.New("timeout")), true},
		{"timed out", errors.New("read tcp 192.168.1.103:57601->104.18.32.47:443: read: operation timed out"), true},
		{"http2 reset", errors.New("stream error: stream ID 9; INTERNAL_ERROR; received from peer"), true},
		{"truncated sse", errors.New("unexpected EOF"), true},
		{"truncated json", errors.New("unexpected end of JSON input"), true},

		// Terminal: a 429 that is quota exhaustion, not rate limiting. These
		// are the majority of 429s in the session corpus, and retrying them
		// only delays a failure the user has to fix by upgrading.
		{"ollama weekly quota", errors.New("429 Too Many Requests: you (dimetron) have reached your weekly usage limit, upgrade for higher limits: https://ollama.com/upgrade"), false},
		{"ollama session quota", errors.New("429 Too Many Requests: you (dimetron) have reached your session usage limit, upgrade for higher limits: https://ollama.com/upgrade"), false},
		{"openai quota", errors.New(`POST "https://api.openai.com/v1/chat/completions": 429 Too Many Requests {"message": "You exceeded your current quota, please check your plan and billing details."}`), false},

		// Terminal: auth and malformed requests.
		{"401", errors.New("401 Unauthorized"), false},
		{"expired token", errors.New(`POST "https://chatgpt.com/backend-api/codex/responses": 401 Unauthorized {"message": "Provided authentication token is expired.","code": "token_expired"}`), false},
		{"invalid key", errors.New("invalid api key"), false},
		{"missing scopes", errors.New("401 Unauthorized {\"message\": \"You have insufficient permissions for this operation. Missing scopes: api.responses.write\"}"), false},
		{"400", errors.New(`POST "https://api.openai.com/v1/responses": 400 Bad Request {"message": "Invalid 'input[2].call_id': empty string."}`), false},
		{"missing model", errors.New("404 Not Found: model 'deepseek-v4-flash:0731' not found"), false},
		{"sandbox dial", errors.New(`Post "https://api.openai.com/v1/chat/completions": dial tcp 172.66.0.243:443: connect: operation not permitted`), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTransient(tt.err); got != tt.want {
				t.Errorf("IsTransient(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

type timeoutError struct{ msg string }

func (e *timeoutError) Error() string { return e.msg }
func (e *timeoutError) Timeout() bool { return true }

type temporaryError struct{ msg string }

func (e *temporaryError) Error() string   { return e.msg }
func (e *temporaryError) Temporary() bool { return true }

func TestIsTransientInterfaces(t *testing.T) {
	if !IsTransient(&timeoutError{msg: "unique xyz123"}) {
		t.Error("Timeout() error should be transient")
	}
	if !IsTransient(&temporaryError{msg: "unique xyz123"}) {
		t.Error("Temporary() error should be transient")
	}
	if IsTransient(errors.New("unique non-matching error xyz123")) {
		t.Error("unmatched error should not be transient")
	}
}

// A Temporary() error whose message says the quota is gone must stay terminal:
// the interface is a transport-level hint and knows nothing about billing.
func TestIsTransientTerminalBeatsInterface(t *testing.T) {
	err := &temporaryError{msg: "429: you have reached your weekly usage limit"}
	if IsTransient(err) {
		t.Error("quota exhaustion should be terminal despite Temporary()")
	}
	if !IsTerminal(err) {
		t.Error("IsTerminal should report quota exhaustion")
	}
}

func TestServerDelay(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want time.Duration
		ok   bool
	}{
		{"fractional seconds", "Please try again in 9.422s. Visit ...", 9422 * time.Millisecond, true},
		{"minutes and seconds", "Please try again in 6m0.339s.", 6*time.Minute + 339*time.Millisecond, true},
		{"milliseconds", "Please try again in 500ms.", 500 * time.Millisecond, true},
		{"whole seconds", "Please try again in 20s.", 20 * time.Second, true},
		{"spelled out", "retry after 30 seconds", 30 * time.Second, true},
		{"spelled minutes", "Retry after 2 minutes", 2 * time.Minute, true},
		{"bare number is seconds", "Retry-After: 30", 30 * time.Second, true},
		{"no hint", "429 Too Many Requests", 0, false},
		{"empty", "", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ServerDelay(errors.New(tt.msg))
			if ok != tt.ok {
				t.Fatalf("ServerDelay(%q) ok = %v, want %v", tt.msg, ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("ServerDelay(%q) = %v, want %v", tt.msg, got, tt.want)
			}
		})
	}
}

func TestServerDelayNil(t *testing.T) {
	if _, ok := ServerDelay(nil); ok {
		t.Error("ServerDelay(nil) should not report a delay")
	}
}

func TestDelayBackoff(t *testing.T) {
	cfg := Config{MaxRetries: 5, InitialDelay: 100 * time.Millisecond, MaxDelay: time.Second}
	err := errors.New("503 service unavailable")

	for attempt, want := range map[int]time.Duration{
		0: 100 * time.Millisecond,
		1: 200 * time.Millisecond,
		2: 400 * time.Millisecond,
		3: 800 * time.Millisecond,
		4: time.Second, // capped
	} {
		if got := Delay(cfg, attempt, err); got != want {
			t.Errorf("Delay(attempt %d) = %v, want %v", attempt, got, want)
		}
	}
}

// The whole point of parsing the hint: the server's figure must beat a backoff
// that would otherwise retry inside the same exhausted window.
func TestDelayPrefersServerHint(t *testing.T) {
	cfg := Config{MaxRetries: 5, InitialDelay: time.Second, MaxDelay: time.Minute}
	err := providerErr("Rate limit reached. Please try again in 9.422s.")

	if got := Delay(cfg, 0, err); got != 9422*time.Millisecond {
		t.Errorf("Delay = %v, want the server's 9.422s", got)
	}
}

func TestDelayClampsServerHint(t *testing.T) {
	cfg := Config{MaxRetries: 5, InitialDelay: time.Second, MaxDelay: 10 * time.Second}
	err := providerErr("Please try again in 6m0.339s.")

	if got := Delay(cfg, 0, err); got != 10*time.Second {
		t.Errorf("Delay = %v, want the 10s cap", got)
	}
}

func TestDelayZeroConfigUsesDefaults(t *testing.T) {
	if got := Delay(Config{}, 0, errors.New("503")); got != DefaultConfig().InitialDelay {
		t.Errorf("Delay with zero config = %v, want %v", got, DefaultConfig().InitialDelay)
	}
}
