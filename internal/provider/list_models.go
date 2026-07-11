package provider

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ModelInfo holds a model ID and optional metadata returned by a provider's
// model listing API.
type ModelInfo struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by,omitempty"`
}

// ListModelsOptions controls how models are fetched from a provider.
type ListModelsOptions struct {
	APIKey   string
	BaseURL  string // override the default API endpoint
	Insecure bool   // skip TLS certificate verification
}

// defaultListTimeout is the per-request timeout for model listing.
const defaultListTimeout = 30 * time.Second

// httpClient returns an HTTP client for model listing. When Insecure is true,
// TLS certificate verification is skipped.
func (o ListModelsOptions) httpClient() *http.Client {
	if !o.Insecure {
		return http.DefaultClient
	}
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

// providerDefaultBaseURL returns the default API base URL for each provider.
func providerDefaultBaseURL(p string) string {
	switch p {
	case "anthropic":
		return "https://api.anthropic.com"
	case "openai":
		return "https://api.openai.com"
	case "gemini":
		return "https://generativelanguage.googleapis.com"
	case "mistral":
		return "https://api.mistral.ai"
	default:
		return ""
	}
}

// ListModels calls the given provider's model listing API and returns
// available model IDs. Supported providers: anthropic, openai, gemini,
// mistral, ollama.
func ListModels(ctx context.Context, providerName string, opts ListModelsOptions) ([]ModelInfo, error) {
	switch providerName {
	case "anthropic":
		return listAnthropicModels(ctx, opts)
	case "openai":
		return listOpenAIModels(ctx, opts)
	case "gemini":
		return listGeminiModels(ctx, opts)
	case "mistral":
		return listMistralModels(ctx, opts)
	case "ollama":
		names, err := OllamaListModels(ctx, opts.BaseURL)
		if err != nil {
			return nil, err
		}
		result := make([]ModelInfo, len(names))
		for i, n := range names {
			result[i] = ModelInfo{ID: n}
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", providerName)
	}
}

// listOpenAIModels fetches models from GET /v1/models.
// Works for OpenAI platform, Azure-compatible, and Mistral-compatible endpoints.
func listOpenAIModels(ctx context.Context, opts ListModelsOptions) ([]ModelInfo, error) {
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = providerDefaultBaseURL("openai")
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/v1/models"
	if strings.HasSuffix(strings.TrimRight(baseURL, "/"), "/v1") {
		endpoint = strings.TrimRight(baseURL, "/") + "/models"
	}

	var payload struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := fetchJSON(ctx, http.MethodGet, endpoint, opts, "openai", &payload); err != nil {
		return nil, fmt.Errorf("listing OpenAI models: %w", err)
	}
	models := make([]ModelInfo, len(payload.Data))
	for i, m := range payload.Data {
		models[i] = ModelInfo{ID: m.ID, OwnedBy: m.OwnedBy}
	}
	return models, nil
}

// listMistralModels fetches models from GET /v1/models.
func listMistralModels(ctx context.Context, opts ListModelsOptions) ([]ModelInfo, error) {
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = providerDefaultBaseURL("mistral")
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/v1/models"

	var payload struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := fetchJSON(ctx, http.MethodGet, endpoint, opts, "mistral", &payload); err != nil {
		return nil, fmt.Errorf("listing Mistral models: %w", err)
	}
	models := make([]ModelInfo, len(payload.Data))
	for i, m := range payload.Data {
		models[i] = ModelInfo{ID: m.ID, OwnedBy: m.OwnedBy}
	}
	return models, nil
}

// listAnthropicModels fetches models from GET /v1/models.
func listAnthropicModels(ctx context.Context, opts ListModelsOptions) ([]ModelInfo, error) {
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = providerDefaultBaseURL("anthropic")
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/v1/models"

	var payload struct {
		Data []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"data"`
	}
	if err := fetchJSON(ctx, http.MethodGet, endpoint, opts, "anthropic", &payload); err != nil {
		return nil, fmt.Errorf("listing Anthropic models: %w", err)
	}
	models := make([]ModelInfo, len(payload.Data))
	for i, m := range payload.Data {
		models[i] = ModelInfo{ID: m.ID, OwnedBy: m.Type}
	}
	return models, nil
}

// listGeminiModels fetches models from GET /v1beta/models.
func listGeminiModels(ctx context.Context, opts ListModelsOptions) ([]ModelInfo, error) {
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = providerDefaultBaseURL("gemini")
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/v1beta/models"

	// Gemini uses query-param auth, not a bearer token.
	reqCtx, cancel := context.WithTimeout(ctx, defaultListTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	if opts.APIKey != "" {
		q := req.URL.Query()
		q.Set("key", opts.APIKey)
		req.URL.RawQuery = q.Encode()
	}

	resp, err := opts.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var payload struct {
		Models []struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	models := make([]ModelInfo, len(payload.Models))
	for i, m := range payload.Models {
		// Gemini returns "models/gemini-2.5-flash" — strip the "models/" prefix.
		id := strings.TrimPrefix(m.Name, "models/")
		models[i] = ModelInfo{ID: id, OwnedBy: m.DisplayName}
	}
	return models, nil
}

// fetchJSON performs an HTTP request with the appropriate auth headers for the
// given provider and decodes the JSON response into dst.
func fetchJSON(ctx context.Context, method, url string, opts ListModelsOptions, providerName string, dst any) error {
	reqCtx, cancel := context.WithTimeout(ctx, defaultListTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, method, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	switch providerName {
	case "anthropic":
		if opts.APIKey != "" {
			req.Header.Set("x-api-key", opts.APIKey)
		}
		req.Header.Set("anthropic-version", "2023-06-01")
	case "openai", "mistral":
		if opts.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+opts.APIKey)
		}
	}

	resp, err := opts.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}
