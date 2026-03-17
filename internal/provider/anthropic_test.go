package provider

import (
	"context"
	"testing"

	"google.golang.org/adk/model"

	"github.com/anthropics/anthropic-sdk-go"
	"google.golang.org/genai"
)

func TestAntStopReasonToGenai(t *testing.T) {
	tests := []struct {
		reason anthropic.StopReason
		want   genai.FinishReason
	}{
		{anthropic.StopReasonEndTurn, genai.FinishReasonStop},
		{anthropic.StopReasonMaxTokens, genai.FinishReasonMaxTokens},
		{anthropic.StopReasonToolUse, genai.FinishReasonStop},
		{anthropic.StopReason("unknown"), genai.FinishReasonStop}, // default case
	}
	for _, tt := range tests {
		t.Run(string(tt.reason), func(t *testing.T) {
			got := antStopReasonToGenai(tt.reason)
			if got != tt.want {
				t.Errorf("antStopReasonToGenai(%q) = %v, want %v", tt.reason, got, tt.want)
			}
		})
	}
}

func TestAntContentsToMessages(t *testing.T) {
	t.Run("extracts system prompt", func(t *testing.T) {
		config := &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: "You are a coding agent."}},
			},
		}
		contents := []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
		}

		msgs, sysPrompt := antContentsToMessages(contents, config)
		if sysPrompt != "You are a coding agent." {
			t.Errorf("system prompt = %q, want %q", sysPrompt, "You are a coding agent.")
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

		msgs, _ := antContentsToMessages(contents, nil)
		if len(msgs) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(msgs))
		}
		if msgs[0].Role != anthropic.MessageParamRoleUser {
			t.Errorf("first message role = %q, want user", msgs[0].Role)
		}
		if msgs[1].Role != anthropic.MessageParamRoleAssistant {
			t.Errorf("second message role = %q, want assistant", msgs[1].Role)
		}
	})

	t.Run("handles function calls with tool results", func(t *testing.T) {
		fc := genai.NewPartFromFunctionCall("bash", map[string]any{"command": "ls"})
		fc.FunctionCall.ID = "tool_abc"

		fr := &genai.Part{
			FunctionResponse: &genai.FunctionResponse{
				ID:       "tool_abc",
				Name:     "bash",
				Response: map[string]any{"result": "file1.go\nfile2.go"},
			},
		}

		contents := []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "List files"}}},
			{Role: "model", Parts: []*genai.Part{fc}},
			{Role: "user", Parts: []*genai.Part{fr}},
		}

		msgs, _ := antContentsToMessages(contents, nil)
		// user + assistant(tool_use) + user(tool_result)
		if len(msgs) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(msgs))
		}
		if msgs[1].Role != anthropic.MessageParamRoleAssistant {
			t.Errorf("assistant message role = %q", msgs[1].Role)
		}
		if msgs[2].Role != anthropic.MessageParamRoleUser {
			t.Errorf("tool result message role = %q, want user", msgs[2].Role)
		}
	})

	t.Run("skips system role contents", func(t *testing.T) {
		contents := []*genai.Content{
			{Role: "system", Parts: []*genai.Part{{Text: "ignored"}}},
			{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
		}

		msgs, _ := antContentsToMessages(contents, nil)
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
	})

	t.Run("handles nil parts", func(t *testing.T) {
		contents := []*genai.Content{
			{Role: "user", Parts: []*genai.Part{nil}},
		}
		msgs, _ := antContentsToMessages(contents, nil)
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
	})

	t.Run("handles empty text parts", func(t *testing.T) {
		contents := []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: ""}}},
		}
		msgs, _ := antContentsToMessages(contents, nil)
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
	})

	t.Run("nil config is handled", func(t *testing.T) {
		contents := []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
		}
		msgs, sysPrompt := antContentsToMessages(contents, nil)
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
		if sysPrompt != "" {
			t.Errorf("expected empty system prompt, got %q", sysPrompt)
		}
	})
}

func TestAntGenaiToolsToAnthropic(t *testing.T) {
	t.Run("basic tools", func(t *testing.T) {
		tools := []*genai.Tool{
			{
				FunctionDeclarations: []*genai.FunctionDeclaration{
					{
						Name:        "read_file",
						Description: "Read a file from disk",
						ParametersJsonSchema: map[string]any{
							"type": "object",
							"properties": map[string]any{
								"path": map[string]any{"type": "string", "description": "File path"},
							},
							"required": []any{"path"},
						},
					},
					{
						Name:        "bash",
						Description: "Execute shell command",
						ParametersJsonSchema: map[string]any{
							"type": "object",
							"properties": map[string]any{
								"command": map[string]any{"type": "string"},
							},
							"required": []any{"command"},
						},
					},
				},
			},
		}

		result := antGenaiToolsToAnthropic(tools)
		if len(result) != 2 {
			t.Fatalf("expected 2 tools, got %d", len(result))
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
		result := antGenaiToolsToAnthropic(tools)
		if len(result) != 1 {
			t.Fatalf("expected 1 tool, got %d", len(result))
		}
	})

	t.Run("required fields extraction", func(t *testing.T) {
		tools := []*genai.Tool{
			{
				FunctionDeclarations: []*genai.FunctionDeclaration{
					{
						Name:        "test",
						Description: "Test",
						ParametersJsonSchema: map[string]any{
							"type": "object",
							"properties": map[string]any{
								"arg1": map[string]any{"type": "string"},
							},
							"required": []any{"arg1", "arg2"}, // arg2 not in properties
						},
					},
				},
			},
		}
		result := antGenaiToolsToAnthropic(tools)
		if len(result) != 1 {
			t.Fatalf("expected 1 tool, got %d", len(result))
		}
	})
}

func TestNewLLMFactory(t *testing.T) {
	t.Run("unsupported provider", func(t *testing.T) {
		_, err := NewLLM(nil, Info{Provider: "unknown", Model: "test"}, "key", "", "")
		if err == nil {
			t.Fatal("expected error for unsupported provider")
		}
	})

	t.Run("openai requires key", func(t *testing.T) {
		_, err := NewOpenAI(nil, "gpt-4o", "", "")
		if err == nil {
			t.Fatal("expected error for empty API key")
		}
	})

	t.Run("anthropic requires key without baseURL", func(t *testing.T) {
		_, err := NewAnthropic(nil, "claude-sonnet-4-6", "", "", "")
		if err == nil {
			t.Fatal("expected error for empty API key")
		}
	})

	t.Run("anthropic allows empty key with baseURL", func(t *testing.T) {
		llm, err := NewAnthropic(nil, "qwen2.5", "", "http://localhost:11434", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if llm.Name() != "qwen2.5" {
			t.Errorf("model name = %q, want %q", llm.Name(), "qwen2.5")
		}
	})
}

func TestResolveCloudSuffix(t *testing.T) {
	t.Run("cloud suffix routes to anthropic", func(t *testing.T) {
		info, err := Resolve("qwen2.5:cloud")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Provider != "anthropic" {
			t.Errorf("provider = %q, want %q", info.Provider, "anthropic")
		}
		if info.Model != "qwen2.5:cloud" {
			t.Errorf("model = %q, want %q", info.Model, "qwen2.5:cloud")
		}
	})
}

func TestAntThinkingConfig(t *testing.T) {
	tests := []struct {
		level   string
		wantNil bool
	}{
		{"none", true},
		{"", true},
		{"unknown", true},
		{"low", false},
		{"medium", false},
		{"high", false},
	}
	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			got := antThinkingConfig(tt.level)
			if tt.wantNil {
				if got != nil {
					t.Errorf("antThinkingConfig(%q) = %v, want nil", tt.level, got)
				}
				return
			}
			if got == nil {
				t.Errorf("antThinkingConfig(%q) = nil, want non-nil", tt.level)
			}
		})
	}
}

func TestAnthropicGenerateContentErrors(t *testing.T) {
	// Test with invalid API key to trigger error path
	llm, err := NewAnthropic(context.Background(), "claude-sonnet-4-6", "test-key-invalid", "", "")
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	t.Run("empty contents", func(t *testing.T) {
		req := &model.LLMRequest{
			Contents: []*genai.Content{},
		}
		seq := llm.GenerateContent(context.Background(), req, false)
		// Consume the sequence to trigger the execution
		for resp, err := range seq {
			if err != nil {
				// Expected - no valid content to process
				return
			}
			_ = resp // result may be nil or empty
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

func TestAnthropicGenerateContentStreaming(t *testing.T) {
	// Test streaming mode (will fail with invalid key, but exercises the code path)
	llm, err := NewAnthropic(context.Background(), "claude-sonnet-4-6", "test-key-invalid", "", "")
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		},
	}

	// Test streaming mode
	seq := llm.GenerateContent(context.Background(), req, true)
	for resp, err := range seq {
		if err != nil {
			// Expected - API will fail with invalid key
			return
		}
		_ = resp
	}
}

func TestAnthropicGenerateContentWithTools(t *testing.T) {
	// Test with tools configured
	llm, err := NewAnthropic(context.Background(), "claude-sonnet-4-6", "test-key-invalid", "", "")
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

func TestAnthropicGenerateContentWithModelOverride(t *testing.T) {
	// Test with model override in request
	llm, err := NewAnthropic(context.Background(), "claude-sonnet-4-6", "test-key-invalid", "", "")
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	req := &model.LLMRequest{
		Model: "claude-3-5-sonnet-20241022",
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

func TestAnthropicGenerateContentWithThinking(t *testing.T) {
	// Test with thinking enabled
	llm, err := NewAnthropic(context.Background(), "claude-sonnet-4-6", "test-key-invalid", "", "medium")
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	req := &model.LLMRequest{
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
