package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/genai"
)

// NewGemini creates a Gemini model.LLM using ADK Go's native Gemini support.
// It reads the API key from GEMINI_API_KEY or GOOGLE_API_KEY env vars.
// If neither is set, it falls back to Application Default Credentials.
// If baseURL is non-empty, it overrides the default API endpoint.
func NewGemini(ctx context.Context, modelName, baseURL string, opts *LLMOptions) (model.LLM, error) {
	cfg := &genai.ClientConfig{
		Backend: genai.BackendGeminiAPI,
	}

	// Check for API key in env vars
	if apiKey := geminiAPIKey(); apiKey != "" {
		cfg.APIKey = apiKey
	}

	if httpOpts, overridden := geminiHTTPOptions(baseURL, opts); overridden {
		cfg.HTTPOptions = httpOpts
	}
	if geminiNeedsHTTPClient(opts) {
		httpClient, err := BuildHTTPClient(opts, 0)
		if err != nil {
			return nil, err
		}
		cfg.HTTPClient = httpClient
	}

	llm, err := gemini.NewModel(ctx, modelName, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating gemini model %q: %w", modelName, err)
	}

	return llm, nil
}

// geminiAPIKey reads the Gemini API key from the environment, preferring
// GEMINI_API_KEY over GOOGLE_API_KEY. It returns "" when neither is set, in
// which case the client falls back to Application Default Credentials.
func geminiAPIKey() string {
	if apiKey := os.Getenv("GEMINI_API_KEY"); apiKey != "" {
		return apiKey
	}
	return os.Getenv("GOOGLE_API_KEY")
}

// geminiHTTPOptions builds the endpoint and header overrides for the Gemini
// client. The second result reports whether any override was requested — when
// false the options are left at the SDK default rather than being overwritten
// with an empty struct.
func geminiHTTPOptions(baseURL string, opts *LLMOptions) (genai.HTTPOptions, bool) {
	hasExtraHeaders := opts != nil && len(opts.ExtraHeaders) > 0

	httpOpts := genai.HTTPOptions{}
	if baseURL != "" {
		httpOpts.BaseURL = baseURL
	}
	if hasExtraHeaders {
		httpOpts.Headers = make(http.Header)
		for k, v := range opts.ExtraHeaders {
			httpOpts.Headers.Set(k, v)
		}
	}
	return httpOpts, baseURL != "" || hasExtraHeaders
}

// geminiNeedsHTTPClient reports whether opts asks for transport settings that
// only a custom *http.Client can carry.
func geminiNeedsHTTPClient(opts *LLMOptions) bool {
	return opts != nil && (opts.InsecureSkipTLS || opts.CACertPath != "" || opts.ConnectTimeout > 0)
}
