package config

import "github.com/dimetron/pi-go/internal/ratelimit"

// ResolveRateLimits returns the pacing budget for one provider/model pair.
//
// Resolution is per field, not per entry: an entry that only sets
// requestsPerMinute keeps the built-in input-token budget rather than silently
// switching it off. That matters because the token budget is the one that
// binds — a user adding an RPM cap is tightening the limits, and dropping the
// default underneath them would loosen them instead.
//
// Precedence for each field: the provider's own entry, then the "*" entry,
// then ratelimit.DefaultsFor.
func (c Config) ResolveRateLimits(providerName, modelName string) ratelimit.Limits {
	limits := ratelimit.DefaultsFor(providerName, modelName)
	for _, entry := range c.rateLimitEntries(providerName) {
		if entry.RequestsPerMinute != nil {
			limits.RequestsPerMinute = max(0, *entry.RequestsPerMinute)
		}
		if entry.InputTokensPerMinute != nil {
			limits.InputTokensPerMinute = max(0, *entry.InputTokensPerMinute)
		}
	}
	return limits
}

// rateLimitEntries returns the entries that apply to providerName, least
// specific first, so a caller applying them in order ends on the most specific.
func (c Config) rateLimitEntries(providerName string) []RateLimitConfig {
	if len(c.RateLimits) == 0 {
		return nil
	}
	var entries []RateLimitConfig
	if entry, ok := c.RateLimits[rateLimitWildcard]; ok {
		entries = append(entries, entry)
	}
	if entry, ok := c.RateLimits[providerName]; ok && providerName != rateLimitWildcard {
		entries = append(entries, entry)
	}
	return entries
}
