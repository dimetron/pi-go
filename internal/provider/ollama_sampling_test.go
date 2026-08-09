package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// clearSamplingEnv pins every sampling var to empty, so the test is
// deterministic on a machine where the operator has set them for real.
func clearSamplingEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"PI_OLLAMA_REPEAT_PENALTY",
		"PI_OLLAMA_REPEAT_LAST_N",
		"PI_OLLAMA_PRESENCE_PENALTY",
		"PI_OLLAMA_FREQUENCY_PENALTY",
	} {
		t.Setenv(k, "")
	}
}

func TestOllamaSamplingOptions_UnsetSendsNothing(t *testing.T) {
	clearSamplingEnv(t)
	if got := ollamaSamplingOptions(); len(got) != 0 {
		t.Errorf("unset must send nothing so Ollama's defaults stay in force, got %v", got)
	}
}

func TestOllamaSamplingOptions_SetValues(t *testing.T) {
	clearSamplingEnv(t)
	t.Setenv("PI_OLLAMA_REPEAT_PENALTY", "1.25")
	t.Setenv("PI_OLLAMA_REPEAT_LAST_N", "512")
	t.Setenv("PI_OLLAMA_PRESENCE_PENALTY", "0.5")
	t.Setenv("PI_OLLAMA_FREQUENCY_PENALTY", "-0.25")

	got := ollamaSamplingOptions()
	want := map[string]any{
		"repeat_penalty":    1.25,
		"repeat_last_n":     512,
		"presence_penalty":  0.5,
		"frequency_penalty": -0.25,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d options, want %d: %v", len(got), len(want), got)
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %v (%T), want %v (%T)", k, got[k], got[k], w, w)
		}
	}
}

// A typo in an env var must not take down a session that would otherwise run.
func TestOllamaSamplingOptions_GarbageIgnored(t *testing.T) {
	clearSamplingEnv(t)
	t.Setenv("PI_OLLAMA_REPEAT_PENALTY", "not-a-number")
	t.Setenv("PI_OLLAMA_REPEAT_LAST_N", "1.5") // not an int
	t.Setenv("PI_OLLAMA_PRESENCE_PENALTY", "0.3")

	got := ollamaSamplingOptions()
	if _, ok := got["repeat_penalty"]; ok {
		t.Error("unparseable float must be dropped, not sent")
	}
	if _, ok := got["repeat_last_n"]; ok {
		t.Error("unparseable int must be dropped, not sent")
	}
	if got["presence_penalty"] != 0.3 {
		t.Errorf("a valid sibling must still be sent, got %v", got["presence_penalty"])
	}
}

// repeat_last_n = 0 disables the penalty window and -1 means num_ctx; both are
// meaningful to Ollama, so neither may be swallowed as "unset".
func TestOllamaSamplingOptions_ZeroAndNegativeAreMeaningful(t *testing.T) {
	clearSamplingEnv(t)
	t.Setenv("PI_OLLAMA_REPEAT_LAST_N", "0")
	if got := ollamaSamplingOptions()["repeat_last_n"]; got != 0 {
		t.Errorf("repeat_last_n=0 must be sent, got %v", got)
	}
	t.Setenv("PI_OLLAMA_REPEAT_LAST_N", "-1")
	if got := ollamaSamplingOptions()["repeat_last_n"]; got != -1 {
		t.Errorf("repeat_last_n=-1 must be sent, got %v", got)
	}
}

func TestOllamaChatRequest_CarriesSamplingOptions(t *testing.T) {
	clearSamplingEnv(t)
	t.Setenv("PI_OLLAMA_REPEAT_PENALTY", "1.3")
	t.Setenv("PI_OLLAMA_REPEAT_LAST_N", "256")

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
		t.Fatal("request carried no options")
	}
	if got, ok := gotOptions["repeat_penalty"].(float64); !ok || got != 1.3 {
		t.Errorf("repeat_penalty = %v, want 1.3", gotOptions["repeat_penalty"])
	}
	if got, ok := gotOptions["repeat_last_n"].(float64); !ok || int(got) != 256 {
		t.Errorf("repeat_last_n = %v, want 256", gotOptions["repeat_last_n"])
	}
	// The existing output cap must survive the merge.
	if got, ok := gotOptions["num_predict"].(float64); !ok || int(got) != defaultOllamaNumPredict {
		t.Errorf("num_predict = %v, want %d", gotOptions["num_predict"], defaultOllamaNumPredict)
	}
}

// With nothing configured the request must look exactly as it did before this
// feature existed: the cap, and no sampling keys at all.
func TestOllamaChatRequest_NoSamplingKeysWhenUnset(t *testing.T) {
	clearSamplingEnv(t)

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

	for _, k := range []string{"repeat_penalty", "repeat_last_n", "presence_penalty", "frequency_penalty"} {
		if _, ok := gotOptions[k]; ok {
			t.Errorf("%s must not be sent when unset", k)
		}
	}
}
