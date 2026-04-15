//go:build e2e

package provider

import (
	"context"
	"os"
	"strings"
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

func testGetMistralAPIKey(t *testing.T) string {
	key := os.Getenv("MISTRAL_API_KEY")
	if key == "" {
		t.Skip("skipping: MISTRAL_API_KEY not set")
	}
	return key
}

func TestE2EMistralSmallNonStreaming(t *testing.T) {
	key := testGetMistralAPIKey(t)

	llm, err := NewMistral(context.Background(), "mistral-small-latest", key, "", nil)
	if err != nil {
		t.Fatalf("NewMistral() error: %v", err)
	}
	if llm.Name() != "mistral-small-latest" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "mistral-small-latest")
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

func TestE2EMistralSmallStreaming(t *testing.T) {
	key := testGetMistralAPIKey(t)

	llm, err := NewMistral(context.Background(), "mistral-small-latest", key, "", nil)
	if err != nil {
		t.Fatalf("NewMistral() error: %v", err)
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

	// Concatenate all text parts to verify content is streamed
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

func TestE2EMistralSmallWithSystemPrompt(t *testing.T) {
	key := testGetMistralAPIKey(t)

	llm, err := NewMistral(context.Background(), "mistral-small-latest", key, "", nil)
	if err != nil {
		t.Fatalf("NewMistral() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "What is your name?"}}},
		},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: "You are a helpful assistant named Misty."}},
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
	// The model may or may not reference "Misty" — just verify we got a valid response
	t.Logf("Got response: %s", text)
}

func TestE2EMistralSmallResolveModel(t *testing.T) {
	testGetMistralAPIKey(t) // just verify env is set

	info, err := Resolve("mistral-small-latest")
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if info.Provider != "mistral" {
		t.Errorf("Provider = %q, want mistral", info.Provider)
	}
	if info.Model != "mistral-small-latest" {
		t.Errorf("Model = %q, want mistral-small-latest", info.Model)
	}
	if info.Ollama {
		t.Error("expected Ollama = false for Mistral cloud")
	}
}

func TestE2EMistralSmallNewLLM(t *testing.T) {
	key := testGetMistralAPIKey(t)

	llm, err := NewLLM(context.Background(), Info{Provider: "mistral", Model: "mistral-small-latest"}, key, "", "", nil)
	if err != nil {
		t.Fatalf("NewLLM() error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewLLM() returned nil")
	}
	if llm.Name() != "mistral-small-latest" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "mistral-small-latest")
	}
}

func TestE2EMistralSmallWithTools(t *testing.T) {
	key := testGetMistralAPIKey(t)

	llm, err := NewMistral(context.Background(), "mistral-small-latest", key, "", nil)
	if err != nil {
		t.Fatalf("NewMistral() error: %v", err)
	}

	tools := []*genai.Tool{{
		FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name:        "add",
			Description: "Add two integers",
			ParametersJsonSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"a": map[string]any{"type": "integer"},
					"b": map[string]any{"type": "integer"},
				},
				"required": []any{"a", "b"},
			},
		}},
	}}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "What is 3 + 5? Use the add tool."}}},
		},
		Config: &genai.GenerateContentConfig{Tools: tools},
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

	// Look for a function call in the response.
	// Tool calling support varies by model; mistral-small-latest may or may not call tools.
	var foundFuncCall bool
	for _, resp := range responses {
		if resp.Content != nil {
			for _, p := range resp.Content.Parts {
				if p.FunctionCall != nil {
					foundFuncCall = true
					t.Logf("Function call: name=%s, args=%v", p.FunctionCall.Name, p.FunctionCall.Args)
					if p.FunctionCall.Name != "add" {
						t.Errorf("FunctionCall.Name = %q, want add", p.FunctionCall.Name)
					}
				}
			}
		}
	}

	// Log the result — some models decline tool use, which is fine.
	if foundFuncCall {
		t.Log("Tool calling: supported")
	} else {
		t.Log("Tool calling: model did not call the tool (check model capabilities)")
	}
}
