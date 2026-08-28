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

func testGetMistralAPIKey(t *testing.T) string {
	key := os.Getenv("MISTRAL_API_KEY")
	if key == "" {
		t.Skip("skipping: MISTRAL_API_KEY not set")
	}
	return key
}

func TestE2EMistralSmallNonStreaming(t *testing.T) {
	key := testGetMistralAPIKey(t)

	llm, err := NewMistral(context.Background(), "mistral-small-latest", key, "", "", nil)
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

	llm, err := NewMistral(context.Background(), "mistral-small-latest", key, "", "", nil)
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

	llm, err := NewMistral(context.Background(), "mistral-small-latest", key, "", "", nil)
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

	llm, err := NewMistral(context.Background(), "mistral-small-latest", key, "", "", nil)
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

// mistralReasoningE2EModel is the model these reasoning tests drive. Mistral
// documents reasoning_effort for the small and medium reasoning models; the
// small one is what the rest of this file already exercises.
const mistralReasoningE2EModel = "mistral-small-latest"

// TestE2EMistralReasoningStreaming asserts that a streaming turn with thinking
// turned on surfaces thinking-role partials before the answer, and that the
// answer text is intact — the thinking chunks turn content into a JSON array,
// so a regression here shows up as raw JSON in the transcript.
//
// Whether the account's model actually emits thinking is the provider's call:
// no thinking is logged, not failed. Raw JSON leaking into the answer is a
// failure either way.
func TestE2EMistralReasoningStreaming(t *testing.T) {
	key := testGetMistralAPIKey(t)

	llm, err := NewMistral(context.Background(), mistralReasoningE2EModel, key, "", "high", nil)
	if err != nil {
		t.Fatalf("NewMistral() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "A farmer has 17 sheep; all but 9 run away. How many are left? Think it through."}}},
		},
	}

	var thinking, answer strings.Builder
	var last *model.LLMResponse
	var sawThinkingBeforeAnswer bool
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		last = resp
		if !resp.Partial || resp.Content == nil || len(resp.Content.Parts) == 0 {
			continue
		}
		text := resp.Content.Parts[0].Text
		if resp.Content.Role == "thinking" {
			if answer.Len() == 0 {
				sawThinkingBeforeAnswer = true
			}
			thinking.WriteString(text)
			continue
		}
		answer.WriteString(text)
	}

	if last == nil || !last.TurnComplete {
		t.Fatal("expected a final TurnComplete response")
	}
	if answer.Len() == 0 {
		t.Fatal("expected non-empty answer text")
	}
	if strings.HasPrefix(strings.TrimSpace(answer.String()), "[{") {
		t.Errorf("answer looks like a raw Mistral chunk array, not text: %q", answer.String())
	}
	if thinking.Len() == 0 {
		t.Logf("model emitted no thinking for this turn (provider's choice); answer: %s", answer.String())
		return
	}
	if !sawThinkingBeforeAnswer {
		t.Error("thinking arrived only after the answer had started")
	}
	t.Logf("thinking (%d chars): %s", thinking.Len(), thinking.String())
}

// TestE2EMistralNonStreamingThinking is the non-streaming half: thinking, when
// the model emits it, arrives as its own part ahead of the answer part.
func TestE2EMistralNonStreamingThinking(t *testing.T) {
	key := testGetMistralAPIKey(t)

	llm, err := NewMistral(context.Background(), mistralReasoningE2EModel, key, "", "high", nil)
	if err != nil {
		t.Fatalf("NewMistral() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "What is 12 * 12? Reason step by step, then answer."}}},
		},
	}

	var last *model.LLMResponse
	for resp, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		last = resp
	}

	if last == nil || last.Content == nil || len(last.Content.Parts) == 0 {
		t.Fatal("expected content with parts")
	}
	for i, p := range last.Content.Parts {
		if strings.HasPrefix(strings.TrimSpace(p.Text), "[{") {
			t.Errorf("part %d is a raw Mistral chunk array, not text: %q", i, p.Text)
		}
	}
	if len(last.Content.Parts) < 2 {
		t.Logf("model returned a single part (no thinking emitted): %s", last.Content.Parts[0].Text)
		return
	}
	t.Logf("thinking part: %s", last.Content.Parts[0].Text)
	if last.Content.Parts[len(last.Content.Parts)-1].Text == "" {
		t.Error("expected a non-empty answer part after the thinking part")
	}
}

// TestE2EMistralPromptCacheKeyStable sends two turns through one instance. The
// key itself is unit-tested; what this pins is that Mistral accepts the field —
// an unknown body field would come back as a 422 on the first call.
func TestE2EMistralPromptCacheKeyStable(t *testing.T) {
	key := testGetMistralAPIKey(t)

	llm, err := NewMistral(context.Background(), mistralReasoningE2EModel, key, "", "", nil)
	if err != nil {
		t.Fatalf("NewMistral() error: %v", err)
	}

	ask := func(question string) {
		t.Helper()
		req := &model.LLMRequest{
			Contents: []*genai.Content{
				{Role: "user", Parts: []*genai.Part{{Text: question}}},
			},
		}
		var last *model.LLMResponse
		for resp, err := range llm.GenerateContent(context.Background(), req, false) {
			if err != nil {
				t.Fatalf("GenerateContent error: %v", err)
			}
			last = resp
		}
		if last == nil || last.Content == nil || len(last.Content.Parts) == 0 {
			t.Fatal("expected content with parts")
		}
		if last.Content.Parts[len(last.Content.Parts)-1].Text == "" {
			t.Error("expected non-empty text response")
		}
	}

	ask("Name one primary colour. One word.")
	ask("Name another primary colour. One word.")
}

// TestE2EMistralReasoningEffortAccepted pins the wire vocabulary: Mistral
// documents exactly "high" and "none" for reasoning_effort, so both must be
// accepted by the live API. A 422 here means the mapping drifted.
func TestE2EMistralReasoningEffortAccepted(t *testing.T) {
	key := testGetMistralAPIKey(t)

	for _, level := range []string{"none", "high"} {
		t.Run(level, func(t *testing.T) {
			llm, err := NewMistral(context.Background(), mistralReasoningE2EModel, key, "", level, nil)
			if err != nil {
				t.Fatalf("NewMistral() error: %v", err)
			}
			req := &model.LLMRequest{
				Contents: []*genai.Content{
					{Role: "user", Parts: []*genai.Part{{Text: "Say OK."}}},
				},
			}
			for _, err := range llm.GenerateContent(context.Background(), req, false) {
				if err != nil {
					t.Fatalf("thinking level %q rejected by Mistral: %v", level, err)
				}
			}
		})
	}
}

// TestE2EMistralStreamingWithTools covers the path an interactive session
// actually drives: streaming *and* tools together. The existing tool test is
// non-streaming, so a tool call that survives a plain request but is lost or
// mangled while being reassembled from stream deltas would not have been
// caught. A tool call missing its id cannot be matched to its result, which
// ends the turn early with no error — so each field is asserted, not just the
// call's presence.
func TestE2EMistralStreamingWithTools(t *testing.T) {
	key := testGetMistralAPIKey(t)

	llm, err := NewMistral(context.Background(), "mistral-small-latest", key, "", "", nil)
	if err != nil {
		t.Fatalf("NewMistral() error: %v", err)
	}

	tools := []*genai.Tool{{
		FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name:        "read",
			Description: "Read a file from the project",
			ParametersJsonSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string"},
				},
				"required": []any{"file_path"},
			},
		}},
	}}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Read the file README.md using the read tool."}}},
		},
		Config: &genai.GenerateContentConfig{Tools: tools},
	}

	var calls []*genai.FunctionCall
	var final *model.LLMResponse
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		final = resp
		if resp.Partial || resp.Content == nil {
			continue
		}
		for _, p := range resp.Content.Parts {
			if p.FunctionCall != nil {
				calls = append(calls, p.FunctionCall)
			}
		}
	}

	if final == nil || !final.TurnComplete {
		t.Fatal("expected a final TurnComplete response")
	}
	if len(calls) == 0 {
		// Tool calling is the model's choice; the assertions below are what
		// this test exists for, so an unused tool is logged, not failed.
		t.Logf("model chose not to call a tool; finish reason %v", final.FinishReason)
		return
	}
	for i, c := range calls {
		if c.ID == "" {
			t.Errorf("call %d: empty ID — the result could not be matched back to the call", i)
		}
		if c.Name != "read" {
			t.Errorf("call %d: name = %q, want %q", i, c.Name, "read")
		}
		if len(c.Args) == 0 {
			t.Errorf("call %d: arguments did not survive stream reassembly", i)
		}
		t.Logf("call %d: id=%q name=%q args=%v", i, c.ID, c.Name, c.Args)
	}
}
