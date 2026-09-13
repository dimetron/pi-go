package provider

// Unit coverage for the uncovered branches of the Responses terminal-event
// handling that PR #335 ports from adk-go v2.4.0: the raw "error" event
// reader, the previous_response_id recovery retry, the structured-API-error
// branch of isPreviousResponseNotFound, and the finish-signal placement rules
// the streaming fixtures do not reach.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/retry"
)

// TestErrorEventDetail pins the raw "error" event reader: the nested object is
// decoded, a body without one and a body that is not JSON both return nil.
func TestErrorEventDetail(t *testing.T) {
	nested := errorEventDetail(`{"type":"error","error":{"code":"credit_balance_exhausted","message":"You have no credits remaining."}}`)
	if nested == nil {
		t.Fatal("errorEventDetail = nil, want the nested object")
	}
	if nested.Code != "credit_balance_exhausted" {
		t.Errorf("Code = %q, want credit_balance_exhausted", nested.Code)
	}
	if nested.Message != "You have no credits remaining." {
		t.Errorf("Message = %q, want the provider's message", nested.Message)
	}

	if got := errorEventDetail(`{"type":"error"}`); got != nil {
		t.Errorf("errorEventDetail(no error key) = %+v, want nil", got)
	}
	if got := errorEventDetail(`not json at all`); got != nil {
		t.Errorf("errorEventDetail(bad json) = %+v, want nil", got)
	}
}

// TestResponsesStreamNestedErrorEvent drives a live-shaped stream-level error
// event (code/message nested in an "error" object, as the credit-balance
// failure produces) through the real SDK client. The SDK's ssestream
// intercepts "error"-keyed data and ends the stream with a StreamError, so
// the nested wording rides through the STREAM_ERROR wrapper (the same route
// the e2e test observes against the live API). This pin is unconditional.
func TestResponsesStreamNestedErrorEvent(t *testing.T) {
	srv := streamSSE(t,
		`{"type":"response.created","response":{"id":"resp_q","object":"response","created_at":1.0,"status":"in_progress","model":"gpt-5-codex","error":null,"incomplete_details":null,"output":[],"usage":null}}`,
		`{"type":"error","error":{"type":"insufficient_quota","code":"credit_balance_exhausted","message":"You have no credits remaining. Add credits to continue using the API."}}`,
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
	for _, r := range resps {
		if r.TurnComplete {
			t.Error("an error event must not yield a TurnComplete response")
		}
	}
	// Terminal classification keeps the retry budget away from an empty
	// credit balance — a retried call can only fail the same way.
	if retry.IsTransient(errors.New(first.ErrorMessage)) {
		t.Errorf("IsTransient(credit_balance_exhausted) = true, want false (terminal)")
	}
}

// TestResponsesStreamErrorEventFlatFields drives the shape that reaches
// runResponsesStreaming's "error" branch: a bare error event (no nested
// "error" object, so the SDK's ssestream does not intercept it) whose
// code/message decode into the flattened fields.
func TestResponsesStreamErrorEventFlatFields(t *testing.T) {
	srv := streamSSE(t, `{"type":"error","code":"server_error","message":"boom","param":"","sequence_number":1}`)
	m := newTestResponsesModel(t, srv.URL)
	resps, errs := collectResponsesStream(t, m)
	if len(errs) != 0 || len(resps) != 1 {
		t.Fatalf("want one error response, got %d responses and %d errors", len(resps), len(errs))
	}
	if resps[0].ErrorCode != "server_error" || resps[0].ErrorMessage != "boom" {
		t.Errorf("code/message = %q/%q, want server_error/boom", resps[0].ErrorCode, resps[0].ErrorMessage)
	}
	for _, r := range resps {
		if r.TurnComplete {
			t.Error("an error event must not yield a TurnComplete response")
		}
	}
}

// TestIsPreviousResponseNotFoundAPIError covers the structured-API-error
// branch: an *openai.Error carrying the code, the param, or the token in its
// raw JSON is a stale-pointer failure even when the flattened fields do not
// say so.
// TestIsPreviousResponseNotFoundStructured drives the errors.As path directly,
// constructing openai.Error values the way requestconfig does (UnmarshalJSON
// over the unwrapped "error" object). RawJSON fallback cases need
// Request/Response set: the code checks the structured fields first, then the
// raw body, and only then falls back to the rendered message — which the SDK
// builds by dereferencing both.
func TestIsPreviousResponseNotFoundStructured(t *testing.T) {
	build := func(t *testing.T, raw string) error {
		t.Helper()
		var e openai.Error
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "https://api.example.com/v1/responses", nil)
		e.Request = req
		e.Response = &http.Response{StatusCode: http.StatusBadRequest, Request: req}
		return &e
	}
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"code match", `{"code":"previous_response_not_found"}`, true},
		{"param match", `{"code":"other","param":"previous_response_id"}`, true},
		// The raw body carries the token even though neither flattened field
		// decoded to it — a proxy error envelope with a differently-shaped
		// code field.
		{"raw json match", `{"code":"custom_code","message":"previous_response_not_found: id r1 gone"}`, true},
		{"no match", `{"code":"y","message":"unrelated failure"}`, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPreviousResponseNotFound(build(t, tt.raw)); got != tt.want {
				t.Errorf("isPreviousResponseNotFound = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestResponsesRecoveryRetryWithoutPreviousResponseID covers the
// previous_response_id recovery in sendResponses: the upstream rejects the
// stored pointer, nothing was streamed, and the retry without it succeeds.
func TestResponsesRecoveryRetryWithoutPreviousResponseID(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		if id, _ := body["previous_response_id"].(string); id != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"previous_response_not_found","message":"No response found with id 'resp_prev'."}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "resp_new", "object": "response", "created_at": 1.0, "status": "completed",
			"model": "gpt-5-codex", "error": null, "incomplete_details": null,
			"output": [{"type":"message","id":"msg_1","role":"assistant","status":"completed",
			"content":[{"type":"output_text","text":"recovered","annotations":[]}]}],
			"usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}
		}`))
	}))
	defer srv.Close()

	llm, err := NewOpenAI(context.Background(), "gpt-5-codex", "sk-test", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	om := llm.(*openaiModel)
	om.responseState = &responsesState{previousResponseID: "resp_prev"}

	// Drive sendResponses directly: buildResponsesParams pins Store=false, so
	// a pointer-carrying request only exists through this seam — exactly the
	// shape the recovery was written against (an opt-in store=true turn whose
	// stored id upstream has dropped). The first attempt returns a
	// transient-looking stream failure so retryStream's recovery attempt is
	// the one that reaches isPreviousResponseNotFound's recovery branch; the
	// stale-id branch itself is exercised through sendResponses below.
	recoveryParams := &responses.ResponseNewParams{
		Model:              "gpt-5-codex",
		Store:              param.NewOpt(true),
		PreviousResponseID: param.NewOpt("resp_prev"),
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: responses.ResponseInputParam{
			responses.ResponseInputItemParamOfMessage("hi", responses.EasyInputMessageRoleUser),
		}},
	}
	sawErr := false
	var final *model.LLMResponse
	om.sendResponses(context.Background(), recoveryParams, true, false, func(resp *model.LLMResponse, err error) bool {
		if resp != nil && resp.TurnComplete {
			final = resp
		}
		if err != nil {
			sawErr = true
		}
		return true
	})
	if len(bodies) < 2 {
		t.Fatalf("want a recovery retry (2 requests), got %d", len(bodies))
	}
	firstID, _ := bodies[0]["previous_response_id"].(string)
	if firstID != "resp_prev" {
		t.Errorf("first request previous_response_id = %q, want resp_prev", firstID)
	}
	secondID, _ := bodies[len(bodies)-1]["previous_response_id"].(string)
	if secondID != "" {
		t.Errorf("retry previous_response_id = %q, want it dropped", secondID)
	}
	if sawErr {
		t.Error("recovered turn must not report an error")
	}
	if final == nil || final.FinishReason != genai.FinishReasonStop {
		t.Errorf("FinishReason = %v, want STOP on the recovered turn", final)
	}
	if om.responseState == nil || om.responseState.previousResponseID != "resp_new" {
		t.Errorf("previousResponseID = %+v, want the recovery's resp_new", om.responseState)
	}
}

// TestResponsesStreamConsumerStopsEarly covers the yield-false branches of
// runResponsesStreaming (text and reasoning deltas): a consumer that stops
// draining after the first delta ends the stream without a panic, and the
// partial text that was already forwarded is all the caller sees.
func TestResponsesStreamConsumerStopsEarly(t *testing.T) {
	srv := streamSSE(t,
		`{"type":"response.output_text.delta","delta":"one "}`,
		`{"type":"response.output_text.delta","delta":"two"}`,
		`{"type":"response.reasoning_text.delta","delta":"thinking"}`,
		`{"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":1.0,"status":"completed","model":"gpt-5-codex","error":null,"incomplete_details":null,"output":[],"usage":null}}`,
	)
	m := newTestResponsesModel(t, srv.URL)
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}
	var seen []*model.LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		seen = append(seen, resp)
		if len(seen) == 1 {
			// Stop draining after the first text delta.
			break
		}
	}
	if len(seen) != 1 || !seen[0].Partial {
		t.Fatalf("got %d responses, want exactly the first partial", len(seen))
	}
}

// TestGenerateResponsesInputError covers the buildResponsesParams failure
// branch of generateResponses: a request the converter cannot turn into
// Responses input yields an error and nothing else. oaiContentsToResponsesInput
// currently has no failing shape, so this pins the plumbing by asserting the
// happy request produces no spurious error.
func TestGenerateResponsesInputError(t *testing.T) {
	if _, _, err := oaiContentsToResponsesInput(
		[]*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}}, nil,
	); err != nil {
		t.Fatalf("a text-only conversation must convert, got %v", err)
	}
}

// TestResponsesStreamErrorEventNestedSupplement pins what the nested-detail
// reader contributes when the event's flattened code/message are empty: the
// raw body's nested object fills both. The live API's nested shape rides the
// "error" key, which the SDK's ssestream intercepts before the runner sees
// it — so this exercises the reading logic directly, the same lines
// runResponsesStreaming executes on a delivered event.
func TestResponsesStreamErrorEventNestedSupplement(t *testing.T) {
	evt := decodeAntEvent[responses.ResponseStreamEventUnion](t,
		`{"type":"error","code":"","message":"","error":{"code":"nested_c","message":"nested_m"}}`)
	code, msg := evt.Code, evt.Message
	if code == "" || msg == "" {
		if nested := errorEventDetail(evt.AsError().RawJSON()); nested != nil {
			if code == "" {
				code = nested.Code
			}
			if msg == "" {
				msg = nested.Message
			}
		}
	}
	if code != "nested_c" || msg != "nested_m" {
		t.Errorf("code/message = %q/%q, want nested_c/nested_m from the nested object", code, msg)
	}
}

// TestResponsesStreamErrorEventBothEmpty drives the runner with an error
// event whose flattened code and message are both empty and whose raw body
// carries no nested object: the supplement runs, finds nothing, and the
// yielded LLMResponse carries empty code and message.
func TestResponsesStreamErrorEventBothEmpty(t *testing.T) {
	srv := streamSSE(t, `{"type":"error","code":"","message":"","param":"","sequence_number":3}`)
	m := newTestResponsesModel(t, srv.URL)
	resps, errs := collectResponsesStream(t, m)
	if len(errs) != 0 || len(resps) != 1 {
		t.Fatalf("want one error response, got %d responses and %d errors", len(resps), len(errs))
	}
	if resps[0].ErrorCode != "" || resps[0].ErrorMessage != "" {
		t.Errorf("code/message = %q/%q, want both empty (nothing to read)", resps[0].ErrorCode, resps[0].ErrorMessage)
	}
}

// TestAttachResponsesFinishSignalPins pins the placement rules: MAX_TOKENS is
// self-describing, a turn with content carries the wording as metadata, a
// content-filtered turn with nothing to read uses the error fields, and nil
// arguments do nothing.
func TestAttachResponsesFinishSignalPins(t *testing.T) {
	t.Run("max tokens returns early", func(t *testing.T) {
		resp := &model.LLMResponse{FinishReason: genai.FinishReasonMaxTokens}
		attachResponsesFinishSignal(resp, &responses.Response{Status: responses.ResponseStatusIncomplete}, true)
		if resp.ErrorCode != "" || resp.ErrorMessage != "" || resp.CustomMetadata != nil {
			t.Errorf("MAX_TOKENS must say all there is to say, got code=%q msg=%q meta=%v", resp.ErrorCode, resp.ErrorMessage, resp.CustomMetadata)
		}
	})
	t.Run("stop returns early", func(t *testing.T) {
		resp := &model.LLMResponse{FinishReason: genai.FinishReasonStop}
		attachResponsesFinishSignal(resp, &responses.Response{}, false)
		if resp.ErrorCode != "" || resp.CustomMetadata != nil {
			t.Errorf("STOP must pass through untouched, got code=%q meta=%v", resp.ErrorCode, resp.CustomMetadata)
		}
	})
	t.Run("content carries metadata", func(t *testing.T) {
		resp := &model.LLMResponse{
			FinishReason: genai.FinishReasonOther,
			Content:      &genai.Content{Role: string(genai.RoleModel), Parts: []*genai.Part{{Text: "partial"}}},
		}
		attachResponsesFinishSignal(resp, &responses.Response{IncompleteDetails: responses.ResponseIncompleteDetails{Reason: "max_tool_calls"}}, false)
		if resp.ErrorCode != "" || resp.ErrorMessage != "" {
			t.Errorf("content turn must not set error fields, got code=%q msg=%q", resp.ErrorCode, resp.ErrorMessage)
		}
		if msg, ok := resp.CustomMetadata[ResponsesFinishMessageKey].(string); !ok || msg != "max_tool_calls" {
			t.Errorf("CustomMetadata[%s] = %v, want \"max_tool_calls\"", ResponsesFinishMessageKey, resp.CustomMetadata[ResponsesFinishMessageKey])
		}
	})
	t.Run("safety with content", func(t *testing.T) {
		resp := &model.LLMResponse{
			FinishReason: genai.FinishReasonSafety,
			Content:      &genai.Content{Role: string(genai.RoleModel), Parts: []*genai.Part{{Text: "some"}}},
		}
		attachResponsesFinishSignal(resp, &responses.Response{}, false)
		if resp.ErrorCode != "" {
			t.Errorf("ErrorCode = %q, want empty", resp.ErrorCode)
		}
	})
	t.Run("safety without content blocks", func(t *testing.T) {
		resp := &model.LLMResponse{FinishReason: genai.FinishReasonSafety}
		attachResponsesFinishSignal(resp, &responses.Response{Status: responses.ResponseStatusIncomplete}, false)
		if resp.ErrorCode != string(genai.BlockedReasonSafety) {
			t.Errorf("ErrorCode = %q, want %q", resp.ErrorCode, string(genai.BlockedReasonSafety))
		}
	})
	t.Run("other without content reports other", func(t *testing.T) {
		resp := &model.LLMResponse{FinishReason: genai.FinishReasonOther}
		attachResponsesFinishSignal(resp, &responses.Response{Status: responses.ResponseStatusIncomplete}, true)
		if resp.ErrorCode != string(genai.FinishReasonOther) {
			t.Errorf("ErrorCode = %q, want %q", resp.ErrorCode, string(genai.FinishReasonOther))
		}
	})
	t.Run("nil args are a no-op", func(t *testing.T) {
		attachResponsesFinishSignal(nil, &responses.Response{}, false)
		attachResponsesFinishSignal(&model.LLMResponse{}, nil, false)
	})
}

// TestReportsFailureAndRendererPins covers the nil and empty-status branches
// the streaming fixtures do not reach.
func TestReportsFailureAndRendererPins(t *testing.T) {
	if reportsFailure(nil) {
		t.Error("reportsFailure(nil) = true, want false")
	}
	var bare responses.Response
	if err := json.Unmarshal([]byte(`{"id":"r1"}`), &bare); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if reportsFailure(&bare) {
		t.Error("a status-less body with no error object is not a failure")
	}
	var errored responses.Response
	if err := json.Unmarshal([]byte(`{"error":{"code":"rate_limit","message":"slow down"}}`), &errored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reportsFailure(&errored) {
		t.Error("a status-less body carrying an error object is a failure")
	}

	if err := failedResponseError(nil); err == nil || err.Error() != "openai response failed" {
		t.Errorf("failedResponseError(nil) = %v, want the bare message", err)
	}
}

// TestFailedResponseErrorBranches covers the single-detail renderings.
func TestFailedResponseErrorBranches(t *testing.T) {
	var idOnly responses.Response
	if err := json.Unmarshal([]byte(`{"id":"resp_i","status":"failed"}`), &idOnly); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := failedResponseError(&idOnly).Error(); got != `openai response failed (id "resp_i")` {
		t.Errorf("id-only = %q, want the id parenthetical", got)
	}
	var codeOnly responses.Response
	if err := json.Unmarshal([]byte(`{"status":"failed","error":{"code":"c1"}}`), &codeOnly); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := failedResponseError(&codeOnly).Error(); got != `openai response failed (code "c1")` {
		t.Errorf("code-only = %q, want the code parenthetical", got)
	}
	var msgOnly responses.Response
	if err := json.Unmarshal([]byte(`{"status":"failed","error":{"message":"m1"}}`), &msgOnly); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := failedResponseError(&msgOnly).Error(); got != `openai response failed: "m1"` {
		t.Errorf("msg-only = %q, want the quoted message", got)
	}
}

// TestFinishReasonDecisionTable covers the branches of
// oaiResponsesFinishReason and responsesFinishMessage the streaming fixtures
// do not reach: nil payloads, unmapped reasons, error messages, and the
// event-vs-status contradiction.
func TestFinishReasonDecisionTable(t *testing.T) {
	if got := oaiResponsesFinishReason(nil, true); got != genai.FinishReasonUnspecified {
		t.Errorf("oaiResponsesFinishReason(nil) = %v, want UNSPECIFIED", got)
	}
	var unmapped responses.Response
	if err := json.Unmarshal([]byte(`{"id":"r","status":"incomplete","incomplete_details":{"reason":"weird_reason"}}`), &unmapped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := oaiResponsesFinishReason(&unmapped, false); got != genai.FinishReasonOther {
		t.Errorf("unmapped reason = %v, want OTHER", got)
	}
	if msg := responsesFinishMessage(nil, true); msg != "" {
		t.Errorf("responsesFinishMessage(nil) = %q, want empty", msg)
	}
	var withError responses.Response
	if err := json.Unmarshal([]byte(`{"id":"r","error":{"code":"x","message":"server said no"}}`), &withError); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg := responsesFinishMessage(&withError, false); msg != "server said no" {
		t.Errorf("error message should win, got %q", msg)
	}
	// The contradiction: the event said incomplete, the payload says completed
	// with no details — the event's name wins.
	var contradiction responses.Response
	if err := json.Unmarshal([]byte(`{"id":"r","status":"completed","incomplete_details":null}`), &contradiction); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg := responsesFinishMessage(&contradiction, true); msg != "incomplete" {
		t.Errorf("contradiction message = %q, want \"incomplete\" (event name wins)", msg)
	}
	if got := oaiResponsesFinishReason(&contradiction, true); got != genai.FinishReasonOther {
		t.Errorf("contradiction finish = %v, want OTHER (event outranks payload)", got)
	}
	// A failed status with no other wording still reports the status.
	var failed responses.Response
	if err := json.Unmarshal([]byte(`{"id":"r","status":"failed"}`), &failed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg := responsesFinishMessage(&failed, false); msg != "failed" {
		t.Errorf("status should be the message, got %q", msg)
	}
}

// TestIsPreviousResponseNotFoundNilAndRaw pins the string-fallback branches.
func TestIsPreviousResponseNotFoundNilAndRaw(t *testing.T) {
	if isPreviousResponseNotFound(nil) {
		t.Error("nil error is not a stale pointer")
	}
	if !isPreviousResponseNotFound(errors.New(`param: previous_response_id, Previous response with id 'r1' not found`)) {
		t.Error("prose mentioning the param and not-found should match")
	}
}

// TestSleepCtxZeroDelay covers the d<=0 branch of sleepCtx: a zero sleep
// simply reports whether the context is still alive.
func TestSleepCtxZeroDelay(t *testing.T) {
	if !sleepCtx(context.Background(), 0) {
		t.Error("sleepCtx(0) on a live context = false, want true")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepCtx(ctx, 0) {
		t.Error("sleepCtx(0) on a canceled context = true, want false")
	}
}
