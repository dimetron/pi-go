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
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"google.golang.org/adk/v2/model"
)

const xaiDefaultBaseURL = "https://api.x.ai/v1"

// xaiConversationHeader is xAI's cache-affinity hint. xAI routes every request
// carrying the same value to the same server, which is what makes a prompt
// cache hit likely; without it a multi-turn session lands on a cache-cold
// server and pays full input price on the whole prefix every turn. The
// Responses API also accepts prompt_cache_key; we keep the header because it
// is what the Chat Completions docs named and gateways already know.
const xaiConversationHeader = "x-grok-conv-id"

// xaiModel implements model.LLM for the xAI (Grok) API.
//
// xAI's recommended surface is the OpenAI-compatible Responses API
// (/v1/responses). Chat Completions still works for function calling, but
// server-side tools (web_search, x_search, code_interpreter) only run on
// Responses — that is the loop the Python SDK's server_side_tools.py example
// drives. This type embeds openaiModel so it can reuse the Responses stream
// and non-stream runners; the xAI-specific pieces are the conversation
// header, the extra reasoning_effort tier, and the built-in tools.
type xaiModel struct {
	openaiModel
	// reasoningEffort is the resolved thinking level, empty when the level is
	// unset or unrecognized so the field is left off the wire entirely.
	reasoningEffort shared.ReasoningEffort
	enableXAITools  bool
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
		openaiModel: openaiModel{
			modelName: modelName,
			client:    client,
		},
		enableXAITools:  xaiToolsEnabled(llmOpts != nil && llmOpts.EnableXAITools),
		reasoningEffort: xaiReasoningEffort(thinkingLevel),
	}, nil
}

func (m *xaiModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if req == nil {
			_ = yield(nil, fmt.Errorf("xAI responses: nil LLM request"))
			return
		}
		params, err := m.buildXAIResponsesParams(req)
		if err != nil {
			_ = yield(nil, err)
			return
		}

		// send performs the request once. It takes its own yield so retryStream
		// can re-run it across attempts.
		send := func(y func(*model.LLMResponse, error) bool) {
			m.sendXAIResponses(ctx, params, stream, y)
		}

		if !stream {
			send(yield)
			return
		}
		retryStream(ctx, streamRetryConfig(), yield, send)
	}
}

// buildXAIResponsesParams assembles the Responses request for one turn.
func (m *xaiModel) buildXAIResponsesParams(req *model.LLMRequest) (responses.ResponseNewParams, error) {
	input, instructions, err := oaiContentsToResponsesInput(req.Contents, req.Config)
	if err != nil {
		return responses.ResponseNewParams{}, fmt.Errorf("xAI responses input: %w", err)
	}

	modelName := req.Model
	if modelName == "" {
		modelName = m.modelName
	}

	params := responses.ResponseNewParams{
		Model: modelName,
		Input: input,
		// Match the OpenAI Responses default: do not persist the turn
		// server-side. Multi-turn continues via the full conversation
		// replayed in params.Input.
		Store: param.NewOpt(false),
	}
	if instructions != "" {
		params.Instructions = param.NewOpt(instructions)
	}
	if tools := xaiRequestTools(req, m.enableXAITools); len(tools) > 0 {
		params.Tools = tools
	}
	if m.reasoningEffort != "" && xaiModelReasons(modelName) {
		params.Reasoning = shared.ReasoningParam{Effort: m.reasoningEffort}
	}
	return params, nil
}

// sendXAIResponses runs one Responses request and reports a failure in the shape
// the caller's mode expects: a STREAM_ERROR response while streaming, so the
// turn ends cleanly, and a yielded error otherwise.
//
// The name keeps it distinct from the embedded openaiModel's sendResponses,
// which it does not replace: that one also recovers from a rejected
// previous_response_id, and xAI requests never carry one.
func (m *xaiModel) sendXAIResponses(ctx context.Context, params responses.ResponseNewParams, stream bool, y func(*model.LLMResponse, error) bool) {
	var err error
	if stream {
		_, err = m.runResponsesStreaming(ctx, params, y)
	} else {
		_, err = m.runResponsesNonStreaming(ctx, params, y)
	}
	if err == nil {
		return
	}
	if stream {
		_ = y(&model.LLMResponse{ErrorCode: "STREAM_ERROR", ErrorMessage: err.Error()}, nil)
		return
	}
	_ = y(nil, fmt.Errorf("xAI Responses API failed: %w", err))
}

// xaiRequestTools is the function declarations from the ADK request plus
// xAI's built-in server-side tools. The built-ins run inside the request
// (the model searches / executes and keeps going); client-side functions
// still come back as FunctionCalls for pi's own loop to execute.
func xaiRequestTools(req *model.LLMRequest, enabled bool) []responses.ToolUnionParam {
	var tools []responses.ToolUnionParam
	if req != nil && req.Config != nil && len(req.Config.Tools) > 0 {
		tools = oaiGenaiToolsToResponses(req.Config.Tools)
	}
	if enabled && !xaiToolsDisabled() {
		tools = append(tools, xaiServerSideTools()...)
	}
	return tools
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
