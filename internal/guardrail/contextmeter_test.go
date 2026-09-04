package guardrail_test

import (
	"testing"

	"github.com/dimetron/pi-go/internal/autocompact"
	"github.com/dimetron/pi-go/internal/guardrail"
)

// Auto-compaction reads the live context size through autocompact.ContextMeter
// rather than through this package, so that piagent — which may not import
// internal/guardrail, see TestPiagentStaysIsolated — can install the same hook.
// That indirection means nothing in the compiler forces the two halves to stay
// compatible; this assertion does.
var _ autocompact.ContextMeter = (*guardrail.Tracker)(nil)

// TestTrackerAndMeterAgreeOnBodyTokens pins the rule both implementations
// encode: the first request of a window establishes the cached prefix, and
// body tokens are what accumulated after it. Two entry points measuring the
// same session differently would compact at different points.
func TestTrackerAndMeterAgreeOnBodyTokens(t *testing.T) {
	t.Parallel()

	steps := []struct {
		desc   string
		prompt int32
		want   int64
	}{
		{desc: "first request establishes the prefix, so no body yet", prompt: 12_000, want: 0},
		{desc: "growth after the prefix is body", prompt: 30_000, want: 18_000},
		{desc: "further growth accumulates", prompt: 109_498, want: 97_498},
	}

	tracker := guardrail.NewWithPath(0, t.TempDir()+"/usage.json")
	meter := autocompact.NewMeter()

	for _, step := range steps {
		if err := tracker.AddWithCache(step.prompt, 100, 0); err != nil {
			t.Fatalf("%s: AddWithCache: %v", step.desc, err)
		}
		meter.Observe(int64(step.prompt))

		if got := tracker.BodyTokens(); got != step.want {
			t.Errorf("%s: tracker.BodyTokens() = %d, want %d", step.desc, got, step.want)
		}
		if got := meter.BodyTokens(); got != step.want {
			t.Errorf("%s: meter.BodyTokens() = %d, want %d", step.desc, got, step.want)
		}
	}

	// Compaction rewrites the window, so both must re-establish a prefix from
	// the next request rather than keeping the old one.
	tracker.SetLastPromptTokens(20_000)
	meter.SetLastPromptTokens(20_000)
	if got := tracker.BodyTokens(); got != 20_000 {
		t.Errorf("after compaction tracker.BodyTokens() = %d, want 20000", got)
	}
	if got := meter.BodyTokens(); got != 20_000 {
		t.Errorf("after compaction meter.BodyTokens() = %d, want 20000", got)
	}
}
