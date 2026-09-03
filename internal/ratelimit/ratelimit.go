// Package ratelimit paces outbound provider requests so pi-go stays inside a
// published quota instead of discovering it by being rejected.
//
// It exists because retrying is not pacing. pi-go's stream retry (see
// internal/provider/retry.go) re-sends a rejected request after 3s, 5s, 7s —
// pauses shorter than the per-minute window that rejected it, so the retries
// land inside the same exhausted window and burn the budget before the turn
// gives up. The failure this was written against: Gemini returns
//
//	Quota exceeded for metric:
//	generativelanguage.googleapis.com/generate_content_paid_tier_input_token_count,
//	limit: 2000000, model: gemini-3.8-flash
//
// after pi-go pushed 2,005,778 input tokens through 23 requests in 60 seconds.
// No request was individually unreasonable; their sum was.
//
// Two properties follow from that failure and shape the design:
//
//   - The binding limit is *input tokens* per minute, not requests per minute.
//     23 requests a minute is nothing; 2M tokens a minute is the wall. A
//     requests-per-minute cap tuned to stop it would throttle small requests
//     for no reason, so tokens are the primary budget and requests are an
//     optional secondary one.
//
//   - The budget is shared, not per-client. The three rejections above arrived
//     0.7s apart from concurrent callers — a subagent and the main turn, each
//     holding its own model.LLM. A limiter owned by one client would not have
//     seen the others' spend, so limiters are looked up from a process-wide
//     registry keyed by endpoint (see Shared).
package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limits describes how fast pi-go may send to one provider endpoint. A zero
// field means "no limit of this kind"; a zero Limits disables pacing entirely.
type Limits struct {
	// RequestsPerMinute caps request count. Secondary to the token budget:
	// useful for providers that publish an RPM ceiling, and for free tiers
	// where it is the limit that actually binds.
	RequestsPerMinute int

	// InputTokensPerMinute caps the input tokens sent per minute. This is the
	// limit that produces the 429 in practice — see the package comment.
	InputTokensPerMinute int
}

// Enabled reports whether these limits ask for any pacing at all.
func (l Limits) Enabled() bool {
	return l.RequestsPerMinute > 0 || l.InputTokensPerMinute > 0
}

// maxCooldown caps how long a server-ordered pause may park a request.
//
// A minute, for the same reason retry.DefaultConfig caps its backoff there:
// the limits that produce a retryable 429 are per-minute windows, so a longer
// wait cannot be needed to clear one, and honoring an unbounded Retry-After
// would let a misconfigured gateway freeze a turn indefinitely.
const maxCooldown = 60 * time.Second

// Limiter paces requests against a token-per-minute and/or
// request-per-minute budget, and absorbs server-ordered cooldowns.
//
// The zero value is not usable; a nil *Limiter is, and does nothing — that is
// how "no limits configured" is represented at call sites.
type Limiter struct {
	// requests and tokens are nil when that budget is unlimited.
	requests *rate.Limiter
	tokens   *rate.Limiter
	// tokenBurst mirrors tokens' burst so Wait can clamp to it without
	// reaching back into the rate.Limiter.
	tokenBurst int

	mu        sync.Mutex
	coolUntil time.Time
}

// New builds a Limiter for the given budget, or nil when nothing is limited.
//
// Each bucket's burst is a full window's worth rather than 1. Smoothing to a
// single unit would serialize an agent turn that legitimately fans out several
// small requests at once, and it would make the token bucket reject any
// request larger than one token. A full-window burst matches the shape of the
// server's own limit: spend it all at once if you like, then wait.
func New(l Limits) *Limiter {
	if !l.Enabled() {
		return nil
	}
	lim := &Limiter{}
	if l.RequestsPerMinute > 0 {
		lim.requests = rate.NewLimiter(rate.Limit(float64(l.RequestsPerMinute)/60), l.RequestsPerMinute)
	}
	if l.InputTokensPerMinute > 0 {
		lim.tokens = rate.NewLimiter(rate.Limit(float64(l.InputTokensPerMinute)/60), l.InputTokensPerMinute)
		lim.tokenBurst = l.InputTokensPerMinute
	}
	return lim
}

// Wait blocks until sending a request costing inputTokens is within budget, or
// until ctx is canceled.
//
// A nil Limiter returns immediately, so callers never have to branch on
// whether pacing is configured.
func (l *Limiter) Wait(ctx context.Context, inputTokens int) error {
	if l == nil {
		return nil
	}
	if err := l.waitCooldown(ctx); err != nil {
		return err
	}
	if l.requests != nil {
		if err := l.requests.Wait(ctx); err != nil {
			return waitErr(ctx, err)
		}
	}
	if l.tokens != nil && inputTokens > 0 {
		// Clamp to the burst. A request bigger than the whole per-minute
		// budget can never be admitted, and rate.WaitN reports that as an
		// error — which would turn an over-large prompt into a local failure
		// with no useful text. Drain the bucket and let the server answer
		// instead: its 429 names the quota and the model, which is the message
		// the user actually needs.
		n := inputTokens
		if n > l.tokenBurst {
			n = l.tokenBurst
		}
		if err := l.tokens.WaitN(ctx, n); err != nil {
			return waitErr(ctx, err)
		}
	}
	return nil
}

// waitErr translates a rate.Limiter refusal into the vocabulary the rest of
// pi-go already speaks.
//
// rate.Limiter refuses up front when the pause it would impose runs past the
// caller's deadline, and reports that as an error of its own —
// "rate: Wait(n=87208) would exceed context deadline". Surfacing that verbatim
// would be wrong twice over: it reads as a rate-limiter internal rather than
// as what happened, and it matches nothing in internal/retry's pattern lists,
// so a turn that could have been retried would be classified as an unknown
// failure and dropped. A deadline is what actually ran out, so say so.
func waitErr(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return fmt.Errorf("rate limit pacing: %w", context.DeadlineExceeded)
}

// Backoff records a pause the server asked for, holding back every caller
// sharing this limiter until it lapses.
//
// This is what makes a Retry-After actually change pi-go's behavior. Applying
// the hint only to the request that was rejected would leave the other
// concurrent callers on the same quota sending into the closed window — which
// is how three rejections arrived 0.7s apart in the failure this was written
// against. Extending an existing cooldown rather than replacing it keeps the
// longest hint any caller was given.
func (l *Limiter) Backoff(d time.Duration) {
	if l == nil || d <= 0 {
		return
	}
	if d > maxCooldown {
		d = maxCooldown
	}
	until := time.Now().Add(d)
	l.mu.Lock()
	defer l.mu.Unlock()
	if until.After(l.coolUntil) {
		l.coolUntil = until
	}
}

// waitCooldown blocks out any server-ordered pause still in force.
func (l *Limiter) waitCooldown(ctx context.Context) error {
	for {
		l.mu.Lock()
		until := l.coolUntil
		l.mu.Unlock()

		d := time.Until(until)
		if d <= 0 {
			return nil
		}
		timer := time.NewTimer(d)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			// Loop rather than return: a concurrent Backoff may have pushed
			// coolUntil further out while this caller was asleep.
		}
	}
}

var (
	registryMu sync.Mutex
	registry   = map[string]*Limiter{}
)

// Shared returns the process-wide Limiter for scope, creating it on first use.
//
// Every model.LLM aimed at the same endpoint must share one limiter, because
// the quota is enforced on the account, not on the client: the main turn, a
// subagent, and a title generation all spend from the same budget. scope is
// the identity of that budget — see ScopeFor.
//
// The first caller's limits win. A later caller asking for the same scope with
// different numbers gets the existing limiter rather than a second bucket,
// since two buckets over one quota would each permit the full rate.
func Shared(scope string, l Limits) *Limiter {
	if !l.Enabled() {
		return nil
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if lim, ok := registry[scope]; ok {
		return lim
	}
	lim := New(l)
	registry[scope] = lim
	return lim
}

// ScopeFor names the budget a request spends from.
//
// It is keyed by model as well as provider because that is how the quota is
// actually enforced: Gemini's rejection carries
// quotaId=GenerateContentPaidTierInputTokensPerModelPerMinute with
// quotaDimensions{model: gemini-3.8-flash}. Keying on the provider alone would
// pool two models that have separate budgets, throttling both to one's limit.
//
// The base URL is included so the same model reached through a local gateway
// and reached directly are not treated as one budget — they may well be
// different accounts.
func ScopeFor(provider, model, baseURL string) string {
	return provider + "|" + model + "|" + baseURL
}

// resetRegistry drops every shared limiter. Tests only: the registry is
// process-wide by design, which would otherwise leak state between cases.
func resetRegistry() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = map[string]*Limiter{}
}
