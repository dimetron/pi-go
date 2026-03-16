package provider

import (
	"testing"

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
}

func TestOaiGenaiToolsToOpenAI(t *testing.T) {
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
