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
//
// Every field here is populated from the provider's own response when the
// provider sends it. ContextWindow and MaxInputTokens are not interchangeable:
// a provider that reports both (agentgateway, OpenAI-compatible catalog
// entries) is describing total context and the prompt ceiling separately, and
// the ceiling is the smaller number because the output reservation comes out of
// the total. Compaction budgets against the ceiling, so it wins when present.
type ModelInfo struct {
	ID          string `json:"id"`
	OwnedBy     string `json:"owned_by,omitempty"`
	ReleaseDate string `json:"release_date,omitempty"` // models.dev catalog date, when known

	// ContextWindow is total context. For providers that report a single
	// limit (Anthropic max_input_tokens, Gemini inputTokenLimit) this and
	// MaxInputTokens carry the same number.
	ContextWindow int64 `json:"context_window,omitempty"`

	// MaxInputTokens is the prompt ceiling, when the provider distinguishes it
	// from total context. 0 means the provider did not say.
	MaxInputTokens int64 `json:"max_input_tokens,omitempty"`

	// MaxTokens is the output reservation.
	MaxTokens int64 `json:"max_tokens,omitempty"`

	DisplayName string `json:"display_name,omitempty"`
	// Lifecycle is active, deprecated, or retired when the provider says so.
	Lifecycle string `json:"lifecycle,omitempty"`
	// RetiresAt is when a deprecated model stops answering, as the provider
	// spells it (RFC3339, or a bare date for OpenAI's shutdown_date).
	RetiresAt string `json:"retires_at,omitempty"`

	// Capabilities is the display list rendered in the table's Notes column.
	Capabilities []string `json:"capabilities,omitempty"`

	// Structured capability flags. Pointers because a provider that omits a
	// flag means "unknown", which is not the same as false: agentgateway drops
	// a flag entirely when the targets of a virtual model disagree, and
	// recording that as false would be a guess.
	Reasoning        *bool `json:"reasoning,omitempty"`
	ToolCall         *bool `json:"tool_call,omitempty"`
	StructuredOutput *bool `json:"structured_output,omitempty"`
	Temperature      *bool `json:"temperature,omitempty"`
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
		// OllamaListModels already fills context window and capabilities
		// from the daemon's /api/tags response — nothing to enrich.
		return OllamaListModels(ctx, opts.BaseURL)
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
// Since gateway v1.7.0 the response is catalog-backed: entries for models the
// embedded catalog knows carry context_window, context_length,
// max_input_tokens, max_tokens, name, architecture, capabilities and
// supported_parameters. Those are read here and trusted.
//
// It previously read only id/owned_by and backfilled the window from
// context-windows.json. That made a working gateway produce worse data than a
// missing one: measured against a live v1.7.0-alpha.1, 109 of the 241 entries
// that report a window agreed with the local table, 46 over-estimated it
// (compaction fires past the real limit and the request overflows) and 62 were
// blanked to 0 (compaction never fires at all). Do not reintroduce a local
// lookup here — the gateway knows its own routes.
//
// Virtual models are the case that catches a well-meaning fallback: the
// gateway reports them with owned_by "virtual" and no limits at all, because
// its own combine() intersects the targets and refuses to advertise a limit
// that does not hold for every one of them. A 0 window here is the correct,
// deliberate answer, not missing data to fill in.
func listAgentGatewayModels(ctx context.Context, opts ListModelsOptions) ([]ModelInfo, error) {
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = providerDefaultBaseURL("agentgateway")
	}
	trimmed := strings.TrimRight(baseURL, "/")
	endpoint := trimmed + "/v1/models"
	if strings.HasSuffix(trimmed, "/v1") {
		endpoint = trimmed + "/models"
	}

	var payload struct {
		Data []struct {
			ID            string `json:"id"`
			OwnedBy       string `json:"owned_by"`
			Name          string `json:"name"`
			ContextWindow int64  `json:"context_window"`
			ContextLength int64  `json:"context_length"`
			MaxInput      int64  `json:"max_input_tokens"`
			MaxTokens     int64  `json:"max_tokens"`
			Capabilities  struct {
				Reasoning        *bool `json:"reasoning"`
				StructuredOutput *bool `json:"structured_outputs"`
				Tools            *bool `json:"tools"`
				Temperature      *bool `json:"temperature"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	if err := fetchJSON(ctx, http.MethodGet, endpoint, opts, "agentgateway", &payload); err != nil {
		return nil, fmt.Errorf("listing agentgateway models: %w", err)
	}

	models := make([]ModelInfo, 0, len(payload.Data))
	for _, m := range payload.Data {
		info := ModelInfo{
			ID:               m.ID,
			OwnedBy:          m.OwnedBy,
			DisplayName:      m.Name,
			ContextWindow:    m.ContextWindow,
			MaxInputTokens:   m.MaxInput,
			MaxTokens:        m.MaxTokens,
			Reasoning:        m.Capabilities.Reasoning,
			ToolCall:         m.Capabilities.Tools,
			StructuredOutput: m.Capabilities.StructuredOutput,
			Temperature:      m.Capabilities.Temperature,
		}
		// context_length duplicates context_window on every entry that carries
		// both; it is the pre-v1.7 spelling and is read only as a fallback.
		if info.ContextWindow == 0 {
			info.ContextWindow = m.ContextLength
		}
		models = append(models, info)
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
