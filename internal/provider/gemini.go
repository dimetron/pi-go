package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/genai"
)

// NewGemini creates a Gemini model.LLM using ADK Go's native Gemini support.
// It reads the API key from GOOGLE_API_KEY or GEMINI_API_KEY env vars.
// If neither is set, it falls back to Application Default Credentials.
// If baseURL is non-empty, it overrides the default API endpoint.
func NewGemini(ctx context.Context, modelName, baseURL string, opts *LLMOptions) (model.LLM, error) {
	cfg := &genai.ClientConfig{
		Backend: genai.BackendGeminiAPI,
	}

	// Check for API key in env vars
	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if apiKey != "" {
		cfg.APIKey = apiKey
	}

	httpOpts := genai.HTTPOptions{}
	if baseURL != "" {
		httpOpts.BaseURL = baseURL
	}
	if opts != nil && len(opts.ExtraHeaders) > 0 {
		httpOpts.Headers = make(http.Header)
		for k, v := range opts.ExtraHeaders {
			httpOpts.Headers.Set(k, v)
		}
	}
	if baseURL != "" || (opts != nil && len(opts.ExtraHeaders) > 0) {
		cfg.HTTPOptions = httpOpts
	}
	if opts != nil && opts.InsecureSkipTLS {
		cfg.HTTPClient = BuildHTTPClient(opts, 0)
	}

	llm, err := gemini.NewModel(ctx, modelName, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating gemini model %q: %w", modelName, err)
	}

	return llm, nil
}
