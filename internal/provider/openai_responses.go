package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// modelNeedsResponses returns true for models that only support the Responses API.
func modelNeedsResponses(modelName string) bool {
	lower := strings.ToLower(modelName)
	// Responses-only model families.
	responsesOnly := []string{
		"gpt-5-codex", "gpt-5.1-codex", "gpt-5.1-codex-mini", "gpt-5.1-codex-max",
		"gpt-5.2-codex", "gpt-5.3-codex", "gpt-5.3-codex-spark", "gpt-5.4-codex", "gpt-5.5-codex",
		"gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra",
	}
	for _, m := range responsesOnly {
		if strings.HasPrefix(lower, m) {
			return true
		}
	}
	return false
}

// generateResponses implements the Responses API path (required for Codex models,
// supports reasoning effort, stateful multi-turn via previous_response_id).
func (m *openaiModel) generateResponses(ctx context.Context, req *model.LLMRequest, modelName string, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.mu.Lock()
		state := m.responseState
		m.mu.Unlock()

		input, instructions := oaiContentsToResponsesInput(req.Contents, req.Config)

		params, sentPreviousResponseID := m.buildResponsesParams(req, modelName, input, instructions, state)

		// send performs the request once, including the previous_response_id
		// recovery. It takes its own yield so retryStream can re-run it, and
		// params by pointer so that clearing a rejected previous_response_id
		// persists into any later attempt retryStream makes.
		send := func(y func(*model.LLMResponse, error) bool) {
			m.sendResponses(ctx, &params, stream, sentPreviousResponseID, y)
		}

		if stream {
			retryStream(ctx, streamRetryConfig(), yield, send)
			return
		}
		send(yield)
	}
}

// buildResponsesParams assembles the parameters for one Responses call. It
// also reports whether a previous_response_id was attached, which the caller
// needs in order to decide whether a rejection is worth retrying without it.
func (m *openaiModel) buildResponsesParams(
	req *model.LLMRequest,
	modelName string,
	input responses.ResponseNewParamsInputUnion,
	instructions string,
	state *responsesState,
) (responses.ResponseNewParams, bool) {
	params := responses.ResponseNewParams{
		Model: modelName,
		Input: input,
		// Default to store=false so we never persist LLM responses
		// server-side without an explicit opt-in. Multi-turn continues
		// to work on the platform Responses API via full conversation
		// replay (params.Input carries the whole thread).
		Store: param.NewOpt(false),
	}
	if instructions != "" {
		params.Instructions = param.NewOpt(instructions)
	}

	// The ChatGPT codex backend is stateless — it rejects requests that
	// expect server-side persistence and requires clients to opt in to
	// encrypted reasoning echo so multi-turn context can round-trip on
	// the client side. Matches pi-mono's openai-codex-responses body.
	if m.codexBackend {
		params.Include = []responses.ResponseIncludable{
			responses.ResponseIncludableReasoningEncryptedContent,
		}
	}

	// previous_response_id requires OpenAI to retain responses
	// server-side, which conflicts with store=false. Skip threading
	// the pointer; the full conversation in params.Input is sufficient.
	sentPreviousResponseID := shouldSendPreviousResponseID(params.Store.Value, m.codexBackend, state)
	if sentPreviousResponseID {
		params.PreviousResponseID = param.NewOpt(state.previousResponseID)
	}

	if req.Config != nil && len(req.Config.Tools) > 0 {
		params.Tools = oaiGenaiToolsToResponses(req.Config.Tools)
	}

	if effort, ok := responsesReasoningEffort(req.Config); ok {
		params.Reasoning = shared.ReasoningParam{Effort: effort}
	}

	return params, sentPreviousResponseID
}

// responsesReasoningEffort maps a configured thinking budget onto a Responses
// reasoning effort, reporting false when no budget is set or the budget is not
// positive — in which case the request carries no reasoning parameter at all.
// Low tokens (100-500) → low effort; medium (2000-4000) → medium; high (8000+) → high.
func responsesReasoningEffort(config *genai.GenerateContentConfig) (shared.ReasoningEffort, bool) {
	if config == nil || config.ThinkingConfig == nil || config.ThinkingConfig.ThinkingBudget == nil {
		return "", false
	}
	switch bt := *config.ThinkingConfig.ThinkingBudget; {
	case bt >= 8000:
		return shared.ReasoningEffortHigh, true
	case bt >= 2000:
		return shared.ReasoningEffortMedium, true
	case bt > 0:
		return shared.ReasoningEffortLow, true
	default:
		return "", false
	}
}

// sendResponses performs one Responses request, recovering from a rejected
// previous_response_id. params is a pointer because clearing that pointer must
// persist into any later attempt the caller makes.
func (m *openaiModel) sendResponses(
	ctx context.Context,
	params *responses.ResponseNewParams,
	stream, sentPreviousResponseID bool,
	y func(*model.LLMResponse, error) bool,
) {
	runOnce := func() (bool, error) {
		if stream {
			return m.runResponsesStreaming(ctx, *params, y)
		}
		return m.runResponsesNonStreaming(ctx, *params, y)
	}

	emitted, err := runOnce()

	// A stored previous_response_id can stop resolving upstream at any
	// point: a proxy or load balancer routing the next turn to a different
	// deployment, `store=false` (zero-data-retention accounts), expiry, or
	// a model switch. The full conversation is already in params.Input —
	// previous_response_id is only an optimisation here — so retrying
	// without it is lossless. Only retry when nothing was streamed yet,
	// otherwise the caller would see the turn's text twice.
	if err != nil && sentPreviousResponseID && !emitted && isPreviousResponseNotFound(err) {
		m.clearPreviousResponseID()
		params.PreviousResponseID = param.Opt[string]{}
		// The retry is the last attempt, so its emitted flag is moot.
		_, err = runOnce()
	}

	if err == nil {
		return
	}

	// Clear the stale pointer so the next turn starts fresh rather
	// than replaying the same rejected id.
	m.clearPreviousResponseID()
	if stream {
		_ = y(&model.LLMResponse{ErrorCode: "STREAM_ERROR", ErrorMessage: err.Error()}, nil)
		return
	}
	_ = y(nil, fmt.Errorf("OpenAI Responses API failed: %w", err))
}

// shouldSendPreviousResponseID reports whether a retained response pointer can
// be used for the request. Stateless and Codex backends must replay the input.
func shouldSendPreviousResponseID(store, codexBackend bool, state *responsesState) bool {
	return store && !codexBackend && state != nil && state.previousResponseID != ""
}

// isPreviousResponseNotFound reports whether err is the upstream's rejection of
// a previous_response_id it cannot resolve. The structured API error is checked
// first (code, then param); the string fallback covers proxies that return an
// error envelope the SDK cannot decode into openai.Error, in which case only
// the raw body survives in the message.
func isPreviousResponseNotFound(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		if apiErr.Code == "previous_response_not_found" || apiErr.Param == "previous_response_id" {
			return true
		}
		if strings.Contains(strings.ToLower(apiErr.RawJSON()), "previous_response_not_found") {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "previous_response_not_found") ||
		(strings.Contains(msg, "previous_response_id") && strings.Contains(msg, "not found"))
}

// clearPreviousResponseID drops the stored server-side conversation pointer.
func (m *openaiModel) clearPreviousResponseID() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.responseState != nil {
		m.responseState.previousResponseID = ""
	}
}

// oaiContentsToResponsesInput converts ADK contents to Responses API input and instructions.
// Always returns list form — the ChatGPT codex backend rejects bare strings with
// `{"detail":"Input must be a list"}`, and the platform Responses API accepts lists too.
func oaiContentsToResponsesInput(contents []*genai.Content, config *genai.GenerateContentConfig) (responses.ResponseNewParamsInputUnion, string) {
	instructions := genaiSystemInstruction(config)
	callIDs, responseIDs := oaiResponsesPairedIDs(contents)

	// Always build a list of input items. The ChatGPT codex backend
	// (/backend-api/codex/responses) rejects a bare string with
	// `{"detail":"Input must be a list"}`. The platform Responses API
	// (/v1/responses) accepts either form, so the list shape is safe
	// for both endpoints.
	items := make(responses.ResponseInputParam, 0, len(contents))
	for _, content := range contents {
		if content == nil || strings.TrimSpace(content.Role) == "system" {
			continue
		}
		items = oaiAppendResponsesItems(items, content, callIDs, responseIDs)
	}

	return responses.ResponseNewParamsInputUnion{OfInputItemList: items}, instructions
}

// oaiResponsesPairedIDs indexes the function call and function response IDs
// present in contents, so the item builder can tell a complete call/result
// pair from a half of one. A canceled turn can persist a function call before
// its tool result is emitted, and Responses requires calls and outputs to be
// paired, so replaying history must omit whichever half has no partner.
// Blank IDs are ignored: they cannot pair with anything.
func oaiResponsesPairedIDs(contents []*genai.Content) (callIDs, responseIDs map[string]struct{}) {
	callIDs = make(map[string]struct{})
	responseIDs = make(map[string]struct{})
	for _, content := range contents {
		if content == nil {
			continue
		}
		for _, part := range content.Parts {
			if part == nil {
				continue
			}
			if part.FunctionCall != nil {
				if id := strings.TrimSpace(part.FunctionCall.ID); id != "" {
					callIDs[id] = struct{}{}
				}
			}
			if part.FunctionResponse != nil {
				if id := strings.TrimSpace(part.FunctionResponse.ID); id != "" {
					responseIDs[id] = struct{}{}
				}
			}
		}
	}
	return callIDs, responseIDs
}

// oaiAppendResponsesItems appends one content's parts to the Responses input
// list and returns the grown list. Consecutive text parts coalesce into a
// single message item, flushed before each function call or output so the
// emitted items keep the order the parts appeared in. An unpaired call or
// output is skipped — see oaiResponsesPairedIDs.
func oaiAppendResponsesItems(
	items responses.ResponseInputParam,
	content *genai.Content,
	callIDs, responseIDs map[string]struct{},
) responses.ResponseInputParam {
	role := oaiResponsesRole(content.Role)
	var textParts []string
	flushText := func() {
		if len(textParts) == 0 {
			return
		}
		items = append(items, responses.ResponseInputItemParamOfMessage(strings.Join(textParts, "\n"), role))
		textParts = nil
	}

	for _, part := range content.Parts {
		if part == nil {
			continue
		}
		switch {
		case part.Text != "":
			textParts = append(textParts, part.Text)
		case part.FunctionCall != nil:
			flushText()
			fc := part.FunctionCall
			if strings.TrimSpace(fc.ID) == "" {
				continue
			}
			if _, ok := responseIDs[fc.ID]; !ok {
				continue
			}
			argsJSON, _ := json.Marshal(fc.Args)
			items = append(items, responses.ResponseInputItemParamOfFunctionCall(string(argsJSON), fc.ID, fc.Name))
		case part.FunctionResponse != nil:
			flushText()
			fr := part.FunctionResponse
			if strings.TrimSpace(fr.ID) == "" {
				continue
			}
			if _, ok := callIDs[fr.ID]; !ok {
				continue
			}
			items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(fr.ID, oaiFunctionResponseContent(fr.Response)))
		}
	}
	flushText()
	return items
}

func oaiResponsesRole(role string) responses.EasyInputMessageRole {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "model", "assistant":
		return responses.EasyInputMessageRoleAssistant
	case "developer":
		return responses.EasyInputMessageRoleDeveloper
	case "system":
		return responses.EasyInputMessageRoleSystem
	default:
		return responses.EasyInputMessageRoleUser
	}
}

// oaiGenaiToolsToResponses converts genai.Tool definitions to Responses API tool schema.
// Unlike Chat Completions (externally tagged {type:"function", function:{...}}),
// Responses uses a flat internally-tagged form: {type:"function", name:"...", parameters:...}.
func oaiGenaiToolsToResponses(tools []*genai.Tool) []responses.ToolUnionParam {
	var out []responses.ToolUnionParam
	for _, t := range tools {
		if t == nil || t.FunctionDeclarations == nil {
			continue
		}
		for _, fd := range t.FunctionDeclarations {
			if fd == nil {
				continue
			}
			paramsMap := oaiFunctionParameters(fd.ParametersJsonSchema)
			out = append(out, responses.ToolUnionParam{
				OfFunction: &responses.FunctionToolParam{
					Name:        fd.Name,
					Parameters:  paramsMap,
					Description: param.NewOpt(fd.Description),
					Strict:      param.NewOpt(false),
				},
			})
		}
	}
	return out
}

// responsesStreamState holds accumulated state from Responses streaming.
type responsesStreamState struct {
	text             string
	reasoning        strings.Builder
	toolCalls        map[int64]toolCallAcc
	finishReason     string
	promptTokens     int64
	completionTokens int64
	cachedTokens     int64
}

type toolCallAcc struct {
	id, name, arguments string
}

func updateResponsesToolCall(s *responsesStreamState, idx int64, id, name, arguments string, appendArguments bool) {
	acc := s.toolCalls[idx]
	if id != "" {
		acc.id = id
	}
	if name != "" {
		acc.name = name
	}
	if arguments != "" {
		if appendArguments {
			acc.arguments += arguments
		} else {
			acc.arguments = arguments
		}
	}
	s.toolCalls[idx] = acc
}

// runResponsesStreaming processes a streaming Responses call. It reports
// whether anything was yielded to the caller, and returns the transport error
// instead of surfacing it, so generateResponses can decide whether the call is
// safe to retry.
// The streaming protocol is documented at
// https://platform.openai.com/docs/guides/responses-streaming
func (m *openaiModel) runResponsesStreaming(ctx context.Context, params responses.ResponseNewParams, yield func(*model.LLMResponse, error) bool) (emitted bool, retErr error) {
	stream := m.client.Responses.NewStreaming(ctx, params)
	//nolint:errcheck // Close() may return error but we can't recover from it in defer
	defer stream.Close()

	state := &responsesStreamState{
		toolCalls: make(map[int64]toolCallAcc),
	}
	var finalResp *responses.Response

	for stream.Next() {
		evt := stream.Current()

		switch evt.Type {
		// response.completed — capture final response with usage and status.
		case "response.completed":
			finalResp = &evt.Response
			applyResponsesCompleted(state, &evt.Response)

		// response.error — surface as LLM error.
		case "error":
			_ = yield(&model.LLMResponse{ErrorCode: evt.Code, ErrorMessage: evt.Message}, nil)
			return true, nil

		// response.output_text.delta — text token.
		case "response.output_text.delta":
			state.text += evt.Delta
			emitted = true
			if !yield(responsesPartialResponse(string(genai.RoleModel), evt.Delta), nil) {
				return emitted, nil
			}

		// response.reasoning_text.delta — reasoning token.
		case "response.reasoning_text.delta":
			state.reasoning.WriteString(evt.Delta)
			emitted = true
			if !yield(responsesPartialResponse("thinking", evt.Delta), nil) {
				return emitted, nil
			}

		default:
			applyResponsesToolCallEvent(state, &evt)
		}
	}

	if err := stream.Err(); err != nil {
		if ctx.Err() == context.Canceled {
			_ = yield(canceledResponse(), nil)
			return emitted, nil
		}
		// Hand the error back to generateResponses: it owns the stale-id
		// retry and clears previousResponseID before surfacing anything.
		return emitted, err
	}

	m.finishResponsesStream(state, finalResp, yield)
	return true, nil
}

// applyResponsesCompleted folds the terminal response.completed event into the
// accumulated state. Zero-valued usage fields are left alone, so a backend
// that omits a count in the final event does not clear a value seen earlier.
func applyResponsesCompleted(s *responsesStreamState, resp *responses.Response) {
	if resp.Usage.InputTokens > 0 {
		s.promptTokens = resp.Usage.InputTokens
	}
	if resp.Usage.OutputTokens > 0 {
		s.completionTokens = resp.Usage.OutputTokens
	}
	if c := resp.Usage.InputTokensDetails.CachedTokens; c > 0 {
		s.cachedTokens = c
	}
	s.finishReason = string(resp.Status)
}

// applyResponsesToolCallEvent folds the four event types that carry function
// call data into the accumulator, ignoring every other event type. The call
// header (id, name) arrives on response.output_item.added; argument text
// streams in on response.function_call_arguments.delta, and both `.done`
// events supply an authoritative complete arguments string as a safety net
// for deltas that were missed.
func applyResponsesToolCallEvent(s *responsesStreamState, evt *responses.ResponseStreamEventUnion) {
	switch evt.Type {
	case "response.function_call_arguments.delta":
		updateResponsesToolCall(s, evt.OutputIndex, "", "", evt.Delta, true)

	case "response.function_call_arguments.done":
		updateResponsesToolCall(s, evt.OutputIndex, "", evt.Name, evt.Arguments, false)

	// The header event carries no arguments to trust: pass "" so a partially
	// populated Item cannot clobber argument text already accumulated.
	case "response.output_item.added":
		fc := evt.Item.AsFunctionCall()
		updateResponsesToolCall(s, evt.OutputIndex, fc.CallID, fc.Name, "", false)

	// Some Responses streams only populate arguments here.
	case "response.output_item.done":
		fc := evt.Item.AsFunctionCall()
		updateResponsesToolCall(s, evt.OutputIndex, fc.CallID, fc.Name, fc.Arguments, false)
	}
}

// responsesPartialResponse wraps one streamed delta as a partial response
// under the given role — "thinking" for reasoning tokens, the model role for
// output text.
func responsesPartialResponse(role, delta string) *model.LLMResponse {
	return &model.LLMResponse{
		Partial:      true,
		TurnComplete: false,
		Content:      &genai.Content{Role: role, Parts: []*genai.Part{{Text: delta}}},
	}
}

// responsesUsageMetadata converts the accumulated token counts into genai
// usage metadata, reporting nil when the stream carried no usage at all.
func responsesUsageMetadata(s *responsesStreamState) *genai.GenerateContentResponseUsageMetadata {
	if s.promptTokens <= 0 && s.completionTokens <= 0 {
		return nil
	}
	return &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:        int32(s.promptTokens),
		CandidatesTokenCount:    int32(s.completionTokens),
		CachedContentTokenCount: int32(s.cachedTokens),
	}
}

// finishResponsesStream emits the aggregated terminal response and records the
// response ID so the next turn can continue the server-side conversation.
func (m *openaiModel) finishResponsesStream(
	s *responsesStreamState,
	finalResp *responses.Response,
	yield func(*model.LLMResponse, error) bool,
) {
	finalParts := buildResponsesFinalParts(s)
	usage := responsesUsageMetadata(s)

	if finalResp != nil && finalResp.ID != "" {
		m.mu.Lock()
		if m.responseState == nil {
			m.responseState = &responsesState{}
		}
		m.responseState.previousResponseID = finalResp.ID
		m.mu.Unlock()
	}

	_ = yield(&model.LLMResponse{
		Partial:       false,
		TurnComplete:  true,
		FinishReason:  oaiFinishReasonToGenai(s.finishReason),
		UsageMetadata: usage,
		Content:       &genai.Content{Role: string(genai.RoleModel), Parts: finalParts},
	}, nil)
}

// buildResponsesFinalParts assembles the final parts from streaming state.
func buildResponsesFinalParts(s *responsesStreamState) []*genai.Part {
	parts := make([]*genai.Part, 0, 1+len(s.toolCalls))

	// Reasoning content (if any).
	if s.reasoning.Len() > 0 {
		parts = append(parts, &genai.Part{Text: s.reasoning.String()})
	}

	// Text content.
	if s.text != "" {
		parts = append(parts, &genai.Part{Text: s.text})
	}

	// Function calls in index order.
	indices := make([]int64, 0, len(s.toolCalls))
	for k := range s.toolCalls {
		indices = append(indices, k)
	}
	slices.Sort(indices)
	for _, idx := range indices {
		tc := s.toolCalls[idx]
		var args map[string]any
		if tc.arguments != "" {
			_ = json.Unmarshal([]byte(tc.arguments), &args)
		}
		if tc.name != "" || tc.id != "" {
			p := genai.NewPartFromFunctionCall(tc.name, args)
			p.FunctionCall.ID = tc.id
			parts = append(parts, p)
		}
	}

	return parts
}

// runResponsesNonStreaming processes a non-streaming Responses call. Like the
// streaming variant it returns the error rather than yielding it, so
// generateResponses can retry a call rejected for a stale previous_response_id.
func (m *openaiModel) runResponsesNonStreaming(ctx context.Context, params responses.ResponseNewParams, yield func(*model.LLMResponse, error) bool) (bool, error) {
	resp, err := m.client.Responses.New(ctx, params)
	if err != nil {
		return false, err
	}

	parts, finishReason := parseResponsesOutput(resp.Output)
	var usage *genai.GenerateContentResponseUsageMetadata
	if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 {
		usage = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:        int32(resp.Usage.InputTokens),
			CandidatesTokenCount:    int32(resp.Usage.OutputTokens),
			CachedContentTokenCount: int32(resp.Usage.InputTokensDetails.CachedTokens),
		}
	}

	// Save response ID for multi-turn continuation.
	if resp.ID != "" {
		m.mu.Lock()
		if m.responseState == nil {
			m.responseState = &responsesState{}
		}
		m.responseState.previousResponseID = resp.ID
		m.mu.Unlock()
	}

	_ = yield(&model.LLMResponse{
		Partial:       false,
		TurnComplete:  true,
		FinishReason:  oaiFinishReasonToGenai(finishReason),
		UsageMetadata: usage,
		Content:       &genai.Content{Role: string(genai.RoleModel), Parts: parts},
	}, nil)
	return true, nil
}

// parseResponsesOutput converts Responses API output items to genai.Part list.
func parseResponsesOutput(items []responses.ResponseOutputItemUnion) ([]*genai.Part, string) {
	var parts []*genai.Part
	var finishReason string

	for _, item := range items {
		switch variant := item.AsAny().(type) {
		case responses.ResponseOutputMessage:
			// Text content from message.
			for _, content := range variant.Content {
				text := content.AsOutputText()
				if text.Text != "" {
					parts = append(parts, &genai.Part{Text: text.Text})
				}
			}
			// gpt-5.3-codex uses phase labels (commentary/final_answer).
			if variant.Phase != "" {
				parts = append(parts, &genai.Part{Text: "\n[phase: " + string(variant.Phase) + "]"})
			}

		case responses.ResponseFunctionToolCall:
			var args map[string]any
			if variant.Arguments != "" {
				_ = json.Unmarshal([]byte(variant.Arguments), &args)
			}
			p := genai.NewPartFromFunctionCall(variant.Name, args)
			p.FunctionCall.ID = variant.CallID
			parts = append(parts, p)

		case responses.ResponseReasoningItem:
			// Raw reasoning tokens.
			for _, c := range variant.Content {
				parts = append(parts, &genai.Part{Text: c.Text})
			}
			// Encrypted reasoning for ZDR (not currently used by pi-go).
		}
	}

	return parts, finishReason
}
