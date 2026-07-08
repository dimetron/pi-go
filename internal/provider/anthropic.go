package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"slices"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicopt "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

const defaultMaxTokens int64 = 8192
const anthropicOAuthBetaHeader = "claude-code-20250219,oauth-2025-04-20,fine-grained-tool-streaming-2025-05-14"
const anthropicOAuthUserAgent = "claude-cli/2.1.75"

// anthropicModel implements model.LLM for the Anthropic API.
type anthropicModel struct {
	modelName      string
	client         anthropic.Client
	betaClient     anthropic.BetaService
	thinkingLevel  string // "none", "low", "medium", "high"
	advisorModel   string // Advisor model (e.g., "claude-opus-4-7")
	advisorMaxUses int    // Max advisor calls per request (0 = unlimited)
	advisorCaching bool   // Enable ephemeral prompt caching for advisor
}

// NewAnthropic creates an Anthropic model.LLM.
// If baseURL is non-empty, it overrides the default API endpoint.
// When baseURL is set, the API key is optional (for Ollama compatibility).
// thinkingLevel controls extended thinking: "none", "low", "medium", "high".
// llmOpts.AdvisorModel enables the advisor tool (beta).
// See specs/features/LLM/001-claude-models/advisor-tool.md
func NewAnthropic(_ context.Context, modelName, apiKey, baseURL, thinkingLevel string, llmOpts *LLMOptions) (model.LLM, error) {
	if apiKey == "" && baseURL == "" {
		return nil, fmt.Errorf("anthropic API key is required")
	}
	var opts []anthropicopt.RequestOption
	if apiKey != "" {
		if isAnthropicOAuthToken(apiKey) {
			opts = append(opts,
				anthropicopt.WithAuthToken(apiKey),
				anthropicopt.WithHeaderDel("X-Api-Key"),
				anthropicopt.WithHeader("accept", "application/json"),
				anthropicopt.WithHeader("anthropic-beta", anthropicOAuthBetaHeader),
				anthropicopt.WithHeader("user-agent", anthropicOAuthUserAgent),
				anthropicopt.WithHeader("x-app", "cli"),
			)
		} else {
			opts = append(opts, anthropicopt.WithAPIKey(apiKey))
		}
	}
	if baseURL != "" {
		opts = append(opts, anthropicopt.WithBaseURL(baseURL))
	}
	if llmOpts != nil {
		for k, v := range llmOpts.ExtraHeaders {
			opts = append(opts, anthropicopt.WithHeader(k, v))
		}
		if transport := BuildTransport(llmOpts); transport != nil {
			opts = append(opts, anthropicopt.WithHTTPClient(&http.Client{Transport: transport}))
		}
	}
	client := anthropic.NewClient(opts...)

	// Initialize beta client for advisor tool support.
	betaClient := anthropic.NewBetaService(opts...)

	// Extract advisor settings from LLMOptions.
	var advisorModel string
	var advisorMaxUses int
	var advisorCaching bool
	if llmOpts != nil {
		advisorModel = llmOpts.AdvisorModel
		advisorMaxUses = llmOpts.AdvisorMaxUses
		advisorCaching = llmOpts.AdvisorCaching
	}

	return &anthropicModel{
		modelName:      modelName,
		client:         client,
		betaClient:     betaClient,
		thinkingLevel:  thinkingLevel,
		advisorModel:   advisorModel,
		advisorMaxUses: advisorMaxUses,
		advisorCaching: advisorCaching,
	}, nil
}

func (m *anthropicModel) Name() string { return m.modelName }

func isAnthropicOAuthToken(apiKey string) bool {
	return strings.Contains(apiKey, "sk-ant-oat")
}

func (m *anthropicModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		messages, systemPrompt := antContentsToMessages(req.Contents, req.Config)

		modelName := m.modelName
		if req.Model != "" && req.Model != "anthropic" {
			modelName = req.Model
		}
		if modelName == "" || modelName == "anthropic" {
			modelName = "claude-opus-4-7"
		}

		maxTokens := defaultMaxTokens
		thinkingCfg := antThinkingConfig(m.thinkingLevel)
		if thinkingCfg != nil {
			// Thinking requires higher max_tokens to accommodate the thinking budget.
			maxTokens = 16384
		}

		// Route to advisor-aware or standard implementation.
		if m.advisorModel != "" {
			thinkingCfgBeta := antThinkingConfigBeta(m.thinkingLevel)
			params := m.buildBetaParams(modelName, messages, systemPrompt, maxTokens, thinkingCfgBeta, req.Config)
			if stream {
				antRunStreamingBeta(ctx, &m.betaClient, params, yield)
			} else {
				antRunNonStreamingBeta(ctx, &m.betaClient, params, yield)
			}
			return
		}

		params := anthropic.MessageNewParams{
			Model:     modelName,
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

// buildBetaParams constructs BetaMessageNewParams for advisor tool support.
// See specs/features/LLM/001-claude-models/advisor-tool.md
func (m *anthropicModel) buildBetaParams(modelName string, messages []anthropic.MessageParam, systemPrompt string, maxTokens int64, thinkingCfg *anthropic.BetaThinkingConfigParamUnion, config *genai.GenerateContentConfig) anthropic.BetaMessageNewParams {
	// Convert messages to beta format.
	bMessages := make([]anthropic.BetaMessageParam, 0, len(messages))
	for _, msg := range messages {
		betaContent := make([]anthropic.BetaContentBlockParamUnion, 0, len(msg.Content))
		for _, c := range msg.Content {
			betaContent = append(betaContent, convertContentBlockToBeta(c))
		}
		bMessages = append(bMessages, anthropic.BetaMessageParam{
			Role:    convertRoleToBeta(msg.Role),
			Content: betaContent,
		})
	}

	advisorTool := anthropic.BetaAdvisorTool20260301Param{
		Model: m.advisorModel,
	}
	if m.advisorMaxUses > 0 {
		advisorTool.MaxUses = param.NewOpt(int64(m.advisorMaxUses))
	}
	if m.advisorCaching {
		advisorTool.Caching = anthropic.BetaCacheControlEphemeralParam{Type: "ephemeral"}
	}

	params := anthropic.BetaMessageNewParams{
		Model:     modelName,
		Messages:  bMessages,
		MaxTokens: maxTokens,
		Betas: []anthropic.AnthropicBeta{
			anthropic.AnthropicBetaAdvisorTool2026_03_01,
		},
		Tools: []anthropic.BetaToolUnionParam{
			{OfAdvisorTool20260301: &advisorTool},
		},
	}

	if thinkingCfg != nil {
		params.Thinking = *thinkingCfg
	}

	if systemPrompt != "" {
		params.System = []anthropic.BetaTextBlockParam{
			{Text: systemPrompt},
		}
	}

	if config != nil && len(config.Tools) > 0 {
		params.Tools = append(params.Tools, antGenaiToolsToBetaAnthropic(config.Tools)...)
	}

	return params
}

// convertRoleToBeta converts MessageParamRole to BetaMessageParamRole.
func convertRoleToBeta(role anthropic.MessageParamRole) anthropic.BetaMessageParamRole {
	switch role {
	case anthropic.MessageParamRoleAssistant:
		return anthropic.BetaMessageParamRoleAssistant
	default:
		return anthropic.BetaMessageParamRoleUser
	}
}

// convertContentBlockToBeta converts ContentBlockParamUnion to BetaContentBlockParamUnion.
func convertContentBlockToBeta(c anthropic.ContentBlockParamUnion) anthropic.BetaContentBlockParamUnion {
	if c.OfText != nil {
		return anthropic.NewBetaTextBlock(c.OfText.Text)
	}
	// For other types, return empty - they need specialized conversion
	return anthropic.NewBetaTextBlock("")
}

// antGenaiToolsToBetaAnthropic converts genai tools to Anthropic beta tool format.
func antGenaiToolsToBetaAnthropic(tools []*genai.Tool) []anthropic.BetaToolUnionParam {
	var out []anthropic.BetaToolUnionParam
	for _, t := range tools {
		if t == nil || t.FunctionDeclarations == nil {
			continue
		}
		for _, fd := range t.FunctionDeclarations {
			if fd == nil {
				continue
			}
			inputSchema := anthropic.BetaToolInputSchemaParam{
				Properties: make(map[string]any),
			}
			if m := schemaToMap(fd.ParametersJsonSchema); m != nil {
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
			tool := anthropic.BetaToolParam{
				Name:        fd.Name,
				Description: anthropic.String(fd.Description),
				InputSchema: inputSchema,
			}
			out = append(out, anthropic.BetaToolUnionParam{OfTool: &tool})
		}
	}
	return out
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
			if m := schemaToMap(fd.ParametersJsonSchema); m != nil {
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
// Uses adaptive type as "enabled" is not supported by all models.
func antThinkingConfig(level string) *anthropic.ThinkingConfigParamUnion {
	switch level {
	case "low", "medium", "high":
		return &anthropic.ThinkingConfigParamUnion{
			OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{
				Display: anthropic.ThinkingConfigAdaptiveDisplaySummarized,
			},
		}
	default:
		return nil
	}
}

// antThinkingConfigBeta maps a thinking level string to Anthropic beta thinking config.
func antThinkingConfigBeta(level string) *anthropic.BetaThinkingConfigParamUnion {
	switch level {
	case "low", "medium", "high":
		return &anthropic.BetaThinkingConfigParamUnion{
			OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{
				Display: anthropic.BetaThinkingConfigAdaptiveDisplaySummarized,
			},
		}
	default:
		return nil
	}
}

// antToolUseAcc accumulates a single Anthropic tool_use block during streaming.
type antToolUseAcc struct {
	id        string
	name      string
	inputJSON string
}

// antStreamState holds accumulated state from processing Anthropic stream events.
type antStreamState struct {
	text         string
	toolUse      map[int]antToolUseAcc
	stopReason   anthropic.StopReason
	inputTokens  int64
	outputTokens int64
}

// buildAntFinalResponse constructs the final LLMResponse from accumulated streaming state.
func buildAntFinalResponse(s *antStreamState) *model.LLMResponse {
	// Sort tool_use indices for deterministic output order (matching OpenAI path).
	indices := make([]int, 0, len(s.toolUse))
	for k := range s.toolUse {
		indices = append(indices, k)
	}
	slices.Sort(indices)

	finalParts := make([]*genai.Part, 0, 1+len(s.toolUse))
	if s.text != "" {
		finalParts = append(finalParts, &genai.Part{Text: s.text})
	}
	for _, idx := range indices {
		block := s.toolUse[idx]
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
	if s.inputTokens > 0 || s.outputTokens > 0 {
		usage = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     int32(s.inputTokens),
			CandidatesTokenCount: int32(s.outputTokens),
		}
	}
	return &model.LLMResponse{
		Partial:       false,
		TurnComplete:  true,
		FinishReason:  antStopReasonToGenai(s.stopReason),
		UsageMetadata: usage,
		Content:       &genai.Content{Role: string(genai.RoleModel), Parts: finalParts},
	}
}

func antRunStreaming(ctx context.Context, client *anthropic.Client, params anthropic.MessageNewParams, yield func(*model.LLMResponse, error) bool) {
	stream := client.Messages.NewStreaming(ctx, params)
	//nolint:errcheck // Close() may return error but we can't recover from it in defer
	defer stream.Close()

	state := &antStreamState{toolUse: make(map[int]antToolUseAcc)}

	for stream.Next() {
		event := stream.Current()

		switch e := event.AsAny().(type) {
		case anthropic.MessageStartEvent:
			state.inputTokens = e.Message.Usage.InputTokens
		case anthropic.ContentBlockStartEvent:
			idx := int(e.Index)
			if e.ContentBlock.Type == "tool_use" {
				if toolUse, ok := e.ContentBlock.AsAny().(anthropic.ToolUseBlock); ok {
					state.toolUse[idx] = antToolUseAcc{id: toolUse.ID, name: toolUse.Name}
				}
			}
		case anthropic.ContentBlockDeltaEvent:
			idx := int(e.Index)
			delta := e.Delta
			switch delta.Type {
			case "text_delta":
				if textDelta, ok := delta.AsAny().(anthropic.TextDelta); ok {
					state.text += textDelta.Text
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
					if block, exists := state.toolUse[idx]; exists {
						block.inputJSON += jsonDelta.PartialJSON
						state.toolUse[idx] = block
					}
				}
			}
		case anthropic.MessageDeltaEvent:
			state.stopReason = e.Delta.StopReason
			state.outputTokens = e.Usage.OutputTokens
		}
	}

	if err := stream.Err(); err != nil {
		if ctx.Err() == context.Canceled {
			return
		}
		_ = yield(&model.LLMResponse{ErrorCode: "STREAM_ERROR", ErrorMessage: err.Error()}, nil)
		return
	}

	_ = yield(buildAntFinalResponse(state), nil)
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

// antRunStreamingBeta handles streaming with the beta API (advisor tool support).
// See specs/features/LLM/001-claude-models/advisor-tool.md
func antRunStreamingBeta(ctx context.Context, client *anthropic.BetaService, params anthropic.BetaMessageNewParams, yield func(*model.LLMResponse, error) bool) {
	stream := client.Messages.NewStreaming(ctx, params)
	//nolint:errcheck // Close() may return error but we can't recover from it in defer
	defer stream.Close()

	state := &antStreamState{toolUse: make(map[int]antToolUseAcc)}

	for stream.Next() {
		event := stream.Current()

		switch e := event.AsAny().(type) {
		case anthropic.BetaRawMessageStartEvent:
			state.inputTokens = e.Message.Usage.InputTokens
		case anthropic.BetaRawContentBlockStartEvent:
			idx := int(e.Index)
			switch e.ContentBlock.Type {
			case "tool_use":
				if toolUse, ok := e.ContentBlock.AsAny().(anthropic.BetaToolUseBlock); ok {
					state.toolUse[idx] = antToolUseAcc{id: toolUse.ID, name: toolUse.Name}
				}
			case "advisor_tool_result":
				// Advisor result arrives fully formed in a single event.
				// Yield it as a text part so the executor sees the advice.
				if advResult, ok := e.ContentBlock.AsAny().(anthropic.BetaAdvisorToolResultBlock); ok {
					advText := extractAdvisorResultText(advResult)
					if advText != "" {
						// Yield as a special "advisor" role to distinguish from regular text.
						if !yield(&model.LLMResponse{
							Partial:      true,
							TurnComplete: false,
							Content:      &genai.Content{Role: "advisor", Parts: []*genai.Part{{Text: advText}}},
						}, nil) {
							return
						}
					}
				}
			}
		case anthropic.BetaRawContentBlockDeltaEvent:
			idx := int(e.Index)
			delta := e.Delta
			switch delta.Type {
			case "text_delta":
				textDelta := delta.AsTextDelta()
				state.text += textDelta.Text
				if !yield(&model.LLMResponse{
					Partial:      true,
					TurnComplete: false,
					Content:      &genai.Content{Role: string(genai.RoleModel), Parts: []*genai.Part{{Text: textDelta.Text}}},
				}, nil) {
					return
				}
			case "thinking_delta":
				thinkingDelta := delta.AsThinkingDelta()
				if !yield(&model.LLMResponse{
					Partial:      true,
					TurnComplete: false,
					Content:      &genai.Content{Role: "thinking", Parts: []*genai.Part{{Text: thinkingDelta.Thinking}}},
				}, nil) {
					return
				}
			case "input_json_delta":
				jsonDelta := delta.AsInputJSONDelta()
				if block, exists := state.toolUse[idx]; exists {
					block.inputJSON += jsonDelta.PartialJSON
					state.toolUse[idx] = block
				}
			}
		case anthropic.BetaRawMessageDeltaEvent:
			state.stopReason = anthropic.StopReason(e.Delta.StopReason)
			state.outputTokens = e.Usage.OutputTokens
		}
	}

	if err := stream.Err(); err != nil {
		if ctx.Err() == context.Canceled {
			return
		}
		_ = yield(&model.LLMResponse{ErrorCode: "STREAM_ERROR", ErrorMessage: err.Error()}, nil)
		return
	}

	_ = yield(buildAntFinalResponse(state), nil)
}

// antRunNonStreamingBeta handles non-streaming with the beta API (advisor tool support).
func antRunNonStreamingBeta(ctx context.Context, client *anthropic.BetaService, params anthropic.BetaMessageNewParams, yield func(*model.LLMResponse, error) bool) {
	message, err := client.Messages.New(ctx, params)
	if err != nil {
		yield(nil, fmt.Errorf("anthropic API error: %w", err))
		return
	}

	parts := make([]*genai.Part, 0, len(message.Content))
	for _, block := range message.Content {
		switch block.Type {
		case "text":
			textBlock := block.Text
			if textBlock != "" {
				parts = append(parts, &genai.Part{Text: textBlock})
			}
		case "thinking":
			thinkingBlock := block.Thinking
			if thinkingBlock != "" {
				parts = append(parts, &genai.Part{Text: thinkingBlock})
			}
		case "tool_use":
			id := block.ID
			name := block.Name
			var args map[string]any
			if block.Input != nil {
				_ = json.Unmarshal(block.Input, &args)
			}
			if name != "" || id != "" {
				p := genai.NewPartFromFunctionCall(name, args)
				p.FunctionCall.ID = id
				parts = append(parts, p)
			}
		case "advisor_tool_result":
			advText := extractBetaAdvisorResultText(block)
			if advText != "" {
				parts = append(parts, &genai.Part{Text: advText})
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
		FinishReason:  antStopReasonToGenaiBeta(message.StopReason),
		UsageMetadata: usage,
		Content:       &genai.Content{Role: string(genai.RoleModel), Parts: parts},
	}, nil)
}

// extractAdvisorResultText extracts the advisor result text from an advisor_tool_result block.
func extractAdvisorResultText(block anthropic.BetaAdvisorToolResultBlock) string {
	switch block.Content.Type {
	case "advisor_result":
		return block.Content.Text
	case "advisor_redacted_result":
		// The actual decryption happens on the server-side; we cannot read encrypted_content.
		return "[advisor result encrypted; will be decrypted on next turn]"
	}
	return ""
}

// extractBetaAdvisorResultText extracts the advisor result text from a BetaContentBlockUnion
// whose Type is "advisor_tool_result".
func extractBetaAdvisorResultText(block anthropic.BetaContentBlockUnion) string {
	advResult := block.AsAdvisorToolResult()
	return extractAdvisorResultText(advResult)
}

// antStopReasonToGenaiBeta maps an Anthropic BetaStopReason to genai.FinishReason.
func antStopReasonToGenaiBeta(reason anthropic.BetaStopReason) genai.FinishReason {
	switch reason {
	case anthropic.BetaStopReasonMaxTokens:
		return genai.FinishReasonMaxTokens
	case anthropic.BetaStopReasonEndTurn, anthropic.BetaStopReasonToolUse:
		return genai.FinishReasonStop
	default:
		return genai.FinishReasonStop
	}
}
