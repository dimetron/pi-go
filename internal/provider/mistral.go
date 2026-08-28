package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"google.golang.org/adk/v2/model"
)

const mistralDefaultBaseURL = "https://api.mistral.ai/v1"

// mistralModel implements model.LLM for the Mistral API.
// Mistral exposes an OpenAI-compatible chat completions endpoint,
// so we reuse the OpenAI SDK with a custom base URL.
type mistralModel struct {
	modelName string
	client    openai.Client
	// thinkingLevel is stored verbatim ("none", "low", "medium", "high",
	// "max"); it is mapped onto Mistral's wire vocabulary per request,
	// because which field carries it depends on the model.
	thinkingLevel string
	// promptCacheKey keys Mistral's prompt cache. Mistral bills cached input
	// tokens at a discount and routes on this value, so one key per model
	// instance — which is one key per pi session — keeps every turn of a
	// conversation on the same cached prefix (the xAI precedent, xai.go:59).
	promptCacheKey string
}

// NewMistral creates a Mistral model.LLM.
// If baseURL is empty, the default Mistral API endpoint is used.
// thinkingLevel controls reasoning: "none", "low", "medium", "high", "max".
// It reaches the wire as `reasoning_effort` on the models that accept it and
// as `prompt_mode` on the magistral family; empty leaves the model's default.
func NewMistral(_ context.Context, modelName, apiKey, baseURL, thinkingLevel string, llmOpts *LLMOptions) (model.LLM, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("mistral API key is required (set MISTRAL_API_KEY)")
	}
	if baseURL == "" {
		baseURL = mistralDefaultBaseURL
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
	return &mistralModel{
		modelName:      modelName,
		client:         client,
		thinkingLevel:  thinkingLevel,
		promptCacheKey: uuid.NewString(),
	}, nil
}

func (m *mistralModel) Name() string { return m.modelName }

func (m *mistralModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
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

		// openai-go has no fields for prompt_cache_key, reasoning_effort or
		// prompt_mode, so they go on the wire as extra JSON fields. One call
		// with one map: SetExtraFields replaces the whole map rather than
		// merging into it (param.metadata.SetExtraFields).
		params.SetExtraFields(mistralExtraFields(modelName, m.thinkingLevel, m.promptCacheKey))

		if stream {
			retryStream(ctx, streamRetryConfig(), yield, func(y func(*model.LLMResponse, error) bool) {
				oaiRunStreamingHooks(ctx, &m.client, params, y, mistralHooks)
			})
		} else {
			oaiRunNonStreamingHooks(ctx, &m.client, params, yield, mistralHooks)
		}
	}
}

// mistralHooks teaches the shared Chat Completions runners the two places
// Mistral puts data the openai-go SDK cannot represent: thinking chunks and an
// answer that arrives as a JSON array rather than a string.
var mistralHooks = oaiExtractHooks{
	deltaThinking:   mistralDeltaThinking,
	messageThinking: mistralMessageThinking,
	answerText:      mistralAnswerText,
}

// mistralExtraFields builds the Mistral-specific request body fields for one
// call: the prompt cache key always, plus whichever reasoning control this
// model understands.
func mistralExtraFields(modelName, thinkingLevel, promptCacheKey string) map[string]any {
	extra := map[string]any{"prompt_cache_key": promptCacheKey}
	switch {
	case mistralUsesReasoningEffort(modelName):
		if effort := mistralReasoningEffort(thinkingLevel); effort != "" {
			extra["reasoning_effort"] = effort
		}
	case mistralUsesPromptMode(modelName):
		// prompt_mode has no "off" value — the field is simply omitted when
		// thinking is not wanted, which leaves the model's default in force.
		if level := strings.ToLower(strings.TrimSpace(thinkingLevel)); level != "" && level != "none" {
			extra["prompt_mode"] = "reasoning"
		}
	}
	return extra
}

// mistralUsesReasoningEffort reports whether a model id takes reasoning_effort
// rather than prompt_mode. Mistral documents the parameter for the small and
// medium reasoning models only.
func mistralUsesReasoningEffort(modelName string) bool {
	switch strings.ToLower(strings.TrimSpace(modelName)) {
	case "mistral-small-2603", "mistral-small-latest", "mistral-medium-3.5":
		return true
	default:
		return false
	}
}

// mistralUsesPromptMode reports whether a model is a reasoning model that
// controls thinking through prompt_mode:"reasoning" — the magistral family.
func mistralUsesPromptMode(modelName string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(modelName)), "magistral")
}

// mistralReasoningEffort maps pi's thinking level onto Mistral's
// reasoning_effort. Mistral documents exactly two values — "high" (emit a full
// thinking chunk) and "none" (think minimally, omit the chunk) — so every
// active level collapses onto "high"; there is no low/medium tier to map onto.
// An empty or unrecognized level returns "" so the field is omitted and the
// model's own default applies.
func mistralReasoningEffort(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "none", "off":
		return "none"
	case "low", "medium", "high", "max", "xhigh":
		return "high"
	default:
		return ""
	}
}

// Mistral reasoning models do not use delta.reasoning the way OpenRouter does.
// Thinking arrives inside the content field, which switches from a string to a
// list of typed chunks for the duration of the thinking phase:
//
//	[{"type":"thinking","thinking":[{"type":"text","text":"..."}]},
//	 {"type":"text","text":"..."}]
//
// openai-go declares Content as a plain string, and its decoder leaves the raw
// array text in that string rather than failing, so both the thinking and the
// answer have to be recovered from the JSON by hand.
// See https://docs.mistral.ai/studio-api/conversations/reasoning.

// mistralContentChunk is one element of a Mistral content array.
type mistralContentChunk struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"thinking"`
}

// mistralParseContentChunks parses a Mistral content payload into its chunks.
// ok is false when the payload is not a Mistral chunk array — a plain answer
// string, malformed JSON, or an array carrying no chunk type this code knows —
// so callers can leave such content exactly as it arrived.
func mistralParseContentChunks(content []byte) ([]mistralContentChunk, bool) {
	trimmed := strings.TrimSpace(string(content))
	if !strings.HasPrefix(trimmed, "[") {
		return nil, false
	}
	var chunks []mistralContentChunk
	if err := json.Unmarshal([]byte(trimmed), &chunks); err != nil {
		return nil, false
	}
	for _, c := range chunks {
		if c.Type == "thinking" || c.Type == "text" {
			return chunks, true
		}
	}
	return nil, false
}

// mistralChunksThinking concatenates the thinking text carried by a chunk list.
func mistralChunksThinking(chunks []mistralContentChunk) string {
	var sb strings.Builder
	for _, c := range chunks {
		if c.Type != "thinking" {
			continue
		}
		for _, t := range c.Thinking {
			sb.WriteString(t.Text)
		}
	}
	return sb.String()
}

// mistralAnswerText recovers the answer text from the content string openai-go
// decoded. Plain answers pass through untouched; a chunk array is reduced to
// its "text" chunks, which is what keeps the raw JSON out of the transcript
// once a thinking chunk has turned content into a list.
func mistralAnswerText(content string) string {
	chunks, ok := mistralParseContentChunks([]byte(content))
	if !ok {
		return content
	}
	var sb strings.Builder
	for _, c := range chunks {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String()
}

// mistralDeltaThinking extracts the thinking text from one streaming chunk's
// raw JSON. Returns "" when the chunk carries none.
func mistralDeltaThinking(rawChunk string) string {
	var payload struct {
		Choices []struct {
			Delta struct {
				Content json.RawMessage `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(rawChunk), &payload); err != nil {
		return ""
	}
	if len(payload.Choices) == 0 {
		return ""
	}
	chunks, ok := mistralParseContentChunks(payload.Choices[0].Delta.Content)
	if !ok {
		return ""
	}
	return mistralChunksThinking(chunks)
}

// mistralMessageThinking extracts the thinking text from a non-streaming
// completion's raw JSON. Returns "" when the response carries none.
func mistralMessageThinking(rawResponse string) string {
	var payload struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(rawResponse), &payload); err != nil {
		return ""
	}
	if len(payload.Choices) == 0 {
		return ""
	}
	chunks, ok := mistralParseContentChunks(payload.Choices[0].Message.Content)
	if !ok {
		return ""
	}
	return mistralChunksThinking(chunks)
}
