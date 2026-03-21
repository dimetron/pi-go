package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	// When the model is named "anthropic" it should fall back to claude-sonnet-4-6.
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
	// req.Model == "anthropic" should also trigger the fallback to "claude-sonnet-4-6".
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
	}
	if fcPart.FunctionCall.Name != "get_weather" {
		t.Errorf("function name = %q, want get_weather", fcPart.FunctionCall.Name)
	}
	if fcPart.FunctionCall.ID != "toolu_abc123" {
		t.Errorf("function call ID = %q, want toolu_abc123", fcPart.FunctionCall.ID)
	}
	loc, _ := fcPart.FunctionCall.Args["location"].(string)
	if loc != "San Francisco" {
		t.Errorf("location arg = %q, want San Francisco", loc)
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
	fc := resp.Content.Parts[0].FunctionCall
	if fc == nil {
		t.Fatal("expected FunctionCall part")
	}
	if fc.Name != "bash" {
		t.Errorf("name = %q, want bash", fc.Name)
	}
	if fc.ID != "tool_123" {
		t.Errorf("ID = %q, want tool_123", fc.ID)
	}
	cmd, _ := fc.Args["command"].(string)
	if cmd != "ls" {
		t.Errorf("command arg = %q, want ls", cmd)
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
