package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"
	"github.com/openai/openai-go/v3/shared/constant"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

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
			retryStream(ctx, streamRetryConfig(), yield, func(y func(*model.LLMResponse, error) bool) {
				oaiRunStreaming(ctx, &m.client, params, y)
			})
		} else {
			oaiRunNonStreaming(ctx, &m.client, params, yield)
		}
	}
}

// The genai* helpers below are the parts of the genai.Content -> wire-format
// conversion that every provider does identically (Anthropic, Ollama, OpenAI
// Chat Completions and OpenAI Responses). Only the wire types they feed differ,
// so they live here alongside oaiFunctionResponseContent's other callers.

// genaiSystemInstruction flattens config.SystemInstruction into the single
// system prompt string the providers send out of band.
func genaiSystemInstruction(config *genai.GenerateContentConfig) string {
	var systemBuilder strings.Builder
	if config != nil && config.SystemInstruction != nil {
		for _, p := range config.SystemInstruction.Parts {
			if p != nil && p.Text != "" {
				systemBuilder.WriteString(p.Text)
				systemBuilder.WriteByte('\n')
			}
		}
	}
	return strings.TrimSpace(systemBuilder.String())
}

// genaiFunctionResponses indexes every function response in the conversation by
// its call ID, so a function call can be paired with its result.
func genaiFunctionResponses(contents []*genai.Content) map[string]*genai.FunctionResponse {
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
	return functionResponses
}

// genaiSplitParts splits one content's parts into its text fragments and its
// function calls. A part with text is never also read as a function call.
func genaiSplitParts(parts []*genai.Part) ([]string, []*genai.FunctionCall) {
	var textParts []string
	var functionCalls []*genai.FunctionCall
	for _, part := range parts {
		if part == nil {
			continue
		}
		if part.Text != "" {
			textParts = append(textParts, part.Text)
		} else if part.FunctionCall != nil {
			functionCalls = append(functionCalls, part.FunctionCall)
		}
	}
	return textParts, functionCalls
}

// genaiIsAssistantRole reports whether a genai role denotes the model's turn.
func genaiIsAssistantRole(role string) bool {
	return role == "model" || role == "assistant"
}

// oaiContentsToMessages converts genai.Content to OpenAI messages (Chat Completions path).
func oaiContentsToMessages(contents []*genai.Content, config *genai.GenerateContentConfig) ([]openai.ChatCompletionMessageParamUnion, string) {
	systemInstruction := genaiSystemInstruction(config)
	functionResponses := genaiFunctionResponses(contents)

	var messages []openai.ChatCompletionMessageParamUnion
	for _, content := range contents {
		if content == nil || strings.TrimSpace(content.Role) == "system" {
			continue
		}
		role := strings.TrimSpace(content.Role)
		textParts, functionCalls := genaiSplitParts(content.Parts)

		switch {
		case len(functionCalls) > 0 && genaiIsAssistantRole(role):
			messages = append(messages, oaiToolCallMessages(textParts, functionCalls, functionResponses)...)
		case len(textParts) > 0:
			messages = append(messages, oaiTextMessage(role, strings.Join(textParts, "\n")))
		}
	}
	return messages, systemInstruction
}

// oaiToolCallMessages renders one assistant turn that called tools: the
// assistant message carrying the tool calls, followed by one tool message per
// call holding its result.
func oaiToolCallMessages(
	textParts []string,
	functionCalls []*genai.FunctionCall,
	functionResponses map[string]*genai.FunctionResponse,
) []openai.ChatCompletionMessageParamUnion {
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
	messages := make([]openai.ChatCompletionMessageParamUnion, 0, 1+len(toolResponseMessages))
	messages = append(messages, openai.ChatCompletionMessageParamUnion{OfAssistant: &asst})
	return append(messages, toolResponseMessages...)
}

// oaiTextMessage renders a text-only turn as the assistant or user message its
// genai role calls for.
func oaiTextMessage(role, text string) openai.ChatCompletionMessageParamUnion {
	if !genaiIsAssistantRole(role) {
		return openai.UserMessage(text)
	}
	asst := openai.ChatCompletionAssistantMessageParam{
		Role: constant.Assistant("assistant"),
	}
	asst.Content.OfString = param.NewOpt(text)
	return openai.ChatCompletionMessageParamUnion{OfAssistant: &asst}
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
			paramsMap := oaiFunctionParameters(fd.ParametersJsonSchema)
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
	cachedTokens     int64
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
			PromptTokenCount:        int32(s.promptTokens),
			CandidatesTokenCount:    int32(s.completionTokens),
			CachedContentTokenCount: int32(s.cachedTokens),
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
			state.cachedTokens = chunk.Usage.PromptTokensDetails.CachedTokens
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
			_ = yield(canceledResponse(), nil)
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
			PromptTokenCount:        int32(completion.Usage.PromptTokens),
			CandidatesTokenCount:    int32(completion.Usage.CompletionTokens),
			CachedContentTokenCount: int32(completion.Usage.PromptTokensDetails.CachedTokens),
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
