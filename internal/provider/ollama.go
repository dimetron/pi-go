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

// ResolveOllamaEndpoint picks the server a model should be sent to. It is
// exported because every caller that builds an Ollama client — the CLI, the
// TUI's /model switch, the ACP server, ping — has to reach the same answer;
// each one deciding for itself is how the key-as-destination bug survived in
// three places after this function stopped making that mistake.
//
// Routing follows the model's cloud tag, which is the rule the CLI help and
// every other routing check in the tree already state. It used to follow the
// presence of an API key instead, and that made OLLAMA_API_KEY a global switch:
// exporting it once for a :cloud model silently sent every *local* model to
// api.ollama.com too, where a privately pulled name like qwen3.8:27b-mlx does
// not exist. The key is a credential, not a destination.
//
// An explicit baseURL (OLLAMA_HOST) always wins — that is how someone points at
// another machine, a container, or an authenticated proxy.
//
// Who receives the key is deliberately not decided here. It still goes to
// whatever endpoint is chosen whenever one is set, unchanged, because an
// authenticated daemon may be reached over loopback as easily as over the
// network and this function cannot tell the two apart.
func ResolveOllamaEndpoint(modelName, baseURL string) string {
	if endpoint := normalizeBaseURL(baseURL); endpoint != "" {
		return endpoint
	}
	if IsOllamaCloudModel(modelName) {
		return ollamaCloudURL
	}
	return ollamaLocalURL
}

// IsOllamaCloudEndpoint reports whether an already-resolved endpoint is
// ollama.com's hosted API.
//
// Callers need this to tell a reachability problem from a credential problem:
// the TCP-and-GET health check in CheckOllama is a statement about a daemon on
// a host someone controls, and running it against api.ollama.com turns a
// missing OLLAMA_API_KEY into a misleading "ollama not reachable".
//
// It matches on host, not on a string compare with ollamaCloudURL, so an
// OLLAMA_HOST pointed at the cloud API by hand is recognized too.
func IsOllamaCloudEndpoint(baseURL string) bool {
	u, err := url.Parse(normalizeBaseURL(baseURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "api.ollama.com" || host == "ollama.com"
}

// NewOllama creates an Ollama model.LLM using the native Ollama Go client.
// The server is chosen by ResolveOllamaEndpoint: an explicit baseURL if given,
// otherwise api.ollama.com for a :cloud/-cloud tagged model and localhost for
// everything else.
// thinkingLevel controls extended thinking: "none", "low", "medium", "high".
func NewOllama(_ context.Context, modelName, apiKey, baseURL, thinkingLevel string, opts *LLMOptions) (model.LLM, error) {
	if modelName == "" {
		return nil, fmt.Errorf("model name is required")
	}
	baseURL = ResolveOllamaEndpoint(modelName, baseURL)
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
		chatReq := m.buildChatRequest(req)

		if !stream {
			chatReq.Stream = new(false)
			ollamaRunNonStreaming(ctx, m.client, chatReq, yield)
			return
		}

		retryStream(ctx, streamRetryConfig(), yield, func(y func(*model.LLMResponse, error) bool) {
			ollamaRunStreaming(ctx, m.client, chatReq, y)
		})
	}
}

// buildChatRequest assembles the /api/chat request for one turn.
//
// No num_ctx is set, for cloud models or any other. api.ollama.com already
// serves each model at its native window, and sending a fixed value caps it
// instead of raising it: deepseek-v4-flash:0731-cloud has 1M, so the 256K that
// used to be set here would have thrown away three quarters of it.
//
// The old test also only matched ":cloud", never the ":<size>-cloud" form that
// most of the cloud catalog uses, so it silently did nothing for those models —
// the routing checks in config.go and provider.go accept both suffixes.
func (m *ollamaModel) buildChatRequest(req *model.LLMRequest) *ollamaapi.ChatRequest {
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
		Options:  ollamaChatOptions(),
		Think:    ollamaThinkValue(modelName, m.thinkingLevel),
	}

	// Convert tools.
	if req.Config != nil && len(req.Config.Tools) > 0 {
		chatReq.Tools = ollamaGenaiToolsToOllama(req.Config.Tools)
	}

	return chatReq
}

// ollamaChatOptions collects the per-request entries of the Ollama options map,
// returning nil when none apply so the field stays absent and the server's own
// defaults hold.
func ollamaChatOptions() map[string]any {
	opts := ollamaSamplingOptions()
	if n := ollamaNumPredict(); n > 0 {
		opts["num_predict"] = n
	}
	if len(opts) == 0 {
		return nil
	}
	return opts
}

// ollamaThinkValue resolves the think field for one request. nothink models must
// not have thinking forced on, whichever level the session is running at; a nil
// result leaves the field off so the model's own default applies.
func ollamaThinkValue(modelName, thinkingLevel string) *ollamaapi.ThinkValue {
	if strings.Contains(strings.ToLower(modelName), "nothink") {
		return &ollamaapi.ThinkValue{Value: false}
	}
	return ollamaThinkingConfig(thinkingLevel)
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

		textParts, functionCalls := genaiSplitParts(content.Parts)

		switch {
		case len(functionCalls) > 0 && genaiIsAssistantRole(role):
			messages = append(messages, ollamaToolCallMessages(textParts, functionCalls, functionResponses)...)
		case len(textParts) > 0:
			messages = append(messages, ollamaTextMessage(role, strings.Join(textParts, "\n")))
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

// ollamaToolCallMessages renders one assistant turn that called tools: the
// assistant message carrying the tool calls, then one "tool" message per call
// holding its result.
func ollamaToolCallMessages(
	textParts []string,
	functionCalls []*genai.FunctionCall,
	functionResponses map[string]*genai.FunctionResponse,
) []ollamaapi.Message {
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
	messages := make([]ollamaapi.Message, 0, 1+len(functionCalls))
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
	return messages
}

// ollamaTextMessage renders a text-only turn under the Ollama role its genai
// role maps to.
func ollamaTextMessage(role, text string) ollamaapi.Message {
	msgRole := "user"
	if genaiIsAssistantRole(role) {
		msgRole = "assistant"
	}
	return ollamaapi.Message{
		Role:    msgRole,
		Content: text,
	}
}

// ollamaGenaiToolsToOllama converts genai tools to Ollama native tool format.
func ollamaGenaiToolsToOllama(tools []*genai.Tool) ollamaapi.Tools {
	var out ollamaapi.Tools
	for _, t := range tools {
		if t == nil {
			continue
		}
		for _, fd := range t.FunctionDeclarations {
			if fd == nil {
				continue
			}
			out = append(out, ollamaapi.Tool{
				Type: "function",
				Function: ollamaapi.ToolFunction{
					Name:        fd.Name,
					Description: fd.Description,
					Parameters:  ollamaToolParameters(fd.ParametersJsonSchema),
				},
			})
		}
	}
	return out
}

// ollamaToolParameters renders one declaration's JSON schema as Ollama's
// parameter object. The Properties map is always allocated, even for a
// declaration that takes no arguments, and Required is left nil unless the
// schema carries a list with at least one string in it.
func ollamaToolParameters(rawSchema any) ollamaapi.ToolFunctionParameters {
	params := ollamaapi.ToolFunctionParameters{
		Type:       "object",
		Properties: ollamaapi.NewToolPropertiesMap(),
	}
	m := schemaToMap(rawSchema)
	if m == nil {
		return params
	}
	if props, ok := m["properties"].(map[string]any); ok {
		for name, propRaw := range props {
			params.Properties.Set(name, convertToToolProperty(propRaw))
		}
	}
	params.Required = append(params.Required, jsonSchemaRequiredNames(m["required"])...)
	return params
}

// jsonSchemaRequiredNames reads the string entries of a JSON schema "required"
// list. Anything that is not a list, and any entry in it that is not a string,
// is dropped: the schema reaches here as `any` and a malformed one must not
// take down the request that carries it.
func jsonSchemaRequiredNames(raw any) []string {
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(list))
	for _, r := range list {
		if s, ok := r.(string); ok {
			names = append(names, s)
		}
	}
	return names
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

// ollamaStreamState accumulates the chunks of an Ollama chat stream while
// yielding each one as a partial response, and builds the final response from
// what it accumulated.
type ollamaStreamState struct {
	yield func(*model.LLMResponse, error) bool

	aggregatedText     string
	aggregatedThinking string
	toolCalls          []ollamaapi.ToolCall
	doneReason         string
	promptTokens       int
	evalTokens         int
	splitter           thinkSplitter
}

// emitThinking yields reasoning text on the thinking stream.
func (s *ollamaStreamState) emitThinking(text string) error {
	if text == "" {
		return nil
	}
	s.aggregatedThinking += text
	if !s.yield(&model.LLMResponse{
		Partial:      true,
		TurnComplete: false,
		Content:      &genai.Content{Role: "thinking", Parts: []*genai.Part{{Text: text}}},
	}, nil) {
		return fmt.Errorf("yield canceled")
	}
	return nil
}

// emitText yields answer text on the model stream.
func (s *ollamaStreamState) emitText(text string) error {
	if text == "" {
		return nil
	}
	s.aggregatedText += text
	if !s.yield(&model.LLMResponse{
		Partial:      true,
		TurnComplete: false,
		Content:      &genai.Content{Role: string(genai.RoleModel), Parts: []*genai.Part{{Text: text}}},
	}, nil) {
		return fmt.Errorf("yield canceled")
	}
	return nil
}

// emitSplit yields one thinkSplitter result: its reasoning half, then its
// answer half.
func (s *ollamaStreamState) emitSplit(thinking, text string) error {
	if err := s.emitThinking(thinking); err != nil {
		return err
	}
	return s.emitText(text)
}

// handleChunk folds one streamed chat response into the state.
func (s *ollamaStreamState) handleChunk(resp ollamaapi.ChatResponse) error {
	msg := resp.Message

	// Reasoning that Ollama already separated out.
	if err := s.emitThinking(msg.Thinking); err != nil {
		return err
	}

	// Reasoning the model left inline as <think>...</think> is routed to
	// the thinking stream instead of surfacing as the answer.
	if msg.Content != "" {
		inlineThinking, text := s.splitter.split(msg.Content)
		if err := s.emitSplit(inlineThinking, text); err != nil {
			return err
		}
	}

	if resp.Done {
		inlineThinking, text := s.splitter.flush()
		if err := s.emitSplit(inlineThinking, text); err != nil {
			return err
		}
	}

	// Accumulate tool calls.
	if len(msg.ToolCalls) > 0 {
		s.toolCalls = append(s.toolCalls, msg.ToolCalls...)
	}

	// Capture metrics from final response.
	if resp.Done {
		s.doneReason = resp.DoneReason
		s.promptTokens = resp.PromptEvalCount
		s.evalTokens = resp.EvalCount
	}

	return nil
}

// finalParts assembles the parts of the completed turn: the answer text (or the
// thinking fallback), then one part per tool call.
func (s *ollamaStreamState) finalParts() []*genai.Part {
	finalParts := make([]*genai.Part, 0, 1+len(s.toolCalls))
	if s.aggregatedText != "" {
		finalParts = append(finalParts, &genai.Part{Text: s.aggregatedText})
	} else if s.aggregatedThinking != "" && len(s.toolCalls) == 0 {
		// Fallback: model responded entirely via thinking tokens (e.g. thinking forced
		// on a nothink model). Surface the thinking content rather than returning nothing.
		//
		// Only when the turn produced no tool call. A turn that thinks and then
		// calls a tool has already said something, so the fallback isn't needed
		// — and firing it there restates the reasoning as if it were the answer
		// (seen with minimax-m3:cloud: "The user wants me to run a bash
		// command..." printed above the real reply).
		finalParts = append(finalParts, &genai.Part{Text: s.aggregatedThinking})
	}
	for _, tc := range s.toolCalls {
		args := tc.Function.Arguments.ToMap()
		p := genai.NewPartFromFunctionCall(tc.Function.Name, args)
		p.FunctionCall.ID = tc.ID
		finalParts = append(finalParts, p)
	}
	return finalParts
}

// finalResponse builds the terminal response for the completed turn.
func (s *ollamaStreamState) finalResponse() *model.LLMResponse {
	finalParts := s.finalParts()

	var usage *genai.GenerateContentResponseUsageMetadata
	if s.promptTokens > 0 || s.evalTokens > 0 {
		usage = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     int32(s.promptTokens),
			CandidatesTokenCount: int32(s.evalTokens),
		}
	}
	return &model.LLMResponse{
		Partial:       false,
		TurnComplete:  true,
		FinishReason:  ollamaFinishReasonToGenai(s.doneReason),
		UsageMetadata: usage,
		Content:       &genai.Content{Role: string(genai.RoleModel), Parts: finalParts},
	}
}

func ollamaRunStreaming(ctx context.Context, client *ollamaapi.Client, chatReq *ollamaapi.ChatRequest, yield func(*model.LLMResponse, error) bool) {
	state := &ollamaStreamState{yield: yield}

	if err := client.Chat(ctx, chatReq, state.handleChunk); err != nil {
		if ctx.Err() == context.Canceled {
			_ = yield(canceledResponse(), nil)
			return
		}
		_ = yield(&model.LLMResponse{ErrorCode: "STREAM_ERROR", ErrorMessage: err.Error()}, nil)
		return
	}

	_ = yield(state.finalResponse(), nil)
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

	// num_ctx takes precedence as it reflects the configured context window.
	if n, ok := ollamaNumCtxParameter(resp.Parameters); ok {
		return n
	}

	// Fall back to the model's native context length from ModelInfo.
	return ollamaNativeContextLength(resp.ModelInfo)
}

// ollamaNumCtxParameter reads num_ctx out of a /api/show parameters block, which
// is a newline-separated list of "key value" pairs. The second return says
// whether a value was found at all, so a configured num_ctx of 0 is reported as
// itself rather than falling through to the native context length.
func ollamaNumCtxParameter(parameters string) (int64, bool) {
	for _, line := range strings.Split(parameters, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "num_ctx" {
			continue
		}
		if n, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

// ollamaNativeContextLength reads the model's native context length out of a
// /api/show model_info map, returning 0 when no key carries one. A value that
// arrived over HTTP is always a float64; the integer cases are here for a map
// built in process.
func ollamaNativeContextLength(modelInfo map[string]any) int64 {
	for key, val := range modelInfo {
		if !strings.HasSuffix(key, ".context_length") {
			continue
		}
		switch v := val.(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		case int:
			return int64(v)
		}
	}
	return 0
}
