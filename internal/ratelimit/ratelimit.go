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
//
// The token budget is a *rolling window*, not a token bucket. The server's
// quota is a rolling 60-second window: a burst at t=0 still counts against the
// window at t=59 and only falls out at t=60. A token bucket with a full-window
// burst lets a client dump a whole window's worth instantly and again a minute
// later, which the server counts as two windows' worth in one — the bursty
// failure mode that produced mid-stream 429s after the original fix. The
// rolling window here delays a request until its input tokens fit in the last
// window, so a second full burst is held rather than sent and rejected.
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

// window is how long a token charge stays in the rolling window. It matches
// the server's own per-minute quota window.
const window = time.Minute

// spendEvent records one admitted request's token charge and when it was
// admitted, so the rolling window can drop it once it falls out.
type spendEvent struct {
	at     time.Time
	tokens int
}

// waiter is one request queued for token capacity. Waiters are served in FIFO
// order so a large request cannot be starved by a stream of small ones.
type waiter struct {
	charge int
	// ready is closed when this waiter becomes the head and should re-check
	// whether it can be admitted.
	ready chan struct{}
}

// Limiter paces requests against a rolling input-token window and/or a
// request-per-minute budget, and absorbs server-ordered cooldowns.
//
// The zero value is not usable; a nil *Limiter is, and does nothing — that is
// how "no limits configured" is represented at call sites.
type Limiter struct {
	// requests is nil when the RPM budget is unlimited.
	requests *rate.Limiter

	// tokenLimit is the max input tokens admitted in any window; tokenBurst
	// mirrors it so Wait can clamp an oversize request without reaching back
	// into the ledger. Both are zero when the token budget is unlimited.
	tokenLimit int
	tokenBurst int
	// spend holds the token charges still inside the window, oldest first.
	spend []spendEvent
	// waiters is the FIFO queue of requests waiting for token capacity.
	waiters []*waiter

	mu        sync.Mutex
	coolUntil time.Time
}

// New builds a Limiter for the given budget, or nil when nothing is limited.
//
// The RPM budget is a token bucket with a full-window burst, so a turn that
// legitimately fans out several small requests at once is not serialized. The
// token budget is a rolling window instead — see the package comment for why a
// bucket is the wrong shape for a per-minute quota.
func New(l Limits) *Limiter {
	if !l.Enabled() {
		return nil
	}
	lim := &Limiter{}
	if l.RequestsPerMinute > 0 {
		lim.requests = rate.NewLimiter(rate.Limit(float64(l.RequestsPerMinute)/60), l.RequestsPerMinute)
	}
	if l.InputTokensPerMinute > 0 {
		lim.tokenLimit = l.InputTokensPerMinute
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
	if l.tokenLimit > 0 && inputTokens > 0 {
		if err := l.waitTokenWindow(ctx, inputTokens); err != nil {
			return err
		}
	}
	return nil
}

// waitTokenWindow holds a request until its token charge fits in the rolling
// window, then records the charge.
//
// A request larger than the whole window can never be admitted, and blocking
// on it would turn an over-large prompt into a local failure with no useful
// text. Clamp its charge to a full window and let the server answer instead:
// its 429 names the quota and the model, which is the message the user
// actually needs. The window is then full, so later requests wait.
//
// Waiters are served in FIFO order. Without ordering, a large request queued
// behind a stream of small ones could be leapfrogged at every window expiry
// and starve; the queue guarantees each waiter is admitted before any that
// arrived after it.
func (l *Limiter) waitTokenWindow(ctx context.Context, inputTokens int) error {
	charge := inputTokens
	if charge > l.tokenLimit {
		charge = l.tokenLimit
	}

	// Fast path: no one is waiting and the charge fits, admit immediately.
	// A canceled context must not record a charge for a request that will
	// never be sent.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// Enqueue behind any existing waiters, or admit immediately when the queue
	// is empty and the charge fits.
	w := &waiter{charge: charge, ready: make(chan struct{})}
	l.mu.Lock()
	if len(l.waiters) == 0 && l.windowSumLocked() <= l.tokenLimit-charge {
		l.spend = append(l.spend, spendEvent{at: time.Now(), tokens: charge})
		l.mu.Unlock()
		return nil
	}
	l.waiters = append(l.waiters, w)
	head := len(l.waiters) == 1
	l.mu.Unlock()

	// Wait until this waiter is at the head of the queue, then try to admit.
	// The head waiter proceeds immediately; later waiters wait to be woken by
	// the waiter ahead of them being admitted or removed.
	for {
		if !head {
			select {
			case <-ctx.Done():
				l.removeWaiter(w)
				return ctx.Err()
			case <-w.ready:
				head = true
			}
		}

		l.mu.Lock()
		if l.waiters[0] != w {
			// Not head yet; a predecessor is still ahead. Wait to be woken.
			head = false
			l.mu.Unlock()
			continue
		}
		now := time.Now()
		l.dropExpiredLocked(now)
		if l.windowSumLocked() <= l.tokenLimit-charge {
			// Admit and dequeue.
			l.spend = append(l.spend, spendEvent{at: now, tokens: charge})
			l.waiters = l.waiters[1:]
			l.wakeNextLocked()
			l.mu.Unlock()
			return nil
		}
		// Head but the window is full. Wait until the oldest charge falls out,
		// then re-check.
		wait := l.spend[0].at.Add(window).Sub(now)
		l.mu.Unlock()
		if !sleepCtx(ctx, wait) {
			l.removeWaiter(w)
			return ctx.Err()
		}
	}
}

// windowSumLocked returns the sum of token charges still inside the window.
// Caller must hold l.mu.
func (l *Limiter) windowSumLocked() int {
	sum := 0
	for _, e := range l.spend {
		sum += e.tokens
	}
	return sum
}

// dropExpiredLocked removes charges that have fallen out of the window.
// Caller must hold l.mu.
func (l *Limiter) dropExpiredLocked(now time.Time) {
	cutoff := now.Add(-window)
	keep := 0
	for keep < len(l.spend) && l.spend[keep].at.Before(cutoff) {
		keep++
	}
	l.spend = l.spend[keep:]
}

// wakeNextLocked signals the next waiter in line, if any, that it may re-check.
// Caller must hold l.mu.
func (l *Limiter) wakeNextLocked() {
	if len(l.waiters) > 0 {
		close(l.waiters[0].ready)
	}
}

// removeWaiter drops w from the queue, waking the next waiter if w was head.
func (l *Limiter) removeWaiter(w *waiter) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, c := range l.waiters {
		if c == w {
			l.waiters = append(l.waiters[:i], l.waiters[i+1:]...)
			if i == 0 && len(l.waiters) > 0 {
				close(l.waiters[0].ready)
			}
			return
		}
	}
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
		if !sleepCtx(ctx, d) {
			return ctx.Err()
		}
	}
}

// sleepCtx waits for d, reporting false if ctx was canceled first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
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
