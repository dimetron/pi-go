package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"strings"
	"sync"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/auth"
)

// openaiModel implements model.LLM for OpenAI-compatible APIs.
// It multiplexes over two protocols:
//   - Chat Completions (see openai_completions.go) — default for GPT-4, o1 and
//     anything that supports the legacy /v1/chat/completions shape.
//   - Responses API (see openai_responses.go) — required for Codex models and
//     used transparently for multi-turn state via previous_response_id.
//
// When authenticated with a ChatGPT codex OAuth token, the client is
// re-pointed at the ChatGPT backend (/backend-api/codex/responses) — see
// openai_codex.go for the codex-specific helpers.
type openaiModel struct {
	modelName string
	client    openai.Client
	// codexBackend is true when the client is pointed at the ChatGPT
	// backend (authenticated with a codex OAuth token). Those deployments
	// only expose /codex/responses, so chat completions must be skipped.
	codexBackend bool
	// maxOutputTokens caps the reply, in tokens. See
	// defaultOaiMaxOutputTokens for why omitting it is not an option.
	maxOutputTokens int64
	// mu protects responseState for Responses mode multi-turn.
	mu            sync.Mutex
	responseState *responsesState // nil when using Chat Completions
}

// defaultOaiMaxOutputTokens caps a reply on the OpenAI-compatible paths when
// neither the request nor the configuration asks for something else.
//
// Sending nothing is not the neutral choice it looks like. Every server
// substitutes its own default, and some are small: agentgateway in front of
// Anthropic settles on 4096, which cuts a long coding turn off mid-sentence
// (finish_reason "length"). Worse, when the cut lands inside a tool call's
// arguments the JSON never closes, the call reaches the tool with no arguments
// at all, and — before marshalFunctionCallArgs — that malformed turn went into
// the history and 400ed every request after it.
//
// 64000 is the output ceiling of the current Claude and GPT models, which is
// what this path mostly carries. It is an output cap, not a context window:
// the million-token figure quoted for these models is how much they can *read*.
// Override it with the maxOutputTokens setting for a backend whose models stop
// lower and reject the request rather than clamping it.
const defaultOaiMaxOutputTokens int64 = 64000

// responsesState holds state threaded across Responses API calls.
type responsesState struct {
	previousResponseID string // OpenAI-side session ID for multi-turn
}

// NewOpenAI creates an OpenAI model.LLM.
// If baseURL is non-empty, it overrides the default API endpoint.
// When apiKey is recognized as a codex ChatGPT OAuth token and no explicit
// baseURL is provided, the client is pointed at the ChatGPT backend
// (/codex/responses) and the `chatgpt-account-id` + `originator` headers
// pi-mono sends are injected — the platform /v1/responses endpoint rejects
// codex tokens with 401 "Missing scopes: api.responses.write".
func NewOpenAI(_ context.Context, modelName, apiKey, baseURL string, llmOpts *LLMOptions) (model.LLM, error) {
	if apiKey == "" {
		if baseURL == "" {
			return nil, fmt.Errorf("OpenAI API key is required")
		}
		apiKey = "dummy"
	}
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}

	useCodexBackend := baseURL == "" && auth.IsCodexOAuthToken(apiKey)
	if useCodexBackend && !isCodexBackendSupported(modelName) {
		return nil, fmt.Errorf(
			"model %q is not supported by the ChatGPT codex backend. "+
				"supported models: %s; "+
				"pick one of these or log in with a platform API key (sk-…) to use other models",
			modelName, strings.Join(codexBackendSupportedModels, ", "),
		)
	}
	if useCodexBackend {
		opts = append(opts, option.WithBaseURL(codexBackendBaseURL))
		if accountID := extractChatGPTAccountID(apiKey); accountID != "" {
			opts = append(opts, option.WithHeader("chatgpt-account-id", accountID))
		}
		opts = append(opts,
			option.WithHeader("originator", "pi-go"),
			option.WithHeader("OpenAI-Beta", "responses=experimental"),
		)
	} else if baseURL != "" {
		baseURL = normalizeOpenAIBaseURL(baseURL)
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	// Install a transport that captures 4xx/5xx response bodies from the
	// codex backend so stream errors surface the real OpenAI rejection
	// reason (the openai-go streaming error strips the body by default).
	var baseTransport http.RoundTripper
	if llmOpts != nil {
		for k, v := range llmOpts.ExtraHeaders {
			opts = append(opts, option.WithHeader(k, v))
		}
		var err error
		baseTransport, err = BuildTransport(llmOpts)
		if err != nil {
			return nil, err
		}
	}
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	baseTransport = &errorBodyLoggingTransport{base: baseTransport}
	opts = append(opts, option.WithHTTPClient(&http.Client{Transport: baseTransport}))
	client := openai.NewClient(opts...)
	maxOutputTokens := defaultOaiMaxOutputTokens
	if llmOpts != nil && llmOpts.MaxOutputTokens > 0 {
		maxOutputTokens = llmOpts.MaxOutputTokens
	}
	return &openaiModel{
		modelName:       modelName,
		client:          client,
		codexBackend:    useCodexBackend,
		maxOutputTokens: maxOutputTokens,
		responseState:   nil, // determined per-call based on model
	}, nil
}

func (m *openaiModel) Name() string { return m.modelName }

func normalizeOpenAIBaseURL(baseURL string) string {
	baseURL = strings.TrimRight(normalizeBaseURL(strings.TrimSpace(baseURL)), "/")
	if baseURL == "" {
		return ""
	}
	lower := strings.ToLower(baseURL)
	if strings.HasSuffix(lower, "/v1") || strings.Contains(lower, "/v1/") {
		return baseURL
	}
	return baseURL + "/v1"
}

// endpointMode returns whether to use Responses or Chat Completions for this model.
// Responses is used for: Codex models (Responses-only), any model with an active
// multi-turn previous_response_id. Chat Completions is used for GPT-4, o1, and
// other models that support it and have no multi-turn state.
func (m *openaiModel) endpointMode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.codexBackend {
		// The ChatGPT backend only exposes /codex/responses.
		return "responses"
	}
	if modelNeedsResponses(m.modelName) {
		return "responses"
	}
	if m.responseState != nil && m.responseState.previousResponseID != "" {
		return "responses"
	}
	return "chat"
}

func (m *openaiModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	modelName := req.Model
	if modelName == "" {
		modelName = m.modelName
	}

	mode := m.endpointMode()
	if mode == "responses" || modelNeedsResponses(modelName) {
		return m.generateResponses(ctx, req, modelName, stream)
	}
	return m.generateChat(ctx, req, modelName, stream)
}

// oaiFinishReasonToGenai maps finish reasons to genai.FinishReason.
// oaiFinishReasonToGenai maps a Chat Completions finish_reason onto
// genai.FinishReason. It serves every provider on this path, so it carries the
// union of their vocabularies: OpenAI's stop/length/content_filter/tool_calls
// plus Mistral's model_length and error (its full enum is
// stop|length|model_length|error|tool_calls — mistralai/client-python,
// ChatCompletionChoiceFinishReason).
//
// model_length and error must not fall through to Stop. The TUI warns about a
// truncated reply by watching for FinishReasonMaxTokens
// (tui/agent_loop.go:1014), so a Mistral turn cut off at the context limit
// would otherwise be presented as a complete one — a turn that just looks
// short. error means generation failed partway; reporting that as a clean stop
// claims a success the provider never gave.
func oaiFinishReasonToGenai(reason string) genai.FinishReason {
	switch reason {
	case "length", "model_length":
		return genai.FinishReasonMaxTokens
	case "content_filter":
		return genai.FinishReasonSafety
	case "error":
		return genai.FinishReasonOther
	default:
		// stop and tool_calls both end the turn cleanly; an unrecognized
		// value from an open enum is treated the same way.
		return genai.FinishReasonStop
	}
}

// oaiFunctionResponseContent extracts the string payload of a genai.FunctionResponse.
// Used by both Chat Completions and Responses paths to serialize tool outputs.
func oaiFunctionResponseContent(resp any) string {
	if resp == nil {
		return ""
	}
	if s, ok := resp.(string); ok {
		return s
	}
	if m, ok := resp.(map[string]any); ok {
		if c, ok := m["content"].([]any); ok && len(c) > 0 {
			if item, ok := c[0].(map[string]any); ok {
				if t, ok := item["text"].(string); ok {
					return t
				}
			}
		}
		if r, ok := m["result"].(string); ok {
			return r
		}
	}
	b, _ := json.Marshal(resp)
	return string(b)
}
