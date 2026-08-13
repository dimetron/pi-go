package provider

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed modeldata/*.json
var modelCatalogFS embed.FS

type llmPricesCatalog struct {
	Vendor string `json:"vendor"`
	Models []struct {
		ID string `json:"id"`
	} `json:"models"`
}

var compatibilityModelAliases = map[string][]string{
	"anthropic": {
		"claude-3-opus-20240229",
		"claude-3-opus-latest",
		"claude-3-sonnet-20240229",
		"claude-3-sonnet-latest",
		"claude-3-haiku-20240307",
		"claude-3-haiku-latest",
		"claude-3-5-sonnet-20240620",
		"claude-3-5-sonnet-20241022",
		"claude-3-5-sonnet-latest",
		"claude-3-5-haiku-latest",
		"claude-3-7-sonnet-20250219",
		"claude-3-7-sonnet-latest",
		"claude-opus-4-0",
		"claude-opus-4-1-20250805",
		"claude-opus-5",
		"claude-opus-4-5-20251101",
		"claude-sonnet-4-0",
		"claude-sonnet-4-5",
		"claude-sonnet-4-6",
		"claude-sonnet-4-7",
		"claude-haiku-4-5",
		"claude-haiku-4-7",
		"claude-haiku-4-5-20251001",
	},
	"openai": {
		"gpt-5.5-codex",
		"gpt-5.4-codex",
		"gpt-5.3-codex",
		"gpt-5.3-codex-spark",
		"gpt-5.2-codex",
		"gpt-5.1-codex",
		"gpt-5.1-codex-max",
	},
}

func mustLoadKnownModels() map[string][]string {
	models, err := loadKnownModels()
	if err != nil {
		panic(err)
	}
	return models
}

func loadKnownModels() (map[string][]string, error) {
	models := map[string][]string{
		"anthropic": nil,
		"openai":    nil,
		"gemini": {
			// Latest stable Gemini tier: 3.7 Flash.
			"gemini-3.7-flash",
			// 3.6 Flash (previous stable).
			"gemini-3.6-flash",
			// 3.5 series.
			"gemini-3.5-flash",
			"gemini-3.5-flash-lite",
			"gemini-3.5-pro",
			// Stable Gemini 3.1 Flash-Lite (no -preview suffix).
			"gemini-3.1-flash-lite",
			// Gemini 3.x preview tiers: 3.1 Pro, 3 Flash, and 3.1 Flash-Lite.
			"gemini-3.1-pro-preview",
			"gemini-3.1-pro-preview-customtools",
			"gemini-3-flash-preview",
			"gemini-3.1-flash-lite-preview",
			// Stable Gemini 2.5 fallbacks.
			"gemini-2.5-pro",
			"gemini-2.5-flash",
			"gemini-2.5-flash-lite",
		},
		"mistral": {
			// Latest Mistral generalist tiers: Large 3, Medium 3.2,
			// Small 4, plus documented "latest" aliases.
			"mistral-large-2512",
			"mistral-large-latest",
			"mistral-medium-2603",
			"mistral-medium-latest",
			"mistral-small-2603",
			"mistral-small-latest",
			// Magistral reasoning models.
			"magistral-medium-latest",
			"magistral-small-latest",
			// Compatibility with older/specialized Mistral config names.
			"codestral",
			"pixtral",
			"ministral",
		},
		"xai": {
			// Grok 4.x generalist tiers. Matching is prefix-based, so the
			// undated base names below also accept xAI's "-latest" aliases
			// (grok-4.6-latest) and any future dated build of the same tier.
			"grok-4.6",
			"grok-4.5",
			"grok-4.3",
			// The 4.20 builds ship as separate reasoning and non-reasoning
			// model IDs; neither name is a prefix of the other, so both are
			// listed.
			"grok-4.20-0309-reasoning",
			"grok-4.20-0309-non-reasoning",
			"grok-4.20-multi-agent-0309",
			"grok-build-0.1",
		},
	}

	for _, provider := range []string{"anthropic", "openai"} {
		ids, err := loadLLMPricesModelIDs(provider)
		if err != nil {
			return nil, err
		}
		models[provider] = ids
	}
	for provider, aliases := range compatibilityModelAliases {
		models[provider] = append(models[provider], aliases...)
	}
	for provider, ids := range models {
		models[provider] = uniqueSorted(ids)
	}
	return models, nil
}

func loadLLMPricesModelIDs(provider string) ([]string, error) {
	path := fmt.Sprintf("modeldata/llm-prices-%s.json", provider)
	b, err := modelCatalogFS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read embedded model catalog %s: %w", path, err)
	}
	var catalog llmPricesCatalog
	if err := json.Unmarshal(b, &catalog); err != nil {
		return nil, fmt.Errorf("parse embedded model catalog %s: %w", path, err)
	}
	if catalog.Vendor != provider {
		return nil, fmt.Errorf("embedded model catalog %s vendor = %q, want %q", path, catalog.Vendor, provider)
	}

	ids := make([]string, 0, len(catalog.Models))
	for _, m := range catalog.Models {
		id := strings.ToLower(strings.TrimSpace(m.ID))
		if id == "" || !shouldIncludeKnownModel(provider, id) {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func shouldIncludeKnownModel(provider, id string) bool {
	switch provider {
	case "openai":
		return (strings.HasPrefix(id, "gpt-") || strings.HasPrefix(id, "chatgpt-")) &&
			!strings.HasPrefix(id, "gpt-image-")
	case "anthropic":
		return strings.HasPrefix(id, "claude-")
	default:
		return true
	}
}

func mustLoadContextWindowSizes() map[string]int64 {
	sizes, err := loadContextWindowSizes()
	if err != nil {
		panic(err)
	}
	return sizes
}

func mustLoadContextWindowSizesByProvider() map[string]map[string]int64 {
	sizes, err := loadContextWindowSizesByProvider()
	if err != nil {
		panic(err)
	}
	return sizes
}

// loadContextWindowSizesByProvider keeps each vendor's windows separate.
//
// The flattened form below cannot represent Azure: a deployment is named after
// the model it serves, but its window is whatever the deployment was
// provisioned with, and 15 of the 28 Azure deployments disagree with the same
// model ID on OpenAI's own API — gpt-5.1 is 272K on Azure against 1.05M
// upstream, gpt-5.6-luna is 1.05M against 272K, both directions. Merging them
// into one prefix map makes the winner depend on Go's map iteration order, so
// the same binary would compact at a different threshold run to run.
func loadContextWindowSizesByProvider() (map[string]map[string]int64, error) {
	b, err := modelCatalogFS.ReadFile("modeldata/context-windows.json")
	if err != nil {
		return nil, fmt.Errorf("read embedded context windows: %w", err)
	}
	var byProvider map[string]map[string]int64
	if err := json.Unmarshal(b, &byProvider); err != nil {
		return nil, fmt.Errorf("parse embedded context windows: %w", err)
	}
	out := make(map[string]map[string]int64, len(byProvider))
	for provider, models := range byProvider {
		provider = strings.ToLower(strings.TrimSpace(provider))
		if provider == "" {
			continue
		}
		cleaned := make(map[string]int64, len(models))
		for prefix, size := range models {
			prefix = strings.ToLower(strings.TrimSpace(prefix))
			if prefix == "" || size <= 0 {
				continue
			}
			cleaned[prefix] = size
		}
		if len(cleaned) > 0 {
			out[provider] = cleaned
		}
	}
	return out, nil
}

// loadContextWindowSizes flattens every vendor into one prefix map, for the
// provider-less ContextWindowSize path.
//
// Azure is deliberately excluded. Its deployment names collide with OpenAI's
// model IDs while carrying different windows, so folding it in would corrupt
// the OpenAI answers for a caller that never asked about Azure. Azure windows
// are reachable only through ContextWindowSizeFor.
func loadContextWindowSizes() (map[string]int64, error) {
	byProvider, err := loadContextWindowSizesByProvider()
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64)
	for provider, models := range byProvider {
		if provider == "azure" {
			continue
		}
		for prefix, size := range models {
			out[prefix] = size
		}
	}
	return out, nil
}

func uniqueSorted(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
