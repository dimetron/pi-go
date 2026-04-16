package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/openai/openai-go/v3/shared/constant"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// openaiModel implements model.LLM for OpenAI-compatible APIs.
type openaiModel struct {
	modelName string
	client    openai.Client
	// mu protects responseState for Responses mode multi-turn.
	mu            sync.Mutex
	responseState *responsesState // nil when using Chat Completions
}

// responsesState holds state threaded across Responses API calls.
type responsesState struct {
	previousResponseID string // OpenAI-side session ID for multi-turn
}

// NewOpenAI creates an OpenAI model.LLM.
// If baseURL is non-empty, it overrides the default API endpoint.
func NewOpenAI(_ context.Context, modelName, apiKey, baseURL string, llmOpts *LLMOptions) (model.LLM, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
	}
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	if llmOpts != nil {
		for k, v := range llmOpts.ExtraHeaders {
			opts = append(opts, option.WithHeader(k, v))
		}
		if transport := BuildTransport(llmOpts); transport != nil {
			opts = append(opts, option.WithHTTPClient(&http.Client{Transport: transport}))
		}
	}
	client := openai.NewClient(opts...)
	return &openaiModel{
		modelName:     modelName,
		client:        client,
		responseState: nil, // determined per-call based on model
	}, nil
}

func (m *openaiModel) Name() string { return m.modelName }

// endpointMode returns whether to use Responses or Chat Completions for this model.
// Responses is used for: Codex models (Responses-only), any model with an active
// multi-turn previous_response_id. Chat Completions is used for GPT-4, o1, and
// other models that support it and have no multi-turn state.
func (m *openaiModel) endpointMode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
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

// modelNeedsResponses returns true for models that only support the Responses API.
func modelNeedsResponses(modelName string) bool {
	lower := strings.ToLower(modelName)
	// Responses-only model families.
	responsesOnly := []string{
		"gpt-5-codex", "gpt-5.3-codex", "gpt-5.2-codex",
		"gpt-5.1-codex", "gpt-5.1-codex-mini", "gpt-5.1-codex-max",
	}
	for _, m := range responsesOnly {
		if strings.HasPrefix(lower, m) {
			return true
		}
	}
	return false
}

// generateChat implements the Chat Completions API path (default for GPT-4, o1, etc.).
func (m *openaiModel) generateChat(ctx context.Context, req *model.LLMRequest, modelName string, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		messages, systemInstruction := oaiContentsToMessages(req.Contents, req.Config)

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
			oaiRunStreaming(ctx, &m.client, params, yield)
		} else {
			oaiRunNonStreaming(ctx, &m.client, params, yield)
		}
	}
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

		// Thread previous_response_id for multi-turn.
		if state != nil && state.previousResponseID != "" {
			params.PreviousResponseID = param.NewOpt(state.previousResponseID)
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

		if stream {
			m.runResponsesStreaming(ctx, params, yield)
		} else {
			m.runResponsesNonStreaming(ctx, params, yield)
		}
	}
}

// oaiContentsToResponsesInput converts ADK contents to Responses API input and instructions.
// For a single user text, returns a simple string input.
// For function call rounds, returns an InputItemList.
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

	// Collect function responses for matching with function calls.
	functionResponses := make(map[string]*genai.FunctionResponse)
	for _, c := range contents {
		if c == nil || c.Parts == nil {
			continue
		}
		for _, p := range c.Parts {
			if p != nil && p.FunctionResponse != nil {
				functionResponses[p.FunctionResponse.ID] = p.FunctionResponse
			}
		}
	}

	// Collect non-system content.
	var textParts []string
	var functionCalls []*genai.FunctionCall

	for _, content := range contents {
		if content == nil || strings.TrimSpace(content.Role) == "system" {
			continue
		}
		for _, part := range content.Parts {
			if part == nil {
				continue
			}
			if part.Text != "" {
				textParts = append(textParts, part.Text)
			} else if part.FunctionCall != nil {
				functionCalls = append(functionCalls, part.FunctionCall)
			}
		}
	}

	// Build input. Use string for simple cases; list for function call rounds.
	if len(functionCalls) == 0 {
		input := strings.Join(textParts, "\n")
		return responses.ResponseNewParamsInputUnion{OfString: param.NewOpt(input)}, instructions, nil
	}

	// Function call rounds: build a list of input items.
	items := make(responses.ResponseInputParam, 0, 1+len(functionCalls)*2)

	// User text part (first content block).
	if len(textParts) > 0 {
		msg := responses.EasyInputMessageParam{
			Content: responses.EasyInputMessageContentUnionParam{
				OfString: param.NewOpt(strings.Join(textParts, "\n")),
			},
			Role: responses.EasyInputMessageRoleUser,
		}
		items = append(items, responses.ResponseInputItemUnionParam{OfMessage: &msg})
	}

	// Function call and response rounds.
	for _, fc := range functionCalls {
		if fc == nil || strings.TrimSpace(fc.ID) == "" {
			continue
		}
		argsJSON, _ := json.Marshal(fc.Args)

		// Function call item.
		fcItem := responses.ResponseInputItemUnionParam{
			OfFunctionCall: &responses.ResponseFunctionToolCallParam{
				CallID:    fc.ID,
				Name:      fc.Name,
				Arguments: string(argsJSON),
			},
		}
		items = append(items, fcItem)

		// Function output item.
		contentStr := "No response available for this function call."
		if fr := functionResponses[fc.ID]; fr != nil {
			contentStr = oaiFunctionResponseContent(fr.Response)
		}
		outItem := responses.ResponseInputItemUnionParam{
			OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
				CallID: fc.ID,
				Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
					OfString: param.NewOpt(contentStr),
				},
			},
		}
		items = append(items, outItem)
	}

	return responses.ResponseNewParamsInputUnion{OfInputItemList: items}, instructions, nil
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
			paramsMap := make(shared.FunctionParameters)
			if fd.ParametersJsonSchema != nil {
				if m, ok := fd.ParametersJsonSchema.(map[string]any); ok {
					maps.Copy(paramsMap, m)
				}
			}
			if _, ok := paramsMap["type"]; !ok {
				paramsMap["type"] = "object"
			}
			if paramsMap["type"] == "object" {
				if _, ok := paramsMap["properties"]; !ok {
					paramsMap["properties"] = map[string]any{}
				}
			}
			out = append(out, responses.ToolUnionParam{
				OfFunction: &responses.FunctionToolParam{
					Name:        fd.Name,
					Parameters:  paramsMap,
					Description: param.NewOpt(fd.Description),
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
	responseID       string
}

type toolCallAcc struct {
	id, name, arguments string
}

// runResponsesStreaming processes a streaming Responses call.
// The streaming protocol is documented at
// https://platform.openai.com/docs/guides/responses-streaming
func (m *openaiModel) runResponsesStreaming(ctx context.Context, params responses.ResponseNewParams, yield func(*model.LLMResponse, error) bool) {
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
			state.finishReason = string(evt.Response.Status)
			continue
		}

		// response.error — surface as LLM error.
		if evtType == "error" {
			_ = yield(&model.LLMResponse{ErrorCode: evt.Code, ErrorMessage: evt.Message}, nil)
			return
		}

		// response.output_text.delta — text token.
		if evtType == "response.output_text.delta" {
			state.text += evt.Delta
			if !yield(&model.LLMResponse{
				Partial:      true,
				TurnComplete: false,
				Content:      &genai.Content{Role: string(genai.RoleModel), Parts: []*genai.Part{{Text: evt.Delta}}},
			}, nil) {
				return
			}
		}

		// response.reasoning_text.delta — reasoning token.
		if evtType == "response.reasoning_text.delta" {
			state.reasoning.WriteString(evt.Delta)
			if !yield(&model.LLMResponse{
				Partial:      true,
				TurnComplete: false,
				Content:      &genai.Content{Role: "thinking", Parts: []*genai.Part{{Text: evt.Delta}}},
			}, nil) {
				return
			}
		}

		// response.function_call_arguments.delta — streaming function call args.
		// response.output_item.added carries the function call header (id, name).
		if evtType == "response.function_call_arguments.delta" {
			idx := evt.OutputIndex
			if acc, ok := state.toolCalls[idx]; ok {
				state.toolCalls[idx] = toolCallAcc{id: acc.id, name: acc.name, arguments: acc.arguments + evt.Arguments}
			} else {
				state.toolCalls[idx] = toolCallAcc{arguments: evt.Arguments}
			}
		}

		// response.output_item.added — function call item header (call_id, name).
		if evtType == "response.output_item.added" {
			fc := evt.Item.AsFunctionCall()
			if fc.CallID != "" || fc.Name != "" {
				idx := evt.OutputIndex
				if acc, ok := state.toolCalls[idx]; ok {
					state.toolCalls[idx] = toolCallAcc{id: fc.CallID, name: fc.Name, arguments: acc.arguments}
				} else {
					state.toolCalls[idx] = toolCallAcc{id: fc.CallID, name: fc.Name}
				}
			}
		}
	}

	if err := stream.Err(); err != nil {
		if ctx.Err() == context.Canceled {
			return
		}
		// Clear stale previous_response_id so retry starts fresh.
		m.mu.Lock()
		if m.responseState != nil {
			m.responseState.previousResponseID = ""
		}
		m.mu.Unlock()
		_ = yield(&model.LLMResponse{ErrorCode: "STREAM_ERROR", ErrorMessage: err.Error()}, nil)
		return
	}

	// Build final response parts.
	finalParts := buildResponsesFinalParts(state)
	var usage *genai.GenerateContentResponseUsageMetadata
	if state.promptTokens > 0 || state.completionTokens > 0 {
		usage = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     int32(state.promptTokens),
			CandidatesTokenCount: int32(state.completionTokens),
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

// runResponsesNonStreaming processes a non-streaming Responses call.
func (m *openaiModel) runResponsesNonStreaming(ctx context.Context, params responses.ResponseNewParams, yield func(*model.LLMResponse, error) bool) {
	resp, err := m.client.Responses.New(ctx, params)
	if err != nil {
		_ = yield(nil, fmt.Errorf("OpenAI Responses API failed: %w", err))
		return
	}

	parts, finishReason := parseResponsesOutput(resp.Output)
	var usage *genai.GenerateContentResponseUsageMetadata
	if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 {
		usage = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     int32(resp.Usage.InputTokens),
			CandidatesTokenCount: int32(resp.Usage.OutputTokens),
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

// oaiFinishReasonToGenai maps finish reasons to genai.FinishReason.
func oaiFinishReasonToGenai(reason string) genai.FinishReason {
	switch reason {
	case "length":
		return genai.FinishReasonMaxTokens
	case "content_filter":
		return genai.FinishReasonSafety
	default:
		return genai.FinishReasonStop
	}
}

// oaiContentsToMessages converts genai.Content to OpenAI messages (Chat Completions path).
func oaiContentsToMessages(contents []*genai.Content, config *genai.GenerateContentConfig) ([]openai.ChatCompletionMessageParamUnion, string) {
	var systemBuilder strings.Builder
	if config != nil && config.SystemInstruction != nil {
		for _, p := range config.SystemInstruction.Parts {
			if p != nil && p.Text != "" {
				systemBuilder.WriteString(p.Text)
				systemBuilder.WriteByte('\n')
			}
		}
	}
	systemInstruction := strings.TrimSpace(systemBuilder.String())

	// Collect function responses for matching with function calls.
	functionResponses := make(map[string]*genai.FunctionResponse)
	for _, c := range contents {
		if c == nil || c.Parts == nil {
			continue
		}
		for _, p := range c.Parts {
			if p != nil && p.FunctionResponse != nil {
				functionResponses[p.FunctionResponse.ID] = p.FunctionResponse
			}
		}
	}

	var messages []openai.ChatCompletionMessageParamUnion
	for _, content := range contents {
		if content == nil || strings.TrimSpace(content.Role) == "system" {
			continue
		}
		role := strings.TrimSpace(content.Role)
		var textParts []string
		var functionCalls []*genai.FunctionCall

		for _, part := range content.Parts {
			if part == nil {
				continue
			}
			if part.Text != "" {
				textParts = append(textParts, part.Text)
			} else if part.FunctionCall != nil {
				functionCalls = append(functionCalls, part.FunctionCall)
			}
		}

		if len(functionCalls) > 0 && (role == "model" || role == "assistant") {
			toolCalls := make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(functionCalls))
			var toolResponseMessages []openai.ChatCompletionMessageParamUnion
			for _, fc := range functionCalls {
				if fc == nil || strings.TrimSpace(fc.ID) == "" {
					continue
				}
				argsJSON, _ := json.Marshal(fc.Args)
				toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
						ID:   fc.ID,
						Type: constant.Function("function"),
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name:      fc.Name,
							Arguments: string(argsJSON),
						},
					},
				})
				contentStr := "No response available for this function call."
				if fr := functionResponses[fc.ID]; fr != nil {
					contentStr = oaiFunctionResponseContent(fr.Response)
				}
				toolResponseMessages = append(toolResponseMessages, openai.ToolMessage(contentStr, fc.ID))
			}
			asst := openai.ChatCompletionAssistantMessageParam{
				Role:      constant.Assistant("assistant"),
				ToolCalls: toolCalls,
			}
			if len(textParts) > 0 {
				asst.Content.OfString = param.NewOpt(strings.Join(textParts, "\n"))
			}
			messages = append(messages, openai.ChatCompletionMessageParamUnion{OfAssistant: &asst})
			messages = append(messages, toolResponseMessages...)
		} else if len(textParts) > 0 {
			text := strings.Join(textParts, "\n")
			if role == "model" || role == "assistant" {
				asst := openai.ChatCompletionAssistantMessageParam{
					Role: constant.Assistant("assistant"),
				}
				asst.Content.OfString = param.NewOpt(text)
				messages = append(messages, openai.ChatCompletionMessageParamUnion{OfAssistant: &asst})
			} else {
				messages = append(messages, openai.UserMessage(text))
			}
		}
	}
	return messages, systemInstruction
}

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

func oaiGenaiToolsToOpenAI(tools []*genai.Tool) []openai.ChatCompletionToolUnionParam {
	var out []openai.ChatCompletionToolUnionParam
	for _, t := range tools {
		if t == nil || t.FunctionDeclarations == nil {
			continue
		}
		for _, fd := range t.FunctionDeclarations {
			if fd == nil {
				continue
			}
			paramsMap := make(shared.FunctionParameters)
			if fd.ParametersJsonSchema != nil {
				if m, ok := fd.ParametersJsonSchema.(map[string]any); ok {
					maps.Copy(paramsMap, m)
				}
			}
			if _, ok := paramsMap["type"]; !ok {
				paramsMap["type"] = "object"
			}
			if paramsMap["type"] == "object" {
				if _, ok := paramsMap["properties"]; !ok {
					paramsMap["properties"] = map[string]any{}
				}
			}
			def := shared.FunctionDefinitionParam{
				Name:        fd.Name,
				Parameters:  paramsMap,
				Description: openai.String(fd.Description),
			}
			out = append(out, openai.ChatCompletionFunctionTool(def))
		}
	}
	return out
}

// oaiStreamState holds accumulated state from processing OpenAI stream chunks (Chat Completions path).
type oaiStreamState struct {
	text             string
	toolCalls        map[int64]map[string]any
	finishReason     string
	promptTokens     int64
	completionTokens int64
}

// accumulateOaiToolCall updates the tool call accumulator with a single delta chunk.
func accumulateOaiToolCall(acc map[int64]map[string]any, idx int64, id, name, arguments string) {
	if acc[idx] == nil {
		acc[idx] = map[string]any{"id": "", "name": "", "arguments": ""}
	}
	if id != "" {
		acc[idx]["id"] = id
	}
	if name != "" {
		acc[idx]["name"] = name
	}
	if arguments != "" {
		prev, _ := acc[idx]["arguments"].(string)
		acc[idx]["arguments"] = prev + arguments
	}
}

// buildOaiFinalResponse constructs the final LLMResponse from accumulated streaming state.
func buildOaiFinalResponse(s *oaiStreamState) *model.LLMResponse {
	indices := make([]int64, 0, len(s.toolCalls))
	for k := range s.toolCalls {
		indices = append(indices, k)
	}
	slices.Sort(indices)

	finalParts := make([]*genai.Part, 0, 1+len(s.toolCalls))
	if s.text != "" {
		finalParts = append(finalParts, &genai.Part{Text: s.text})
	}
	for _, idx := range indices {
		tc := s.toolCalls[idx]
		argsStr, _ := tc["arguments"].(string)
		var args map[string]any
		if argsStr != "" {
			_ = json.Unmarshal([]byte(argsStr), &args)
		}
		name, _ := tc["name"].(string)
		id, _ := tc["id"].(string)
		if name != "" || id != "" {
			p := genai.NewPartFromFunctionCall(name, args)
			p.FunctionCall.ID = id
			finalParts = append(finalParts, p)
		}
	}

	var usage *genai.GenerateContentResponseUsageMetadata
	if s.promptTokens > 0 || s.completionTokens > 0 {
		usage = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     int32(s.promptTokens),
			CandidatesTokenCount: int32(s.completionTokens),
		}
	}
	return &model.LLMResponse{
		Partial:       false,
		TurnComplete:  true,
		FinishReason:  oaiFinishReasonToGenai(s.finishReason),
		UsageMetadata: usage,
		Content:       &genai.Content{Role: string(genai.RoleModel), Parts: finalParts},
	}
}

func oaiRunStreaming(ctx context.Context, client *openai.Client, params openai.ChatCompletionNewParams, yield func(*model.LLMResponse, error) bool) {
	params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
		IncludeUsage: param.NewOpt(true),
	}
	stream := client.Chat.Completions.NewStreaming(ctx, params)
	//nolint:errcheck // Close() may return error but we can't recover from it in defer
	defer stream.Close()

	state := &oaiStreamState{toolCalls: make(map[int64]map[string]any)}

	for stream.Next() {
		chunk := stream.Current()
		if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			state.promptTokens = chunk.Usage.PromptTokens
			state.completionTokens = chunk.Usage.CompletionTokens
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		delta := choice.Delta
		if delta.Content != "" {
			state.text += delta.Content
			if !yield(&model.LLMResponse{
				Partial:      true,
				TurnComplete: false,
				Content:      &genai.Content{Role: string(genai.RoleModel), Parts: []*genai.Part{{Text: delta.Content}}},
			}, nil) {
				return
			}
		}
		for _, tc := range delta.ToolCalls {
			accumulateOaiToolCall(state.toolCalls, tc.Index, tc.ID, tc.Function.Name, tc.Function.Arguments)
		}
		if choice.FinishReason != "" {
			state.finishReason = choice.FinishReason
		}
	}

	if err := stream.Err(); err != nil {
		if ctx.Err() == context.Canceled {
			return
		}
		_ = yield(&model.LLMResponse{ErrorCode: "STREAM_ERROR", ErrorMessage: err.Error()}, nil)
		return
	}

	_ = yield(buildOaiFinalResponse(state), nil)
}

func oaiRunNonStreaming(ctx context.Context, client *openai.Client, params openai.ChatCompletionNewParams, yield func(*model.LLMResponse, error) bool) {
	completion, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		yield(nil, fmt.Errorf("OpenAI chat completion failed: %w", err))
		return
	}
	if len(completion.Choices) == 0 {
		yield(&model.LLMResponse{ErrorCode: "API_ERROR", ErrorMessage: "no choices in response"}, nil)
		return
	}
	choice := completion.Choices[0]
	msg := choice.Message
	parts := make([]*genai.Part, 0, 1+len(msg.ToolCalls))
	if msg.Content != "" {
		parts = append(parts, &genai.Part{Text: msg.Content})
	}
	for _, tc := range msg.ToolCalls {
		if tc.Type == "function" && tc.Function.Name != "" {
			var args map[string]any
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			}
			p := genai.NewPartFromFunctionCall(tc.Function.Name, args)
			p.FunctionCall.ID = tc.ID
			parts = append(parts, p)
		}
	}
	var usage *genai.GenerateContentResponseUsageMetadata
	if completion.Usage.PromptTokens > 0 || completion.Usage.CompletionTokens > 0 {
		usage = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     int32(completion.Usage.PromptTokens),
			CandidatesTokenCount: int32(completion.Usage.CompletionTokens),
		}
	}
	yield(&model.LLMResponse{
		Partial:       false,
		TurnComplete:  true,
		FinishReason:  oaiFinishReasonToGenai(choice.FinishReason),
		UsageMetadata: usage,
		Content:       &genai.Content{Role: string(genai.RoleModel), Parts: parts},
	}, nil)
}

// NewAzureOpenAI creates an Azure OpenAI model.LLM.
// It uses AZURE_OPENAI_API_KEY, AZURE_OPENAI_ENDPOINT, and OPENAI_API_VERSION (defaults to 2025-04-01-preview).
// The deploymentName is the Azure deployment name (not the model ID).
func NewAzureOpenAI(_ context.Context, deploymentName, apiKey, endpoint, apiVersion string, llmOpts *LLMOptions) (model.LLM, error) {
	if apiKey == "" {
		apiKey = osGetenv("AZURE_OPENAI_API_KEY")
		if apiKey == "" {
			apiKey = osGetenv("AZUREOPENAI_API_KEY")
		}
		if apiKey == "" {
			apiKey = osGetenv("AZURE_API_KEY")
		}
	}
	if endpoint == "" {
		endpoint = osGetenv("AZURE_OPENAI_ENDPOINT")
	}
	if apiVersion == "" {
		apiVersion = osGetenv("OPENAI_API_VERSION")
	}
	if apiVersion == "" {
		apiVersion = "2025-04-01-preview"
	}
	if endpoint == "" {
		return nil, fmt.Errorf("azure OpenAI endpoint is required (set AZURE_OPENAI_ENDPOINT)")
	}

	baseURL := strings.TrimSuffix(endpoint, "/") + "/"
	opts := []option.RequestOption{option.WithBaseURL(baseURL)}
	// Some enterprise gateways expose Azure models behind an OpenAI-compatible
	// proxy path (for example, /api/v1/proxy). In that mode, Azure-specific path
	// rewriting and forced api-version query parameters break routing/auth.
	if !isAzureOpenAICompatProxyEndpoint(endpoint) {
		opts = append(opts,
			option.WithQueryAdd("api-version", apiVersion),
			option.WithMiddleware(azurePathRewriteMiddleware()),
		)
	}
	if apiKey != "" {
		opts = append(opts, option.WithHeader("Api-Key", apiKey))
	}
	if llmOpts != nil {
		for k, v := range llmOpts.ExtraHeaders {
			opts = append(opts, option.WithHeader(k, v))
		}
		if transport := BuildTransport(llmOpts); transport != nil {
			opts = append(opts, option.WithHTTPClient(&http.Client{Transport: transport}))
		}
	}

	client := openai.NewClient(opts...)
	return &openaiModel{modelName: deploymentName, client: client}, nil
}

func isAzureOpenAICompatProxyEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	path := strings.ToLower(strings.TrimSpace(u.Path))
	if path == "" || path == "/" {
		return false
	}
	return strings.Contains(path, "/openai/v1")
}

// azurePathRewriteMiddleware rewrites .../chat/completions and .../responses to
// .../openai/deployments/{deployment}/... so Azure can route to the correct deployment.
func azurePathRewriteMiddleware() option.Middleware {
	return func(r *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		pathSuffix := strings.TrimPrefix(r.URL.Path, "/")
		var suffix string
		switch {
		case strings.HasSuffix(pathSuffix, "chat/completions"):
			suffix = "chat/completions"
		case strings.HasSuffix(pathSuffix, "responses"):
			suffix = "responses"
		case strings.HasSuffix(pathSuffix, "completions"):
			suffix = "completions"
		case strings.HasSuffix(pathSuffix, "embeddings"):
			suffix = "embeddings"
		default:
			return next(r)
		}
		if r.Body == nil {
			return next(r)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(r.Body); err != nil {
			return nil, err
		}
		r.Body = io.NopCloser(&buf)
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&payload); err != nil || payload.Model == "" {
			r.Body = io.NopCloser(bytes.NewReader(buf.Bytes()))
			return next(r)
		}
		deployment := url.PathEscape(payload.Model)
		// Keep base path (e.g. /api/v1/proxy), replace suffix with Azure-style path
		basePath := strings.TrimSuffix(r.URL.Path, suffix)
		basePath = strings.TrimRight(basePath, "/")
		r.URL.Path = basePath + "/openai/deployments/" + deployment + "/" + suffix
		r.Body = io.NopCloser(bytes.NewReader(buf.Bytes()))
		return next(r)
	}
}

// osGetenv wraps os.Getenv for testability.
var osGetenv = func(key string) string {
	return os.Getenv(key)
}
