package provider

import (
	"testing"
)

func TestOllamaCloudCost(t *testing.T) {
	// Exact base-name match.
	pm, ok := OllamaCloudCost("glm-5.3")
	if !ok {
		t.Fatal("OllamaCloudCost(glm-5.3) not found")
	}
	if pm.Input != 1.40 || pm.Output != 4.40 {
		t.Errorf("OllamaCloudCost(glm-5.3) = %v/%v, want 1.4/4.4", pm.Input, pm.Output)
	}
	if pm.CacheRead != 0.26 {
		t.Errorf("OllamaCloudCost(glm-5.3) CacheRead = %v, want 0.26", pm.CacheRead)
	}
	// Prefix match: API IDs with a size or date tag resolve to the base entry.
	pm, ok = OllamaCloudCost("deepseek-v4-pro:0813")
	if !ok {
		t.Fatal("OllamaCloudCost(deepseek-v4-pro:0813) not found")
	}
	if pm.Input != 0.66 || pm.Output != 1.98 {
		t.Errorf("OllamaCloudCost(deepseek-v4-pro:0813) = %v/%v, want 0.66/1.98", pm.Input, pm.Output)
	}
	pm, ok = OllamaCloudCost("gemma4:31b")
	if !ok {
		t.Fatal("OllamaCloudCost(gemma4:31b) not found")
	}
	if pm.Input != 0.14 || pm.Output != 0.40 {
		t.Errorf("OllamaCloudCost(gemma4:31b) = %v/%v, want 0.14/0.4", pm.Input, pm.Output)
	}
	// A model absent from the page is not found.
	if _, ok := OllamaCloudCost("llama3:latest"); ok {
		t.Error("OllamaCloudCost(llama3:latest) should not be found")
	}
	if _, ok := OllamaCloudCost(""); ok {
		t.Error("OllamaCloudCost(\"\") should not be found")
	}
}

func TestOllamaCloudPeakCost(t *testing.T) {
	// Peak rates apply to the models the page discounts with an "(Off-Peak)"
	// row: the plain row is the 12:00-18:00 UTC weekday rate.
	pm, ok := OllamaCloudPeakCost("deepseek-v4-pro")
	if !ok {
		t.Fatal("OllamaCloudPeakCost(deepseek-v4-pro) not found")
	}
	if pm.Input != 1.32 || pm.Output != 3.96 {
		t.Errorf("OllamaCloudPeakCost(deepseek-v4-pro) = %v/%v, want 1.32/3.96", pm.Input, pm.Output)
	}
	// A model the page bills the same rate all day is not in the peak table,
	// but it still prices normally.
	if _, ok := OllamaCloudPeakCost("glm-5.3"); ok {
		t.Error("OllamaCloudPeakCost(glm-5.3) should not be found: it has no separate peak rate")
	}
	if _, ok := OllamaCloudCost("glm-5.3"); !ok {
		t.Error("OllamaCloudCost(glm-5.3) should still be found")
	}
	// The discounted rate must be the one stored for normal pricing: if the
	// scraper's row labels invert, this is the assertion that catches it.
	peak, _ := OllamaCloudPeakCost("deepseek-v4-pro")
	off, _ := OllamaCloudCost("deepseek-v4-pro")
	if !(off.Input < peak.Input) {
		t.Errorf("off-peak input %v should be cheaper than peak %v", off.Input, peak.Input)
	}
}

func TestOllamaCloudCostUnpricedCache(t *testing.T) {
	// The page renders "-" for some cache columns; those decode to zero, not a
	// wrong number.
	pm, ok := OllamaCloudCost("nemotron-3-nano")
	if !ok {
		t.Fatal("OllamaCloudCost(nemotron-3-nano) not found")
	}
	if pm.CacheRead != 0 {
		t.Errorf("nemotron-3-nano CacheRead = %v, want 0 (page shows \"-\")", pm.CacheRead)
	}
	if pm.Input != 0.06 || pm.Output != 0.24 {
		t.Errorf("nemotron-3-nano = %v/%v, want 0.06/0.24", pm.Input, pm.Output)
	}
}

func TestOllamaCloudSnapshotCoversAPI(t *testing.T) {
	// Every cloud model the pricing page lists must carry input and output
	// rates: a row with either missing means the scraper mis-parsed.
	for id, r := range ollamaCloudPricingParsed.Models {
		if r.Input == nil || r.Output == nil {
			t.Errorf("snapshot model %q is missing input or output", id)
		}
	}
	if len(ollamaCloudPricingParsed.Models) == 0 {
		t.Error("embedded ollama-cloud snapshot is empty")
	}
	if len(ollamaCloudPricingParsed.Peak) == 0 {
		t.Error("embedded ollama-cloud peak table is empty")
	}
}
