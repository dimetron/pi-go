package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"net/url"
	"os"
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

// Ollama's two default endpoints. Which one a model belongs to is decided by
// its tag, never by whether a key happens to be exported.
const (
	ollamaLocalURL = "http://localhost:11434"
	ollamaCloudURL = "https://api.ollama.com"
)

// resolveOllamaEndpoint picks the server a model should be sent to.
//
// Routing follows the model's cloud tag, which is the rule the CLI help and
// every other routing check in the tree already state. It used to follow the
// presence of an API key instead, and that made OLLAMA_API_KEY a global switch:
// exporting it once for a :cloud model silently sent every *local* model to
// api.ollama.com too, where a privately pulled name like muse-glimmer:30b-mlx
// does not exist. The key is a credential, not a destination.
//
// An explicit baseURL (OLLAMA_HOST) always wins — that is how someone points at
// another machine, a container, or an authenticated proxy.
//
// Who receives the key is deliberately not decided here. It still goes to
// whatever endpoint is chosen whenever one is set, unchanged, because an
// authenticated daemon may be reached over loopback as easily as over the
// network and this function cannot tell the two apart.
func resolveOllamaEndpoint(modelName, baseURL string) string {
	if endpoint := normalizeBaseURL(baseURL); endpoint != "" {
		return endpoint
	}
	if isOllamaCloudModel(modelName) {
		return ollamaCloudURL
	}
	return ollamaLocalURL
}

// NewOllama creates an Ollama model.LLM using the native Ollama Go client.
// The server is chosen by resolveOllamaEndpoint: an explicit baseURL if given,
// otherwise api.ollama.com for a :cloud/-cloud tagged model and localhost for
// everything else.
// thinkingLevel controls extended thinking: "none", "low", "medium", "high".
func NewOllama(_ context.Context, modelName, apiKey, baseURL, thinkingLevel string, opts *LLMOptions) (model.LLM, error) {
	if modelName == "" {
		return nil, fmt.Errorf("model name is required")
	}
	baseURL = resolveOllamaEndpoint(modelName, baseURL)
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Ollama URL %q: %w", baseURL, err)
	}
	httpClient, err := BuildHTTPClient(opts, 10*time.Minute)
	if err != nil {
		return nil, err
	}
	// Inject Bearer token for the ollama.com cloud API when an API key is provided.
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

		if n := ollamaNumPredict(); n > 0 {
			if chatReq.Options == nil {
				chatReq.Options = map[string]any{}
			}
			chatReq.Options["num_predict"] = n
		}

		if sampling := ollamaSamplingOptions(); len(sampling) > 0 {
			if chatReq.Options == nil {
				chatReq.Options = map[string]any{}
			}
			for k, v := range sampling {
				chatReq.Options[k] = v
			}
		}

		// No num_ctx for cloud models. api.ollama.com already serves each model
		// at its native window, and sending a fixed value caps it instead of
		// raising it: deepseek-v4-flash:0731-cloud has 1M, so the 256K that
		// used to be set here would have thrown away three quarters of it.
		//
		// The old test also only matched ":cloud", never the ":<size>-cloud"
		// form that most of the cloud catalog uses, so it silently did nothing
		// for those models — the routing checks in config.go and provider.go
		// accept both suffixes.

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
			retryStream(ctx, streamRetryConfig(), yield, func(y func(*model.LLMResponse, error) bool) {
				ollamaRunStreaming(ctx, m.client, chatReq, y)
			})
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
// defaultOllamaNumPredict bounds the output of a single Ollama turn.
//
// Ollama defaults to no client-side limit, so a model that falls into a
// repetition loop streams until the server's own ceiling: one
// deepseek-v4-flash:0731:cloud turn emitted 148 KB of the same sentence over 87
// seconds before it stopped. The cap matches what the Anthropic path already
// allows a thinking turn (16K), which is far above a normal coding reply.
const defaultOllamaNumPredict = 16384

// ollamaNumPredict returns the per-turn output cap in tokens.
// PI_OLLAMA_NUM_PREDICT overrides the default; a value <= 0 removes the cap,
// restoring the old unbounded behavior for anyone who needs it.
func ollamaNumPredict() int {
	raw := strings.TrimSpace(os.Getenv("PI_OLLAMA_NUM_PREDICT"))
	if raw == "" {
		return defaultOllamaNumPredict
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return defaultOllamaNumPredict
	}
	return n
}

// ollamaSamplingOptions returns Ollama's repetition-control knobs, omitting any
// the operator has not set so the server's own defaults stay in force. Nothing
// is sent by default: these change generation quality for every model, and the
// value that helps one can degrade another.
//
// They exist because num_predict only bounds how far a degenerate turn runs, it
// does not stop the turn degenerating. Ollama applies repeat_penalty (default
// 1.1) across the last repeat_last_n (default 64) tokens only. Measured
// degenerate turns on deepseek-v4-flash cycle on phrases of 89-194 bytes,
// roughly 25-55 tokens, so a 64-token window may not span a full cycle and the
// penalty never sees the repetition it is meant to suppress. Widening
// PI_OLLAMA_REPEAT_LAST_N is the knob that addresses that directly.
//
//	PI_OLLAMA_REPEAT_PENALTY     float, Ollama default 1.1 (1.0 disables)
//	PI_OLLAMA_REPEAT_LAST_N      int, Ollama default 64 (0 disables, -1 = num_ctx)
//	PI_OLLAMA_PRESENCE_PENALTY   float, Ollama default 0.0
//	PI_OLLAMA_FREQUENCY_PENALTY  float, Ollama default 0.0
//
// An unparseable value is ignored rather than fatal: a typo in an env var
// should not take down a session that would otherwise run.
func ollamaSamplingOptions() map[string]any {
	opts := make(map[string]any, 4)
	if v, ok := ollamaEnvFloat("PI_OLLAMA_REPEAT_PENALTY"); ok {
		opts["repeat_penalty"] = v
	}
	if v, ok := ollamaEnvInt("PI_OLLAMA_REPEAT_LAST_N"); ok {
		opts["repeat_last_n"] = v
	}
	if v, ok := ollamaEnvFloat("PI_OLLAMA_PRESENCE_PENALTY"); ok {
		opts["presence_penalty"] = v
	}
	if v, ok := ollamaEnvFloat("PI_OLLAMA_FREQUENCY_PENALTY"); ok {
		opts["frequency_penalty"] = v
	}
	return opts
}

// ollamaEnvFloat reads a float env var, reporting whether it was set and valid.
func ollamaEnvFloat(name string) (float64, bool) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// ollamaEnvInt reads an int env var, reporting whether it was set and valid.
func ollamaEnvInt(name string) (int, bool) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, false
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return v, true
}

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
	systemPrompt := genaiSystemInstruction(config)
	functionResponses := genaiFunctionResponses(contents)

	var messages []ollamaapi.Message
	for _, content := range contents {
		if content == nil {
			continue
		}
		role := strings.TrimSpace(content.Role)
		if role == "system" {
			continue
		}
		textParts, functionCalls := genaiSplitParts(content)

		switch {
		case len(functionCalls) > 0 && genaiRoleIsAssistant(role):
			messages = append(messages, ollamaToolCallMessages(textParts, functionCalls, functionResponses)...)
		case len(textParts) > 0:
			messages = append(messages, ollamaTextMessage(role, textParts))
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

// ollamaToolCallMessages renders an assistant turn that made tool calls: the
// assistant message carrying the calls, followed by one "tool" message per
// call holding its result. A call with no matching response gets empty
// content rather than being dropped, so the call/result pairing stays intact.
// Note this differs from the OpenAI and Anthropic converters, which substitute
// placeholder text for a missing result.
func ollamaToolCallMessages(
	textParts []string,
	functionCalls []*genai.FunctionCall,
	functionResponses map[string]*genai.FunctionResponse,
) []ollamaapi.Message {
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

	messages := make([]ollamaapi.Message, 0, 1+len(functionCalls))
	messages = append(messages, msg)
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
	return messages
}

// ollamaTextMessage renders a plain text turn, mapping genai's "model" role
// onto Ollama's "assistant" and everything else onto "user".
func ollamaTextMessage(role string, textParts []string) ollamaapi.Message {
	msgRole := "user"
	if genaiRoleIsAssistant(role) {
		msgRole = "assistant"
	}
	return ollamaapi.Message{
		Role:    msgRole,
		Content: strings.Join(textParts, "\n"),
	}
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

// ollamaStreamAccumulator collects one streaming Ollama chat into the pieces
// the terminal response is built from. It owns the yield callback because the
// text and reasoning streams are forwarded as they arrive rather than at the
// end, and it owns the think splitter because inline <think> tags can span
// chunk boundaries.
type ollamaStreamAccumulator struct {
	yield    func(*model.LLMResponse, error) bool
	splitter thinkSplitter

	text         string
	thinking     string
	toolCalls    []ollamaapi.ToolCall
	doneReason   string
	promptTokens int
	evalTokens   int
}

// emit forwards one chunk under the given role, reporting an error when the
// consumer stopped consuming so that client.Chat unwinds.
func (a *ollamaStreamAccumulator) emit(role, text string) error {
	if !a.yield(&model.LLMResponse{
		Partial:      true,
		TurnComplete: false,
		Content:      &genai.Content{Role: role, Parts: []*genai.Part{{Text: text}}},
	}, nil) {
		return fmt.Errorf("yield canceled")
	}
	return nil
}

// emitThinking forwards reasoning text and accumulates it for the fallback in
// answerText. Empty text is not forwarded.
func (a *ollamaStreamAccumulator) emitThinking(text string) error {
	if text == "" {
		return nil
	}
	a.thinking += text
	return a.emit("thinking", text)
}

// emitText forwards answer text and accumulates it for the final response.
// Empty text is not forwarded.
func (a *ollamaStreamAccumulator) emitText(text string) error {
	if text == "" {
		return nil
	}
	a.text += text
	return a.emit(string(genai.RoleModel), text)
}

// emitSplit forwards a (thinking, text) pair as returned by the think
// splitter, reasoning first so it precedes the answer it explains.
func (a *ollamaStreamAccumulator) emitSplit(inlineThinking, text string) error {
	if err := a.emitThinking(inlineThinking); err != nil {
		return err
	}
	return a.emitText(text)
}

// handleChunk folds one streaming chat response into the accumulator.
// Returning an error unwinds client.Chat, which is how a consumer that
// stopped consuming cancels the request.
func (a *ollamaStreamAccumulator) handleChunk(resp ollamaapi.ChatResponse) error {
	msg := resp.Message

	// Reasoning that Ollama already separated out.
	if err := a.emitThinking(msg.Thinking); err != nil {
		return err
	}

	// Reasoning the model left inline as <think>...</think> is routed to
	// the thinking stream instead of surfacing as the answer.
	if msg.Content != "" {
		if err := a.emitSplit(a.splitter.split(msg.Content)); err != nil {
			return err
		}
	}

	if resp.Done {
		if err := a.emitSplit(a.splitter.flush()); err != nil {
			return err
		}
	}

	// Accumulate tool calls. Appending an empty slice is a no-op, so this
	// needs no length guard.
	a.toolCalls = append(a.toolCalls, msg.ToolCalls...)

	// Capture metrics from final response.
	if resp.Done {
		a.doneReason = resp.DoneReason
		a.promptTokens = resp.PromptEvalCount
		a.evalTokens = resp.EvalCount
	}

	return nil
}

// answerText picks the text of the terminal response. Normally that is the
// accumulated answer, but a model that responded entirely via thinking tokens
// (e.g. thinking forced on a nothink model) would otherwise return nothing,
// so its reasoning is surfaced as the answer instead.
//
// The fallback applies only when the turn produced no tool call. A turn that
// thinks and then calls a tool has already said something, so the fallback
// isn't needed — and firing it there restates the reasoning as if it were the
// answer (seen with minimax-m3:cloud: "The user wants me to run a bash
// command..." printed above the real reply).
func (a *ollamaStreamAccumulator) answerText() string {
	if a.text != "" {
		return a.text
	}
	if len(a.toolCalls) == 0 {
		return a.thinking
	}
	return ""
}

// finalParts assembles the parts of the terminal response: the answer text,
// if any, followed by one part per accumulated tool call.
func (a *ollamaStreamAccumulator) finalParts() []*genai.Part {
	parts := make([]*genai.Part, 0, 1+len(a.toolCalls))
	if text := a.answerText(); text != "" {
		parts = append(parts, &genai.Part{Text: text})
	}
	for _, tc := range a.toolCalls {
		args := tc.Function.Arguments.ToMap()
		p := genai.NewPartFromFunctionCall(tc.Function.Name, args)
		p.FunctionCall.ID = tc.ID
		parts = append(parts, p)
	}
	return parts
}

// usage converts the accumulated token counts into genai usage metadata,
// reporting nil when the stream carried no counts at all.
func (a *ollamaStreamAccumulator) usage() *genai.GenerateContentResponseUsageMetadata {
	if a.promptTokens <= 0 && a.evalTokens <= 0 {
		return nil
	}
	return &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:     int32(a.promptTokens),
		CandidatesTokenCount: int32(a.evalTokens),
	}
}

func ollamaRunStreaming(ctx context.Context, client *ollamaapi.Client, chatReq *ollamaapi.ChatRequest, yield func(*model.LLMResponse, error) bool) {
	acc := &ollamaStreamAccumulator{yield: yield}

	if err := client.Chat(ctx, chatReq, acc.handleChunk); err != nil {
		if ctx.Err() == context.Canceled {
			_ = yield(canceledResponse(), nil)
			return
		}
		_ = yield(&model.LLMResponse{ErrorCode: "STREAM_ERROR", ErrorMessage: err.Error()}, nil)
		return
	}

	finalParts := acc.finalParts()
	usage := acc.usage()
	_ = yield(&model.LLMResponse{
		Partial:       false,
		TurnComplete:  true,
		FinishReason:  ollamaFinishReasonToGenai(acc.doneReason),
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

	// Include thinking content as text if present. Reasoning the model left
	// inline as <think>...</think> is pulled out of the content the same way
	// the streaming path does it, so the tags never reach the caller.
	thinking := msg.Thinking
	content := msg.Content
	if content != "" {
		var splitter thinkSplitter
		inlineThinking, text := splitter.split(content)
		trailingThinking, trailingText := splitter.flush()
		thinking += inlineThinking + trailingThinking
		content = text + trailingText
	}
	if thinking != "" {
		parts = append(parts, &genai.Part{Text: thinking})
	}
	if content != "" {
		parts = append(parts, &genai.Part{Text: content})
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
