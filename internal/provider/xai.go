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

	"github.com/dimetron/pi-go/internal/auth"
)

const xaiDefaultBaseURL = "https://api.x.ai/v1"

// xaiSubscriptionBaseURL is the Grok CLI chat proxy, the surface that honors
// a SuperGrok / X Premium+ subscription bearer. The per-token developer API at
// xaiDefaultBaseURL rejects that token with HTTP 402, so the two are not
// interchangeable. See internal/auth/xai.go for the credential half.
const xaiSubscriptionBaseURL = "https://cli-chat-proxy.grok.com/v1"

// xaiSubscriptionHeaders are the Grok-CLI identity headers the chat proxy
// requires on a subscription request. They mirror what the official `grok`
// CLI sends; the client version is the minimum the proxy accepts.
func xaiSubscriptionHeaders() []option.RequestOption {
	return []option.RequestOption{
		option.WithHeader("X-XAI-Token-Auth", "xai-grok-cli"),
		option.WithHeader("x-grok-client-identifier", "grok-shell"),
		option.WithHeader("x-grok-client-version", xaiClientVersion),
	}
}

// xaiClientVersion is the Grok CLI version advertised in
// xaiSubscriptionHeaders. The proxy rejects older clients outright.
const xaiClientVersion = "0.2.93"

// StoredSubscriptionCredential returns a credential held outside the
// provider's environment variable, for providers that persist OAuth material
// separately. Only xAI needs it today: its access token lives in
// ~/.pi-go/xai_auth.json so the refresh token can be kept alongside it, while
// the env var may hold either a developer key or an access token.
//
// Callers that gate on "is a key present" consult this before declaring a
// provider unconfigured — without it a subscription-only setup is rejected
// with "set XAI_API_KEY" even though a usable credential exists.
//
// The returned value is a token that may be expired; refresh happens later, in
// resolveXAIEndpoint, so an expired-but-refreshable credential must still be
// reported as present here.
func StoredSubscriptionCredential(providerName string) string {
	if providerName != "xai" {
		return ""
	}
	set, err := auth.LoadXAITokens()
	if err != nil || set == nil {
		return ""
	}
	return set.AccessToken
}

// resolveXAIEndpoint decides which xAI surface a credential belongs to and
// returns the key, base URL and whether the credential is a subscription
// token.
//
// The two surfaces are not interchangeable. A per-token developer key
// (xai-…) belongs to api.x.ai. A SuperGrok / X Premium+ OAuth access token
// belongs to the Grok CLI chat proxy: api.x.ai bills it against prepaid
// credits and answers HTTP 402 `personal-team-blocked:spending-limit`.
//
// An explicit baseURL is authoritative and suppresses the reroute entirely —
// the caller is stating which surface the token belongs to (a gateway, a
// self-hosted proxy, or a test server). The credential is still resolved, so
// a subscription login works against a caller-named host, and the
// subscription flag stays set so the Grok-CLI identity headers are sent.
func resolveXAIEndpoint(ctx context.Context, apiKey, baseURL string) (key, resolvedBaseURL string, subscription bool, err error) {
	explicitBaseURL := baseURL != ""

	// A developer API key passed by the caller is used as-is; refresh must
	// never second-guess an operator's configured credential.
	//
	// Subscription tokens are different: the CLI and the token loader both
	// hand us the value from XAI_API_KEY, which may be an access token that
	// expired up to six hours after it was written to ~/.pi-go/.env. That
	// value must go through resolution so the stored refresh token can renew
	// it — using it directly would 401 after the first six hours.
	useExplicitKey := apiKey != "" && !auth.IsXAISubscriptionToken(apiKey)
	if useExplicitKey {
		subscription = false
	} else {
		resolved, isSub, resolveErr := auth.ResolveXAICredential(ctx)
		if resolveErr != nil {
			return "", "", false, resolveErr
		}
		if resolved != "" {
			apiKey, subscription = resolved, isSub
		} else if apiKey != "" {
			// Resolution found nothing (no store, unreadable file); fall back
			// to the caller's value rather than reporting no credential.
			subscription = auth.IsXAISubscriptionToken(apiKey)
		}
	}

	if apiKey == "" {
		return "", "", false, fmt.Errorf(
			"xAI API key is required (set XAI_API_KEY, or run `pi login xai` to use a SuperGrok subscription)")
	}

	switch {
	case explicitBaseURL:
		resolvedBaseURL = baseURL
	case subscription:
		resolvedBaseURL = xaiSubscriptionBaseURL
	default:
		resolvedBaseURL = xaiDefaultBaseURL
	}
	return apiKey, resolvedBaseURL, subscription, nil
}

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
	// A credential resolved here may be either a per-token developer API key
	// or a subscription OAuth access token. They are not interchangeable:
	// api.x.ai bills a subscription bearer against prepaid credits and answers
	// HTTP 402 `personal-team-blocked:spending-limit`, while the Grok CLI chat
	// proxy accepts it and draws on the subscription's weekly pool instead.
	// Resolve once, here, so the choice of endpoint and headers stays
	// consistent for the life of the model instance.
	//
	// An explicit baseURL always wins: it is the caller saying "I know which
	// surface this token belongs to" (a gateway, a self-hosted proxy, or a
	// deliberate test).
	apiKey, baseURL, subscription, err := resolveXAIEndpoint(context.Background(), apiKey, baseURL)
	if err != nil {
		return nil, err
	}
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
		// One id per model instance, which is one id per pi session: every
		// turn of a conversation shares a prefix, and that is exactly the
		// scope xAI's cache is keyed on.
		option.WithHeader(xaiConversationHeader, uuid.NewString()),
	}
	if subscription {
		// The CLI proxy gates on Grok-CLI identity headers: without them it
		// answers as though the caller were an unentitled API client, which
		// surfaces as a 403 that reads like a subscription problem. Sent
		// before ExtraHeaders so an explicit --header can still override.
		opts = append(opts, xaiSubscriptionHeaders()...)
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
