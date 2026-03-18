package tui

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"testing"

	llmmodel "google.golang.org/adk/model"
	"google.golang.org/genai"
)

// pingMockLLM returns a fixed text response for ping tests.
type pingMockLLM struct {
	name     string
	response string
}

func (m *pingMockLLM) Name() string { return m.name }

func (m *pingMockLLM) GenerateContent(_ context.Context, _ *llmmodel.LLMRequest, _ bool) iter.Seq2[*llmmodel.LLMResponse, error] {
	return func(yield func(*llmmodel.LLMResponse, error) bool) {
		yield(&llmmodel.LLMResponse{
			TurnComplete: true,
			Content: &genai.Content{
				Role:  string(genai.RoleModel),
				Parts: []*genai.Part{{Text: m.response}},
			},
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:     10,
				CandidatesTokenCount: 5,
			},
		}, nil)
	}
}

// pingErrorLLM always returns an error.
type pingErrorLLM struct{}

func (m *pingErrorLLM) Name() string { return "error-model" }

func (m *pingErrorLLM) GenerateContent(_ context.Context, _ *llmmodel.LLMRequest, _ bool) iter.Seq2[*llmmodel.LLMResponse, error] {
	return func(yield func(*llmmodel.LLMResponse, error) bool) {
		yield(nil, fmt.Errorf("connection refused"))
	}
}

// pingEmptyLLM returns an empty response.
type pingEmptyLLM struct{}

func (m *pingEmptyLLM) Name() string { return "empty-model" }

func (m *pingEmptyLLM) GenerateContent(_ context.Context, _ *llmmodel.LLMRequest, _ bool) iter.Seq2[*llmmodel.LLMResponse, error] {
	return func(yield func(*llmmodel.LLMResponse, error) bool) {
		yield(&llmmodel.LLMResponse{
			TurnComplete: true,
			Content:      &genai.Content{Role: string(genai.RoleModel), Parts: []*genai.Part{{Text: ""}}},
		}, nil)
	}
}

func TestExecutePing_OutputContainsProviderAndModel(t *testing.T) {
	llm := &pingMockLLM{name: "test-model", response: "Pong"}
	output, err := executePing(context.Background(), llm, "anthropic", "claude-sonnet-4-20250514", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(output, "**Provider:** anthropic") {
		t.Errorf("output missing Provider, got:\n%s", output)
	}
	if !strings.Contains(output, "**Model:** claude-sonnet-4-20250514") {
		t.Errorf("output missing Model, got:\n%s", output)
	}
	if !strings.Contains(output, "is ALIVE") {
		t.Errorf("output missing ALIVE status, got:\n%s", output)
	}
}

func TestExecutePing_OllamaModel(t *testing.T) {
	llm := &pingMockLLM{name: "qwen3.5:latest", response: "Pong"}
	output, err := executePing(context.Background(), llm, "ollama", "qwen3.5:latest", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(output, "**Provider:** ollama") {
		t.Errorf("output missing Provider ollama, got:\n%s", output)
	}
	if !strings.Contains(output, "**Model:** qwen3.5:latest") {
		t.Errorf("output missing Model, got:\n%s", output)
	}
	if !strings.Contains(output, "**Reply:** Pong") {
		t.Errorf("output missing reply, got:\n%s", output)
	}
}

func TestExecutePing_CustomPrompt(t *testing.T) {
	llm := &pingMockLLM{name: "test-model", response: "4"}
	output, err := executePing(context.Background(), llm, "openai", "gpt-4o", "What is 2+2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(output, "**Reply:** 4") {
		t.Errorf("expected reply '4', got:\n%s", output)
	}
	if !strings.Contains(output, "is ALIVE") {
		t.Errorf("output missing ALIVE, got:\n%s", output)
	}
}

func TestExecutePing_TokensDisplayed(t *testing.T) {
	llm := &pingMockLLM{name: "test-model", response: "Pong"}
	output, err := executePing(context.Background(), llm, "anthropic", "test-model", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(output, "tokens(in=10 out=5)") {
		t.Errorf("output missing token counts, got:\n%s", output)
	}
}

func TestExecutePing_LLMError(t *testing.T) {
	llm := &pingErrorLLM{}
	output, err := executePing(context.Background(), llm, "anthropic", "error-model", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("expected connection refused error, got: %v", err)
	}
	if !strings.Contains(output, "ERROR:") {
		t.Errorf("output should contain ERROR, got:\n%s", output)
	}
}

func TestExecutePing_EmptyResponse(t *testing.T) {
	llm := &pingEmptyLLM{}
	_, err := executePing(context.Background(), llm, "anthropic", "empty-model", "")
	if err == nil {
		t.Fatal("expected error for empty response, got nil")
	}
	if !strings.Contains(err.Error(), "empty response") {
		t.Errorf("expected empty response error, got: %v", err)
	}
}

func TestSlashCommands_PingRegistered(t *testing.T) {
	found := false
	for _, cmd := range slashCommands {
		if cmd == "/ping" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected /ping in slashCommands list")
	}
}

func TestSlashCommandDesc_Ping(t *testing.T) {
	desc := slashCommandDesc("/ping")
	if desc == "" {
		t.Error("expected non-empty description for /ping")
	}
	if !strings.Contains(strings.ToLower(desc), "connectivity") {
		t.Errorf("expected connectivity in description, got %q", desc)
	}
}

func TestHandlePingCommand_SetsPlaceholder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockLLM := &pingMockLLM{name: "test-model", response: "Pong"}
	m := &model{
		ctx:      ctx,
		input:    "/ping",
		messages: make([]message, 0),
		cfg:      Config{ActiveRole: "default", LLM: mockLLM},
	}

	newM, cmd := m.handlePingCommand(nil)
	mm := newM.(*model)

	if cmd == nil {
		t.Error("expected non-nil cmd for async ping")
	}
	if len(mm.messages) != 1 {
		t.Fatalf("expected 1 placeholder message, got %d", len(mm.messages))
	}
	if mm.messages[0].content != "Pinging model..." {
		t.Errorf("expected placeholder 'Pinging model...', got %q", mm.messages[0].content)
	}
	if mm.input != "" {
		t.Errorf("input should be cleared, got %q", mm.input)
	}
}

func TestHandlePingCommand_NoLLM(t *testing.T) {
	m := &model{
		input:    "/ping",
		messages: make([]message, 0),
		cfg:      Config{},
	}

	newM, cmd := m.handlePingCommand(nil)
	mm := newM.(*model)

	if cmd != nil {
		t.Error("expected nil cmd when no LLM")
	}
	if !strings.Contains(mm.messages[0].content, "No LLM configured") {
		t.Errorf("expected no LLM message, got %q", mm.messages[0].content)
	}
}

func TestPingDoneMsg_ReplacesPlaceholder(t *testing.T) {
	m := &model{
		messages: []message{
			{role: "assistant", content: "Pinging model..."},
		},
	}

	msg := pingDoneMsg{output: "**Provider:** test\n✓ Model **test** is ALIVE"}
	newM, _ := m.Update(msg)
	mm := newM.(*model)

	if len(mm.messages) != 1 {
		t.Fatalf("expected 1 message (replaced), got %d", len(mm.messages))
	}
	if strings.Contains(mm.messages[0].content, "Pinging model...") {
		t.Error("placeholder should have been replaced")
	}
	if !strings.Contains(mm.messages[0].content, "is ALIVE") {
		t.Errorf("expected ALIVE in output, got %q", mm.messages[0].content)
	}
}

func TestPingDoneMsg_ErrorIncluded(t *testing.T) {
	m := &model{
		messages: []message{
			{role: "assistant", content: "Pinging model..."},
		},
	}

	msg := pingDoneMsg{output: "**Provider:** test\n", err: fmt.Errorf("timeout")}
	newM, _ := m.Update(msg)
	mm := newM.(*model)

	if !strings.Contains(mm.messages[0].content, "Ping failed: timeout") {
		t.Errorf("expected error in output, got %q", mm.messages[0].content)
	}
}
