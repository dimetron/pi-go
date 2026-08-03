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
// OpenAI and Anthropic IDs are loaded from embedded llm-prices snapshots under
// modeldata/. Gemini and Mistral IDs are maintained in model_catalog.go.
// Update context-window metadata in modeldata/context-windows.json in the same
// change when official limits change.
var KnownModels = mustLoadKnownModels()

// contextWindowSizes maps model name prefixes to context window sizes (in tokens).
var contextWindowSizes = mustLoadContextWindowSizes()

// ContextWindowSize returns the context window size for a model (in tokens).
// Returns 0 if the model is unknown.
func ContextWindowSize(modelName string) int64 {
	lower := strings.ToLower(modelName)
	lower = strings.TrimPrefix(lower, "ollama/")
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

	// Detect opencode/ prefix → OpenCode Go provider.
	// The prefix is stripped; the remainder is the bare model ID.
	if strings.HasPrefix(strings.ToLower(modelName), "opencode/") {
		return Info{Provider: "opencode", Model: modelName[len("opencode/"):]}, nil
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
		return NewOllama(ctx, info.Model, apiKey, baseURL, thinkingLevel, opts)
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
	case "opencode":
		return NewOpenCode(ctx, info.Model, apiKey, baseURL, thinkingLevel, opts)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", info.Provider)
	}
}
