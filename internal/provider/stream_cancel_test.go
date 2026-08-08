package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestCanceledResponse_IsTerminal(t *testing.T) {
	resp := canceledResponse()
	if resp.Partial {
		t.Error("a cancellation event must not be Partial: ADK reports a partial last event as \"TODO: last event is not final\"")
	}
	if !resp.TurnComplete {
		t.Error("expected TurnComplete = true")
	}
	if resp.Content == nil {
		t.Error("Content must be non-nil or ADK skips the event before it can be the final one")
	}
	if len(resp.Content.Parts) != 0 {
		t.Errorf("expected no parts, got %d", len(resp.Content.Parts))
	}
	if resp.FinishReason == genai.FinishReasonStop {
		t.Error("a canceled turn must not report STOP")
	}
}

// TestOllamaRunStreaming_CancelYieldsTerminalEvent covers the regression
// directly: the stream is canceled after the first chunk, and the last thing
// the caller sees must be a non-partial event. Leaving a partial chunk as the
// last event is what made a canceled turn surface as ADK's internal
// "TODO: last event is not final" string.
func TestOllamaRunStreaming_CancelYieldsTerminalEvent(t *testing.T) {
	srv := newMockOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		writeNDJSON(w, []any{
			ollamaChatLine("test", "assistant", "thinking out loud", "", "", false, 0, 0, nil),
		})
		// Hold the connection open so the cancellation, not EOF, ends the stream.
		<-r.Context().Done()
	})

	llm := newOllamaModelFromServer(t, srv, "test", "none")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}}},
	}

	var last *model.LLMResponse
	for resp, err := range llm.GenerateContent(ctx, req, true) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		last = resp
		cancel() // cancel as soon as the first chunk lands
	}

	if last == nil {
		t.Fatal("expected at least one response")
	}
	if last.Partial {
		t.Error("last event after cancellation is still Partial -- ADK will abort with \"TODO: last event is not final\"")
	}
	if !last.TurnComplete {
		t.Error("expected the cancellation event to complete the turn")
	}
}

func TestOllamaNumPredict_DefaultAndOverride(t *testing.T) {
	if got := ollamaNumPredict(); got != defaultOllamaNumPredict {
		t.Errorf("default = %d, want %d", got, defaultOllamaNumPredict)
	}
	t.Setenv("PI_OLLAMA_NUM_PREDICT", "512")
	if got := ollamaNumPredict(); got != 512 {
		t.Errorf("override = %d, want 512", got)
	}
	t.Setenv("PI_OLLAMA_NUM_PREDICT", "0")
	if got := ollamaNumPredict(); got != 0 {
		t.Errorf("0 must disable the cap, got %d", got)
	}
	t.Setenv("PI_OLLAMA_NUM_PREDICT", "not-a-number")
	if got := ollamaNumPredict(); got != defaultOllamaNumPredict {
		t.Errorf("garbage must fall back to the default, got %d", got)
	}
}

func TestOllamaChatRequest_CarriesNumPredict(t *testing.T) {
	var gotOptions map[string]any
	srv := newMockOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotOptions, _ = body["options"].(map[string]any)
		w.Header().Set("Content-Type", "application/x-ndjson")
		writeNDJSON(w, []any{
			ollamaChatLine("test", "assistant", "hi", "", "stop", true, 1, 1, nil),
		})
	})

	llm := newOllamaModelFromServer(t, srv, "test", "none")
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}}},
	}
	for range llm.GenerateContent(context.Background(), req, true) { //nolint:revive // drain
	}

	if gotOptions == nil {
		t.Fatal("request carried no options: the output cap was not sent")
	}
	if got, ok := gotOptions["num_predict"].(float64); !ok || int(got) != defaultOllamaNumPredict {
		t.Errorf("num_predict = %v, want %d", gotOptions["num_predict"], defaultOllamaNumPredict)
	}
}
