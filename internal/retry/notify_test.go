package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The failure from ~/.pi-go/sessions/260822-0917-d94e2-b868d: the stream's
// local socket went away (EADDRNOTAVAIL) and the turn died on the spot. Both
// the macOS and Linux spellings must be retryable, since a fresh dial picks a
// live address.
func TestIsTransientSocketGoneAway(t *testing.T) {
	for _, msg := range []string{
		"read tcp 10.5.50.62:58742->104.18.2.115:443: read: can't assign requested address",
		"dial tcp 104.18.2.115:443: connect: cannot assign requested address",
		"dial tcp 104.18.2.115:443: connect: network is unreachable",
		"dial tcp 104.18.2.115:443: connect: no route to host",
		"write tcp 10.5.50.62:58742->104.18.2.115:443: write: broken pipe",
		"read tcp 10.5.50.62:58742->104.18.2.115:443: read: connection aborted",
	} {
		if !IsTransient(errors.New(msg)) {
			t.Errorf("IsTransient(%q) = false, want true", msg)
		}
		if IsTerminal(errors.New(msg)) {
			t.Errorf("IsTerminal(%q) = true, want false", msg)
		}
	}
}

// An explicit schedule replaces the exponential backoff and reuses its last
// entry past the end, so a 3/5/7 schedule with a larger retry budget keeps
// waiting 7s rather than falling back to doubling.
func TestDelayExplicitSchedule(t *testing.T) {
	cfg := Config{
		MaxRetries:   5,
		InitialDelay: time.Second,
		MaxDelay:     time.Minute,
		Delays:       []time.Duration{3 * time.Second, 5 * time.Second, 7 * time.Second},
	}
	err := errors.New("503 service unavailable")
	for attempt, want := range map[int]time.Duration{
		0: 3 * time.Second,
		1: 5 * time.Second,
		2: 7 * time.Second,
		3: 7 * time.Second,
		9: 7 * time.Second,
	} {
		if got := Delay(cfg, attempt, err); got != want {
			t.Errorf("Delay(attempt %d) = %v, want %v", attempt, got, want)
		}
	}
}

func TestDelayExplicitScheduleClampedAndOverridden(t *testing.T) {
	cfg := Config{
		MaxRetries: 3,
		MaxDelay:   4 * time.Second,
		Delays:     []time.Duration{3 * time.Second, 5 * time.Second, 7 * time.Second},
	}
	if got := Delay(cfg, 2, errors.New("503")); got != 4*time.Second {
		t.Errorf("schedule entry above MaxDelay = %v, want the 4s cap", got)
	}
	// The server's own window still beats the schedule.
	if got := Delay(cfg, 0, providerErr("Please try again in 1s.")); got != time.Second {
		t.Errorf("server hint = %v, want 1s", got)
	}
}

func TestNotify(t *testing.T) {
	// No notifier: a no-op, including on a nil context.
	Notify(context.Background(), Attempt{Attempt: 1})
	Notify(nil, Attempt{Attempt: 1}) //nolint:staticcheck // nil ctx is the case under test

	var got []Attempt
	ctx := WithNotifier(context.Background(), func(a Attempt) { got = append(got, a) })
	want := Attempt{Attempt: 2, MaxRetries: 3, Delay: 5 * time.Second, Err: errors.New("boom")}
	Notify(ctx, want)

	if len(got) != 1 {
		t.Fatalf("notifier called %d times, want 1", len(got))
	}
	if got[0].Attempt != 2 || got[0].MaxRetries != 3 || got[0].Delay != 5*time.Second || got[0].Err == nil {
		t.Errorf("notifier got %+v, want %+v", got[0], want)
	}

	// A nil notifier leaves the context alone rather than installing a hook
	// that panics when called.
	if WithNotifier(ctx, nil) != ctx {
		t.Error("WithNotifier(ctx, nil) should return ctx unchanged")
	}
}

func TestAttemptString(t *testing.T) {
	a := Attempt{
		Attempt:    1,
		MaxRetries: 3,
		Delay:      3 * time.Second,
		Err:        errors.New("read tcp 10.5.50.62:58742->104.18.2.115:443: read: can't assign requested address"),
	}
	const want = "Retrying in 3s (1/3): read tcp 10.5.50.62:58742->104.18.2.115:443: read: can't assign requested address"
	if got := a.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if got := (Attempt{Attempt: 1, MaxRetries: 1}).String(); got != "Retrying in 0s (1/1): unknown error" {
		t.Errorf("String() with nil err = %q", got)
	}
}
