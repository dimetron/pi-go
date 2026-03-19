package provider

import (
	"context"
	"testing"

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
