package provider

import (
	"net/http"
	"testing"

	"github.com/dimetron/pi-go/internal/ratelimit"
)

// BuildTransport returns nil when nothing needs customizing, which tells the
// caller to leave the SDK's own client alone. Pacing has to defeat that, or a
// configured limit is silently dropped for every provider that passes no other
// transport option.
func TestBuildTransportInstallsPacing(t *testing.T) {
	transport, err := BuildTransport(&LLMOptions{
		RateLimit:      ratelimit.Limits{InputTokensPerMinute: 1_000_000},
		RateLimitScope: "test|build-transport|",
	})
	if err != nil {
		t.Fatalf("BuildTransport: %v", err)
	}
	if _, ok := transport.(*ratelimit.Transport); !ok {
		t.Fatalf("BuildTransport = %T, want a *ratelimit.Transport", transport)
	}
}

// Pacing must sit outside the header injection, so the wait happens before the
// request is spent rather than after it has been decorated and traced.
func TestBuildTransportPacingIsOutermost(t *testing.T) {
	transport, err := BuildTransport(&LLMOptions{
		ExtraHeaders:   map[string]string{"X-Test": "1"},
		RateLimit:      ratelimit.Limits{RequestsPerMinute: 60},
		RateLimitScope: "test|outermost|",
	})
	if err != nil {
		t.Fatalf("BuildTransport: %v", err)
	}
	paced, ok := transport.(*ratelimit.Transport)
	if !ok {
		t.Fatalf("outermost transport = %T, want a *ratelimit.Transport", transport)
	}
	if _, ok := paced.Base.(*headerTransport); !ok {
		t.Fatalf("transport under the limiter = %T, want a *headerTransport", paced.Base)
	}
}

// No limits configured means no wrapper: BuildTransport must still be able to
// report "no customization needed" so the SDK client is left in place.
func TestBuildTransportNoPacingWhenUnlimited(t *testing.T) {
	transport, err := BuildTransport(&LLMOptions{})
	if err != nil {
		t.Fatalf("BuildTransport: %v", err)
	}
	if transport != nil {
		t.Fatalf("BuildTransport with no options = %T, want nil", transport)
	}
}

func TestGeminiNeedsHTTPClientForPacing(t *testing.T) {
	if !geminiNeedsHTTPClient(&LLMOptions{RateLimit: ratelimit.Limits{InputTokensPerMinute: 1000}}) {
		t.Fatal("gemini would build its client without the pacing transport")
	}
	if geminiNeedsHTTPClient(&LLMOptions{}) {
		t.Fatal("gemini asked for a custom client with nothing to customize")
	}
}

// The scope is what makes concurrent clients share one bucket, so NewLLM has
// to fill it in even when the caller did not.
func TestNewLLMFillsRateLimitScope(t *testing.T) {
	opts := &LLMOptions{}
	// The provider is deliberately unsupported: NewLLM sets the scope before
	// it dispatches, and this avoids constructing a real client.
	_, err := NewLLM(t.Context(), Info{Provider: "nope", Model: "m"}, "", "http://localhost:4000", "none", opts)
	if err == nil {
		t.Fatal("NewLLM accepted an unsupported provider")
	}
	want := ratelimit.ScopeFor("nope", "m", "http://localhost:4000")
	if opts.RateLimitScope != want {
		t.Fatalf("RateLimitScope = %q, want %q", opts.RateLimitScope, want)
	}
}

func TestNewLLMKeepsExplicitRateLimitScope(t *testing.T) {
	opts := &LLMOptions{RateLimitScope: "caller-chosen"}
	_, _ = NewLLM(t.Context(), Info{Provider: "nope", Model: "m"}, "", "", "none", opts)
	if opts.RateLimitScope != "caller-chosen" {
		t.Fatalf("RateLimitScope = %q, want the caller's value", opts.RateLimitScope)
	}
}

var _ http.RoundTripper = (*ratelimit.Transport)(nil)
