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
