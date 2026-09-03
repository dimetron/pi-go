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

		// Retryable: Gemini's per-minute token limit. The prose reuses the
		// quota-exhaustion wording that the terminal list matches, but it also
		// names its reopening window ("Please retry in 59.44s", retryDelay:59s)
		// — a window only a clearing failure carries, so it must beat the
		// terminal prose.
		{"gemini per-minute token limit", errors.New("Error 429, Message: You exceeded your current quota, please check your plan and billing details. For more information on this error, head to:    https://ai.google.dev/gemini-api/docs/rate-limits. To monitor your current usage, head to: https://ai.dev/rate-limit.    * Quota exceeded for metric: generativelanguage.googleapis.com/generate_content_paid_tier_input_token_count, limit: 2000000, model: gemini-3.7-flash    Please retry in 59.440629838s., Status: RESOURCE_EXHAUSTED, Details: [map[@type:type.googleapis.com/google.rpc.Help links:[map[description:Learn more about Gemini API quotas    url:https://ai.google.dev/gemini-api/docs/rate-limits]]] map[@type:type.googleapis.com/google.rpc.QuotaFailure violations:[map[quotaDimensions:map[location:global model:gemini-3.7-flash]    quotaId:GenerateContentPaidTierInputTokensPerModelPerMinute quotaMetric:generativelanguage.googleapis.com/generate_content_paid_tier_input_token_count quotaValue:2000000]]]    map[@type:type.googleapis.com/google.rpc.RetryInfo retryDelay:59s]]"), true},

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
		{"dns no such host", errors.New(`Post "https://chatgpt.com/backend-api/codex/responses": dial tcp: lookup chatgpt.com: no such host`), true},
		{"dns temporary failure", errors.New("dial tcp: lookup api.example.com: temporary failure in name resolution"), true},
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

// A failure that names a retry window must be transient even when its prose
// also matches the terminal quota patterns: a provider only tells you when its
// window reopens if the failure clears when that window closes. This is what
// Gemini's per-minute token limit does — it carries the plan/billing copy and
// a "retry in 59.4s" figure at once.
func TestIsTransientServerWindowBeatsTerminalProse(t *testing.T) {
	err := providerErr("429 Too Many Requests: you have exceeded your current quota, please check your plan and billing details. Please retry in 59.440629838s.")
	if !IsTransient(err) {
		t.Error("a named retry window should make a quota-looking failure transient")
	}
	if IsTerminal(err) {
		t.Error("a named retry window should clear the terminal classification")
	}
}

// The terminal list's own quota cases carry no window, so they must stay
// terminal even with the ServerDelay override in place.
func TestIsTransientQuotaWithoutWindowStaysTerminal(t *testing.T) {
	err := providerErr("429 Too Many Requests: you have reached your weekly usage limit, upgrade for higher limits: https://ollama.com/upgrade")
	if IsTransient(err) {
		t.Error("quota exhaustion without a retry window should be terminal")
	}
	if !IsTerminal(err) {
		t.Error("quota exhaustion without a retry window should be terminal")
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
		{"gemini retry in", "Please retry in 59.440629838s.", 59*time.Second + 440629838*time.Nanosecond, true},
		{"gemini retrydelay detail", "retryDelay:59s", 59 * time.Second, true},
		{"no hint", "429 Too Many Requests", 0, false},
		{"empty", "", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ServerDelay(providerErr(tt.msg))
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

// Gemini's retryDelay detail must feed the delay just like any other hint, so
// the retry lands inside the window the server named rather than a backoff
// schedule that keeps hitting the same exhausted per-minute window.
func TestDelayPrefersGeminiServerHint(t *testing.T) {
	cfg := Config{MaxRetries: 5, InitialDelay: time.Second, MaxDelay: time.Minute}
	err := providerErr("Please retry in 59.440629838s.")

	if got := Delay(cfg, 0, err); got != 59*time.Second+440629838*time.Nanosecond {
		t.Errorf("Delay = %v, want the server's 59.44s", got)
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

// A transport that reads a 429 body directly sees Gemini's RetryInfo detail as
// raw JSON rather than as the Go-map rendering that reaches the session log.
// Both spellings have to parse, or the hint is lost exactly where it is most
// useful — on the OpenAI-compatible paths, whose SDK discards the body before
// the retry loop can classify it.
func TestServerDelayReadsJSONRetryInfo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		msg  string
		want time.Duration
	}{
		{
			name: "json retryDelay",
			msg:  `{"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"11s"}]}`,
			want: 11 * time.Second,
		},
		{
			name: "json retryDelay with spacing",
			msg:  `{"@type": "…/google.rpc.RetryInfo", "retryDelay": "59s"}`,
			want: 59 * time.Second,
		},
		{
			name: "go map rendering",
			msg:  "map[@type:type.googleapis.com/google.rpc.RetryInfo retryDelay:11s]",
			want: 11 * time.Second,
		},
		{
			name: "prose alongside the quota",
			msg: "Quota exceeded for metric: generativelanguage.googleapis.com/" +
				"generate_content_paid_tier_input_token_count, limit: 2000000, " +
				"model: gemini-3.8-flash\\nPlease retry in 10.419145242s.",
			want: 10419145242 * time.Nanosecond,
		},
	}
	for _, tt := range tests {
		got, ok := ServerDelay(providerErr(tt.msg))
		if !ok {
			t.Errorf("%s: ServerDelay found no hint in %q", tt.name, tt.msg)
			continue
		}
		if got != tt.want {
			t.Errorf("%s: ServerDelay = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// A quota 429 that names a window is retryable even though its prose reads
// like plan exhaustion — the Gemini per-minute token limit reuses that wording
// verbatim. This is the classification the JSON form above now also reaches.
func TestJSONRetryInfoMakesQuotaErrorTransient(t *testing.T) {
	t.Parallel()
	err := providerErr(`429: You exceeded your current quota, please check your plan and ` +
		`billing details. {"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"11s"}`)
	if IsTerminal(err) {
		t.Error("a quota error naming a retry window was classified terminal")
	}
	if !IsTransient(err) {
		t.Error("a quota error naming a retry window was not classified transient")
	}
}
