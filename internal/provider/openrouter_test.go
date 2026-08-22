package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestNewOpenRouterRequiresAPIKey(t *testing.T) {
	_, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "", "", "", nil)
	if err == nil {
		t.Fatal("expected error when API key is empty")
	}
}

func TestNewOpenRouterDefaultBaseURL(t *testing.T) {
	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", "", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name() != "google/gemini-3.7-flash" {
		t.Errorf("Name() = %q, want %q", m.Name(), "google/gemini-3.7-flash")
	}
}

func TestNewOpenRouterCustomBaseURL(t *testing.T) {
	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", "https://custom.example.com/v1", "", nil)
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
	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", "", "", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil model")
	}
}

func TestNewOpenRouterWithInsecureTLS(t *testing.T) {
	opts := &LLMOptions{InsecureSkipTLS: true}
	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", "", "", opts)
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

	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", srv.URL, "", nil)
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

	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", srv.URL, "", nil)
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

	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", srv.URL, "", nil)
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

func TestOpenRouterStreamingReasoning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reasoning arrives as delta.reasoning string chunks, then the
		// answer as delta.content — OpenRouter's documented streaming shape.
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"id":"chat-1","object":"chat.completion.chunk","model":"google/gemini-3.7-flash","choices":[{"index":0,"delta":{"reasoning":"Let me think"},"finish_reason":null}]}`,
			`{"id":"chat-1","object":"chat.completion.chunk","model":"google/gemini-3.7-flash","choices":[{"index":0,"delta":{"reasoning":" about it."},"finish_reason":null}]}`,
			`{"id":"chat-1","object":"chat.completion.chunk","model":"google/gemini-3.7-flash","choices":[{"index":0,"delta":{"content":"The answer is 42."},"finish_reason":null}]}`,
			`{"id":"chat-1","object":"chat.completion.chunk","model":"google/gemini-3.7-flash","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":9,"total_tokens":14}}`,
		}
		for _, c := range chunks {
			w.Write([]byte("data: " + c + "\n\n"))
		}
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", srv.URL, "", nil)
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

	var thinkingTexts []string
	var final *model.LLMResponse
	for _, r := range responses {
		if r.Content == nil || len(r.Content.Parts) == 0 {
			continue
		}
		if r.Content.Role == "thinking" && r.Partial {
			thinkingTexts = append(thinkingTexts, r.Content.Parts[0].Text)
			continue
		}
		if !r.Partial && r.TurnComplete {
			final = r
		}
	}

	if got := strings.Join(thinkingTexts, ""); got != "Let me think about it." {
		t.Errorf("streamed thinking = %q, want %q", got, "Let me think about it.")
	}

	if final == nil {
		t.Fatal("expected a final TurnComplete response")
	}
	if len(final.Content.Parts) != 1 || final.Content.Parts[0].Text != "The answer is 42." {
		parts := make([]string, 0, len(final.Content.Parts))
		for _, p := range final.Content.Parts {
			parts = append(parts, p.Text)
		}
		t.Errorf("final parts = %v, want [The answer is 42.]", parts)
	}
}

func TestOpenRouterStreamingReasoningDetails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Anthropic-backed models surface reasoning as typed entries in
		// delta.reasoning_details instead of a plain delta.reasoning string.
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"id":"chat-2","object":"chat.completion.chunk","model":"anthropic/claude-sonnet-4.5","choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"Step one.","format":"anthropic-claude-v1","index":0}]},"finish_reason":null}]}`,
			`{"id":"chat-2","object":"chat.completion.chunk","model":"anthropic/claude-sonnet-4.5","choices":[{"index":0,"delta":{"content":"Done."},"finish_reason":null}]}`,
			`{"id":"chat-2","object":"chat.completion.chunk","model":"anthropic/claude-sonnet-4.5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		}
		for _, c := range chunks {
			w.Write([]byte("data: " + c + "\n\n"))
		}
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	m, err := NewOpenRouter(context.Background(), "anthropic/claude-sonnet-4.5", "test-key", srv.URL, "", nil)
	if err != nil {
		t.Fatalf("NewOpenRouter() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		},
	}

	var gotThinking string
	var gotAnswer string
	sawThinkingPartial := false
	for resp, err := range m.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		if resp.Content == nil || len(resp.Content.Parts) == 0 {
			continue
		}
		text := resp.Content.Parts[0].Text
		switch {
		case resp.Content.Role == "thinking" && resp.Partial:
			sawThinkingPartial = true
			gotThinking += text
		case !resp.Partial && resp.TurnComplete:
			if text == "Step one." {
				gotThinking += "(aggregate:" + text + ")"
			} else {
				gotAnswer += text
			}
		}
	}

	if !sawThinkingPartial || gotThinking == "" {
		t.Errorf("expected streamed thinking partials, got %q (partial seen: %v)", gotThinking, sawThinkingPartial)
	}
	if !strings.Contains(gotAnswer, "Done.") {
		t.Errorf("expected answer text, got %q", gotAnswer)
	}
}

func TestOpenRouterNonStreamingReasoning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"id":     "chat-9",
			"object": "chat.completion",
			"model":  "google/gemini-3.7-flash",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":      "assistant",
					"content":   "The answer is 42.",
					"reasoning": "I computed this carefully.",
				},
				"finish_reason": "stop",
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", srv.URL, "", nil)
	if err != nil {
		t.Fatalf("NewOpenRouter() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
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
	parts := responses[0].Content.Parts
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts (reasoning + answer), got %d", len(parts))
	}
	if parts[0].Text != "I computed this carefully." {
		t.Errorf("parts[0].Text = %q, want reasoning first", parts[0].Text)
	}
	if parts[1].Text != "The answer is 42." {
		t.Errorf("parts[1].Text = %q, want the answer second", parts[1].Text)
	}
}

func TestOpenRouterReasoningEffortRequest(t *testing.T) {
	tests := []struct {
		level    string
		wantBody bool
		effort   string
	}{
		{"high", true, "high"},
		{"medium", true, "medium"},
		{"low", true, "low"},
		{"max", true, "max"},
		{"none", false, ""},
		{"", false, ""},
	}
	for _, tc := range tests {
		t.Run("level="+tc.level, func(t *testing.T) {
			var body map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &body)
				resp := map[string]any{
					"id":     "chat-e",
					"object": "chat.completion",
					"model":  "google/gemini-3.7-flash",
					"choices": []map[string]any{{
						"index":         0,
						"message":       map[string]any{"role": "assistant", "content": "ok"},
						"finish_reason": "stop",
					}},
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			}))
			defer srv.Close()

			m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", srv.URL, tc.level, nil)
			if err != nil {
				t.Fatalf("NewOpenRouter() error: %v", err)
			}
			req := &model.LLMRequest{
				Contents: []*genai.Content{
					{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
				},
			}
			for _, err := range m.GenerateContent(context.Background(), req, false) {
				if err != nil {
					t.Fatalf("GenerateContent error: %v", err)
				}
			}

			reasoning, ok := body["reasoning"].(map[string]any)
			if tc.wantBody {
				if !ok {
					t.Fatalf("expected reasoning object in request body, got %v", body["reasoning"])
				}
				if reasoning["effort"] != tc.effort {
					t.Errorf("reasoning.effort = %v, want %q", reasoning["effort"], tc.effort)
				}
			} else if ok {
				t.Errorf("did not expect a reasoning object in request body, got %v", reasoning)
			}
		})
	}
}

func TestOpenRouterDeltaThinkingRawExtraction(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "plain reasoning string",
			raw:  `{"choices":[{"delta":{"reasoning":"hmm"},"finish_reason":null}]}`,
			want: "hmm",
		},
		{
			name: "reasoning_details array",
			raw:  `{"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.text","text":"a"},{"type":"reasoning.text","text":"b"}]},"finish_reason":null}]}`,
			want: "ab",
		},
		{
			name: "summary-only details contribute no text",
			raw:  `{"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.summary","summary":"analyzed"}]},"finish_reason":null}]}`,
			want: "",
		},
		{
			name: "no choices",
			raw:  `{"object":"chat.completion.chunk"}`,
			want: "",
		},
		{
			name: "invalid json",
			raw:  `{broken`,
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := openrouterDeltaThinking(tc.raw); got != tc.want {
				t.Errorf("openrouterDeltaThinking = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOpenRouterMessageReasoningRawExtraction(t *testing.T) {
	if got := openrouterMessageReasoning(`{"choices":[{"message":{"role":"assistant","content":"x","reasoning":"why"}}]}`); got != "why" {
		t.Errorf("openrouterMessageReasoning = %q, want %q", got, "why")
	}
	if got := openrouterMessageReasoning(`{"choices":[{"message":{"role":"assistant","content":"x","reasoning_details":[{"type":"reasoning.text","text":"trace"}]}}]}`); got != "trace" {
		t.Errorf("openrouterMessageReasoning via details = %q, want %q", got, "trace")
	}
	if got := openrouterMessageReasoning(`{"choices":[{"message":{"role":"assistant","content":"x"}}]}`); got != "" {
		t.Errorf("openrouterMessageReasoning without reasoning = %q, want empty", got)
	}
	if got := openrouterMessageReasoning(`not json`); got != "" {
		t.Errorf("openrouterMessageReasoning invalid json = %q, want empty", got)
	}
}

func TestOpenRouterReasoningEffortMapping(t *testing.T) {
	tests := []struct {
		level string
		want  string
	}{
		{"", ""},
		{"none", ""},
		{"low", "low"},
		{"medium", "medium"},
		{"high", "high"},
		{"max", "max"},
	}
	for _, tc := range tests {
		if got := openrouterReasoningEffort(tc.level); got != tc.want {
			t.Errorf("openrouterReasoningEffort(%q) = %q, want %q", tc.level, got, tc.want)
		}
	}
}

func TestOpenRouterStreamingThinkingOnlyTurn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The model reasoned but never produced answer content — the final
		// response must surface the reasoning rather than return nothing.
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"id":"chat-3","object":"chat.completion.chunk","model":"google/gemini-3.7-flash","choices":[{"index":0,"delta":{"role":"assistant","reasoning":"pondering"},"finish_reason":null}]}`,
			`{"id":"chat-3","object":"chat.completion.chunk","model":"google/gemini-3.7-flash","choices":[{"index":0,"delta":{"reasoning":" deeply"},"finish_reason":null}]}`,
			`{"id":"chat-3","object":"chat.completion.chunk","model":"google/gemini-3.7-flash","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`,
		}
		for _, c := range chunks {
			w.Write([]byte("data: " + c + "\n\n"))
		}
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", srv.URL, "", nil)
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

	var final *model.LLMResponse
	thinkingPartials := 0
	for _, r := range responses {
		if r.Content != nil && r.Content.Role == "thinking" && r.Partial {
			thinkingPartials++
			continue
		}
		if !r.Partial && r.TurnComplete {
			final = r
		}
	}
	if thinkingPartials != 2 {
		t.Errorf("got %d thinking partials, want 2", thinkingPartials)
	}
	if final == nil {
		t.Fatal("expected a final TurnComplete response")
	}
	if len(final.Content.Parts) != 1 || final.Content.Parts[0].Text != "pondering deeply" {
		parts := make([]string, 0, len(final.Content.Parts))
		for _, p := range final.Content.Parts {
			parts = append(parts, p.Text)
		}
		t.Errorf("final parts = %v, want [pondering deeply]", parts)
	}
}

func TestOpenRouterStreamingThinkingWithToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reasoning followed by a tool call: the reasoning is scratch work
		// ahead of the calls and must NOT be duplicated into the final
		// response's text parts.
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"id":"chat-4","object":"chat.completion.chunk","model":"google/gemini-3.7-flash","choices":[{"index":0,"delta":{"role":"assistant","reasoning":"I should read the file"},"finish_reason":null}]}`,
			`{"id":"chat-4","object":"chat.completion.chunk","model":"google/gemini-3.7-flash","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}`,
			`{"id":"chat-4","object":"chat.completion.chunk","model":"google/gemini-3.7-flash","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"/tmp/x\"}"}}]},"finish_reason":null}]}`,
			`{"id":"chat-4","object":"chat.completion.chunk","model":"google/gemini-3.7-flash","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		}
		for _, c := range chunks {
			w.Write([]byte("data: " + c + "\n\n"))
		}
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", srv.URL, "", nil)
	if err != nil {
		t.Fatalf("NewOpenRouter() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "read /tmp/x"}}},
		},
	}

	var responses []*model.LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		responses = append(responses, resp)
	}

	var final *model.LLMResponse
	for _, r := range responses {
		if !r.Partial && r.TurnComplete {
			final = r
		}
	}
	if final == nil {
		t.Fatal("expected a final TurnComplete response")
	}
	if len(final.Content.Parts) != 1 {
		t.Fatalf("expected exactly 1 part (the function call), got %d", len(final.Content.Parts))
	}
	fc := final.Content.Parts[0].FunctionCall
	if fc == nil || fc.Name != "read_file" {
		t.Errorf("expected a read_file function call part, got %+v", final.Content.Parts[0])
	}
	if fc != nil && fc.Args["path"] != "/tmp/x" {
		t.Errorf("fc.Args[path] = %v, want /tmp/x", fc.Args["path"])
	}
}

func TestOpenRouterStreamingInterleavedOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Some providers interleave reasoning and content deltas; each must
		// land in its own stream in arrival order.
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"choices":[{"index":0,"delta":{"reasoning":"think1"}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"say1"}}]}`,
			`{"choices":[{"index":0,"delta":{"reasoning":"think2"}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"say2"}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		}
		for _, c := range chunks {
			w.Write([]byte("data: " + `{"id":"c","object":"chat.completion.chunk","model":"m",` + c[1:] + "\n\n"))
		}
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", srv.URL, "", nil)
	if err != nil {
		t.Fatalf("NewOpenRouter() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		},
	}

	var thinking strings.Builder
	var answer strings.Builder
	for resp, err := range m.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		if resp.Content == nil || len(resp.Content.Parts) == 0 || resp.Content.Parts[0].Text == "" {
			continue
		}
		if resp.Partial && resp.Content.Role == "thinking" {
			thinking.WriteString(resp.Content.Parts[0].Text)
		} else if !resp.Partial {
			for _, p := range resp.Content.Parts {
				answer.WriteString(p.Text)
			}
		}
	}

	if thinking.String() != "think1think2" {
		t.Errorf("thinking stream = %q, want %q", thinking.String(), "think1think2")
	}
	if answer.String() != "say1say2" {
		t.Errorf("answer aggregate = %q, want %q", answer.String(), "say1say2")
	}
}

func TestOpenRouterNonStreamingReasoningDetails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"id":     "chat-10",
			"object": "chat.completion",
			"model":  "anthropic/claude-sonnet-4.5",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "Done.",
					"reasoning_details": []map[string]any{
						{"type": "reasoning.text", "text": "Step one."},
						{"type": "reasoning.text", "text": "Step two."},
					},
				},
				"finish_reason": "stop",
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	m, err := NewOpenRouter(context.Background(), "anthropic/claude-sonnet-4.5", "test-key", srv.URL, "", nil)
	if err != nil {
		t.Fatalf("NewOpenRouter() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		},
	}

	for resp, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		parts := resp.Content.Parts
		if len(parts) != 2 {
			t.Fatalf("expected 2 parts (reasoning + answer), got %d", len(parts))
		}
		if parts[0].Text != "Step one.Step two." {
			t.Errorf("parts[0].Text = %q, want concatenated reasoning_details", parts[0].Text)
		}
		if parts[1].Text != "Done." {
			t.Errorf("parts[1].Text = %q, want Done.", parts[1].Text)
		}
	}
}

func TestOpenRouterReasoningEffortOnStreamRequest(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"id":"c","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	m, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", "test-key", srv.URL, "high", nil)
	if err != nil {
		t.Fatalf("NewOpenRouter() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		},
	}
	for _, err := range m.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
	}

	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("expected reasoning object in streaming request body, got %v", body["reasoning"])
	}
	if reasoning["effort"] != "high" {
		t.Errorf("streaming reasoning.effort = %v, want high", reasoning["effort"])
	}
}

func TestOAIPlainProviderIgnoresReasoningChunks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// An OpenAI-compat backend that emits delta.reasoning without the
		// extraction hook (plain openai provider): nothing may crash or leak
		// into the text stream.
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"id":"c1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","reasoning":"hidden"},"finish_reason":null}]}`,
			`{"id":"c1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"visible"},"finish_reason":null}]}`,
			`{"id":"c1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		}
		for _, c := range chunks {
			w.Write([]byte("data: " + c + "\n\n"))
		}
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	m, err := NewOpenAI(context.Background(), "gpt-4o", "test-key", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewOpenAI() error: %v", err)
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

	for i, r := range responses {
		if r.Content != nil && r.Content.Role == "thinking" {
			t.Errorf("response[%d]: unexpected thinking-role event from un-hooked provider", i)
		}
	}
	last := responses[len(responses)-1]
	if last.Content == nil || len(last.Content.Parts) != 1 || last.Content.Parts[0].Text != "visible" {
		t.Errorf("final response should carry only the visible text, got %+v", last.Content)
	}
}
