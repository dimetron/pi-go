//go:build e2e

package provider

// E2E tests for the OpenAI Responses path against the live API, using
// gpt-5.6-luna — a Responses-only model, so every test here exercises the
// streaming/non-streaming code that the adk-go v2.4.0 port changed
// (terminal-event handling, finish reasons, tool calls). Requires
// OPENAI_API_KEY.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/retry"
)

// e2eOpenAIModel is the model the OpenAI e2e suite drives. It must stay in
// modelNeedsResponses (internal/provider/openai_responses.go) so the tests
// cover the Responses path, not Chat Completions.
const e2eOpenAIModel = "gpt-5.6-luna"

func testGetOpenAIAPIKey(t *testing.T) string {
	t.Helper()
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("skipping: OPENAI_API_KEY not set")
	}
	return key
}

func newE2EOpenAI(t *testing.T) model.LLM {
	t.Helper()
	llm, err := NewOpenAI(context.Background(), e2eOpenAIModel, testGetOpenAIAPIKey(t), "", nil)
	if err != nil {
		t.Fatalf("NewOpenAI(%s) error: %v", e2eOpenAIModel, err)
	}
	return llm
}

func e2eTerminalOf(t *testing.T, resps []*model.LLMResponse) *model.LLMResponse {
	t.Helper()
	for _, r := range resps {
		if r.TurnComplete {
			return r
		}
	}
	t.Fatalf("no terminal (TurnComplete) response among %d", len(resps))
	return nil
}

// TestE2EOpenAIResponsesNonStreaming covers the blocking path end to end: a
// well-formed completed body must read as a clean STOP with real text.
func TestE2EOpenAIResponsesNonStreaming(t *testing.T) {
	e2eOpenAICreditsAvailable(t)

	llm := newE2EOpenAI(t)

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "What is 2+2? Answer with just the number."}}},
		},
	}
	var resps []*model.LLMResponse
	for resp, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		resps = append(resps, resp)
	}
	if len(resps) == 0 {
		t.Fatal("expected at least one response")
	}
	final := e2eTerminalOf(t, resps)

	if final.FinishReason != genai.FinishReasonStop {
		t.Errorf("FinishReason = %v, want STOP on a clean completed body", final.FinishReason)
	}
	if final.UsageMetadata == nil || final.UsageMetadata.PromptTokenCount == 0 {
		t.Errorf("UsageMetadata = %+v, want non-zero prompt tokens", final.UsageMetadata)
	}

	var text strings.Builder
	if final.Content != nil {
		for _, p := range final.Content.Parts {
			text.WriteString(p.Text)
		}
	}
	if !strings.Contains(text.String(), "4") {
		t.Errorf("answer text = %q, want it to contain 4", text.String())
	}
}

// TestE2EOpenAIResponsesStreaming covers the streamed path: partial deltas, a
// TurnComplete final carrying the terminal event's finish reason (STOP), usage
// from the terminal event, and content that round-trips.
func TestE2EOpenAIResponsesStreaming(t *testing.T) {
	e2eOpenAICreditsAvailable(t)

	llm := newE2EOpenAI(t)

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Count from 1 to 3. Reply with just the numbers separated by spaces."}}},
		},
	}
	var resps []*model.LLMResponse
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("GenerateContent streaming error: %v", err)
		}
		resps = append(resps, resp)
	}
	if len(resps) < 2 {
		t.Fatalf("expected partials plus a final, got %d responses", len(resps))
	}
	final := e2eTerminalOf(t, resps)

	if final.FinishReason != genai.FinishReasonStop {
		t.Errorf("FinishReason = %v, want STOP from the response.completed event", final.FinishReason)
	}
	if final.UsageMetadata == nil || final.UsageMetadata.CandidatesTokenCount == 0 {
		t.Errorf("UsageMetadata = %+v, want non-zero output tokens from the terminal event", final.UsageMetadata)
	}

	var fullText strings.Builder
	for _, resp := range resps {
		if resp.Content != nil {
			for _, p := range resp.Content.Parts {
				fullText.WriteString(p.Text)
			}
		}
	}
	if !strings.Contains(fullText.String(), "1") || !strings.Contains(fullText.String(), "3") {
		t.Errorf("streamed text = %q, want the counted numbers", fullText.String())
	}
}

// TestE2EOpenAIResponsesToolCall covers function calling on the Responses path:
// the model must call the declared tool with parseable arguments and the call
// ID the streaming accumulator or the terminal event carries.
func TestE2EOpenAIResponsesToolCall(t *testing.T) {
	e2eOpenAICreditsAvailable(t)

	llm := newE2EOpenAI(t)

	tools := []*genai.Tool{{
		FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name:        "get_weather",
			Description: "Get the current weather for a city",
			ParametersJsonSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
				"required": []any{"city"},
			},
		}},
	}}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "What is the weather in Paris? Use the get_weather tool."}}},
		},
		Config: &genai.GenerateContentConfig{Tools: tools},
	}

	// Streaming, so the tool call arrives through the accumulator and the
	// terminal event's output_item list — exactly the pairing the adk PRs
	// harden.
	var resps []*model.LLMResponse
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("GenerateContent streaming error: %v", err)
		}
		resps = append(resps, resp)
	}
	final := e2eTerminalOf(t, resps)

	var call *genai.FunctionCall
	if final.Content != nil {
		for _, p := range final.Content.Parts {
			if p.FunctionCall != nil {
				call = p.FunctionCall
				break
			}
		}
	}
	if call == nil {
		t.Fatalf("no function call in the terminal response; parts: %+v", final.Content)
	}
	if call.Name != "get_weather" {
		t.Errorf("FunctionCall.Name = %q, want get_weather", call.Name)
	}
	if call.ID == "" {
		t.Error("FunctionCall.ID is empty — the tool result could not be matched back")
	}
	if len(call.Args) == 0 {
		t.Errorf("FunctionCall.Args = %v, want a city argument", call.Args)
	} else if _, ok := call.Args["city"].(string); !ok {
		t.Errorf("FunctionCall.Args[city] = %v, want a string", call.Args["city"])
	}
}

// TestE2EOpenAIResponsesToolRoundTrip drives the full call→response→answer
// loop: the model calls the tool, the result is fed back, and the model answers
// in text. This is the path oaiResponsesPairedIDs keeps pairable.
func TestE2EOpenAIResponsesToolRoundTrip(t *testing.T) {
	e2eOpenAICreditsAvailable(t)

	llm := newE2EOpenAI(t)

	tools := []*genai.Tool{{
		FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name:        "meaning_of_life",
			Description: "Returns the meaning of life, the universe and everything",
			ParametersJsonSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		}},
	}}

	// Turn 1: ask, expect the call.
	req1 := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "What is the meaning of life? Use the meaning_of_life tool."}}},
		},
		Config: &genai.GenerateContentConfig{Tools: tools},
	}
	var contents []*genai.Content
	contents = append(contents, req1.Contents...)

	var call *genai.FunctionCall
	for resp, err := range llm.GenerateContent(context.Background(), req1, false) {
		if err != nil {
			t.Fatalf("turn 1 error: %v", err)
		}
		if resp.Content != nil {
			contents = append(contents, resp.Content)
			for _, p := range resp.Content.Parts {
				if p.FunctionCall != nil {
					call = p.FunctionCall
				}
			}
		}
	}
	if call == nil {
		t.Fatal("turn 1 produced no function call")
	}

	// Turn 2: feed the tool result back, expect a text answer.
	contents = append(contents, &genai.Content{
		Role: "user",
		Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{
			ID:       call.ID,
			Name:     call.Name,
			Response: map[string]any{"result": "42"},
		}}},
	})
	req2 := &model.LLMRequest{Contents: contents, Config: &genai.GenerateContentConfig{Tools: tools}}
	var answered bool
	for resp, err := range llm.GenerateContent(context.Background(), req2, false) {
		if err != nil {
			t.Fatalf("turn 2 error: %v", err)
		}
		if resp.Content != nil {
			for _, p := range resp.Content.Parts {
				if strings.Contains(p.Text, "42") {
					answered = true
				}
			}
		}
	}
	if !answered {
		t.Error("turn 2 did not answer with the tool result (42)")
	}
}

// TestE2EOpenAIResponsesMultiTurn covers conversation continuation: a second
// request replaying the first exchange must be answered coherently — the
// paired-ID replay path and the store=false default.
func TestE2EOpenAIResponsesMultiTurn(t *testing.T) {
	e2eOpenAICreditsAvailable(t)

	llm := newE2EOpenAI(t)

	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "My favorite color is teal. Remember it."}}},
		{Role: "model", Parts: []*genai.Part{{Text: "Noted: teal."}}},
		{Role: "user", Parts: []*genai.Part{{Text: "What is my favorite color? One word."}}},
	}
	req := &model.LLMRequest{Contents: contents}
	var answer strings.Builder
	for resp, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		if resp.Content != nil {
			for _, p := range resp.Content.Parts {
				answer.WriteString(p.Text)
			}
		}
	}
	if !strings.Contains(strings.ToLower(answer.String()), "teal") {
		t.Errorf("answer = %q, want it to recall teal", answer.String())
	}
}

// TestE2EOpenAIResponsesSystemInstruction covers instructions threading.
func TestE2EOpenAIResponsesSystemInstruction(t *testing.T) {
	e2eOpenAICreditsAvailable(t)

	llm := newE2EOpenAI(t)

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Say hello."}}},
		},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: "Always answer in exactly five words."}},
			},
		},
	}
	var text strings.Builder
	for resp, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		if resp.Content != nil {
			for _, p := range resp.Content.Parts {
				text.WriteString(p.Text)
			}
		}
	}
	// The synthesized phase label ("[phase: final_answer]", added by
	// parseResponsesOutput for codex-style responses) is not the model's
	// words — strip it before counting.
	answer := strings.ReplaceAll(text.String(), "[phase: final_answer]", "")
	words := strings.Fields(strings.TrimSpace(answer))
	if len(words) != 5 {
		t.Errorf("answer = %q (%d words), want exactly five words per the instruction", answer, len(words))
	}
}

// TestE2EOpenAIResponsesReasoningEffort covers the ThinkingConfig→reasoning
// effort mapping on the Responses path: high effort must be accepted and still
// answer. Reasoning output itself is the model's call — logged, not asserted.
func TestE2EOpenAIResponsesReasoningEffort(t *testing.T) {
	e2eOpenAICreditsAvailable(t)

	llm := newE2EOpenAI(t)

	budget := int32(9000)
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "What is 17*23? Just the number."}}},
		},
		Config: &genai.GenerateContentConfig{
			ThinkingConfig: &genai.ThinkingConfig{ThinkingBudget: &budget},
		},
	}
	var text strings.Builder
	for resp, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		if resp.Content != nil {
			for _, p := range resp.Content.Parts {
				text.WriteString(p.Text)
			}
		}
	}
	if !strings.Contains(text.String(), "391") {
		t.Errorf("answer = %q, want 391", text.String())
	}
}

// TestE2EOpenAIResponsesStreamingWithTools is the combination the port changed
// most: streamed text and a tool call in one turn, with the terminal event's
// finish reason and usage riding on the final response.
func TestE2EOpenAIResponsesStreamingWithTools(t *testing.T) {
	e2eOpenAICreditsAvailable(t)

	llm := newE2EOpenAI(t)

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
			{Role: "user", Parts: []*genai.Part{{Text: "Use the add tool to compute 3+5."}}},
		},
		Config: &genai.GenerateContentConfig{Tools: tools},
	}
	var resps []*model.LLMResponse
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("GenerateContent streaming error: %v", err)
		}
		resps = append(resps, resp)
	}
	final := e2eTerminalOf(t, resps)

	var call *genai.FunctionCall
	if final.Content != nil {
		for _, p := range final.Content.Parts {
			if p.FunctionCall != nil {
				call = p.FunctionCall
			}
		}
	}
	if call == nil {
		t.Fatalf("no function call in streamed terminal response; parts: %+v", final.Content)
	}
	if call.Name != "add" {
		t.Errorf("FunctionCall.Name = %q, want add", call.Name)
	}
	if call.ID == "" {
		t.Error("FunctionCall.ID is empty")
	}
	if a, ok := call.Args["a"].(float64); !ok || a != 3 {
		t.Errorf("FunctionCall.Args[a] = %v, want 3", call.Args["a"])
	}
	if b, ok := call.Args["b"].(float64); !ok || b != 5 {
		t.Errorf("FunctionCall.Args[b] = %v, want 5", call.Args["b"])
	}
}

// TestE2EOpenAIResponsesErrorEventInsufficientQuota pins how a stream-level
// error event surfaces. The live API emits it with code/message nested in an
// "error" object — the shape the credit_balance_exhausted failure produced.
// openai-go's ssestream intercepts any event carrying an "error" key and
// terminates the stream with a StreamError before our event loop sees it, so
// the error arrives through the STREAM_ERROR wrapper carrying the provider's
// own wording, and the retry classifier must call it terminal (no retries
// against an empty credit balance).
func TestE2EOpenAIResponsesErrorEventInsufficientQuota(t *testing.T) {
	srv := streamSSE(t,
		`{"type":"response.created","response":{"id":"resp_q","object":"response","created_at":1.0,"status":"in_progress","model":"gpt-5.6-luna","error":null,"incomplete_details":null,"output":[],"usage":null}}`,
		`{"type":"error","error":{"type":"insufficient_quota","code":"credit_balance_exhausted","message":"You have no credits remaining. Add credits to continue using the API."}}`,
		`{"type":"response.failed","response":{"id":"resp_q","object":"response","created_at":1.0,"status":"failed","model":"gpt-5.6-luna","error":{"code":"credit_balance_exhausted","message":"You have no credits remaining."},"incomplete_details":null,"output":[],"usage":null}}`,
	)
	m := newTestResponsesModel(t, srv.URL)
	resps, errs := collectResponsesStream(t, m)
	if len(errs) != 0 || len(resps) != 1 {
		t.Fatalf("want one STREAM_ERROR response, got %d responses and %d errors", len(resps), len(errs))
	}
	first := resps[0]
	if first.ErrorCode != "STREAM_ERROR" {
		t.Errorf("ErrorCode = %q, want STREAM_ERROR", first.ErrorCode)
	}
	if !strings.Contains(first.ErrorMessage, "credit_balance_exhausted") {
		t.Errorf("ErrorMessage = %q, want the nested error object quoted", first.ErrorMessage)
	}
	if !strings.Contains(first.ErrorMessage, "no credits remaining") {
		t.Errorf("ErrorMessage = %q, want the provider's message", first.ErrorMessage)
	}
	// Terminal classification keeps the retry budget away from an empty
	// credit balance — a retried call can only fail the same way.
	if retry.IsTransient(errors.New(first.ErrorMessage)) {
		t.Errorf("IsTransient(credit_balance_exhausted) = true, want false (terminal)")
	}
}

// TestE2EOpenAIResponsesFailedStreamIsNotATurn pins that a response.failed
// event ends the turn as an error instead of the pre-port behavior — a
// fabricated TurnComplete/STOP on a stream that never produced anything.
func TestE2EOpenAIResponsesFailedStreamIsNotATurn(t *testing.T) {
	srv := streamSSE(t,
		`{"type":"response.created","response":{"id":"resp_q","object":"response","created_at":1.0,"status":"in_progress","model":"gpt-5.6-luna","error":null,"incomplete_details":null,"output":[],"usage":null}}`,
		`{"type":"response.failed","response":{"id":"resp_q","object":"response","created_at":1.0,"status":"failed","model":"gpt-5.6-luna","error":{"code":"credit_balance_exhausted","message":"You have no credits remaining."},"incomplete_details":null,"output":[],"usage":null}}`,
	)
	m := newTestResponsesModel(t, srv.URL)
	resps, errs := collectResponsesStream(t, m)
	if len(errs) == 0 {
		t.Fatal("want an error from the response.failed event")
	}
	if !strings.Contains(errs[0].Error(), "credit_balance_exhausted") {
		t.Errorf("error = %q, want the failure quoted", errs[0].Error())
	}
	for _, r := range resps {
		if r.TurnComplete {
			t.Error("a failed stream must not yield a TurnComplete response")
		}
	}
}
