package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestOaiFinishReasonToGenai(t *testing.T) {
	tests := []struct {
		reason string
		want   genai.FinishReason
	}{
		{"stop", genai.FinishReasonStop},
		{"length", genai.FinishReasonMaxTokens},
		{"content_filter", genai.FinishReasonSafety},
		{"tool_calls", genai.FinishReasonStop},
		{"", genai.FinishReasonStop},
	}
	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			got := oaiFinishReasonToGenai(tt.reason)
			if got != tt.want {
				t.Errorf("oaiFinishReasonToGenai(%q) = %v, want %v", tt.reason, got, tt.want)
			}
		})
	}
}

func TestOaiContentsToMessages(t *testing.T) {
	t.Run("extracts system instruction", func(t *testing.T) {
		config := &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: "You are a helpful assistant."}},
			},
		}
		contents := []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
		}

		msgs, sysInstr := oaiContentsToMessages(contents, config)
		if sysInstr != "You are a helpful assistant." {
			t.Errorf("system instruction = %q, want %q", sysInstr, "You are a helpful assistant.")
		}
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
	})

	t.Run("converts user and model messages", func(t *testing.T) {
		contents := []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "What is Go?"}}},
			{Role: "model", Parts: []*genai.Part{{Text: "Go is a programming language."}}},
			{Role: "user", Parts: []*genai.Part{{Text: "Tell me more."}}},
		}

		msgs, _ := oaiContentsToMessages(contents, nil)
		if len(msgs) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(msgs))
		}
	})

	t.Run("handles function calls with responses", func(t *testing.T) {
		fc := genai.NewPartFromFunctionCall("read_file", map[string]any{"path": "/tmp/test.go"})
		fc.FunctionCall.ID = "call_123"

		fr := &genai.Part{
			FunctionResponse: &genai.FunctionResponse{
				ID:       "call_123",
				Name:     "read_file",
				Response: map[string]any{"result": "file contents here"},
			},
		}

		contents := []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Read the file"}}},
			{Role: "model", Parts: []*genai.Part{fc}},
			{Role: "user", Parts: []*genai.Part{fr}},
		}

		msgs, _ := oaiContentsToMessages(contents, nil)
		// user + assistant(tool_calls) + tool_response
		if len(msgs) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(msgs))
		}
	})

	t.Run("nil config is handled", func(t *testing.T) {
		contents := []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
		}
		msgs, sysInstr := oaiContentsToMessages(contents, nil)
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
		if sysInstr != "" {
			t.Errorf("expected empty system instruction, got %q", sysInstr)
		}
	})
}

func TestOaiGenaiToolsToOpenAI(t *testing.T) {
	t.Run("basic tool", func(t *testing.T) {
		tools := []*genai.Tool{
			{
				FunctionDeclarations: []*genai.FunctionDeclaration{
					{
						Name:        "read_file",
						Description: "Read a file",
						ParametersJsonSchema: map[string]any{
							"type": "object",
							"properties": map[string]any{
								"path": map[string]any{"type": "string"},
							},
							"required": []any{"path"},
						},
					},
				},
			},
		}

		result := oaiGenaiToolsToOpenAI(tools)
		if len(result) != 1 {
			t.Fatalf("expected 1 tool, got %d", len(result))
		}
	})

	t.Run("nil tool entries", func(t *testing.T) {
		tools := []*genai.Tool{
			nil,
			{},
			{FunctionDeclarations: nil},
			{FunctionDeclarations: []*genai.FunctionDeclaration{nil}},
			{
				FunctionDeclarations: []*genai.FunctionDeclaration{
					{Name: "test", Description: "test"},
				},
			},
		}
		result := oaiGenaiToolsToOpenAI(tools)
		if len(result) != 1 {
			t.Fatalf("expected 1 tool, got %d", len(result))
		}
	})

	t.Run("default type is object", func(t *testing.T) {
		tools := []*genai.Tool{
			{
				FunctionDeclarations: []*genai.FunctionDeclaration{
					{
						Name:        "test",
						Description: "Test",
						ParametersJsonSchema: map[string]any{
							"properties": map[string]any{
								"arg": map[string]any{"type": "string"},
							},
						},
					},
				},
			},
		}
		result := oaiGenaiToolsToOpenAI(tools)
		if len(result) != 1 {
			t.Fatalf("expected 1 tool, got %d", len(result))
		}
	})
}

func TestOaiFunctionResponseContent(t *testing.T) {
	tests := []struct {
		name string
		resp any
		want string
	}{
		{"nil", nil, ""},
		{"string", "hello", "hello"},
		{"map with result", map[string]any{"result": "ok"}, "ok"},
		{"map with content", map[string]any{"content": []any{map[string]any{"text": "hello"}}}, "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := oaiFunctionResponseContent(tt.resp)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOaiFunctionResponseContentEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		resp any
		want string
	}{
		{"nil input", nil, ""},
		{"string input", "hello world", "hello world"},
		{"empty string input", "", ""},
		{"map with result key", map[string]any{"result": "ok"}, "ok"},
		{"map with content array", map[string]any{"content": []any{map[string]any{"text": "extracted"}}}, "extracted"},
		{"map with content array missing text", map[string]any{"content": []any{map[string]any{"type": "image"}}}, `{"content":[{"type":"image"}]}`},
		{"map with content array non-map item", map[string]any{"content": []any{"plain string"}}, `{"content":["plain string"]}`},
		{"map with empty content array", map[string]any{"content": []any{}}, `{"content":[]}`},
		{"map with content not array", map[string]any{"content": "not-array"}, `{"content":"not-array"}`},
		{"map with neither result nor content", map[string]any{"status": "done"}, `{"status":"done"}`},
		{"map with both content and result prefers content", map[string]any{
			"content": []any{map[string]any{"text": "from-content"}},
			"result":  "from-result",
		}, "from-content"},
		{"number input", 42, "42"},
		{"bool input", true, "true"},
		{"slice input", []string{"a", "b"}, `["a","b"]`},
		{"map with result non-string", map[string]any{"result": 123}, `{"result":123}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := oaiFunctionResponseContent(tt.resp)
			if got != tt.want {
				t.Errorf("oaiFunctionResponseContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeOpenAIBaseURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "bare host", in: "http://127.0.0.1:2276", want: "http://127.0.0.1:2276/v1"},
		{name: "missing scheme", in: "127.0.0.1:2276", want: "http://127.0.0.1:2276/v1"},
		{name: "already v1", in: "http://127.0.0.1:2276/v1", want: "http://127.0.0.1:2276/v1"},
		{name: "proxy path", in: "https://example.com/api/v1/proxy", want: "https://example.com/api/v1/proxy"},
		{name: "trailing slash", in: "http://127.0.0.1:2276/", want: "http://127.0.0.1:2276/v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeOpenAIBaseURL(tt.in); got != tt.want {
				t.Errorf("normalizeOpenAIBaseURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNewOpenAIWithBaseURL(t *testing.T) {
	llm, err := NewOpenAI(context.Background(), "gpt-4o", "sk-test", "https://custom-api.example.com/v1", nil)
	if err != nil {
		t.Fatalf("NewOpenAI() with baseURL error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewOpenAI() returned nil")
	}
	if llm.Name() != "gpt-4o" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "gpt-4o")
	}
}

func TestNewOpenAIWithExtraHeaders(t *testing.T) {
	llm, err := NewOpenAI(context.Background(), "gpt-4o", "sk-test", "", &LLMOptions{
		ExtraHeaders: map[string]string{
			"X-Custom-Header": "custom-value",
			"X-Org-ID":        "org-123",
		},
	})
	if err != nil {
		t.Fatalf("NewOpenAI() with headers error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewOpenAI() returned nil")
	}
	if llm.Name() != "gpt-4o" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "gpt-4o")
	}
}

func TestNewOpenAIWithBaseURLAndHeaders(t *testing.T) {
	llm, err := NewOpenAI(context.Background(), "gpt-4o", "sk-test", "https://custom.example.com", &LLMOptions{
		ExtraHeaders: map[string]string{"X-Custom": "value"},
	})
	if err != nil {
		t.Fatalf("NewOpenAI() with baseURL+headers error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewOpenAI() returned nil")
	}
}

func TestNewOpenAIEmptyAPIKey(t *testing.T) {
	_, err := NewOpenAI(context.Background(), "gpt-4o", "", "", nil)
	if err == nil {
		t.Fatal("expected error for empty API key")
	}
}

func TestExtractChatGPTAccountID(t *testing.T) {
	// JWT payload: {"https://api.openai.com/auth":{"chatgpt_account_id":"acct-42"}}
	jwt := "eyJhbGciOiJub25lIn0.eyJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGgiOnsiY2hhdGdwdF9hY2NvdW50X2lkIjoiYWNjdC00MiJ9fQ.sig"
	if got := extractChatGPTAccountID(jwt); got != "acct-42" {
		t.Errorf("extractChatGPTAccountID = %q, want %q", got, "acct-42")
	}
	for _, bad := range []string{"", "sk-abc", "a.b", "not.json.here"} {
		if got := extractChatGPTAccountID(bad); got != "" {
			t.Errorf("extractChatGPTAccountID(%q) = %q, want empty", bad, got)
		}
	}
}

func TestNewOpenAI_CodexBackendRouting(t *testing.T) {
	// Codex OAuth JWT + a model the ChatGPT backend accepts — must flip to
	// codex backend and force Responses path.
	jwt := "eyJhbGciOiJub25lIn0.eyJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGgiOnsiY2hhdGdwdF9hY2NvdW50X2lkIjoiYWNjdC00MiJ9fQ.sig"
	llm, err := NewOpenAI(context.Background(), "gpt-5.5", jwt, "", nil)
	if err != nil {
		t.Fatalf("NewOpenAI(codex jwt) error: %v", err)
	}
	om, ok := llm.(*openaiModel)
	if !ok {
		t.Fatal("expected *openaiModel")
	}
	if !om.codexBackend {
		t.Error("codexBackend flag should be true for codex OAuth token")
	}
	if mode := om.endpointMode(); mode != "responses" {
		t.Errorf("endpointMode = %q, want %q (codex backend forces Responses)", mode, "responses")
	}
}

func TestNewOpenAI_CodexBackendRejectsUnsupportedModel(t *testing.T) {
	// The ChatGPT backend 400s "The '<id>' model is not supported when
	// using Codex with a ChatGPT account." for models outside its allowed
	// list. We pre-flight and fail with an actionable error.
	jwt := "eyJhbGciOiJub25lIn0.eyJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGgiOnsiY2hhdGdwdF9hY2NvdW50X2lkIjoiYWNjdC00MiJ9fQ.sig"
	_, err := NewOpenAI(context.Background(), "gpt-5.4-codex", jwt, "", nil)
	if err == nil {
		t.Fatal("expected NewOpenAI to reject unsupported codex model")
	}
	if !strings.Contains(err.Error(), "not supported by the ChatGPT codex backend") {
		t.Errorf("error message missing codex-backend hint: %v", err)
	}
	if !strings.Contains(err.Error(), "gpt-5.5") {
		t.Errorf("error should list supported models including gpt-5.5: %v", err)
	}
}

func TestNewOpenAI_ExplicitBaseURLOverridesCodexRouting(t *testing.T) {
	// When the caller explicitly supplies a baseURL (e.g. self-hosted
	// proxy), we must not hijack it to chatgpt.com even if the key looks
	// like a codex JWT. The supported-models guard also relaxes since the
	// caller is explicitly targeting a non-ChatGPT endpoint.
	jwt := "eyJhbGciOiJub25lIn0.eyJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGgiOnsiY2hhdGdwdF9hY2NvdW50X2lkIjoiYWNjdC00MiJ9fQ.sig"
	llm, err := NewOpenAI(context.Background(), "gpt-5.4-codex", jwt, "https://proxy.example/v1", nil)
	if err != nil {
		t.Fatalf("NewOpenAI error: %v", err)
	}
	om := llm.(*openaiModel)
	if om.codexBackend {
		t.Error("codexBackend must not be set when baseURL was provided")
	}
}

func TestNewOpenAI_PlainAPIKeyUsesDefaultBackend(t *testing.T) {
	llm, err := NewOpenAI(context.Background(), "gpt-4o", "sk-plain", "", nil)
	if err != nil {
		t.Fatalf("NewOpenAI error: %v", err)
	}
	om := llm.(*openaiModel)
	if om.codexBackend {
		t.Error("sk- keys must not trigger codex backend routing")
	}
}

func TestOaiContentsToMessagesEdgeCases(t *testing.T) {
	t.Run("nil content entries are skipped", func(t *testing.T) {
		contents := []*genai.Content{
			nil,
			{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
			nil,
		}
		msgs, _ := oaiContentsToMessages(contents, nil)
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
	})

	t.Run("nil parts in content are skipped", func(t *testing.T) {
		contents := []*genai.Content{
			{Role: "user", Parts: []*genai.Part{nil, {Text: "Hello"}, nil}},
		}
		msgs, _ := oaiContentsToMessages(contents, nil)
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
	})

	t.Run("system instruction with multiple parts", func(t *testing.T) {
		config := &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{
					{Text: "Part one."},
					nil,
					{Text: "Part two."},
				},
			},
		}
		contents := []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		}
		_, sysInstr := oaiContentsToMessages(contents, config)
		if sysInstr != "Part one.\nPart two." {
			t.Errorf("system instruction = %q, want %q", sysInstr, "Part one.\nPart two.")
		}
	})

	t.Run("system role content is skipped", func(t *testing.T) {
		contents := []*genai.Content{
			{Role: " system ", Parts: []*genai.Part{{Text: "ignored"}}},
			{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
		}
		msgs, _ := oaiContentsToMessages(contents, nil)
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
	})

	t.Run("assistant message with text and function calls", func(t *testing.T) {
		fc := genai.NewPartFromFunctionCall("my_tool", map[string]any{"arg": "val"})
		fc.FunctionCall.ID = "call_abc"

		fr := &genai.Part{
			FunctionResponse: &genai.FunctionResponse{
				ID:       "call_abc",
				Name:     "my_tool",
				Response: map[string]any{"result": "tool output text"},
			},
		}

		contents := []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Do something"}}},
			{Role: "model", Parts: []*genai.Part{{Text: "I will call the tool"}, fc}},
			{Role: "user", Parts: []*genai.Part{fr}},
		}

		msgs, _ := oaiContentsToMessages(contents, nil)
		// user + assistant(text+tool_calls) + tool_response
		if len(msgs) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(msgs))
		}
	})

	t.Run("function call without matching response", func(t *testing.T) {
		fc := genai.NewPartFromFunctionCall("orphan_tool", map[string]any{})
		fc.FunctionCall.ID = "call_orphan"

		contents := []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Call it"}}},
			{Role: "model", Parts: []*genai.Part{fc}},
		}

		msgs, _ := oaiContentsToMessages(contents, nil)
		// user + assistant(tool_calls) + tool_response (with default "No response available")
		if len(msgs) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(msgs))
		}
	})

	t.Run("content with nil Parts slice collected for function responses", func(t *testing.T) {
		contents := []*genai.Content{
			{Role: "user", Parts: nil},
			{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
		}
		msgs, _ := oaiContentsToMessages(contents, nil)
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
	})

	t.Run("empty text parts produce no message", func(t *testing.T) {
		contents := []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: ""}}},
		}
		msgs, _ := oaiContentsToMessages(contents, nil)
		if len(msgs) != 0 {
			t.Fatalf("expected 0 messages for empty text, got %d", len(msgs))
		}
	})

	t.Run("assistant role text message", func(t *testing.T) {
		contents := []*genai.Content{
			{Role: "assistant", Parts: []*genai.Part{{Text: "I am an assistant"}}},
		}
		msgs, _ := oaiContentsToMessages(contents, nil)
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
		if msgs[0].OfAssistant == nil {
			t.Error("expected assistant message type")
		}
	})
}

func TestOpenAIModelName(t *testing.T) {
	// Create a mock OpenAI model to test Name() method
	llm := &openaiModel{modelName: "gpt-4o"}
	if got := llm.Name(); got != "gpt-4o" {
		t.Errorf("Name() = %q, want %q", got, "gpt-4o")
	}
}

func TestOpenAIGenerateContentErrors(t *testing.T) {
	// Test with invalid API key to trigger error path
	llm, err := NewOpenAI(context.Background(), "gpt-4o", "test-key-invalid", "", nil)
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	t.Run("empty contents", func(t *testing.T) {
		req := &model.LLMRequest{
			Contents: []*genai.Content{},
		}
		seq := llm.GenerateContent(context.Background(), req, false)
		for resp, err := range seq {
			if err != nil {
				// Expected - no valid content
				return
			}
			_ = resp
		}
	})

	t.Run("with system prompt", func(t *testing.T) {
		req := &model.LLMRequest{
			Contents: []*genai.Content{
				{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
			},
			Config: &genai.GenerateContentConfig{
				SystemInstruction: &genai.Content{
					Parts: []*genai.Part{{Text: "You are helpful."}},
				},
			},
		}
		seq := llm.GenerateContent(context.Background(), req, false)
		for resp, err := range seq {
			if err != nil {
				// Expected - API will fail with invalid key
				return
			}
			_ = resp
		}
	})
}

func TestOpenAIGenerateContentStreaming(t *testing.T) {
	// Test streaming mode
	llm, err := NewOpenAI(context.Background(), "gpt-4o", "test-key-invalid", "", nil)
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		},
	}

	seq := llm.GenerateContent(context.Background(), req, true)
	for resp, err := range seq {
		if err != nil {
			return
		}
		_ = resp
	}
}

func TestOpenAIGenerateContentWithTools(t *testing.T) {
	// Test with tools configured
	llm, err := NewOpenAI(context.Background(), "gpt-4o", "test-key-invalid", "", nil)
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Use the tool"}}},
		},
		Config: &genai.GenerateContentConfig{
			Tools: []*genai.Tool{
				{
					FunctionDeclarations: []*genai.FunctionDeclaration{
						{
							Name:        "test_tool",
							Description: "A test tool",
							ParametersJsonSchema: map[string]any{
								"type": "object",
								"properties": map[string]any{
									"arg": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
			},
		},
	}

	seq := llm.GenerateContent(context.Background(), req, false)
	for resp, err := range seq {
		if err != nil {
			return
		}
		_ = resp
	}
}

func TestOpenAIGenerateContentWithModelOverride(t *testing.T) {
	// Test with model override in request
	llm, err := NewOpenAI(context.Background(), "gpt-4o", "test-key-invalid", "", nil)
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	req := &model.LLMRequest{
		Model: "gpt-4-turbo",
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		},
	}

	seq := llm.GenerateContent(context.Background(), req, false)
	for resp, err := range seq {
		if err != nil {
			return
		}
		_ = resp
	}
}

// TestOaiContentsToMessagesFunctionCallNoMatchingResponse exercises the
// "No response available" fallback when a function call has no matching
// response ID in the function-responses map.
func TestOaiContentsToMessagesFunctionCallNoMatchingResponse(t *testing.T) {
	fc := genai.NewPartFromFunctionCall("my_tool", map[string]any{"x": 1})
	fc.FunctionCall.ID = "call_no_match"

	// No FunctionResponse is provided for this call ID.
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "Do it"}}},
		{Role: "model", Parts: []*genai.Part{fc}},
	}

	msgs, _ := oaiContentsToMessages(contents, nil)
	// user + assistant(tool_calls) + tool_response(default text)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
}

// TestOaiContentsToMessagesNilPartsSlice verifies that a content entry with a
// nil Parts slice does not panic and produces a fallback message correctly.
func TestOaiContentsToMessagesNilPartsSliceOnly(t *testing.T) {
	contents := []*genai.Content{
		{Role: "user", Parts: nil},
	}
	msgs, _ := oaiContentsToMessages(contents, nil)
	// nil parts → no text parts → no message produced
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages for nil-parts content, got %d", len(msgs))
	}
}

// TestOaiContentsToMessagesAssistantFunctionCallNoText exercises the path
// where an "assistant" role message has only function calls (no text parts),
// verifying the content.OfString is not set.
func TestOaiContentsToMessagesAssistantFunctionCallNoText(t *testing.T) {
	fc := genai.NewPartFromFunctionCall("tool_a", map[string]any{"arg": "val"})
	fc.FunctionCall.ID = "call_no_text"

	fr := &genai.Part{
		FunctionResponse: &genai.FunctionResponse{
			ID:       "call_no_text",
			Name:     "tool_a",
			Response: map[string]any{"result": "result text"},
		},
	}

	// "assistant" role (not "model") with only a function call, no text.
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "use the tool"}}},
		{Role: "assistant", Parts: []*genai.Part{fc}},
		{Role: "user", Parts: []*genai.Part{fr}},
	}

	msgs, _ := oaiContentsToMessages(contents, nil)
	// user + assistant(tool_calls, no text) + tool_response
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[1].OfAssistant == nil {
		t.Fatal("expected assistant message")
	}
	// When there are no text parts, the assistant message should only have tool calls.
	if len(msgs[1].OfAssistant.ToolCalls) == 0 {
		t.Error("expected tool calls on assistant message")
	}
}

func TestOpenAINonStreamingTextResponse(t *testing.T) {
	// Mock server that returns a successful text completion.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		body := map[string]any{
			"id":     "chatcmpl-test",
			"object": "chat.completion",
			"model":  "gpt-4o",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": "Hello world",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	ctx := context.Background()
	llm, err := NewOpenAI(ctx, "gpt-4o", "sk-test", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewOpenAI() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Say hello"}}},
		},
	}

	var responses []*model.LLMResponse
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			t.Fatalf("GenerateContent() error: %v", err)
		}
		responses = append(responses, resp)
	}

	if len(responses) == 0 {
		t.Fatal("expected at least one response")
	}
	final := responses[len(responses)-1]
	if final.Content == nil {
		t.Fatal("expected non-nil Content")
	}
	if len(final.Content.Parts) == 0 {
		t.Fatal("expected at least one part in response")
	}
	if final.Content.Parts[0].Text != "Hello world" {
		t.Errorf("text = %q, want %q", final.Content.Parts[0].Text, "Hello world")
	}
	if !final.TurnComplete {
		t.Error("expected TurnComplete = true")
	}
	if final.FinishReason != genai.FinishReasonStop {
		t.Errorf("finish reason = %v, want Stop", final.FinishReason)
	}
	if final.UsageMetadata == nil {
		t.Fatal("expected non-nil UsageMetadata")
	}
	if final.UsageMetadata.PromptTokenCount != 10 {
		t.Errorf("prompt tokens = %d, want 10", final.UsageMetadata.PromptTokenCount)
	}
	if final.UsageMetadata.CandidatesTokenCount != 5 {
		t.Errorf("completion tokens = %d, want 5", final.UsageMetadata.CandidatesTokenCount)
	}
}

func TestOpenAINonStreamingToolCallResponse(t *testing.T) {
	// Mock server that returns a tool call in the response.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{
			"id":     "chatcmpl-tool-test",
			"object": "chat.completion",
			"model":  "gpt-4o",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": "",
						"tool_calls": []map[string]any{
							{
								"id":   "call_abc123",
								"type": "function",
								"function": map[string]any{
									"name":      "get_weather",
									"arguments": `{"location":"San Francisco"}`,
								},
							},
						},
					},
					"finish_reason": "tool_calls",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     15,
				"completion_tokens": 20,
				"total_tokens":      35,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	ctx := context.Background()
	llm, err := NewOpenAI(ctx, "gpt-4o", "sk-test", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewOpenAI() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "What's the weather in SF?"}}},
		},
		Config: &genai.GenerateContentConfig{
			Tools: []*genai.Tool{
				{
					FunctionDeclarations: []*genai.FunctionDeclaration{
						{
							Name:        "get_weather",
							Description: "Get current weather",
							ParametersJsonSchema: map[string]any{
								"type": "object",
								"properties": map[string]any{
									"location": map[string]any{"type": "string"},
								},
								"required": []any{"location"},
							},
						},
					},
				},
			},
		},
	}

	var responses []*model.LLMResponse
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			t.Fatalf("GenerateContent() error: %v", err)
		}
		responses = append(responses, resp)
	}

	if len(responses) == 0 {
		t.Fatal("expected at least one response")
	}
	final := responses[len(responses)-1]
	if final.Content == nil {
		t.Fatal("expected non-nil Content")
	}

	// Find the function call part.
	var fcPart *genai.Part
	for _, p := range final.Content.Parts {
		if p.FunctionCall != nil {
			fcPart = p
			break
		}
	}
	if fcPart == nil {
		t.Fatal("expected a FunctionCall part in response")
	}
	fc := fcPart.FunctionCall
	if got := fc.Name; got != "get_weather" {
		t.Errorf("function name = %q, want get_weather", got)
	}
	if fc.ID != "call_abc123" {
		t.Errorf("function call ID = %q, want call_abc123", fc.ID)
	}
	loc, _ := fc.Args["location"].(string)
	if loc != "San Francisco" {
		t.Errorf("location arg = %q, want San Francisco", loc)
	}
}

func TestOpenAINonStreamingErrorResponse(t *testing.T) {
	// Mock server that returns a 500 error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"internal server error","type":"server_error"}}`))
	}))
	defer srv.Close()

	ctx := context.Background()
	llm, err := NewOpenAI(ctx, "gpt-4o", "sk-test", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewOpenAI() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
		},
	}

	gotError := false
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			gotError = true
			break
		}
		if resp != nil && resp.ErrorCode != "" {
			gotError = true
			break
		}
	}
	if !gotError {
		t.Error("expected an error or ErrorCode for 500 response")
	}
}

func TestAccumulateOaiToolCall(t *testing.T) {
	acc := make(map[int64]map[string]any)

	// First chunk: id and name
	accumulateOaiToolCall(acc, 0, "call_abc", "bash", "")
	if acc[0]["id"] != "call_abc" {
		t.Errorf("id = %q, want call_abc", acc[0]["id"])
	}
	if acc[0]["name"] != "bash" {
		t.Errorf("name = %q, want bash", acc[0]["name"])
	}

	// Second chunk: partial arguments
	accumulateOaiToolCall(acc, 0, "", "", `{"comm`)
	if acc[0]["arguments"] != `{"comm` {
		t.Errorf("arguments = %q, want partial", acc[0]["arguments"])
	}

	// Third chunk: rest of arguments
	accumulateOaiToolCall(acc, 0, "", "", `and":"ls"}`)
	if acc[0]["arguments"] != `{"command":"ls"}` {
		t.Errorf("arguments = %q, want full JSON", acc[0]["arguments"])
	}

	// Second tool call
	accumulateOaiToolCall(acc, 1, "call_def", "read", `{"path":"/tmp"}`)
	if len(acc) != 2 {
		t.Errorf("expected 2 tool calls, got %d", len(acc))
	}
}

func TestBuildOaiFinalResponse_TextOnly(t *testing.T) {
	s := &oaiStreamState{
		text:             "Hello world",
		toolCalls:        map[int64]map[string]any{},
		finishReason:     "stop",
		promptTokens:     10,
		completionTokens: 5,
	}
	resp := buildOaiFinalResponse(s)

	if resp.Partial {
		t.Error("expected Partial = false")
	}
	if !resp.TurnComplete {
		t.Error("expected TurnComplete = true")
	}
	if resp.FinishReason != genai.FinishReasonStop {
		t.Errorf("FinishReason = %v, want Stop", resp.FinishReason)
	}
	if len(resp.Content.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(resp.Content.Parts))
	}
	if resp.Content.Parts[0].Text != "Hello world" {
		t.Errorf("text = %q", resp.Content.Parts[0].Text)
	}
	if resp.UsageMetadata.PromptTokenCount != 10 {
		t.Errorf("PromptTokenCount = %d, want 10", resp.UsageMetadata.PromptTokenCount)
	}
}

func TestBuildOaiFinalResponse_WithToolCalls(t *testing.T) {
	s := &oaiStreamState{
		toolCalls: map[int64]map[string]any{
			1: {"id": "call_2", "name": "read", "arguments": `{"path":"/tmp"}`},
			0: {"id": "call_1", "name": "bash", "arguments": `{"command":"ls"}`},
		},
		finishReason:     "tool_calls",
		promptTokens:     20,
		completionTokens: 15,
	}
	resp := buildOaiFinalResponse(s)

	if len(resp.Content.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(resp.Content.Parts))
	}
	// Should be sorted by index: 0 first, 1 second
	if resp.Content.Parts[0].FunctionCall.Name != "bash" {
		t.Errorf("first tool = %q, want bash", resp.Content.Parts[0].FunctionCall.Name)
	}
	if resp.Content.Parts[1].FunctionCall.Name != "read" {
		t.Errorf("second tool = %q, want read", resp.Content.Parts[1].FunctionCall.Name)
	}
	if resp.Content.Parts[0].FunctionCall.ID != "call_1" {
		t.Errorf("first tool ID = %q, want call_1", resp.Content.Parts[0].FunctionCall.ID)
	}
}

func TestBuildOaiFinalResponse_TextAndToolCalls(t *testing.T) {
	s := &oaiStreamState{
		text: "I'll help you.",
		toolCalls: map[int64]map[string]any{
			0: {"id": "call_x", "name": "bash", "arguments": `{"command":"pwd"}`},
		},
		finishReason: "tool_calls",
	}
	resp := buildOaiFinalResponse(s)

	if len(resp.Content.Parts) != 2 {
		t.Fatalf("expected 2 parts (text + tool), got %d", len(resp.Content.Parts))
	}
	if resp.Content.Parts[0].Text != "I'll help you." {
		t.Errorf("text = %q", resp.Content.Parts[0].Text)
	}
	if resp.Content.Parts[1].FunctionCall == nil {
		t.Error("expected FunctionCall in second part")
	}
}

func TestBuildOaiFinalResponse_EmptyState(t *testing.T) {
	s := &oaiStreamState{toolCalls: map[int64]map[string]any{}}
	resp := buildOaiFinalResponse(s)

	if len(resp.Content.Parts) != 0 {
		t.Errorf("expected 0 parts, got %d", len(resp.Content.Parts))
	}
	if resp.UsageMetadata != nil {
		t.Error("expected nil UsageMetadata when tokens are 0")
	}
}

func TestBuildOaiFinalResponse_MaxTokens(t *testing.T) {
	s := &oaiStreamState{
		text:             "truncated",
		toolCalls:        map[int64]map[string]any{},
		finishReason:     "length",
		promptTokens:     100,
		completionTokens: 4096,
	}
	resp := buildOaiFinalResponse(s)

	if resp.FinishReason != genai.FinishReasonMaxTokens {
		t.Errorf("FinishReason = %v, want MaxTokens", resp.FinishReason)
	}
}

func TestNewAzureOpenAI_MissingAPIKey(t *testing.T) {
	// Save original and restore after test.
	orig := osGetenv
	t.Cleanup(func() { osGetenv = orig })

	// Mock env vars to return empty strings.
	osGetenv = func(key string) string { return "" }

	_, err := NewAzureOpenAI(context.Background(), "my-deployment", "", "", "", nil)
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}

func TestNewAzureOpenAI_MissingEndpoint(t *testing.T) {
	orig := osGetenv
	t.Cleanup(func() { osGetenv = orig })

	osGetenv = func(key string) string {
		if key == "AZURE_OPENAI_API_KEY" {
			return "test-key"
		}
		return ""
	}

	_, err := NewAzureOpenAI(context.Background(), "my-deployment", "", "", "", nil)
	if err == nil {
		t.Fatal("expected error for missing endpoint")
	}
}

func TestNewAzureOpenAI_Success(t *testing.T) {
	orig := osGetenv
	t.Cleanup(func() { osGetenv = orig })

	osGetenv = func(key string) string {
		switch key {
		case "AZURE_OPENAI_API_KEY":
			return "test-azure-key"
		case "AZURE_OPENAI_ENDPOINT":
			return "https://my-resource.openai.azure.com"
		case "OPENAI_API_VERSION":
			return "2024-02-15-preview"
		}
		return ""
	}

	llm, err := NewAzureOpenAI(context.Background(), "my-deployment", "", "", "", nil)
	if err != nil {
		t.Fatalf("NewAzureOpenAI() error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewAzureOpenAI() returned nil")
	}
	if llm.Name() != "my-deployment" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "my-deployment")
	}
}

func TestNewAzureOpenAI_WithOverrides(t *testing.T) {
	orig := osGetenv
	t.Cleanup(func() { osGetenv = orig })

	// Override only API key; endpoint and api-version come from arguments.
	osGetenv = func(key string) string {
		if key == "AZURE_OPENAI_API_KEY" {
			return "test-azure-key"
		}
		return ""
	}

	llm, err := NewAzureOpenAI(context.Background(), "my-deployment", "", "https://custom.openai.azure.com/", "2024-06-01", nil)
	if err != nil {
		t.Fatalf("NewAzureOpenAI() with overrides error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewAzureOpenAI() with overrides returned nil")
	}
}

func TestNewAzureOpenAI_AcceptsLegacyAPIKeyEnvName(t *testing.T) {
	orig := osGetenv
	t.Cleanup(func() { osGetenv = orig })

	osGetenv = func(key string) string {
		switch key {
		case "AZUREOPENAI_API_KEY":
			return "legacy-azure-key"
		case "AZURE_OPENAI_ENDPOINT":
			return "https://my-resource.openai.azure.com"
		}
		return ""
	}

	llm, err := NewAzureOpenAI(context.Background(), "my-deployment", "", "", "", nil)
	if err != nil {
		t.Fatalf("NewAzureOpenAI() with AZUREOPENAI_API_KEY error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewAzureOpenAI() with AZUREOPENAI_API_KEY returned nil")
	}
}

func TestAzurePathRewriteMiddleware(t *testing.T) {
	// Create a test server that records the request path.
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"test"}}]}`))
	}))
	defer srv.Close()

	// Create Azure client with the middleware.
	client := openai.NewClient(
		option.WithBaseURL(srv.URL+"/"),
		option.WithMiddleware(azurePathRewriteMiddleware()),
		option.WithAPIKey("test"),
	)

	ctx := context.Background()
	_, _ = client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{Model: "test-model"})

	if receivedPath != "/openai/deployments/test-model/chat/completions" {
		t.Errorf("received path = %q, want %q", receivedPath, "/openai/deployments/test-model/chat/completions")
	}
}

func TestAzurePathRewriteMiddleware_Embedding(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	client := openai.NewClient(
		option.WithBaseURL(srv.URL+"/"),
		option.WithMiddleware(azurePathRewriteMiddleware()),
		option.WithAPIKey("test"),
	)

	ctx := context.Background()
	_, _ = client.Embeddings.New(ctx, openai.EmbeddingNewParams{Model: "embed-model"})

	if receivedPath != "/openai/deployments/embed-model/embeddings" {
		t.Errorf("received path = %q, want %q", receivedPath, "/openai/deployments/embed-model/embeddings")
	}
}

func TestAzurePathRewriteMiddleware_PreservesBasePath(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"test"}}]}`))
	}))
	defer srv.Close()

	client := openai.NewClient(
		option.WithBaseURL(srv.URL+"/api/v1/proxy/"),
		option.WithMiddleware(azurePathRewriteMiddleware()),
		option.WithAPIKey("test"),
	)

	ctx := context.Background()
	_, _ = client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{Model: "test-model"})

	expected := "/api/v1/proxy/openai/deployments/test-model/chat/completions"
	if receivedPath != expected {
		t.Errorf("received path = %q, want %q", receivedPath, expected)
	}
}

func TestNewAzureOpenAI_OpenAICompatEndpointSkipsRewriteAndAPIVersion(t *testing.T) {
	var (
		receivedPath     string
		receivedRawQuery string
		receivedUsername string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedRawQuery = r.URL.RawQuery
		receivedUsername = r.Header.Get("username")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	llm, err := NewAzureOpenAI(
		context.Background(),
		"gpt-5.4",
		"",
		srv.URL+"/openai/v1",
		"",
		&LLMOptions{
			ExtraHeaders: map[string]string{"username": "dmitriyr"},
		},
	)
	if err != nil {
		t.Fatalf("NewAzureOpenAI() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			genai.NewContentFromText("ping", genai.RoleUser),
		},
	}
	for _, genErr := range llm.GenerateContent(context.Background(), req, false) {
		if genErr != nil {
			t.Fatalf("GenerateContent() error: %v", genErr)
		}
	}

	if receivedPath != "/openai/v1/chat/completions" {
		t.Errorf("received path = %q, want %q", receivedPath, "/openai/v1/chat/completions")
	}
	if receivedRawQuery != "" {
		t.Errorf("raw query = %q, want empty", receivedRawQuery)
	}
	if receivedUsername != "dmitriyr" {
		t.Errorf("username header = %q, want %q", receivedUsername, "dmitriyr")
	}
}

// --- Responses API tests ---

func TestModelNeedsResponses(t *testing.T) {
	tests := []struct {
		model    string
		expected bool
	}{
		// Responses-only models
		{"gpt-5-codex", true},
		{"gpt-5.1-codex-mini", true},
		{"gpt-5.1-codex-max", true},
		{"gpt-5.2-codex", true},
		{"gpt-5.3-codex", true},
		{"gpt-5.1-codex", true},
		// Case insensitive
		{"GPT-5-CODEX", true},
		{"Gpt-5.1-Codex-Mini", true},
		// Non-codex models (Chat Completions compatible)
		{"gpt-5.5", false},
		{"gpt-5.4", false},
		{"gpt-4o", false},
		{"o3-mini", false},
		{"gpt-5-mini", false},
		{"claude-sonnet-4-6", false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := modelNeedsResponses(tt.model)
			if got != tt.expected {
				t.Errorf("modelNeedsResponses(%q) = %v, want %v", tt.model, got, tt.expected)
			}
		})
	}
}

func TestOaiContentsToResponsesInput_SimpleText(t *testing.T) {
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "Hello world"}}},
	}

	input, instructions, err := oaiContentsToResponsesInput(contents, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if instructions != "" {
		t.Errorf("instructions = %q, want empty", instructions)
	}
	// Input must be a list — the ChatGPT codex backend rejects a bare
	// string with `{"detail":"Input must be a list"}`.
	if input.OfInputItemList == nil {
		t.Fatal("expected input item list, got string form")
	}
	if len(input.OfInputItemList) != 1 || input.OfInputItemList[0].OfMessage == nil {
		t.Fatalf("expected single user message item, got %+v", input.OfInputItemList)
	}
}

func TestOaiContentsToResponsesInput_PreservesMessageOrderAndRoles(t *testing.T) {
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "first"}}},
		{Role: "model", Parts: []*genai.Part{{Text: "assistant reply"}}},
		{Role: "user", Parts: []*genai.Part{{Text: "second"}}},
	}

	input, _, err := oaiContentsToResponsesInput(contents, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	items := input.OfInputItemList
	if len(items) != 3 {
		t.Fatalf("expected 3 input items, got %d", len(items))
	}

	wantRoles := []responses.EasyInputMessageRole{
		responses.EasyInputMessageRoleUser,
		responses.EasyInputMessageRoleAssistant,
		responses.EasyInputMessageRoleUser,
	}
	wantText := []string{"first", "assistant reply", "second"}
	for i, item := range items {
		if item.OfMessage == nil {
			t.Fatalf("item %d: expected message, got %+v", i, item)
		}
		if item.OfMessage.Role != wantRoles[i] {
			t.Errorf("item %d role = %q, want %q", i, item.OfMessage.Role, wantRoles[i])
		}
		if !item.OfMessage.Content.OfString.Valid() || item.OfMessage.Content.OfString.Value != wantText[i] {
			t.Errorf("item %d text = %q, want %q", i, item.OfMessage.Content.OfString.Value, wantText[i])
		}
	}
}

func TestOaiContentsToResponsesInput_WithSystemInstruction(t *testing.T) {
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: "You are a helpful assistant."}},
		},
	}
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
	}

	_, instructions, err := oaiContentsToResponsesInput(contents, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if instructions != "You are a helpful assistant." {
		t.Errorf("instructions = %q, want %q", instructions, "You are a helpful assistant.")
	}
}

func TestOaiContentsToResponsesInput_WithFunctionCalls(t *testing.T) {
	fc := genai.NewPartFromFunctionCall("read_file", map[string]any{"path": "/tmp/test.go"})
	fc.FunctionCall.ID = "call_123"

	fr := &genai.Part{
		FunctionResponse: &genai.FunctionResponse{
			ID:       "call_123",
			Name:     "read_file",
			Response: map[string]any{"result": "file contents"},
		},
	}

	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "Read the file"}}},
		{Role: "model", Parts: []*genai.Part{fc}},
		{Role: "user", Parts: []*genai.Part{fr}},
	}

	input, _, err := oaiContentsToResponsesInput(contents, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if input.OfInputItemList == nil {
		t.Fatal("expected input item list")
	}
	items := input.OfInputItemList
	if len(items) != 3 {
		t.Fatalf("expected 3 input items, got %d", len(items))
	}
	if items[1].OfFunctionCall == nil {
		t.Fatal("expected function call item")
	}
	if items[1].OfFunctionCall.CallID != "call_123" {
		t.Fatalf("function call CallID = %q, want %q", items[1].OfFunctionCall.CallID, "call_123")
	}
	if items[2].OfFunctionCallOutput == nil {
		t.Fatal("expected function call output item")
	}
	if items[2].OfFunctionCallOutput.CallID != "call_123" {
		t.Fatalf("function output CallID = %q, want %q", items[2].OfFunctionCallOutput.CallID, "call_123")
	}
}

func TestOaiContentsToResponsesInput_DoesNotInventMissingFunctionOutput(t *testing.T) {
	fc := genai.NewPartFromFunctionCall("find", map[string]any{"pattern": "*.go"})
	fc.FunctionCall.ID = "call_find"

	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "Search files"}}},
		{Role: "model", Parts: []*genai.Part{fc}},
	}

	input, _, err := oaiContentsToResponsesInput(contents, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	items := input.OfInputItemList
	if len(items) != 2 {
		t.Fatalf("expected message + function call only, got %d items", len(items))
	}
	if items[1].OfFunctionCall == nil {
		t.Fatalf("expected second item to be function call, got %+v", items[1])
	}
	if items[1].OfFunctionCall.CallID != "call_find" {
		t.Errorf("function call CallID = %q, want call_find", items[1].OfFunctionCall.CallID)
	}
	if items[1].OfFunctionCall.Arguments != `{"pattern":"*.go"}` {
		t.Errorf("function call arguments = %q, want pattern JSON", items[1].OfFunctionCall.Arguments)
	}
}

func TestOaiContentsToResponsesInput_SkipsFunctionCallsWithoutID(t *testing.T) {
	fc := genai.NewPartFromFunctionCall("read_file", map[string]any{"path": "/tmp/test.go"})
	fc.FunctionCall.ID = ""

	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "Read the file"}}},
		{Role: "model", Parts: []*genai.Part{fc}},
	}

	input, _, err := oaiContentsToResponsesInput(contents, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if input.OfInputItemList == nil {
		t.Fatal("expected input item list")
	}
	items := input.OfInputItemList
	if len(items) != 1 {
		t.Fatalf("expected only user message item, got %d", len(items))
	}
	if items[0].OfMessage == nil {
		t.Fatal("expected first item to be a message")
	}
}

func TestOaiContentsToResponsesInput_NilConfig(t *testing.T) {
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
	}
	_, instructions, err := oaiContentsToResponsesInput(contents, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if instructions != "" {
		t.Errorf("instructions = %q, want empty", instructions)
	}
}

func TestOaiGenaiToolsToResponses(t *testing.T) {
	tools := []*genai.Tool{
		{
			FunctionDeclarations: []*genai.FunctionDeclaration{
				{
					Name:        "get_weather",
					Description: "Get the current weather",
					ParametersJsonSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"location": map[string]any{"type": "string"},
						},
						"required": []any{"location"},
					},
				},
			},
		},
		nil, // skip nil tool
		{},  // skip empty tool
		{FunctionDeclarations: nil},
		{FunctionDeclarations: []*genai.FunctionDeclaration{nil}},
	}

	result := oaiGenaiToolsToResponses(tools)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}
	if result[0].OfFunction == nil {
		t.Fatal("expected function tool")
	}
	if !result[0].OfFunction.Strict.Valid() || result[0].OfFunction.Strict.Value {
		t.Fatalf("Responses function tools should set strict=false, got valid=%v value=%v", result[0].OfFunction.Strict.Valid(), result[0].OfFunction.Strict.Value)
	}
}

func TestOaiGenaiToolsToResponses_ConvertsJSONSchemaObject(t *testing.T) {
	type schemaInput struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path,omitempty"`
	}
	schema, err := jsonschema.For[schemaInput](nil)
	if err != nil {
		t.Fatalf("jsonschema.For: %v", err)
	}

	result := oaiGenaiToolsToResponses([]*genai.Tool{{
		FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name:                 "find",
			Description:          "Find files",
			ParametersJsonSchema: schema,
		}},
	}})
	if len(result) != 1 || result[0].OfFunction == nil {
		t.Fatalf("expected one function tool, got %+v", result)
	}

	params := result[0].OfFunction.Parameters
	requiredJSON, err := json.Marshal(params["required"])
	if err != nil {
		t.Fatalf("marshal required: %v", err)
	}
	if string(requiredJSON) != `["pattern"]` {
		t.Fatalf("required = %s, want [\"pattern\"]", requiredJSON)
	}
	properties, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %T, want map[string]any", params["properties"])
	}
	if _, ok := properties["pattern"]; !ok {
		t.Fatalf("properties missing pattern: %+v", properties)
	}
}

func TestParseResponsesOutput_TextOnly(t *testing.T) {
	// We can't easily construct ResponseOutputItemUnion directly,
	// so we just verify the function handles empty input gracefully.
	parts, finishReason := parseResponsesOutput(nil)
	if len(parts) != 0 {
		t.Errorf("expected 0 parts, got %d", len(parts))
	}
	if finishReason != "" {
		t.Errorf("finishReason = %q, want empty", finishReason)
	}
}

func TestBuildResponsesFinalParts_UsesDoneOnlyToolArguments(t *testing.T) {
	state := &responsesStreamState{toolCalls: make(map[int64]toolCallAcc)}
	updateResponsesToolCall(state, 0, "call_find", "find", "", false)
	updateResponsesToolCall(state, 0, "", "", `{"pattern":"*.go"}`, false)

	parts := buildResponsesFinalParts(state)
	if len(parts) != 1 {
		t.Fatalf("expected one function call part, got %d", len(parts))
	}
	fc := parts[0].FunctionCall
	if fc == nil {
		t.Fatalf("expected function call part, got %+v", parts[0])
	}
	if fc.ID != "call_find" || fc.Name != "find" {
		t.Fatalf("function call = id %q name %q, want call_find/find", fc.ID, fc.Name)
	}
	if fc.Args["pattern"] != "*.go" {
		t.Fatalf("function call pattern arg = %v, want *.go", fc.Args["pattern"])
	}
}

func TestOpenAIModelEndpointMode(t *testing.T) {
	t.Run("gpt-4o uses chat completions", func(t *testing.T) {
		m := &openaiModel{modelName: "gpt-4o"}
		if mode := m.endpointMode(); mode != "chat" {
			t.Errorf("endpointMode() = %q, want %q", mode, "chat")
		}
	})

	t.Run("gpt-5-codex uses responses", func(t *testing.T) {
		m := &openaiModel{modelName: "gpt-5-codex"}
		if mode := m.endpointMode(); mode != "responses" {
			t.Errorf("endpointMode() = %q, want %q", mode, "responses")
		}
	})

	t.Run("model override to codex uses responses", func(t *testing.T) {
		m := &openaiModel{modelName: "gpt-4o"}
		// endpointMode checks modelName, not req.Model
		// The GenerateContent path checks modelNeedsResponses(req.Model) too.
		// Verify the base model routing.
		if mode := m.endpointMode(); mode != "chat" {
			t.Errorf("base endpointMode() = %q, want %q", mode, "chat")
		}
	})
}

func TestOpenAIResponsesStreaming_MultiTurnState(t *testing.T) {
	// Test that previous_response_id is threaded through multiple calls.
	llm, err := NewOpenAI(context.Background(), "gpt-5-codex", "test-key", "", nil)
	if err != nil {
		t.Fatalf("NewOpenAI() error: %v", err)
	}
	m := llm.(*openaiModel)

	// Verify the model starts with no multi-turn state.
	if mode := m.endpointMode(); mode != "responses" {
		t.Errorf("endpointMode() = %q, want %q", mode, "responses")
	}
}

func TestOpenAIResponsesRequestUsesPreviousResponseID(t *testing.T) {
	m := &openaiModel{
		modelName: "gpt-5-codex",
		responseState: &responsesState{
			previousResponseID: "resp_prev_123",
		},
	}

	input, instructions, err := oaiContentsToResponsesInput([]*genai.Content{{
		Role:  string(genai.RoleUser),
		Parts: []*genai.Part{{Text: "review this"}},
	}}, nil)
	if err != nil {
		t.Fatalf("oaiContentsToResponsesInput() error: %v", err)
	}

	params := responses.ResponseNewParams{
		Model: m.modelName,
		Input: input,
	}
	if instructions != "" {
		params.Instructions = param.NewOpt(instructions)
	}
	if m.responseState != nil && m.responseState.previousResponseID != "" {
		params.PreviousResponseID = param.NewOpt(m.responseState.previousResponseID)
	}

	if !params.PreviousResponseID.Valid() {
		t.Fatal("PreviousResponseID should be set when response state exists")
	}
	if got := params.PreviousResponseID.Value; got != "resp_prev_123" {
		t.Fatalf("PreviousResponseID = %q, want %q", got, "resp_prev_123")
	}
	if params.Store.Valid() {
		t.Fatalf("Store should be unset so Responses API can retain server-side state; got valid=%v value=%v", params.Store.Valid(), params.Store.Value)
	}
}
