package provider

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"google.golang.org/adk/model"
)

// BuildTransport creates an http.Transport with optional TLS skip and extra headers.
// Returns nil if no customization is needed.
func BuildTransport(opts *LLMOptions) http.RoundTripper {
	if opts == nil {
		return nil
	}
	hasHeaders := len(opts.ExtraHeaders) > 0
	if !opts.InsecureSkipTLS && !hasHeaders {
		return nil
	}

	base := http.DefaultTransport
	if opts.InsecureSkipTLS {
		base = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // user-requested
		}
	}
	if hasHeaders {
		base = &headerTransport{base: base, headers: opts.ExtraHeaders}
	}
	return base
}

// BuildHTTPClient creates an *http.Client with optional TLS skip, extra headers, and timeout.
// Returns a default client if no customization is needed.
func BuildHTTPClient(opts *LLMOptions, timeout time.Duration) *http.Client {
	transport := BuildTransport(opts)
	if transport == nil {
		return &http.Client{Timeout: timeout}
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

// headerTransport injects extra HTTP headers into every request.
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return t.base.RoundTrip(req)
}

// Info describes a provider and the model to use.
type Info struct {
	Provider string
	Model    string
	Ollama   bool // true when model is served by Ollama
}

// Known model prefixes mapped to providers.
var modelPrefixes = map[string]string{
	"claude":  "anthropic",
	"gpt":     "openai",
	"gpt-5":   "openai",
	"gemini":  "gemini",
	"mistral": "mistral",
}

// OllamaModelPrefixes are common Ollama model name prefixes.
var OllamaModelPrefixes = []string{"qwen", "minimax", "deepseek", "llama", "phi", "codellama", "gemma"}

// KnownModels lists recognized model names per provider.
// The check is prefix-based: a model is valid if it starts with any entry.
// Ollama models are not validated here (they are dynamic).
//
// April 2026 source snapshot and update path:
//   - OpenAI: https://developers.openai.com/api/docs/models
//   - Anthropic: https://platform.claude.com/docs/en/about-claude/models/overview
//   - Gemini: https://ai.google.dev/gemini-api/docs/models
//   - Mistral: https://docs.mistral.ai/getting-started/models
//
// To update: re-check those official model pages, keep the top current
// max/large/medium/small or fast tier IDs per provider, and update
// contextWindowSizes in the same change.
var KnownModels = map[string][]string{
	"anthropic": {
		// Latest Claude tiers: Opus 4.7 (large), Sonnet 4.6 (medium),
		// Haiku 4.5 (small/fast). Haiku has both snapshot ID and alias.
		"claude-opus-4-7",
		"claude-sonnet-4-6",
		"claude-haiku-4-5-20251001",
		"claude-haiku-4-5",
	},
	"openai": {
		// Latest OpenAI frontier tiers: GPT-5.4 pro/max, GPT-5.4
		// large, GPT-5.4 mini, and GPT-5.4 nano.
		"gpt-5.4-pro",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.4-nano",
		// Responses-only codex models.
		"gpt-5.3-codex",
		"gpt-5.2-codex",
		"gpt-5.1-codex",
		"gpt-5.1-codex-mini",
		"gpt-5.1-codex-max",
	},
	"gemini": {
		// Latest Gemini tiers: 3.1 Pro, 3 Flash, and 3.1 Flash-Lite.
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
		// Latest Mistral generalist tiers: Large 3, Medium 3.1,
		// Small 4, plus documented "latest" aliases.
		"mistral-large-2512",
		"mistral-large-latest",
		"mistral-medium-2508",
		"mistral-medium-latest",
		"mistral-small-2603",
		"mistral-small-latest",

		// Compatibility with older/specialized Mistral config names.
		"codestral",
		"pixtral",
		"ministral",
	},
}

// contextWindowSizes maps model name prefixes to context window sizes (in tokens).
var contextWindowSizes = map[string]int64{
	// Anthropic
	"claude-opus-4-7":   1_000_000,
	"claude-sonnet-4-6": 1_000_000,
	"claude-haiku-4-5":  200_000,
	// OpenAI
	"gpt-5.4-pro":   1_050_000,
	"gpt-5.4-mini":  400_000,
	"gpt-5.4-nano":  400_000,
	"gpt-5.4":       1_050_000,
	"gpt-5.3-codex": 1_050_000,
	"gpt-5.2-codex": 1_050_000,
	"gpt-5.1-codex": 1_050_000,
	"gpt-5":         400_000,
	// Gemini
	"gemini-3.1-pro-preview":        1_048_576,
	"gemini-3-flash-preview":        1_048_576,
	"gemini-3.1-flash-lite-preview": 1_048_576,
	"gemini-2.5":                    1_048_576,
	// Mistral
	"mistral-large-2512":    256_000,
	"mistral-large-latest":  256_000,
	"mistral-medium-2508":   128_000,
	"mistral-medium-latest": 128_000,
	"mistral-small-2603":    256_000,
	"mistral-small-latest":  256_000,
	"codestral":             256_000,
}

// ContextWindowSize returns the context window size for a model (in tokens).
// Returns 0 if the model is unknown. For Ollama models, returns 0 (unknown).
func ContextWindowSize(modelName string) int64 {
	lower := strings.ToLower(modelName)
	// Try longest prefix match first for accuracy.
	bestLen := 0
	var bestSize int64
	for prefix, size := range contextWindowSizes {
		if strings.HasPrefix(lower, prefix) && len(prefix) > bestLen {
			bestLen = len(prefix)
			bestSize = size
		}
	}
	return bestSize
}

// ValidateModel checks whether the model name is recognized for its provider.
// Returns an error with suggestions if the model is unknown.
// Ollama models are always considered valid (they are pulled dynamically).
func ValidateModel(info Info) error {
	if info.Ollama {
		return nil
	}
	known, ok := KnownModels[info.Provider]
	if !ok {
		return nil // unknown provider, skip validation
	}
	lower := strings.ToLower(info.Model)
	for _, prefix := range known {
		if strings.HasPrefix(lower, prefix) {
			return nil
		}
	}
	return fmt.Errorf("unknown %s model %q; known models: %s",
		info.Provider, info.Model, strings.Join(known, ", "))
}

// Resolve determines the provider from a model name.
// Ollama models are routed to the native "ollama" provider.
func Resolve(modelName string) (Info, error) {
	if modelName == "" {
		return Info{}, fmt.Errorf("no model specified")
	}

	// Detect ollama/ prefix → native Ollama provider.
	// The prefix is stripped; the remainder is the Ollama model name.
	if strings.HasPrefix(strings.ToLower(modelName), "ollama/") {
		return Info{Provider: "ollama", Model: modelName[len("ollama/"):], Ollama: true}, nil
	}

	// Detect azure/ prefix → Azure OpenAI provider.
	// The prefix is stripped; the remainder is the Azure deployment name.
	if strings.HasPrefix(strings.ToLower(modelName), "azure/") {
		return Info{Provider: "azure", Model: modelName[len("azure/"):]}, nil
	}

	// Detect :cloud or :local suffix → native Ollama provider.
	// Keep the full model name — :cloud/:local are valid Ollama model tags.
	if strings.HasSuffix(modelName, ":cloud") || strings.HasSuffix(modelName, ":local") {
		return Info{Provider: "ollama", Model: modelName, Ollama: true}, nil
	}

	lower := strings.ToLower(modelName)
	for prefix, provider := range modelPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return Info{Provider: provider, Model: modelName}, nil
		}
	}

	// Detect common Ollama model prefixes → native Ollama provider.
	for _, prefix := range OllamaModelPrefixes {
		if strings.HasPrefix(lower, prefix) {
			model := modelName
			if !strings.Contains(model, ":") {
				model = modelName + ":latest"
			}
			return Info{Provider: "ollama", Model: model, Ollama: true}, nil
		}
	}

	return Info{}, fmt.Errorf("unknown model %q: cannot determine provider (known prefixes: claude, gpt, gemini, mistral, qwen, minimax, deepseek, llama, phi, codellama, gemma, or use ollama/ prefix for Ollama)", modelName)
}

func normalizeBaseURL(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	if !strings.Contains(baseURL, "://") {
		return "http://" + baseURL
	}
	return baseURL
}

// CheckOllama verifies that the Ollama server at baseURL is reachable.
// It first checks TCP connectivity on the port, then issues a GET to the root
// endpoint (Ollama returns "Ollama is running").
func CheckOllama(baseURL string) error {
	baseURL = normalizeBaseURL(baseURL)
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("invalid Ollama URL %q: %w", baseURL, err)
	}

	host := u.Host
	if !strings.Contains(host, ":") {
		if u.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}

	// TCP port check.
	conn, err := net.DialTimeout("tcp", host, 3*time.Second)
	if err != nil {
		return fmt.Errorf("ollama not reachable at %s: %w", host, err)
	}
	conn.Close()

	// HTTP health check.
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(baseURL)
	if err != nil {
		return fmt.Errorf("ollama HTTP check failed at %s: %w", baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama returned status %d at %s", resp.StatusCode, baseURL)
	}
	return nil
}

// LLMOptions holds optional configuration for LLM provider creation.
type LLMOptions struct {
	ExtraHeaders    map[string]string
	InsecureSkipTLS bool
}

// NewLLM creates a model.LLM for the given provider info, API key, optional base URL, thinking level, and options.
func NewLLM(ctx context.Context, info Info, apiKey, baseURL, thinkingLevel string, opts *LLMOptions) (model.LLM, error) {
	if opts == nil {
		opts = &LLMOptions{}
	}
	switch info.Provider {
	case "ollama":
		return NewOllama(ctx, info.Model, baseURL, thinkingLevel, opts)
	case "gemini":
		return NewGemini(ctx, info.Model, baseURL, opts)
	case "openai":
		return NewOpenAI(ctx, info.Model, apiKey, baseURL, opts)
	case "azure":
		// For Azure, baseURL can be used as endpoint override if provided via --url flag.
		// API key and api-version are read from env vars if not provided.
		return NewAzureOpenAI(ctx, info.Model, apiKey, baseURL, "", opts)
	case "anthropic":
		return NewAnthropic(ctx, info.Model, apiKey, baseURL, thinkingLevel, opts)
	case "mistral":
		return NewMistral(ctx, info.Model, apiKey, baseURL, opts)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", info.Provider)
	}
}
