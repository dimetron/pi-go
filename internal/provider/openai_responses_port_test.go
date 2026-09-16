package provider

// Tests for the Responses terminal-event handling ported from adk-go v2.4.0
// model/openaimodel (PRs #1373, #1359, #1467). Each case mirrors a defect the
// adk PRs fixed: a truncated turn reported as a clean stop, a failure body
// read as a turn, a zero-value terminal event latching, and the finish
// message's placement.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3/responses"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// streamSSE writes the given SSE events as an OpenAI Responses stream.
func streamSSE(t *testing.T, events ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, e := range events {
			_, _ = w.Write([]byte("event: response.stream\n"))
			_, _ = w.Write([]byte("data: " + strings.ReplaceAll(e, "\n", "\ndata: ") + "\n\n"))
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestResponsesModel(t *testing.T, baseURL string) *openaiModel {
	t.Helper()
	ctx := context.Background()
	llm, err := NewOpenAI(ctx, "gpt-5-codex", "sk-test", baseURL, nil)
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	return llm.(*openaiModel)
}

func collectResponsesStream(t *testing.T, m *openaiModel) ([]*model.LLMResponse, []error) {
	t.Helper()
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}
	var resps []*model.LLMResponse
	var errs []error
	for resp, err := range m.GenerateContent(context.Background(), req, true) {
		if resp != nil {
			resps = append(resps, resp)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return resps, errs
}

func terminalOf(t *testing.T, resps []*model.LLMResponse) *model.LLMResponse {
	t.Helper()
	for _, r := range resps {
		if r.TurnComplete {
			return r
		}
	}
	t.Fatalf("no terminal (TurnComplete) response in %d responses", len(resps))
	return nil
}

// eventCompleted is a well-formed response.completed event.
func eventCompleted(status, reason string, text string) string {
	id := json.RawMessage{}
	_ = id
	incompleteDetails := "null"
	if reason != "" {
		incompleteDetails = `{"reason": "` + reason + `"}`
	}
	return `{"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":1.0,` +
		`"status":"` + status + `","model":"gpt-5-codex","error":null,"incomplete_details":` + incompleteDetails + `,` +
		`"instructions":null,"metadata":{},"tool_choice":"auto","tools":[],"parallel_tool_calls":true,` +
		`"output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed",` +
		`"content":[{"type":"output_text","text":"` + text + `","annotations":[]}]}],` +
		`"usage":{"input_tokens":3,"output_tokens":5,"total_tokens":8}}}`
}

// TestResponsesStreamTruncatedIncomplete covers defect 1: a response.incomplete
// with no reason must not read as a clean STOP.
func TestResponsesStreamTruncatedIncomplete(t *testing.T) {
	srv := streamSSE(t,
		`{"type":"response.output_text.delta","delta":"partial an"}`,
		`{"type":"response.incomplete","response":{"id":"resp_1","object":"response","created_at":1.0,`+
			`"status":"incomplete","model":"gpt-5-codex","error":null,"incomplete_details":null,`+
			`"instructions":null,"metadata":{},"tool_choice":"auto","tools":[],"parallel_tool_calls":true,`+
			`"output":[{"type":"message","id":"msg_1","role":"assistant","status":"incomplete",`+
			`"content":[{"type":"output_text","text":"partial an","annotations":[]}]}],`+
			`"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`,
	)
	m := newTestResponsesModel(t, srv.URL)
	resps, errs := collectResponsesStream(t, m)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	final := terminalOf(t, resps)
	if final.FinishReason != genai.FinishReasonOther {
		t.Errorf("FinishReason = %v, want OTHER (truncated turn must not read as STOP)", final.FinishReason)
	}
	if msg, ok := final.CustomMetadata[ResponsesFinishMessageKey].(string); !ok || msg != "incomplete" {
		t.Errorf("CustomMetadata[%s] = %v, want \"incomplete\"", ResponsesFinishMessageKey, final.CustomMetadata[ResponsesFinishMessageKey])
	}
}

// TestResponsesStreamIncompleteMaxTokens covers the reason-bearing shape:
// incomplete_details.reason "max_output_tokens" maps to MAX_TOKENS.
func TestResponsesStreamIncompleteMaxTokens(t *testing.T) {
	srv := streamSSE(t,
		`{"type":"response.output_text.delta","delta":"par"}`,
		`{"type":"response.incomplete","response":{"id":"resp_1","object":"response","created_at":1.0,`+
			`"status":"incomplete","model":"gpt-5-codex","error":null,`+
			`"incomplete_details":{"reason":"max_output_tokens"},`+
			`"instructions":null,"metadata":{},"tool_choice":"auto","tools":[],"parallel_tool_calls":true,`+
			`"output":[{"type":"message","id":"msg_1","role":"assistant","status":"incomplete",`+
			`"content":[{"type":"output_text","text":"par","annotations":[]}]}],`+
			`"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}}`,
	)
	m := newTestResponsesModel(t, srv.URL)
	resps, errs := collectResponsesStream(t, m)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	final := terminalOf(t, resps)
	if final.FinishReason != genai.FinishReasonMaxTokens {
		t.Errorf("FinishReason = %v, want MAX_TOKENS", final.FinishReason)
	}
}

// TestResponsesStreamCompletedCleanStop covers the happy path: a completed
// event with a full message reads as STOP.
func TestResponsesStreamCompletedCleanStop(t *testing.T) {
	srv := streamSSE(t,
		`{"type":"response.output_text.delta","delta":"hello"}`,
		eventCompleted("completed", "", "hello"),
	)
	m := newTestResponsesModel(t, srv.URL)
	resps, errs := collectResponsesStream(t, m)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	final := terminalOf(t, resps)
	if final.FinishReason != genai.FinishReasonStop {
		t.Errorf("FinishReason = %v, want STOP", final.FinishReason)
	}
	if final.CustomMetadata != nil {
		t.Errorf("CustomMetadata = %v, want nil on a clean stop", final.CustomMetadata)
	}
}

// TestResponsesStreamCompletedContentFilter covers defect 2: a content-filtered
// turn that still carries text reports SAFETY plus the provider's wording.
func TestResponsesStreamCompletedContentFilter(t *testing.T) {
	srv := streamSSE(t,
		`{"type":"response.output_text.delta","delta":"some text"}`,
		eventCompleted("completed", "content_filter", "some text"),
	)
	m := newTestResponsesModel(t, srv.URL)
	resps, errs := collectResponsesStream(t, m)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	final := terminalOf(t, resps)
	if final.FinishReason != genai.FinishReasonSafety {
		t.Errorf("FinishReason = %v, want SAFETY", final.FinishReason)
	}
	if final.ErrorCode != "" {
		t.Errorf("ErrorCode = %q, want empty (turn carries an answer)", final.ErrorCode)
	}
	if msg, ok := final.CustomMetadata[ResponsesFinishMessageKey].(string); !ok || msg != "content_filter" {
		t.Errorf("CustomMetadata[%s] = %v, want \"content_filter\"", ResponsesFinishMessageKey, final.CustomMetadata[ResponsesFinishMessageKey])
	}
}

// TestResponsesStreamEmptyTerminalEvent covers defect 3: a
// response.completed whose response object never decoded must not latch over a
// well-formed event behind it, nor zero out a turn's finish reason.
func TestResponsesStreamEmptyTerminalEvent(t *testing.T) {
	srv := streamSSE(t,
		`{"type":"response.output_text.delta","delta":"hi"}`,
		`{"type":"response.completed"}`,
		`{"type":"response.completed","response":{"id":"resp_2","object":"response","created_at":1.0,`+
			`"status":"completed","model":"gpt-5-codex","error":null,"incomplete_details":null,`+
			`"instructions":null,"metadata":{},"tool_choice":"auto","tools":[],"parallel_tool_calls":true,`+
			`"output":[{"type":"message","id":"msg_2","role":"assistant","status":"completed",`+
			`"content":[{"type":"output_text","text":"hi","annotations":[]}]}],`+
			`"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
	)
	m := newTestResponsesModel(t, srv.URL)
	resps, errs := collectResponsesStream(t, m)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	final := terminalOf(t, resps)
	if final.FinishReason != genai.FinishReasonStop {
		t.Errorf("FinishReason = %v, want STOP from the well-formed event", final.FinishReason)
	}
}

// TestResponsesStreamFailedEvent covers the response.failed event: the turn
// ends as an error, quoting the server's message.
func TestResponsesStreamFailedEvent(t *testing.T) {
	srv := streamSSE(t,
		`{"type":"response.output_text.delta","delta":"star"}`,
		`{"type":"response.failed","response":{"id":"resp_9","object":"response","created_at":1.0,`+
			`"status":"failed","model":"gpt-5-codex","error":{"code":"server_error","message":"boom"},`+
			`"incomplete_details":null,"instructions":null,"metadata":{},"tool_choice":"auto",`+
			`"tools":[],"parallel_tool_calls":true,"output":[],`+
			`"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}}`,
	)
	m := newTestResponsesModel(t, srv.URL)
	resps, errs := collectResponsesStream(t, m)
	if len(errs) == 0 {
		t.Fatalf("want an error from a response.failed event, got %d responses", len(resps))
	}
	if !strings.Contains(errs[0].Error(), "boom") {
		t.Errorf("error = %q, want it to quote the server message", errs[0].Error())
	}
	if !strings.Contains(errs[0].Error(), "resp_9") {
		t.Errorf("error = %q, want it to quote the response id", errs[0].Error())
	}
}

// TestResponsesNonStreamingFailedBody covers the blocking-path counterpart of
// reportsFailure: a failed body on HTTP 200 is a failure, not a turn.
func TestResponsesNonStreamingFailedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "resp_fail", "object": "response", "created_at": 1.0, "status": "failed",
			"model": "gpt-5-codex",
			"error": {"code": "rate_limit", "message": "slow down"},
			"incomplete_details": null, "instructions": null, "metadata": {},
			"tool_choice": "auto", "tools": [], "parallel_tool_calls": true,
			"output": [],
			"usage": {"input_tokens": 1, "output_tokens": 0, "total_tokens": 1}
		}`))
	}))
	defer srv.Close()

	m := newTestResponsesModel(t, srv.URL)
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}
	var gotErr error
	for _, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			gotErr = err
		}
	}
	if gotErr == nil {
		t.Fatal("want an error from a failed response body")
	}
	if !strings.Contains(gotErr.Error(), "slow down") {
		t.Errorf("error = %q, want the server message quoted", gotErr.Error())
	}
}

// TestResponsesStreamNoTerminalEvent covers defect 9: a stream that ends
// without any terminal event reports Unspecified, not a fabricated STOP.
func TestResponsesStreamNoTerminalEvent(t *testing.T) {
	srv := streamSSE(t,
		`{"type":"response.output_text.delta","delta":"partial"}`,
	)
	m := newTestResponsesModel(t, srv.URL)
	resps, errs := collectResponsesStream(t, m)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	final := terminalOf(t, resps)
	if final.FinishReason != genai.FinishReasonUnspecified {
		t.Errorf("FinishReason = %v, want UNSPECIFIED (the model never said why)", final.FinishReason)
	}
}

// TestCarriesResponseRejectsBareObject pins the zero-value gate.
func TestCarriesResponseRejectsBareObject(t *testing.T) {
	var empty responses.Response
	if err := json.Unmarshal([]byte(`{}`), &empty); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if carriesResponse(&empty) {
		t.Error("carriesResponse(bare {}) = true, want false")
	}
	var full responses.Response
	if err := json.Unmarshal([]byte(`{"id":"resp_1","status":"completed"}`), &full); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !carriesResponse(&full) {
		t.Error("carriesResponse(populated) = false, want true")
	}
}

// TestResponsesTruncated pins the truncated() decision table from adk-go.
func TestResponsesTruncated(t *testing.T) {
	tests := []struct {
		name            string
		status          string
		incompleteEvent bool
		incompleteSet   bool
		want            bool
	}{
		{"completed", "completed", false, false, false},
		{"empty status, no details", "", false, false, false},
		{"empty status, details present", "", false, true, true},
		{"incomplete status", "incomplete", false, false, true},
		{"in_progress", "in_progress", false, false, true},
		{"failed", "failed", false, false, true},
		{"event name wins", "completed", true, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body string
			if tt.incompleteSet {
				body = `{"id":"r","status":"` + tt.status + `","incomplete_details":{}}`
			} else {
				body = `{"id":"r","status":"` + tt.status + `"}`
			}
			var resp responses.Response
			if err := json.Unmarshal([]byte(body), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := responsesTruncated(&resp, tt.incompleteEvent); got != tt.want {
				t.Errorf("responsesTruncated(%q, event=%v) = %v, want %v", tt.status, tt.incompleteEvent, got, tt.want)
			}
		})
	}
}

// TestClipServerText pins the cap and its marker.
func TestClipServerText(t *testing.T) {
	if got := clipServerText("  hi  "); got != "hi" {
		t.Errorf("clipServerText trimmed = %q, want %q", got, "hi")
	}
	long := strings.Repeat("x", 300)
	got := clipServerText(long)
	if len([]rune(got)) != maxServerTextRunes+1 {
		t.Errorf("clipped length = %d runes, want %d", len([]rune(got)), maxServerTextRunes+1)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("clipped = %q, want a truncation marker", got)
	}
}

// TestFailedResponseErrorRendering pins the labeled-parenthetical renderer.
func TestFailedResponseErrorRendering(t *testing.T) {
	var resp responses.Response
	if err := json.Unmarshal([]byte(`{"id":"resp_x","status":"failed","error":{"code":"c1","message":"m1"}}`), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := failedResponseError(&resp).Error()
	want := `openai response failed (id "resp_x", code "c1"): "m1"`
	if got != want {
		t.Errorf("failedResponseError = %q, want %q", got, want)
	}

	var bare responses.Response
	if err := json.Unmarshal([]byte(`{"status":"failed"}`), &bare); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := failedResponseError(&bare).Error(); got != "openai response failed" {
		t.Errorf("bare = %q, want the bare message", got)
	}
}
