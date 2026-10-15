package cost

import (
	"math"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/provider"
)

func TestBilledOutput(t *testing.T) {
	tests := []struct {
		name                           string
		input, output, thoughts, total int64
		wantBilled, wantExtra          int64
	}{
		{"no reasoning", 1000, 100, 0, 1100, 100, 0},
		{"openai keeps reasoning inside output", 1000, 100, 50, 1100, 100, 0},
		{"gemini bills reasoning on top", 1000, 100, 50, 1150, 150, 50},
		{"missing total does not double-count", 1000, 100, 50, 0, 100, 0},
		{"gap larger than thoughts is capped", 100, 50, 10, 200, 60, 10},
		{"negative output is zero", 10, -5, 0, 10, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, extra := BilledOutput(tt.input, tt.output, tt.thoughts, tt.total)
			if got != tt.wantBilled || extra != tt.wantExtra {
				t.Errorf("BilledOutput() = (%d, %d), want (%d, %d)", got, extra, tt.wantBilled, tt.wantExtra)
			}
		})
	}
}

func TestSplitInput(t *testing.T) {
	tests := []struct {
		name          string
		input, cached int64
		fresh, cache  int64
	}{
		{"no cache", 1000, 0, 1000, 0},
		{"partial", 1000, 400, 600, 400},
		{"cached above input clamps", 100, 500, 0, 100},
		{"negatives", -10, -5, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fresh, cache := SplitInput(tt.input, tt.cached)
			if fresh != tt.fresh || cache != tt.cache {
				t.Errorf("SplitInput() = (%d, %d), want (%d, %d)", fresh, cache, tt.fresh, tt.cache)
			}
		})
	}
}

func TestPriceCall_BaseRates(t *testing.T) {
	price := provider.PricingModel{Input: 4, Output: 20, CacheRead: 0.5}
	// 750k fresh * $4 + 250k cached * $0.50 + 500k out * $20, per million.
	got := PriceCall(Call{
		Provider: "openai", Model: "gpt-test",
		Input: 1_000_000, Cached: 250_000, Output: 500_000,
	}, price, true, false)
	if !got.Priced {
		t.Fatal("expected a priced result")
	}
	if got.Fresh != 750_000 || got.Cached != 250_000 || got.Output != 500_000 {
		t.Errorf("tokens = fresh %d cached %d out %d", got.Fresh, got.Cached, got.Output)
	}
	near(t, got.USD, 3+0.125+10)
	if got.Tier || got.CacheAtInput || got.Explicit {
		t.Errorf("flags = tier %v cacheAtInput %v explicit %v, want all false", got.Tier, got.CacheAtInput, got.Explicit)
	}
}

func TestPriceCall_CacheWithoutRateUsesInputPrice(t *testing.T) {
	price := provider.PricingModel{Input: 2, Output: 10}
	got := PriceCall(Call{Input: 1_000_000, Cached: 500_000}, price, true, false)
	// 500k fresh and 500k cached, both at $2 / million.
	near(t, got.USD, 2)
	if !got.CacheAtInput {
		t.Error("cached tokens with no cache_read rate should be marked as billed at the input price")
	}
}

func TestPriceCall_ContextTier(t *testing.T) {
	price := provider.PricingModel{
		Input: 4, Output: 20,
		Tiers: []provider.PricingTier{
			{ContextOver: 100_000, Input: 6, Output: 24},
			{ContextOver: 272_000, Input: 8, Output: 30},
		},
	}
	below := PriceCall(Call{Input: 100_000, Output: 0}, price, true, false)
	if below.Tier {
		t.Error("a prompt equal to the threshold is not over it")
	}
	near(t, below.USD, 0.4)

	mid := PriceCall(Call{Input: 150_000}, price, true, false)
	if !mid.Tier {
		t.Error("expected the 100k tier")
	}
	near(t, mid.USD, 0.9)

	high := PriceCall(Call{Input: 300_000}, price, true, false)
	if !high.Tier {
		t.Error("expected the 272k tier")
	}
	near(t, high.USD, 2.4)

	// A summed counter is not one prompt. Negative Context keeps the base rate
	// even when the token total is past every tier.
	summed := PriceCall(Call{Input: 1_000_000, Context: -1}, price, true, false)
	if summed.Tier {
		t.Error("a negative context must not select a tier")
	}
	near(t, summed.USD, 4)
}

func TestPriceCall_ExplicitOverrideSkipsTiersAndAllowsZero(t *testing.T) {
	price := provider.PricingModel{
		Input: 9,
		Tiers: []provider.PricingTier{{ContextOver: 1, Input: 99}},
	}
	got := PriceCall(Call{Input: 1_000_000}, price, true, true)
	near(t, got.USD, 9)
	if got.Tier {
		t.Error("an override must not pick up catalog tiers")
	}
	if !got.Explicit || !got.Priced {
		t.Errorf("explicit=%v priced=%v", got.Explicit, got.Priced)
	}

	free := PriceCall(Call{Input: 1_000_000, Output: 500}, provider.PricingModel{}, true, true)
	if !free.Priced || free.USD != 0 {
		t.Errorf("explicit zero rate = priced %v usd %v, want priced at 0", free.Priced, free.USD)
	}
}

func TestPriceCall_UnknownModelIsUnpriced(t *testing.T) {
	got := PriceCall(Call{Provider: "openai", Model: "nope", Input: 1000, Output: 100}, provider.PricingModel{}, false, false)
	if got.Priced || got.USD != 0 {
		t.Errorf("unknown model priced=%v usd=%v", got.Priced, got.USD)
	}
	if got.Fresh != 1000 || got.Output != 100 {
		t.Errorf("unpriced result dropped tokens: %+v", got)
	}
}

func TestPriceCall_ReasoningAddsToOutputBill(t *testing.T) {
	price := provider.PricingModel{Output: 10}
	got := PriceCall(Call{Input: 1000, Output: 100, Thoughts: 50, Total: 1150}, price, true, false)
	if got.Output != 150 || got.Reasoning != 50 {
		t.Errorf("output=%d reasoning=%d, want 150 and 50", got.Output, got.Reasoning)
	}
	near(t, got.USD, 0.0015)
}

func TestSummarizeSkipsUnpriced(t *testing.T) {
	results := []Result{
		{USD: 1.5, Priced: true},
		{USD: 0, Priced: false},
		{USD: 0.25, Priced: true},
	}
	s := Summarize(results)
	if s.Requests != 3 || s.Unpriced != 1 {
		t.Errorf("summary = %+v", s)
	}
	near(t, s.USD, 1.75)
}

func TestGroupSortsAndSums(t *testing.T) {
	results := []Result{
		{Provider: "openai", Model: "b", Fresh: 10, USD: 1, Priced: true},
		{Provider: "anthropic", Model: "a", Fresh: 5, Cached: 2, Output: 3, Reasoning: 1, USD: 2, Priced: true, Tier: true},
		{Provider: "openai", Model: "b", Fresh: 7, USD: 0.5, Priced: true, CacheAtInput: true},
	}
	groups := ByModel(results)
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	if groups[0].Provider != "anthropic" || groups[0].Model != "a" {
		t.Errorf("first group = %s/%s, want anthropic/a", groups[0].Provider, groups[0].Model)
	}
	if !groups[0].Tier || groups[0].Requests != 1 {
		t.Errorf("anthropic group = %+v", groups[0])
	}
	b := groups[1]
	if b.Requests != 2 || b.Fresh != 17 || b.USD != 1.5 || !b.CacheAtInput {
		t.Errorf("openai group = %+v", b)
	}
}

func TestLookupOverrideBeatsCatalog(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	overrides := map[string]provider.PricingModel{
		"openai/gpt-5.6-sol": {Input: 9, Output: 1},
		"openai/gpt-5":       {Input: 3},
	}
	got, found, explicit := Lookup("OpenAI", "gpt-5.6-sol", overrides)
	if !found || !explicit || got.Input != 9 {
		t.Errorf("exact override = %+v found=%v explicit=%v", got, found, explicit)
	}
	// A dated id falls through to the longest override prefix when no exact key exists.
	dated, found, explicit := Lookup("openai", "gpt-5-extra", overrides)
	if !found || !explicit || dated.Input != 3 {
		t.Errorf("prefix override = %+v found=%v explicit=%v", dated, found, explicit)
	}
	// gpt-6-sol does not share the gpt-5 override prefix.
	catalog, found, explicit := Lookup("openai", "gpt-6-sol", overrides)
	if !found || explicit || catalog.Input != 2 {
		t.Errorf("catalog fallback = %+v found=%v explicit=%v", catalog, found, explicit)
	}
	if _, found, _ := Lookup("openai", "definitely-not-a-model", nil); found {
		t.Error("unknown model should not resolve")
	}
}

func TestLookupOllamaCloud(t *testing.T) {
	got, found, explicit := Lookup("ollama", "glm-5.3:cloud", nil)
	if !found || explicit || got.Input != 1.4 || got.Output != 4.4 {
		t.Errorf("ollama cloud = %+v found=%v explicit=%v", got, found, explicit)
	}
	if _, found, _ := Lookup("ollama", "local-model", nil); found {
		t.Error("a local ollama model has no catalog price")
	}
}

func TestRatesFromConfig(t *testing.T) {
	got := RatesFromConfig(map[string]config.ModelPrice{
		" OpenAI/GPT-5.6-sol ": {Input: 4, Output: 20, CacheRead: 0.4},
		"  ":                   {Input: 1},
	})
	if len(got) != 1 {
		t.Fatalf("rates = %d entries, want 1", len(got))
	}
	p := got["openai/gpt-5.6-sol"]
	if p.Input != 4 || p.Output != 20 || p.CacheRead != 0.4 {
		t.Errorf("rate = %+v", p)
	}
	if RatesFromConfig(nil) != nil {
		t.Error("nil config should produce no overrides")
	}
}

func TestFormatUSD(t *testing.T) {
	tests := []struct {
		v    float64
		want string
	}{
		{0, "$0.00"},
		{-1, "$0.00"},
		{4, "$4.00"},
		{13.125, "$13.125"},
		{0.125, "$0.125"},
		{0.000012, "$0.000012"},
		{1234.5, "$1234.50"},
	}
	for _, tt := range tests {
		if got := FormatUSD(tt.v); got != tt.want {
			t.Errorf("FormatUSD(%v) = %q, want %q", tt.v, got, tt.want)
		}
	}
}

func near(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("got %v, want %v", got, want)
	}
}
