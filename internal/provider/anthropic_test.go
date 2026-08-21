package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"

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
		_, err := NewLLM(context.TODO(), Info{Provider: "unknown", Model: "test"}, "key", "", "", nil)
		if err == nil {
			t.Fatal("expected error for unsupported provider")
		}
	})

	t.Run("openai requires key", func(t *testing.T) {
		_, err := NewOpenAI(context.TODO(), "gpt-4o", "", "", nil)
		if err == nil {
			t.Fatal("expected error for empty API key")
		}
	})

	t.Run("anthropic requires key without baseURL", func(t *testing.T) {
		_, err := NewAnthropic(context.TODO(), "claude-sonnet-4-6", "", "", "", nil)
		if err == nil {
			t.Fatal("expected error for empty API key")
		}
	})

	t.Run("anthropic allows empty key with baseURL", func(t *testing.T) {
		llm, err := NewAnthropic(context.TODO(), "qwen2.5", "", "http://localhost:11434", "", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if llm.Name() != "qwen2.5" {
			t.Errorf("model name = %q, want %q", llm.Name(), "qwen2.5")
		}
	})
}

func TestResolveCloudSuffix(t *testing.T) {
	t.Run("cloud suffix routes to ollama", func(t *testing.T) {
		info, err := Resolve("qwen2.5:cloud")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.Provider != "ollama" {
			t.Errorf("provider = %q, want %q", info.Provider, "ollama")
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
	llm, err := NewAnthropic(context.Background(), "claude-sonnet-4-6", "test-key-invalid", "", "", nil)
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
	llm, err := NewAnthropic(context.Background(), "claude-sonnet-4-6", "test-key-invalid", "", "", nil)
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
	llm, err := NewAnthropic(context.Background(), "claude-sonnet-4-6", "test-key-invalid", "", "", nil)
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
	llm, err := NewAnthropic(context.Background(), "claude-sonnet-4-6", "test-key-invalid", "", "", nil)
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
	llm, err := NewAnthropic(context.Background(), "claude-sonnet-4-6", "test-key-invalid", "", "medium", nil)
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

func TestAnthropicGenerateContentModelNameFallback(t *testing.T) {
	// When the model is named "anthropic" it should fall back to claude-sonnet-5.
	// We create the model with name "anthropic" so modelName == "anthropic" after no override.
	llm, err := NewAnthropic(context.Background(), "anthropic", "test-key-invalid", "", "", nil)
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
			// Expected - API will fail with invalid key
			return
		}
		_ = resp
	}
}

func TestAnthropicGenerateContentModelOverrideToAnthropic(t *testing.T) {
	// req.Model == "anthropic" should also trigger the fallback to "claude-sonnet-5".
	llm, err := NewAnthropic(context.Background(), "some-model", "test-key-invalid", "", "", nil)
	if err != nil {
		t.Fatalf("failed to create model: %v", err)
	}

	req := &model.LLMRequest{
		Model: "anthropic", // triggers the fallback branch
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

func TestAntContentsToMessagesAssistantRoleText(t *testing.T) {
	// Covers the "assistant" role keyword (as opposed to "model") for plain text.
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
		{Role: "assistant", Parts: []*genai.Part{{Text: "Hi there"}}},
	}
	msgs, _ := antContentsToMessages(contents, nil)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[1].Role != anthropic.MessageParamRoleAssistant {
		t.Errorf("second message role = %q, want assistant", msgs[1].Role)
	}
}

func TestAntContentsToMessagesAssistantRoleFunctionCall(t *testing.T) {
	// Covers the "assistant" keyword (not "model") for the function-call path.
	fc := genai.NewPartFromFunctionCall("bash", map[string]any{"command": "ls"})
	fc.FunctionCall.ID = "tool_xyz"

	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "List files"}}},
		{Role: "assistant", Parts: []*genai.Part{fc}},
	}

	msgs, _ := antContentsToMessages(contents, nil)
	// user + assistant(tool_use) + user(tool_result with default msg)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[1].Role != anthropic.MessageParamRoleAssistant {
		t.Errorf("assistant message role = %q, want assistant", msgs[1].Role)
	}
}

func TestNewAnthropicWithExtraHeaders(t *testing.T) {
	llm, err := NewAnthropic(context.Background(), "claude-sonnet-4-6", "test-key", "", "", &LLMOptions{
		ExtraHeaders: map[string]string{
			"X-Custom-Header": "value1",
			"X-Org-ID":        "org-456",
		},
	})
	if err != nil {
		t.Fatalf("NewAnthropic() with extra headers error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewAnthropic() returned nil")
	}
	if llm.Name() != "claude-sonnet-4-6" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "claude-sonnet-4-6")
	}
}

func TestNewAnthropicWithInsecureTLS(t *testing.T) {
	llm, err := NewAnthropic(context.Background(), "claude-sonnet-4-6", "test-key", "", "", &LLMOptions{
		InsecureSkipTLS: true,
	})
	if err != nil {
		t.Fatalf("NewAnthropic() with InsecureSkipTLS error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewAnthropic() returned nil")
	}
}

func TestNewAnthropicWithBaseURLAndKey(t *testing.T) {
	// Both apiKey and baseURL set - exercises both option branches.
	llm, err := NewAnthropic(context.Background(), "custom-model", "test-key", "http://localhost:8080", "low", nil)
	if err != nil {
		t.Fatalf("NewAnthropic() with baseURL+key error: %v", err)
	}
	if llm.Name() != "custom-model" {
		t.Errorf("Name() = %q, want custom-model", llm.Name())
	}
}

func TestNewAnthropicWithOAuthTokenUsesBearerHeaders(t *testing.T) {
	var sawRequest bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if auth := r.Header.Get("Authorization"); auth != "Bearer sk-ant-oat-test" {
			t.Errorf("Authorization = %q, want bearer OAuth token", auth)
		}
		if apiKey := r.Header.Get("X-Api-Key"); apiKey != "" {
			t.Errorf("X-Api-Key = %q, want empty for OAuth token", apiKey)
		}
		if beta := r.Header.Get("anthropic-beta"); !strings.Contains(beta, "oauth-2025-04-20") {
			t.Errorf("anthropic-beta = %q, want oauth beta", beta)
		}
		if app := r.Header.Get("x-app"); app != "cli" {
			t.Errorf("x-app = %q, want cli", app)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":            "msg_123",
			"type":          "message",
			"role":          "assistant",
			"model":         "claude-sonnet-4-6",
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  1,
				"output_tokens": 1,
			},
			"content": []map[string]any{
				{"type": "text", "text": "Hello world"},
			},
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	llm, err := NewAnthropic(ctx, "claude-sonnet-4-6", "sk-ant-oat-test", srv.URL, "none", nil)
	if err != nil {
		t.Fatalf("NewAnthropic() error: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Say hello"}}},
		},
	}
	for _, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			t.Fatalf("GenerateContent() error: %v", err)
		}
	}
	if !sawRequest {
		t.Fatal("expected server request")
	}
}

// TestAntContentsToMessagesEmptyFallback verifies that when no messages are
// produced (e.g. only nil contents) a default user "Hello" message is injected.
func TestAntContentsToMessagesEmptyFallback(t *testing.T) {
	// All nil contents → no messages produced → fallback to default "Hello" message.
	msgs, sysPrompt := antContentsToMessages([]*genai.Content{nil, nil}, nil)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 fallback message, got %d", len(msgs))
	}
	if msgs[0].Role != anthropic.MessageParamRoleUser {
		t.Errorf("fallback message role = %q, want user", msgs[0].Role)
	}
	if sysPrompt != "" {
		t.Errorf("expected empty system prompt, got %q", sysPrompt)
	}
}

// TestAntContentsToMessagesMultipleSystemParts verifies that multiple text
// parts in the system instruction are concatenated into one system prompt.
func TestAntContentsToMessagesMultipleSystemParts(t *testing.T) {
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
		{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
	}

	_, sysPrompt := antContentsToMessages(contents, config)
	if sysPrompt != "Part one.\nPart two." {
		t.Errorf("system prompt = %q, want %q", sysPrompt, "Part one.\nPart two.")
	}
}

// TestAntContentsToMessagesAssistantFunctionCallWithText verifies that an
// assistant message with BOTH text and function calls is handled correctly:
// a text block is prepended before the tool_use blocks.
func TestAntContentsToMessagesAssistantFunctionCallWithText(t *testing.T) {
	fc := genai.NewPartFromFunctionCall("search", map[string]any{"q": "go lang"})
	fc.FunctionCall.ID = "call_text_fc"

	fr := &genai.Part{
		FunctionResponse: &genai.FunctionResponse{
			ID:       "call_text_fc",
			Name:     "search",
			Response: map[string]any{"result": "results here"},
		},
	}

	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "Search for Go"}}},
		{Role: "model", Parts: []*genai.Part{{Text: "I will search."}, fc}},
		{Role: "user", Parts: []*genai.Part{fr}},
	}

	msgs, _ := antContentsToMessages(contents, nil)
	// user + assistant(text+tool_use) + user(tool_result)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[1].Role != anthropic.MessageParamRoleAssistant {
		t.Errorf("assistant message role = %q, want assistant", msgs[1].Role)
	}
}

// TestAntContentsToMessagesFunctionResponseContentPaths exercises the
// oaiFunctionResponseContent helper via antContentsToMessages to ensure the
// content string extraction works for the map-with-result path.
func TestAntContentsToMessagesFunctionResponseContentPaths(t *testing.T) {
	fc := genai.NewPartFromFunctionCall("bash", map[string]any{"cmd": "ls"})
	fc.FunctionCall.ID = "call_resp_path"

	// Response using the "result" key path in oaiFunctionResponseContent.
	fr := &genai.Part{
		FunctionResponse: &genai.FunctionResponse{
			ID:       "call_resp_path",
			Name:     "bash",
			Response: map[string]any{"result": "output text"},
		},
	}

	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "run ls"}}},
		{Role: "model", Parts: []*genai.Part{fc}},
		{Role: "user", Parts: []*genai.Part{fr}},
	}

	msgs, _ := antContentsToMessages(contents, nil)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d: %v", len(msgs), msgs)
	}
}

func TestAnthropicNonStreamingTextResponse(t *testing.T) {
	// Mock server that returns a successful Anthropic message response.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		body := map[string]any{
			"id":   "msg_test",
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{
				{"type": "text", "text": "Hello world"},
			},
			"model":       "claude-sonnet-4-6",
			"stop_reason": "end_turn",
			"usage": map[string]any{
				"input_tokens":  10,
				"output_tokens": 5,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	ctx := context.Background()
	llm, err := NewAnthropic(ctx, "claude-sonnet-4-6", "sk-test", srv.URL, "none", nil)
	if err != nil {
		t.Fatalf("NewAnthropic() error: %v", err)
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
		t.Errorf("input tokens = %d, want 10", final.UsageMetadata.PromptTokenCount)
	}
	if final.UsageMetadata.CandidatesTokenCount != 5 {
		t.Errorf("output tokens = %d, want 5", final.UsageMetadata.CandidatesTokenCount)
	}
}

func TestAnthropicNonStreamingToolCallResponse(t *testing.T) {
	// Mock server that returns a tool_use block in the response.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{
			"id":   "msg_tool_test",
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{
				{
					"type":  "tool_use",
					"id":    "toolu_abc123",
					"name":  "get_weather",
					"input": map[string]any{"location": "San Francisco"},
				},
			},
			"model":       "claude-sonnet-4-6",
			"stop_reason": "tool_use",
			"usage": map[string]any{
				"input_tokens":  15,
				"output_tokens": 20,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	ctx := context.Background()
	llm, err := NewAnthropic(ctx, "claude-sonnet-4-6", "sk-test", srv.URL, "none", nil)
	if err != nil {
		t.Fatalf("NewAnthropic() error: %v", err)
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
	} else {
		fc := fcPart.FunctionCall
		if got := fc.Name; got != "get_weather" {
			t.Errorf("function name = %q, want get_weather", got)
		}
		if fc.ID != "toolu_abc123" {
			t.Errorf("function call ID = %q, want toolu_abc123", fc.ID)
		}
		loc, _ := fc.Args["location"].(string)
		if loc != "San Francisco" {
			t.Errorf("location arg = %q, want San Francisco", loc)
		}
	}
}

func TestBuildAntFinalResponse_TextOnly(t *testing.T) {
	s := &antStreamState{
		text:         "Hello world",
		toolUse:      map[int]antToolUseAcc{},
		stopReason:   anthropic.StopReasonEndTurn,
		inputTokens:  10,
		outputTokens: 5,
	}
	resp := buildAntFinalResponse(s)

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
		t.Errorf("text = %q, want %q", resp.Content.Parts[0].Text, "Hello world")
	}
	if resp.UsageMetadata.PromptTokenCount != 10 {
		t.Errorf("PromptTokenCount = %d, want 10", resp.UsageMetadata.PromptTokenCount)
	}
	if resp.UsageMetadata.CandidatesTokenCount != 5 {
		t.Errorf("CandidatesTokenCount = %d, want 5", resp.UsageMetadata.CandidatesTokenCount)
	}
}

func TestBuildAntFinalResponse_WithToolUse(t *testing.T) {
	s := &antStreamState{
		toolUse: map[int]antToolUseAcc{
			0: {id: "tool_123", name: "bash", inputJSON: `{"command":"ls"}`},
		},
		stopReason:   anthropic.StopReasonToolUse,
		inputTokens:  20,
		outputTokens: 15,
	}
	resp := buildAntFinalResponse(s)

	if len(resp.Content.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(resp.Content.Parts))
	}
	p := resp.Content.Parts[0]
	fc := p.FunctionCall
	if fc == nil {
		t.Fatal("expected FunctionCall part")
	} else {
		name := fc.Name
		id := fc.ID
		cmd, _ := fc.Args["command"].(string)
		if name != "bash" {
			t.Errorf("name = %q, want bash", name)
		}
		if id != "tool_123" {
			t.Errorf("ID = %q, want tool_123", id)
		}
		if cmd != "ls" {
			t.Errorf("command arg = %q, want ls", cmd)
		}
	}
}

func TestBuildAntFinalResponse_TextAndToolUse(t *testing.T) {
	s := &antStreamState{
		text: "I'll run a command.",
		toolUse: map[int]antToolUseAcc{
			1: {id: "tool_456", name: "read", inputJSON: `{"path":"/tmp/x"}`},
		},
		stopReason:   anthropic.StopReasonToolUse,
		inputTokens:  0,
		outputTokens: 0,
	}
	resp := buildAntFinalResponse(s)

	if len(resp.Content.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(resp.Content.Parts))
	}
	if resp.Content.Parts[0].Text != "I'll run a command." {
		t.Errorf("text part = %q", resp.Content.Parts[0].Text)
	}
	if resp.Content.Parts[1].FunctionCall == nil {
		t.Error("expected FunctionCall in second part")
	}
	if resp.UsageMetadata != nil {
		t.Error("expected nil UsageMetadata when tokens are 0")
	}
}

func TestBuildAntFinalResponse_EmptyState(t *testing.T) {
	s := &antStreamState{toolUse: map[int]antToolUseAcc{}}
	resp := buildAntFinalResponse(s)

	if len(resp.Content.Parts) != 0 {
		t.Errorf("expected 0 parts, got %d", len(resp.Content.Parts))
	}
	if resp.UsageMetadata != nil {
		t.Error("expected nil UsageMetadata")
	}
}

func TestBuildAntFinalResponse_MultipleToolUseSortedByIndex(t *testing.T) {
	s := &antStreamState{
		toolUse: map[int]antToolUseAcc{
			2: {id: "tool_c", name: "write", inputJSON: `{"path":"/c"}`},
			0: {id: "tool_a", name: "bash", inputJSON: `{"cmd":"ls"}`},
			1: {id: "tool_b", name: "read", inputJSON: `{"path":"/b"}`},
		},
		stopReason: anthropic.StopReasonToolUse,
	}

	// Run multiple times to verify determinism (maps iterate randomly).
	for range 10 {
		resp := buildAntFinalResponse(s)
		if len(resp.Content.Parts) != 3 {
			t.Fatalf("expected 3 parts, got %d", len(resp.Content.Parts))
		}
		names := []string{
			resp.Content.Parts[0].FunctionCall.Name,
			resp.Content.Parts[1].FunctionCall.Name,
			resp.Content.Parts[2].FunctionCall.Name,
		}
		if names[0] != "bash" || names[1] != "read" || names[2] != "write" {
			t.Fatalf("expected [bash, read, write], got %v", names)
		}
	}
}

func TestBuildAntFinalResponse_MaxTokens(t *testing.T) {
	s := &antStreamState{
		text:         "truncated",
		toolUse:      map[int]antToolUseAcc{},
		stopReason:   anthropic.StopReasonMaxTokens,
		inputTokens:  100,
		outputTokens: 4096,
	}
	resp := buildAntFinalResponse(s)

	if resp.FinishReason != genai.FinishReasonMaxTokens {
		t.Errorf("FinishReason = %v, want MaxTokens", resp.FinishReason)
	}
}

func TestAnthropicNonStreamingErrorResponse(t *testing.T) {
	// Mock server that returns a 500 error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"internal server error"}}`))
	}))
	defer srv.Close()

	ctx := context.Background()
	llm, err := NewAnthropic(ctx, "claude-sonnet-4-6", "sk-test", srv.URL, "none", nil)
	if err != nil {
		t.Fatalf("NewAnthropic() error: %v", err)
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

// anthropicCapturingServer is a minimal httptest server that captures the
// request body and returns a successful Anthropic message response. Used by
// the prompt-cache E2E tests below.
func anthropicCapturingServer(t *testing.T) (*httptest.Server, *[]byte) {
	t.Helper()
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id":"msg_x","type":"message","role":"assistant","model":"claude-sonnet-4-6",
			"content":[{"type":"text","text":"ok"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":10,"output_tokens":1}
		}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

// countCacheControl walks a decoded JSON request body and returns the
// number of cache_control: {type: "ephemeral"} occurrences across every
// nested object.
func countCacheControl(t *testing.T, raw []byte) int {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal request body: %v\nbody=%s", err, raw)
	}
	n := 0
	var walk func(any)
	walk = func(x any) {
		switch vv := x.(type) {
		case map[string]any:
			if cc, ok := vv["cache_control"].(map[string]any); ok {
				if cc["type"] == "ephemeral" {
					n++
				}
			}
			for _, child := range vv {
				walk(child)
			}
		case []any:
			for _, child := range vv {
				walk(child)
			}
		}
	}
	walk(v)
	return n
}

// TestAnthropicCacheControl_DefaultOn asserts that a vanilla NewAnthropic
// call (no opts) sends a request body with 3 cache_control markers — one
// each on the last tool, the system block, and the last message block.
func TestAnthropicCacheControl_DefaultOn(t *testing.T) {
	srv, captured := anthropicCapturingServer(t)
	llm, err := NewAnthropic(context.Background(), "claude-sonnet-4-6", "sk-test", srv.URL, "none", nil)
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "hi"}}},
		},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Role:  "system",
				Parts: []*genai.Part{{Text: "you are a helpful agent."}},
			},
			Tools: []*genai.Tool{
				{FunctionDeclarations: []*genai.FunctionDeclaration{
					{Name: "read", Description: "read a file"},
				}},
			},
		},
	}
	for range llm.GenerateContent(context.Background(), req, false) {
	}
	if got := countCacheControl(t, *captured); got != 3 {
		t.Errorf("default opts: cache_control markers = %d, want 3\nbody=%s", got, *captured)
	}
}

// TestAnthropicCacheControl_DisabledByOptOut asserts that DisablePromptCaching
// true sends a request body with ZERO cache_control markers.
func TestAnthropicCacheControl_DisabledByOptOut(t *testing.T) {
	srv, captured := anthropicCapturingServer(t)
	llm, err := NewAnthropic(context.Background(), "claude-sonnet-4-6", "sk-test", srv.URL, "none",
		&LLMOptions{DisablePromptCaching: true})
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
		Config: &genai.GenerateContentConfig{
			Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{Name: "read"}}}},
		},
	}
	for range llm.GenerateContent(context.Background(), req, false) {
	}
	if got := countCacheControl(t, *captured); got != 0 {
		t.Errorf("DisablePromptCaching=true: cache_control markers = %d, want 0\nbody=%s", got, *captured)
	}
}

// TestAnthropicCacheControl_OpenAIIsNoOp asserts that NewOpenAI requests
// do not contain any cache_control markers — the cache_apply extension
// point is opt-in for now, and the OpenAI provider doesn't implement it.
func TestAnthropicCacheControl_OpenAIIsNoOp(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-x","object":"chat.completion","created":1,"model":"gpt-4o",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer srv.Close()

	llm, err := NewOpenAI(context.Background(), "gpt-4o", "sk-test", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}
	for range llm.GenerateContent(context.Background(), req, false) {
	}
	if got := countCacheControl(t, captured); got != 0 {
		t.Errorf("NewOpenAI body has cache_control markers (%d) — should be 0\nbody=%s", got, captured)
	}
}

// TestAnthropicCacheControl_EmptyMessages asserts the standard path works
// (single marker only) when only a user message is present (no system, no
// tools). The 4th breakpoint slot is intentionally left empty.
func TestAnthropicCacheControl_EmptyMessages(t *testing.T) {
	srv, captured := anthropicCapturingServer(t)
	llm, err := NewAnthropic(context.Background(), "claude-sonnet-4-6", "sk-test", srv.URL, "none", nil)
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}
	for range llm.GenerateContent(context.Background(), req, false) {
	}
	// Only 1 marker expected: on the last (and only) block of the last message.
	if got := countCacheControl(t, *captured); got != 1 {
		t.Errorf("messages-only: cache_control markers = %d, want 1\nbody=%s", got, *captured)
	}
}

// TestAnthropicCacheControl_BetaPathWithAdvisor verifies the beta path
// (buildBetaParams) also stamps the three breakpoints when the advisor
// tool is enabled. We can't easily fire a real beta request through
// GenerateContent in this test (it requires a real beta endpoint), so we
// exercise the helper directly.
func TestAnthropicCacheControl_BetaPathWithAdvisor(t *testing.T) {
	llmIface, err := NewAnthropic(context.Background(), "claude-sonnet-4-6", "sk-test",
		"http://localhost:1", "none",
		&LLMOptions{AdvisorModel: "claude-opus-4-7"})
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	m, ok := llmIface.(*anthropicModel)
	if !ok {
		t.Fatalf("expected *anthropicModel, got %T", llmIface)
	}

	msgs := []anthropic.MessageParam{
		{Role: anthropic.MessageParamRoleUser, Content: []anthropic.ContentBlockParamUnion{
			anthropic.NewTextBlock("hi"),
		}},
	}
	system := "you are an agent."
	tools := []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{Name: "read", Description: "read"}}}}
	cfg := &genai.GenerateContentConfig{Tools: tools}

	params := m.buildBetaParams("claude-sonnet-4-6", msgs, system, 1024, nil, cfg)

	// Tools slice must include the advisor + the read tool (2 tools).
	if len(params.Tools) < 2 {
		t.Fatalf("expected at least 2 tools (advisor + read), got %d", len(params.Tools))
	}
	// Apply the helper and verify the 3 markers.
	applyAnthropicCacheControlBeta(&params)

	// Last tool should be the marker holder. The helper stamps Tools[len-1]
	// unconditionally. We don't know the order (advisor first or last) but
	// exactly one tool must carry the marker.
	marked := 0
	for _, tl := range params.Tools {
		if cc := tl.GetCacheControl(); cc != nil && cc.Type != "" {
			marked++
		}
	}
	if marked != 1 {
		t.Errorf("tools: %d marked, want 1", marked)
	}
	if cc := params.System[0].CacheControl; cc.Type == "" {
		t.Errorf("system block missing cache_control, got %+v", cc)
	}
	last := params.Messages[len(params.Messages)-1]
	if cc := last.Content[len(last.Content)-1].GetCacheControl(); cc == nil || cc.Type == "" {
		t.Errorf("last message block missing cache_control, got %+v", cc)
	}
}

// TestAnthropicNonStreamingPropagatesCacheRead verifies that the
// cache_creation_input_tokens and cache_read_input_tokens fields from
// Anthropic's non-streaming response are surfaced on the LLMResponse.UsageMetadata
// as CachedContentTokenCount so the sidebar's cache indicator reflects them.
func TestAnthropicNonStreamingPropagatesCacheRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_cache_test",
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-sonnet-4-6",
			"stop_reason": "end_turn",
			"content":     []map[string]any{{"type": "text", "text": "ok"}},
			"usage": map[string]any{
				"input_tokens":                100,
				"output_tokens":               20,
				"cache_creation_input_tokens": 50,
				"cache_read_input_tokens":     75,
			},
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	llm, err := NewAnthropic(ctx, "claude-sonnet-4-6", "sk-test", srv.URL, "none", nil)
	if err != nil {
		t.Fatalf("NewAnthropic() error: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}
	var final *model.LLMResponse
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			t.Fatalf("GenerateContent() error: %v", err)
		}
		if resp != nil {
			final = resp
		}
	}
	if final == nil {
		t.Fatal("expected a final response")
	}
	if final.UsageMetadata == nil {
		t.Fatal("expected non-nil UsageMetadata")
	}
	if final.UsageMetadata.CachedContentTokenCount != 75 {
		t.Errorf("CachedContentTokenCount = %d, want 75 (cache_read_input_tokens)", final.UsageMetadata.CachedContentTokenCount)
	}
}

func TestAntTextMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		role     string
		wantRole anthropic.MessageParamRole
	}{
		{name: "model maps to assistant", role: "model", wantRole: anthropic.MessageParamRoleAssistant},
		{name: "assistant stays assistant", role: "assistant", wantRole: anthropic.MessageParamRoleAssistant},
		{name: "user maps to user", role: "user", wantRole: anthropic.MessageParamRoleUser},
		{name: "unknown role maps to user", role: "narrator", wantRole: anthropic.MessageParamRoleUser},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := antTextMessage(tt.role, []string{"one", "two"})
			if got.Role != tt.wantRole {
				t.Errorf("role = %q, want %q", got.Role, tt.wantRole)
			}
			if len(got.Content) != 1 {
				t.Fatalf("content has %d blocks, want a single text block", len(got.Content))
			}
			if got.Content[0].OfText == nil {
				t.Fatal("content block is not a text block")
			}
			if text := got.Content[0].OfText.Text; text != "one\ntwo" {
				t.Errorf("text = %q, want the parts joined by a newline", text)
			}
		})
	}
}

func TestAntToolUseMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		textParts  []string
		calls      []*genai.FunctionCall
		wantBlocks int
		wantText   string
	}{
		{
			name:       "text block precedes the tool_use blocks",
			textParts:  []string{"thinking"},
			calls:      []*genai.FunctionCall{{ID: "a", Name: "f", Args: map[string]any{"k": "v"}}},
			wantBlocks: 2,
			wantText:   "thinking",
		},
		{
			name:       "no text yields tool_use blocks only",
			calls:      []*genai.FunctionCall{{ID: "a", Name: "f"}, {ID: "b", Name: "g"}},
			wantBlocks: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := antToolUseMessage(tt.textParts, tt.calls)
			if got.Role != anthropic.MessageParamRoleAssistant {
				t.Errorf("role = %q, want %q", got.Role, anthropic.MessageParamRoleAssistant)
			}
			if len(got.Content) != tt.wantBlocks {
				t.Fatalf("content has %d blocks, want %d", len(got.Content), tt.wantBlocks)
			}
			if tt.wantText == "" {
				if got.Content[0].OfToolUse == nil {
					t.Error("first block is not a tool_use block")
				}
				return
			}
			if got.Content[0].OfText == nil || got.Content[0].OfText.Text != tt.wantText {
				t.Errorf("first block = %+v, want text %q", got.Content[0], tt.wantText)
			}
		})
	}
}

func TestAntToolUseMessageNilArgsBecomeEmptyObject(t *testing.T) {
	t.Parallel()

	// A nil Args map marshals to JSON null, which unmarshals back to a nil
	// map. The Messages API rejects a null tool_use input, so it must become
	// an empty object instead.
	got := antToolUseMessage(nil, []*genai.FunctionCall{{ID: "a", Name: "f", Args: nil}})
	if len(got.Content) != 1 {
		t.Fatalf("content has %d blocks, want 1", len(got.Content))
	}
	block := got.Content[0].OfToolUse
	if block == nil {
		t.Fatal("content block is not a tool_use block")
	}
	input, ok := block.Input.(map[string]any)
	if !ok {
		t.Fatalf("tool_use input is %T, want map[string]any", block.Input)
	}
	if input == nil {
		t.Error("tool_use input is a nil map, want an empty non-nil map")
	}
	if len(input) != 0 {
		t.Errorf("tool_use input = %v, want it empty", input)
	}
}

func TestAntToolResultMessage(t *testing.T) {
	t.Parallel()

	calls := []*genai.FunctionCall{
		{ID: "answered", Name: "f"},
		{ID: "unanswered", Name: "g"},
	}
	responses := map[string]*genai.FunctionResponse{
		"answered": {ID: "answered", Response: map[string]any{"result": "ok"}},
	}

	got := antToolResultMessage(calls, responses)

	if got.Role != anthropic.MessageParamRoleUser {
		t.Errorf("role = %q, want %q", got.Role, anthropic.MessageParamRoleUser)
	}
	if len(got.Content) != 2 {
		t.Fatalf("content has %d blocks, want one per call", len(got.Content))
	}
	for i, block := range got.Content {
		if block.OfToolResult == nil {
			t.Fatalf("block %d is not a tool_result block", i)
		}
	}
	if id := got.Content[0].OfToolResult.ToolUseID; id != "answered" {
		t.Errorf("first tool_use_id = %q, want %q", id, "answered")
	}

	// Anthropic substitutes placeholder text for a missing result, where the
	// Ollama converter leaves the content empty.
	want := "No response available for this function call."
	unanswered := got.Content[1].OfToolResult.Content
	if len(unanswered) != 1 || unanswered[0].OfText == nil || unanswered[0].OfText.Text != want {
		t.Errorf("unanswered tool_result content = %+v, want the placeholder %q", unanswered, want)
	}
}

func TestAntFinishStream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		streamErr     error
		ctxCanceled   bool
		wantErrorCode string
		wantFinish    genai.FinishReason
	}{
		{
			name:       "clean stream yields the assembled response",
			wantFinish: genai.FinishReasonStop,
		},
		{
			// A canceled context is the user interrupting, not a failure:
			// it must not surface as STREAM_ERROR.
			name:        "canceled context yields the cancellation response",
			streamErr:   io.ErrUnexpectedEOF,
			ctxCanceled: true,
			wantFinish:  genai.FinishReasonOther,
		},
		{
			name:          "transport failure yields STREAM_ERROR",
			streamErr:     io.ErrUnexpectedEOF,
			wantErrorCode: "STREAM_ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tt.ctxCanceled {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}

			var got []*model.LLMResponse
			antFinishStream(ctx, tt.streamErr, &antStreamState{toolUse: map[int]antToolUseAcc{}},
				func(r *model.LLMResponse, _ error) bool {
					got = append(got, r)
					return true
				})

			if len(got) != 1 {
				t.Fatalf("antFinishStream() yielded %d responses, want 1", len(got))
			}
			if got[0].ErrorCode != tt.wantErrorCode {
				t.Errorf("ErrorCode = %q, want %q", got[0].ErrorCode, tt.wantErrorCode)
			}
			if tt.wantErrorCode == "" && got[0].FinishReason != tt.wantFinish {
				t.Errorf("FinishReason = %q, want %q", got[0].FinishReason, tt.wantFinish)
			}
		})
	}
}

func TestAntAppendToolInput(t *testing.T) {
	t.Parallel()

	t.Run("appends to an open block", func(t *testing.T) {
		t.Parallel()
		state := &antStreamState{toolUse: map[int]antToolUseAcc{0: {id: "a", inputJSON: `{"k`}}}
		antAppendToolInput(state, 0, `":1}`)
		if got := state.toolUse[0].inputJSON; got != `{"k":1}` {
			t.Errorf("inputJSON = %q, want the concatenated deltas", got)
		}
	})

	t.Run("drops a delta for a block that never opened", func(t *testing.T) {
		t.Parallel()
		// There is no id or name to attach the JSON to, so the delta has
		// nowhere to go and must not create a phantom accumulator entry.
		state := &antStreamState{toolUse: map[int]antToolUseAcc{}}
		antAppendToolInput(state, 7, `{"orphan":true}`)
		if len(state.toolUse) != 0 {
			t.Errorf("toolUse = %+v, want no entries", state.toolUse)
		}
	})
}

func TestAntPartialResponse(t *testing.T) {
	t.Parallel()

	for _, role := range []string{"thinking", "advisor", string(genai.RoleModel)} {
		t.Run("role="+role, func(t *testing.T) {
			t.Parallel()
			got := antPartialResponse(role, "tok")
			if !got.Partial || got.TurnComplete {
				t.Errorf("Partial=%v TurnComplete=%v, want true/false", got.Partial, got.TurnComplete)
			}
			if got.Content.Role != role {
				t.Errorf("role = %q, want %q", got.Content.Role, role)
			}
			if len(got.Content.Parts) != 1 || got.Content.Parts[0].Text != "tok" {
				t.Errorf("parts = %v, want a single %q part", got.Content.Parts, "tok")
			}
		})
	}
}

// antEventFromJSON decodes a wire payload into an Anthropic stream event.
// The delta unions carry their source JSON in an unexported field and their
// As*() accessors read it, so an event has to be unmarshalled rather than
// built with a struct literal.
func antEventFromJSON[E any](t *testing.T, payload string) E {
	t.Helper()
	var e E
	if err := json.Unmarshal([]byte(payload), &e); err != nil {
		t.Fatalf("decode %T from %s: %v", e, payload, err)
	}
	return e
}

func TestAntApplyMessageDelta(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		initial           antStreamState
		payload           string
		wantStop          anthropic.StopReason
		wantOutput        int64
		wantCacheRead     int64
		wantCacheCreation int64
	}{
		{
			name:     "stop reason and usage are recorded",
			payload:  `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":42,"cache_read_input_tokens":10,"cache_creation_input_tokens":5}}`,
			wantStop: anthropic.StopReasonEndTurn, wantOutput: 42,
			wantCacheRead: 10, wantCacheCreation: 5,
		},
		{
			// Cache counts are monotonically increasing across a response, so
			// a lower value in a later delta must not overwrite a higher one.
			name:     "a smaller cache count does not overwrite a larger one",
			initial:  antStreamState{cacheReadTokens: 99, cacheCreationTokens: 88},
			payload:  `{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":7,"cache_read_input_tokens":10,"cache_creation_input_tokens":5}}`,
			wantStop: anthropic.StopReasonMaxTokens, wantOutput: 7,
			wantCacheRead: 99, wantCacheCreation: 88,
		},
		{
			name:     "a larger cache count is taken",
			initial:  antStreamState{cacheReadTokens: 1, cacheCreationTokens: 1},
			payload:  `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7,"cache_read_input_tokens":10,"cache_creation_input_tokens":5}}`,
			wantStop: anthropic.StopReasonToolUse, wantOutput: 7,
			wantCacheRead: 10, wantCacheCreation: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state := tt.initial
			state.toolUse = map[int]antToolUseAcc{}
			antApplyMessageDelta(&state, antEventFromJSON[anthropic.MessageDeltaEvent](t, tt.payload))

			if state.stopReason != tt.wantStop {
				t.Errorf("stopReason = %q, want %q", state.stopReason, tt.wantStop)
			}
			if state.outputTokens != tt.wantOutput {
				t.Errorf("outputTokens = %d, want %d", state.outputTokens, tt.wantOutput)
			}
			if state.cacheReadTokens != tt.wantCacheRead {
				t.Errorf("cacheReadTokens = %d, want %d", state.cacheReadTokens, tt.wantCacheRead)
			}
			if state.cacheCreationTokens != tt.wantCacheCreation {
				t.Errorf("cacheCreationTokens = %d, want %d", state.cacheCreationTokens, tt.wantCacheCreation)
			}
		})
	}
}

func TestAntApplyContentBlockStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		payload  string
		wantOpen bool
	}{
		{
			name:     "a tool_use block is recorded",
			payload:  `{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tu_1","name":"bash","input":{}}}`,
			wantOpen: true,
		},
		{
			name:    "a text block opens nothing",
			payload: `{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`,
		},
		{
			name:    "a thinking block opens nothing",
			payload: `{"type":"content_block_start","index":2,"content_block":{"type":"thinking","thinking":""}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state := &antStreamState{toolUse: map[int]antToolUseAcc{}}
			antApplyContentBlockStart(state, antEventFromJSON[anthropic.ContentBlockStartEvent](t, tt.payload))

			block, open := state.toolUse[2]
			if open != tt.wantOpen {
				t.Fatalf("tool call open at index 2 = %v, want %v", open, tt.wantOpen)
			}
			if !tt.wantOpen {
				return
			}
			if block.id != "tu_1" || block.name != "bash" {
				t.Errorf("accumulator = %+v, want id=tu_1 name=bash", block)
			}
		})
	}
}

func TestAntApplyContentBlockDelta(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		payload      string
		stopConsumer bool
		wantText     string
		wantRole     string
		wantJSON     string
		wantStop     bool
	}{
		{
			name:     "text delta accumulates and is forwarded",
			payload:  `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
			wantText: "hi", wantRole: string(genai.RoleModel),
		},
		{
			// Reasoning is forwarded but deliberately not accumulated into
			// state.text: it is not part of the answer.
			name:     "thinking delta is forwarded but not accumulated",
			payload:  `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`,
			wantRole: "thinking",
		},
		{
			name:     "input json delta accumulates and is not forwarded",
			payload:  `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"k\":1}"}}`,
			wantJSON: `{"k":1}`,
		},
		{
			name:         "a consumer that stops is reported",
			payload:      `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
			stopConsumer: true,
			wantText:     "hi", wantRole: string(genai.RoleModel), wantStop: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := &antStreamState{toolUse: map[int]antToolUseAcc{0: {id: "tu_1", name: "bash"}}}
			var got []*model.LLMResponse
			stop := antApplyContentBlockDelta(state,
				antEventFromJSON[anthropic.ContentBlockDeltaEvent](t, tt.payload),
				func(r *model.LLMResponse, _ error) bool {
					got = append(got, r)
					return !tt.stopConsumer
				})

			if stop != tt.wantStop {
				t.Errorf("stop = %v, want %v", stop, tt.wantStop)
			}
			if state.text != tt.wantText {
				t.Errorf("state.text = %q, want %q", state.text, tt.wantText)
			}
			if state.toolUse[0].inputJSON != tt.wantJSON {
				t.Errorf("inputJSON = %q, want %q", state.toolUse[0].inputJSON, tt.wantJSON)
			}

			if tt.wantRole == "" {
				if len(got) != 0 {
					t.Errorf("forwarded %d responses, want none", len(got))
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("forwarded %d responses, want 1", len(got))
			}
			if got[0].Content.Role != tt.wantRole {
				t.Errorf("forwarded role = %q, want %q", got[0].Content.Role, tt.wantRole)
			}
		})
	}
}
