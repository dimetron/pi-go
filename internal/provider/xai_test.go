package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/openai/openai-go/v3/shared"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestNewXAIRequiresAPIKey(t *testing.T) {
	if _, err := NewXAI(context.Background(), "grok-4.6", "", "", "high", nil); err == nil {
		t.Fatal("expected error when API key is empty")
	}
}

func TestNewXAIDefaultBaseURL(t *testing.T) {
	m, err := NewXAI(context.Background(), "grok-4.6", "test-key", "", "high", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name() != "grok-4.6" {
		t.Errorf("Name() = %q, want %q", m.Name(), "grok-4.6")
	}
}

func TestNewXAIWithTransportOptions(t *testing.T) {
	opts := &LLMOptions{
		ExtraHeaders:    map[string]string{"X-Custom": "value"},
		InsecureSkipTLS: true,
	}
	m, err := NewXAI(context.Background(), "grok-4.6", "test-key", "", "high", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil model")
	}
}

func TestNewXAIRejectsBadCACert(t *testing.T) {
	opts := &LLMOptions{CACertPath: filepath.Join(t.TempDir(), "absent.pem")}
	if _, err := NewXAI(context.Background(), "grok-4.6", "test-key", "", "high", opts); err == nil {
		t.Fatal("expected an error for an unreadable CA certificate")
	}
}

func TestResolveGrokModels(t *testing.T) {
	for _, name := range []string{"grok-4.6", "grok-4.6-latest", "grok-4.3", "grok-build-0.1", "GROK-4.5"} {
		t.Run(name, func(t *testing.T) {
			info, err := Resolve(name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Provider != "xai" {
				t.Errorf("provider = %q, want xai", info.Provider)
			}
			if info.Ollama {
				t.Error("expected Ollama = false for xAI cloud provider")
			}
			if info.Model != name {
				t.Errorf("model = %q, want %q", info.Model, name)
			}
		})
	}
}

func TestNewLLMCreatesXAIProvider(t *testing.T) {
	llm, err := NewLLM(context.Background(), Info{Provider: "xai", Model: "grok-4.6"}, "test-key", "", "high", nil)
	if err != nil {
		t.Fatalf("NewLLM() error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewLLM() returned nil")
	}
	if llm.Name() != "grok-4.6" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "grok-4.6")
	}
}

func TestValidateGrokModels(t *testing.T) {
	if err := ValidateModel(Info{Provider: "xai", Model: "grok-4.6-latest"}); err != nil {
		t.Errorf("grok-4.6-latest should validate: %v", err)
	}
	if err := ValidateModel(Info{Provider: "xai", Model: "grok-4.20-0309-non-reasoning"}); err != nil {
		t.Errorf("grok-4.20-0309-non-reasoning should validate: %v", err)
	}
	if err := ValidateModel(Info{Provider: "xai", Model: "grok-9-imaginary"}); err == nil {
		t.Error("expected an unknown grok model to be rejected")
	}
}

func TestXAIContextWindows(t *testing.T) {
	tests := []struct {
		model string
		want  int64
	}{
		{"grok-4.6", 500_000},
		{"grok-4.6-latest", 500_000},
		{"grok-4.5", 500_000},
		{"grok-4.3", 1_000_000},
		{"grok-4.20-0309-reasoning", 1_000_000},
		{"grok-build-0.1", 256_000},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := ContextWindowSizeFor("xai", tt.model); got != tt.want {
				t.Errorf("ContextWindowSizeFor(xai, %q) = %d, want %d", tt.model, got, tt.want)
			}
		})
	}
}

func TestXAIReasoningEffort(t *testing.T) {
	tests := []struct {
		level string
		want  shared.ReasoningEffort
	}{
		// Grok's reasoning models have no off switch, so "none" lands on the
		// lowest tier the API offers rather than being omitted — omitting it
		// would leave xAI's "high" default in force.
		{"none", shared.ReasoningEffortLow},
		{"low", shared.ReasoningEffortLow},
		{"medium", shared.ReasoningEffortMedium},
		{"high", shared.ReasoningEffortHigh},
		{"max", shared.ReasoningEffortXhigh},
		{"xhigh", shared.ReasoningEffortXhigh},
		{"", ""},
		{"nonsense", ""},
	}
	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			if got := xaiReasoningEffort(tt.level); got != tt.want {
				t.Errorf("xaiReasoningEffort(%q) = %q, want %q", tt.level, got, tt.want)
			}
		})
	}
}

func TestXAIModelReasons(t *testing.T) {
	if !xaiModelReasons("grok-4.20-0309-reasoning") {
		t.Error("grok-4.20-0309-reasoning should accept reasoning_effort")
	}
	if xaiModelReasons("grok-4.20-0309-non-reasoning") {
		t.Error("grok-4.20-0309-non-reasoning should not be sent reasoning_effort")
	}
}

// xaiCaptureServer answers one chat completion and hands the caller the
// request headers and decoded body that produced it.
func xaiCaptureServer(t *testing.T, header *http.Header, body *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*header = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, body)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chat-123",
			"object": "chat.completion",
			"model":  "grok-4.6",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "Hello from Grok!"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	}))
}

func xaiDrain(t *testing.T, m model.LLM, modelName string) []*model.LLMResponse {
	t.Helper()
	req := &model.LLMRequest{
		Model:    modelName,
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}}},
	}
	var out []*model.LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		out = append(out, resp)
	}
	return out
}

func TestXAINonStreamingSendsReasoningAndConvID(t *testing.T) {
	var header http.Header
	var body map[string]any
	srv := xaiCaptureServer(t, &header, &body)
	defer srv.Close()

	m, err := NewXAI(context.Background(), "grok-4.6", "test-key", srv.URL, "medium", nil)
	if err != nil {
		t.Fatalf("NewXAI() error: %v", err)
	}

	responses := xaiDrain(t, m, "")
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}
	resp := responses[0]
	if resp.Content == nil || len(resp.Content.Parts) == 0 {
		t.Fatal("expected content with parts")
	}
	if got := resp.Content.Parts[0].Text; got != "Hello from Grok!" {
		t.Errorf("text = %q, want %q", got, "Hello from Grok!")
	}
	if !resp.TurnComplete {
		t.Error("expected TurnComplete = true")
	}

	if got := body["reasoning_effort"]; got != "medium" {
		t.Errorf("reasoning_effort = %v, want medium", got)
	}
	if got := header.Get(xaiConversationHeader); got == "" {
		t.Errorf("%s header not sent; cache affinity depends on it", xaiConversationHeader)
	}
}

func TestXAIConvIDIsStableAcrossTurns(t *testing.T) {
	var header http.Header
	var body map[string]any
	srv := xaiCaptureServer(t, &header, &body)
	defer srv.Close()

	m, err := NewXAI(context.Background(), "grok-4.6", "test-key", srv.URL, "high", nil)
	if err != nil {
		t.Fatalf("NewXAI() error: %v", err)
	}

	xaiDrain(t, m, "")
	first := header.Get(xaiConversationHeader)
	xaiDrain(t, m, "")
	second := header.Get(xaiConversationHeader)

	if first == "" || first != second {
		t.Errorf("%s = %q then %q; must stay stable across a conversation", xaiConversationHeader, first, second)
	}
}

func TestXAIOmitsReasoningForNonReasoningModel(t *testing.T) {
	var header http.Header
	var body map[string]any
	srv := xaiCaptureServer(t, &header, &body)
	defer srv.Close()

	m, err := NewXAI(context.Background(), "grok-4.20-0309-non-reasoning", "test-key", srv.URL, "high", nil)
	if err != nil {
		t.Fatalf("NewXAI() error: %v", err)
	}

	xaiDrain(t, m, "")
	if _, ok := body["reasoning_effort"]; ok {
		t.Errorf("reasoning_effort sent to a non-reasoning model: %v", body["reasoning_effort"])
	}
}

func TestXAIPerRequestModelOverrideGatesReasoning(t *testing.T) {
	var header http.Header
	var body map[string]any
	srv := xaiCaptureServer(t, &header, &body)
	defer srv.Close()

	// Constructed against a reasoning model, but the request names a
	// non-reasoning one — the gate must follow the request, not the default.
	m, err := NewXAI(context.Background(), "grok-4.6", "test-key", srv.URL, "high", nil)
	if err != nil {
		t.Fatalf("NewXAI() error: %v", err)
	}

	xaiDrain(t, m, "grok-4.20-0309-non-reasoning")
	if _, ok := body["reasoning_effort"]; ok {
		t.Errorf("reasoning_effort sent for request model override: %v", body["reasoning_effort"])
	}
	if got := body["model"]; got != "grok-4.20-0309-non-reasoning" {
		t.Errorf("model = %v, want the per-request override", got)
	}
}

func TestXAIExtraHeaderOverridesConvID(t *testing.T) {
	var header http.Header
	var body map[string]any
	srv := xaiCaptureServer(t, &header, &body)
	defer srv.Close()

	opts := &LLMOptions{ExtraHeaders: map[string]string{xaiConversationHeader: "pinned-id"}}
	m, err := NewXAI(context.Background(), "grok-4.6", "test-key", srv.URL, "high", opts)
	if err != nil {
		t.Fatalf("NewXAI() error: %v", err)
	}

	xaiDrain(t, m, "")
	if got := header.Get(xaiConversationHeader); got != "pinned-id" {
		t.Errorf("%s = %q, want the explicit override %q", xaiConversationHeader, got, "pinned-id")
	}
}

func TestXAISendsSystemInstructionAndTools(t *testing.T) {
	var header http.Header
	var body map[string]any
	srv := xaiCaptureServer(t, &header, &body)
	defer srv.Close()

	m, err := NewXAI(context.Background(), "grok-4.6", "test-key", srv.URL, "high", nil)
	if err != nil {
		t.Fatalf("NewXAI() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}}},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: "You are terse."}}},
			Tools: []*genai.Tool{{
				FunctionDeclarations: []*genai.FunctionDeclaration{{
					Name:        "read_file",
					Description: "Read a file",
				}},
			}},
		},
	}
	for _, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
	}

	messages, _ := body["messages"].([]any)
	if len(messages) == 0 {
		t.Fatal("expected messages on the wire")
	}
	first, _ := messages[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "You are terse." {
		t.Errorf("first message = %v, want the system instruction prepended", first)
	}

	tools, _ := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v, want one declaration", body["tools"])
	}
	if body["tool_choice"] != "auto" {
		t.Errorf("tool_choice = %v, want auto", body["tool_choice"])
	}
}

func TestXAIStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"id":"c1","object":"chat.completion.chunk","model":"grok-4.6","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}`,
			`{"id":"c1","object":"chat.completion.chunk","model":"grok-4.6","choices":[{"index":0,"delta":{"content":" from Grok"},"finish_reason":null}]}`,
			`{"id":"c1","object":"chat.completion.chunk","model":"grok-4.6","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11}}`,
		}
		for _, c := range chunks {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
			w.(http.Flusher).Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	m, err := NewXAI(context.Background(), "grok-4.6", "test-key", srv.URL, "high", nil)
	if err != nil {
		t.Fatalf("NewXAI() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}}},
	}
	var partials []string
	var final *model.LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		if resp.Partial {
			partials = append(partials, resp.Content.Parts[0].Text)
			continue
		}
		final = resp
	}

	if len(partials) != 2 {
		t.Errorf("partials = %v, want two deltas", partials)
	}
	if final == nil {
		t.Fatal("expected a final response")
	}
	if !final.TurnComplete {
		t.Error("expected TurnComplete = true on the final response")
	}
	if final.Content == nil || len(final.Content.Parts) == 0 {
		t.Fatal("expected content with parts")
	}
	if got := final.Content.Parts[0].Text; got != "Hello from Grok" {
		t.Errorf("accumulated text = %q, want %q", got, "Hello from Grok")
	}
	if final.UsageMetadata == nil || final.UsageMetadata.PromptTokenCount != 8 {
		t.Errorf("usage = %+v, want prompt_tokens 8", final.UsageMetadata)
	}
}

func TestListXAIModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want bearer auth", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "grok-4.6", "owned_by": "xai"}},
		})
	}))
	defer srv.Close()

	models, err := ListModels(context.Background(), "xai", ListModelsOptions{APIKey: "test-key", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("ListModels() error: %v", err)
	}
	if len(models) != 1 || models[0].ID != "grok-4.6" {
		t.Fatalf("models = %+v, want one grok-4.6 entry", models)
	}
}

// A base URL that already carries the version segment must not be extended
// again — XAI_BASE_URL is documented as https://api.x.ai/v1, and the naive
// concatenation would request /v1/v1/models.
func TestListXAIModelsWithVersionedBaseURL(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{}})
	}))
	defer srv.Close()

	if _, err := ListModels(context.Background(), "xai", ListModelsOptions{APIKey: "k", BaseURL: srv.URL + "/v1"}); err != nil {
		t.Fatalf("ListModels() error: %v", err)
	}
	if path != "/v1/models" {
		t.Errorf("path = %q, want /v1/models", path)
	}
}
