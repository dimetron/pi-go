package ratelimit

import "strings"

// geminiInputTokensPerMinute is the default input-token budget for Gemini.
//
// The number is not a guess: Google's own rejection names it.
//
//	quotaId:      GenerateContentPaidTierInputTokensPerModelPerMinute
//	quotaMetric:  generativelanguage.googleapis.com/generate_content_paid_tier_input_token_count
//	quotaValue:   2000000
//
// The 10% haircut off that 2,000,000 is the *only* safety margin in the
// pacing path, and it is deliberately the only one. pi-go's minute is a token
// bucket rather than the server's window, so the two ledgers drift even when
// the token count is right; pacing exactly at the published figure would
// convert every rounding error into a 429.
//
// The margin lives here rather than in ratelimit.bytesPerToken on purpose.
// That constant is a measurement — bytes per token, checked against what the
// gateway reports — so folding a fudge factor into it would stop it meaning
// what it says and make it impossible to re-measure. Anyone tightening the
// margin should move this number, not that one, and should not do both.
//
// It costs throughput nobody was using: the turn that triggered this spent
// 2,005,778 tokens in the minute it was rejected, so it was over the line by a
// quarter of a percent, not by a factor.
//
// Google no longer publishes per-model RPM/TPM tables on
// ai.google.dev/gemini-api/docs/rate-limits — it points at the AI Studio
// dashboard instead — so a per-tier table here would go stale unverifiably.
// Free-tier accounts have a much smaller budget than this default; they should
// set rateLimits in config, which is why the knob exists.
const geminiInputTokensPerMinute = 1_800_000

// DefaultsFor returns the built-in budget for a provider/model pair, or a zero
// Limits when pi-go has no evidence for one.
//
// Defaults are deliberately sparse. A limit invented for a provider whose
// quota has not been observed would either throttle a user who was fine or sit
// too high to help, and it would be indistinguishable in the config from a
// number that means something. Gemini is here because its rejection states the
// quota outright; everything else is unlimited until it earns an entry.
func DefaultsFor(provider, model string) Limits {
	if isGeminiTarget(provider, model) {
		return Limits{InputTokensPerMinute: geminiInputTokensPerMinute}
	}
	return Limits{}
}

// isGeminiTarget reports whether a request will be charged against Google's
// generativelanguage quota.
//
// The provider name alone is not enough. pi-go's "agentgateway" provider is a
// local OpenAI-compatible multiplexer that forwards gemini-* models straight
// to generativelanguage.googleapis.com — that is the path the 2M rejection
// arrived on, with pi-go believing it was talking to localhost:4000. So the
// model name decides, and the provider only limits which paths are considered.
//
// Aggregators that resell Gemini under their own quota (openrouter, opencode)
// are excluded on purpose: their limit is theirs, not Google's.
func isGeminiTarget(provider, model string) bool {
	switch provider {
	case "gemini", "agentgateway", "":
	default:
		return false
	}
	m := strings.ToLower(model)
	m = strings.TrimPrefix(m, "gemini/")
	return strings.HasPrefix(m, "gemini-") || m == "gemini"
}
