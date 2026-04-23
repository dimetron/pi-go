package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3/responses"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// TestContextWindowSize covers prefix matching, longest-prefix rule, and the
// unknown-model (returns 0) branch.
func TestContextWindowSize(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  int64
	}{
		{"exact claude-opus-4-7", "claude-opus-4-7", 1_000_000},
		{"claude-3-5-sonnet prefix", "claude-3-5-sonnet-20241022", 200_000},
		{"case-insensitive GEMINI-2.5", "GEMINI-2.5-PRO", 1_048_576},
		{"gpt-5.5 latest frontier", "gpt-5.5", 1_050_000},
		{"gpt-5.4-mini has longer prefix", "gpt-5.4-mini", 400_000},
		{"mistral-large tag variant", "mistral-large-2512", 256_000},
		{"unknown returns 0", "nonexistent-model-xyz", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ContextWindowSize(tt.model)
			if got != tt.want {
				t.Errorf("ContextWindowSize(%q) = %d, want %d", tt.model, got, tt.want)
			}
		})
	}
}

// TestOaiResponsesRole covers the developer / system / default branches that
// the earlier tests didn't exercise.
func TestOaiResponsesRole(t *testing.T) {
	tests := []struct {
		role string
		want responses.EasyInputMessageRole
	}{
		{"user", responses.EasyInputMessageRoleUser},
		{"model", responses.EasyInputMessageRoleAssistant},
		{"assistant", responses.EasyInputMessageRoleAssistant},
		{"developer", responses.EasyInputMessageRoleDeveloper},
		{"  SYSTEM  ", responses.EasyInputMessageRoleSystem},
		{"somethingelse", responses.EasyInputMessageRoleUser},
		{"", responses.EasyInputMessageRoleUser},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			got := oaiResponsesRole(tt.role)
			if got != tt.want {
				t.Errorf("oaiResponsesRole(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

// TestErrorBodyLoggingTransport_PassThrough verifies the 2xx path doesn't
// touch the body and returns the response unchanged.
func TestErrorBodyLoggingTransport_PassThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	transport := &errorBodyLoggingTransport{base: http.DefaultTransport}
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `"ok":true`) {
		t.Errorf("body = %q, want contains ok:true", body)
	}
}

// TestErrorBodyLoggingTransport_ErrorBody verifies that on 4xx/5xx the body
// is read+logged and the response Body is replaced with a fresh reader.
func TestErrorBodyLoggingTransport_ErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad"}`))
	}))
	defer srv.Close()

	transport := &errorBodyLoggingTransport{base: http.DefaultTransport}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/codex/responses", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `"error":"bad"`) {
		t.Errorf("body should be replayable after logging; got %q", body)
	}
}

// TestErrorBodyLoggingTransport_NilBody covers the branch where resp.Body is nil.
type nilBodyTransport struct{}

func (nilBodyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusInternalServerError, Body: nil, Header: make(http.Header)}, nil
}

func TestErrorBodyLoggingTransport_NilBody(t *testing.T) {
	transport := &errorBodyLoggingTransport{base: nilBodyTransport{}}
	req, _ := http.NewRequest(http.MethodGet, "http://example/x", nil)
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.Body != nil {
		t.Error("expected Body to remain nil when upstream returns nil body")
	}
}

// TestTruncateHelper exercises the codex truncate helper.
func TestTruncateHelper(t *testing.T) {
	tests := []struct {
		in, want string
		n        int
	}{
		{"short", "short", 10},
		{"exact size", "exact size", 10},
		{"this is a long string", "this is a…", 9},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := truncate(tt.in, tt.n)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
			}
		})
	}
}

// TestExtractChatGPTAccountID_MissingClaim covers the branch where the JWT
// parses but doesn't contain the auth blob / chatgpt_account_id claim.
func TestExtractChatGPTAccountID_MissingClaim(t *testing.T) {
	// JWT payload with no auth blob: {"sub":"user-x"}.
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"user-x"}`))
	jwt := "h." + payload + ".s"
	if got := extractChatGPTAccountID(jwt); got != "" {
		t.Errorf("extractChatGPTAccountID(no auth blob) = %q, want empty", got)
	}

	// JWT payload with auth blob but no chatgpt_account_id.
	payload2 := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"other":"x"}}`))
	jwt2 := "h." + payload2 + ".s"
	if got := extractChatGPTAccountID(jwt2); got != "" {
		t.Errorf("extractChatGPTAccountID(no account_id) = %q, want empty", got)
	}

	// JWT payload with malformed auth claims JSON (non-object value).
	payload3 := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":"not-an-object"}`))
	jwt3 := "h." + payload3 + ".s"
	if got := extractChatGPTAccountID(jwt3); got != "" {
		t.Errorf("extractChatGPTAccountID(bad auth type) = %q, want empty", got)
	}

	// Payload that isn't valid JSON — force the json.Unmarshal error branch.
	payload4 := base64.RawURLEncoding.EncodeToString([]byte(`not-json`))
	jwt4 := "h." + payload4 + ".s"
	if got := extractChatGPTAccountID(jwt4); got != "" {
		t.Errorf("extractChatGPTAccountID(bad json) = %q, want empty", got)
	}
}

// TestParseResponsesOutput covers reasoning and phase-message branches that
// the existing tests don't reach via the public API.
func TestParseResponsesOutput(t *testing.T) {
	// Build JSON for the three output types, then unmarshal into the SDK union.
	raw := []byte(`[
		{
			"type": "message",
			"id": "msg_1",
			"status": "completed",
			"role": "assistant",
			"phase": "commentary",
			"content": [
				{"type": "output_text", "text": "hello from message", "annotations": []}
			]
		},
		{
			"type": "function_call",
			"id": "fc_1",
			"call_id": "call_xyz",
			"name": "bash",
			"arguments": "{\"cmd\":\"ls\"}",
			"status": "completed"
		},
		{
			"type": "reasoning",
			"id": "rsn_1",
			"content": [
				{"type": "reasoning_text", "text": "thinking about it"}
			],
			"summary": []
		}
	]`)
	var items []responses.ResponseOutputItemUnion
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("unmarshal items: %v", err)
	}

	parts, finish := parseResponsesOutput(items)
	if finish != "" {
		t.Errorf("finishReason = %q, want empty (not derived from items)", finish)
	}
	if len(parts) < 3 {
		t.Fatalf("expected at least 3 parts (text, phase, fc, reasoning), got %d: %+v", len(parts), parts)
	}

	foundText, foundPhase, foundFC, foundReasoning := false, false, false, false
	for _, p := range parts {
		if p == nil {
			continue
		}
		if strings.Contains(p.Text, "hello from message") {
			foundText = true
		}
		if strings.Contains(p.Text, "[phase: commentary]") {
			foundPhase = true
		}
		if p.FunctionCall != nil && p.FunctionCall.Name == "bash" && p.FunctionCall.ID == "call_xyz" {
			foundFC = true
			if cmd, _ := p.FunctionCall.Args["cmd"].(string); cmd != "ls" {
				t.Errorf("fc arg cmd = %q, want ls", cmd)
			}
		}
		if strings.Contains(p.Text, "thinking about it") {
			foundReasoning = true
		}
	}
	if !foundText {
		t.Error("missing message text part")
	}
	if !foundPhase {
		t.Error("missing phase label part")
	}
	if !foundFC {
		t.Error("missing function_call part")
	}
	if !foundReasoning {
		t.Error("missing reasoning text part")
	}
}

// TestOpenAIResponses_NonStreaming covers generateResponses →
// runResponsesNonStreaming end-to-end via a mock server. Uses a non-codex
// baseURL path so the openai client goes to `<base>/responses`.
func TestOpenAIResponses_NonStreaming(t *testing.T) {
	var receivedPath string
	var receivedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		body := map[string]any{
			"id":                  "resp_test_123",
			"object":              "response",
			"created_at":          1.0,
			"status":              "completed",
			"model":               "gpt-5-codex",
			"error":               nil,
			"incomplete_details":  nil,
			"instructions":        nil,
			"metadata":            map[string]any{},
			"tool_choice":         "auto",
			"tools":               []any{},
			"parallel_tool_calls": true,
			"temperature":         1,
			"top_p":               1,
			"output": []map[string]any{
				{
					"type":   "message",
					"id":     "msg_x",
					"role":   "assistant",
					"status": "completed",
					"content": []map[string]any{
						{"type": "output_text", "text": "response text", "annotations": []any{}},
					},
				},
			},
			"usage": map[string]any{
				"input_tokens":  13,
				"output_tokens": 7,
				"total_tokens":  20,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	ctx := context.Background()
	// Use gpt-5-codex so modelNeedsResponses() forces the Responses path,
	// and give an explicit baseURL so codex-backend routing is skipped.
	llm, err := NewOpenAI(ctx, "gpt-5-codex", "sk-test", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "ping"}}},
		},
		Config: &genai.GenerateContentConfig{
			ThinkingConfig: &genai.ThinkingConfig{
				ThinkingBudget: func() *int32 { v := int32(3000); return &v }(),
			},
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: "sys prompt"}},
			},
		},
	}
	var final *model.LLMResponse
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			t.Fatalf("GenerateContent: %v", err)
		}
		final = resp
	}
	if final == nil {
		t.Fatal("no response")
	}
	if !strings.HasSuffix(receivedPath, "/responses") {
		t.Errorf("received path = %q, want suffix /responses", receivedPath)
	}
	// Instructions from SystemInstruction should be in the outgoing body.
	if receivedBody["instructions"] != "sys prompt" {
		t.Errorf("instructions = %v, want 'sys prompt'", receivedBody["instructions"])
	}
	// Reasoning should be set to medium (budget 3000).
	reasoning, _ := receivedBody["reasoning"].(map[string]any)
	if reasoning == nil || reasoning["effort"] != "medium" {
		t.Errorf("reasoning = %v, want effort=medium", receivedBody["reasoning"])
	}
	// Final response should contain the text part.
	if len(final.Content.Parts) == 0 || final.Content.Parts[0].Text != "response text" {
		t.Errorf("final parts = %+v", final.Content.Parts)
	}
	if final.UsageMetadata == nil || final.UsageMetadata.PromptTokenCount != 13 {
		t.Errorf("usage = %+v", final.UsageMetadata)
	}
	if final.FinishReason != genai.FinishReasonStop {
		t.Errorf("finish = %v, want Stop", final.FinishReason)
	}
	// The model should now remember the response ID for multi-turn.
	om := llm.(*openaiModel)
	if om.responseState == nil || om.responseState.previousResponseID != "resp_test_123" {
		t.Errorf("responseState not saved, got %+v", om.responseState)
	}

	// Second call — the stored previous_response_id must be threaded.
	for range llm.GenerateContent(ctx, req, false) {
	}
	if receivedBody["previous_response_id"] != "resp_test_123" {
		t.Errorf("previous_response_id = %v, want resp_test_123", receivedBody["previous_response_id"])
	}
}

// TestOpenAIResponses_NonStreamingError covers the error branch of
// runResponsesNonStreaming when the server returns a non-2xx status.
func TestOpenAIResponses_NonStreamingError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"bad","message":"nope"}}`))
	}))
	defer srv.Close()

	ctx := context.Background()
	llm, err := NewOpenAI(ctx, "gpt-5-codex", "sk-test", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "ping"}}},
		},
	}
	gotErr := false
	for _, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			gotErr = true
		}
	}
	if !gotErr {
		t.Error("expected error from Responses non-streaming")
	}
}

// TestOpenAIResponses_Streaming drives runResponsesStreaming via an SSE
// mock server and verifies the streaming event handling.
func TestOpenAIResponses_Streaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		writeEvent := func(payload map[string]any) {
			b, _ := json.Marshal(payload)
			_, _ = w.Write([]byte("event: " + payload["type"].(string) + "\n"))
			_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
			flusher.Flush()
		}

		writeEvent(map[string]any{
			"type":            "response.output_text.delta",
			"sequence_number": 1,
			"item_id":         "msg_1",
			"output_index":    0,
			"content_index":   0,
			"delta":           "hi ",
		})
		writeEvent(map[string]any{
			"type":            "response.output_text.delta",
			"sequence_number": 2,
			"item_id":         "msg_1",
			"output_index":    0,
			"content_index":   0,
			"delta":           "there",
		})
		writeEvent(map[string]any{
			"type":            "response.reasoning_text.delta",
			"sequence_number": 3,
			"item_id":         "rsn_1",
			"output_index":    0,
			"content_index":   0,
			"delta":           "reasoning!",
		})
		// Function-call item header
		writeEvent(map[string]any{
			"type":            "response.output_item.added",
			"sequence_number": 4,
			"output_index":    1,
			"item": map[string]any{
				"type":      "function_call",
				"id":        "fc_1",
				"call_id":   "call_abc",
				"name":      "bash",
				"arguments": "",
				"status":    "in_progress",
			},
		})
		// Function-call args delta
		writeEvent(map[string]any{
			"type":            "response.function_call_arguments.delta",
			"sequence_number": 5,
			"item_id":         "fc_1",
			"output_index":    1,
			"delta":           `{"cmd":`,
		})
		writeEvent(map[string]any{
			"type":            "response.function_call_arguments.delta",
			"sequence_number": 6,
			"item_id":         "fc_1",
			"output_index":    1,
			"delta":           `"ls"}`,
		})
		// Args done — final authoritative string (covers the "done" branch).
		writeEvent(map[string]any{
			"type":            "response.function_call_arguments.done",
			"sequence_number": 7,
			"item_id":         "fc_1",
			"output_index":    1,
			"name":            "bash",
			"arguments":       `{"cmd":"ls"}`,
		})
		// Output-item done (safety-net branch).
		writeEvent(map[string]any{
			"type":            "response.output_item.done",
			"sequence_number": 8,
			"output_index":    1,
			"item": map[string]any{
				"type":      "function_call",
				"id":        "fc_1",
				"call_id":   "call_abc",
				"name":      "bash",
				"arguments": `{"cmd":"ls"}`,
				"status":    "completed",
			},
		})
		// Completed event with final response
		writeEvent(map[string]any{
			"type":            "response.completed",
			"sequence_number": 9,
			"response": map[string]any{
				"id":                  "resp_stream_123",
				"object":              "response",
				"created_at":          1.0,
				"status":              "completed",
				"model":               "gpt-5-codex",
				"error":               nil,
				"incomplete_details":  nil,
				"instructions":        nil,
				"metadata":            map[string]any{},
				"tool_choice":         "auto",
				"tools":               []any{},
				"parallel_tool_calls": true,
				"temperature":         1,
				"top_p":               1,
				"output":              []any{},
				"usage": map[string]any{
					"input_tokens":  5,
					"output_tokens": 3,
					"total_tokens":  8,
				},
			},
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	llm, err := NewOpenAI(ctx, "gpt-5-codex", "sk-test", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "ping"}}},
		},
	}

	var gotText, gotReasoning bool
	var final *model.LLMResponse
	for resp, err := range llm.GenerateContent(ctx, req, true) {
		if err != nil {
			t.Fatalf("GenerateContent: %v", err)
		}
		if resp == nil || resp.Content == nil {
			continue
		}
		if resp.TurnComplete {
			final = resp
			continue
		}
		switch resp.Content.Role {
		case "thinking":
			for _, p := range resp.Content.Parts {
				if p != nil && strings.Contains(p.Text, "reasoning") {
					gotReasoning = true
				}
			}
		case string(genai.RoleModel):
			for _, p := range resp.Content.Parts {
				if p != nil && p.Text != "" {
					gotText = true
				}
			}
		}
	}
	if !gotText {
		t.Error("expected streamed text deltas")
	}
	if !gotReasoning {
		t.Error("expected streamed reasoning deltas")
	}
	if final == nil {
		t.Fatal("no final aggregated response")
	}
	// Final response should contain the tool call with full args.
	foundFC := false
	for _, p := range final.Content.Parts {
		if p.FunctionCall != nil && p.FunctionCall.Name == "bash" {
			foundFC = true
			if id := p.FunctionCall.ID; id != "call_abc" {
				t.Errorf("FunctionCall.ID = %q, want call_abc", id)
			}
			if cmd, _ := p.FunctionCall.Args["cmd"].(string); cmd != "ls" {
				t.Errorf("cmd arg = %q, want ls", cmd)
			}
		}
	}
	if !foundFC {
		t.Error("expected a FunctionCall part in the final streaming response")
	}
	// responseState should be populated from response.completed.
	om := llm.(*openaiModel)
	if om.responseState == nil || om.responseState.previousResponseID != "resp_stream_123" {
		t.Errorf("responseState not saved, got %+v", om.responseState)
	}
}

// TestOpenAIResponses_StreamingError covers the stream error branch of
// runResponsesStreaming (server errors out).
func TestOpenAIResponses_StreamingError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"type":"server","message":"boom"}}`))
	}))
	defer srv.Close()

	ctx := context.Background()
	llm, err := NewOpenAI(ctx, "gpt-5-codex", "sk-test", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	// Seed a stored previous_response_id so the error path clears it.
	om := llm.(*openaiModel)
	om.responseState = &responsesState{previousResponseID: "resp_prev"}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "ping"}}},
		},
	}
	sawErr := false
	for resp, err := range llm.GenerateContent(ctx, req, true) {
		if err != nil {
			sawErr = true
			break
		}
		if resp != nil && resp.ErrorCode != "" {
			sawErr = true
			break
		}
	}
	if !sawErr {
		t.Error("expected error from streaming Responses")
	}
	// Stale previous_response_id should have been cleared.
	if om.responseState == nil || om.responseState.previousResponseID != "" {
		t.Errorf("previousResponseID should be cleared on error, got %+v", om.responseState)
	}
}
