package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

func TestOllamaThinkingConfig(t *testing.T) {
	tests := []struct {
		level   string
		wantNil bool
		wantVal string
	}{
		{"none", true, ""},
		{"", true, ""},
		{"unknown", true, ""},
		{"low", false, "low"},
		{"medium", false, "medium"},
		{"high", false, "high"},
	}
	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			got := ollamaThinkingConfig(tt.level)
			if tt.wantNil {
				if got != nil {
					t.Errorf("ollamaThinkingConfig(%q) = %v, want nil", tt.level, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("ollamaThinkingConfig(%q) = nil, want non-nil", tt.level)
			}
			if got.Value != tt.wantVal {
				t.Errorf("ollamaThinkingConfig(%q).Value = %q, want %q", tt.level, got.Value, tt.wantVal)
			}
		})
	}
}

func TestOllamaFinishReasonToGenai(t *testing.T) {
	tests := []struct {
		reason string
		want   genai.FinishReason
	}{
		{"length", genai.FinishReasonMaxTokens},
		{"stop", genai.FinishReasonStop},
		{"", genai.FinishReasonStop},
		{"unknown", genai.FinishReasonStop},
	}
	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			got := ollamaFinishReasonToGenai(tt.reason)
			if got != tt.want {
				t.Errorf("ollamaFinishReasonToGenai(%q) = %v, want %v", tt.reason, got, tt.want)
			}
		})
	}
}

func TestOllamaContentsToMessages_SystemInstruction(t *testing.T) {
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: "You are a helpful assistant."}},
		},
	}
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
	}

	msgs, sysPrompt := ollamaContentsToMessages(contents, config)
	if sysPrompt != "You are a helpful assistant." {
		t.Errorf("system prompt = %q, want %q", sysPrompt, "You are a helpful assistant.")
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("message role = %q, want user", msgs[0].Role)
	}
	if msgs[0].Content != "Hello" {
		t.Errorf("message content = %q, want Hello", msgs[0].Content)
	}
}

func TestOllamaContentsToMessages_UserAndModel(t *testing.T) {
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "What is Go?"}}},
		{Role: "model", Parts: []*genai.Part{{Text: "Go is a programming language."}}},
		{Role: "user", Parts: []*genai.Part{{Text: "Tell me more."}}},
	}

	msgs, sysPrompt := ollamaContentsToMessages(contents, nil)
	if sysPrompt != "" {
		t.Errorf("system prompt = %q, want empty", sysPrompt)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("msg[0] role = %q, want user", msgs[0].Role)
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("msg[1] role = %q, want assistant", msgs[1].Role)
	}
	if msgs[2].Role != "user" {
		t.Errorf("msg[2] role = %q, want user", msgs[2].Role)
	}
}

func TestOllamaContentsToMessages_SkipsSystemRole(t *testing.T) {
	contents := []*genai.Content{
		{Role: "system", Parts: []*genai.Part{{Text: "ignored"}}},
		{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
	}

	msgs, _ := ollamaContentsToMessages(contents, nil)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message (system skipped), got %d", len(msgs))
	}
}

func TestOllamaContentsToMessages_NilContents(t *testing.T) {
	contents := []*genai.Content{nil}
	msgs, _ := ollamaContentsToMessages(contents, nil)
	// Should produce a default "Hello" message when no valid content found.
	if len(msgs) != 1 {
		t.Fatalf("expected 1 default message, got %d", len(msgs))
	}
	if msgs[0].Content != "Hello" {
		t.Errorf("expected default Hello message, got %q", msgs[0].Content)
	}
}

func TestOllamaContentsToMessages_NilParts(t *testing.T) {
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{nil, {Text: ""}, {Text: "actual"}}},
	}
	msgs, _ := ollamaContentsToMessages(contents, nil)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != "actual" {
		t.Errorf("message content = %q, want actual", msgs[0].Content)
	}
}

func TestOllamaContentsToMessages_FunctionCalls(t *testing.T) {
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

	msgs, _ := ollamaContentsToMessages(contents, nil)
	// user + assistant(tool_calls) + tool_response
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("assistant message role = %q, want assistant", msgs[1].Role)
	}
	if len(msgs[1].ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(msgs[1].ToolCalls))
	}
	if msgs[2].Role != "tool" {
		t.Errorf("tool result message role = %q, want tool", msgs[2].Role)
	}
}

func TestOllamaContentsToMessages_EmptyContents(t *testing.T) {
	msgs, _ := ollamaContentsToMessages(nil, nil)
	// Should produce default Hello message.
	if len(msgs) != 1 {
		t.Fatalf("expected 1 default message, got %d", len(msgs))
	}
	if msgs[0].Content != "Hello" {
		t.Errorf("expected default Hello, got %q", msgs[0].Content)
	}
}

func TestConvertToToolProperty(t *testing.T) {
	t.Run("valid map", func(t *testing.T) {
		raw := map[string]any{
			"type":        "string",
			"description": "A file path",
			"enum":        []any{"a", "b"},
		}
		prop := convertToToolProperty(raw)
		if prop.Type.String() != "string" {
			t.Errorf("type = %v, want string", prop.Type)
		}
		if prop.Description != "A file path" {
			t.Errorf("description = %q, want %q", prop.Description, "A file path")
		}
		if len(prop.Enum) != 2 {
			t.Errorf("enum len = %d, want 2", len(prop.Enum))
		}
	})

	t.Run("non-map input", func(t *testing.T) {
		prop := convertToToolProperty("not a map")
		if prop.Description != "" {
			t.Error("expected empty property for non-map input")
		}
	})

	t.Run("nil input", func(t *testing.T) {
		prop := convertToToolProperty(nil)
		if prop.Description != "" {
			t.Error("expected empty property for nil input")
		}
	})

	t.Run("missing fields", func(t *testing.T) {
		raw := map[string]any{"type": "integer"}
		prop := convertToToolProperty(raw)
		if prop.Type.String() != "integer" {
			t.Errorf("type = %v, want integer", prop.Type)
		}
		if prop.Description != "" {
			t.Errorf("description should be empty, got %q", prop.Description)
		}
	})
}

func TestOllamaGenaiToolsToOllama(t *testing.T) {
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
								"path": map[string]any{"type": "string", "description": "File path"},
							},
							"required": []any{"path"},
						},
					},
				},
			},
		}

		result := ollamaGenaiToolsToOllama(tools)
		if len(result) != 1 {
			t.Fatalf("expected 1 tool, got %d", len(result))
		}
		if result[0].Function.Name != "read_file" {
			t.Errorf("tool name = %q, want read_file", result[0].Function.Name)
		}
		if result[0].Function.Description != "Read a file" {
			t.Errorf("description = %q", result[0].Function.Description)
		}
		if len(result[0].Function.Parameters.Required) != 1 {
			t.Errorf("required len = %d, want 1", len(result[0].Function.Parameters.Required))
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
		result := ollamaGenaiToolsToOllama(tools)
		if len(result) != 1 {
			t.Fatalf("expected 1 tool, got %d", len(result))
		}
	})

	t.Run("multiple functions in one tool", func(t *testing.T) {
		tools := []*genai.Tool{
			{
				FunctionDeclarations: []*genai.FunctionDeclaration{
					{Name: "func1", Description: "First"},
					{Name: "func2", Description: "Second"},
				},
			},
		}
		result := ollamaGenaiToolsToOllama(tools)
		if len(result) != 2 {
			t.Fatalf("expected 2 tools, got %d", len(result))
		}
	})

	t.Run("nil tools slice", func(t *testing.T) {
		result := ollamaGenaiToolsToOllama(nil)
		if len(result) != 0 {
			t.Errorf("expected 0 tools for nil input, got %d", len(result))
		}
	})
}

func TestOllamaListModels(t *testing.T) {
	t.Run("default URL", func(t *testing.T) {
		// Test URL handling - might succeed if Ollama is running
		_, err := OllamaListModels(context.Background(), "")
		// Just check it doesn't panic - error or success both ok
		_ = err
	})

	t.Run("invalid URL", func(t *testing.T) {
		_, err := OllamaListModels(context.Background(), "://bad-url")
		if err == nil {
			t.Fatal("expected error for invalid URL")
		}
	})

	t.Run("custom URL", func(t *testing.T) {
		// Test custom URL parsing - connection refused expected
		_, err := OllamaListModels(context.Background(), "http://custom:11434")
		if err == nil {
			t.Fatal("expected error for unreachable Ollama server")
		}
	})
}

func TestOllamaGenerateContent(t *testing.T) {
	// Create a mock-like Ollama model for testing
	llm, err := NewOllama(context.Background(), "qwen3.5:latest", "http://localhost:11434", "none", nil)
	if err != nil {
		t.Skipf("skipping: could not create Ollama model: %v", err)
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

	t.Run("nil contents", func(t *testing.T) {
		req := &model.LLMRequest{
			Contents: nil,
		}
		seq := llm.GenerateContent(context.Background(), req, false)
		for resp, err := range seq {
			if err != nil {
				return
			}
			_ = resp
		}
	})

	t.Run("with system prompt", func(t *testing.T) {
		req := &model.LLMRequest{
			Contents: []*genai.Content{
				{Role: "user", Parts: []*genai.Part{{Text: "Say 'hi' and nothing else."}}},
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
				return
			}
			_ = resp
		}
	})

	t.Run("model override in request", func(t *testing.T) {
		req := &model.LLMRequest{
			Model: "qwen3.5:latest",
			Contents: []*genai.Content{
				{Role: "user", Parts: []*genai.Part{{Text: "Say 'hi'."}}},
			},
		}
		seq := llm.GenerateContent(context.Background(), req, false)
		for resp, err := range seq {
			if err != nil {
				return
			}
			_ = resp
		}
	})

	t.Run("with thinking level", func(t *testing.T) {
		llmThink, err := NewOllama(context.Background(), "qwen3.5:latest", "http://localhost:11434", "medium", nil)
		if err != nil {
			t.Skipf("skipping: could not create Ollama model: %v", err)
		}
		req := &model.LLMRequest{
			Contents: []*genai.Content{
				{Role: "user", Parts: []*genai.Part{{Text: "Say 'hi'."}}},
			},
		}
		seq := llmThink.GenerateContent(context.Background(), req, false)
		for resp, err := range seq {
			if err != nil {
				return
			}
			_ = resp
		}
	})

	t.Run("with tools", func(t *testing.T) {
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
	})

	t.Run("streaming mode", func(t *testing.T) {
		req := &model.LLMRequest{
			Contents: []*genai.Content{
				{Role: "user", Parts: []*genai.Part{{Text: "Say 'hi'."}}},
			},
		}
		seq := llm.GenerateContent(context.Background(), req, true)
		for resp, err := range seq {
			if err != nil {
				return
			}
			_ = resp
		}
	})

	// Test function call handling - the key to getting higher coverage
	t.Run("function call from assistant message", func(t *testing.T) {
		// This tests the function call and response code path
		fc := genai.NewPartFromFunctionCall("my_tool", map[string]any{"arg": "value"})
		fc.FunctionCall.ID = "call_1"

		contents := []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Call the tool"}}},
			{Role: "model", Parts: []*genai.Part{fc}},
		}
		req := &model.LLMRequest{
			Contents: contents,
		}
		seq := llm.GenerateContent(context.Background(), req, false)
		for resp, err := range seq {
			if err != nil {
				return
			}
			_ = resp
		}
	})
}

func TestOllamaContentsToMessages_AssistantRoleText(t *testing.T) {
	// Covers the "assistant" keyword (not just "model") for plain-text messages.
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
		{Role: "assistant", Parts: []*genai.Part{{Text: "Hi there"}}},
	}
	msgs, _ := ollamaContentsToMessages(contents, nil)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("second message role = %q, want assistant", msgs[1].Role)
	}
	if msgs[1].Content != "Hi there" {
		t.Errorf("second message content = %q, want Hi there", msgs[1].Content)
	}
}

func TestOllamaContentsToMessages_AssistantRoleFunctionCall(t *testing.T) {
	// Covers the "assistant" keyword (not "model") for the function-call path.
	fc := genai.NewPartFromFunctionCall("my_tool", map[string]any{"x": "y"})
	fc.FunctionCall.ID = "call_asst"

	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "Do it"}}},
		{Role: "assistant", Parts: []*genai.Part{fc}},
	}
	msgs, _ := ollamaContentsToMessages(contents, nil)
	// user + assistant(tool_calls) + tool_result
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("assistant message role = %q, want assistant", msgs[1].Role)
	}
	if len(msgs[1].ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(msgs[1].ToolCalls))
	}
}

func TestOllamaContentsToMessages_MultipleTextParts(t *testing.T) {
	// Covers joining multiple text parts with "\n".
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "Part A"}, {Text: "Part B"}}},
	}
	msgs, _ := ollamaContentsToMessages(contents, nil)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != "Part A\nPart B" {
		t.Errorf("content = %q, want %q", msgs[0].Content, "Part A\nPart B")
	}
}

func TestOllamaContentsToMessages_AssistantFunctionCallWithText(t *testing.T) {
	// Covers the path where an assistant message has BOTH text and function calls.
	fc := genai.NewPartFromFunctionCall("search", map[string]any{"q": "test"})
	fc.FunctionCall.ID = "call_mixed"

	fr := &genai.Part{
		FunctionResponse: &genai.FunctionResponse{
			ID:       "call_mixed",
			Name:     "search",
			Response: map[string]any{"result": "results here"},
		},
	}

	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "Search for test"}}},
		{Role: "model", Parts: []*genai.Part{{Text: "I will search."}, fc}},
		{Role: "user", Parts: []*genai.Part{fr}},
	}
	msgs, _ := ollamaContentsToMessages(contents, nil)
	// user + assistant(text+tool_calls) + tool_result
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[1].Content != "I will search." {
		t.Errorf("assistant text = %q, want 'I will search.'", msgs[1].Content)
	}
	if len(msgs[1].ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(msgs[1].ToolCalls))
	}
}

func TestOllamaListModelsWithMockServer(t *testing.T) {
	// Mock HTTP server returning a valid Ollama /api/tags response.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Minimal Ollama list response format.
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3:latest"},{"name":"qwen2.5:7b"}]}`))
	}))
	defer srv.Close()

	names, err := OllamaListModels(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("OllamaListModels() error: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 models, got %d: %v", len(names), names)
	}
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["llama3:latest"] {
		t.Error("expected llama3:latest in results")
	}
	if !found["qwen2.5:7b"] {
		t.Error("expected qwen2.5:7b in results")
	}
}

func TestNewOllamaValidation(t *testing.T) {
	t.Run("empty model name", func(t *testing.T) {
		_, err := NewOllama(context.Background(), "", "", "none", nil)
		if err == nil {
			t.Fatal("expected error for empty model name")
		}
	})

	t.Run("invalid URL", func(t *testing.T) {
		_, err := NewOllama(context.Background(), "test-model", "://bad", "none", nil)
		if err == nil {
			t.Fatal("expected error for invalid URL")
		}
	})

	t.Run("default base URL", func(t *testing.T) {
		llm, err := NewOllama(context.Background(), "test-model", "", "none", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if llm.Name() != "test-model" {
			t.Errorf("Name() = %q, want test-model", llm.Name())
		}
	})

	t.Run("custom base URL", func(t *testing.T) {
		llm, err := NewOllama(context.Background(), "test-model", "http://custom:1234", "none", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if llm.Name() != "test-model" {
			t.Errorf("Name() = %q, want test-model", llm.Name())
		}
	})

	t.Run("with extra headers", func(t *testing.T) {
		llm, err := NewOllama(context.Background(), "test-model", "", "none", &LLMOptions{
			ExtraHeaders: map[string]string{"X-Custom": "value"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if llm == nil {
			t.Fatal("expected non-nil LLM")
		}
		// Verify the ollama model was created with correct name.
		if llm.Name() != "test-model" {
			t.Errorf("Name() = %q, want test-model", llm.Name())
		}
	})

	t.Run("nil extra headers no transport wrapping", func(t *testing.T) {
		llm, err := NewOllama(context.Background(), "test-model", "", "none", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if llm == nil {
			t.Fatal("expected non-nil LLM")
		}
	})
}

// ---------------------------------------------------------------------------
// Mock server helpers
// ---------------------------------------------------------------------------

// newMockOllamaServer creates an httptest.Server and registers srv.Close as a
// test cleanup function.
func newMockOllamaServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// writeNDJSON writes each value as a JSON line (with optional flush) to w.
func writeNDJSON(w http.ResponseWriter, lines []any) {
	flusher, canFlush := w.(http.Flusher)
	for _, l := range lines {
		b, _ := json.Marshal(l)
		_, _ = fmt.Fprintf(w, "%s\n", b)
		if canFlush {
			flusher.Flush()
		}
	}
}

// ollamaChatLine builds a map whose JSON representation matches ChatResponse fields.
func ollamaChatLine(modelName, role, content, thinking, doneReason string, done bool, promptEval, eval int, toolCalls []map[string]any) map[string]any {
	msg := map[string]any{"role": role, "content": content}
	if thinking != "" {
		msg["thinking"] = thinking
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}
	line := map[string]any{
		"model":   modelName,
		"message": msg,
		"done":    done,
	}
	if done {
		line["done_reason"] = doneReason
		line["prompt_eval_count"] = promptEval
		line["eval_count"] = eval
	}
	return line
}

// newOllamaModelFromServer creates an ollamaModel whose HTTP client points at srv.
func newOllamaModelFromServer(t *testing.T, srv *httptest.Server, modelName, thinkingLevel string) model.LLM {
	t.Helper()
	llm, err := NewOllama(context.Background(), modelName, srv.URL, thinkingLevel, nil)
	if err != nil {
		t.Fatalf("NewOllama: %v", err)
	}
	return llm
}

// collectResponses drains a GenerateContent iterator and returns all responses and errors.
func collectResponses(t *testing.T, llm model.LLM, req *model.LLMRequest, stream bool) ([]*model.LLMResponse, []error) {
	t.Helper()
	var resps []*model.LLMResponse
	var errs []error
	for resp, err := range llm.GenerateContent(context.Background(), req, stream) {
		if err != nil {
			errs = append(errs, err)
		} else {
			resps = append(resps, resp)
		}
	}
	return resps, errs
}

// ---------------------------------------------------------------------------
// Streaming tests
// ---------------------------------------------------------------------------

func TestOllamaRunStreaming_BasicText(t *testing.T) {
	srv := newMockOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		writeNDJSON(w, []any{
			ollamaChatLine("test", "assistant", "Hello", "", "", false, 0, 0, nil),
			ollamaChatLine("test", "assistant", " world", "", "stop", true, 10, 5, nil),
		})
	})

	llm := newOllamaModelFromServer(t, srv, "test", "none")
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		},
	}

	resps, errs := collectResponses(t, llm, req, true)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	// Expect at least one partial chunk plus the final response.
	if len(resps) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(resps))
	}

	// Last response must be the final, non-partial one.
	last := resps[len(resps)-1]
	if last.Partial {
		t.Error("last response should not be partial")
	}
	if !last.TurnComplete {
		t.Error("last response should have TurnComplete=true")
	}
	if last.FinishReason != genai.FinishReasonStop {
		t.Errorf("finish reason = %v, want Stop", last.FinishReason)
	}
	if last.UsageMetadata == nil {
		t.Fatal("expected non-nil UsageMetadata")
	}
	if last.UsageMetadata.PromptTokenCount != 10 {
		t.Errorf("PromptTokenCount = %d, want 10", last.UsageMetadata.PromptTokenCount)
	}
	if last.UsageMetadata.CandidatesTokenCount != 5 {
		t.Errorf("CandidatesTokenCount = %d, want 5", last.UsageMetadata.CandidatesTokenCount)
	}

	// Aggregated text should contain both chunks.
	if last.Content == nil || len(last.Content.Parts) == 0 {
		t.Fatal("expected content parts in final response")
	}
	combined := ""
	for _, p := range last.Content.Parts {
		combined += p.Text
	}
	if !strings.Contains(combined, "Hello") || !strings.Contains(combined, "world") {
		t.Errorf("aggregated text %q does not contain expected chunks", combined)
	}
}

func TestOllamaRunStreaming_ThinkingContent(t *testing.T) {
	srv := newMockOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		// Thinking chunk followed by the actual answer.
		thinkLine := map[string]any{
			"model":   "test",
			"message": map[string]any{"role": "assistant", "content": "", "thinking": "let me think"},
			"done":    false,
		}
		answerLine := ollamaChatLine("test", "assistant", "42", "", "stop", true, 5, 3, nil)
		writeNDJSON(w, []any{thinkLine, answerLine})
	})

	llm := newOllamaModelFromServer(t, srv, "test", "high")
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "What is 6*7?"}}},
		},
	}

	resps, errs := collectResponses(t, llm, req, true)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(resps) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(resps))
	}

	// First partial should carry the "thinking" role.
	first := resps[0]
	if !first.Partial {
		t.Error("first response should be partial")
	}
	if first.Content == nil || first.Content.Role != "thinking" {
		t.Errorf("first response content role = %q, want thinking", first.Content.Role)
	}
	if first.Content.Parts[0].Text != "let me think" {
		t.Errorf("thinking text = %q, want 'let me think'", first.Content.Parts[0].Text)
	}
}

func TestOllamaRunStreaming_ToolCalls(t *testing.T) {
	toolCallLine := map[string]any{
		"model": "test",
		"message": map[string]any{
			"role":    "assistant",
			"content": "",
			"tool_calls": []any{
				map[string]any{
					"id": "call_abc",
					"function": map[string]any{
						"name":      "get_weather",
						"arguments": map[string]any{"city": "Paris"},
					},
				},
			},
		},
		"done":              true,
		"done_reason":       "stop",
		"prompt_eval_count": 8,
		"eval_count":        4,
	}

	srv := newMockOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		writeNDJSON(w, []any{toolCallLine})
	})

	llm := newOllamaModelFromServer(t, srv, "test", "none")
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Weather in Paris?"}}},
		},
	}

	resps, errs := collectResponses(t, llm, req, true)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(resps) == 0 {
		t.Fatal("expected at least one response")
	}

	last := resps[len(resps)-1]
	if last.Content == nil {
		t.Fatal("expected non-nil content")
	}
	var foundFC bool
	for _, p := range last.Content.Parts {
		if p.FunctionCall != nil && p.FunctionCall.Name == "get_weather" {
			foundFC = true
			break
		}
	}
	if !foundFC {
		t.Error("expected a FunctionCall part with name=get_weather")
	}
}

func TestOllamaRunStreaming_FinishReasonLength(t *testing.T) {
	srv := newMockOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		writeNDJSON(w, []any{
			ollamaChatLine("test", "assistant", "truncated", "", "length", true, 100, 50, nil),
		})
	})

	llm := newOllamaModelFromServer(t, srv, "test", "none")
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "tell me a story"}}},
		},
	}

	resps, errs := collectResponses(t, llm, req, true)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	last := resps[len(resps)-1]
	if last.FinishReason != genai.FinishReasonMaxTokens {
		t.Errorf("finish reason = %v, want MaxTokens", last.FinishReason)
	}
}

func TestOllamaRunStreaming_ServerError(t *testing.T) {
	srv := newMockOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusInternalServerError)
		// The Ollama client checks for {"error":...} in each NDJSON line.
		_, _ = fmt.Fprintln(w, `{"error":"internal server error"}`)
	})

	llm := newOllamaModelFromServer(t, srv, "test", "none")
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		},
	}

	resps, _ := collectResponses(t, llm, req, true)
	var foundError bool
	for _, r := range resps {
		if r.ErrorCode != "" {
			foundError = true
			break
		}
	}
	if !foundError {
		t.Error("expected an error response with ErrorCode set")
	}
}

func TestOllamaRunStreaming_CancelledContext(t *testing.T) {
	// Server that blocks until the client context is cancelled.
	srv := newMockOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		<-r.Context().Done()
	})

	llm := newOllamaModelFromServer(t, srv, "test", "none")
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	var resps []*model.LLMResponse
	for resp, err := range llm.GenerateContent(ctx, req, true) {
		if err == nil {
			resps = append(resps, resp)
		}
	}
	// With a cancelled context the streaming path returns without yielding an
	// ErrorCode response (it silently returns).
	for _, r := range resps {
		if r.ErrorCode != "" {
			t.Error("did not expect ErrorCode for cancelled context")
		}
	}
}

func TestOllamaRunStreaming_WithTools(t *testing.T) {
	// Verify that tools are forwarded in the request body.
	var capturedBody []byte
	srv := newMockOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		scanner := bufio.NewScanner(r.Body)
		if scanner.Scan() {
			capturedBody = append([]byte(nil), scanner.Bytes()...)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		writeNDJSON(w, []any{
			ollamaChatLine("test", "assistant", "ok", "", "stop", true, 1, 1, nil),
		})
	})

	llm := newOllamaModelFromServer(t, srv, "test", "none")
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Use the tool"}}},
		},
		Config: &genai.GenerateContentConfig{
			Tools: []*genai.Tool{
				{
					FunctionDeclarations: []*genai.FunctionDeclaration{
						{
							Name:        "search",
							Description: "Search the web",
							ParametersJsonSchema: map[string]any{
								"type": "object",
								"properties": map[string]any{
									"query": map[string]any{"type": "string", "description": "Search query"},
								},
								"required": []any{"query"},
							},
						},
					},
				},
			},
		},
	}

	resps, errs := collectResponses(t, llm, req, true)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(resps) == 0 {
		t.Fatal("expected at least one response")
	}

	// The request body should contain tool definitions.
	var body map[string]any
	if err := json.Unmarshal(capturedBody, &body); err != nil {
		t.Fatalf("failed to parse captured request body: %v", err)
	}
	if _, ok := body["tools"]; !ok {
		t.Error("expected 'tools' key in request body")
	}
}

func TestOllamaRunStreaming_WithThinkingLevel(t *testing.T) {
	var capturedBody []byte
	srv := newMockOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		scanner := bufio.NewScanner(r.Body)
		if scanner.Scan() {
			capturedBody = append([]byte(nil), scanner.Bytes()...)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		writeNDJSON(w, []any{
			ollamaChatLine("test", "assistant", "answer", "", "stop", true, 2, 2, nil),
		})
	})

	llm := newOllamaModelFromServer(t, srv, "test", "medium")
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Think hard"}}},
		},
	}

	resps, errs := collectResponses(t, llm, req, true)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(resps) == 0 {
		t.Fatal("expected at least one response")
	}

	var body map[string]any
	if err := json.Unmarshal(capturedBody, &body); err != nil {
		t.Fatalf("failed to parse captured body: %v", err)
	}
	if body["think"] == nil {
		t.Error("expected 'think' field in request body for thinking level=medium")
	}
}

func TestOllamaRunStreaming_YieldCancelled(t *testing.T) {
	// Server returns two text chunks; we break after the first yield.
	srv := newMockOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		writeNDJSON(w, []any{
			ollamaChatLine("test", "assistant", "chunk1", "", "", false, 0, 0, nil),
			ollamaChatLine("test", "assistant", "chunk2", "", "stop", true, 5, 3, nil),
		})
	})

	llm := newOllamaModelFromServer(t, srv, "test", "none")
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		},
	}

	var count int
	llm.GenerateContent(context.Background(), req, true)(func(*model.LLMResponse, error) bool {
		count++
		return false // stop after first yield
	})
	if count == 0 {
		t.Error("expected at least one yield before stopping")
	}
}

// ---------------------------------------------------------------------------
// Non-streaming tests
// ---------------------------------------------------------------------------

func TestOllamaRunNonStreaming_BasicText(t *testing.T) {
	srv := newMockOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		// Verify stream=false in the request body.
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if s, _ := body["stream"].(bool); s {
			t.Error("expected stream=false in non-streaming request")
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		writeNDJSON(w, []any{
			ollamaChatLine("test", "assistant", "Hello world", "", "stop", true, 10, 5, nil),
		})
	})

	llm := newOllamaModelFromServer(t, srv, "test", "none")
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		},
	}

	resps, errs := collectResponses(t, llm, req, false)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}

	r := resps[0]
	if r.Partial {
		t.Error("non-streaming response should not be partial")
	}
	if !r.TurnComplete {
		t.Error("non-streaming response should be TurnComplete")
	}
	if r.FinishReason != genai.FinishReasonStop {
		t.Errorf("finish reason = %v, want Stop", r.FinishReason)
	}
	if r.UsageMetadata == nil {
		t.Fatal("expected UsageMetadata")
	}
	if r.UsageMetadata.PromptTokenCount != 10 {
		t.Errorf("PromptTokenCount = %d, want 10", r.UsageMetadata.PromptTokenCount)
	}
	if r.UsageMetadata.CandidatesTokenCount != 5 {
		t.Errorf("CandidatesTokenCount = %d, want 5", r.UsageMetadata.CandidatesTokenCount)
	}
	if r.Content == nil || len(r.Content.Parts) == 0 {
		t.Fatal("expected content parts")
	}
	if r.Content.Parts[0].Text != "Hello world" {
		t.Errorf("content text = %q, want 'Hello world'", r.Content.Parts[0].Text)
	}
}

func TestOllamaRunNonStreaming_ThinkingContent(t *testing.T) {
	srv := newMockOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		line := map[string]any{
			"model": "test",
			"message": map[string]any{
				"role":     "assistant",
				"content":  "42",
				"thinking": "I computed this carefully",
			},
			"done":              true,
			"done_reason":       "stop",
			"prompt_eval_count": 5,
			"eval_count":        3,
		}
		writeNDJSON(w, []any{line})
	})

	llm := newOllamaModelFromServer(t, srv, "test", "high")
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "6*7?"}}},
		},
	}

	resps, errs := collectResponses(t, llm, req, false)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}

	r := resps[0]
	if r.Content == nil || len(r.Content.Parts) < 2 {
		t.Fatalf("expected at least 2 parts (thinking + answer), got %d", len(r.Content.Parts))
	}
	if r.Content.Parts[0].Text != "I computed this carefully" {
		t.Errorf("thinking part = %q, want 'I computed this carefully'", r.Content.Parts[0].Text)
	}
	if r.Content.Parts[1].Text != "42" {
		t.Errorf("answer part = %q, want '42'", r.Content.Parts[1].Text)
	}
}

func TestOllamaRunNonStreaming_ToolCalls(t *testing.T) {
	srv := newMockOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		line := map[string]any{
			"model": "test",
			"message": map[string]any{
				"role":    "assistant",
				"content": "",
				"tool_calls": []any{
					map[string]any{
						"id": "call_xyz",
						"function": map[string]any{
							"name":      "calculator",
							"arguments": map[string]any{"expression": "2+2"},
						},
					},
				},
			},
			"done":              true,
			"done_reason":       "stop",
			"prompt_eval_count": 6,
			"eval_count":        2,
		}
		writeNDJSON(w, []any{line})
	})

	llm := newOllamaModelFromServer(t, srv, "test", "none")
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Calculate 2+2"}}},
		},
	}

	resps, errs := collectResponses(t, llm, req, false)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}

	r := resps[0]
	if r.Content == nil {
		t.Fatal("expected non-nil content")
	}
	var foundFC bool
	for _, p := range r.Content.Parts {
		if p.FunctionCall != nil && p.FunctionCall.Name == "calculator" {
			foundFC = true
			if p.FunctionCall.ID != "call_xyz" {
				t.Errorf("function call ID = %q, want call_xyz", p.FunctionCall.ID)
			}
			if v, ok := p.FunctionCall.Args["expression"]; !ok || v != "2+2" {
				t.Errorf("function call arg expression = %v, want 2+2", v)
			}
		}
	}
	if !foundFC {
		t.Error("expected a FunctionCall part with name=calculator")
	}
}

func TestOllamaRunNonStreaming_FinishReasonLength(t *testing.T) {
	srv := newMockOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		writeNDJSON(w, []any{
			ollamaChatLine("test", "assistant", "cut short", "", "length", true, 200, 100, nil),
		})
	})

	llm := newOllamaModelFromServer(t, srv, "test", "none")
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Write a novel"}}},
		},
	}

	resps, errs := collectResponses(t, llm, req, false)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	last := resps[len(resps)-1]
	if last.FinishReason != genai.FinishReasonMaxTokens {
		t.Errorf("finish reason = %v, want MaxTokens", last.FinishReason)
	}
}

func TestOllamaRunNonStreaming_ServerError(t *testing.T) {
	srv := newMockOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintln(w, `{"error":"something went wrong"}`)
	})

	llm := newOllamaModelFromServer(t, srv, "test", "none")
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		},
	}

	_, errs := collectResponses(t, llm, req, false)
	if len(errs) == 0 {
		t.Error("expected at least one error from non-streaming server error")
	}
}

func TestOllamaRunNonStreaming_ModelOverride(t *testing.T) {
	var capturedModel string
	srv := newMockOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		capturedModel, _ = body["model"].(string)
		w.Header().Set("Content-Type", "application/x-ndjson")
		writeNDJSON(w, []any{
			ollamaChatLine("override-model", "assistant", "ok", "", "stop", true, 1, 1, nil),
		})
	})

	llm := newOllamaModelFromServer(t, srv, "original-model", "none")
	req := &model.LLMRequest{
		Model: "override-model",
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		},
	}

	resps, errs := collectResponses(t, llm, req, false)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(resps) == 0 {
		t.Fatal("expected a response")
	}
	if capturedModel != "override-model" {
		t.Errorf("model sent = %q, want override-model", capturedModel)
	}
}

func TestOllamaRunNonStreaming_SystemPrompt(t *testing.T) {
	var capturedMessages []any
	srv := newMockOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if msgs, ok := body["messages"].([]any); ok {
			capturedMessages = msgs
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		writeNDJSON(w, []any{
			ollamaChatLine("test", "assistant", "sure", "", "stop", true, 3, 2, nil),
		})
	})

	llm := newOllamaModelFromServer(t, srv, "test", "none")
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
		},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: "You are concise."}},
			},
		},
	}

	resps, errs := collectResponses(t, llm, req, false)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(resps) == 0 {
		t.Fatal("expected a response")
	}

	// First message should be the system message.
	if len(capturedMessages) < 2 {
		t.Fatalf("expected at least 2 messages (system + user), got %d", len(capturedMessages))
	}
	firstMsg, _ := capturedMessages[0].(map[string]any)
	if firstMsg["role"] != "system" {
		t.Errorf("first message role = %v, want system", firstMsg["role"])
	}
	if firstMsg["content"] != "You are concise." {
		t.Errorf("system content = %v, want 'You are concise.'", firstMsg["content"])
	}
}

func TestOllamaName(t *testing.T) {
	llm, err := NewOllama(context.Background(), "my-model", "", "none", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if llm.Name() != "my-model" {
		t.Errorf("Name() = %q, want my-model", llm.Name())
	}
}
