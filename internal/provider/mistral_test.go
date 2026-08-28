package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestNewMistralRequiresAPIKey(t *testing.T) {
	_, err := NewMistral(context.Background(), "mistral-large-latest", "", "", "", nil)
	if err == nil {
		t.Fatal("expected error when API key is empty")
	}
}

func TestNewMistralDefaultBaseURL(t *testing.T) {
	m, err := NewMistral(context.Background(), "mistral-large-latest", "test-key", "", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name() != "mistral-large-latest" {
		t.Errorf("Name() = %q, want %q", m.Name(), "mistral-large-latest")
	}
}

func TestNewMistralCustomBaseURL(t *testing.T) {
	m, err := NewMistral(context.Background(), "mistral-small-latest", "test-key", "https://custom.example.com/v1", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name() != "mistral-small-latest" {
		t.Errorf("Name() = %q, want %q", m.Name(), "mistral-small-latest")
	}
}

func TestNewMistralWithExtraHeaders(t *testing.T) {
	opts := &LLMOptions{
		ExtraHeaders: map[string]string{"X-Custom": "value"},
	}
	m, err := NewMistral(context.Background(), "mistral-large-latest", "test-key", "", "", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil model")
	}
}

func TestNewMistralWithInsecureTLS(t *testing.T) {
	opts := &LLMOptions{InsecureSkipTLS: true}
	m, err := NewMistral(context.Background(), "mistral-large-latest", "test-key", "", "", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil model")
	}
}

func TestResolveMistralModels(t *testing.T) {
	tests := []struct {
		model    string
		provider string
	}{
		{"mistral-large-latest", "mistral"},
		{"mistral-small-latest", "mistral"},
		{"mistral-medium-latest", "mistral"},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			info, err := Resolve(tt.model)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Provider != tt.provider {
				t.Errorf("provider = %q, want %q", info.Provider, tt.provider)
			}
			if info.Ollama {
				t.Error("expected Ollama = false for Mistral cloud provider")
			}
		})
	}
}

func TestResolveMistralViaOllamaPrefix(t *testing.T) {
	info, err := Resolve("ollama/mistral:7b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "ollama" {
		t.Errorf("provider = %q, want ollama", info.Provider)
	}
	if !info.Ollama {
		t.Error("expected Ollama = true")
	}
	if info.Model != "mistral:7b" {
		t.Errorf("model = %q, want mistral:7b", info.Model)
	}
}

func TestNewLLMCreatesMistralProvider(t *testing.T) {
	llm, err := NewLLM(context.Background(), Info{Provider: "mistral", Model: "mistral-large-latest"}, "test-key", "", "", nil)
	if err != nil {
		t.Fatalf("NewLLM() error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewLLM() returned nil")
	}
	if llm.Name() != "mistral-large-latest" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "mistral-large-latest")
	}
}

func TestMistralNonStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"id":     "chat-123",
			"object": "chat.completion",
			"model":  "mistral-large-latest",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "Hello from Mistral!"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m, err := NewMistral(context.Background(), "mistral-large-latest", "test-key", srv.URL, "", nil)
	if err != nil {
		t.Fatalf("NewMistral() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
		},
	}

	var responses []*model.LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		responses = append(responses, resp)
	}

	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}
	resp := responses[0]
	if resp.Content == nil || len(resp.Content.Parts) == 0 {
		t.Fatal("expected content with parts")
	}
	if resp.Content.Parts[0].Text != "Hello from Mistral!" {
		t.Errorf("text = %q, want %q", resp.Content.Parts[0].Text, "Hello from Mistral!")
	}
	if !resp.TurnComplete {
		t.Error("expected TurnComplete = true")
	}
}

func TestMistralStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"id":"chat-1","object":"chat.completion.chunk","model":"mistral-large-latest","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}`,
			`{"id":"chat-1","object":"chat.completion.chunk","model":"mistral-large-latest","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
			`{"id":"chat-1","object":"chat.completion.chunk","model":"mistral-large-latest","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
		}
		for _, c := range chunks {
			w.Write([]byte("data: " + c + "\n\n"))
		}
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	m, err := NewMistral(context.Background(), "mistral-large-latest", "test-key", srv.URL, "", nil)
	if err != nil {
		t.Fatalf("NewMistral() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		},
	}

	var responses []*model.LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		responses = append(responses, resp)
	}

	if len(responses) < 2 {
		t.Fatalf("expected at least 2 responses (partials + final), got %d", len(responses))
	}

	// Last response should be the final one
	last := responses[len(responses)-1]
	if !last.TurnComplete {
		t.Error("expected last response TurnComplete = true")
	}
}

func TestMistralWithToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"id":     "chat-456",
			"object": "chat.completion",
			"model":  "mistral-large-latest",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "",
					"tool_calls": []map[string]any{{
						"id":   "call_abc",
						"type": "function",
						"function": map[string]any{
							"name":      "read_file",
							"arguments": `{"path":"/tmp/test.go"}`,
						},
					}},
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]any{
				"prompt_tokens":     20,
				"completion_tokens": 10,
				"total_tokens":      30,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m, err := NewMistral(context.Background(), "mistral-large-latest", "test-key", srv.URL, "", nil)
	if err != nil {
		t.Fatalf("NewMistral() error: %v", err)
	}

	tools := []*genai.Tool{{
		FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name:        "read_file",
			Description: "Read a file",
			ParametersJsonSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
				},
			},
		}},
	}}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Read the file"}}},
		},
		Config: &genai.GenerateContentConfig{Tools: tools},
	}

	var responses []*model.LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		responses = append(responses, resp)
	}

	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}
	resp := responses[0]
	if resp.Content == nil {
		t.Fatal("expected content")
	}

	var foundToolCall bool
	for _, p := range resp.Content.Parts {
		if p.FunctionCall != nil {
			foundToolCall = true
			if p.FunctionCall.Name != "read_file" {
				t.Errorf("tool name = %q, want read_file", p.FunctionCall.Name)
			}
			if p.FunctionCall.ID != "call_abc" {
				t.Errorf("tool ID = %q, want call_abc", p.FunctionCall.ID)
			}
		}
	}
	if !foundToolCall {
		t.Error("expected a function call in response parts")
	}
}

func TestMistralFinishReasonMapping(t *testing.T) {
	tests := []struct {
		reason string
		want   genai.FinishReason
	}{
		{"stop", genai.FinishReasonStop},
		{"length", genai.FinishReasonMaxTokens},
		{"content_filter", genai.FinishReasonSafety},
		{"tool_calls", genai.FinishReasonStop},
	}
	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			got := mistralFinishReasonToGenai(tt.reason)
			if got != tt.want {
				t.Errorf("mistralFinishReasonToGenai(%q) = %v, want %v", tt.reason, got, tt.want)
			}
		})
	}
}
