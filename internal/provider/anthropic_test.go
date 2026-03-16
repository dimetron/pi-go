package provider

import (
	"testing"

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
}

func TestAntGenaiToolsToAnthropic(t *testing.T) {
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
		_, err := NewAnthropic(nil, "claude-sonnet-4-20250514", "", "", "")
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
		if !info.Ollama {
			t.Error("expected Ollama = true")
		}
	})

	t.Run("regular model unaffected", func(t *testing.T) {
		info, err := Resolve("claude-sonnet-4-20250514")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Ollama {
			t.Error("expected Ollama = false for regular model")
		}
	})
}
