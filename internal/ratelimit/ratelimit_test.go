package ratelimit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLimitsEnabled(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   Limits
		want bool
	}{
		{"zero", Limits{}, false},
		{"requests only", Limits{RequestsPerMinute: 10}, true},
		{"tokens only", Limits{InputTokensPerMinute: 1000}, true},
		{"both", Limits{RequestsPerMinute: 10, InputTokensPerMinute: 1000}, true},
	}
	for _, tt := range tests {
		if got := tt.in.Enabled(); got != tt.want {
			t.Errorf("%s: Enabled() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// A nil *Limiter is how "no pacing configured" is spelled at call sites, so it
// has to be usable rather than a panic waiting to happen.
func TestNilLimiterIsNoop(t *testing.T) {
	t.Parallel()
	var lim *Limiter
	if got := New(Limits{}); got != nil {
		t.Fatalf("New(zero Limits) = %v, want nil", got)
	}
	if err := lim.Wait(context.Background(), 1_000_000); err != nil {
		t.Fatalf("nil Limiter Wait: %v", err)
	}
	lim.Backoff(time.Hour) // must not panic
}

func TestLimiterAdmitsWithinBudget(t *testing.T) {
	t.Parallel()
	lim := New(Limits{InputTokensPerMinute: 1000})

	start := time.Now()
	if err := lim.Wait(context.Background(), 1000); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("a full burst should be admitted immediately, waited %v", elapsed)
	}
}

// The point of the whole package: once the minute's budget is spent, the next
// request waits instead of being sent and rejected.
func TestLimiterBlocksOnceBudgetSpent(t *testing.T) {
	t.Parallel()
	lim := New(Limits{InputTokensPerMinute: 1000})
	if err := lim.Wait(context.Background(), 1000); err != nil {
		t.Fatalf("first Wait: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := lim.Wait(ctx, 1000); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second Wait err = %v, want context.DeadlineExceeded", err)
	}
}

func TestLimiterBlocksOnRequestBudget(t *testing.T) {
	t.Parallel()
	lim := New(Limits{RequestsPerMinute: 2})
	for i := range 2 {
		if err := lim.Wait(context.Background(), 0); err != nil {
			t.Fatalf("Wait %d: %v", i, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := lim.Wait(ctx, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("third Wait err = %v, want context.DeadlineExceeded", err)
	}
}

// The regression this package was reworked for: a second full burst, sent a
// minute after the first, must be held rather than admitted. The server's
// rolling window still counts the first burst when the second lands, so a
// token bucket with a full-window burst would over-spend and 429. The rolling
// window delays the second burst until the first falls out.
func TestLimiterHoldsSecondFullBurst(t *testing.T) {
	t.Parallel()
	lim := New(Limits{InputTokensPerMinute: 1000})
	if err := lim.Wait(context.Background(), 1000); err != nil {
		t.Fatalf("first burst Wait: %v", err)
	}

	// A second full burst immediately must wait for the first to fall out of
	// the window, not be admitted.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := lim.Wait(ctx, 1000); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second full burst err = %v, want it held by the rolling window", err)
	}
}

// This is the case that distinguishes the rolling window from the old token
// bucket. A token bucket refills continuously, so at t=59s it has ~983 tokens
// back and would admit a 100-token request. The rolling window still counts
// the full 1000-token burst at t=59, so it must reject the same request. The
// old bucket would pass this test; the rolling window is what makes it fail.
func TestLimiterRollingWindowRejectsPartialChargeBeforeExpiry(t *testing.T) {
	t.Parallel()
	lim := New(Limits{InputTokensPerMinute: 1000})
	if err := lim.Wait(context.Background(), 1000); err != nil {
		t.Fatalf("first Wait: %v", err)
	}

	// At t=59s the full burst is still in the window, so even a small charge
	// must be held. A token bucket would have refilled and admitted it.
	lim.mu.Lock()
	lim.spend[0].at = time.Now().Add(-59 * time.Second)
	lim.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := lim.Wait(ctx, 100); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait(100) at t=59 err = %v, want it held by the rolling window", err)
	}
}

// A charge admitted at t=0 still counts against the window at t=59 and only
// falls out at t=60. This is the property a token bucket does not model.
func TestLimiterRollingWindowExpiry(t *testing.T) {
	t.Parallel()
	lim := New(Limits{InputTokensPerMinute: 1000})
	if err := lim.Wait(context.Background(), 1000); err != nil {
		t.Fatalf("first Wait: %v", err)
	}

	// Just before the window closes, the budget is still spent.
	lim.mu.Lock()
	lim.spend[0].at = time.Now().Add(-59 * time.Second)
	lim.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := lim.Wait(ctx, 1000); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait at t=59 err = %v, want it still held", err)
	}

	// Once the charge falls out of the window, the budget is free again.
	lim.mu.Lock()
	lim.spend[0].at = time.Now().Add(-61 * time.Second)
	lim.mu.Unlock()
	if err := lim.Wait(context.Background(), 1000); err != nil {
		t.Fatalf("Wait after window expiry: %v", err)
	}
}

// A canceled context must not record a charge for a request that will never be
// sent. The old rate.WaitN rejected a pre-canceled context; the rolling window
// must too, or it would charge budget for a request that never goes out.
func TestLimiterPreCanceledContextDoesNotCharge(t *testing.T) {
	t.Parallel()
	lim := New(Limits{InputTokensPerMinute: 1000})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := lim.Wait(ctx, 100); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait with canceled ctx err = %v, want context.Canceled", err)
	}

	lim.mu.Lock()
	defer lim.mu.Unlock()
	if len(lim.spend) != 0 {
		t.Fatalf("canceled request recorded %d charges, want none", len(lim.spend))
	}
}

// A large request queued behind a stream of small ones must not be starved.
// Without FIFO ordering, a small request arriving after the large one could
// consume the freed capacity and leapfrog it. Here the large request is queued
// first and the small one second; neither may be admitted while the window is
// full, and the small one must not slip ahead of the large one.
func TestLimiterFIFOOrderingPreventsStarvation(t *testing.T) {
	t.Parallel()
	lim := New(Limits{InputTokensPerMinute: 1000})
	if err := lim.Wait(context.Background(), 1000); err != nil {
		t.Fatalf("fill Wait: %v", err)
	}

	// Queue a large request first, then a small one. Both must be held while
	// the window is full.
	largeDone := make(chan error, 1)
	go func() {
		largeDone <- lim.Wait(context.Background(), 1000)
	}()
	// Give the large request a moment to enqueue before the small one.
	time.Sleep(20 * time.Millisecond)
	smallDone := make(chan error, 1)
	go func() {
		smallDone <- lim.Wait(context.Background(), 100)
	}()

	// Neither may complete while the window is full.
	select {
	case err := <-largeDone:
		t.Fatalf("large request admitted while window full: %v", err)
	case err := <-smallDone:
		t.Fatalf("small request admitted ahead of the queued large one: %v", err)
	case <-time.After(50 * time.Millisecond):
		// Both correctly held.
	}
}

// A request larger than the whole per-minute budget must still be sent: the
// server's 429 names the quota, whereas a local rate.WaitN error would say
// nothing useful and would never clear.
func TestLimiterClampsOversizeRequest(t *testing.T) {
	t.Parallel()
	lim := New(Limits{InputTokensPerMinute: 100})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := lim.Wait(ctx, 10_000_000); err != nil {
		t.Fatalf("oversize Wait = %v, want it to be admitted", err)
	}
}

func TestLimiterBackoffHoldsCallers(t *testing.T) {
	t.Parallel()
	lim := New(Limits{RequestsPerMinute: 1000})
	lim.Backoff(80 * time.Millisecond)

	start := time.Now()
	if err := lim.Wait(context.Background(), 0); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond {
		t.Fatalf("Wait returned after %v, want it held for the ~80ms cooldown", elapsed)
	}
}

func TestLimiterBackoffCancelable(t *testing.T) {
	t.Parallel()
	lim := New(Limits{RequestsPerMinute: 1000})
	lim.Backoff(30 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := lim.Wait(ctx, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait err = %v, want context.DeadlineExceeded", err)
	}
}

func TestLimiterBackoffKeepsLongestAndCaps(t *testing.T) {
	t.Parallel()
	lim := New(Limits{RequestsPerMinute: 1000})

	lim.Backoff(10 * time.Second)
	first := lim.cooldownRemaining()

	// A shorter hint from a second caller must not shorten the pause already
	// in force — the longest window any caller was told about is the true one.
	lim.Backoff(time.Second)
	if got := lim.cooldownRemaining(); got < first-time.Second {
		t.Fatalf("shorter Backoff shrank the cooldown: %v then %v", first, got)
	}

	// An absurd hint is clamped, so a misconfigured gateway cannot park a turn.
	lim.Backoff(24 * time.Hour)
	if got := lim.cooldownRemaining(); got > maxCooldown {
		t.Fatalf("cooldown %v exceeds the %v cap", got, maxCooldown)
	}
}

func TestLimiterBackoffIgnoresNonPositive(t *testing.T) {
	t.Parallel()
	lim := New(Limits{RequestsPerMinute: 1000})
	lim.Backoff(0)
	lim.Backoff(-time.Second)
	if got := lim.cooldownRemaining(); got > 0 {
		t.Fatalf("cooldownRemaining = %v, want none", got)
	}
}

// cooldownRemaining reports how much of a server-ordered pause is left. Test
// helper: production code only ever waits it out.
func (l *Limiter) cooldownRemaining() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return time.Until(l.coolUntil)
}

// Concurrent callers must share one bucket, because the quota is enforced on
// the account rather than on the client.
func TestSharedReturnsOneLimiterPerScope(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)

	limits := Limits{InputTokensPerMinute: 1000}
	a := Shared("gemini|gemini-3.8-flash|", limits)
	b := Shared("gemini|gemini-3.8-flash|", limits)
	if a == nil || a != b {
		t.Fatalf("Shared returned %p and %p for one scope, want the same limiter", a, b)
	}

	other := Shared("gemini|gemini-3.8-pro|", limits)
	if other == a {
		t.Fatal("distinct scopes share a limiter; separate model quotas would be pooled")
	}
}

// The first caller's limits win rather than a second bucket being created —
// two buckets over one quota would each permit the full rate.
func TestSharedKeepsFirstLimits(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)

	first := Shared("scope", Limits{InputTokensPerMinute: 1000})
	second := Shared("scope", Limits{InputTokensPerMinute: 9_000_000})
	if first != second {
		t.Fatal("Shared created a second bucket for one scope")
	}
	if first.tokenBurst != 1000 {
		t.Fatalf("tokenBurst = %d, want the first caller's 1000", first.tokenBurst)
	}
}

func TestSharedDisabledIsNil(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)

	if got := Shared("scope", Limits{}); got != nil {
		t.Fatalf("Shared with no limits = %v, want nil", got)
	}
}

func TestScopeForSeparatesEndpoints(t *testing.T) {
	t.Parallel()
	direct := ScopeFor("gemini", "gemini-3.8-flash", "")
	viaGateway := ScopeFor("agentgateway", "gemini-3.8-flash", "http://localhost:4000")
	if direct == viaGateway {
		t.Fatal("the same model through two endpoints must not share a budget")
	}
	if ScopeFor("gemini", "gemini-3.8-flash", "") != direct {
		t.Fatal("ScopeFor is not deterministic")
	}
}
