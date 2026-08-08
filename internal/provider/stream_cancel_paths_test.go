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

// sseHangingServer streams one chunk of an event stream and then holds the
// connection open, so the only thing that can end the stream is the client
// canceling it. That is the shape that produced the ADK
// "TODO: last event is not final" abort.
func sseHangingServer(t *testing.T, chunks ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		for _, c := range chunks {
			_, _ = fmt.Fprint(w, c)
			if ok {
				flusher.Flush()
			}
		}
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// assertCancelTerminates drains a stream, canceling as soon as the first
// response arrives, and asserts the caller was left with a terminal event
// rather than a dangling partial one.
func assertCancelTerminates(ctx context.Context, t *testing.T, llm model.LLM, cancel context.CancelFunc) {
	t.Helper()
	var last *model.LLMResponse
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}}},
	}
	for resp, err := range llm.GenerateContent(ctx, req, true) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		last = resp
		cancel()
	}
	if last == nil {
		t.Fatal("expected at least one response before cancellation")
	}
	if last.Partial {
		t.Error("last event after cancellation is Partial -- ADK aborts such a turn with \"TODO: last event is not final\"")
	}
	if !last.TurnComplete {
		t.Error("expected the cancellation event to complete the turn")
	}
	if last.FinishReason == genai.FinishReasonStop {
		t.Error("a canceled turn must not report STOP")
	}
}

const antSSEPrelude = `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"thinking out loud"}}

`

func TestAnthropicStreaming_CancelYieldsTerminalEvent(t *testing.T) {
	srv := sseHangingServer(t, antSSEPrelude)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	llm, err := NewAnthropic(ctx, "claude-sonnet-4-6", "sk-test", srv.URL, "none", nil)
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	assertCancelTerminates(ctx, t, llm, cancel)
}

// The beta path is a separate streaming implementation with its own copy of
// the cancellation branch, so it needs its own regression test. Configuring an
// advisor model is what routes the request through the beta API.
func TestAnthropicBetaStreaming_CancelYieldsTerminalEvent(t *testing.T) {
	srv := sseHangingServer(t, antSSEPrelude)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := &LLMOptions{AdvisorModel: "claude-opus-4-7"}
	llm, err := NewAnthropic(ctx, "claude-sonnet-4-6", "sk-test", srv.URL, "high", opts)
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	assertCancelTerminates(ctx, t, llm, cancel)
}

func TestOpenAICompletions_CancelYieldsTerminalEvent(t *testing.T) {
	chunk := `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"thinking out loud"},"finish_reason":null}]}` + "\n\n"
	srv := sseHangingServer(t, chunk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	llm, err := NewOpenAI(ctx, "gpt-4o", "sk-test", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	assertCancelTerminates(ctx, t, llm, cancel)
}

func TestOpenAIResponses_CancelYieldsTerminalEvent(t *testing.T) {
	chunk := `event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":0,"item_id":"msg_1","content_index":0,"delta":"thinking out loud","sequence_number":1}

`
	srv := sseHangingServer(t, chunk)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// gpt-5-codex routes through the Responses endpoint.
	llm, err := NewOpenAI(ctx, "gpt-5-codex", "sk-test", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	assertCancelTerminates(ctx, t, llm, cancel)
}
