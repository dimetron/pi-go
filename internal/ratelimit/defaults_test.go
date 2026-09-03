package ratelimit

import "testing"

func TestDefaultsForGemini(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		provider string
		model    string
		want     Limits
	}{
		// The path the 2M rejection actually arrived on: pi-go believed it was
		// talking to localhost:4000, and the gateway forwarded to Google.
		{"via agentgateway", "agentgateway", "gemini-3.8-flash", Limits{InputTokensPerMinute: geminiInputTokensPerMinute}},
		{"direct", "gemini", "gemini-3.8-flash", Limits{InputTokensPerMinute: geminiInputTokensPerMinute}},
		{"prefixed model", "gemini", "gemini/gemini-3.8-flash", Limits{InputTokensPerMinute: geminiInputTokensPerMinute}},
		{"mixed case", "gemini", "Gemini-3.8-Flash", Limits{InputTokensPerMinute: geminiInputTokensPerMinute}},

		// Resellers bill against their own quota, not Google's.
		{"openrouter reseller", "openrouter", "google/gemini-3.8-flash", Limits{}},
		{"opencode reseller", "opencode", "gemini-3.8-flash", Limits{}},

		{"non-gemini on the gateway", "agentgateway", "claude-opus-5", Limits{}},
		{"other provider", "anthropic", "claude-opus-5", Limits{}},
		{"empty", "", "", Limits{}},
	}
	for _, tt := range tests {
		if got := DefaultsFor(tt.provider, tt.model); got != tt.want {
			t.Errorf("%s: DefaultsFor(%q, %q) = %+v, want %+v", tt.name, tt.provider, tt.model, got, tt.want)
		}
	}
}

// The default has to sit under the quota Google's rejection names, not on it:
// pi-go estimates tokens from byte length and keeps its own bucket, so pacing
// exactly at the published figure turns every rounding error into a 429.
func TestGeminiDefaultLeavesHeadroom(t *testing.T) {
	t.Parallel()
	const observedQuota = 2_000_000
	if geminiInputTokensPerMinute >= observedQuota {
		t.Fatalf("default %d leaves no headroom under the observed %d quota",
			geminiInputTokensPerMinute, observedQuota)
	}
	if geminiInputTokensPerMinute < observedQuota/2 {
		t.Fatalf("default %d is far below the observed %d quota; it would throttle needlessly",
			geminiInputTokensPerMinute, observedQuota)
	}
}
