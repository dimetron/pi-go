package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	ollamaapi "github.com/ollama/ollama/api"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// ollamaModel implements model.LLM for the native Ollama API.
type ollamaModel struct {
	modelName     string
	client        *ollamaapi.Client
	thinkingLevel string // "none", "low", "medium", "high"
}

// NewOllama creates an Ollama model.LLM using the native Ollama Go client.
// baseURL defaults to http://localhost:11434 if empty, or https://api.ollama.com
// if an apiKey is provided (ollama.com cloud).
// thinkingLevel controls extended thinking: "none", "low", "medium", "high".
func NewOllama(_ context.Context, modelName, apiKey, baseURL, thinkingLevel string, opts *LLMOptions) (model.LLM, error) {
	if modelName == "" {
		return nil, fmt.Errorf("model name is required")
	}
	baseURL = normalizeBaseURL(baseURL)
	if baseURL == "" {
		if apiKey != "" {
			baseURL = "https://api.ollama.com"
		} else {
			baseURL = "http://localhost:11434"
		}
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Ollama URL %q: %w", baseURL, err)
	}
	httpClient, err := BuildHTTPClient(opts, 10*time.Minute)
	if err != nil {
		return nil, err
	}
	// Inject Bearer token for ollama.com cloud API when an API key is provided.
	if apiKey != "" {
		baseTransport := httpClient.Transport
		if baseTransport == nil {
			baseTransport = http.DefaultTransport
		}
		httpClient.Transport = &bearerTransport{
			base:  baseTransport,
			token: apiKey,
		}
	}
	client := ollamaapi.NewClient(u, httpClient)
	return &ollamaModel{
		modelName:     modelName,
		client:        client,
		thinkingLevel: thinkingLevel,
	}, nil
}

// bearerTransport injects an Authorization: Bearer header into every request.
type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req)
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

		// Cloud models (e.g. "model:cloud") support large context windows.
		if strings.HasSuffix(modelName, ":cloud") {
			chatReq.Options = map[string]any{"num_ctx": 262144} // 256K
		}

		// Configure thinking. nothink models must not have thinking forced on.
		if strings.Contains(strings.ToLower(modelName), "nothink") {
			chatReq.Think = &ollamaapi.ThinkValue{Value: false}
		} else if thinkCfg := ollamaThinkingConfig(m.thinkingLevel); thinkCfg != nil {
			chatReq.Think = thinkCfg
		}

		// Convert tools.
		if req.Config != nil && len(req.Config.Tools) > 0 {
			chatReq.Tools = ollamaGenaiToolsToOllama(req.Config.Tools)
		}

		if stream {
			ollamaRunStreaming(ctx, m.client, chatReq, yield)
		} else {
			chatReq.Stream = new(false)
			ollamaRunNonStreaming(ctx, m.client, chatReq, yield)
		}
	}
}

// ollamaThinkingConfig maps a thinking level string to Ollama ThinkValue.
//
// "none" returns an explicit false rather than nil: omitting the think field
// leaves the model's own default in force, and thinking-capable models such as
// gemma-4 then think anyway, spending latency and tokens the user asked to
// avoid. Unrecognized levels (including "") still return nil so the model
// default applies.
func ollamaThinkingConfig(level string) *ollamaapi.ThinkValue {
	switch level {
	case "none":
		return &ollamaapi.ThinkValue{Value: false}
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

			if m := schemaToMap(fd.ParametersJsonSchema); m != nil {
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

// schemaToMap normalizes a genai FunctionDeclaration.ParametersJsonSchema (typed
// as `any`) into a plain map[string]any. The pi-go tools registry sets this field
// to a typed *jsonschema.Schema, so a direct map assertion fails and the tool would
// be advertised to the model with no properties — leaving weaker models (e.g.
// minimax-m3) unable to tell which arguments to send, so every parameterized call
// arrives empty. Marshaling through JSON (jsonschema.Schema has a custom
// MarshalJSON) yields a faithful schema map regardless of the concrete type, while
// still accepting a raw map for callers/tests that pass one directly.
func schemaToMap(raw any) map[string]any {
	if raw == nil {
		return nil
	}
	if m, ok := raw.(map[string]any); ok {
		return m
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
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
				return fmt.Errorf("yield canceled")
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
				return fmt.Errorf("yield canceled")
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
	} else if aggregatedThinking != "" && len(toolCalls) == 0 {
		// Fallback: model responded entirely via thinking tokens (e.g. thinking forced
		// on a nothink model). Surface the thinking content rather than returning nothing.
		//
		// Only when the turn produced no tool call. A turn that thinks and then
		// calls a tool has already said something, so the fallback isn't needed
		// — and firing it there restates the reasoning as if it were the answer
		// (seen with minimax-m3:cloud: "The user wants me to run a bash
		// command..." printed above the real reply).
		finalParts = append(finalParts, &genai.Part{Text: aggregatedThinking})
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

// OllamaContextWindowSize queries the Ollama server for the context window size
// of the given model. Returns 0 if the size cannot be determined.
func OllamaContextWindowSize(ctx context.Context, baseURL, modelName string) int64 {
	baseURL = normalizeBaseURL(baseURL)
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return 0
	}
	client := ollamaapi.NewClient(u, &http.Client{Timeout: 10 * time.Second})
	resp, err := client.Show(ctx, &ollamaapi.ShowRequest{Model: modelName})
	if err != nil {
		return 0
	}

	// Parameters is a newline-separated list of "key value" pairs.
	// num_ctx takes precedence as it reflects the configured context window.
	for _, line := range strings.Split(resp.Parameters, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "num_ctx" {
			if n, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
				return n
			}
		}
	}

	// Fall back to the model's native context length from ModelInfo.
	for key, val := range resp.ModelInfo {
		if strings.HasSuffix(key, ".context_length") {
			switch v := val.(type) {
			case float64:
				return int64(v)
			case int64:
				return v
			case int:
				return int64(v)
			}
		}
	}

	return 0
}
