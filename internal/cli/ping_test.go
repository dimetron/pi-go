package cli

import (
	"context"
	"crypto/tls"
	"errors"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// ---------------------------------------------------------------------------
// Mocks for modelPing tests
// ---------------------------------------------------------------------------

// pingMockLLM yields a fixed set of responses (or a single error) for every
// GenerateContent call.  modelPing calls GenerateContent twice (non-streaming
// then streaming), so the same slice is replayed each time.
type pingMockLLM struct {
	name      string
	responses []*model.LLMResponse
	err       error
}

func (m *pingMockLLM) Name() string { return m.name }

func (m *pingMockLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if m.err != nil {
			yield(nil, m.err)
			return
		}
		for _, r := range m.responses {
			if !yield(r, nil) {
				return
			}
		}
	}
}

// cliThinkingLLM yields a "thinking"-role event then a normal model-text event.
type cliThinkingLLM struct {
	name         string
	thoughtText  string
	responseText string
}

func (m *cliThinkingLLM) Name() string { return m.name }

func (m *cliThinkingLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		thinking := &model.LLMResponse{
			Content: &genai.Content{
				Role:  "thinking",
				Parts: []*genai.Part{{Text: m.thoughtText}},
			},
		}
		if !yield(thinking, nil) {
			return
		}
		text := &model.LLMResponse{
			Content: genai.NewContentFromText(m.responseText, genai.RoleModel),
		}
		yield(text, nil)
	}
}

// ---------------------------------------------------------------------------
// modelPing unit tests
// ---------------------------------------------------------------------------

// TestModelPingPingPong verifies that modelPing succeeds and returns the model
// reply when isPingPong=true and the mock LLM echoes "Prompt:Prompt".
func TestModelPingPingPong(t *testing.T) {
	llm := &pingMockLLM{
		name:      "mock-ping-pong",
		responses: []*model.LLMResponse{{Content: genai.NewContentFromText("Prompt:Prompt", genai.RoleModel)}},
	}

	reply, err := modelPing(context.Background(), llm, "Prompt", true)
	if err != nil {
		t.Fatalf("modelPing returned unexpected error: %v", err)
	}
	if reply != "Prompt:Prompt" {
		t.Errorf("modelPing reply = %q, want %q", reply, "Prompt:Prompt")
	}
}

// TestModelPingCustomPrompt verifies that modelPing works with isPingPong=false
// and a custom prompt / arbitrary response text.
func TestModelPingCustomPrompt(t *testing.T) {
	want := "42"
	llm := &pingMockLLM{
		name:      "mock-custom",
		responses: []*model.LLMResponse{{Content: genai.NewContentFromText(want, genai.RoleModel)}},
	}

	reply, err := modelPing(context.Background(), llm, "2+2", false)
	if err != nil {
		t.Fatalf("modelPing returned unexpected error: %v", err)
	}
	if reply != want {
		t.Errorf("modelPing reply = %q, want %q", reply, want)
	}
}

// TestModelPingEmptyResponse verifies that an LLM returning no text causes
// modelPing to return a descriptive non-nil error.
func TestModelPingEmptyResponse(t *testing.T) {
	llm := &pingMockLLM{
		name: "mock-empty",
		responses: []*model.LLMResponse{
			{Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{}}},
		},
	}

	_, err := modelPing(context.Background(), llm, "Prompt", true)
	if err == nil {
		t.Fatal("expected error for empty LLM response, got nil")
	}
	if !strings.Contains(err.Error(), "empty response") {
		t.Errorf("error %q should mention empty response", err.Error())
	}
}

// TestModelPingLLMError verifies that an error from the LLM is wrapped and
// propagated by modelPing.  The non-streaming call executes first, so its
// error surfaces.
func TestModelPingLLMError(t *testing.T) {
	sentinel := errors.New("llm backend unavailable")
	llm := &pingMockLLM{
		name: "mock-error",
		err:  sentinel,
	}

	_, err := modelPing(context.Background(), llm, "Prompt", true)
	if err == nil {
		t.Fatal("expected error from modelPing, got nil")
	}
	if !errors.Is(err, sentinel) && !strings.Contains(err.Error(), sentinel.Error()) {
		t.Errorf("expected sentinel error to be wrapped, got: %v", err)
	}
}

// TestModelPingThinkingRole verifies that content with role "thinking" is
// excluded from the streaming text accumulator but does not prevent modelPing
// from returning the non-streaming text result.
func TestModelPingThinkingRole(t *testing.T) {
	// The mock returns: [thinking event, text event].
	// Non-stream pass: collects text from the text event.
	// Stream pass: ignores the thinking chunk; collects text from the text event.
	llm := &pingMockLLM{
		name: "mock-thinking",
		responses: []*model.LLMResponse{
			{
				Content: &genai.Content{
					Role:  "thinking",
					Parts: []*genai.Part{{Text: "internal thought"}},
				},
			},
			{Content: genai.NewContentFromText("Final answer", genai.RoleModel)},
		},
	}

	reply, err := modelPing(context.Background(), llm, "Explain Go", false)
	if err != nil {
		t.Fatalf("modelPing returned unexpected error: %v", err)
	}
	if reply == "" {
		t.Error("expected non-empty reply from modelPing with thinking+text response")
	}
}

// ---------------------------------------------------------------------------
// runPrint / runJSON context-cancellation and thinking-output tests
// ---------------------------------------------------------------------------

// TestRunPrintContextCancelled verifies that runPrint returns nil (not an
// error) when the context is already cancelled before execution.
func TestRunPrintContextCancelled(t *testing.T) {
	llm := &cliMockLLM{name: "test-cancel-print", response: "should not appear"}
	ag, sessionID := newTestAgent(t, llm)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := runPrint(ctx, ag, sessionID, "hello", nil)
	if err != nil {
		t.Fatalf("runPrint with cancelled context returned error: %v", err)
	}
}

// TestRunJSONContextCancelled verifies that runJSON emits a message_end event
// and returns nil when the context is already cancelled.
func TestRunJSONContextCancelled(t *testing.T) {
	llm := &cliMockLLM{name: "test-cancel-json", response: "should not appear"}
	ag, sessionID := newTestAgent(t, llm)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	stdout := captureStdout(t, func() {
		err := runJSON(ctx, ag, sessionID, "hello", nil)
		if err != nil {
			t.Errorf("runJSON with cancelled context returned error: %v", err)
		}
	})

	if !strings.Contains(stdout, "message_end") {
		t.Errorf("runJSON should emit message_end on context cancellation, got: %q", stdout)
	}
}

// TestRunPrintThinkingOutput verifies that thinking-role content is written to
// stderr (dim ANSI) and that normal text goes to stdout.
func TestRunPrintThinkingOutput(t *testing.T) {
	llm := &cliThinkingLLM{
		name:         "test-thinking-print",
		thoughtText:  "internal reasoning",
		responseText: "visible answer",
	}
	ag, sessionID := newTestAgent(t, llm)

	var stdout, stderr string
	stderr = captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			if err := runPrint(context.Background(), ag, sessionID, "think about it", nil); err != nil {
				t.Errorf("runPrint error: %v", err)
			}
		})
	})

	if !strings.Contains(stdout, "visible answer") {
		t.Errorf("stdout should contain the visible answer, got: %q", stdout)
	}
	if !strings.Contains(stderr, "internal reasoning") {
		t.Errorf("stderr should contain thinking content, got: %q", stderr)
	}
}

func TestDefaultAPIBaseURL(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"anthropic", "https://api.anthropic.com"},
		{"openai", "https://api.openai.com"},
		{"gemini", "https://generativelanguage.googleapis.com"},
		{"ollama", ""},
		{"", ""},
		{"unknown", ""},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := defaultAPIBaseURL(tt.provider)
			if got != tt.want {
				t.Errorf("defaultAPIBaseURL(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestPingEndpoint(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"anthropic", "/v1/messages"},
		{"openai", "/v1/models"},
		{"gemini", "/v1beta/models"},
		{"ollama", "/"},
		{"", "/"},
		{"unknown", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := pingEndpoint(tt.provider)
			if got != tt.want {
				t.Errorf("pingEndpoint(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"over limit", "hello world", 5, "hello..."},
		{"empty string", "", 5, ""},
		{"zero limit", "hello", 0, "..."},
		{"single char limit", "hello", 1, "h..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.s, tt.n)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
			}
		})
	}
}

func TestTLSVersionString(t *testing.T) {
	tests := []struct {
		version uint16
		want    string
	}{
		{tls.VersionTLS10, "1.0"},
		{tls.VersionTLS11, "1.1"},
		{tls.VersionTLS12, "1.2"},
		{tls.VersionTLS13, "1.3"},
		{0x0000, "0x0000"},
		{0xFFFF, "0xffff"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tlsVersionString(tt.version)
			if got != tt.want {
				t.Errorf("tlsVersionString(0x%04x) = %q, want %q", tt.version, got, tt.want)
			}
		})
	}
}

func TestNewPingCmd(t *testing.T) {
	cmd := newPingCmd()
	if cmd.Use != "ping [prompt...]" {
		t.Errorf("unexpected Use: %s", cmd.Use)
	}
	// Verify flags exist.
	flags := []string{"model", "url", "smol", "slow", "plan"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing flag: %s", name)
		}
	}
}
