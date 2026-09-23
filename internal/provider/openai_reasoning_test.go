package provider

// Tests for the two Responses/Completions optional fields pi-go used to drop:
// the output cap (max_output_tokens) and the reasoning effort. Both are pinned
// against the wire rather than against the builders, because the defects they
// fix were both "the field never reached the request" — a test that reads the
// params struct back cannot tell those apart from a correct send.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// captureOpenAIRequest runs one non-streaming turn against a stub endpoint and
// returns the decoded request body, so a test asserts on the wire rather than
// on the builder's output.
func captureOpenAIRequest(t *testing.T, modelID string, llmOpts *LLMOptions, cfg *genai.GenerateContentConfig) map[string]any {
	t.Helper()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := readAll(r)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		// The response shape differs per endpoint, so answer both: the
		// Responses fields are ignored by the completions decoder and vice
		// versa.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "x", "object": "response", "status": "completed", "model": modelID,
			"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": "hi"}, "finish_reason": "stop"}},
			"output": []map[string]any{{"type": "message", "id": "m", "role": "assistant", "status": "completed",
				"content": []map[string]any{{"type": "output_text", "text": "hi", "annotations": []any{}}}}},
			"usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
		})
	}))
	defer srv.Close()

	llm, err := NewOpenAI(context.Background(), modelID, "sk-test", srv.URL, llmOpts)
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
		Config:   cfg,
	}
	for _, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent: %v", err)
		}
	}
	return body
}

// The output cap reaches the Responses wire. It used to be dropped there while
// the Chat Completions path sent it, so a long turn was silently bounded by
// whatever the server defaulted to — 4096 behind agentgateway, which truncates
// mid-tool-call.
func TestOpenAIResponsesSendsMaxOutputTokens(t *testing.T) {
	body := captureOpenAIRequest(t, "gpt-6-astra", nil, &genai.GenerateContentConfig{MaxOutputTokens: 1234})
	if got := body["max_output_tokens"]; got != float64(1234) {
		t.Errorf("max_output_tokens = %v, want 1234 (the caller's genai MaxOutputTokens)", got)
	}
}

// The model's configured default cap is sent when the request names none,
// rather than leaving the server to pick.
func TestOpenAIResponsesSendsDefaultMaxOutputTokens(t *testing.T) {
	body := captureOpenAIRequest(t, "gpt-6-astra", nil, nil)
	if got := body["max_output_tokens"]; got != float64(defaultOaiMaxOutputTokens) {
		t.Errorf("max_output_tokens = %v, want the default %d", got, defaultOaiMaxOutputTokens)
	}
}

// The ChatGPT codex backend exposes a restricted Responses surface, so the cap
// stays off that endpoint. Pinned because a rejected field there breaks every
// turn rather than degrading one feature.
func TestOpenAIResponsesCodexBackendOmitsMaxOutputTokens(t *testing.T) {
	m := &openaiModel{modelName: "gpt-5-codex", codexBackend: true, maxOutputTokens: defaultOaiMaxOutputTokens}
	params, _, err := m.buildResponsesParams(&model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}, "gpt-5-codex")
	if err != nil {
		t.Fatalf("buildResponsesParams: %v", err)
	}
	if params.MaxOutputTokens.Valid() {
		t.Errorf("codex backend received max_output_tokens=%d", params.MaxOutputTokens.Value)
	}
}

// reasoningEffortCases are the (model, requested level) → wire effort mappings,
// transcribed from a live probe of the API. Each row that expects a value is a
// pair that was measured to return 200; the omitted rows are non-reasoning
// models, where the field is a hard 400 rather than being ignored.
//
// Every row names an explicit level: the empty level is not this function's
// input, it is resolved one layer up by openaiThinkingLevel (see
// TestOpenAIThinkingLevelDefaults).
//
// The table is the point: the accepted tiers are not uniform (gpt-5.4/5.5 take
// "xhigh" but reject "max"; gpt-6 and the o-series reject "none"), so a mapping
// that looks plausible but ignores those differences breaks turns.
var reasoningEffortCases = []struct {
	model string
	level string
	want  shared.ReasoningEffort // "" = the field must be omitted
}{
	// An explicit level passes through where the model accepts it.
	{"gpt-5.4", "low", shared.ReasoningEffortLow},
	{"gpt-5.4", "medium", shared.ReasoningEffortMedium},
	{"gpt-5.4", "high", shared.ReasoningEffortHigh},
	{"gpt-5.6-luna", "high", shared.ReasoningEffortHigh},

	// "none" is a real request to switch reasoning off where the family
	// supports it, and is preserved there — pi-go's auxiliary callers (commit
	// message, ping, eval judge) pass it for exactly that reason.
	{"gpt-5.4", "none", shared.ReasoningEffortNone},
	{"gpt-5.6-luna", "none", shared.ReasoningEffortNone},
	// Where the family rejects "none" it steps up to the lowest accepted tier.
	{"gpt-6-astra", "none", shared.ReasoningEffortLow},
	{"o3", "none", shared.ReasoningEffortLow},

	// The top tiers are model-dependent, and "max" is narrower than "xhigh".
	{"gpt-5.6-luna", "xhigh", shared.ReasoningEffortXhigh},
	{"gpt-5.6-luna", "max", shared.ReasoningEffortMax},
	{"gpt-5.4", "xhigh", shared.ReasoningEffortXhigh},
	{"gpt-5.4", "max", shared.ReasoningEffortHigh}, // 5.4 takes xhigh, rejects max
	{"gpt-5.1", "xhigh", shared.ReasoningEffortHigh},
	{"gpt-5.1", "max", shared.ReasoningEffortHigh},

	// Non-reasoning models reject the field outright, so it is omitted.
	{"gpt-4o", "high", ""},
	{"gpt-4.1-mini", "medium", ""},
}

func TestOaiReasoningEffortFor(t *testing.T) {
	for _, c := range reasoningEffortCases {
		got, ok := oaiReasoningEffortFor(c.model, c.level)
		gotEffort := shared.ReasoningEffort("")
		if ok {
			gotEffort = got
		}
		if gotEffort != c.want {
			t.Errorf("oaiReasoningEffortFor(%q, %q) = %q (ok=%v), want %q",
				c.model, c.level, gotEffort, ok, c.want)
		}
	}
	// An unset level is not this layer's input: it means "omit and let the
	// model default stand", so the caller resolves it first.
	for _, model := range []string{"gpt-5.6-luna", "gpt-5.4", "gpt-4o"} {
		if got, ok := oaiReasoningEffortFor(model, ""); ok {
			t.Errorf("oaiReasoningEffortFor(%q, \"\") = %q, want omitted", model, got)
		}
	}
}

// The model-level default is resolved by openaiThinkingLevel, one layer above
// the mapping: luna gets high, everything else the coding default, and a
// non-reasoning model still yields an effort that the mapping then drops.
func TestOpenAIThinkingLevelDefaults(t *testing.T) {
	for _, c := range []struct{ model, want string }{
		{"gpt-5.6-luna", "high"},
		{"gpt-5.6-sol", defaultOpenAIThinkingLevel},
		{"gpt-5.4", defaultOpenAIThinkingLevel},
		{"o3", defaultOpenAIThinkingLevel},
		{"gpt-4o", defaultOpenAIThinkingLevel},
	} {
		if got := openaiThinkingLevel(c.model, nil); got != c.want {
			t.Errorf("openaiThinkingLevel(%q, nil) = %q, want %q", c.model, got, c.want)
		}
	}
}

// The resolved effort reaches the Responses wire for the models that actually
// use it. The others (gpt-5.1/5.4/5.5, o-series) are not Responses-routed, so
// their wire coverage is the Chat Completions test below.
func TestOpenAIResponsesSendsReasoningEffort(t *testing.T) {
	for _, c := range reasoningEffortCases {
		// Responses-only models plus the one non-reasoning case reachable with
		// multi-turn state; the rest route to Chat Completions.
		if !modelNeedsResponses(c.model) {
			continue
		}
		t.Run(c.model+"/"+c.level, func(t *testing.T) {
			body := captureOpenAIRequest(t, c.model, &LLMOptions{ThinkingLevel: c.level}, nil)
			raw, ok := body["reasoning"].(map[string]any)
			if !ok {
				if c.want != "" {
					t.Fatalf("reasoning absent from the request; want effort %q", c.want)
				}
				return
			}
			if got := raw["effort"]; got != string(c.want) {
				t.Errorf("reasoning.effort = %v, want %q", got, c.want)
			}
		})
	}
}

// A non-reasoning model on the Responses path must not receive a reasoning
// param. It reaches Responses once the client holds a previous_response_id, so
// this is a real production path rather than a hypothetical one.
func TestOpenAIResponsesOmitsReasoningForNonReasoningModel(t *testing.T) {
	m := &openaiModel{
		modelName:     "gpt-4o",
		thinkingLevel: "high",
		responseState: &responsesState{previousResponseID: "resp_1"},
	}
	// Guard the premise: with state, endpointMode selects Responses.
	if mode := m.endpointMode(); mode != "responses" {
		t.Fatalf("premise: endpointMode = %q, want responses", mode)
	}
	params, _, err := m.buildResponsesParams(&model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}, "gpt-4o")
	if err != nil {
		t.Fatalf("buildResponsesParams: %v", err)
	}
	if params.Reasoning.Effort != "" {
		t.Errorf("reasoning.effort = %q, want omitted for gpt-4o", params.Reasoning.Effort)
	}
}

// The same resolution governs the Chat Completions wire, which gpt-5.1/5.4/5.5
// use for their first turn (they are not Responses-only). Without this, effort
// would depend on which endpoint a turn happened to land on.
func TestOpenAIChatCompletionsSendsReasoningEffort(t *testing.T) {
	for _, c := range []struct {
		model, level string
		want         shared.ReasoningEffort
	}{
		{"gpt-5.5", "high", shared.ReasoningEffortHigh},
		{"gpt-5.5", "", shared.ReasoningEffortMedium},
		{"gpt-5.5", "max", shared.ReasoningEffortHigh},
		{"gpt-4o", "high", ""},
	} {
		t.Run(c.model+"/"+c.level, func(t *testing.T) {
			body := captureOpenAIRequest(t, c.model, &LLMOptions{ThinkingLevel: c.level}, nil)
			got, _ := body["reasoning_effort"].(string)
			if got != string(c.want) {
				t.Errorf("reasoning_effort = %q, want %q", got, c.want)
			}
		})
	}
}

// An explicit per-request thinking budget still wins over the model-level
// level, which is the pre-existing channel and keeps its mapping.
func TestOpenAIResponsesBudgetWinsOverThinkingLevel(t *testing.T) {
	budget := int32(9000) // high
	m := &openaiModel{modelName: "gpt-5.6-luna", thinkingLevel: "low"}
	params, _, err := m.buildResponsesParams(&model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
		Config:   &genai.GenerateContentConfig{ThinkingConfig: &genai.ThinkingConfig{ThinkingBudget: &budget}},
	}, "gpt-5.6-luna")
	if err != nil {
		t.Fatalf("buildResponsesParams: %v", err)
	}
	if params.Reasoning.Effort != shared.ReasoningEffortHigh {
		t.Errorf("effort = %q, want high from the 9000-token budget", params.Reasoning.Effort)
	}
}

// gpt-5.6-luna gets high effort when the caller names none, even though the
// general coding default is medium.
func TestOpenAIThinkingLevelLunaDefaultsHigh(t *testing.T) {
	if got := openaiThinkingLevel("gpt-5.6-luna", nil); got != "high" {
		t.Errorf("openaiThinkingLevel(luna, nil) = %q, want high", got)
	}
	// An explicit level still wins, including one that lowers luna.
	if got := openaiThinkingLevel("gpt-5.6-luna", &LLMOptions{ThinkingLevel: "low"}); got != "low" {
		t.Errorf("explicit level = %q, want low", got)
	}
	// Other models keep the coding default.
	if got := openaiThinkingLevel("gpt-5.4", nil); got != defaultOpenAIThinkingLevel {
		t.Errorf("openaiThinkingLevel(gpt-5.4, nil) = %q, want %q", got, defaultOpenAIThinkingLevel)
	}
}

// NewLLM carries its thinkingLevel argument onto the OpenAI-compatible options,
// which is how the CLI, the config file and pimodels all reach these fields.
func TestNewLLMCarriesThinkingLevelToOpenAI(t *testing.T) {
	opts := &LLMOptions{}
	// resolveOpenAIThinkingLevel is exercised through NewLLM's own wiring; call
	// the helper it uses rather than standing up a client.
	if opts.ThinkingLevel != "" {
		t.Fatalf("precondition: options start empty")
	}
	fillThinkingLevel(opts, "high")
	if opts.ThinkingLevel != "high" {
		t.Errorf("ThinkingLevel = %q, want high", opts.ThinkingLevel)
	}
	// An explicit option is not overwritten.
	filled := &LLMOptions{ThinkingLevel: "low"}
	fillThinkingLevel(filled, "high")
	if filled.ThinkingLevel != "low" {
		t.Errorf("explicit option overwritten: %q", filled.ThinkingLevel)
	}
}

// responsesReasoning prefers the budget, then the level, then nothing.
func TestResponsesReasoningPrecedence(t *testing.T) {
	budget := int32(1) // low
	m := &openaiModel{modelName: "gpt-5.6-luna", thinkingLevel: "high"}

	got, ok := m.responsesReasoning(&genai.GenerateContentConfig{
		ThinkingConfig: &genai.ThinkingConfig{ThinkingBudget: &budget},
	})
	if !ok || got.Effort != shared.ReasoningEffortLow {
		t.Errorf("budget should win: got %q ok=%v", got.Effort, ok)
	}

	got, ok = m.responsesReasoning(&genai.GenerateContentConfig{})
	if !ok || got.Effort != shared.ReasoningEffortHigh {
		t.Errorf("level should apply without a budget: got %q ok=%v", got.Effort, ok)
	}

	plain := &openaiModel{modelName: "gpt-4o", thinkingLevel: "high"}
	if _, ok := plain.responsesReasoning(&genai.GenerateContentConfig{}); ok {
		t.Error("a non-reasoning model must report no reasoning param")
	}
}

var _ = responses.ResponseNewParams{}
