package provider

import (
	"context"
	"fmt"
	"iter"
	"net/http"
	"net/url"
	"strings"
	"time"

	ollamaapi "github.com/ollama/ollama/api"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// ollamaModel implements model.LLM for the native Ollama API.
type ollamaModel struct {
	modelName     string
	client        *ollamaapi.Client
	thinkingLevel string // "none", "low", "medium", "high"
}

// NewOllama creates an Ollama model.LLM using the native Ollama Go client.
// baseURL defaults to http://localhost:11434 if empty.
// thinkingLevel controls extended thinking: "none", "low", "medium", "high".
func NewOllama(_ context.Context, modelName, baseURL, thinkingLevel string, opts *LLMOptions) (model.LLM, error) {
	if modelName == "" {
		return nil, fmt.Errorf("model name is required")
	}
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Ollama URL %q: %w", baseURL, err)
	}
	httpClient := BuildHTTPClient(opts, 10*time.Minute)
	client := ollamaapi.NewClient(u, httpClient)
	return &ollamaModel{
		modelName:     modelName,
		client:        client,
		thinkingLevel: thinkingLevel,
	}, nil
}

func (m *ollamaModel) Name() string { return m.modelName }

func (m *ollamaModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		messages, systemPrompt := ollamaContentsToMessages(req.Contents, req.Config)

		// Prepend system message if present.
		if systemPrompt != "" {
			messages = append([]ollamaapi.Message{{Role: "system", Content: systemPrompt}}, messages...)
		}

		modelName := m.modelName
		if req.Model != "" {
			modelName = req.Model
		}

		chatReq := &ollamaapi.ChatRequest{
			Model:    modelName,
			Messages: messages,
		}

		// Configure thinking.
		thinkCfg := ollamaThinkingConfig(m.thinkingLevel)
		if thinkCfg != nil {
			chatReq.Think = thinkCfg
		}

		// Convert tools.
		if req.Config != nil && len(req.Config.Tools) > 0 {
			chatReq.Tools = ollamaGenaiToolsToOllama(req.Config.Tools)
		}

		if stream {
			ollamaRunStreaming(ctx, m.client, chatReq, yield)
		} else {
			streamOff := false
			chatReq.Stream = &streamOff
			ollamaRunNonStreaming(ctx, m.client, chatReq, yield)
		}
	}
}

// ollamaThinkingConfig maps a thinking level string to Ollama ThinkValue.
func ollamaThinkingConfig(level string) *ollamaapi.ThinkValue {
	switch level {
	case "low", "medium", "high":
		return &ollamaapi.ThinkValue{Value: level}
	default:
		return nil
	}
}

// ollamaFinishReasonToGenai maps Ollama done_reason to genai.FinishReason.
func ollamaFinishReasonToGenai(reason string) genai.FinishReason {
	switch reason {
	case "length":
		return genai.FinishReasonMaxTokens
	default:
		return genai.FinishReasonStop
	}
}

// ollamaContentsToMessages converts genai.Content to Ollama messages.
func ollamaContentsToMessages(contents []*genai.Content, config *genai.GenerateContentConfig) ([]ollamaapi.Message, string) {
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

	// Collect function responses for matching.
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

	var messages []ollamaapi.Message
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
			// Assistant message with tool calls.
			toolCalls := make([]ollamaapi.ToolCall, 0, len(functionCalls))
			for _, fc := range functionCalls {
				args := ollamaapi.NewToolCallFunctionArguments()
				for k, v := range fc.Args {
					args.Set(k, v)
				}
				toolCalls = append(toolCalls, ollamaapi.ToolCall{
					ID: fc.ID,
					Function: ollamaapi.ToolCallFunction{
						Name:      fc.Name,
						Arguments: args,
					},
				})
			}

			msg := ollamaapi.Message{
				Role:      "assistant",
				ToolCalls: toolCalls,
			}
			if len(textParts) > 0 {
				msg.Content = strings.Join(textParts, "\n")
			}
			messages = append(messages, msg)

			// Tool results as separate messages.
			for _, fc := range functionCalls {
				contentStr := ""
				if fr := functionResponses[fc.ID]; fr != nil {
					contentStr = oaiFunctionResponseContent(fr.Response) // reuse helper
				}
				messages = append(messages, ollamaapi.Message{
					Role:       "tool",
					Content:    contentStr,
					ToolCallID: fc.ID,
				})
			}
		} else if len(textParts) > 0 {
			msgRole := "user"
			if role == "model" || role == "assistant" {
				msgRole = "assistant"
			}
			messages = append(messages, ollamaapi.Message{
				Role:    msgRole,
				Content: strings.Join(textParts, "\n"),
			})
		}
	}

	// Ensure at least one message.
	if len(messages) == 0 {
		messages = append(messages, ollamaapi.Message{
			Role:    "user",
			Content: "Hello",
		})
	}

	return messages, systemPrompt
}

// ollamaGenaiToolsToOllama converts genai tools to Ollama native tool format.
func ollamaGenaiToolsToOllama(tools []*genai.Tool) ollamaapi.Tools {
	var out ollamaapi.Tools
	for _, t := range tools {
		if t == nil || t.FunctionDeclarations == nil {
			continue
		}
		for _, fd := range t.FunctionDeclarations {
			if fd == nil {
				continue
			}
			params := ollamaapi.ToolFunctionParameters{
				Type:       "object",
				Properties: ollamaapi.NewToolPropertiesMap(),
			}

			if fd.ParametersJsonSchema != nil {
				if m, ok := fd.ParametersJsonSchema.(map[string]any); ok {
					if props, ok := m["properties"].(map[string]any); ok {
						for name, propRaw := range props {
							prop := convertToToolProperty(propRaw)
							params.Properties.Set(name, prop)
						}
					}
					if required, ok := m["required"].([]any); ok {
						for _, r := range required {
							if s, ok := r.(string); ok {
								params.Required = append(params.Required, s)
							}
						}
					}
				}
			}

			out = append(out, ollamaapi.Tool{
				Type: "function",
				Function: ollamaapi.ToolFunction{
					Name:        fd.Name,
					Description: fd.Description,
					Parameters:  params,
				},
			})
		}
	}
	return out
}

// convertToToolProperty converts a raw JSON schema property to Ollama ToolProperty.
func convertToToolProperty(raw any) ollamaapi.ToolProperty {
	prop := ollamaapi.ToolProperty{}
	m, ok := raw.(map[string]any)
	if !ok {
		return prop
	}
	if t, ok := m["type"].(string); ok {
		prop.Type = ollamaapi.PropertyType{t}
	}
	if d, ok := m["description"].(string); ok {
		prop.Description = d
	}
	if e, ok := m["enum"].([]any); ok {
		prop.Enum = e
	}
	return prop
}

func ollamaRunStreaming(ctx context.Context, client *ollamaapi.Client, chatReq *ollamaapi.ChatRequest, yield func(*model.LLMResponse, error) bool) {
	var aggregatedText string
	var aggregatedThinking string
	var toolCalls []ollamaapi.ToolCall
	var doneReason string
	var promptTokens, evalTokens int

	err := client.Chat(ctx, chatReq, func(resp ollamaapi.ChatResponse) error {
		msg := resp.Message

		// Yield thinking content as partial response.
		if msg.Thinking != "" {
			aggregatedThinking += msg.Thinking
			if !yield(&model.LLMResponse{
				Partial:      true,
				TurnComplete: false,
				Content:      &genai.Content{Role: "thinking", Parts: []*genai.Part{{Text: msg.Thinking}}},
			}, nil) {
				return fmt.Errorf("yield cancelled")
			}
		}

		// Yield text content as partial response.
		if msg.Content != "" {
			aggregatedText += msg.Content
			if !yield(&model.LLMResponse{
				Partial:      true,
				TurnComplete: false,
				Content:      &genai.Content{Role: string(genai.RoleModel), Parts: []*genai.Part{{Text: msg.Content}}},
			}, nil) {
				return fmt.Errorf("yield cancelled")
			}
		}

		// Accumulate tool calls.
		if len(msg.ToolCalls) > 0 {
			toolCalls = append(toolCalls, msg.ToolCalls...)
		}

		// Capture metrics from final response.
		if resp.Done {
			doneReason = resp.DoneReason
			promptTokens = resp.PromptEvalCount
			evalTokens = resp.EvalCount
		}

		return nil
	})

	if err != nil {
		if ctx.Err() == context.Canceled {
			return
		}
		_ = yield(&model.LLMResponse{ErrorCode: "STREAM_ERROR", ErrorMessage: err.Error()}, nil)
		return
	}

	// Build final response.
	finalParts := make([]*genai.Part, 0, 1+len(toolCalls))
	if aggregatedText != "" {
		finalParts = append(finalParts, &genai.Part{Text: aggregatedText})
	}
	for _, tc := range toolCalls {
		args := tc.Function.Arguments.ToMap()
		p := genai.NewPartFromFunctionCall(tc.Function.Name, args)
		p.FunctionCall.ID = tc.ID
		finalParts = append(finalParts, p)
	}

	var usage *genai.GenerateContentResponseUsageMetadata
	if promptTokens > 0 || evalTokens > 0 {
		usage = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     int32(promptTokens),
			CandidatesTokenCount: int32(evalTokens),
		}
	}
	_ = yield(&model.LLMResponse{
		Partial:       false,
		TurnComplete:  true,
		FinishReason:  ollamaFinishReasonToGenai(doneReason),
		UsageMetadata: usage,
		Content:       &genai.Content{Role: string(genai.RoleModel), Parts: finalParts},
	}, nil)
}

func ollamaRunNonStreaming(ctx context.Context, client *ollamaapi.Client, chatReq *ollamaapi.ChatRequest, yield func(*model.LLMResponse, error) bool) {
	var finalResp ollamaapi.ChatResponse

	err := client.Chat(ctx, chatReq, func(resp ollamaapi.ChatResponse) error {
		finalResp = resp
		return nil
	})

	if err != nil {
		yield(nil, fmt.Errorf("ollama API error: %w", err))
		return
	}

	msg := finalResp.Message
	parts := make([]*genai.Part, 0, 1+len(msg.ToolCalls))

	// Include thinking content as text if present.
	if msg.Thinking != "" {
		parts = append(parts, &genai.Part{Text: msg.Thinking})
	}
	if msg.Content != "" {
		parts = append(parts, &genai.Part{Text: msg.Content})
	}

	for _, tc := range msg.ToolCalls {
		args := tc.Function.Arguments.ToMap()
		p := genai.NewPartFromFunctionCall(tc.Function.Name, args)
		p.FunctionCall.ID = tc.ID
		parts = append(parts, p)
	}

	var usage *genai.GenerateContentResponseUsageMetadata
	if finalResp.PromptEvalCount > 0 || finalResp.EvalCount > 0 {
		usage = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     int32(finalResp.PromptEvalCount),
			CandidatesTokenCount: int32(finalResp.EvalCount),
		}
	}

	yield(&model.LLMResponse{
		Partial:       false,
		TurnComplete:  true,
		FinishReason:  ollamaFinishReasonToGenai(finalResp.DoneReason),
		UsageMetadata: usage,
		Content:       &genai.Content{Role: string(genai.RoleModel), Parts: parts},
	}, nil)
}

// OllamaListModels lists available models from the Ollama server.
func OllamaListModels(ctx context.Context, baseURL string) ([]string, error) {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	client := ollamaapi.NewClient(u, &http.Client{Timeout: 10 * time.Second})
	resp, err := client.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing models: %w", err)
	}
	var names []string
	for _, m := range resp.Models {
		names = append(names, m.Name)
	}
	return names, nil
}
