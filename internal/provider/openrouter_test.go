package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestNewOpenRouterRequiresAPIKey(t *testing.T) {
	_, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "", "", nil)
	if err == nil {
		t.Fatal("expected error when API key is empty")
	}
}

func TestNewOpenRouterDefaultBaseURL(t *testing.T) {
	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name() != "google/gemini-3.7-flash" {
		t.Errorf("Name() = %q, want %q", m.Name(), "google/gemini-3.7-flash")
	}
}

func TestNewOpenRouterCustomBaseURL(t *testing.T) {
	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", "https://custom.example.com/v1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name() != "google/gemini-3.7-flash" {
		t.Errorf("Name() = %q, want %q", m.Name(), "google/gemini-3.7-flash")
	}
}

func TestNewOpenRouterWithExtraHeaders(t *testing.T) {
	opts := &LLMOptions{
		ExtraHeaders: map[string]string{"X-Custom": "value"},
	}
	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", "", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil model")
	}
}

func TestNewOpenRouterWithInsecureTLS(t *testing.T) {
	opts := &LLMOptions{InsecureSkipTLS: true}
	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", "", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil model")
	}
}

func TestOpenRouterNonStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"id":     "chat-123",
			"object": "chat.completion",
			"model":  "google/gemini-3.7-flash",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "Hello from OpenRouter!"},
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

	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewOpenRouter() error: %v", err)
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
	if resp.Content.Parts[0].Text != "Hello from OpenRouter!" {
		t.Errorf("text = %q, want %q", resp.Content.Parts[0].Text, "Hello from OpenRouter!")
	}
	if !resp.TurnComplete {
		t.Error("expected TurnComplete = true")
	}
}

func TestOpenRouterStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"id":"chat-1","object":"chat.completion.chunk","model":"google/gemini-3.7-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}`,
			`{"id":"chat-1","object":"chat.completion.chunk","model":"google/gemini-3.7-flash","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
			`{"id":"chat-1","object":"chat.completion.chunk","model":"google/gemini-3.7-flash","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
		}
		for _, c := range chunks {
			w.Write([]byte("data: " + c + "\n\n"))
		}
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewOpenRouter() error: %v", err)
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

func TestOpenRouterWithToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"id":     "chat-456",
			"object": "chat.completion",
			"model":  "google/gemini-3.7-flash",
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

	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewOpenRouter() error: %v", err)
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

func TestOpenRouterFinishReasonMapping(t *testing.T) {
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
			got := openrouterFinishReasonToGenai(tt.reason)
			if got != tt.want {
				t.Errorf("openrouterFinishReasonToGenai(%q) = %v, want %v", tt.reason, got, tt.want)
			}
		})
	}
}

// openrouterResetWindowCache empties the shared /models cache between tests so
// each one starts with a cold lookup.
func openrouterResetWindowCache() {
	openrouterWindowCacheMu.Lock()
	openrouterWindowCache = map[string]openrouterWindowEntry{}
	openrouterWindowCacheMu.Unlock()
}

func TestOpenRouterContextWindowSize(t *testing.T) {
	tests := []struct {
		name  string
		json  string
		model string
		want  int64
	}{
		{
			name:  "top_provider context length wins",
			json:  `{"data":[{"id":"a/model","context_length":1000000,"top_provider":{"context_length":200000}}]}`,
			model: "a/model",
			want:  200000,
		},
		{
			name:  "falls back to model-level context length",
			json:  `{"data":[{"id":"a/model","context_length":262144}]}`,
			model: "a/model",
			want:  262144,
		},
		{
			name:  "lookup is case-insensitive",
			json:  `{"data":[{"id":"A/Model","context_length":4096}]}`,
			model: "A/Model",
			want:  4096,
		},
		{
			name:  "zero or negative lengths are not answers",
			json:  `{"data":[{"id":"a/model","context_length":0,"top_provider":{"context_length":-5}}]}`,
			model: "a/model",
			want:  0,
		},
		{
			name:  "unknown model yields zero",
			json:  `{"data":[{"id":"other/model","context_length":8192}]}`,
			model: "a/model",
			want:  0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			openrouterResetWindowCache()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.json))
			}))
			t.Cleanup(srv.Close)

			if got := OpenRouterContextWindowSize(context.Background(), srv.URL, tc.model); got != tc.want {
				t.Errorf("OpenRouterContextWindowSize = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestOpenRouterContextWindowSizeFailures(t *testing.T) {
	t.Run("empty model name", func(t *testing.T) {
		if got := OpenRouterContextWindowSize(context.Background(), "http://example.invalid", ""); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})
	t.Run("openrouter/auto has no window of its own", func(t *testing.T) {
		if got := OpenRouterContextWindowSize(context.Background(), "http://example.invalid", "auto"); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})
	t.Run("unparseable base URL", func(t *testing.T) {
		openrouterResetWindowCache()
		if got := OpenRouterContextWindowSize(context.Background(), "http://[::1", "m"); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})
	t.Run("server error", func(t *testing.T) {
		openrouterResetWindowCache()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		t.Cleanup(srv.Close)
		if got := OpenRouterContextWindowSize(context.Background(), srv.URL, "m"); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})
	t.Run("malformed JSON body", func(t *testing.T) {
		openrouterResetWindowCache()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html>not json</html>"))
		}))
		t.Cleanup(srv.Close)
		if got := OpenRouterContextWindowSize(context.Background(), srv.URL, "m"); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})
	t.Run("repeat lookups are served from the cache", func(t *testing.T) {
		openrouterResetWindowCache()
		var fetches int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&fetches, 1)
			_, _ = w.Write([]byte(`{"data":[{"id":"m","context_length":1234}]}`))
		}))
		t.Cleanup(srv.Close)

		for range 3 {
			if got := OpenRouterContextWindowSize(context.Background(), srv.URL, "m"); got != 1234 {
				t.Fatalf("OpenRouterContextWindowSize = %d, want 1234", got)
			}
		}
		if n := atomic.LoadInt32(&fetches); n != 1 {
			t.Errorf("server handled %d requests, want 1 (cache miss only)", n)
		}
	})
}
