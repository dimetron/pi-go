package guardrail

import (
	"context"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// mockLLM is a test LLM that returns a single response with usage metadata.
type mockLLM struct {
	name         string
	inputTokens  int32
	outputTokens int32
}

func (m *mockLLM) Name() string { return m.name }

func (m *mockLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(&model.LLMResponse{
			Content:      &genai.Content{Role: "model", Parts: []*genai.Part{{Text: "hello"}}},
			TurnComplete: true,
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:     m.inputTokens,
				CandidatesTokenCount: m.outputTokens,
			},
		}, nil)
	}
}

func TestWrapModel_TracksUsage(t *testing.T) {
	tracker := NewWithPath(0, "")
	llm := &mockLLM{name: "test-model", inputTokens: 100, outputTokens: 50}

	wrapped := WrapModel(llm, tracker)

	if wrapped.Name() != "test-model" {
		t.Errorf("expected name 'test-model', got %q", wrapped.Name())
	}

	// Generate content and consume the iterator.
	for resp, err := range wrapped.GenerateContent(context.Background(), nil, false) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Content == nil {
			t.Error("expected content")
		}
	}

	// Verify usage was tracked.
	if tracker.TotalUsed() != 150 {
		t.Errorf("expected 150 tokens tracked, got %d", tracker.TotalUsed())
	}
	u := tracker.Current()
	if u.InputTokens != 100 {
		t.Errorf("expected 100 input, got %d", u.InputTokens)
	}
	if u.OutputTokens != 50 {
		t.Errorf("expected 50 output, got %d", u.OutputTokens)
	}
}

func TestWrapModel_BlocksWhenLimitExceeded(t *testing.T) {
	tracker := NewWithPath(100, "")
	tracker.Add(50, 50) // exactly 100 — at limit

	llm := &mockLLM{name: "test", inputTokens: 10, outputTokens: 5}
	wrapped := WrapModel(llm, tracker)

	for resp, err := range wrapped.GenerateContent(context.Background(), nil, false) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.ErrorCode != "DAILY_LIMIT_EXCEEDED" {
			t.Errorf("expected DAILY_LIMIT_EXCEEDED, got %q", resp.ErrorCode)
		}
		if !strings.Contains(resp.ErrorMessage, "daily token limit exceeded") {
			t.Errorf("expected limit message, got %q", resp.ErrorMessage)
		}
	}
}

func TestWrapModel_NilTracker(t *testing.T) {
	llm := &mockLLM{name: "test", inputTokens: 100, outputTokens: 50}
	wrapped := WrapModel(llm, nil)

	// Should be the same model (no wrapper).
	if wrapped.Name() != "test" {
		t.Errorf("expected 'test', got %q", wrapped.Name())
	}

	// Should work normally.
	for resp, err := range wrapped.GenerateContent(context.Background(), nil, false) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Content == nil {
			t.Error("expected content")
		}
	}
}

func TestWrapModel_MultipleResponses(t *testing.T) {
	tracker := NewWithPath(0, "")
	llm := &mockLLM{name: "test", inputTokens: 200, outputTokens: 100}
	wrapped := WrapModel(llm, tracker)

	// Simulate 3 calls.
	for range 3 {
		for range wrapped.GenerateContent(context.Background(), nil, false) {
		}
	}

	if tracker.TotalUsed() != 900 {
		t.Errorf("expected 900 tokens after 3 calls, got %d", tracker.TotalUsed())
	}
	u := tracker.Current()
	if u.Requests != 3 {
		t.Errorf("expected 3 requests, got %d", u.Requests)
	}
}

func TestWrapModel_NoUsageMetadata(t *testing.T) {
	tracker := NewWithPath(0, "")

	// LLM that returns response without usage metadata.
	noUsageLLM := &noUsageMockLLM{}
	wrapped := WrapModel(noUsageLLM, tracker)

	for range wrapped.GenerateContent(context.Background(), nil, false) {
	}

	if tracker.TotalUsed() != 0 {
		t.Errorf("expected 0 tokens when no metadata, got %d", tracker.TotalUsed())
	}
}

type noUsageMockLLM struct{}

func (m *noUsageMockLLM) Name() string { return "no-usage" }

func (m *noUsageMockLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(&model.LLMResponse{
			Content:      &genai.Content{Role: "model", Parts: []*genai.Part{{Text: "hi"}}},
			TurnComplete: true,
		}, nil)
	}
}
