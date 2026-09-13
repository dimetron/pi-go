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
	pm, ok = OllamaCloudCost("deepseek-v4-flash:0731")
	if !ok {
		t.Fatal("OllamaCloudCost(deepseek-v4-flash:0731) not found")
	}
	if pm.Input != 0.22 || pm.Output != 0.66 {
		t.Errorf("OllamaCloudCost(deepseek-v4-flash:0731) = %v/%v, want 0.22/0.66", pm.Input, pm.Output)
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
	// Peak rates apply to the models the peak table lists.
	pm, ok := OllamaCloudPeakCost("deepseek-v4-flash")
	if !ok {
		t.Fatal("OllamaCloudPeakCost(deepseek-v4-flash) not found")
	}
	if pm.Input != 0.44 || pm.Output != 1.32 {
		t.Errorf("OllamaCloudPeakCost(deepseek-v4-flash) = %v/%v, want 0.44/1.32", pm.Input, pm.Output)
	}
	// Models outside the peak table are not found there but price normally.
	if _, ok := OllamaCloudPeakCost("glm-5.3"); ok {
		t.Error("OllamaCloudPeakCost(glm-5.3) should not be found: it is not in the peak table")
	}
	if _, ok := OllamaCloudCost("glm-5.3"); !ok {
		t.Error("OllamaCloudCost(glm-5.3) should still be found")
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
