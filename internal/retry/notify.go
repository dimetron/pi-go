package retry

import (
	"context"
	"fmt"
	"time"
)

// Attempt describes a retry that is about to be made: the failure that
// triggered it, which retry this is, and how long the caller will wait first.
type Attempt struct {
	// Attempt is the 1-based number of the retry about to run.
	Attempt int
	// MaxRetries is the total retry budget, for "1/3"-style reporting.
	MaxRetries int
	// Delay is the pause before the retry is sent.
	Delay time.Duration
	// Err is the transient failure being retried.
	Err error
}

// String renders the attempt as a one-line notice suitable for a status line
// or a transcript warning, e.g.
//
//	Retrying in 3s (1/3): read tcp 10.5.50.62:58742->104.18.2.115:443: read: can't assign requested address
func (a Attempt) String() string {
	msg := "unknown error"
	if a.Err != nil {
		msg = a.Err.Error()
	}
	return fmt.Sprintf("Retrying in %s (%d/%d): %s",
		a.Delay.Round(100*time.Millisecond), a.Attempt, a.MaxRetries, msg)
}

// Notifier is told about each retry before the pause that precedes it. It is
// how a front-end shows "retrying…" while the provider re-sends a request
// that died underneath it; without one, retries are silent and a stalled
// stream is indistinguishable from a slow model.
type Notifier func(Attempt)

type notifierKey struct{}

// WithNotifier returns a context that carries fn to every retry loop run
// under it. Contexts are the only thing that travels from a front-end through
// the ADK runner down to the provider's HTTP call, which is why the hook rides
// on one rather than on a config struct.
func WithNotifier(ctx context.Context, fn Notifier) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, notifierKey{}, fn)
}

// Notify reports an imminent retry to the Notifier on ctx, if any. A nil ctx
// or a ctx without a notifier is a no-op, so retry loops can call this
// unconditionally.
func Notify(ctx context.Context, a Attempt) {
	if ctx == nil {
		return
	}
	if fn, ok := ctx.Value(notifierKey{}).(Notifier); ok && fn != nil {
		fn(a)
	}
}
