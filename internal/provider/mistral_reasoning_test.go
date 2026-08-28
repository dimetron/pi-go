package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// mistralCapture serves a fixed non-streaming completion and records every
// request body it received, so tests can assert on what went on the wire.
type mistralCapture struct {
	srv    *httptest.Server
	bodies []map[string]any
}

func newMistralCapture(t *testing.T, respond func(w http.ResponseWriter)) *mistralCapture {
	t.Helper()
	c := &mistralCapture{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("request body is not JSON: %v (%s)", err, raw)
			return
		}
		c.bodies = append(c.bodies, body)
		respond(w)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func mistralPlainCompletion(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"id":"c1","object":"chat.completion","model":"m",` +
		`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
}

// mistralSend runs one non-streaming turn against the capture server.
func mistralSend(t *testing.T, m model.LLM) {
	t.Helper()
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}
	for _, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
	}
}

func TestMistralRequestReasoningFields(t *testing.T) {
	tests := []struct {
		name           string
		modelName      string
		thinkingLevel  string
		wantEffort     string // "" means the field must be absent
		wantPromptMode string
	}{
		{name: "small latest high", modelName: "mistral-small-latest", thinkingLevel: "high", wantEffort: "high"},
		{name: "medium 3.5 medium", modelName: "mistral-medium-3.5", thinkingLevel: "medium", wantEffort: "high"},
		{name: "small 2603 max", modelName: "mistral-small-2603", thinkingLevel: "max", wantEffort: "high"},
		{name: "small latest none", modelName: "mistral-small-latest", thinkingLevel: "none", wantEffort: "none"},
		{name: "small latest unset", modelName: "mistral-small-latest", thinkingLevel: ""},
		{name: "magistral high", modelName: "magistral-medium-latest", thinkingLevel: "high", wantPromptMode: "reasoning"},
		{name: "magistral none", modelName: "magistral-medium-latest", thinkingLevel: "none"},
		{name: "magistral unset", modelName: "magistral-medium-latest", thinkingLevel: ""},
		{name: "non-reasoning model ignores the level", modelName: "mistral-large-latest", thinkingLevel: "high"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newMistralCapture(t, mistralPlainCompletion)
			m, err := NewMistral(context.Background(), tt.modelName, "test-key", c.srv.URL, tt.thinkingLevel, nil)
			if err != nil {
				t.Fatalf("NewMistral() error: %v", err)
			}
			mistralSend(t, m)

			if len(c.bodies) != 1 {
				t.Fatalf("expected 1 captured request, got %d", len(c.bodies))
			}
			body := c.bodies[0]

			effort, _ := body["reasoning_effort"].(string)
			if effort != tt.wantEffort {
				t.Errorf("reasoning_effort = %q, want %q", effort, tt.wantEffort)
			}
			promptMode, _ := body["prompt_mode"].(string)
			if promptMode != tt.wantPromptMode {
				t.Errorf("prompt_mode = %q, want %q", promptMode, tt.wantPromptMode)
			}
		})
	}
}

var mistralUUIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func TestMistralPromptCacheKeyStablePerInstance(t *testing.T) {
	c := newMistralCapture(t, mistralPlainCompletion)
	m, err := NewMistral(context.Background(), "mistral-large-latest", "test-key", c.srv.URL, "", nil)
	if err != nil {
		t.Fatalf("NewMistral() error: %v", err)
	}
	mistralSend(t, m)
	mistralSend(t, m)

	if len(c.bodies) != 2 {
		t.Fatalf("expected 2 captured requests, got %d", len(c.bodies))
	}
	first, _ := c.bodies[0]["prompt_cache_key"].(string)
	second, _ := c.bodies[1]["prompt_cache_key"].(string)
	if !mistralUUIDRe.MatchString(first) {
		t.Errorf("prompt_cache_key = %q, want a UUID", first)
	}
	if first != second {
		t.Errorf("prompt_cache_key changed between turns: %q then %q", first, second)
	}

	// A second instance is a second session, so it must not share the key.
	other, err := NewMistral(context.Background(), "mistral-large-latest", "test-key", c.srv.URL, "", nil)
	if err != nil {
		t.Fatalf("NewMistral() error: %v", err)
	}
	mistralSend(t, other)
	third, _ := c.bodies[2]["prompt_cache_key"].(string)
	if third == first {
		t.Error("two model instances shared one prompt_cache_key")
	}
}

func TestMistralReasoningEffort(t *testing.T) {
	tests := []struct {
		level string
		want  string
	}{
		{"none", "none"},
		{"NONE", "none"},
		{"off", "none"},
		{"low", "high"},
		{"medium", "high"},
		{"high", "high"},
		{" High ", "high"},
		{"max", "high"},
		{"xhigh", "high"},
		{"", ""},
		{"turbo", ""},
	}
	for _, tt := range tests {
		if got := mistralReasoningEffort(tt.level); got != tt.want {
			t.Errorf("mistralReasoningEffort(%q) = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func TestMistralUsesReasoningEffort(t *testing.T) {
	yes := []string{"mistral-small-2603", "mistral-small-latest", "mistral-medium-3.5", "MISTRAL-SMALL-LATEST"}
	no := []string{"mistral-large-latest", "magistral-medium-latest", "codestral-2508", ""}
	for _, name := range yes {
		if !mistralUsesReasoningEffort(name) {
			t.Errorf("mistralUsesReasoningEffort(%q) = false, want true", name)
		}
	}
	for _, name := range no {
		if mistralUsesReasoningEffort(name) {
			t.Errorf("mistralUsesReasoningEffort(%q) = true, want false", name)
		}
	}
}

func TestMistralUsesPromptMode(t *testing.T) {
	yes := []string{"magistral-medium-latest", "magistral-small-2509", "Magistral-Medium"}
	no := []string{"mistral-large-latest", "mistral-small-latest", ""}
	for _, name := range yes {
		if !mistralUsesPromptMode(name) {
			t.Errorf("mistralUsesPromptMode(%q) = false, want true", name)
		}
	}
	for _, name := range no {
		if mistralUsesPromptMode(name) {
			t.Errorf("mistralUsesPromptMode(%q) = true, want false", name)
		}
	}
}

const mistralThinkChunk = `[{"type":"thinking","thinking":[{"type":"text","text":"step one "},` +
	`{"type":"text","text":"step two"}]},{"type":"text","text":"the answer"}]`

func TestMistralDeltaThinking(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "thinking chunk plus text chunk",
			raw:  `{"choices":[{"index":0,"delta":{"content":` + mistralThinkChunk + `}}]}`,
			want: "step one step two",
		},
		{
			name: "two thinking chunks concatenate",
			raw: `{"choices":[{"index":0,"delta":{"content":[` +
				`{"type":"thinking","thinking":[{"type":"text","text":"a"}]},` +
				`{"type":"thinking","thinking":[{"type":"text","text":"b"}]}]}}]}`,
			want: "ab",
		},
		{name: "plain string content", raw: `{"choices":[{"index":0,"delta":{"content":"hello"}}]}`},
		{name: "no choices", raw: `{"choices":[]}`},
		{name: "malformed JSON", raw: `{"choices":[`},
		{name: "empty array content", raw: `{"choices":[{"index":0,"delta":{"content":[]}}]}`},
		{name: "unknown chunk types", raw: `{"choices":[{"index":0,"delta":{"content":[{"type":"image"}]}}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mistralDeltaThinking(tt.raw); got != tt.want {
				t.Errorf("mistralDeltaThinking() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMistralMessageThinking(t *testing.T) {
	raw := `{"choices":[{"index":0,"message":{"role":"assistant","content":` + mistralThinkChunk + `}}]}`
	if got := mistralMessageThinking(raw); got != "step one step two" {
		t.Errorf("mistralMessageThinking() = %q, want %q", got, "step one step two")
	}
	plain := `{"choices":[{"index":0,"message":{"role":"assistant","content":"hello"}}]}`
	if got := mistralMessageThinking(plain); got != "" {
		t.Errorf("mistralMessageThinking(plain) = %q, want %q", got, "")
	}
	if got := mistralMessageThinking(`not json`); got != "" {
		t.Errorf("mistralMessageThinking(malformed) = %q, want %q", got, "")
	}
}

func TestMistralAnswerText(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "plain answer passes through", content: "hello", want: "hello"},
		{name: "empty stays empty"},
		{name: "chunk array yields text chunks only", content: mistralThinkChunk, want: "the answer"},
		{
			name:    "thinking-only array yields nothing",
			content: `[{"type":"thinking","thinking":[{"type":"text","text":"a"}]}]`,
		},
		{
			name:    "multiple text chunks concatenate",
			content: `[{"type":"text","text":"one "},{"type":"text","text":"two"}]`,
			want:    "one two",
		},
		// An answer that merely looks like JSON must survive untouched: only a
		// parseable array of recognized Mistral chunks is treated as one.
		{name: "bracketed prose", content: "[see note 1]", want: "[see note 1]"},
		{name: "unrecognized array", content: `[{"type":"image","url":"x"}]`, want: `[{"type":"image","url":"x"}]`},
		{name: "array of strings", content: `["a","b"]`, want: `["a","b"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mistralAnswerText(tt.content); got != tt.want {
				t.Errorf("mistralAnswerText(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

// TestMistralNonStreamingThinking pins the whole non-streaming path: the
// thinking text is prepended as its own part and the answer part carries the
// text, not the raw JSON array the SDK decoded into Content.
func TestMistralNonStreamingThinking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","object":"chat.completion","model":"mistral-small-latest",` +
			`"choices":[{"index":0,"message":{"role":"assistant","content":` + mistralThinkChunk +
			`},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`))
	}))
	defer srv.Close()

	m, err := NewMistral(context.Background(), "mistral-small-latest", "test-key", srv.URL, "high", nil)
	if err != nil {
		t.Fatalf("NewMistral() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}
	var responses []*model.LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		responses = append(responses, resp)
	}

	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}
	parts := responses[0].Content.Parts
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts (thinking + answer), got %d: %+v", len(parts), parts)
	}
	if parts[0].Text != "step one step two" {
		t.Errorf("thinking part = %q, want %q", parts[0].Text, "step one step two")
	}
	if parts[1].Text != "the answer" {
		t.Errorf("answer part = %q, want %q", parts[1].Text, "the answer")
	}
}

// TestMistralStreamingThinking walks the shape Mistral documents for a
// streaming reasoning turn: a thinking-only chunk, a chunk that closes thinking
// and opens the answer, then plain-string answer deltas.
func TestMistralStreamingThinking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"id":"s1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":[{"type":"thinking","thinking":[{"type":"text","text":"first "}]}]}}]}`,
			`{"id":"s1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"second"}]},{"type":"text","text":"Hello"}]}}]}`,
			`{"id":"s1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"}}]}`,
			`{"id":"s1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
		}
		for _, c := range chunks {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	m, err := NewMistral(context.Background(), "mistral-small-latest", "test-key", srv.URL, "high", nil)
	if err != nil {
		t.Fatalf("NewMistral() error: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}
	var thinking, answer strings.Builder
	var last *model.LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("GenerateContent error: %v", err)
		}
		last = resp
		if !resp.Partial || resp.Content == nil || len(resp.Content.Parts) == 0 {
			continue
		}
		if resp.Content.Role == "thinking" {
			thinking.WriteString(resp.Content.Parts[0].Text)
		} else {
			answer.WriteString(resp.Content.Parts[0].Text)
		}
	}

	if thinking.String() != "first second" {
		t.Errorf("streamed thinking = %q, want %q", thinking.String(), "first second")
	}
	if answer.String() != "Hello world" {
		t.Errorf("streamed answer = %q, want %q", answer.String(), "Hello world")
	}
	if last == nil || !last.TurnComplete {
		t.Fatal("expected a final TurnComplete response")
	}
	if len(last.Content.Parts) == 0 || last.Content.Parts[0].Text != "Hello world" {
		t.Errorf("final content = %+v, want the answer text intact", last.Content.Parts)
	}
}
