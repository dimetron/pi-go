package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

const openrouterDefaultBaseURL = "https://openrouter.ai/api/v1"

// openrouterListTimeout bounds each /models fetch for context-window lookup.
const openrouterListTimeout = 10 * time.Second

// openrouterModel implements model.LLM for the OpenRouter API.
// OpenRouter exposes an OpenAI-compatible chat completions endpoint,
// so we reuse the OpenAI SDK with a custom base URL.
type openrouterModel struct {
	modelName string
	client    openai.Client
}

// NewOpenRouter creates an OpenRouter model.LLM.
// If baseURL is empty, the default OpenRouter API endpoint is used.
func NewOpenRouter(_ context.Context, modelName, apiKey, baseURL string, llmOpts *LLMOptions) (model.LLM, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("openrouter API key is required (set OPENROUTER_API_KEY)")
	}
	if baseURL == "" {
		baseURL = openrouterDefaultBaseURL
	}
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
	}
	if llmOpts != nil {
		for k, v := range llmOpts.ExtraHeaders {
			opts = append(opts, option.WithHeader(k, v))
		}
		transport, err := BuildTransport(llmOpts)
		if err != nil {
			return nil, err
		}
		if transport != nil {
			opts = append(opts, option.WithHTTPClient(&http.Client{Transport: transport}))
		}
	}
	client := openai.NewClient(opts...)
	return &openrouterModel{modelName: modelName, client: client}, nil
}

func (m *openrouterModel) Name() string { return m.modelName }

func (m *openrouterModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		messages, systemInstruction := oaiContentsToMessages(req.Contents, req.Config)

		modelName := req.Model
		if modelName == "" {
			modelName = m.modelName
		}

		params := openai.ChatCompletionNewParams{
			Model:    modelName,
			Messages: messages,
		}
		if systemInstruction != "" {
			params.Messages = append([]openai.ChatCompletionMessageParamUnion{
				openai.SystemMessage(systemInstruction),
			}, params.Messages...)
		}

		if req.Config != nil && len(req.Config.Tools) > 0 {
			params.Tools = oaiGenaiToolsToOpenAI(req.Config.Tools)
			params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{
				OfAuto: openai.String("auto"),
			}
		}

		if stream {
			retryStream(ctx, streamRetryConfig(), yield, func(y func(*model.LLMResponse, error) bool) {
				oaiRunStreaming(ctx, &m.client, params, y)
			})
		} else {
			oaiRunNonStreaming(ctx, &m.client, params, yield)
		}
	}
}

// openrouterFinishReasonToGenai maps OpenRouter finish_reason to genai.FinishReason.
// OpenRouter uses the same finish reasons as OpenAI.
func openrouterFinishReasonToGenai(reason string) genai.FinishReason {
	return oaiFinishReasonToGenai(reason)
}

// OpenRouterContextWindowSize queries OpenRouter's /models listing for the
// context window of the given model, preferring top_provider.context_length
// (what the best endpoint for this model enforces) over the model-level
// context_length. Returns 0 if the size cannot be determined — mirroring
// OllamaContextWindowSize, so callers can treat 0 as "no live answer" and fall
// back to the static catalog.
//
// OpenRouter has no per-model endpoint (it answers 404), so every lookup scans
// the full ~700KB listing; results are cached per base URL for an hour.
func OpenRouterContextWindowSize(ctx context.Context, baseURL, modelName string) int64 {
	name := strings.ToLower(strings.TrimSpace(modelName))
	if name == "" || name == "auto" {
		return 0
	}

	models, ok := openrouterModelContextLengths(ctx, baseURL)
	if !ok {
		return 0
	}
	return models[name]
}

// openrouterCacheTTL is how long a fetched /models listing stays trusted.
const openrouterCacheTTL = time.Hour

// openrouterWindowEntry pairs a parsed model→window table with the time it
// was fetched, so repeat lookups can tell a fresh entry from a stale one.
type openrouterWindowEntry struct {
	models    map[string]int64
	fetchedAt time.Time
}

var (
	openrouterWindowCacheMu sync.Mutex
	// openrouterWindowCache maps a base URL to its last fetched listing.
	openrouterWindowCache = map[string]openrouterWindowEntry{}
)

// openrouterModelContextLengths fetches and parses OpenRouter's /models
// listing into a lowercase model ID → context length table, serving repeat
// lookups from a per-base-URL cache until openrouterCacheTTL elapses. ok is
// false when the listing cannot be fetched or understood; callers treat that
// as "unknown window".
func openrouterModelContextLengths(ctx context.Context, baseURL string) (map[string]int64, bool) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = openrouterDefaultBaseURL
	}

	openrouterWindowCacheMu.Lock()
	entry, ok := openrouterWindowCache[baseURL]
	openrouterWindowCacheMu.Unlock()
	if ok && time.Since(entry.fetchedAt) < openrouterCacheTTL {
		return entry.models, true
	}

	models := openrouterFetchModelContextLengths(ctx, baseURL)
	if models == nil {
		return nil, false
	}

	entry = openrouterWindowEntry{models: models, fetchedAt: time.Now()}
	openrouterWindowCacheMu.Lock()
	openrouterWindowCache[baseURL] = entry
	openrouterWindowCacheMu.Unlock()
	return entry.models, true
}

// openrouterFetchModelContextLengths performs the live GET <base>/models and
// extracts each model's context length. It never returns an error: any failure
// yields nil, matching the Ollama helper's no-error contract.
func openrouterFetchModelContextLengths(ctx context.Context, baseURL string) map[string]int64 {
	fetchCtx, cancel := context.WithTimeout(ctx, openrouterListTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var payload struct {
		Data []struct {
			ID            string `json:"id"`
			ContextLength int64  `json:"context_length"`
			TopProvider   struct {
				ContextLength int64 `json:"context_length"`
			} `json:"top_provider"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return nil
	}

	models := make(map[string]int64, len(payload.Data))
	for _, m := range payload.Data {
		id := strings.ToLower(strings.TrimSpace(m.ID))
		if id == "" {
			continue
		}
		size := m.TopProvider.ContextLength
		if size <= 0 {
			size = m.ContextLength
		}
		if size > 0 {
			models[id] = size
		}
	}
	return models
}
