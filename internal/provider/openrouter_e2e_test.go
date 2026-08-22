//go:build e2e

package provider

import (
	"context"
	"os"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func testGetOpenRouterAPIKey(t *testing.T) string {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("skipping: OPENROUTER_API_KEY not set")
	}
	return key
}

// TestE2EOpenRouterNonStreaming hits the real OpenRouter API with a cheap
// model and verifies a complete non-streaming response.
func TestE2EOpenRouterNonStreaming(t *testing.T) {
	key := testGetOpenRouterAPIKey(t)

	llm, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", key, "", nil)
	if err != nil {
		t.Fatalf("NewOpenRouter() error: %v", err)
	}
	if llm.Name() != "google/gemini-3.7-flash" {
		t.Errorf("Name() = %q, want google/gemini-3.7-flash", llm.Name())
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "What is 2+2? Answer in one short sentence."}}},
		},
	}

	var responses []*model.LLMResponse
	for resp, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		responses = append(responses, resp)
	}

	if len(responses) == 0 {
		t.Fatal("expected at least one response")
	}
	last := responses[len(responses)-1]
	if !last.TurnComplete {
		t.Error("expected TurnComplete = true on final response")
	}
	if last.Content == nil || len(last.Content.Parts) == 0 {
		t.Fatal("expected content with parts")
	}
	text := last.Content.Parts[0].Text
	if text == "" {
		t.Error("expected non-empty text response")
	}
	t.Logf("Got response: %s", text)
}

// TestE2EOpenRouterStreaming hits the real OpenRouter with streaming enabled.
func TestE2EOpenRouterStreaming(t *testing.T) {
	key := testGetOpenRouterAPIKey(t)

	llm, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", key, "", nil)
	if err != nil {
		t.Fatalf("NewOpenRouter() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Count from 1 to 3. Reply with just the numbers separated by spaces."}}},
		},
	}

	var responses []*model.LLMResponse
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("GenerateContent streaming error: %v", err)
		}
		responses = append(responses, resp)
	}

	if len(responses) < 2 {
		t.Fatalf("expected at least 2 streaming responses (partials + final), got %d", len(responses))
	}

	last := responses[len(responses)-1]
	if !last.TurnComplete {
		t.Error("expected TurnComplete = true on last streaming response")
	}

	var fullText strings.Builder
	for _, resp := range responses {
		if resp.Content != nil {
			for _, p := range resp.Content.Parts {
				fullText.WriteString(p.Text)
			}
		}
	}
	t.Logf("Full streamed text: %s", fullText.String())
	if fullText.Len() == 0 {
		t.Error("expected non-empty streamed text")
	}
}

// TestE2EOpenRouterWithSystemPrompt verifies the system prompt is forwarded.
func TestE2EOpenRouterWithSystemPrompt(t *testing.T) {
	key := testGetOpenRouterAPIKey(t)

	llm, err := NewOpenRouter(context.Background(), "google/gemini-3.7-flash", key, "", nil)
	if err != nil {
		t.Fatalf("NewOpenRouter() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "What is your name?"}}},
		},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: "You are a helpful assistant named Orby."}},
			},
		},
	}

	var responses []*model.LLMResponse
	for resp, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		responses = append(responses, resp)
	}

	if len(responses) == 0 {
		t.Fatal("expected at least one response")
	}
	text := responses[len(responses)-1].Content.Parts[0].Text
	if text == "" {
		t.Error("expected non-empty text response")
	}
	t.Logf("Got response: %s", text)
}

// TestE2EOpenRouterResolveModel verifies the openrouter/ prefix resolves.
func TestE2EOpenRouterResolveModel(t *testing.T) {
	testGetOpenRouterAPIKey(t) // just verify env is set

	info, err := Resolve("openrouter/google/gemini-3.7-flash")
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if info.Provider != "openrouter" {
		t.Errorf("Provider = %q, want openrouter", info.Provider)
	}
	if info.Model != "google/gemini-3.7-flash" {
		t.Errorf("Model = %q, want google/gemini-3.7-flash", info.Model)
	}
	if info.Ollama || info.Custom {
		t.Errorf("expected Ollama and Custom to be false, got %+v", info)
	}
}

// TestE2EOpenRouterNewLLM verifies the full NewLLM dispatch path.
func TestE2EOpenRouterNewLLM(t *testing.T) {
	key := testGetOpenRouterAPIKey(t)

	llm, err := NewLLM(context.Background(), Info{Provider: "openrouter", Model: "google/gemini-3.7-flash"}, key, "", "", nil)
	if err != nil {
		t.Fatalf("NewLLM() error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewLLM() returned nil")
	}
	if llm.Name() != "google/gemini-3.7-flash" {
		t.Errorf("Name() = %q, want google/gemini-3.7-flash", llm.Name())
	}
}

// TestE2EOpenRouterListModels hits the real OpenRouter /v1/models endpoint.
func TestE2EOpenRouterListModels(t *testing.T) {
	key := testGetOpenRouterAPIKey(t)

	models, err := ListModels(context.Background(), "openrouter", ListModelsOptions{
		APIKey: key,
	})
	if err != nil {
		t.Fatalf("ListModels() error: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("expected at least one model from OpenRouter")
	}
	t.Logf("Listed %d OpenRouter models; first: %s", len(models), models[0].ID)
}
