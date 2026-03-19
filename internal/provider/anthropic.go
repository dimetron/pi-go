package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicopt "github.com/anthropics/anthropic-sdk-go/option"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

const defaultMaxTokens = 8192

// anthropicModel implements model.LLM for the Anthropic API.
type anthropicModel struct {
	modelName     string
	client        anthropic.Client
	thinkingLevel string // "none", "low", "medium", "high"
}

// NewAnthropic creates an Anthropic model.LLM.
// If baseURL is non-empty, it overrides the default API endpoint.
// When baseURL is set, the API key is optional (for Ollama compatibility).
// thinkingLevel controls extended thinking: "none", "low", "medium", "high".
func NewAnthropic(_ context.Context, modelName, apiKey, baseURL, thinkingLevel string, extraHeaders map[string]string) (model.LLM, error) {
	if apiKey == "" && baseURL == "" {
		return nil, fmt.Errorf("anthropic API key is required")
	}
	var opts []anthropicopt.RequestOption
	if apiKey != "" {
		opts = append(opts, anthropicopt.WithAPIKey(apiKey))
	}
	if baseURL != "" {
		opts = append(opts, anthropicopt.WithBaseURL(baseURL))
	}
	for k, v := range extraHeaders {
		opts = append(opts, anthropicopt.WithHeader(k, v))
	}
	client := anthropic.NewClient(opts...)
	return &anthropicModel{modelName: modelName, client: client, thinkingLevel: thinkingLevel}, nil
}

func (m *anthropicModel) Name() string { return m.modelName }

func (m *anthropicModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		messages, systemPrompt := antContentsToMessages(req.Contents, req.Config)

		modelName := m.modelName
		if req.Model != "" && req.Model != "anthropic" {
			modelName = req.Model
		}
		if modelName == "" || modelName == "anthropic" {
			modelName = "claude-sonnet-4-6"
		}

		maxTokens := int64(defaultMaxTokens)
		thinkingCfg := antThinkingConfig(m.thinkingLevel)
		if thinkingCfg != nil {
			// Thinking requires higher max_tokens to accommodate the thinking budget.
			maxTokens = 16384
		}

		params := anthropic.MessageNewParams{
			Model:     anthropic.Model(modelName),
			Messages:  messages,
			MaxTokens: maxTokens,
		}

		if thinkingCfg != nil {
			params.Thinking = *thinkingCfg
		}

		if systemPrompt != "" {
			params.System = []anthropic.TextBlockParam{
				{Text: systemPrompt},
			}
		}

		if req.Config != nil && len(req.Config.Tools) > 0 {
			params.Tools = antGenaiToolsToAnthropic(req.Config.Tools)
		}

		if stream {
			antRunStreaming(ctx, &m.client, params, yield)
		} else {
			antRunNonStreaming(ctx, &m.client, params, yield)
		}
	}
}

// antStopReasonToGenai maps Anthropic stop reason to genai.FinishReason.
func antStopReasonToGenai(reason anthropic.StopReason) genai.FinishReason {
	switch reason {
	case anthropic.StopReasonMaxTokens:
		return genai.FinishReasonMaxTokens
	case anthropic.StopReasonEndTurn, anthropic.StopReasonToolUse:
		return genai.FinishReasonStop
	default:
		return genai.FinishReasonStop
	}
}

func antContentsToMessages(contents []*genai.Content, config *genai.GenerateContentConfig) ([]anthropic.MessageParam, string) {
	var systemBuilder strings.Builder
	if config != nil && config.SystemInstruction != nil {
		for _, p := range config.SystemInstruction.Parts {
			if p != nil && p.Text != "" {
				systemBuilder.WriteString(p.Text)
				systemBuilder.WriteByte('\n')
			}
		}
	}
	systemPrompt := strings.TrimSpace(systemBuilder.String())

	// Collect function responses for matching
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

	var messages []anthropic.MessageParam
	for _, content := range contents {
		if content == nil {
			continue
		}
		role := strings.TrimSpace(content.Role)
		if role == "system" {
			continue
		}

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
			// Assistant message with tool use blocks
			var contentBlocks []anthropic.ContentBlockParamUnion
			if len(textParts) > 0 {
				contentBlocks = append(contentBlocks, anthropic.NewTextBlock(strings.Join(textParts, "\n")))
			}
			for _, fc := range functionCalls {
				argsJSON, _ := json.Marshal(fc.Args)
				var inputMap map[string]any
				_ = json.Unmarshal(argsJSON, &inputMap)
				if inputMap == nil {
					inputMap = make(map[string]any)
				}
				contentBlocks = append(contentBlocks, anthropic.NewToolUseBlock(fc.ID, inputMap, fc.Name))
			}
			messages = append(messages, anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleAssistant,
				Content: contentBlocks,
			})

			// Tool results as user message
			var toolResultBlocks []anthropic.ContentBlockParamUnion
			for _, fc := range functionCalls {
				contentStr := "No response available for this function call."
				if fr := functionResponses[fc.ID]; fr != nil {
					contentStr = oaiFunctionResponseContent(fr.Response) // reuse helper
				}
				toolResultBlocks = append(toolResultBlocks, anthropic.NewToolResultBlock(fc.ID, contentStr, false))
			}
			messages = append(messages, anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleUser,
				Content: toolResultBlocks,
			})
		} else if len(textParts) > 0 {
			msgRole := anthropic.MessageParamRoleUser
			if role == "model" || role == "assistant" {
				msgRole = anthropic.MessageParamRoleAssistant
			}
			var contentBlocks []anthropic.ContentBlockParamUnion
			contentBlocks = append(contentBlocks, anthropic.NewTextBlock(strings.Join(textParts, "\n")))
			messages = append(messages, anthropic.MessageParam{
				Role:    msgRole,
				Content: contentBlocks,
			})
		}
	}

	// Ollama and some Anthropic-compatible endpoints require at least one message.
	// If no messages were produced (e.g. first call with only system content),
	// add a minimal user message to avoid "messages is required" errors.
	if len(messages) == 0 {
		messages = append(messages, anthropic.MessageParam{
			Role:    anthropic.MessageParamRoleUser,
			Content: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("Hello")},
		})
	}

	return messages, systemPrompt
}

func antGenaiToolsToAnthropic(tools []*genai.Tool) []anthropic.ToolUnionParam {
	var out []anthropic.ToolUnionParam
	for _, t := range tools {
		if t == nil || t.FunctionDeclarations == nil {
			continue
		}
		for _, fd := range t.FunctionDeclarations {
			if fd == nil {
				continue
			}
			inputSchema := anthropic.ToolInputSchemaParam{
				Properties: make(map[string]any),
			}
			if fd.ParametersJsonSchema != nil {
				if m, ok := fd.ParametersJsonSchema.(map[string]any); ok {
					if props, ok := m["properties"].(map[string]any); ok {
						inputSchema.Properties = props
					}
					if required, ok := m["required"].([]any); ok {
						reqStrings := make([]string, 0, len(required))
						for _, r := range required {
							if s, ok := r.(string); ok {
								reqStrings = append(reqStrings, s)
							}
						}
						inputSchema.Required = reqStrings
					}
				}
			}
			tool := anthropic.ToolParam{
				Name:        fd.Name,
				Description: anthropic.String(fd.Description),
				InputSchema: inputSchema,
			}
			out = append(out, anthropic.ToolUnionParam{OfTool: &tool})
		}
	}
	return out
}

// antThinkingConfig maps a thinking level string to Anthropic thinking config.
func antThinkingConfig(level string) *anthropic.ThinkingConfigParamUnion {
	var budget int64
	switch level {
	case "low":
		budget = 2048
	case "medium":
		budget = 4096
	case "high":
		budget = 8192
	default:
		return nil
	}
	cfg := anthropic.ThinkingConfigParamOfEnabled(budget)
	return &cfg
}

func antRunStreaming(ctx context.Context, client *anthropic.Client, params anthropic.MessageNewParams, yield func(*model.LLMResponse, error) bool) {
	stream := client.Messages.NewStreaming(ctx, params)
	//nolint:errcheck // Close() may return error but we can't recover from it in defer
	defer stream.Close()

	var aggregatedText string
	toolUseBlocks := make(map[int]struct {
		id        string
		name      string
		inputJSON string
	})
	var stopReason anthropic.StopReason
	var inputTokens, outputTokens int64

	for stream.Next() {
		event := stream.Current()

		switch e := event.AsAny().(type) {
		case anthropic.MessageStartEvent:
			inputTokens = e.Message.Usage.InputTokens
		case anthropic.ContentBlockStartEvent:
			idx := int(e.Index)
			if e.ContentBlock.Type == "tool_use" {
				if toolUse, ok := e.ContentBlock.AsAny().(anthropic.ToolUseBlock); ok {
					toolUseBlocks[idx] = struct {
						id        string
						name      string
						inputJSON string
					}{id: toolUse.ID, name: toolUse.Name, inputJSON: ""}
				}
			}
		case anthropic.ContentBlockDeltaEvent:
			idx := int(e.Index)
			delta := e.Delta
			switch delta.Type {
			case "text_delta":
				if textDelta, ok := delta.AsAny().(anthropic.TextDelta); ok {
					aggregatedText += textDelta.Text
					if !yield(&model.LLMResponse{
						Partial:      true,
						TurnComplete: false,
						Content:      &genai.Content{Role: string(genai.RoleModel), Parts: []*genai.Part{{Text: textDelta.Text}}},
					}, nil) {
						return
					}
				}
			case "thinking_delta":
				if thinkingDelta, ok := delta.AsAny().(anthropic.ThinkingDelta); ok {
					// Yield thinking content as partial response with a "thinking" role marker.
					if !yield(&model.LLMResponse{
						Partial:      true,
						TurnComplete: false,
						Content:      &genai.Content{Role: "thinking", Parts: []*genai.Part{{Text: thinkingDelta.Thinking}}},
					}, nil) {
						return
					}
				}
			case "input_json_delta":
				if jsonDelta, ok := delta.AsAny().(anthropic.InputJSONDelta); ok {
					if block, exists := toolUseBlocks[idx]; exists {
						block.inputJSON += jsonDelta.PartialJSON
						toolUseBlocks[idx] = block
					}
				}
			}
		case anthropic.MessageDeltaEvent:
			stopReason = e.Delta.StopReason
			outputTokens = e.Usage.OutputTokens
		}
	}

	if err := stream.Err(); err != nil {
		if ctx.Err() == context.Canceled {
			return
		}
		_ = yield(&model.LLMResponse{ErrorCode: "STREAM_ERROR", ErrorMessage: err.Error()}, nil)
		return
	}

	// Build final response
	finalParts := make([]*genai.Part, 0, 1+len(toolUseBlocks))
	if aggregatedText != "" {
		finalParts = append(finalParts, &genai.Part{Text: aggregatedText})
	}
	for _, block := range toolUseBlocks {
		var args map[string]any
		if block.inputJSON != "" {
			_ = json.Unmarshal([]byte(block.inputJSON), &args)
		}
		if block.name != "" || block.id != "" {
			p := genai.NewPartFromFunctionCall(block.name, args)
			p.FunctionCall.ID = block.id
			finalParts = append(finalParts, p)
		}
	}

	var usage *genai.GenerateContentResponseUsageMetadata
	if inputTokens > 0 || outputTokens > 0 {
		usage = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     int32(inputTokens),
			CandidatesTokenCount: int32(outputTokens),
		}
	}
	_ = yield(&model.LLMResponse{
		Partial:       false,
		TurnComplete:  true,
		FinishReason:  antStopReasonToGenai(stopReason),
		UsageMetadata: usage,
		Content:       &genai.Content{Role: string(genai.RoleModel), Parts: finalParts},
	}, nil)
}

func antRunNonStreaming(ctx context.Context, client *anthropic.Client, params anthropic.MessageNewParams, yield func(*model.LLMResponse, error) bool) {
	message, err := client.Messages.New(ctx, params)
	if err != nil {
		yield(nil, fmt.Errorf("anthropic API error: %w", err))
		return
	}

	parts := make([]*genai.Part, 0, len(message.Content))
	for _, block := range message.Content {
		switch block.Type {
		case "text":
			if textBlock, ok := block.AsAny().(anthropic.TextBlock); ok {
				parts = append(parts, &genai.Part{Text: textBlock.Text})
			}
		case "thinking":
			// Handle thinking blocks from models like qwen3.5 that only return thinking
			// Extract thinking content as the response text
			if thinkingBlock, ok := block.AsAny().(anthropic.ThinkingBlock); ok {
				parts = append(parts, &genai.Part{Text: thinkingBlock.Thinking})
			}
		case "tool_use":
			if toolUse, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
				var args map[string]any
				inputBytes, _ := json.Marshal(toolUse.Input)
				_ = json.Unmarshal(inputBytes, &args)
				p := genai.NewPartFromFunctionCall(toolUse.Name, args)
				p.FunctionCall.ID = toolUse.ID
				parts = append(parts, p)
			}
		}
	}

	var usage *genai.GenerateContentResponseUsageMetadata
	if message.Usage.InputTokens > 0 || message.Usage.OutputTokens > 0 {
		usage = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     int32(message.Usage.InputTokens),
			CandidatesTokenCount: int32(message.Usage.OutputTokens),
		}
	}
	yield(&model.LLMResponse{
		Partial:       false,
		TurnComplete:  true,
		FinishReason:  antStopReasonToGenai(message.StopReason),
		UsageMetadata: usage,
		Content:       &genai.Content{Role: string(genai.RoleModel), Parts: parts},
	}, nil)
}
