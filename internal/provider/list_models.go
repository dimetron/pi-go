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
	ID            string   `json:"id"`
	OwnedBy       string   `json:"owned_by,omitempty"`
	ContextWindow int64    `json:"context_window,omitempty"` // max_context_length
	Capabilities  []string `json:"capabilities,omitempty"`   // e.g. completion_chat, vision
	ReleaseDate   string   `json:"release_date,omitempty"`   // models.dev catalog date, when known
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
	case "xai":
		return "https://api.x.ai"
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	case "agentgateway":
		return "http://localhost:4000"
	default:
		return ""
	}
}

// ListModels calls the given provider's model listing API and returns
// available model IDs. The switch below is the list of supported providers;
// an unsupported one returns an error.
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
	case "xai":
		return listXAIModels(ctx, opts)
	case "openrouter":
		return listOpenRouterModels(ctx, opts)
	case "agentgateway":
		return listAgentGatewayModels(ctx, opts)
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
// Works for OpenAI platform and Azure-compatible endpoints.
func listOpenAIModels(ctx context.Context, opts ListModelsOptions) ([]ModelInfo, error) {
	return listBearerModels(ctx, opts, "openai", "OpenAI")
}

// listMistralModels fetches models from GET <base>/v1/models and parses the
// documented Mistral model-card shape:
//
//	{"data":[{"id","owned_by","max_context_length","capabilities":{
//	  "completion_chat":bool,"completion_fim":bool,"function_calling":bool,
//	  "fine_tuning":bool,"vision":bool,"classification":bool}}]}
//
// Only completion_chat-capable models are returned (like the reference TS
// generator's tool_call filter). Context length and capabilities are copied
// through for display and JSON output.
func listMistralModels(ctx context.Context, opts ListModelsOptions) ([]ModelInfo, error) {
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = providerDefaultBaseURL("mistral")
	}
	trimmed := strings.TrimRight(baseURL, "/")
	endpoint := trimmed + "/v1/models"
	if strings.HasSuffix(trimmed, "/v1") {
		endpoint = trimmed + "/models"
	}

	var payload struct {
		Data []struct {
			ID               string `json:"id"`
			OwnedBy          string `json:"owned_by"`
			MaxContextLength int64  `json:"max_context_length"`
			Capabilities     struct {
				CompletionChat bool `json:"completion_chat"`
				CompletionFIM  bool `json:"completion_fim"`
				FunctionCall   bool `json:"function_calling"`
				FineTuning     bool `json:"fine_tuning"`
				Vision         bool `json:"vision"`
				Classification bool `json:"classification"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	if err := fetchJSON(ctx, http.MethodGet, endpoint, opts, "mistral", &payload); err != nil {
		return nil, fmt.Errorf("listing Mistral models: %w", err)
	}
	models := make([]ModelInfo, 0, len(payload.Data))
	for _, m := range payload.Data {
		if !m.Capabilities.CompletionChat {
			continue
		}
		caps := make([]string, 0, 6)
		if m.Capabilities.CompletionChat {
			caps = append(caps, "completion_chat")
		}
		if m.Capabilities.CompletionFIM {
			caps = append(caps, "completion_fim")
		}
		if m.Capabilities.FunctionCall {
			caps = append(caps, "function_calling")
		}
		if m.Capabilities.FineTuning {
			caps = append(caps, "fine_tuning")
		}
		if m.Capabilities.Vision {
			caps = append(caps, "vision")
		}
		if m.Capabilities.Classification {
			caps = append(caps, "classification")
		}
		models = append(models, ModelInfo{
			ID:            m.ID,
			OwnedBy:       m.OwnedBy,
			ContextWindow: m.MaxContextLength,
			Capabilities:  caps,
		})
	}
	return models, nil
}

// listXAIModels fetches models from GET /v1/models.
func listXAIModels(ctx context.Context, opts ListModelsOptions) ([]ModelInfo, error) {
	return listBearerModels(ctx, opts, "xai", "xAI")
}

// listOpenRouterModels fetches models from GET /v1/models.
func listOpenRouterModels(ctx context.Context, opts ListModelsOptions) ([]ModelInfo, error) {
	return listBearerModels(ctx, opts, "openrouter", "OpenRouter")
}

// listAgentGatewayModels fetches models from GET /v1/models on the local
// agentgateway. The gateway is OpenAI-compatible, so the shared bearer
// envelope applies; the default endpoint is localhost:4000.
//
// The gateway's /v1/models only carries id/owned_by — no context window — so
// each model is enriched from the embedded context-window table. That is what
// makes `pi model list agentgateway -o json` show a real context_window for
// the virtual models (e.g. ollama-deepseek → 1M) instead of omitting the field.
func listAgentGatewayModels(ctx context.Context, opts ListModelsOptions) ([]ModelInfo, error) {
	models, err := listBearerModels(ctx, opts, "agentgateway", "agentgateway")
	if err != nil {
		return nil, err
	}
	for i := range models {
		models[i].ContextWindow = ContextWindowSizeFor("agentgateway", models[i].ID)
	}
	return models, nil
}

// listBearerModels fetches models from GET <base>/v1/models with bearer auth
// and OpenAI's {"data":[{"id","owned_by"}]} envelope — the shape every
// OpenAI-compatible vendor endpoint shares. displayName only labels the error.
//
// A base URL that already ends in /v1 is not extended again: the LLM-side
// default for these vendors carries the version segment (api.x.ai/v1), so a
// user who exports the same value as XAI_BASE_URL would otherwise be sent to
// /v1/v1/models.
func listBearerModels(ctx context.Context, opts ListModelsOptions, providerName, displayName string) ([]ModelInfo, error) {
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = providerDefaultBaseURL(providerName)
	}
	trimmed := strings.TrimRight(baseURL, "/")
	endpoint := trimmed + "/v1/models"
	if strings.HasSuffix(trimmed, "/v1") {
		endpoint = trimmed + "/models"
	}

	var payload struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := fetchJSON(ctx, http.MethodGet, endpoint, opts, providerName, &payload); err != nil {
		return nil, fmt.Errorf("listing %s models: %w", displayName, err)
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
	case "openai", "mistral", "xai", "openrouter", "agentgateway":
		if opts.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+opts.APIKey)
		}
		if providerName == "openrouter" {
			openrouterAppAttribution(req.Header)
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
