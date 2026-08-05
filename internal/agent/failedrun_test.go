package agent

import (
	"errors"
	"testing"

	"google.golang.org/adk/v2/session"
)

// TestFailedRun covers the pre-turn failure path. Run returns an iterator, so
// a setup error cannot simply be returned — it has to reach the caller as the
// sequence's first and only yield, with a nil event alongside it. A sequence
// that yielded nothing would look to `for ev, err := range ...` exactly like a
// turn that succeeded and produced no events, silently swallowing the failure.
func TestFailedRun(t *testing.T) {
	t.Parallel()

	t.Run("yields the error once with a nil event", func(t *testing.T) {
		t.Parallel()
		want := errors.New("pre-turn setup failed")

		var (
			events []*session.Event
			errs   []error
		)
		for ev, err := range failedRun(want) {
			events = append(events, ev)
			errs = append(errs, err)
		}

		if len(errs) != 1 {
			t.Fatalf("yielded %d times, want exactly 1", len(errs))
		}
		if !errors.Is(errs[0], want) {
			t.Fatalf("yielded error = %v, want %v", errs[0], want)
		}
		if events[0] != nil {
			t.Fatalf("yielded event = %+v, want nil", events[0])
		}
	})

	t.Run("respects an early break from the consumer", func(t *testing.T) {
		t.Parallel()
		// yield's return value must actually be honored; ignoring it would
		// keep pushing after the consumer has stopped listening.
		calls := 0
		for range failedRun(errors.New("boom")) {
			calls++
			break
		}
		if calls != 1 {
			t.Fatalf("consumer ran %d times, want 1", calls)
		}
	})

	t.Run("nil error still yields so the turn terminates", func(t *testing.T) {
		t.Parallel()
		got := 0
		for range failedRun(nil) {
			got++
		}
		if got != 1 {
			t.Fatalf("yielded %d times, want 1", got)
		}
	})
}
