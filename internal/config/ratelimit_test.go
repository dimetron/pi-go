package config

import (
	"encoding/json"
	"testing"

	"github.com/dimetron/pi-go/internal/ratelimit"
)

func intPtr(n int) *int { return &n }

func TestResolveRateLimitsDefaults(t *testing.T) {
	t.Parallel()
	var cfg Config
	got := cfg.ResolveRateLimits("gemini", "gemini-3.8-flash")
	want := ratelimit.DefaultsFor("gemini", "gemini-3.8-flash")
	if got != want {
		t.Fatalf("ResolveRateLimits with no config = %+v, want the built-in %+v", got, want)
	}
	if got := cfg.ResolveRateLimits("anthropic", "claude-opus-5"); got.Enabled() {
		t.Fatalf("ResolveRateLimits for an unlisted provider = %+v, want unlimited", got)
	}
}

func TestResolveRateLimitsProviderOverride(t *testing.T) {
	t.Parallel()
	cfg := Config{RateLimits: map[string]RateLimitConfig{
		"gemini": {InputTokensPerMinute: intPtr(250_000)},
	}}
	got := cfg.ResolveRateLimits("gemini", "gemini-3.8-flash")
	if got.InputTokensPerMinute != 250_000 {
		t.Fatalf("InputTokensPerMinute = %d, want the configured 250000", got.InputTokensPerMinute)
	}
}

// An explicit 0 has to mean "unlimited", which is why the fields are pointers:
// with plain ints it would be indistinguishable from "not configured" and the
// built-in default would silently override the user.
func TestResolveRateLimitsExplicitZeroDisables(t *testing.T) {
	t.Parallel()
	cfg := Config{RateLimits: map[string]RateLimitConfig{
		"gemini": {InputTokensPerMinute: intPtr(0)},
	}}
	if got := cfg.ResolveRateLimits("gemini", "gemini-3.8-flash"); got.Enabled() {
		t.Fatalf("an explicit 0 left pacing on: %+v", got)
	}
}

// Setting one field must not drop the default under the other. A user adding
// an RPM cap is tightening the limits; silently losing the token budget would
// loosen them.
func TestResolveRateLimitsMergesPerField(t *testing.T) {
	t.Parallel()
	cfg := Config{RateLimits: map[string]RateLimitConfig{
		"gemini": {RequestsPerMinute: intPtr(30)},
	}}
	got := cfg.ResolveRateLimits("gemini", "gemini-3.8-flash")
	if got.RequestsPerMinute != 30 {
		t.Fatalf("RequestsPerMinute = %d, want 30", got.RequestsPerMinute)
	}
	want := ratelimit.DefaultsFor("gemini", "gemini-3.8-flash").InputTokensPerMinute
	if got.InputTokensPerMinute != want {
		t.Fatalf("InputTokensPerMinute = %d, want the default %d to survive", got.InputTokensPerMinute, want)
	}
}

func TestResolveRateLimitsWildcard(t *testing.T) {
	t.Parallel()
	cfg := Config{RateLimits: map[string]RateLimitConfig{
		"*": {RequestsPerMinute: intPtr(12)},
	}}
	if got := cfg.ResolveRateLimits("anthropic", "claude-opus-5"); got.RequestsPerMinute != 12 {
		t.Fatalf("RequestsPerMinute = %d, want the wildcard's 12", got.RequestsPerMinute)
	}
}

// The provider's own entry is more specific than "*" and must win.
func TestResolveRateLimitsProviderBeatsWildcard(t *testing.T) {
	t.Parallel()
	cfg := Config{RateLimits: map[string]RateLimitConfig{
		"*":      {RequestsPerMinute: intPtr(12), InputTokensPerMinute: intPtr(100)},
		"gemini": {RequestsPerMinute: intPtr(60)},
	}}
	got := cfg.ResolveRateLimits("gemini", "gemini-3.8-flash")
	if got.RequestsPerMinute != 60 {
		t.Fatalf("RequestsPerMinute = %d, want the provider entry's 60", got.RequestsPerMinute)
	}
	if got.InputTokensPerMinute != 100 {
		t.Fatalf("InputTokensPerMinute = %d, want the wildcard's 100 to still apply", got.InputTokensPerMinute)
	}
}

func TestResolveRateLimitsClampsNegative(t *testing.T) {
	t.Parallel()
	cfg := Config{RateLimits: map[string]RateLimitConfig{
		"gemini": {RequestsPerMinute: intPtr(-5), InputTokensPerMinute: intPtr(-1)},
	}}
	if got := cfg.ResolveRateLimits("gemini", "gemini-3.8-flash"); got.Enabled() {
		t.Fatalf("negative limits left pacing on: %+v", got)
	}
}

func TestRateLimitConfigRoundTrips(t *testing.T) {
	t.Parallel()
	const raw = `{"rateLimits":{"gemini":{"inputTokensPerMinute":1800000},"*":{"requestsPerMinute":0}}}`
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := cfg.ResolveRateLimits("gemini", "gemini-3.8-flash"); got.InputTokensPerMinute != 1_800_000 {
		t.Fatalf("InputTokensPerMinute = %d, want 1800000", got.InputTokensPerMinute)
	}

	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Config
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if back.ResolveRateLimits("gemini", "gemini-3.8-flash") != cfg.ResolveRateLimits("gemini", "gemini-3.8-flash") {
		t.Fatal("rateLimits did not survive a marshal round trip")
	}
}
