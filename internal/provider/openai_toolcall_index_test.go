package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// sseToolCallServer replays a fixed list of SSE data lines as a streaming chat
// completion.
func sseToolCallServer(t *testing.T, chunks []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, c := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", c)
			if flusher != nil {
				flusher.Flush()
			}
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
}

// collectFunctionCalls runs one streaming turn against srv and returns the
// function calls on the final response.
func collectFunctionCalls(t *testing.T, srv *httptest.Server) []*genai.FunctionCall {
	t.Helper()
	ctx := context.Background()
	llm, err := NewOpenAI(ctx, "gemini-3.8-flash", "sk-test", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewOpenAI() error: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "go"}}}},
	}
	var final *model.LLMResponse
	for resp, err := range llm.GenerateContent(ctx, req, true) {
		if err != nil {
			t.Fatalf("GenerateContent() error: %v", err)
		}
		final = resp
	}
	if final == nil || final.Content == nil {
		t.Fatal("expected a final response with content")
	}
	var calls []*genai.FunctionCall
	for _, p := range final.Content.Parts {
		if p.FunctionCall != nil {
			calls = append(calls, p.FunctionCall)
		}
	}
	return calls
}

// TestOaiStreamingParallelToolCallsWithoutIndex covers the wire shape Google's
// OpenAI-compatible endpoint emits (and that agentgateway forwards verbatim):
// each parallel tool call arrives whole, in its own chunk, with no "index"
// field inside the tool call — the "index" present belongs to the choice.
//
// Reading the missing field as its zero value collapsed all three calls onto
// slot 0 and concatenated their arguments into invalid JSON, so every call
// reached the tool with no arguments at all.
func TestOaiStreamingParallelToolCallsWithoutIndex(t *testing.T) {
	srv := sseToolCallServer(t, []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"function":{"arguments":"{\"command\":\"ls -la\"}","name":"bash"},"id":"call_1","type":"function"}]},"index":0}],"object":"chat.completion.chunk"}`,
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"function":{"arguments":"{\"command\":\"pwd\"}","name":"bash"},"id":"call_2","type":"function"}]},"index":0}],"object":"chat.completion.chunk"}`,
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"function":{"arguments":"{\"command\":\"go version\"}","name":"bash"},"id":"call_3","type":"function"}]},"index":0}],"object":"chat.completion.chunk"}`,
		`{"choices":[{"delta":{"role":"assistant"},"finish_reason":"stop","index":0}],"object":"chat.completion.chunk","usage":{"prompt_tokens":97,"completion_tokens":45,"total_tokens":206}}`,
	})
	defer srv.Close()

	calls := collectFunctionCalls(t, srv)
	if len(calls) != 3 {
		t.Fatalf("got %d function calls, want 3", len(calls))
	}
	wantIDs := []string{"call_1", "call_2", "call_3"}
	wantCmds := []string{"ls -la", "pwd", "go version"}
	for i, fc := range calls {
		if fc.Name != "bash" {
			t.Errorf("call %d name = %q, want bash", i, fc.Name)
		}
		if fc.ID != wantIDs[i] {
			t.Errorf("call %d ID = %q, want %q", i, fc.ID, wantIDs[i])
		}
		cmd, _ := fc.Args["command"].(string)
		if cmd != wantCmds[i] {
			t.Errorf("call %d command = %q, want %q", i, cmd, wantCmds[i])
		}
	}
}

// TestOaiStreamingFragmentedToolCallsWithIndex is the standard OpenAI shape:
// each call is split into fragments that share an explicit index. The index
// stays authoritative, so fragments still reassemble into whole calls.
func TestOaiStreamingFragmentedToolCallsWithIndex(t *testing.T) {
	srv := sseToolCallServer(t, []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"bash","arguments":""}}]},"index":0}],"object":"chat.completion.chunk"}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"read","arguments":""}}]},"index":0}],"object":"chat.completion.chunk"}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"command\":"}}]},"index":0}],"object":"chat.completion.chunk"}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"path\":\"/tmp\"}"}}]},"index":0}],"object":"chat.completion.chunk"}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]},"index":0}],"object":"chat.completion.chunk"}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls","index":0}],"object":"chat.completion.chunk"}`,
	})
	defer srv.Close()

	calls := collectFunctionCalls(t, srv)
	if len(calls) != 2 {
		t.Fatalf("got %d function calls, want 2", len(calls))
	}
	if calls[0].ID != "call_a" || calls[0].Name != "bash" {
		t.Errorf("call 0 = %q/%q, want call_a/bash", calls[0].ID, calls[0].Name)
	}
	if cmd, _ := calls[0].Args["command"].(string); cmd != "ls" {
		t.Errorf("call 0 command = %q, want ls", cmd)
	}
	if calls[1].ID != "call_b" || calls[1].Name != "read" {
		t.Errorf("call 1 = %q/%q, want call_b/read", calls[1].ID, calls[1].Name)
	}
	if p, _ := calls[1].Args["path"].(string); p != "/tmp" {
		t.Errorf("call 1 path = %q, want /tmp", p)
	}
}

// TestOaiToolCallSlotContinuation covers a delta that carries neither an index
// nor an ID: it continues the call before it rather than starting a new one.
func TestOaiToolCallSlotContinuation(t *testing.T) {
	s := &oaiStreamState{toolCalls: make(map[int64]map[string]any)}
	first := s.toolCallSlot(false, 0, "call_1")
	cont := s.toolCallSlot(false, 0, "")
	second := s.toolCallSlot(false, 0, "call_2")
	again := s.toolCallSlot(false, 0, "call_1")

	if cont != first {
		t.Errorf("continuation slot = %d, want %d", cont, first)
	}
	if second == first {
		t.Errorf("second call reused slot %d", second)
	}
	if again != first {
		t.Errorf("repeat of call_1 = %d, want %d", again, first)
	}
}
