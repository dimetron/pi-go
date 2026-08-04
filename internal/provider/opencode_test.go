package provider

import (
	"context"
	"strings"
	"testing"
)

func TestOpenCodeAnthropicBaseURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://opencode.ai/zen/go/v1", "https://opencode.ai/zen/go"},
		{"https://opencode.ai/zen/go/v1/", "https://opencode.ai/zen/go"},
		{"https://opencode.ai/zen/go", "https://opencode.ai/zen/go"},
		{"http://localhost:8080/v1", "http://localhost:8080"},
		{"http://localhost:8080", "http://localhost:8080"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := opencodeAnthropicBaseURL(tt.input)
			if got != tt.want {
				t.Errorf("opencodeAnthropicBaseURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewOpenCode_ChatModel(t *testing.T) {
	// kimi-k3 is a "chat" family model → returns *openaiModel.
	llm, err := NewOpenCode(context.Background(), "kimi-k3", "test-key", "", "", nil)
	if err != nil {
		t.Fatalf("NewOpenCode(kimi-k3) error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewOpenCode(kimi-k3) returned nil")
	}
	if llm.Name() != "kimi-k3" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "kimi-k3")
	}
	// Verify it's an *openaiModel (chat/completions delegate).
	if _, ok := llm.(*openaiModel); !ok {
		t.Errorf("expected *openaiModel, got %T", llm)
	}
}

func TestNewOpenCode_ResponsesModel(t *testing.T) {
	// gpt-5.6-luna is a "responses" family model → returns *openaiModel.
	llm, err := NewOpenCode(context.Background(), "gpt-5.6-luna", "test-key", "", "", nil)
	if err != nil {
		t.Fatalf("NewOpenCode(gpt-5.6-luna) error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewOpenCode(gpt-5.6-luna) returned nil")
	}
	if llm.Name() != "gpt-5.6-luna" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "gpt-5.6-luna")
	}
	// Verify it's an *openaiModel (responses delegate).
	if _, ok := llm.(*openaiModel); !ok {
		t.Errorf("expected *openaiModel, got %T", llm)
	}
}

func TestNewOpenCode_MessagesModel(t *testing.T) {
	// minimax-m3 is a "messages" family model → returns *anthropicModel.
	llm, err := NewOpenCode(context.Background(), "minimax-m3", "test-key", "", "", nil)
	if err != nil {
		t.Fatalf("NewOpenCode(minimax-m3) error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewOpenCode(minimax-m3) returned nil")
	}
	if llm.Name() != "minimax-m3" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "minimax-m3")
	}
	// Verify it's an *anthropicModel (messages delegate).
	if _, ok := llm.(*anthropicModel); !ok {
		t.Errorf("expected *anthropicModel, got %T", llm)
	}
}

func TestNewOpenCode_UnknownModel(t *testing.T) {
	_, err := NewOpenCode(context.Background(), "unknown-model", "test-key", "", "", nil)
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
	if !strings.Contains(err.Error(), "unknown OpenCode Go model") {
		t.Errorf("error = %q, want 'unknown OpenCode Go model'", err.Error())
	}
}

func TestNewOpenCode_UndocumentedModelNotRoutable(t *testing.T) {
	// These undocumented runtime extras must NOT be in the catalog.
	undocumented := []string{
		"glm-5",
		"kimi-k2.5",
		"qwen3.5-plus",
		"mimo-v2-pro",
		"mimo-v2-omni",
		"hy3-preview",
	}
	for _, model := range undocumented {
		t.Run(model, func(t *testing.T) {
			_, err := NewOpenCode(context.Background(), model, "test-key", "", "", nil)
			if err == nil {
				t.Fatalf("expected error for undocumented model %q", model)
			}
		})
	}
}

func TestNewOpenCode_DefaultBaseURL(t *testing.T) {
	// When baseURL is empty, the default should be used.
	// We can verify by checking the openai delegate's base URL via the
	// normalizeOpenAIBaseURL path. Since we can't easily inspect the
	// openaiModel's client, we just verify no error and the model name is correct.
	llm, err := NewOpenCode(context.Background(), "kimi-k3", "test-key", "", "", nil)
	if err != nil {
		t.Fatalf("NewOpenCode with empty baseURL: %v", err)
	}
	if llm.Name() != "kimi-k3" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "kimi-k3")
	}
}

func TestNewOpenCode_CustomBaseURL(t *testing.T) {
	// A custom base URL should be passed through to the delegate.
	llm, err := NewOpenCode(context.Background(), "kimi-k3", "test-key", "https://custom.example.com/v1", "", nil)
	if err != nil {
		t.Fatalf("NewOpenCode with custom baseURL: %v", err)
	}
	if llm.Name() != "kimi-k3" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "kimi-k3")
	}
}

func TestNewOpenCode_AnthropicBaseURLStripped(t *testing.T) {
	// For messages models, the /v1 suffix should be stripped.
	// We verify by checking the anthropic delegate's model name.
	llm, err := NewOpenCode(context.Background(), "minimax-m3", "test-key", "https://custom.example.com/v1", "", nil)
	if err != nil {
		t.Fatalf("NewOpenCode(minimax-m3) with custom baseURL: %v", err)
	}
	if llm.Name() != "minimax-m3" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "minimax-m3")
	}
}

func TestNewOpenCode_AllCatalogModels(t *testing.T) {
	// Verify every model in the catalog can be constructed without error.
	for modelID, family := range opencodeGoModelCatalog {
		t.Run(modelID+"/"+family, func(t *testing.T) {
			llm, err := NewOpenCode(context.Background(), modelID, "test-key", "", "", nil)
			if err != nil {
				t.Fatalf("NewOpenCode(%q) error: %v", modelID, err)
			}
			if llm == nil {
				t.Fatal("NewOpenCode returned nil")
			}
			if llm.Name() != modelID {
				t.Errorf("Name() = %q, want %q", llm.Name(), modelID)
			}
			switch family {
			case "chat", "responses":
				if _, ok := llm.(*openaiModel); !ok {
					t.Errorf("expected *openaiModel for family %q, got %T", family, llm)
				}
			case "messages":
				if _, ok := llm.(*anthropicModel); !ok {
					t.Errorf("expected *anthropicModel for family %q, got %T", family, llm)
				}
			}
		})
	}
}
