package cli

import (
	"encoding/json"
	"testing"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/provider"
)

// usageEvent builds an event carrying a usage block, the way a provider
// response arrives at the emitter.
func usageEvent(partial bool, prompt, candidates, cached, thoughts, total int32) *session.Event {
	ev := &session.Event{Author: "pi"}
	ev.Partial = partial
	ev.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:        prompt,
		CandidatesTokenCount:    candidates,
		CachedContentTokenCount: cached,
		ThoughtsTokenCount:      thoughts,
		TotalTokenCount:         total,
	}
	return ev
}

func TestUsageAccumulator_SumsAcrossResponses(t *testing.T) {
	u := &usageAccumulator{}
	u.add(usageEvent(false, 100, 20, 0, 0, 120))
	u.add(usageEvent(false, 200, 30, 0, 0, 230))

	got := u.snapshot()
	if got == nil {
		t.Fatal("snapshot() = nil, want usage")
	}
	if got.InputTokens != 300 || got.OutputTokens != 50 || got.TotalTokens != 350 {
		t.Errorf("got input=%d output=%d total=%d, want 300/50/350",
			got.InputTokens, got.OutputTokens, got.TotalTokens)
	}
}

// A streaming turn delivers partial deltas whose aggregate repeats the totals.
// Counting both would roughly double a run's reported cost.
func TestUsageAccumulator_SkipsPartialEvents(t *testing.T) {
	u := &usageAccumulator{}
	u.add(usageEvent(true, 100, 20, 0, 0, 120))  // partial delta
	u.add(usageEvent(false, 100, 20, 0, 0, 120)) // the aggregate

	got := u.snapshot()
	if got == nil {
		t.Fatal("snapshot() = nil, want usage")
	}
	if got.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100 (partial double-counted)", got.InputTokens)
	}
}

// The bug this test was written for: providers that leave TotalTokenCount unset
// reported total_tokens: 0 next to a nonzero input count.
func TestUsageAccumulator_TotalFallsBackWhenProviderOmitsIt(t *testing.T) {
	u := &usageAccumulator{}
	u.add(usageEvent(false, 19464, 61, 0, 12, 0))

	got := u.snapshot()
	if got.TotalTokens == 0 {
		t.Fatal("TotalTokens = 0 with nonzero input/output; provider omits total and the sum was not used")
	}
	if want := 19464 + 61 + 12; got.TotalTokens != want {
		t.Errorf("TotalTokens = %d, want %d", got.TotalTokens, want)
	}
}

func TestUsageAccumulator_NoUsageMeansNoField(t *testing.T) {
	u := &usageAccumulator{}
	u.add(&session.Event{Author: "pi"}) // no usage block at all

	if got := u.snapshot(); got != nil {
		t.Errorf("snapshot() = %+v, want nil so the JSON field is omitted", got)
	}
}

func TestUsageAccumulator_NilEventIsIgnored(t *testing.T) {
	u := &usageAccumulator{}
	u.add(nil)
	if got := u.snapshot(); got != nil {
		t.Errorf("snapshot() = %+v, want nil", got)
	}
}

// Cost is per response, so two priced responses must sum rather than the last
// overwriting the first. The provider is required: CostFor("", model) does not
// resolve, which is why the accumulator carries it.
func TestUsageAccumulator_PricesEachResponse(t *testing.T) {
	u := &usageAccumulator{model: "claude-sonnet-5", provider: "anthropic"}

	pm, ok := provider.CostFor("anthropic", "claude-sonnet-5")
	if !ok {
		t.Skip("claude-sonnet-5 is not in this host's pricing snapshot")
	}

	// Two responses of 1M input + 1M output each, no cache.
	u.add(usageEvent(false, 1_000_000, 1_000_000, 0, 0, 2_000_000))
	u.add(usageEvent(false, 1_000_000, 1_000_000, 0, 0, 2_000_000))

	got := u.snapshot()
	if got.ResponsesPriced != 2 {
		t.Fatalf("ResponsesPriced = %d, want 2", got.ResponsesPriced)
	}
	want := 2 * (pm.Input + pm.Output) // two million-token responses
	if diff := got.CostUSD - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("CostUSD = %v, want %v (per-response sum, not last-wins)", got.CostUSD, want)
	}
}

// Cached prompt tokens bill at the cache-read rate instead of the full input
// rate. Getting this backwards overstates a cached heavy run enormously — the
// eval corpus shows a 94% cache hit rate.
func TestUsageAccumulator_CachedTokensBillAtCacheRate(t *testing.T) {
	u := &usageAccumulator{model: "claude-sonnet-5", provider: "anthropic"}
	pm, ok := provider.CostFor("anthropic", "claude-sonnet-5")
	if !ok || pm.CacheRead == 0 {
		t.Skip("no cache-read rate for claude-sonnet-5 in this host's snapshot")
	}

	// 1M prompt of which 800k is cached, 0 output.
	u.add(usageEvent(false, 1_000_000, 0, 800_000, 0, 1_000_000))

	got := u.snapshot()
	want := (200_000*pm.Input + 800_000*pm.CacheRead) / 1e6
	if diff := got.CostUSD - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("CostUSD = %v, want %v (cached portion at the cache-read rate)", got.CostUSD, want)
	}
	uncached := (1_000_000 * pm.Input) / 1e6
	if got.CostUSD >= uncached {
		t.Errorf("CostUSD = %v is not below the fully-uncached %v; the cache discount was not applied",
			got.CostUSD, uncached)
	}
}

func TestUsageAccumulator_UnpricedModelReportsTokensNotCost(t *testing.T) {
	u := &usageAccumulator{model: "definitely-not-a-real-model-xyz"}
	u.add(usageEvent(false, 500, 50, 0, 0, 550))

	got := u.snapshot()
	if got == nil {
		t.Fatal("snapshot() = nil, want tokens even when unpriced")
	}
	if got.CostUSD != 0 || got.ResponsesPriced != 0 {
		t.Errorf("CostUSD = %v priced = %d, want 0/0 for an unpriced model",
			got.CostUSD, got.ResponsesPriced)
	}
	if got.InputTokens != 500 {
		t.Errorf("InputTokens = %d, want 500", got.InputTokens)
	}
}

// The wire contract Harbor and other consumers parse: usage rides on
// message_end, and everything else stays unchanged.
func TestJSONEvent_UsageSerializesOnMessageEndOnly(t *testing.T) {
	withUsage, err := json.Marshal(jsonEvent{
		Type:  "message_end",
		Usage: &jsonUsage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Type  string     `json:"type"`
		Usage *jsonUsage `json:"usage"`
	}
	if err := json.Unmarshal(withUsage, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Usage == nil || decoded.Usage.InputTokens != 10 {
		t.Errorf("usage did not round-trip: %+v", decoded.Usage)
	}

	plain, err := json.Marshal(jsonEvent{Type: "message_end"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(plain, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := raw["usage"]; present {
		t.Error(`"usage" present when there is no usage; it must be omitempty so existing consumers see no change`)
	}
}
