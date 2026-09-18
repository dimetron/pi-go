package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go/v3/shared"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/auth"
	"github.com/dimetron/pi-go/internal/testenv"
)

func TestNewXAIRequiresAPIKey(t *testing.T) {
	// An empty apiKey now means "resolve from the environment or the
	// subscription token store", so the missing-credential case has to clear
	// both. Coverage of the ambient-resolution paths lives in
	// TestResolveXAIEndpoint.
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XAI_API_KEY", "")
	if _, err := NewXAI(context.Background(), "grok-4.6", "", "", "high", nil); err == nil {
		t.Fatal("expected error when no xAI credential is available")
	}
}

// TestNewXAIDefaultBaseURL pins that a developer key with no explicit base URL
// still resolves to the per-token developer API, not the subscription proxy.
func TestNewXAIDefaultBaseURL(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XAI_API_KEY", "")
	m, err := NewXAI(context.Background(), "grok-4.6", "test-key", "", "high", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name() != "grok-4.6" {
		t.Errorf("Name() = %q, want %q", m.Name(), "grok-4.6")
	}
}

func TestNewXAIWithTransportOptions(t *testing.T) {
	opts := &LLMOptions{
		ExtraHeaders:    map[string]string{"X-Custom": "value"},
		InsecureSkipTLS: true,
	}
	m, err := NewXAI(context.Background(), "grok-4.6", "test-key", "", "high", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil model")
	}
}

func TestNewXAIRejectsBadCACert(t *testing.T) {
	opts := &LLMOptions{CACertPath: filepath.Join(t.TempDir(), "absent.pem")}
	if _, err := NewXAI(context.Background(), "grok-4.6", "test-key", "", "high", opts); err == nil {
		t.Fatal("expected an error for an unreadable CA certificate")
	}
}

func TestResolveGrokModels(t *testing.T) {
	for _, name := range []string{"grok-4.6", "grok-4.6-latest", "grok-4.3", "grok-build-0.1", "GROK-4.5"} {
		t.Run(name, func(t *testing.T) {
			info, err := Resolve(name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Provider != "xai" {
				t.Errorf("provider = %q, want xai", info.Provider)
			}
			if info.Ollama {
				t.Error("expected Ollama = false for xAI cloud provider")
			}
			if info.Model != name {
				t.Errorf("model = %q, want %q", info.Model, name)
			}
		})
	}
}

func TestNewLLMCreatesXAIProvider(t *testing.T) {
	llm, err := NewLLM(context.Background(), Info{Provider: "xai", Model: "grok-4.6"}, "test-key", "", "high", nil)
	if err != nil {
		t.Fatalf("NewLLM() error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewLLM() returned nil")
	}
	if llm.Name() != "grok-4.6" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "grok-4.6")
	}
}

func TestValidateGrokModels(t *testing.T) {
	if err := ValidateModel(Info{Provider: "xai", Model: "grok-4.6-latest"}); err != nil {
		t.Errorf("grok-4.6-latest should validate: %v", err)
	}
	if err := ValidateModel(Info{Provider: "xai", Model: "grok-4.20-0309-non-reasoning"}); err != nil {
		t.Errorf("grok-4.20-0309-non-reasoning should validate: %v", err)
	}
	if err := ValidateModel(Info{Provider: "xai", Model: "grok-9-imaginary"}); err == nil {
		t.Error("expected an unknown grok model to be rejected")
	}
}

func TestXAIContextWindows(t *testing.T) {
	tests := []struct {
		model string
		want  int64
	}{
		{"grok-4.6", 500_000},
		{"grok-4.6-latest", 500_000},
		{"grok-4.5", 500_000},
		{"grok-4.3", 1_000_000},
		{"grok-4.20-0309-reasoning", 1_000_000},
		{"grok-build-0.1", 256_000},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := ContextWindowSizeFor("xai", tt.model); got != tt.want {
				t.Errorf("ContextWindowSizeFor(xai, %q) = %d, want %d", tt.model, got, tt.want)
			}
		})
	}
}

func TestXAIReasoningEffort(t *testing.T) {
	tests := []struct {
		level string
		want  shared.ReasoningEffort
	}{
		// Grok's reasoning models have no off switch, so "none" lands on the
		// lowest tier the API offers rather than being omitted — omitting it
		// would leave xAI's "high" default in force.
		{"none", shared.ReasoningEffortLow},
		{"low", shared.ReasoningEffortLow},
		{"medium", shared.ReasoningEffortMedium},
		{"high", shared.ReasoningEffortHigh},
		{"max", shared.ReasoningEffortXhigh},
		{"xhigh", shared.ReasoningEffortXhigh},
		{"", ""},
		{"nonsense", ""},
	}
	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			if got := xaiReasoningEffort(tt.level); got != tt.want {
				t.Errorf("xaiReasoningEffort(%q) = %q, want %q", tt.level, got, tt.want)
			}
		})
	}
}

func TestXAIModelReasons(t *testing.T) {
	if !xaiModelReasons("grok-4.20-0309-reasoning") {
		t.Error("grok-4.20-0309-reasoning should accept reasoning_effort")
	}
	if xaiModelReasons("grok-4.20-0309-non-reasoning") {
		t.Error("grok-4.20-0309-non-reasoning should not be sent reasoning_effort")
	}
}

func TestXAIToolsDisabled(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"empty", "", false},
		{"one", "1", true},
		{"true", "true", true},
		{"yes", "yes", true},
		{"on", "on", true},
		{"mixed case", "TrUe", true},
		{"surrounded by whitespace", "  on\t", true},
		{"zero", "0", false},
		{"false", "false", false},
		{"unrecognized", "maybe", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(xaiToolsDisableEnvVar, tt.value)
			if got := xaiToolsDisabled(); got != tt.want {
				t.Errorf("xaiToolsDisabled() with %s=%q = %v, want %v", xaiToolsDisableEnvVar, tt.value, got, tt.want)
			}
		})
	}
}

func TestXAIToolsEnabled(t *testing.T) {
	tests := []struct {
		name       string
		configured bool
		value      string
		want       bool
	}{
		// The configured flag short-circuits before the environment is read,
		// so an explicit --xai-tools cannot be undone by a stale export.
		{"configured with empty env", true, "", true},
		{"configured with falsey env", true, "false", true},
		{"empty env", false, "", false},
		{"one", false, "1", true},
		{"true", false, "true", true},
		{"yes", false, "yes", true},
		{"on", false, "on", true},
		{"mixed case", false, "YES", true},
		{"surrounded by whitespace", false, " 1 ", true},
		{"zero", false, "0", false},
		{"false", false, "false", false},
		{"unrecognized", false, "maybe", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(xaiToolsEnvVar, tt.value)
			if got := xaiToolsEnabled(tt.configured); got != tt.want {
				t.Errorf("xaiToolsEnabled(%v) with %s=%q = %v, want %v",
					tt.configured, xaiToolsEnvVar, tt.value, got, tt.want)
			}
		})
	}
}

// The built-in tool objects are assembled as raw JSON rather than marshaled,
// so pin that the bytes still match what marshaling the equivalent map would
// have produced — that equivalence is the whole justification for writing them
// out by hand.
func TestXAIBuiltInToolWireFormat(t *testing.T) {
	for _, typ := range xaiServerSideToolTypes {
		t.Run(typ, func(t *testing.T) {
			want, err := json.Marshal(map[string]string{"type": typ})
			if err != nil {
				t.Fatalf("marshaling the reference form: %v", err)
			}
			got, err := json.Marshal(xaiBuiltInTool(typ))
			if err != nil {
				t.Fatalf("marshaling xaiBuiltInTool(%q): %v", typ, err)
			}
			if string(got) != string(want) {
				t.Errorf("xaiBuiltInTool(%q) = %s, want %s", typ, got, want)
			}
		})
	}
}

// xaiCaptureServer answers one Responses call and hands the caller the
// request headers and decoded body that produced it.
func xaiCaptureServer(t *testing.T, header *http.Header, body *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*header = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, body)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "resp_x",
			"object": "response",
			"status": "completed",
			"model":  "grok-4.6",
			"output": []map[string]any{{
				"type":   "message",
				"id":     "msg_x",
				"role":   "assistant",
				"status": "completed",
				"content": []map[string]any{{
					"type":        "output_text",
					"text":        "Hello from Grok!",
					"annotations": []any{},
				}},
			}},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
		})
	}))
}

func xaiReasoningFromBody(body map[string]any) any {
	reasoning, _ := body["reasoning"].(map[string]any)
	if reasoning == nil {
		return nil
	}
	return reasoning["effort"]
}

func xaiToolTypes(body map[string]any) []string {
	raw, _ := body["tools"].([]any)
	var types []string
	for _, item := range raw {
		tool, _ := item.(map[string]any)
		if typ, _ := tool["type"].(string); typ != "" {
			types = append(types, typ)
		}
	}
	return types
}

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func xaiDrain(t *testing.T, m model.LLM, modelName string) []*model.LLMResponse {
	t.Helper()
	req := &model.LLMRequest{
		Model:    modelName,
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}}},
	}
	var out []*model.LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		out = append(out, resp)
	}
	return out
}

func TestXAINonStreamingSendsReasoningAndConvID(t *testing.T) {
	var header http.Header
	var body map[string]any
	srv := xaiCaptureServer(t, &header, &body)
	defer srv.Close()

	m, err := NewXAI(context.Background(), "grok-4.6", "test-key", srv.URL, "medium", nil)
	if err != nil {
		t.Fatalf("NewXAI() error: %v", err)
	}

	responses := xaiDrain(t, m, "")
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}
	resp := responses[0]
	if resp.Content == nil || len(resp.Content.Parts) == 0 {
		t.Fatal("expected content with parts")
	}
	if got := resp.Content.Parts[0].Text; got != "Hello from Grok!" {
		t.Errorf("text = %q, want %q", got, "Hello from Grok!")
	}
	if !resp.TurnComplete {
		t.Error("expected TurnComplete = true")
	}

	if got := xaiReasoningFromBody(body); got != "medium" {
		t.Errorf("reasoning.effort = %v, want medium", got)
	}
	if got := header.Get(xaiConversationHeader); got == "" {
		t.Errorf("%s header not sent; cache affinity depends on it", xaiConversationHeader)
	}
}

func TestXAIConvIDIsStableAcrossTurns(t *testing.T) {
	var header http.Header
	var body map[string]any
	srv := xaiCaptureServer(t, &header, &body)
	defer srv.Close()

	m, err := NewXAI(context.Background(), "grok-4.6", "test-key", srv.URL, "high", nil)
	if err != nil {
		t.Fatalf("NewXAI() error: %v", err)
	}

	xaiDrain(t, m, "")
	first := header.Get(xaiConversationHeader)
	xaiDrain(t, m, "")
	second := header.Get(xaiConversationHeader)

	if first == "" || first != second {
		t.Errorf("%s = %q then %q; must stay stable across a conversation", xaiConversationHeader, first, second)
	}
}

func TestXAIOmitsReasoningForNonReasoningModel(t *testing.T) {
	var header http.Header
	var body map[string]any
	srv := xaiCaptureServer(t, &header, &body)
	defer srv.Close()

	m, err := NewXAI(context.Background(), "grok-4.20-0309-non-reasoning", "test-key", srv.URL, "high", nil)
	if err != nil {
		t.Fatalf("NewXAI() error: %v", err)
	}

	xaiDrain(t, m, "")
	if got := xaiReasoningFromBody(body); got != nil {
		t.Errorf("reasoning.effort sent to a non-reasoning model: %v", got)
	}
}

func TestXAIPerRequestModelOverrideGatesReasoning(t *testing.T) {
	var header http.Header
	var body map[string]any
	srv := xaiCaptureServer(t, &header, &body)
	defer srv.Close()

	// Constructed against a reasoning model, but the request names a
	// non-reasoning one — the gate must follow the request, not the default.
	m, err := NewXAI(context.Background(), "grok-4.6", "test-key", srv.URL, "high", nil)
	if err != nil {
		t.Fatalf("NewXAI() error: %v", err)
	}

	xaiDrain(t, m, "grok-4.20-0309-non-reasoning")
	if got := xaiReasoningFromBody(body); got != nil {
		t.Errorf("reasoning.effort sent for request model override: %v", got)
	}
	if got := body["model"]; got != "grok-4.20-0309-non-reasoning" {
		t.Errorf("model = %v, want the per-request override", got)
	}
}

func TestXAIExtraHeaderOverridesConvID(t *testing.T) {
	var header http.Header
	var body map[string]any
	srv := xaiCaptureServer(t, &header, &body)
	defer srv.Close()

	opts := &LLMOptions{ExtraHeaders: map[string]string{xaiConversationHeader: "pinned-id"}}
	m, err := NewXAI(context.Background(), "grok-4.6", "test-key", srv.URL, "high", opts)
	if err != nil {
		t.Fatalf("NewXAI() error: %v", err)
	}

	xaiDrain(t, m, "")
	if got := header.Get(xaiConversationHeader); got != "pinned-id" {
		t.Errorf("%s = %q, want the explicit override %q", xaiConversationHeader, got, "pinned-id")
	}
}

func TestXAISendsSystemInstructionAndTools(t *testing.T) {
	var header http.Header
	var body map[string]any
	srv := xaiCaptureServer(t, &header, &body)
	defer srv.Close()

	m, err := NewXAI(context.Background(), "grok-4.6", "test-key", srv.URL, "high", &LLMOptions{EnableXAITools: true})
	if err != nil {
		t.Fatalf("NewXAI() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}}},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: "You are terse."}}},
			Tools: []*genai.Tool{{
				FunctionDeclarations: []*genai.FunctionDeclaration{{
					Name:        "read_file",
					Description: "Read a file",
				}},
			}},
		},
	}
	for _, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
	}

	// The OpenAI-style `include` list must NOT be sent: xAI's Responses API
	// rejects it with a 400 ("Argument not supported: \"web_search_call.results\"
	// in \"include\" field"), which took the whole default-on xAI search feature
	// down in production. The server-side tools run and return their results
	// without it.
	if got := body["include"]; got != nil {
		t.Errorf("include = %v, want it omitted: xAI's Responses API rejects the OpenAI include field", got)
	}
	if got := body["instructions"]; got != "You are terse." {
		t.Errorf("instructions = %v, want the system instruction", got)
	}
	types := xaiToolTypes(body)
	if !containsString(types, "function") {
		t.Errorf("tools = %v, want a function declaration", types)
	}
	for _, want := range xaiServerSideToolTypes {
		if !containsString(types, want) {
			t.Errorf("tools = %v, want built-in %q", types, want)
		}
	}
}

// PI_NO_XAI_TOOLS is the kill switch: it has to beat an explicit opt-in, and
// it has to strip the built-in tools while leaving client-side functions
// untouched. There is no include list to strip anymore — the field is never
// sent, because xAI's Responses API rejects it.
func TestXAIToolsKillSwitchBeatsOptIn(t *testing.T) {
	t.Setenv(xaiToolsDisableEnvVar, "1")

	var header http.Header
	var body map[string]any
	srv := xaiCaptureServer(t, &header, &body)
	defer srv.Close()

	m, err := NewXAI(context.Background(), "grok-4.6", "test-key", srv.URL, "high", &LLMOptions{EnableXAITools: true})
	if err != nil {
		t.Fatalf("NewXAI() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}}},
		Config: &genai.GenerateContentConfig{
			Tools: []*genai.Tool{{
				FunctionDeclarations: []*genai.FunctionDeclaration{{Name: "read_file"}},
			}},
		},
	}
	for _, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
	}

	if got := body["include"]; got != nil {
		t.Errorf("include = %v, want it omitted when %s is set", got, xaiToolsDisableEnvVar)
	}
	types := xaiToolTypes(body)
	if !containsString(types, "function") {
		t.Errorf("tools = %v, want the client-side function to survive the kill switch", types)
	}
	for _, unwanted := range xaiServerSideToolTypes {
		if containsString(types, unwanted) {
			t.Errorf("tools = %v, want built-in %q suppressed by %s", types, unwanted, xaiToolsDisableEnvVar)
		}
	}
}

func TestXAIRejectsNilRequest(t *testing.T) {
	m, err := NewXAI(context.Background(), "grok-4.6", "test-key", "http://127.0.0.1:1", "high", nil)
	if err != nil {
		t.Fatalf("NewXAI() error: %v", err)
	}

	for _, stream := range []bool{false, true} {
		name := "non-streaming"
		if stream {
			name = "streaming"
		}
		t.Run(name, func(t *testing.T) {
			var errs []error
			for resp, err := range m.GenerateContent(context.Background(), nil, stream) {
				if resp != nil {
					t.Errorf("expected no response for a nil request, got %+v", resp)
				}
				errs = append(errs, err)
			}
			if len(errs) != 1 {
				t.Fatalf("expected exactly one error, got %d: %v", len(errs), errs)
			}
			if got := errs[0]; got == nil || !strings.Contains(got.Error(), "nil LLM request") {
				t.Errorf("error = %v, want it to name the nil request", got)
			}
		})
	}
}

// A send failure surfaces differently by mode: streaming reports it as a
// content-less response carrying STREAM_ERROR (which is what lets retryStream
// see it), non-streaming as a wrapped Go error.
func TestXAISendErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"invalid request"}}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	m, err := NewXAI(context.Background(), "grok-4.6", "test-key", srv.URL, "high", nil)
	if err != nil {
		t.Fatalf("NewXAI() error: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}}},
	}

	t.Run("non-streaming", func(t *testing.T) {
		var errs []error
		for _, err := range m.GenerateContent(context.Background(), req, false) {
			errs = append(errs, err)
		}
		if len(errs) != 1 || errs[0] == nil {
			t.Fatalf("expected exactly one error, got %v", errs)
		}
		if got := errs[0].Error(); !strings.Contains(got, "xAI Responses API failed") {
			t.Errorf("error = %q, want it wrapped with the xAI Responses prefix", got)
		}
	})

	t.Run("streaming", func(t *testing.T) {
		var resps []*model.LLMResponse
		for resp, err := range m.GenerateContent(context.Background(), req, true) {
			if err != nil {
				t.Fatalf("streaming yielded a Go error, want a STREAM_ERROR response: %v", err)
			}
			resps = append(resps, resp)
		}
		if len(resps) != 1 {
			t.Fatalf("expected exactly one response, got %d", len(resps))
		}
		if got := resps[0].ErrorCode; got != "STREAM_ERROR" {
			t.Errorf("ErrorCode = %q, want STREAM_ERROR", got)
		}
		if resps[0].ErrorMessage == "" {
			t.Error("ErrorMessage is empty; the provider failure is invisible without it")
		}
	})
}

func TestXAIStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		writeEvent := func(payload map[string]any) {
			b, _ := json.Marshal(payload)
			_, _ = w.Write([]byte("event: " + payload["type"].(string) + "\n"))
			_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
			flusher.Flush()
		}
		writeEvent(map[string]any{
			"type": "response.output_text.delta", "sequence_number": 1,
			"item_id": "msg_1", "output_index": 0, "content_index": 0, "delta": "Hello",
		})
		writeEvent(map[string]any{
			"type": "response.output_text.delta", "sequence_number": 2,
			"item_id": "msg_1", "output_index": 0, "content_index": 0, "delta": " from Grok",
		})
		writeEvent(map[string]any{
			"type": "response.completed", "sequence_number": 3,
			"response": map[string]any{
				"id": "resp_1", "object": "response", "status": "completed",
				"usage": map[string]any{"input_tokens": 8, "output_tokens": 3, "total_tokens": 11},
			},
		})
	}))
	defer srv.Close()

	m, err := NewXAI(context.Background(), "grok-4.6", "test-key", srv.URL, "high", nil)
	if err != nil {
		t.Fatalf("NewXAI() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}}},
	}
	var partials []string
	var final *model.LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		if resp.Partial {
			partials = append(partials, resp.Content.Parts[0].Text)
			continue
		}
		final = resp
	}

	if len(partials) != 2 {
		t.Errorf("partials = %v, want two deltas", partials)
	}
	if final == nil {
		t.Fatal("expected a final response")
	}
	if !final.TurnComplete {
		t.Error("expected TurnComplete = true on the final response")
	}
	if final.Content == nil || len(final.Content.Parts) == 0 {
		t.Fatal("expected content with parts")
	}
	if got := final.Content.Parts[0].Text; got != "Hello from Grok" {
		t.Errorf("accumulated text = %q, want %q", got, "Hello from Grok")
	}
	if final.UsageMetadata == nil || final.UsageMetadata.PromptTokenCount != 8 {
		t.Errorf("usage = %+v, want prompt_tokens 8", final.UsageMetadata)
	}
}

func TestListXAIModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want bearer auth", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "grok-4.6", "owned_by": "xai"}},
		})
	}))
	defer srv.Close()

	models, err := ListModels(context.Background(), "xai", ListModelsOptions{APIKey: "test-key", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("ListModels() error: %v", err)
	}
	if len(models) != 1 || models[0].ID != "grok-4.6" {
		t.Fatalf("models = %+v, want one grok-4.6 entry", models)
	}
}

// A base URL that already carries the version segment must not be extended
// again — XAI_BASE_URL is documented as https://api.x.ai/v1, and the naive
// concatenation would request /v1/v1/models.
func TestListXAIModelsWithVersionedBaseURL(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{}})
	}))
	defer srv.Close()

	if _, err := ListModels(context.Background(), "xai", ListModelsOptions{APIKey: "k", BaseURL: srv.URL + "/v1"}); err != nil {
		t.Fatalf("ListModels() error: %v", err)
	}
	if path != "/v1/models" {
		t.Errorf("path = %q, want /v1/models", path)
	}
}

// --- xAI subscription routing ---

// xaiSubToken builds a JWT-shaped xAI subscription token carrying the claims
// the provider uses to tell it apart from a developer API key.
func xaiSubToken(t *testing.T, exp time.Time) string {
	t.Helper()
	enc := func(v any) string {
		body, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(body)
	}
	return enc(map[string]string{"alg": "RS256"}) + "." + enc(map[string]any{
		"iss":          "https://auth.x.ai",
		"scope":        "openid offline_access grok-cli:access api:access",
		"principal_id": "acct-1",
		"exp":          exp.Unix(),
	}) + ".sig"
}

// TestResolveXAIEndpoint is the routing contract. The two xAI surfaces are not
// interchangeable: sending a subscription bearer to the per-token developer API
// is what produces HTTP 402 personal-team-blocked:spending-limit.
func TestResolveXAIEndpoint(t *testing.T) {
	cases := []struct {
		name         string
		apiKey       string
		baseURL      string
		envKey       string
		wantBase     string
		wantSub      bool
		wantContains string
	}{
		{
			name:         "developer key goes to the developer API",
			apiKey:       "xai-developer-key",
			wantBase:     xaiDefaultBaseURL,
			wantContains: "api.x.ai",
		},
		{
			name:         "subscription token goes to the CLI proxy",
			apiKey:       xaiSubToken(t, time.Now().Add(4*time.Hour)),
			wantBase:     xaiSubscriptionBaseURL,
			wantSub:      true,
			wantContains: "cli-chat-proxy.grok.com",
		},
		{
			name:         "ambient developer key from env",
			envKey:       "xai-env-key",
			wantBase:     xaiDefaultBaseURL,
			wantContains: "api.x.ai",
		},
		{
			name:         "ambient subscription token from env",
			envKey:       xaiSubToken(t, time.Now().Add(4*time.Hour)),
			wantBase:     xaiSubscriptionBaseURL,
			wantSub:      true,
			wantContains: "cli-chat-proxy.grok.com",
		},
		{
			name:         "explicit base URL wins over subscription routing",
			apiKey:       xaiSubToken(t, time.Now().Add(4*time.Hour)),
			baseURL:      "https://my-gateway.internal/v1",
			wantBase:     "https://my-gateway.internal/v1",
			wantSub:      true,
			wantContains: "my-gateway.internal",
		},
		{
			name:         "explicit base URL wins over developer routing",
			apiKey:       "xai-developer-key",
			baseURL:      "https://my-gateway.internal/v1",
			wantBase:     "https://my-gateway.internal/v1",
			wantContains: "my-gateway.internal",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			t.Setenv("XAI_API_KEY", tc.envKey)

			key, base, sub, err := resolveXAIEndpoint(context.Background(), tc.apiKey, tc.baseURL)
			if err != nil {
				t.Fatalf("resolveXAIEndpoint: %v", err)
			}
			if base != tc.wantBase {
				t.Errorf("base URL = %q, want %q", base, tc.wantBase)
			}
			if !strings.Contains(base, tc.wantContains) {
				t.Errorf("base URL = %q, want it to contain %q", base, tc.wantContains)
			}
			if sub != tc.wantSub {
				t.Errorf("subscription = %v, want %v", sub, tc.wantSub)
			}
			if key == "" {
				t.Error("key should not be empty")
			}
			// The credential must be passed through untouched.
			wantKey := tc.apiKey
			if wantKey == "" {
				wantKey = tc.envKey
			}
			if key != wantKey {
				t.Errorf("key = %q, want %q", key, wantKey)
			}
		})
	}
}

// TestResolveXAIEndpointNoCredential pins the actionable error: it must name
// both ways to configure xAI, since the subscription path is not discoverable
// from a bare "API key required".
func TestResolveXAIEndpointNoCredential(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XAI_API_KEY", "")

	_, _, _, err := resolveXAIEndpoint(context.Background(), "", "")
	if err == nil {
		t.Fatal("expected an error when no xAI credential is configured")
	}
	if !strings.Contains(err.Error(), "XAI_API_KEY") {
		t.Errorf("error = %v, want it to mention XAI_API_KEY", err)
	}
	if !strings.Contains(err.Error(), "pi login xai") {
		t.Errorf("error = %v, want it to mention the subscription login", err)
	}
}

// TestNewXAIRequiresCredentialAfterRouting ensures the constructor surfaces the
// missing-credential error rather than constructing a keyless client.
func TestNewXAIRequiresCredentialAfterRouting(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XAI_API_KEY", "")
	if _, err := NewXAI(context.Background(), "grok-4.6", "", "", "high", nil); err == nil {
		t.Fatal("expected an error when no xAI credential is available")
	}
}

// TestNewXAISubscriptionSendsIdentityHeaders verifies the Grok-CLI headers the
// proxy gates on actually reach the wire. Without them the proxy answers as
// though the caller were an unentitled API client, which reads as a
// subscription failure rather than a header problem.
func TestNewXAISubscriptionSendsIdentityHeaders(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	var header http.Header
	var body map[string]any
	srv := xaiCaptureServer(t, &header, &body)
	defer srv.Close()

	t.Setenv("XAI_API_KEY", xaiSubToken(t, time.Now().Add(4*time.Hour)))
	llm, err := NewXAI(context.Background(), "grok-4.6", "", srv.URL, "low", nil)
	if err != nil {
		t.Fatalf("NewXAI: %v", err)
	}
	xaiDrain(t, llm, "grok-4.6")

	for _, want := range []struct{ key, value string }{
		{"X-XAI-Token-Auth", "xai-grok-cli"},
		{"x-grok-client-identifier", "grok-shell"},
		{"x-grok-client-version", xaiClientVersion},
	} {
		if got := header.Get(want.key); got != want.value {
			t.Errorf("header %s = %q, want %q", want.key, got, want.value)
		}
	}
}

// TestNewXAIDeveloperKeySendsNoSubscriptionHeaders: the developer API is not
// gated on Grok-CLI identity, so those headers are noise on that path and the
// bearer must be the developer key itself.
func TestNewXAIDeveloperKeySendsNoSubscriptionHeaders(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	var header http.Header
	var body map[string]any
	srv := xaiCaptureServer(t, &header, &body)
	defer srv.Close()

	t.Setenv("XAI_API_KEY", "xai-developer-key")
	llm, err := NewXAI(context.Background(), "grok-4.6", "", srv.URL, "low", nil)
	if err != nil {
		t.Fatalf("NewXAI: %v", err)
	}
	xaiDrain(t, llm, "grok-4.6")

	if got := header.Get("x-xai-token-auth"); got != "" {
		t.Errorf("x-xai-token-auth = %q, want absent for a developer key", got)
	}
	if got := header.Get("Authorization"); got != "Bearer xai-developer-key" {
		t.Errorf("Authorization = %q, want the developer key as bearer", got)
	}
}

// TestResolveXAIEndpointExpiredExplicitTokenStillRoutesSubscription covers the
// shape the CLI actually produces: ~/.pi-go/.env holds an xAI credential that
// was fresh when written and has since aged out. The expired token must still
// be recognized as a subscription credential and routed to the CLI proxy —
// misreading it as a developer key would send it to api.x.ai and earn a 402.
//
// Whether it gets refreshed is auth.ResolveXAICredential's decision, covered by
// TestResolveXAICredential in the auth package. Here the store is empty, so
// there is nothing to refresh with and the value is passed through unchanged.
func TestResolveXAIEndpointExpiredExplicitTokenStillRoutesSubscription(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XAI_API_KEY", "")

	expired := xaiSubToken(t, time.Now().Add(-time.Hour))
	key, base, sub, err := resolveXAIEndpoint(context.Background(), expired, "")
	if err != nil {
		t.Fatalf("resolveXAIEndpoint: %v", err)
	}
	if !sub {
		t.Error("an expired subscription token must still be recognized as one")
	}
	if base != xaiSubscriptionBaseURL {
		t.Errorf("base URL = %q, want the CLI proxy", base)
	}
	if key != expired {
		t.Error("with nothing to refresh with, the given token should be passed through")
	}
}

// TestResolveXAIEndpointExplicitKeyNeverRefreshed: an operator's developer key
// is authoritative. Resolution must not replace it with a stored subscription
// credential, even when one is present.
func TestResolveXAIEndpointExplicitKeyNeverRefreshed(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("XAI_API_KEY", "")
	// A stored subscription credential exists and must be ignored.
	if err := auth.SaveXAITokens(&auth.XAITokenSet{
		AccessToken:  xaiSubToken(t, time.Now().Add(4*time.Hour)),
		RefreshToken: "should-not-be-used",
		ExpiresAt:    time.Now().Add(4 * time.Hour),
	}); err != nil {
		t.Fatalf("SaveXAITokens: %v", err)
	}

	key, base, sub, err := resolveXAIEndpoint(context.Background(), "xai-operator-key", "")
	if err != nil {
		t.Fatalf("resolveXAIEndpoint: %v", err)
	}
	if key != "xai-operator-key" {
		t.Errorf("key = %q, want the operator's developer key kept", key)
	}
	if sub {
		t.Error("a developer key must not be reported as a subscription token")
	}
	if base != xaiDefaultBaseURL {
		t.Errorf("base URL = %q, want the developer API", base)
	}
}

// TestStoredSubscriptionCredential covers the helper that stops the CLI from
// rejecting a subscription-only xAI setup with "no API key found". An expired
// token must still count as present — refresh happens later.
func TestStoredSubscriptionCredential(t *testing.T) {
	t.Run("subscription store is reported for xai", func(t *testing.T) {
		testenv.SetHome(t, t.TempDir())
		tok := xaiSubToken(t, time.Now().Add(-time.Hour)) // expired on purpose
		if err := auth.SaveXAITokens(&auth.XAITokenSet{
			AccessToken:  tok,
			RefreshToken: "refresh",
			ExpiresAt:    time.Now().Add(-time.Hour),
		}); err != nil {
			t.Fatalf("SaveXAITokens: %v", err)
		}
		if got := StoredSubscriptionCredential("xai"); got != tok {
			t.Error("an expired-but-refreshable token must still be reported as present")
		}
	})

	t.Run("empty when no store exists", func(t *testing.T) {
		testenv.SetHome(t, t.TempDir())
		if got := StoredSubscriptionCredential("xai"); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("other providers are never answered from the xai store", func(t *testing.T) {
		testenv.SetHome(t, t.TempDir())
		if err := auth.SaveXAITokens(&auth.XAITokenSet{
			AccessToken:  xaiSubToken(t, time.Now().Add(time.Hour)),
			RefreshToken: "r",
			ExpiresAt:    time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatalf("SaveXAITokens: %v", err)
		}
		for _, name := range []string{"openai", "anthropic", "gemini", "codex", ""} {
			if got := StoredSubscriptionCredential(name); got != "" {
				t.Errorf("StoredSubscriptionCredential(%q) = %q, want empty", name, got)
			}
		}
	})
}
