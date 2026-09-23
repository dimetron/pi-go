package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/url"
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
	// Strip any provider prefix (e.g. "openai/gpt-5.6-luna" or nested
	// "agentgateway/openai/gpt-5.6-luna") so the bare model ID is matched. The
	// gateway forwards the bare ID upstream, so the endpoint decision must be
	// made on it.
	lower := strings.ToLower(StripKnownProviderPrefixes(modelName))
	// Responses-only model families.
	responsesOnly := []string{
		"gpt-5-codex", "gpt-5.1-codex", "gpt-5.1-codex-mini", "gpt-5.1-codex-max",
		"gpt-5.2-codex", "gpt-5.3-codex", "gpt-5.3-codex-spark", "gpt-5.4-codex", "gpt-5.5-codex",
		"gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra",
		"gpt-6-astra", "gpt-6-luna", "gpt-6-sol",
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
		params, sentPreviousResponseID, err := m.buildResponsesParams(req, modelName)
		if err != nil {
			_ = yield(nil, fmt.Errorf("responses input: %w", err))
			return
		}

		// send performs the request once, including the previous_response_id
		// recovery in sendResponses. It takes its own yield so retryStream can
		// re-run it; params is shared across attempts, as before, so a pointer
		// cleared on one attempt stays cleared on the next.
		send := func(y func(*model.LLMResponse, error) bool) {
			m.sendResponses(ctx, params, sentPreviousResponseID, stream, y)
		}

		if stream {
			retryStream(ctx, streamRetryConfig(), yield, send)
		} else {
			send(yield)
		}
	}
}

// buildResponsesParams assembles the Responses request for one turn. It also
// reports whether the request carries a previous_response_id, which decides
// whether a failure is worth retrying without it.
func (m *openaiModel) buildResponsesParams(req *model.LLMRequest, modelName string) (*responses.ResponseNewParams, bool, error) {
	m.mu.Lock()
	state := m.responseState
	m.mu.Unlock()

	input, instructions, err := oaiContentsToResponsesInput(req.Contents, req.Config)
	if err != nil {
		return nil, false, err
	}

	params := &responses.ResponseNewParams{
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

	// The output cap travels on the Responses wire too. Leaving it off is not
	// the neutral choice it looks like: the server substitutes its own default,
	// and agentgateway in front of Anthropic settles on 4096, which cuts a long
	// coding turn off mid-sentence — and a cut landing inside a tool call's
	// arguments leaves the JSON unclosed (see defaultOaiMaxOutputTokens). The
	// Chat Completions path has always sent it; this one dropped it.
	//
	// Not sent to the ChatGPT codex backend. That endpoint exposes a restricted
	// Responses surface that rejects client-side output controls, and the gate
	// keeps a rejected field from breaking every turn rather than degrading one
	// feature. This is the same boundary the web_search include below sits
	// behind, reached the same way — by caution rather than by a live 400, since
	// verifying it needs a codex OAuth token this repo's tests do not carry.
	// The evidence is external (an open report against another client that
	// strips temperature and max_output_tokens for this backend), which is why
	// the field is gated rather than sent-and-seen-to-fail.
	if !m.codexBackend {
		if maxOutputTokens := oaiMaxOutputTokens(req.Config, m.maxOutputTokens); maxOutputTokens > 0 {
			params.MaxOutputTokens = param.NewOpt(maxOutputTokens)
		}
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

	// OpenAI's built-in web_search runs server-side: the model searches, reads
	// and cites without a FunctionCall reaching pi's loop. Appended after the
	// function declarations so both travel in one request.
	//
	// Not sent to the ChatGPT codex backend: that endpoint exposes a fixed
	// tool set, and the include list that makes sources readable is an OpenAI
	// platform feature. The xAI equivalent had to be removed outright when the
	// endpoint rejected it (commit 233134f), so this stays behind the same
	// boundary rather than being discovered live.
	if m.enableWebSearch && !m.codexBackend {
		params.Tools = append(params.Tools, openaiWebSearchTool())
		params.Include = append(params.Include, openaiWebSearchInclude()...)
	}

	if reasoning, ok := m.responsesReasoning(req.Config); ok {
		params.Reasoning = reasoning
	}

	return params, sentPreviousResponseID, nil
}

// responsesReasoning picks the reasoning effort for one turn: an explicit
// per-request thinking budget wins, else the model-level thinking level, else
// nothing (the model's own default).
//
// The budget form is the pre-existing per-request channel and keeps its
// mapping (see oaiResponsesReasoning); the level form is what
// `thinkingLevel` in ~/.pi-go/config.json and WithThinkingLevel reach, and it
// is clamped per model (see oaiReasoningEffortFor).
func (m *openaiModel) responsesReasoning(config *genai.GenerateContentConfig) (shared.ReasoningParam, bool) {
	if reasoning, ok := oaiResponsesReasoning(config); ok {
		return reasoning, true
	}
	effort, ok := oaiReasoningEffortFor(m.modelName, m.thinkingLevel)
	if !ok {
		return shared.ReasoningParam{}, false
	}
	return shared.ReasoningParam{Effort: effort}, true
}

// sendResponses performs one Responses request, recovering from a
// previous_response_id the upstream can no longer resolve, and surfaces any
// remaining error to the caller in the shape the streaming mode expects.
func (m *openaiModel) sendResponses(
	ctx context.Context,
	params *responses.ResponseNewParams,
	sentPreviousResponseID, stream bool,
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

	if err != nil {
		// Clear the stale pointer so the next turn starts fresh rather
		// than replaying the same rejected id.
		m.clearPreviousResponseID()
		if stream {
			_ = y(&model.LLMResponse{ErrorCode: "STREAM_ERROR", ErrorMessage: err.Error()}, nil)
			return
		}
		_ = y(nil, fmt.Errorf("OpenAI Responses API failed: %w", err))
	}
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
func oaiContentsToResponsesInput(contents []*genai.Content, config *genai.GenerateContentConfig) (responses.ResponseNewParamsInputUnion, string, error) {
	// Extract system instruction.
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

	return responses.ResponseNewParamsInputUnion{OfInputItemList: items}, instructions, nil
}

// oaiResponsesPairedIDs collects the function call IDs and function response IDs
// present in the conversation.
//
// A canceled turn can persist a function call before its tool result is
// emitted. Responses requires calls and outputs to be paired, so the two sets
// let the caller omit either half of an incomplete pair when replaying
// conversation history.
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
				addTrimmedID(callIDs, part.FunctionCall.ID)
			}
			if part.FunctionResponse != nil {
				addTrimmedID(responseIDs, part.FunctionResponse.ID)
			}
		}
	}
	return callIDs, responseIDs
}

// addTrimmedID records the trimmed id in set, ignoring blank IDs.
func addTrimmedID(set map[string]struct{}, id string) {
	if trimmed := strings.TrimSpace(id); trimmed != "" {
		set[trimmed] = struct{}{}
	}
}

// oaiAppendResponsesItems appends the input items for one content to items.
// Consecutive text parts coalesce into a single message, which is flushed
// before each function call or output so the items keep their original order.
// Function calls and outputs whose counterpart is missing are dropped.
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
			argsJSON := marshalFunctionCallArgs(fc.Args)
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
			// openai-go v3.54 dropped callID from the helper; set it on the param.
			out := responses.ResponseInputItemParamOfFunctionCallOutput(oaiFunctionResponseContent(fr.Response))
			out.OfFunctionCallOutput.CallID = param.NewOpt(fr.ID)
			items = append(items, out)
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

// errorEventDetail reads the nested error object of a raw "error" event, whose
// code and message the SDK's flattened union fields do not carry. Returns nil
// when the body carries no nested object.
func errorEventDetail(raw string) *struct {
	Code    string `json:"code"`
	Message string `json:"message"`
} {
	var probe struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &probe); err != nil || probe.Error == nil {
		return nil
	}
	return &struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: probe.Error.Code, Message: probe.Error.Message}
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
	responseID       string
	// term is what the stream's terminal event said, as distinct from what the
	// response it carried repeated: a "response.incomplete" declares a turn cut
	// short even when its payload reports no status and no reason. Ported from
	// adk-go v2.4.0 model/openaimodel (PR #1373).
	term terminalEvent
}

// terminalEvent records the terminal event of a Responses stream — the only
// kind that says why the turn ended — as distinct from the response object it
// carried. seen is set by "response.completed" or "response.incomplete" whose
// response actually decoded; incomplete distinguishes the two.
type terminalEvent struct {
	resp       *responses.Response
	seen       bool
	incomplete bool
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
		evtType := evt.Type

		// First terminal object wins: a later one, or a stray
		// "response.created", would relabel a truncated turn a clean stop.
		// An event whose response never decoded is not one of those, hence
		// carriesResponse. Ported from adk-go v2.4.0 (PR #1373).
		if !state.term.seen {
			switch evtType {
			case "response.created":
				created := evt.AsResponseCreated()
				if carriesResponse(&created.Response) {
					// Keep the created response for its metadata only; it
					// never counts as having seen a terminal event.
					state.term.resp = &created.Response
					finalResp = &created.Response
				}
			case "response.completed":
				completed := evt.AsResponseCompleted()
				if carriesResponse(&completed.Response) {
					state.term.resp, state.term.seen = &completed.Response, true
					finalResp = &completed.Response
					applyResponsesCompleted(state, &completed.Response)
				}
			case "response.incomplete":
				incomplete := evt.AsResponseIncomplete()
				if carriesResponse(&incomplete.Response) {
					state.term.resp, state.term.seen, state.term.incomplete = &incomplete.Response, true, true
					finalResp = &incomplete.Response
					applyResponsesCompleted(state, &incomplete.Response)
				}
			case "response.failed":
				failed := evt.AsResponseFailed()
				// A failure stated as its own event, mirroring adk-go v2.4.0:
				// the server failed the turn, so it ends in place of
				// TurnComplete with the same error the blocking path reports.
				_ = yield(nil, failedResponseError(&failed.Response))
				return true, nil
			}
		}

		// response.error — surface as LLM error. The live API nests
		// code/message inside an "error" object (e.g. insufficient_quota), and
		// the SDK's flattened Code/Message fields stay empty for it, so read
		// the raw event JSON when they are.
		if evtType == "error" {
			code, msg := evt.Code, evt.Message
			if code == "" || msg == "" {
				if nested := errorEventDetail(evt.AsError().RawJSON()); nested != nil {
					if code == "" {
						code = nested.Code
					}
					if msg == "" {
						msg = nested.Message
					}
				}
			}
			_ = yield(&model.LLMResponse{ErrorCode: code, ErrorMessage: msg}, nil)
			return true, nil
		}

		// response.output_text.delta — text token.
		if evtType == "response.output_text.delta" {
			state.text += evt.Delta
			emitted = true
			if !yield(&model.LLMResponse{
				Partial:      true,
				TurnComplete: false,
				Content:      &genai.Content{Role: string(genai.RoleModel), Parts: []*genai.Part{{Text: evt.Delta}}},
			}, nil) {
				return emitted, nil
			}
		}

		// response.reasoning_text.delta — reasoning token.
		if evtType == "response.reasoning_text.delta" {
			state.reasoning.WriteString(evt.Delta)
			emitted = true
			if !yield(&model.LLMResponse{
				Partial:      true,
				TurnComplete: false,
				Content:      &genai.Content{Role: "thinking", Parts: []*genai.Part{{Text: evt.Delta}}},
			}, nil) {
				return emitted, nil
			}
		}

		applyResponsesToolCallEvent(state, evt)
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

// applyResponsesCompleted records the usage and status the response.completed
// event carries.
func applyResponsesCompleted(state *responsesStreamState, resp *responses.Response) {
	if resp.ID != "" {
		state.responseID = resp.ID
	}
	if resp.Usage.InputTokens > 0 {
		state.promptTokens = resp.Usage.InputTokens
	}
	if resp.Usage.OutputTokens > 0 {
		state.completionTokens = resp.Usage.OutputTokens
	}
	if c := resp.Usage.InputTokensDetails.CachedTokens; c > 0 {
		state.cachedTokens = c
	}
	if resp.IncompleteDetails.Reason != "" {
		// The reason is the provider's own word for why the turn stopped —
		// "max_output_tokens", "content_filter" — and carries more than the
		// bare status. Ported from adk-go v2.4.0.
		state.finishReason = resp.IncompleteDetails.Reason
	} else {
		state.finishReason = string(resp.Status)
	}
}

// applyResponsesToolCallEvent folds the function-call events into the tool call
// accumulator. Events of any other type are ignored.
func applyResponsesToolCallEvent(state *responsesStreamState, evt responses.ResponseStreamEventUnion) {
	switch evt.Type {
	// response.function_call_arguments.delta — streaming function call args.
	// response.output_item.* carries the function call header (id, name),
	// and may carry the final arguments depending on backend/SDK mapping.
	// The per-chunk argument text is in evt.Delta; evt.Arguments is only set
	// on the `.done` event (as the full summary string).
	case "response.function_call_arguments.delta":
		updateResponsesToolCall(state, evt.OutputIndex, "", "", evt.Delta, true)

	// response.function_call_arguments.done — final full arguments string.
	// Use it as a safety net: if deltas were missed, overwrite with the
	// authoritative complete arguments payload.
	case "response.function_call_arguments.done":
		updateResponsesToolCall(state, evt.OutputIndex, "", evt.Name, evt.Arguments, false)

	// response.output_item.added — function call item header.
	case "response.output_item.added":
		fc := evt.Item.AsFunctionCall()
		updateResponsesToolCall(state, evt.OutputIndex, fc.CallID, fc.Name, "", false)

	// response.output_item.done — final fallback arguments. Some Responses
	// streams only populate arguments here.
	case "response.output_item.done":
		fc := evt.Item.AsFunctionCall()
		updateResponsesToolCall(state, evt.OutputIndex, fc.CallID, fc.Name, fc.Arguments, false)
	}
}

// finishResponsesStream yields the terminal response for a stream that ran to
// completion, and remembers the response ID for multi-turn continuation.
func (m *openaiModel) finishResponsesStream(state *responsesStreamState, finalResp *responses.Response, yield func(*model.LLMResponse, error) bool) {
	// Build final response parts.
	finalParts := buildResponsesFinalParts(state)
	var usage *genai.GenerateContentResponseUsageMetadata
	if state.promptTokens > 0 || state.completionTokens > 0 {
		usage = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:        int32(state.promptTokens),
			CandidatesTokenCount:    int32(state.completionTokens),
			CachedContentTokenCount: int32(state.cachedTokens),
		}
	}

	// The finish reason comes off the terminal event, the only thing that says
	// why the turn ended. Without one, finishReason would read silence as a
	// clean stop, so report Unspecified — the model never said. Ported from
	// adk-go v2.4.0 (PR #1373).
	finish := genai.FinishReasonUnspecified
	var termResp *responses.Response
	if state.term.seen {
		termResp = state.term.resp
		finish = oaiResponsesFinishReason(termResp, state.term.incomplete)
	} else if finalResp != nil {
		// A stream whose only terminal-ish object was a bare "response.created":
		// read the payload, but do not trust it as the provider's verdict.
		termResp = finalResp
		finish = oaiResponsesFinishReason(termResp, false)
	}

	// Save response ID for multi-turn continuation.
	if finalResp != nil && finalResp.ID != "" {
		m.mu.Lock()
		if m.responseState == nil {
			m.responseState = &responsesState{}
		}
		m.responseState.previousResponseID = finalResp.ID
		m.mu.Unlock()
	}

	final := &model.LLMResponse{
		Partial:       false,
		TurnComplete:  true,
		FinishReason:  finish,
		UsageMetadata: usage,
		Content:       &genai.Content{Role: string(genai.RoleModel), Parts: finalParts},
	}
	if termResp != nil {
		// A server-side search reaches the client only through the terminal
		// response's output list — it emits no delta to latch onto while the
		// stream runs, so it is collected here.
		final.GroundingMetadata = openaiWebSearchGrounding(termResp.Output)
		attachResponsesFinishSignal(final, termResp, state.term.incomplete && state.term.seen)
	}
	_ = yield(final, nil)
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
			p := newFunctionCallPart(tc.name, args)
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

	// A failed response body on HTTP 200 is a failure, not a turn. Ported
	// from adk-go v2.4.0 (PR #1359).
	if reportsFailure(resp) {
		_ = yield(nil, failedResponseError(resp))
		return true, nil
	}

	parts, _ := parseResponsesOutput(resp.Output)
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

	// No event announced this response, so its own payload is all there is
	// to read the finish reason from. Ported from adk-go v2.4.0.
	finish := oaiResponsesFinishReason(resp, false)

	final := &model.LLMResponse{
		Partial:           false,
		TurnComplete:      true,
		FinishReason:      finish,
		UsageMetadata:     usage,
		GroundingMetadata: openaiWebSearchGrounding(resp.Output),
		Content:           &genai.Content{Role: string(genai.RoleModel), Parts: parts},
	}
	attachResponsesFinishSignal(final, resp, false)
	_ = yield(final, nil)
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
			p := newFunctionCallPart(variant.Name, args)
			p.FunctionCall.ID = variant.CallID
			parts = append(parts, p)

		case responses.ResponseFunctionWebSearch:
			// A server-side search. The model ran it and cites what it read,
			// so there is no FunctionCall for pi's loop to execute. Its
			// evidence is collected into the response's GroundingMetadata
			// below rather than emitted as a synthetic call: an unmatched
			// FunctionCall part would be executed by ADK's flow, which finds
			// no such tool and injects a "tool not found" result into the
			// conversation.

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

// sourceHostLabel reduces a source URL to the host a reader recognizes, so a
// chat line stays short: "nasa.gov", not the full URL with its tracking query.
// Falls back to the whole URL when it does not parse, because an unparseable
// source is still worth showing.
func sourceHostLabel(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}

// openaiWebSearchGrounding collects the server-side web searches in a response
// into the GroundingMetadata shape pi's surfaces already render, so an OpenAI
// search shows its query and sources exactly as a Gemini one does.
//
// This is the channel rather than a synthetic FunctionCall part because ADK
// executes every FunctionCall it finds in a model response: a name with no
// registered tool comes back as newToolNotFoundError, which lands a
// "tool 'openai_web_search' not found" result in the conversation and feeds it
// to the model on the next turn. GroundingMetadata is opaque to ADK's flow —
// the aggregator only carries it forward (stream_aggregator.go) — so it reaches
// the display layer without being executed.
//
// Returns nil when the response names no search, so callers can leave the field
// unset rather than attach an empty metadata object.
func openaiWebSearchGrounding(items []responses.ResponseOutputItemUnion) *genai.GroundingMetadata {
	var queries []string
	var chunks []*genai.GroundingChunk

	for _, item := range items {
		search, ok := item.AsAny().(responses.ResponseFunctionWebSearch)
		if !ok {
			continue
		}
		action, ok := search.Action.AsAny().(responses.ResponseFunctionWebSearchActionSearch)
		if !ok {
			// open_page / find — the model followed a link instead of
			// searching. Nothing to report as a query.
			continue
		}
		// Queries only; the struct's single-query `Query` field is deprecated,
		// and reading it would need a staticcheck suppression to buy nothing.
		// Measured against the live API, a web_search_call carries both — and
		// `query` is not a fallback for an empty list but a duplicate of
		// queries[0]:
		//
		//	"queries": ["positive news today", "site:reuters.com ..."],
		//	"query":   "positive news today"
		//
		// So the list is always the complete answer, and appending the singular
		// field would double-report the first query.
		queries = append(queries, action.Queries...)

		// One chunk per URL, following the same convention Gemini's grounding
		// uses: Title is the short label the chat shows, URI the full address
		// the trace log keeps. Unlike Gemini — which returns an opaque
		// ~200-char vertexaisearch redirect and so must put the domain in
		// Title — OpenAI returns the real page URL, so the host is the label
		// and the URL itself survives in the log.
		//
		// Without this the chat prints raw URLs (query strings and all), which
		// soft-wrap across the panel: one live search returned 39 sources.
		for _, src := range action.Sources {
			if src.URL == "" {
				continue
			}
			chunks = append(chunks, &genai.GroundingChunk{
				Web: &genai.GroundingChunkWeb{
					Title: sourceHostLabel(src.URL),
					URI:   src.URL,
				},
			})
		}
	}

	if len(queries) == 0 && len(chunks) == 0 {
		return nil
	}
	return &genai.GroundingMetadata{
		WebSearchQueries: queries,
		GroundingChunks:  chunks,
	}
}

// --- Finish reason and failure handling, ported from adk-go v2.4.0
// model/openaimodel (PRs #1373, #1359, #1467). pi-go's Responses path shared
// the same defect shape: the finish reason was read off fields openai-go does
// not mark required, so a truncated or failed turn decoded to a clean stop.

// maxServerTextRunes bounds any server-chosen string an error quotes.
const maxServerTextRunes = 256

// clipServerText trims a server-chosen string and caps its length, so that a
// pathological one — a megabyte of message — cannot become the error a caller
// logs. Truncation is marked, so a clipped value does not read as the whole of
// what the server said.
func clipServerText(s string) string {
	s = strings.TrimSpace(s)
	// Counted by ranging rather than by materializing []rune: an 8 MiB message
	// would otherwise cost 32 MiB to yield at most a kilobyte. Ranging a string
	// yields the byte index of each rune, so s[:i] never splits one.
	n := 0
	for i := range s {
		if n == maxServerTextRunes {
			return strings.TrimSpace(s[:i]) + "…"
		}
		n++
	}
	return s
}

// reportsFailure reports whether the server is describing a failure rather than
// a turn, which on an HTTP 200 is the whole of what separates the two. Only
// "failed" qualifies — "incomplete" is an ordinary truncation — except that a
// body stating no status at all, which the API permits, is judged by its error
// object instead.
func reportsFailure(resp *responses.Response) bool {
	if resp == nil {
		return false
	}
	switch resp.Status {
	case responses.ResponseStatusFailed:
		return true
	case "":
		return clipServerText(resp.Error.Message) != "" || clipServerText(string(resp.Error.Code)) != ""
	default:
		return false
	}
}

// failedResponseError renders a failure as an error quoting the server: the
// message as the text, the response ID and error code as a labeled
// parenthetical. The ID is there because the response is discarded with the
// failure, so nothing else is left to quote back to the provider.
func failedResponseError(resp *responses.Response) error {
	if resp == nil {
		return fmt.Errorf("openai response failed")
	}
	msg := clipServerText(resp.Error.Message)
	var details []string
	if id := clipServerText(resp.ID); id != "" {
		details = append(details, fmt.Sprintf("id %q", id))
	}
	if code := clipServerText(string(resp.Error.Code)); code != "" {
		details = append(details, fmt.Sprintf("code %q", code))
	}
	switch joined := strings.Join(details, ", "); {
	case joined != "" && msg != "":
		return fmt.Errorf("openai response failed (%s): %q", joined, msg)
	case joined != "":
		return fmt.Errorf("openai response failed (%s)", joined)
	case msg != "":
		return fmt.Errorf("openai response failed: %q", msg)
	default:
		// The server said "failed" and nothing more.
		return fmt.Errorf("openai response failed")
	}
}

// carriesResponse reports whether an event delivered the response object the
// schema marks required. AsResponse* discards its unmarshal error and Response
// is a value field, so an omitted or empty object hands back a zero value that
// would otherwise outrank a well-formed event later in the turn. Any field
// having decoded stands for the object's presence; testing "id" alone would
// also reject a populated response that merely omits it. A bare "{}" leaves
// every raw value empty and is still rejected.
func carriesResponse(resp *responses.Response) bool {
	j := &resp.JSON
	return j.ID.Valid() || j.Status.Valid() || j.Output.Valid() ||
		j.IncompleteDetails.Valid() || j.Error.Valid() || j.Model.Valid()
}

// oaiResponsesFinishReason reports why the model stopped generating.
// incompleteEvent says the terminal streaming event was a "response.incomplete"
// (see runResponsesStreaming); blocking, having no event to read, passes false.
// Both paths otherwise decide from the same payload. Ported from adk-go v2.4.0.
func oaiResponsesFinishReason(resp *responses.Response, incompleteEvent bool) genai.FinishReason {
	if resp == nil {
		return genai.FinishReasonUnspecified
	}
	switch resp.IncompleteDetails.Reason {
	case "max_output_tokens", "max_tool_calls", "max_tokens":
		return genai.FinishReasonMaxTokens
	case "content_filter":
		return genai.FinishReasonSafety
	case "":
		// No reason given, so the status and what the event was called are all
		// that is left to go on.
		if responsesTruncated(resp, incompleteEvent) {
			return genai.FinishReasonOther
		}
		return genai.FinishReasonStop
	default:
		return genai.FinishReasonOther
	}
}

// responsesTruncated reports whether a turn that named no incomplete reason
// nonetheless ended before it was done; calling one a clean stop would have a
// caller that retries on anything but STOP accept a partial answer as final.
//
// openai-go marks incomplete_details required but neither its reason nor the
// status, so a provider may declare a turn truncated and leave either empty.
// Every signal that survives that is read here.
func responsesTruncated(resp *responses.Response, incompleteEvent bool) bool {
	if incompleteEvent {
		// The event stands in for "response.completed", so its name is the
		// provider's verdict, and it outranks a payload that says otherwise.
		return true
	}
	switch resp.Status {
	case responses.ResponseStatusCompleted:
		return false
	case "":
		// A finished turn carries incomplete_details as null.
		return resp.JSON.IncompleteDetails.Valid()
	default:
		// failed, canceled, incomplete, in_progress and queued all describe an
		// unfinished turn. Enumerating the finished states instead keeps a
		// status added later from defaulting to a clean stop.
		return true
	}
}

// responsesFinishMessage is the provider's own account of why a turn ended,
// which the finish reason flattens away: an unmapped incomplete reason and a
// failure both arrive as OTHER.
func responsesFinishMessage(resp *responses.Response, incompleteEvent bool) string {
	if resp == nil {
		return ""
	}
	if msg := resp.Error.Message; msg != "" {
		return msg
	}
	if reason := resp.IncompleteDetails.Reason; reason != "" {
		return reason
	}
	// The contradiction responsesTruncated resolves, resolved the same way: the
	// event's name outranks a payload calling the turn completed. Every other
	// status is still the provider's own wording for why.
	if resp.Status != "" && (!incompleteEvent || resp.Status != responses.ResponseStatusCompleted) {
		return string(resp.Status)
	}
	if responsesTruncated(resp, incompleteEvent) {
		// No usable reason or status, yet the turn did not finish: the event's
		// name, or a bare incomplete_details, is all the provider said.
		return string(responses.ResponseStatusIncomplete)
	}
	return ""
}

// ResponsesFinishMessageKey is the model.LLMResponse.CustomMetadata key under
// which a turn that ended badly but still carries an answer reports the
// provider's own account of why — a content filter's "content_filter", an
// incomplete reason this package does not map, or a server error message. Its
// FinishReason says the turn was cut short; this says what the provider called
// it. Same wording as adk-go v2.4.0's openaimodel.FinishMessageKey.
const ResponsesFinishMessageKey = "openai_finish_message"

// attachResponsesFinishSignal surfaces why a turn did not end cleanly, in the
// place that suits what the turn produced. ErrorCode is not advisory — pi-go
// aborts the turn on a non-empty one and discards the content — so a turn with
// content reports the provider's wording as metadata beside a FinishReason that
// already says it was cut short; only a turn with nothing to read uses the
// error fields. Ported from adk-go v2.4.0 (PR #1373).
func attachResponsesFinishSignal(resp *model.LLMResponse, openaiResp *responses.Response, incompleteEvent bool) {
	if resp == nil || openaiResp == nil {
		return
	}
	switch resp.FinishReason {
	case genai.FinishReasonSafety, genai.FinishReasonOther:
	default:
		// MAX_TOKENS and STOP say all there is to say by themselves.
		return
	}
	msg := responsesFinishMessage(openaiResp, incompleteEvent)
	if resp.Content != nil && len(resp.Content.Parts) > 0 {
		if msg != "" {
			if resp.CustomMetadata == nil {
				resp.CustomMetadata = map[string]any{}
			}
			resp.CustomMetadata[ResponsesFinishMessageKey] = msg
		}
		return
	}
	if resp.FinishReason == genai.FinishReasonSafety {
		resp.ErrorCode = string(genai.BlockedReasonSafety)
	} else {
		resp.ErrorCode = string(genai.FinishReasonOther)
	}
	resp.ErrorMessage = msg
}
