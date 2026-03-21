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
	"claude": "anthropic",
	"gpt":    "openai",
	"gpt-5":  "openai",
	"gemini": "gemini",
}

// OllamaModelPrefixes are common Ollama model name prefixes.
var OllamaModelPrefixes = []string{"qwen", "minimax", "deepseek", "llama", "mistral", "phi", "codellama", "gemma"}

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

	return Info{}, fmt.Errorf("unknown model %q: cannot determine provider (known prefixes: claude, gpt, gemini, qwen, minimax, deepseek, llama, mistral, phi, codellama, gemma, or use ollama/ prefix for Ollama)", modelName)
}

// CheckOllama verifies that the Ollama server at baseURL is reachable.
// It first checks TCP connectivity on the port, then issues a GET to the root
// endpoint (Ollama returns "Ollama is running").
func CheckOllama(baseURL string) error {
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
	case "anthropic":
		return NewAnthropic(ctx, info.Model, apiKey, baseURL, thinkingLevel, opts)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", info.Provider)
	}
}
