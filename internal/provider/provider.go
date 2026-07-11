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

	"google.golang.org/adk/v2/model"
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
	Custom   bool // true when using an explicit custom OpenAI-compatible endpoint
}

// Known model prefixes mapped to providers.
var modelPrefixes = map[string]string{
	"claude":    "anthropic",
	"gpt":       "openai",
	"gpt-5":     "openai",
	"gemini":    "gemini",
	"mistral":   "mistral",
	"magistral": "mistral",
}

// OllamaModelPrefixes are model prefixes that previously auto-routed to Ollama.
// Bare Ollama model names are intentionally not auto-detected; use the explicit
// ollama/ prefix, or the :cloud tag for Ollama cloud models.
var OllamaModelPrefixes = []string{}

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
		// April 2026 Anthropic models (from llm-anthropic reference).
		// See https://docs.anthropic.com/claude/docs/models-overview
		//
		// Claude 3 series (legacy, still available)
		"claude-3-opus-20240229",
		"claude-3-opus-latest",
		"claude-3-sonnet-20240229",
		"claude-3-sonnet-latest",
		"claude-3-haiku-20240307",
		"claude-3-haiku-latest",
		// Claude 3.5 series
		"claude-3-5-sonnet-20240620",
		"claude-3-5-sonnet-20241022",
		"claude-3-5-sonnet-latest",
		"claude-3-5-haiku-latest",
		// Claude 3.7 series
		"claude-3-7-sonnet-20250219",
		"claude-3-7-sonnet-latest",
		// Claude 4 series
		"claude-opus-4-0",
		"claude-sonnet-4-0",
		"claude-opus-4-1-20250805",
		// Claude 4.5 series
		"claude-sonnet-4-5",
		"claude-haiku-4-5-20251001",
		"claude-haiku-4-5",
		"claude-opus-4-5-20251101",
		// Claude 4.6 series (current best)
		"claude-opus-4-6",
		"claude-sonnet-4-6",
		// Claude 4.7 series
		"claude-opus-4-7",
		"claude-sonnet-4-7",
		"claude-haiku-4-7",
		// Claude 4.8 series
		"claude-opus-4-8",
		// Claude 5 series (current flagship)
		"claude-sonnet-5",
		"claude-fable-5",
	},
	"openai": {
		// Latest OpenAI frontier model plus retained GPT-5.4 compatibility tiers.
		"gpt-5.6",
		"gpt-5.5-pro",
		"gpt-5.5",
		"gpt-5.5-mini",
		"gpt-5.5-nano",
		"gpt-5.4-pro",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.4-nano",
		// Responses-only codex models.
		"gpt-5.5-codex",
		"gpt-5.4-codex",
		"gpt-5.3-codex",
		"gpt-5.2-codex",
		"gpt-5.1-codex",
		"gpt-5.1-codex-mini",
		"gpt-5.1-codex-max",
	},
	"gemini": {
		// Latest stable Gemini tier: 3.5 Flash.
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
}

// contextWindowSizes maps model name prefixes to context window sizes (in tokens).
var contextWindowSizes = map[string]int64{
	// Anthropic Claude 5 series (flagship)
	"claude-sonnet-5": 1_000_000,
	"claude-fable-5":  1_000_000,
	// Anthropic Claude 4.8 series
	"claude-opus-4-8": 1_000_000,
	// Anthropic Claude 4.7 series (128K output)
	"claude-opus-4-7":   1_000_000,
	"claude-sonnet-4-7": 1_000_000,
	"claude-haiku-4-7":  200_000,
	// Anthropic Claude 4.6 series (current best, 64K output)
	"claude-sonnet-4-6": 1_000_000,
	"claude-opus-4-6":   1_000_000,
	// Anthropic Claude 4.5 series
	"claude-haiku-4-5":  200_000,
	"claude-sonnet-4-5": 1_000_000,
	"claude-opus-4-5":   1_000_000,
	// Anthropic Claude 4.1 series
	"claude-opus-4-1": 1_000_000,
	// Anthropic Claude 4.0 series
	"claude-opus-4-0":   1_000_000,
	"claude-sonnet-4-0": 1_000_000,
	// Anthropic Claude 3.7 series
	"claude-3-7-sonnet": 200_000,
	// Anthropic Claude 3.5 series
	"claude-3-5-sonnet": 200_000,
	"claude-3-5-haiku":  200_000,
	// Anthropic Claude 3 series
	"claude-3-opus":   200_000,
	"claude-3-sonnet": 200_000,
	"claude-3-haiku":  200_000,
	// OpenAI
	"gpt-5.6":       1_050_000,
	"gpt-5.5-pro":   1_050_000,
	"gpt-5.5":       1_050_000,
	"gpt-5.5-mini":  400_000,
	"gpt-5.5-nano":  400_000,
	"gpt-5.5-codex": 1_050_000,
	"gpt-5.4-pro":   1_050_000,
	"gpt-5.4-mini":  400_000,
	"gpt-5.4-nano":  400_000,
	"gpt-5.4":       1_050_000,
	"gpt-5.4-codex": 1_050_000,
	"gpt-5.3-codex": 1_050_000,
	"gpt-5.2-codex": 1_050_000,
	"gpt-5.1-codex": 1_050_000,
	"gpt-5":         400_000,
	// Gemini
	"gemini-3.5-flash":              1_048_576,
	"gemini-3.5-flash-lite":         1_048_576,
	"gemini-3.5-pro":                1_048_576,
	"gemini-3.1-flash-lite":         1_048_576,
	"gemini-3.1-pro-preview":        1_048_576,
	"gemini-3-flash-preview":        1_048_576,
	"gemini-3.1-flash-lite-preview": 1_048_576,
	"gemini-2.5":                    1_048_576,
	// Mistral
	"mistral-large-2512":      256_000,
	"mistral-large-latest":    256_000,
	"mistral-medium-2603":     128_000,
	"mistral-medium-latest":   128_000,
	"mistral-small-2603":      256_000,
	"mistral-small-latest":    256_000,
	"magistral-medium-latest": 128_000,
	"magistral-small-latest":  128_000,
	"codestral":               256_000,
	// Custom/local models
	"qwen":    4_096,
	"glm-5.2": 976_000,
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
// Ollama and custom endpoint models are always considered valid (they are dynamic).
func ValidateModel(info Info) error {
	if info.Ollama || info.Custom {
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

// ResolveWithBaseURL determines the provider from a model name.
// When baseURL is provided and the model is not otherwise recognized, it routes
// the model to an OpenAI-compatible custom endpoint. The optional openai/ prefix
// is stripped so `--model openai/foo` sends `foo` to the custom endpoint.
func ResolveWithBaseURL(modelName, baseURL string) (Info, error) {
	if baseURL != "" {
		lower := strings.ToLower(modelName)
		if strings.HasPrefix(lower, "ollama/") || strings.HasPrefix(lower, "azure/") {
			return Resolve(modelName)
		}
		if strings.HasPrefix(lower, "openai/") {
			modelName = modelName[len("openai/"):]
			return Info{Provider: "openai", Model: modelName, Custom: true}, nil
		}
		info, err := Resolve(modelName)
		if err == nil && !info.Ollama {
			info.Custom = true
			return info, nil
		}
		return Info{Provider: "openai", Model: modelName, Custom: true}, nil
	}

	return Resolve(modelName)
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

	// Detect :cloud or -cloud suffix → native Ollama provider.
	// Keep the full model name — :cloud and -cloud are valid Ollama model tags.
	if strings.HasSuffix(modelName, ":cloud") || strings.HasSuffix(modelName, "-cloud") {
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

	return Info{}, fmt.Errorf("unknown model %q: cannot determine provider (known prefixes: claude, gpt, gemini, mistral; use ollama/ prefix for Ollama, or :cloud/-cloud suffix for Ollama cloud)", modelName)
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
	AdvisorModel    string // Advisor model (e.g., "claude-opus-4-7")
	AdvisorMaxUses  int    // Max advisor calls per request (0 = unlimited)
	AdvisorCaching  bool   // Enable ephemeral prompt caching for advisor
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
