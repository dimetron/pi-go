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

		input, instructions, err := oaiContentsToResponsesInput(req.Contents, req.Config)
		if err != nil {
			_ = yield(nil, fmt.Errorf("responses input: %w", err))
			return
		}

		params := responses.ResponseNewParams{
			Model: modelName,
			Input: input,
		}
		if instructions != "" {
			params.Instructions = param.NewOpt(instructions)
		}

		// The ChatGPT codex backend is stateless — it rejects requests that
		// expect server-side persistence and requires clients to opt in to
		// encrypted reasoning echo so multi-turn context can round-trip on
		// the client side. Matches pi-mono's openai-codex-responses body.
		if m.codexBackend {
			params.Store = param.NewOpt(false)
			params.Include = []responses.ResponseIncludable{
				responses.ResponseIncludableReasoningEncryptedContent,
			}
		}

		// Thread previous_response_id for multi-turn.
		// Skip on the codex backend: it doesn't retain responses server-side
		// (store=false), so PreviousResponseID wouldn't resolve.
		sentPreviousResponseID := false
		if !m.codexBackend && state != nil && state.previousResponseID != "" {
			params.PreviousResponseID = param.NewOpt(state.previousResponseID)
			sentPreviousResponseID = true
		}

		if req.Config != nil && len(req.Config.Tools) > 0 {
			params.Tools = oaiGenaiToolsToResponses(req.Config.Tools)
		}

		// Apply reasoning effort if configured via ThinkingConfig.BudgetTokens.
		// Low tokens (100-500) → low effort; medium (2000-4000) → medium; high (8000+) → high.
		if req.Config != nil && req.Config.ThinkingConfig != nil && req.Config.ThinkingConfig.ThinkingBudget != nil {
			bt := *req.Config.ThinkingConfig.ThinkingBudget
			switch {
			case bt >= 8000:
				params.Reasoning = shared.ReasoningParam{Effort: shared.ReasoningEffortHigh}
			case bt >= 2000:
				params.Reasoning = shared.ReasoningParam{Effort: shared.ReasoningEffortMedium}
			case bt > 0:
				params.Reasoning = shared.ReasoningParam{Effort: shared.ReasoningEffortLow}
			}
		}

		runOnce := func(p responses.ResponseNewParams) (bool, error) {
			if stream {
				return m.runResponsesStreaming(ctx, p, yield)
			}
			return m.runResponsesNonStreaming(ctx, p, yield)
		}

		emitted, err := runOnce(params)

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
			_, err = runOnce(params)
		}

		if err != nil {
			// Clear the stale pointer so the next turn starts fresh rather
			// than replaying the same rejected id.
			m.clearPreviousResponseID()
			if stream {
				_ = yield(&model.LLMResponse{ErrorCode: "STREAM_ERROR", ErrorMessage: err.Error()}, nil)
				return
			}
			_ = yield(nil, fmt.Errorf("OpenAI Responses API failed: %w", err))
		}
	}
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
	var systemBuilder strings.Builder
	if config != nil && config.SystemInstruction != nil {
		for _, p := range config.SystemInstruction.Parts {
			if p != nil && p.Text != "" {
				systemBuilder.WriteString(p.Text)
				systemBuilder.WriteByte('\n')
			}
		}
	}
	instructions := strings.TrimSpace(systemBuilder.String())

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
				argsJSON, _ := json.Marshal(fc.Args)
				items = append(items, responses.ResponseInputItemParamOfFunctionCall(string(argsJSON), fc.ID, fc.Name))
			case part.FunctionResponse != nil:
				flushText()
				fr := part.FunctionResponse
				if strings.TrimSpace(fr.ID) == "" {
					continue
				}
				items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(fr.ID, oaiFunctionResponseContent(fr.Response)))
			}
		}
		flushText()
	}

	return responses.ResponseNewParamsInputUnion{OfInputItemList: items}, instructions, nil
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
	responseID       string
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

		// response.completed — capture final response with usage and status.
		if evtType == "response.completed" {
			finalResp = &evt.Response
			if evt.Response.ID != "" {
				state.responseID = evt.Response.ID
			}
			if evt.Response.Usage.InputTokens > 0 {
				state.promptTokens = evt.Response.Usage.InputTokens
			}
			if evt.Response.Usage.OutputTokens > 0 {
				state.completionTokens = evt.Response.Usage.OutputTokens
			}
			if c := evt.Response.Usage.InputTokensDetails.CachedTokens; c > 0 {
				state.cachedTokens = c
			}
			state.finishReason = string(evt.Response.Status)
			continue
		}

		// response.error — surface as LLM error.
		if evtType == "error" {
			_ = yield(&model.LLMResponse{ErrorCode: evt.Code, ErrorMessage: evt.Message}, nil)
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

		// response.function_call_arguments.delta — streaming function call args.
		// response.output_item.* carries the function call header (id, name),
		// and may carry the final arguments depending on backend/SDK mapping.
		// The per-chunk argument text is in evt.Delta; evt.Arguments is only set
		// on the `.done` event (as the full summary string).
		if evtType == "response.function_call_arguments.delta" {
			updateResponsesToolCall(state, evt.OutputIndex, "", "", evt.Delta, true)
		}
		// response.function_call_arguments.done — final full arguments string.
		// Use it as a safety net: if deltas were missed, overwrite with the
		// authoritative complete arguments payload.
		if evtType == "response.function_call_arguments.done" {
			updateResponsesToolCall(state, evt.OutputIndex, "", evt.Name, evt.Arguments, false)
		}

		// response.output_item.added — function call item header.
		if evtType == "response.output_item.added" {
			fc := evt.Item.AsFunctionCall()
			updateResponsesToolCall(state, evt.OutputIndex, fc.CallID, fc.Name, "", false)
		}

		// response.output_item.done — final fallback arguments. Some Responses
		// streams only populate arguments here.
		if evtType == "response.output_item.done" {
			fc := evt.Item.AsFunctionCall()
			updateResponsesToolCall(state, evt.OutputIndex, fc.CallID, fc.Name, fc.Arguments, false)
		}
	}

	if err := stream.Err(); err != nil {
		if ctx.Err() == context.Canceled {
			return emitted, nil
		}
		// Hand the error back to generateResponses: it owns the stale-id
		// retry and clears previousResponseID before surfacing anything.
		return emitted, err
	}

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

	// Save response ID for multi-turn continuation.
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
		FinishReason:  oaiFinishReasonToGenai(state.finishReason),
		UsageMetadata: usage,
		Content:       &genai.Content{Role: string(genai.RoleModel), Parts: finalParts},
	}, nil)
	return true, nil
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
