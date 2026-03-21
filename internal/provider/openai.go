package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"
	"github.com/openai/openai-go/v3/shared/constant"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// openaiModel implements model.LLM for OpenAI-compatible APIs.
type openaiModel struct {
	modelName string
	client    openai.Client
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
	return &openaiModel{modelName: modelName, client: client}, nil
}

func (m *openaiModel) Name() string { return m.modelName }

func (m *openaiModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
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

		if stream {
			oaiRunStreaming(ctx, &m.client, params, yield)
		} else {
			oaiRunNonStreaming(ctx, &m.client, params, yield)
		}
	}
}

// oaiFinishReasonToGenai maps OpenAI finish_reason to genai.FinishReason.
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

// oaiContentsToMessages converts genai.Content to OpenAI messages.
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

	// Collect function responses for matching with function calls
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

// oaiStreamState holds accumulated state from processing OpenAI stream chunks.
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
