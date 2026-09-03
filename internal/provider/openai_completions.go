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
			signatures := genaiThoughtSignatures(content.Parts)
			messages = append(messages, oaiToolCallMessages(textParts, functionCalls, functionResponses, signatures)...)
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
	signatures map[string][]byte,
) []openai.ChatCompletionMessageParamUnion {
	toolCalls := make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(functionCalls))
	var toolResponseMessages []openai.ChatCompletionMessageParamUnion
	for _, fc := range functionCalls {
		if fc == nil || strings.TrimSpace(fc.ID) == "" {
			continue
		}
		argsJSON, _ := json.Marshal(fc.Args)
		call := &openai.ChatCompletionMessageFunctionToolCallParam{
			ID:   fc.ID,
			Type: constant.Function("function"),
			Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
				Name:      fc.Name,
				Arguments: string(argsJSON),
			},
		}
		// Gemini 3 rejects a replayed call whose thought signature was
		// dropped; every other provider never produced one, so the field
		// is only ever set when the model itself sent it.
		if extra := oaiThoughtSignatureExtraContent(signatures[fc.ID]); extra != nil {
			call.SetExtraFields(extra)
		}
		toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallUnionParam{OfFunction: call})
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
	thinking         string
	toolCalls        map[int64]map[string]any
	finishReason     string
	promptTokens     int64
	completionTokens int64
	cachedTokens     int64

	// Slot bookkeeping for streams that omit the tool call index; see
	// toolCallSlot.
	toolCallSlots    map[string]int64
	nextToolCallSlot int64
	lastToolCallSlot int64
	haveToolCallSlot bool
}

// toolCallSlot resolves the accumulator key for one streamed tool call delta.
//
// OpenAI streams a tool call as fragments that all carry the same "index", and
// that index is what separates one call from the next in a parallel batch.
// Google's OpenAI-compatible endpoint — the one agentgateway puts in front of
// Gemini — omits the field entirely and instead sends each call whole, in its
// own chunk:
//
//	{"delta":{"tool_calls":[{"id":"call_1","function":{"name":"bash",
//	  "arguments":"{\"command\":\"ls\"}"}}]},"index":0}
//
// The "index" there belongs to the choice, not to the tool call. Reading the
// absent field as its zero value collapses every call in a parallel batch onto
// slot 0, where accumulateOaiToolCall concatenates their arguments into
// "{...}{...}" — invalid JSON, which then unmarshals to a nil argument map and
// reaches the tool as a call with no arguments at all ("command is required").
//
// So: trust the index when the wire actually sent one, and otherwise give each
// distinct tool call ID its own slot. A delta with neither index nor ID is
// treated as a continuation of the call before it, which is the best reading
// available and matches the fragment-stream shape.
func (s *oaiStreamState) toolCallSlot(indexPresent bool, index int64, id string) int64 {
	claim := func(slot int64) int64 {
		if slot >= s.nextToolCallSlot {
			s.nextToolCallSlot = slot + 1
		}
		s.lastToolCallSlot, s.haveToolCallSlot = slot, true
		return slot
	}
	if indexPresent {
		return claim(index)
	}
	if id == "" {
		if s.haveToolCallSlot {
			return s.lastToolCallSlot
		}
		return claim(s.nextToolCallSlot)
	}
	if slot, ok := s.toolCallSlots[id]; ok {
		return claim(slot)
	}
	if s.toolCallSlots == nil {
		s.toolCallSlots = make(map[string]int64)
	}
	slot := s.nextToolCallSlot
	s.toolCallSlots[id] = slot
	return claim(slot)
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

// accumulateOaiToolCallSignature records a Gemini thought signature for a tool
// call being streamed. It is separate from accumulateOaiToolCall because the
// signature is not part of the OpenAI delta shape and has to be read out of
// the chunk's raw JSON, and because it arrives whole rather than in fragments.
func accumulateOaiToolCallSignature(acc map[int64]map[string]any, idx int64, sig []byte) {
	if len(sig) == 0 {
		return
	}
	if acc[idx] == nil {
		acc[idx] = map[string]any{"id": "", "name": "", "arguments": ""}
	}
	acc[idx]["thought_signature"] = sig
}

// buildOaiFinalResponse constructs the final LLMResponse from accumulated streaming state.
func buildOaiFinalResponse(s *oaiStreamState) *model.LLMResponse {
	indices := make([]int64, 0, len(s.toolCalls))
	for k := range s.toolCalls {
		indices = append(indices, k)
	}
	slices.Sort(indices)

	finalParts := make([]*genai.Part, 0, 2+len(s.toolCalls))
	if s.text != "" {
		finalParts = append(finalParts, &genai.Part{Text: s.text})
	} else if s.thinking != "" && len(s.toolCalls) == 0 {
		// The model spent the whole turn reasoning and never answered
		// (e.g. reasoning forced on a non-reasoning model). Surface the
		// reasoning as the turn's content rather than returning nothing —
		// the same fallback the Ollama provider applies to thinking-only
		// turns. Skipped when tool calls follow, where the reasoning is
		// scratch work ahead of the calls.
		finalParts = append(finalParts, &genai.Part{Text: s.thinking})
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
			if sig, ok := tc["thought_signature"].([]byte); ok {
				p.ThoughtSignature = sig
			}
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
	oaiRunStreamingExtract(ctx, client, params, yield, nil)
}

// oaiRunStreamingExtract is oaiRunStreaming with an optional hook that pulls
// reasoning text out of each chunk's raw JSON. The openai-go SDK has no field
// for OpenRouter's delta.reasoning / delta.reasoning_details, so providers
// backed by OpenRouter pass openrouterDeltaThinking here; nil skips the
// extraction. Reasoning streams as "thinking"-role partials (the same shape
// the Anthropic and Ollama providers emit, which the TUI renders with 💭);
// it is re-sent as turn content only when the model produced nothing else.
func oaiRunStreamingExtract(ctx context.Context, client *openai.Client, params openai.ChatCompletionNewParams, yield func(*model.LLMResponse, error) bool, extractThinking func(rawChunk string) string) {
	oaiRunStreamingHooks(ctx, client, params, yield, oaiExtractHooks{deltaThinking: extractThinking})
}

// oaiExtractHooks collects the escape hatches a provider needs when it puts
// data where the openai-go SDK has no field for it. Every hook is optional.
type oaiExtractHooks struct {
	// deltaThinking pulls reasoning text out of one streaming chunk's raw JSON.
	deltaThinking func(rawChunk string) string
	// messageThinking pulls reasoning text out of a non-streaming completion's
	// raw JSON.
	messageThinking func(rawResponse string) string
	// answerText rewrites the content string the SDK decoded before it is
	// treated as answer text. Mistral reasoning models send content as a JSON
	// array, and the SDK's decoder leaves that array's raw text in the string
	// field, so without this hook the transcript would show raw JSON. nil
	// means the decoded string is already the answer.
	answerText func(content string) string
}

// answer applies the answerText hook, or passes the content through when the
// provider did not install one.
func (h oaiExtractHooks) answer(content string) string {
	if h.answerText == nil {
		return content
	}
	return h.answerText(content)
}

// oaiRunStreamingHooks is oaiRunStreaming with the full set of provider hooks
// (see oaiExtractHooks). Reasoning streams as "thinking"-role partials (the
// same shape the Anthropic and Ollama providers emit, which the TUI renders
// with 💭); it is re-sent as turn content only when the model produced nothing
// else.
func oaiRunStreamingHooks(ctx context.Context, client *openai.Client, params openai.ChatCompletionNewParams, yield func(*model.LLMResponse, error) bool, hooks oaiExtractHooks) {
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
		if hooks.deltaThinking != nil && len(chunk.Choices) > 0 {
			if think := hooks.deltaThinking(chunk.RawJSON()); think != "" {
				state.thinking += think
				if !yield(&model.LLMResponse{
					Partial:      true,
					TurnComplete: false,
					Content:      &genai.Content{Role: "thinking", Parts: []*genai.Part{{Text: think}}},
				}, nil) {
					return
				}
			}
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		delta := choice.Delta
		if text := hooks.answer(delta.Content); text != "" {
			state.text += text
			if !yield(&model.LLMResponse{
				Partial:      true,
				TurnComplete: false,
				Content:      &genai.Content{Role: string(genai.RoleModel), Parts: []*genai.Part{{Text: text}}},
			}, nil) {
				return
			}
		}
		for _, tc := range delta.ToolCalls {
			slot := state.toolCallSlot(tc.JSON.Index.Valid(), tc.Index, tc.ID)
			accumulateOaiToolCall(state.toolCalls, slot, tc.ID, tc.Function.Name, tc.Function.Arguments)
			accumulateOaiToolCallSignature(state.toolCalls, slot, oaiThoughtSignature(tc.RawJSON()))
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
	oaiRunNonStreamingExtract(ctx, client, params, yield, nil)
}

// oaiRunNonStreamingExtract is oaiRunNonStreaming with an optional hook that
// pulls reasoning text out of the completion's raw JSON (see
// oaiRunStreamingExtract). The reasoning is prepended to the response parts.
func oaiRunNonStreamingExtract(ctx context.Context, client *openai.Client, params openai.ChatCompletionNewParams, yield func(*model.LLMResponse, error) bool, extractThinking func(rawResponse string) string) {
	oaiRunNonStreamingHooks(ctx, client, params, yield, oaiExtractHooks{messageThinking: extractThinking})
}

// oaiRunNonStreamingHooks is oaiRunNonStreaming with the full set of provider
// hooks (see oaiExtractHooks). The reasoning is prepended to the response parts.
func oaiRunNonStreamingHooks(ctx context.Context, client *openai.Client, params openai.ChatCompletionNewParams, yield func(*model.LLMResponse, error) bool, hooks oaiExtractHooks) {
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
	parts := make([]*genai.Part, 0, 2+len(msg.ToolCalls))
	if hooks.messageThinking != nil {
		if thinking := hooks.messageThinking(completion.RawJSON()); thinking != "" {
			parts = append(parts, &genai.Part{Text: thinking})
		}
	}
	if text := hooks.answer(msg.Content); text != "" {
		parts = append(parts, &genai.Part{Text: text})
	}
	for _, tc := range msg.ToolCalls {
		if tc.Type == "function" && tc.Function.Name != "" {
			var args map[string]any
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			}
			p := genai.NewPartFromFunctionCall(tc.Function.Name, args)
			p.FunctionCall.ID = tc.ID
			p.ThoughtSignature = oaiThoughtSignature(tc.RawJSON())
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
