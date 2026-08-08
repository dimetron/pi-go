package guardrail

import (
	"strings"
	"testing"
)

func TestUsage_FreshInputTokens(t *testing.T) {
	tests := []struct {
		name  string
		usage Usage
		want  int64
	}{
		{"no cache means every prompt token is fresh", Usage{InputTokens: 1000}, 1000},
		{"partial cache", Usage{InputTokens: 1000, CachedInputTokens: 400}, 600},
		{"fully cached", Usage{InputTokens: 1000, CachedInputTokens: 1000}, 0},
		{"cached exceeding input never goes negative", Usage{InputTokens: 100, CachedInputTokens: 500}, 0},
		{"zero value", Usage{}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.usage.FreshInputTokens(); got != tc.want {
				t.Errorf("FreshInputTokens() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestUsage_CacheHitRate(t *testing.T) {
	tests := []struct {
		name  string
		usage Usage
		want  float64
	}{
		{"half cached", Usage{InputTokens: 1000, CachedInputTokens: 500}, 50},
		{"fully cached", Usage{InputTokens: 200, CachedInputTokens: 200}, 100},
		{"no hits", Usage{InputTokens: 200}, 0},
		{"no prompt tokens returns 0 rather than dividing by zero", Usage{CachedInputTokens: 50}, 0},
		{"zero value", Usage{}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.usage.CacheHitRate(); got != tc.want {
				t.Errorf("CacheHitRate() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTracker_CachedTokensAndHitRateToday(t *testing.T) {
	tr := NewWithPath(0, "")

	if got := tr.CachedTokensToday(); got != 0 {
		t.Errorf("fresh tracker CachedTokensToday() = %d, want 0", got)
	}
	if got := tr.CacheHitRateToday(); got != 0 {
		t.Errorf("fresh tracker CacheHitRateToday() = %v, want 0", got)
	}

	if err := tr.AddWithCache(1000, 100, 250); err != nil {
		t.Fatalf("AddWithCache: %v", err)
	}
	if got := tr.CachedTokensToday(); got != 250 {
		t.Errorf("CachedTokensToday() = %d, want 250", got)
	}
	if got := tr.CacheHitRateToday(); got != 25 {
		t.Errorf("CacheHitRateToday() = %v, want 25", got)
	}

	// A second request accumulates rather than replacing.
	if err := tr.AddWithCache(1000, 100, 750); err != nil {
		t.Fatalf("AddWithCache: %v", err)
	}
	if got := tr.CachedTokensToday(); got != 1000 {
		t.Errorf("CachedTokensToday() = %d, want 1000", got)
	}
	if got := tr.CacheHitRateToday(); got != 50 {
		t.Errorf("CacheHitRateToday() = %v, want 50", got)
	}
}

func TestTracker_BodyTokens(t *testing.T) {
	tr := NewWithPath(0, "")

	// Nothing recorded yet.
	if got := tr.BodyTokens(); got != 0 {
		t.Errorf("fresh tracker BodyTokens() = %d, want 0", got)
	}

	// The first prompt establishes the cached prefix baseline; the body is
	// whatever accumulates past it.
	tr.SetLastPromptTokens(10_000)
	prefix := tr.CachePrefixTokens()
	if got, want := tr.BodyTokens(), 10_000-prefix; got != want {
		t.Errorf("BodyTokens() = %d, want %d (prompt 10000 - prefix %d)", got, want, prefix)
	}
}

func TestTracker_BodyTokensNeverNegative(t *testing.T) {
	// A prompt smaller than the established prefix (the window was rebuilt
	// under us) must report an empty body, not a negative one.
	tr := NewWithPath(0, "")
	tr.SetLastPromptTokens(50_000)
	tr.SetLastPromptTokens(10)

	if got := tr.BodyTokens(); got < 0 {
		t.Errorf("BodyTokens() = %d, want >= 0", got)
	}
}

func TestTracker_ResetContextWindow(t *testing.T) {
	tr := NewWithPath(0, "")
	tr.SetContextWindowSize(200_000)
	tr.SetLastPromptTokens(50_000)

	tr.ResetContextWindow()

	if got := tr.LastPromptTokens(); got != 0 {
		t.Errorf("LastPromptTokens() = %d after reset, want 0", got)
	}
	if got := tr.CachePrefixTokens(); got != 0 {
		t.Errorf("CachePrefixTokens() = %d after reset, want 0", got)
	}
	if got := tr.LastCachedTokens(); got != 0 {
		t.Errorf("LastCachedTokens() = %d after reset, want 0", got)
	}
	if got := tr.BodyTokens(); got != 0 {
		t.Errorf("BodyTokens() = %d after reset, want 0", got)
	}
	// The window size is a model property, not per-window state, so it
	// survives the reset.
	if got := tr.ContextWindowSize(); got != 200_000 {
		t.Errorf("ContextWindowSize() = %d after reset, want it preserved at 200000", got)
	}
}

func TestFormatCacheSuffix(t *testing.T) {
	tests := []struct {
		name  string
		usage Usage
		want  string
	}{
		{
			name:  "no prompt tokens yet says nothing",
			usage: Usage{},
			want:  "",
		},
		{
			name:  "a single uncached request says nothing (too early to judge)",
			usage: Usage{InputTokens: 1000, Requests: 1},
			want:  "",
		},
		{
			name:  "repeated requests with no hits is worth reporting",
			usage: Usage{InputTokens: 1000, Requests: 2},
			want:  " · cache: no hits",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatCacheSuffix(tc.usage); got != tc.want {
				t.Errorf("formatCacheSuffix() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatCacheSuffix_ReportsHits(t *testing.T) {
	got := formatCacheSuffix(Usage{InputTokens: 10_000, CachedInputTokens: 7_500, Requests: 3})

	for _, want := range []string{"7.5k read", "75% of input", "2.5k fresh"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatCacheSuffix() = %q, want it to contain %q", got, want)
		}
	}
}
