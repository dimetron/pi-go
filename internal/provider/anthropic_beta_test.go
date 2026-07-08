package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// TestAntThinkingConfigBeta verifies that the thinking level string maps to a
// BetaThinkingConfigParamUnion (non-nil for low/medium/high; nil otherwise).
func TestAntThinkingConfigBeta(t *testing.T) {
	tests := []struct {
		level   string
		wantNil bool
	}{
		{"none", true},
		{"", true},
		{"unknown", true},
		{"low", false},
		{"medium", false},
		{"high", false},
	}
	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			got := antThinkingConfigBeta(tt.level)
			if tt.wantNil {
				if got != nil {
					t.Errorf("antThinkingConfigBeta(%q) = %+v, want nil", tt.level, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("antThinkingConfigBeta(%q) = nil, want non-nil", tt.level)
			}
			if got.OfAdaptive == nil {
				t.Errorf("antThinkingConfigBeta(%q) = missing OfAdaptive", tt.level)
			}
		})
	}
}

// TestAntStopReasonToGenaiBeta covers the beta stop reason mapping.
func TestAntStopReasonToGenaiBeta(t *testing.T) {
	tests := []struct {
		reason anthropic.BetaStopReason
		want   genai.FinishReason
	}{
		{anthropic.BetaStopReasonEndTurn, genai.FinishReasonStop},
		{anthropic.BetaStopReasonMaxTokens, genai.FinishReasonMaxTokens},
		{anthropic.BetaStopReasonToolUse, genai.FinishReasonStop},
		{anthropic.BetaStopReasonStopSequence, genai.FinishReasonStop}, // default branch
		{anthropic.BetaStopReason("unknown"), genai.FinishReasonStop},
	}
	for _, tt := range tests {
		t.Run(string(tt.reason), func(t *testing.T) {
			got := antStopReasonToGenaiBeta(tt.reason)
			if got != tt.want {
				t.Errorf("antStopReasonToGenaiBeta(%q) = %v, want %v", tt.reason, got, tt.want)
			}
		})
	}
}

// TestConvertRoleToBeta covers the user/assistant role mapping and the default branch.
func TestConvertRoleToBeta(t *testing.T) {
	tests := []struct {
		role anthropic.MessageParamRole
		want anthropic.BetaMessageParamRole
	}{
		{anthropic.MessageParamRoleUser, anthropic.BetaMessageParamRoleUser},
		{anthropic.MessageParamRoleAssistant, anthropic.BetaMessageParamRoleAssistant},
		{anthropic.MessageParamRole("other"), anthropic.BetaMessageParamRoleUser}, // default
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			got := convertRoleToBeta(tt.role)
			if got != tt.want {
				t.Errorf("convertRoleToBeta(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

// TestConvertContentBlockToBeta verifies the text-block passthrough and the
// non-text fallback (which returns an empty text block).
func TestConvertContentBlockToBeta(t *testing.T) {
	t.Run("text block round-trips text", func(t *testing.T) {
		src := anthropic.NewTextBlock("hello world")
		got := convertContentBlockToBeta(src)
		if got.OfText == nil {
			t.Fatal("expected OfText to be set")
		}
		if got.OfText.Text != "hello world" {
			t.Errorf("text = %q, want %q", got.OfText.Text, "hello world")
		}
	})

	t.Run("non-text fallback returns empty text block", func(t *testing.T) {
		// A tool-use block has no OfText, so the fallback path is taken.
		src := anthropic.NewToolUseBlock("tu_id", map[string]any{"x": 1}, "tool_name")
		got := convertContentBlockToBeta(src)
		if got.OfText == nil {
			t.Fatal("expected fallback to produce a text block")
		}
		if got.OfText.Text != "" {
			t.Errorf("fallback text = %q, want empty", got.OfText.Text)
		}
	})
}

// TestAntGenaiToolsToBetaAnthropic verifies the beta tool conversion handles
// nil entries, required-fields extraction, and a basic tool schema.
func TestAntGenaiToolsToBetaAnthropic(t *testing.T) {
	t.Run("basic tools", func(t *testing.T) {
		tools := []*genai.Tool{
			{
				FunctionDeclarations: []*genai.FunctionDeclaration{
					{
						Name:        "read_file",
						Description: "Read a file",
						ParametersJsonSchema: map[string]any{
							"type": "object",
							"properties": map[string]any{
								"path": map[string]any{"type": "string"},
							},
							"required": []any{"path"},
						},
					},
				},
			},
		}
		out := antGenaiToolsToBetaAnthropic(tools)
		if len(out) != 1 {
			t.Fatalf("expected 1 beta tool, got %d", len(out))
		}
		if out[0].OfTool == nil {
			t.Fatal("expected OfTool variant")
		}
		if out[0].OfTool.Name != "read_file" {
			t.Errorf("name = %q, want read_file", out[0].OfTool.Name)
		}
		if len(out[0].OfTool.InputSchema.Required) != 1 || out[0].OfTool.InputSchema.Required[0] != "path" {
			t.Errorf("required = %v, want [path]", out[0].OfTool.InputSchema.Required)
		}
	})

	t.Run("nil entries are skipped", func(t *testing.T) {
		tools := []*genai.Tool{
			nil,
			{FunctionDeclarations: nil},
			{FunctionDeclarations: []*genai.FunctionDeclaration{nil}},
			{
				FunctionDeclarations: []*genai.FunctionDeclaration{
					{Name: "t", Description: "t"},
				},
			},
		}
		out := antGenaiToolsToBetaAnthropic(tools)
		if len(out) != 1 {
			t.Fatalf("expected 1 tool, got %d", len(out))
		}
	})

	t.Run("required extracts only strings", func(t *testing.T) {
		tools := []*genai.Tool{
			{
				FunctionDeclarations: []*genai.FunctionDeclaration{
					{
						Name:        "tool",
						Description: "tool",
						ParametersJsonSchema: map[string]any{
							"properties": map[string]any{
								"a": map[string]any{"type": "string"},
							},
							"required": []any{"a", 123, "b"}, // non-string entries filtered out
						},
					},
				},
			},
		}
		out := antGenaiToolsToBetaAnthropic(tools)
		if len(out) != 1 {
			t.Fatalf("expected 1 tool, got %d", len(out))
		}
		req := out[0].OfTool.InputSchema.Required
		if len(req) != 2 || req[0] != "a" || req[1] != "b" {
			t.Errorf("required = %v, want [a b]", req)
		}
	})
}

// TestBuildBetaParams covers the advisor-tool beta params construction and
// its opt-in fields (max_uses, caching, thinking, system, tools).
func TestBuildBetaParams(t *testing.T) {
	t.Run("basic params include advisor tool + beta flag", func(t *testing.T) {
		m := &anthropicModel{
			modelName:    "claude-sonnet-4-6",
			advisorModel: "claude-opus-4-7",
		}
		messages, _ := antContentsToMessages([]*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		}, nil)

		params := m.buildBetaParams("claude-sonnet-4-6", messages, "", 8192, nil, nil)
		if params.Model != "claude-sonnet-4-6" {
			t.Errorf("Model = %q", params.Model)
		}
		if len(params.Betas) != 1 {
			t.Fatalf("expected 1 beta flag, got %d", len(params.Betas))
		}
		if params.Betas[0] != anthropic.AnthropicBetaAdvisorTool2026_03_01 {
			t.Errorf("beta flag = %v, want advisor 2026-03-01", params.Betas[0])
		}
		if len(params.Tools) != 1 {
			t.Fatalf("expected advisor tool, got %d tools", len(params.Tools))
		}
		if params.Tools[0].OfAdvisorTool20260301 == nil {
			t.Fatal("advisor tool variant not set")
		}
		if params.Tools[0].OfAdvisorTool20260301.Model != "claude-opus-4-7" {
			t.Errorf("advisor model = %q", params.Tools[0].OfAdvisorTool20260301.Model)
		}
		// MaxUses and Caching are not set by default.
		if params.Tools[0].OfAdvisorTool20260301.MaxUses.Valid() {
			t.Error("MaxUses should be unset when advisorMaxUses == 0")
		}
	})

	t.Run("max uses + caching + system + thinking set", func(t *testing.T) {
		m := &anthropicModel{
			modelName:      "claude-sonnet-4-6",
			advisorModel:   "claude-opus-4-7",
			advisorMaxUses: 3,
			advisorCaching: true,
		}
		messages, sys := antContentsToMessages([]*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		}, &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: "system text"}},
			},
		})
		if sys != "system text" {
			t.Fatalf("sys = %q", sys)
		}

		thinking := antThinkingConfigBeta("high")
		params := m.buildBetaParams("claude-sonnet-4-6", messages, sys, 16384, thinking, nil)

		advisor := params.Tools[0].OfAdvisorTool20260301
		if !advisor.MaxUses.Valid() || advisor.MaxUses.Value != int64(3) {
			t.Errorf("MaxUses = valid=%v value=%d, want valid=true value=3",
				advisor.MaxUses.Valid(), advisor.MaxUses.Value)
		}
		if advisor.Caching.Type == "" {
			t.Error("expected caching to be set")
		}
		if len(params.System) != 1 || params.System[0].Text != "system text" {
			t.Errorf("system = %+v", params.System)
		}
		if params.Thinking.OfAdaptive == nil {
			t.Error("expected thinking config to propagate")
		}
	})

	t.Run("user tools are appended after advisor tool", func(t *testing.T) {
		m := &anthropicModel{advisorModel: "claude-opus-4-7"}
		messages, _ := antContentsToMessages([]*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		}, nil)
		cfg := &genai.GenerateContentConfig{
			Tools: []*genai.Tool{
				{
					FunctionDeclarations: []*genai.FunctionDeclaration{
						{Name: "bash", Description: "run shell"},
					},
				},
			},
		}
		params := m.buildBetaParams("claude-sonnet-4-6", messages, "", 8192, nil, cfg)
		if len(params.Tools) != 2 {
			t.Fatalf("expected 2 tools (advisor + user), got %d", len(params.Tools))
		}
		if params.Tools[0].OfAdvisorTool20260301 == nil {
			t.Error("first tool should be advisor")
		}
		if params.Tools[1].OfTool == nil || params.Tools[1].OfTool.Name != "bash" {
			t.Errorf("second tool = %+v, want user bash tool", params.Tools[1])
		}
	})

	t.Run("converts assistant role and text content to beta", func(t *testing.T) {
		m := &anthropicModel{advisorModel: "claude-opus-4-7"}
		messages, _ := antContentsToMessages([]*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
			{Role: "model", Parts: []*genai.Part{{Text: "Hello"}}},
		}, nil)
		params := m.buildBetaParams("claude-sonnet-4-6", messages, "", 8192, nil, nil)
		if len(params.Messages) != 2 {
			t.Fatalf("expected 2 beta messages, got %d", len(params.Messages))
		}
		if params.Messages[1].Role != anthropic.BetaMessageParamRoleAssistant {
			t.Errorf("second message role = %q, want assistant", params.Messages[1].Role)
		}
	})
}

// TestExtractAdvisorResultText covers the advisor_result / advisor_redacted_result
// branches plus the default-empty branch.
func TestExtractAdvisorResultText(t *testing.T) {
	t.Run("plain advisor result returns text", func(t *testing.T) {
		var block anthropic.BetaAdvisorToolResultBlock
		block.Content.Type = "advisor_result"
		block.Content.Text = "advisor said hi"
		if got := extractAdvisorResultText(block); got != "advisor said hi" {
			t.Errorf("got %q, want %q", got, "advisor said hi")
		}
	})

	t.Run("redacted result returns placeholder", func(t *testing.T) {
		var block anthropic.BetaAdvisorToolResultBlock
		block.Content.Type = "advisor_redacted_result"
		got := extractAdvisorResultText(block)
		if !strings.Contains(got, "encrypted") {
			t.Errorf("got %q, want placeholder mentioning 'encrypted'", got)
		}
	})

	t.Run("unknown type returns empty", func(t *testing.T) {
		var block anthropic.BetaAdvisorToolResultBlock
		block.Content.Type = "something_else"
		if got := extractAdvisorResultText(block); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

// TestExtractBetaAdvisorResultText feeds a full BetaContentBlockUnion via
// JSON unmarshalling (AsAdvisorToolResult reads from the raw JSON).
func TestExtractBetaAdvisorResultText(t *testing.T) {
	raw := []byte(`{
		"type": "advisor_tool_result",
		"tool_use_id": "advtool_123",
		"content": {
			"type": "advisor_result",
			"text": "use option B"
		}
	}`)
	var block anthropic.BetaContentBlockUnion
	if err := json.Unmarshal(raw, &block); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := extractBetaAdvisorResultText(block)
	if got != "use option B" {
		t.Errorf("got %q, want %q", got, "use option B")
	}
}

// TestAnthropicBetaNonStreaming_AdvisorResult drives the advisor-enabled
// non-streaming path through a mock server and verifies the advisor text flows
// through antRunNonStreamingBeta → extractBetaAdvisorResultText.
func TestAnthropicBetaNonStreaming_AdvisorResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/messages") {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		body := map[string]any{
			"id":    "msg_beta_test",
			"type":  "message",
			"role":  "assistant",
			"model": "claude-sonnet-4-6",
			"content": []map[string]any{
				{"type": "text", "text": "Thinking..."},
				{
					"type":        "advisor_tool_result",
					"tool_use_id": "advtool_abc",
					"content": map[string]any{
						"type": "advisor_result",
						"text": "advice payload",
					},
				},
				{
					"type":  "tool_use",
					"id":    "toolu_fn",
					"name":  "bash",
					"input": map[string]any{"command": "ls"},
				},
				{
					"type":     "thinking",
					"thinking": "reasoning trace",
				},
			},
			"stop_reason": "end_turn",
			"usage": map[string]any{
				"input_tokens":  11,
				"output_tokens": 7,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	ctx := context.Background()
	llm, err := NewAnthropic(ctx, "claude-sonnet-4-6", "sk-test", srv.URL, "none", &LLMOptions{
		AdvisorModel:   "claude-opus-4-7",
		AdvisorMaxUses: 2,
		AdvisorCaching: true,
	})
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Should I do X?"}}},
		},
	}
	var last *model.LLMResponse
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			t.Fatalf("GenerateContent: %v", err)
		}
		last = resp
	}
	if last == nil || last.Content == nil {
		t.Fatal("no response received")
	}
	// The response should include text, thinking, tool_use, and advisor result parts.
	texts := make([]string, 0, len(last.Content.Parts))
	gotFC := false
	for _, p := range last.Content.Parts {
		if p.FunctionCall != nil {
			gotFC = true
			if p.FunctionCall.Name != "bash" {
				t.Errorf("FunctionCall.Name = %q", p.FunctionCall.Name)
			}
		}
		if p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	if !gotFC {
		t.Error("expected FunctionCall part for tool_use block")
	}
	joined := strings.Join(texts, "|")
	if !strings.Contains(joined, "advice payload") {
		t.Errorf("missing advisor payload in parts texts: %q", joined)
	}
	if !strings.Contains(joined, "Thinking...") {
		t.Errorf("missing text part: %q", joined)
	}
	if !strings.Contains(joined, "reasoning trace") {
		t.Errorf("missing thinking part: %q", joined)
	}
	if last.UsageMetadata == nil {
		t.Fatal("expected usage metadata")
	}
	if last.UsageMetadata.PromptTokenCount != 11 {
		t.Errorf("PromptTokenCount = %d", last.UsageMetadata.PromptTokenCount)
	}
	if last.FinishReason != genai.FinishReasonStop {
		t.Errorf("FinishReason = %v, want Stop", last.FinishReason)
	}
}

// TestAnthropicBetaNonStreaming_ErrorResponse covers the error branch of
// antRunNonStreamingBeta (server returns 500).
func TestAnthropicBetaNonStreaming_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"boom"}}`))
	}))
	defer srv.Close()

	ctx := context.Background()
	llm, err := NewAnthropic(ctx, "claude-sonnet-4-6", "sk-test", srv.URL, "none", &LLMOptions{
		AdvisorModel: "claude-opus-4-7",
	})
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		},
	}
	gotErr := false
	for _, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			gotErr = true
		}
	}
	if !gotErr {
		t.Error("expected error from beta non-streaming path")
	}
}

// TestAnthropicBetaStreaming_AdvisorFlow drives the advisor-enabled streaming
// path end-to-end through a mock SSE server and verifies advisor/text/tool
// events are processed.
func TestAnthropicBetaStreaming_AdvisorFlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}

		writeEvent := func(ev string, payload map[string]any) {
			b, _ := json.Marshal(payload)
			_, _ = w.Write([]byte("event: " + ev + "\n"))
			_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
			flusher.Flush()
		}

		// message_start
		writeEvent("message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":    "msg_stream",
				"type":  "message",
				"role":  "assistant",
				"model": "claude-sonnet-4-6",
				"usage": map[string]any{"input_tokens": 9, "output_tokens": 0},
			},
		})
		// content_block_start — advisor_tool_result arrives fully formed
		writeEvent("content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": 0,
			"content_block": map[string]any{
				"type":        "advisor_tool_result",
				"tool_use_id": "advtool_stream",
				"content": map[string]any{
					"type": "advisor_result",
					"text": "stream advice",
				},
			},
		})
		// content_block_start — tool_use
		writeEvent("content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": 1,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    "toolu_s",
				"name":  "bash",
				"input": map[string]any{},
			},
		})
		// text delta (use block index 2 so it doesn't clobber tool_use at index 1)
		writeEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": 2,
			"delta": map[string]any{
				"type": "text_delta",
				"text": "hello",
			},
		})
		// thinking delta
		writeEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": 2,
			"delta": map[string]any{
				"type":     "thinking_delta",
				"thinking": "reasoning",
			},
		})
		// input_json_delta for tool_use at index 1
		writeEvent("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": 1,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": `{"cmd":"ls"}`,
			},
		})
		// message_delta with final stop reason + usage
		writeEvent("message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason": "end_turn",
			},
			"usage": map[string]any{"output_tokens": 4},
		})
		// message_stop
		writeEvent("message_stop", map[string]any{"type": "message_stop"})
	}))
	defer srv.Close()

	ctx := context.Background()
	llm, err := NewAnthropic(ctx, "claude-sonnet-4-6", "sk-test", srv.URL, "none", &LLMOptions{
		AdvisorModel: "claude-opus-4-7",
	})
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		},
	}

	var gotAdvisor, gotText, gotThinking bool
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
		}
		switch resp.Content.Role {
		case "advisor":
			gotAdvisor = true
		case "thinking":
			gotThinking = true
		case string(genai.RoleModel):
			for _, p := range resp.Content.Parts {
				if p != nil && p.Text == "hello" {
					gotText = true
				}
			}
		}
	}
	if !gotAdvisor {
		t.Error("expected an advisor-role event")
	}
	if !gotText {
		t.Error("expected text_delta to yield a partial text response")
	}
	if !gotThinking {
		t.Error("expected thinking_delta to yield a partial thinking response")
	}
	if final == nil {
		t.Fatal("expected a final aggregated response")
	}
	// Final response should contain the tool_use call with the accumulated arg.
	foundFC := false
	for _, p := range final.Content.Parts {
		if p.FunctionCall != nil && p.FunctionCall.Name == "bash" {
			foundFC = true
			if cmd, _ := p.FunctionCall.Args["cmd"].(string); cmd != "ls" {
				t.Errorf("arg cmd = %q, want ls", cmd)
			}
		}
	}
	if !foundFC {
		t.Error("expected bash FunctionCall part in final response")
	}
}

// TestAnthropicBetaStreaming_Error covers the streaming error branch when the
// SSE fails to decode.
func TestAnthropicBetaStreaming_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"boom"}}`))
	}))
	defer srv.Close()

	ctx := context.Background()
	llm, err := NewAnthropic(ctx, "claude-sonnet-4-6", "sk-test", srv.URL, "none", &LLMOptions{
		AdvisorModel: "claude-opus-4-7",
	})
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		},
	}

	sawError := false
	for resp, err := range llm.GenerateContent(ctx, req, true) {
		if err != nil {
			sawError = true
			break
		}
		if resp != nil && resp.ErrorCode != "" {
			sawError = true
			break
		}
	}
	if !sawError {
		t.Error("expected error or ErrorCode response from streaming beta path")
	}
}
