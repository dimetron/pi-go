package provider

import (
	"context"
	"fmt"
	"iter"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
	"google.golang.org/adk/v2/model"
)

const xaiDefaultBaseURL = "https://api.x.ai/v1"

// xaiConversationHeader is xAI's cache-affinity hint for the Chat Completions
// endpoint. xAI routes every request carrying the same value to the same
// server, which is what makes a prompt cache hit likely; without it a
// multi-turn session lands on a cache-cold server and pays full input price on
// the whole prefix every turn. (The Responses API spells the same thing
// `prompt_cache_key`.)
const xaiConversationHeader = "x-grok-conv-id"

// xaiModel implements model.LLM for the xAI (Grok) API.
//
// xAI exposes an OpenAI-compatible chat completions endpoint, so this reuses
// the OpenAI SDK and the shared oai* request/response helpers against
// api.x.ai. Two things are xAI's own: the conversation header above, and
// reasoning_effort, which Grok reasoning models accept with an extra "xhigh"
// tier above OpenAI's.
type xaiModel struct {
	modelName string
	client    openai.Client
	// reasoningEffort is the resolved thinking level, empty when the level is
	// unset or unrecognized so the field is left off the wire entirely.
	reasoningEffort shared.ReasoningEffort
}

// NewXAI creates an xAI model.LLM.
// If baseURL is empty, the default xAI API endpoint is used.
// thinkingLevel controls reasoning effort: "none", "low", "medium", "high", "max".
func NewXAI(_ context.Context, modelName, apiKey, baseURL, thinkingLevel string, llmOpts *LLMOptions) (model.LLM, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("xAI API key is required (set XAI_API_KEY)")
	}
	if baseURL == "" {
		baseURL = xaiDefaultBaseURL
	}
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
		// One id per model instance, which is one id per pi session: every
		// turn of a conversation shares a prefix, and that is exactly the
		// scope xAI's cache is keyed on.
		option.WithHeader(xaiConversationHeader, uuid.NewString()),
	}
	if llmOpts != nil {
		// Applied after the conversation header so an explicit --header can
		// override it — a gateway in front of xAI may want to supply its own.
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
	return &xaiModel{
		modelName:       modelName,
		client:          client,
		reasoningEffort: xaiReasoningEffort(thinkingLevel),
	}, nil
}

func (m *xaiModel) Name() string { return m.modelName }

func (m *xaiModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
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

		if m.reasoningEffort != "" && xaiModelReasons(modelName) {
			params.ReasoningEffort = m.reasoningEffort
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

// xaiReasoningEffort maps pi's thinking level onto xAI's reasoning_effort.
//
// Grok's reasoning models have no off switch — the parameter's lowest tier is
// "low", and omitting it altogether leaves xAI's own default of "high" in
// force. "none" therefore maps to "low", the closest the API can get to what
// was asked for; leaving it off would spend the most tokens of any option,
// which is the opposite of the request. An unrecognized level returns "" so
// the field is omitted and the model default stands.
func xaiReasoningEffort(level string) shared.ReasoningEffort {
	switch level {
	case "none", "low":
		return shared.ReasoningEffortLow
	case "medium":
		return shared.ReasoningEffortMedium
	case "high":
		return shared.ReasoningEffortHigh
	case "max", "xhigh":
		return shared.ReasoningEffortXhigh
	default:
		return ""
	}
}

// xaiModelReasons reports whether a Grok model accepts reasoning_effort.
//
// xAI ships explicitly non-reasoning variants of some models
// (grok-4.20-0309-non-reasoning), and those reject the parameter outright
// rather than ignoring it. The name is the only signal available before the
// first request.
func xaiModelReasons(modelName string) bool {
	return !strings.Contains(strings.ToLower(modelName), "non-reasoning")
}
