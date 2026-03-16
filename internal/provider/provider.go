package provider

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/adk/model"
)

// Info describes a provider and the model to use.
type Info struct {
	Provider string
	Model    string
	Ollama   bool // true when model was suffixed with :cloud (Ollama Anthropic-compatible API)
}

// Known model prefixes mapped to providers.
var modelPrefixes = map[string]string{
	"claude": "anthropic",
	"gpt":    "openai",
	"o1":     "openai",
	"o3":     "openai",
	"o4":     "openai",
	"gemini": "gemini",
}

// Resolve determines the provider from a model name.
// Models ending with ":cloud" are routed through the Anthropic provider
// using Ollama's Anthropic-compatible API.
func Resolve(modelName string) (Info, error) {
	if modelName == "" {
		return Info{}, fmt.Errorf("no model specified")
	}

	// Detect ollama/ prefix → Ollama OpenAI-compatible API.
	// The prefix is stripped; the remainder is the Ollama model name.
	if strings.HasPrefix(strings.ToLower(modelName), "ollama/") {
		return Info{Provider: "openai", Model: modelName[len("ollama/"):], Ollama: true}, nil
	}

	// Detect :cloud suffix → Ollama Anthropic-compatible API.
	// The full model name (including :cloud) is passed to Ollama as-is.
	if strings.HasSuffix(modelName, ":cloud") {
		return Info{Provider: "anthropic", Model: modelName, Ollama: true}, nil
	}

	lower := strings.ToLower(modelName)
	for prefix, provider := range modelPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return Info{Provider: provider, Model: modelName}, nil
		}
	}

	return Info{}, fmt.Errorf("unknown model %q: cannot determine provider (known prefixes: claude, gpt, o1, o3, o4, gemini, or use ollama/ prefix or :cloud suffix for Ollama)", modelName)
}

// NewLLM creates a model.LLM for the given provider info, API key, optional base URL, and thinking level.
func NewLLM(ctx context.Context, info Info, apiKey, baseURL, thinkingLevel string) (model.LLM, error) {
	switch info.Provider {
	case "gemini":
		return NewGemini(ctx, info.Model, baseURL)
	case "openai":
		return NewOpenAI(ctx, info.Model, apiKey, baseURL)
	case "anthropic":
		return NewAnthropic(ctx, info.Model, apiKey, baseURL, thinkingLevel)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", info.Provider)
	}
}
