package piagent

import (
	"context"
	"fmt"
	"strings"

	adkmodel "google.golang.org/adk/v2/model"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/guardrail"
	"github.com/dimetron/pi-go/internal/provider"
)

// providerEnvVar names the environment variable a provider's key is read from,
// so a missing credential says which one to set.
func providerEnvVar(name string) string {
	switch name {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "azure":
		return "AZURE_OPENAI_API_KEY"
	case "gemini":
		return "GEMINI_API_KEY"
	default:
		return strings.ToUpper(name) + "_API_KEY"
	}
}

// keylessProviders are reachable without a credential: Gemini and Azure read
// theirs from ambient cloud configuration, and Ollama has none.
func keylessProvider(info provider.Info) bool {
	switch info.Provider {
	case "gemini", "ollama", "azure":
		return true
	}
	return info.Ollama
}

// resolveModel turns configuration plus the model/base-URL overrides into a
// validated provider descriptor and the credential to use with it.
func resolveModel(cfg config.Config, o options) (provider.Info, string, string, error) {
	// An explicit model stands on its own: an embedder that names one should
	// not need a configured default role to exist as well.
	modelName, providerName := o.modelName, ""
	if modelName == "" {
		var err error
		modelName, providerName, _, _, _, err = cfg.ResolveRole("default")
		if err != nil {
			return provider.Info{}, "", "", fmt.Errorf("resolving model role: %w", err)
		}
	}

	baseURLs := cfg.ResolveBaseURLs()
	baseURL := o.baseURL
	if baseURL == "" && providerName != "" {
		baseURL = baseURLs[providerName]
	}

	info, err := provider.ResolveWithBaseURL(modelName, baseURL)
	if err != nil {
		return provider.Info{}, "", "", fmt.Errorf("resolving model %q: %w", modelName, err)
	}
	if providerName != "" {
		info.Provider = providerName
		info.Custom = baseURL != ""
	}
	if baseURL == "" {
		if baseURL = baseURLs[info.Provider]; baseURL != "" {
			info.Custom = true
		}
	}
	if err := provider.ValidateModel(info); err != nil {
		return provider.Info{}, "", "", fmt.Errorf("model validation: %w", err)
	}

	apiKey := o.apiKey
	if apiKey == "" {
		apiKey = config.APIKeys()[info.Provider]
	}
	if apiKey == "" && baseURL == "" && !keylessProvider(info) {
		return provider.Info{}, "", "", fmt.Errorf("no API key found for provider %q (set %s, or pass WithAPIKey)", info.Provider, providerEnvVar(info.Provider))
	}
	if baseURL == "" && info.Ollama {
		if apiKey != "" {
			baseURL = "https://api.ollama.com"
		} else {
			baseURL = "http://localhost:11434"
		}
	}
	return info, apiKey, baseURL, nil
}

// contextWindow resolves the model's usable context, which the token tracker
// reports against. An explicit config value wins: the embedded catalog does not
// cover every provider's models.
func contextWindow(ctx context.Context, cfg config.Config, info provider.Info, baseURL string) int64 {
	if cfg.ContextWindow > 0 {
		return cfg.ContextWindow
	}
	size := provider.ContextWindowSizeFor(info.Provider, info.Model)
	if info.Ollama {
		if n := provider.OllamaContextWindowSize(ctx, baseURL, info.Model); n > 0 {
			return n
		}
	}
	return size
}

// buildLLM constructs the model from configuration and wraps it in the
// daily-token guardrail. An injected model ([WithLLM]) skips both: metering
// someone else's model is their decision.
func buildLLM(ctx context.Context, cfg config.Config, o options) (adkmodel.LLM, provider.Info, error) {
	if o.llm != nil {
		return o.llm, provider.Info{Model: o.llm.Name()}, nil
	}

	info, apiKey, baseURL, err := resolveModel(cfg, o)
	if err != nil {
		return nil, provider.Info{}, err
	}

	llm, err := provider.NewLLM(ctx, info, apiKey, baseURL, cfg.ThinkingLevel, &provider.LLMOptions{
		ExtraHeaders: cfg.ExtraHeaders,
	})
	if err != nil {
		return nil, provider.Info{}, fmt.Errorf("creating LLM provider: %w", err)
	}

	tracker := guardrail.New(cfg.MaxDailyTokens)
	tracker.SetContextWindowSize(contextWindow(ctx, cfg, info, baseURL))
	return guardrail.WrapModel(llm, tracker), info, nil
}
