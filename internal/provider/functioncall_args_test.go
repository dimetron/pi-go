package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestMarshalFunctionCallArgsNeverNull(t *testing.T) {
	if got := string(marshalFunctionCallArgs(nil)); got != "{}" {
		t.Errorf("nil args marshaled to %q, want {}", got)
	}
	if got := string(marshalFunctionCallArgs(map[string]any{})); got != "{}" {
		t.Errorf("empty args marshaled to %q, want {}", got)
	}
	got := string(marshalFunctionCallArgs(map[string]any{"command": "ls"}))
	if got != `{"command":"ls"}` {
		t.Errorf("args marshaled to %q", got)
	}
}

func TestNewFunctionCallPartArgsNeverNil(t *testing.T) {
	p := newFunctionCallPart("bash", nil)
	if p.FunctionCall == nil {
		t.Fatal("expected a FunctionCall")
	}
	if p.FunctionCall.Args == nil {
		t.Error("Args is nil; a nil map replays as tool_use.input null and 400s Anthropic")
	}
}

// TestReplayedToolCallWithNilArgsSendsEmptyObject is the regression the guard
// exists for: a turn holding a tool call whose arguments never parsed must
// replay as `"arguments":"{}"`, not `"arguments":"null"`. A gateway forwards
// the latter to Anthropic as `"input": null`, which is rejected — and since
// the bad turn stays in history, every later request fails the same way.
func TestReplayedToolCallWithNilArgsSendsEmptyObject(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "c1", "object": "chat.completion", "model": "m",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "done"},
				"finish_reason": "stop",
			}},
		})
	}))
	defer srv.Close()

	llm, err := NewOpenAI(context.Background(), "m", "sk-test", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewOpenAI() error: %v", err)
	}

	call := &genai.Part{FunctionCall: &genai.FunctionCall{ID: "call_1", Name: "write", Args: nil}}
	result := &genai.Part{FunctionResponse: &genai.FunctionResponse{
		ID: "call_1", Name: "write", Response: map[string]any{"error": "file_path is required"},
	}}
	req := &model.LLMRequest{Contents: []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "write a file"}}},
		{Role: "model", Parts: []*genai.Part{call}},
		{Role: "user", Parts: []*genai.Part{result}},
	}}
	for _, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent() error: %v", err)
		}
	}

	var sent struct {
		Messages []struct {
			ToolCalls []struct {
				Function struct {
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decoding request: %v", err)
	}
	var seen int
	for _, msg := range sent.Messages {
		for _, tc := range msg.ToolCalls {
			seen++
			if tc.Function.Arguments != "{}" {
				t.Errorf("replayed arguments = %q, want {}", tc.Function.Arguments)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no tool call was replayed")
	}
}

func TestOaiMaxOutputTokensPrefersRequestConfig(t *testing.T) {
	if got := oaiMaxOutputTokens(nil, 64000); got != 64000 {
		t.Errorf("nil config = %d, want the fallback 64000", got)
	}
	if got := oaiMaxOutputTokens(&genai.GenerateContentConfig{}, 64000); got != 64000 {
		t.Errorf("unset MaxOutputTokens = %d, want the fallback 64000", got)
	}
	cfg := &genai.GenerateContentConfig{MaxOutputTokens: 1234}
	if got := oaiMaxOutputTokens(cfg, 64000); got != 1234 {
		t.Errorf("caller's MaxOutputTokens = %d, want 1234", got)
	}
}

// TestChatCompletionsSendsMaxCompletionTokens pins the fix for replies cut off
// at a server-chosen default: agentgateway in front of Anthropic settles on
// 4096 when the client sends nothing, which truncates a long turn and, when
// the cut lands inside a tool call's arguments, produces a call with no
// arguments at all.
func TestChatCompletionsSendsMaxCompletionTokens(t *testing.T) {
	tests := []struct {
		name string
		opts *LLMOptions
		want float64
	}{
		{"default", nil, float64(defaultOaiMaxOutputTokens)},
		{"configured", &LLMOptions{MaxOutputTokens: 8192}, 8192},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id": "c1", "object": "chat.completion", "model": "m",
					"choices": []map[string]any{{
						"index":         0,
						"message":       map[string]any{"role": "assistant", "content": "ok"},
						"finish_reason": "stop",
					}},
				})
			}))
			defer srv.Close()

			llm, err := NewOpenAI(context.Background(), "m", "sk-test", srv.URL, tt.opts)
			if err != nil {
				t.Fatalf("NewOpenAI() error: %v", err)
			}
			req := &model.LLMRequest{Contents: []*genai.Content{
				{Role: "user", Parts: []*genai.Part{{Text: "hi"}}},
			}}
			for _, err := range llm.GenerateContent(context.Background(), req, false) {
				if err != nil {
					t.Fatalf("GenerateContent() error: %v", err)
				}
			}

			var sent map[string]any
			if err := json.Unmarshal(body, &sent); err != nil {
				t.Fatalf("decoding request: %v", err)
			}
			got, ok := sent["max_completion_tokens"].(float64)
			if !ok {
				t.Fatalf("request has no max_completion_tokens: %s", body)
			}
			if got != tt.want {
				t.Errorf("max_completion_tokens = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestAgentGatewaySendsLegacyMaxTokens pins the fix for Ollama ignoring
// max_completion_tokens: the agentgateway provider must send max_tokens
// (the legacy field) instead, or Ollama runs unbounded and hits its own
// 65536-token default — which is what truncated session 260906-1320.
func TestAgentGatewaySendsLegacyMaxTokens(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "c1", "object": "chat.completion", "model": "m",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "ok"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer srv.Close()

	llm, err := NewAgentGateway(context.Background(), "ollama-deepseek", "", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewAgentGateway() error: %v", err)
	}
	req := &model.LLMRequest{Contents: []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "hi"}}},
	}}
	for _, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent() error: %v", err)
		}
	}

	var sent map[string]any
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decoding request: %v", err)
	}
	// max_tokens must be present (the legacy field Ollama understands).
	got, ok := sent["max_tokens"].(float64)
	if !ok {
		t.Fatalf("request has no max_tokens (Ollama would run unbounded): %s", body)
	}
	if got != float64(defaultOaiMaxOutputTokens) {
		t.Errorf("max_tokens = %v, want %v", got, defaultOaiMaxOutputTokens)
	}
	// max_completion_tokens must NOT be present — Ollama ignores it, and
	// sending both is at best redundant and at worst ambiguous.
	if _, ok := sent["max_completion_tokens"]; ok {
		t.Errorf("request has max_completion_tokens, should only send max_tokens for agentgateway")
	}
}

// TestRoutesToOllama pins the split between the gateway's Ollama routes, which
// need the legacy max_tokens field, and every other route, which speaks
// max_completion_tokens.
//
// The cases are real model ids read from a running gateway's /v1/models, not
// invented ones. pi-fast is the regression: it used to be Ollama-first and was
// listed by name, so when the gateway re-routed it to openai/gpt-5.6-luna the
// name alone kept it on max_tokens. A name list cannot describe where a route
// goes; the `ollama` prefix can, because the gateway names the routes it sends
// to Ollama with it.
func TestRoutesToOllama(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		// Ollama backends (owner: ollama).
		{"ollama-cloud/deepseek-v4.1-flash:cloud", true},
		{"ollama/glm-5.3-flash:cloud", true},
		{"ollama1/deepseek-v4-flash:0731-cloud", true},
		{"OLLAMA-cloud/Kimi-K2.6", true}, // case-insensitive
		{"  ollama-cloud/glm-5.3:cloud  ", true},

		// Virtual models over Ollama (owner: virtual, prefix ollama).
		{"ollama-deepseek", true},
		{"ollama-deepseek-balanced", true},
		{"ollama-gemma4", true},
		{"ollama-glm-flash", true},
		{"ollama-glm53", true},
		{"ollama-minimax", true},
		{"ollama-spark", true},
		{"ollama-dsf4", true},

		// Modern routes. pi-fast is the regression: the gateway routes it to
		// openai/gpt-5.6-luna and gemini/gemini-3.8-flash, neither of which
		// understands the legacy field.
		{"pi-fast", false},
		{"pi-default", false},
		{"openai/gpt-5.6-luna", false},
		{"openai/gpt-6-luna", false},
		{"gpt-5.6-luna", false},
		{"gpt-6-luna", false},
		{"anthropic/claude-opus-5", false},
		{"gemini/gemini-3.8-flash", false},
		{"openrouter/openai/gpt-5.6-luna", false},
		{"opencode/gpt-5.6-luna", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := routesToOllama(tt.model); got != tt.want {
			t.Errorf("routesToOllama(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

// TestAgentGatewayRouteFieldOnTheWire drives the whole constructor per route
// and asserts on the request body, because a correct predicate that is not
// wired into NewAgentGateway proves nothing. pi-fast is the case that failed:
// it took the legacy branch while resolving to OpenAI and Gemini.
func TestAgentGatewayRouteFieldOnTheWire(t *testing.T) {
	tests := []struct {
		model      string
		wantLegacy bool
		wantModern bool
		wantOutput bool
	}{
		// Responses-only models: the ceiling rides as max_output_tokens, which
		// is that API's name for it and not the legacy field.
		{"openai/gpt-6-luna", false, false, true},
		{"openai/gpt-5.6-luna", false, false, true},
		// Chat Completions on a modern backend.
		{"pi-fast", false, true, false},
		{"pi-default", false, true, false},
		{"anthropic/claude-opus-5", false, true, false},
		{"pi-fast-something-else", false, true, false},
		// Ollama: the legacy field it actually understands.
		{"ollama-cloud/deepseek-v4.1-flash:cloud", true, false, false},
		{"ollama-deepseek", true, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			var body []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id": "c1", "object": "chat.completion", "model": "m",
					"choices": []map[string]any{{
						"index":         0,
						"message":       map[string]any{"role": "assistant", "content": "ok"},
						"finish_reason": "stop",
					}},
					"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1},
				})
			}))
			defer srv.Close()

			llm, err := NewAgentGateway(context.Background(), tt.model, "", srv.URL, nil)
			if err != nil {
				t.Fatalf("NewAgentGateway() error: %v", err)
			}
			req := &model.LLMRequest{Contents: []*genai.Content{
				{Role: "user", Parts: []*genai.Part{{Text: "hi"}}},
			}}
			for _, err := range llm.GenerateContent(context.Background(), req, false) {
				if err != nil {
					t.Fatalf("GenerateContent() error: %v", err)
				}
			}

			var sent map[string]any
			if err := json.Unmarshal(body, &sent); err != nil {
				t.Fatalf("decoding request: %v", err)
			}
			_, legacy := sent["max_tokens"]
			_, modern := sent["max_completion_tokens"]
			_, output := sent["max_output_tokens"]
			if legacy != tt.wantLegacy {
				t.Errorf("max_tokens present = %v, want %v (body: %s)", legacy, tt.wantLegacy, body)
			}
			if modern != tt.wantModern {
				t.Errorf("max_completion_tokens present = %v, want %v (body: %s)", modern, tt.wantModern, body)
			}
			if output != tt.wantOutput {
				t.Errorf("max_output_tokens present = %v, want %v (body: %s)", output, tt.wantOutput, body)
			}
		})
	}
}

// TestAgentGatewayNonOllamaRouteSendsModernMaxTokens pins that legacy mode is
// scoped to the gateway's Ollama routes, not forced provider-wide. A route that
// resolves to a non-Ollama backend (here anthropic/) must send the modern
// max_completion_tokens field, not the deprecated max_tokens.
func TestAgentGatewayNonOllamaRouteSendsModernMaxTokens(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "c1", "object": "chat.completion", "model": "m",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "ok"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer srv.Close()

	llm, err := NewAgentGateway(context.Background(), "anthropic/claude-opus-5", "", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewAgentGateway() error: %v", err)
	}
	req := &model.LLMRequest{Contents: []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "hi"}}},
	}}
	for _, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent() error: %v", err)
		}
	}

	var sent map[string]any
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decoding request: %v", err)
	}
	// max_completion_tokens must be present (the modern field non-Ollama
	// backends understand).
	if _, ok := sent["max_completion_tokens"]; !ok {
		t.Fatalf("request has no max_completion_tokens: %s", body)
	}
	// max_tokens must NOT be present — it is deprecated on non-Ollama backends.
	if _, ok := sent["max_tokens"]; ok {
		t.Errorf("request has legacy max_tokens, should only send max_completion_tokens for non-Ollama agentgateway routes")
	}
}
