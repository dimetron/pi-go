package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"iter"
	"regexp"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/agent"
)

// handlePrompt is the whole wire contract for the "prompt" method: an
// acknowledgement carrying the session id, then a JSONL event stream. The
// literals below are the exact bytes the pre-refactor handler wrote, captured
// by running these same cases against it before any edit, with only the random
// session id normalized. A change in event order, in which parts produce
// events, or in a single JSON field name fails here.

// cogScriptedLLM drives the agent through a fixed sequence of responses, one
// per turn. A turn that runs off the end repeats the last response.
type cogScriptedLLM struct {
	turns []*genai.Content
	n     int
}

func (m *cogScriptedLLM) Name() string { return "cog-scripted" }

func (m *cogScriptedLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		i := min(m.n, len(m.turns)-1)
		m.n++
		yield(&model.LLMResponse{Content: m.turns[i]}, nil)
	}
}

// cogFailingLLM fails the stream, which reaches handlePrompt as an iteration
// error rather than an event.
type cogFailingLLM struct{ err error }

func (m *cogFailingLLM) Name() string { return "cog-failing" }

func (m *cogFailingLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) { yield(nil, m.err) }
}

func cogServer(t *testing.T, llm model.LLM) *Server {
	t.Helper()
	ag, err := agent.New(agent.Config{Model: llm, Instruction: "Test agent"})
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	return NewServer(Config{Agent: ag})
}

var cogSessionIDRe = regexp.MustCompile(`"session_id":"[0-9a-f-]{36}"`)

// cogPrompt runs one prompt request against an in-memory encoder and returns
// the JSONL the handler wrote, with the generated session id normalized so the
// bytes can be compared literally.
func cogPrompt(t *testing.T, s *Server, params string) string {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	req := Request{JSONRPC: "2.0", Method: "prompt", Params: json.RawMessage(params), ID: 1}
	s.handlePrompt(context.Background(), nil, enc, req)
	return cogSessionIDRe.ReplaceAllString(buf.String(), `"session_id":"SESSION"`)
}

func cogAssertStream(t *testing.T, got string, want []string) {
	t.Helper()
	wantStr := strings.Join(want, "\n") + "\n"
	if got != wantStr {
		t.Errorf("stream mismatch\n got:\n%s\nwant:\n%s", got, wantStr)
	}
}

// A plain text answer: acknowledgement, one message_start, one delta per part,
// message_end.
func TestHandlePromptTextStream(t *testing.T) {
	s := cogServer(t, &cogScriptedLLM{turns: []*genai.Content{
		genai.NewContentFromText("Hello from RPC!", genai.RoleModel),
	}})

	cogAssertStream(t, cogPrompt(t, s, `{"text":"hello"}`), []string{
		`{"jsonrpc":"2.0","result":{"session_id":"SESSION"},"id":1}`,
		`{"type":"message_start","agent":"pi","role":"model"}`,
		`{"type":"text_delta","agent":"pi","delta":"Hello from RPC!"}`,
		`{"type":"message_end"}`,
	})
}

// A tool round trip. message_start appears once even though the agent produces
// several events, the call carries its arguments, and the result is the
// marshaled response string.
func TestHandlePromptToolCallStream(t *testing.T) {
	s := cogServer(t, &cogScriptedLLM{turns: []*genai.Content{
		{Role: genai.RoleModel, Parts: []*genai.Part{
			{Text: "calling"},
			{FunctionCall: &genai.FunctionCall{Name: "search", Args: map[string]any{"q": "x", "n": 2}}},
		}},
		genai.NewContentFromText("done", genai.RoleModel),
	}})

	got := cogPrompt(t, s, `{"text":"go"}`)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")

	if len(lines) != 7 {
		t.Fatalf("got %d lines, want 7:\n%s", len(lines), got)
	}
	if lines[0] != `{"jsonrpc":"2.0","result":{"session_id":"SESSION"},"id":1}` {
		t.Errorf("ack = %s", lines[0])
	}
	if lines[1] != `{"type":"message_start","agent":"pi","role":"model"}` {
		t.Errorf("message_start = %s", lines[1])
	}
	if lines[2] != `{"type":"text_delta","agent":"pi","delta":"calling"}` {
		t.Errorf("text_delta = %s", lines[2])
	}
	if lines[3] != `{"type":"tool_call","agent":"pi","tool_name":"search","tool_input":{"n":2,"q":"x"}}` {
		t.Errorf("tool_call = %s", lines[3])
	}
	// The tool does not exist, so the agent's own not-found report comes back
	// as the result payload. Only its envelope is pinned; the prose is the
	// agent's, not this handler's.
	if !strings.HasPrefix(lines[4], `{"type":"tool_result","agent":"pi","content":"{\"error\":\"tool 'search' not found.`) {
		t.Errorf("tool_result = %s", lines[4])
	}
	if !strings.HasSuffix(lines[4], `","tool_name":"search"}`) {
		t.Errorf("tool_result envelope = %s", lines[4])
	}
	if lines[5] != `{"type":"text_delta","agent":"pi","delta":"done"}` {
		t.Errorf("trailing text_delta = %s", lines[5])
	}
	if lines[6] != `{"type":"message_end"}` {
		t.Errorf("message_end = %s", lines[6])
	}
	if strings.Count(got, `"type":"message_start"`) != 1 {
		t.Errorf("message_start appeared %d times, want 1", strings.Count(got, `"type":"message_start"`))
	}
}

// A part carrying none of text, a call or a response produces no event at all.
func TestHandlePromptEmptyPartProducesNothing(t *testing.T) {
	s := cogServer(t, &cogScriptedLLM{turns: []*genai.Content{
		{Role: genai.RoleModel, Parts: []*genai.Part{{}, {Text: "after"}, {}}},
	}})

	cogAssertStream(t, cogPrompt(t, s, `{"text":"hello"}`), []string{
		`{"jsonrpc":"2.0","result":{"session_id":"SESSION"},"id":1}`,
		`{"type":"message_start","agent":"pi","role":"model"}`,
		`{"type":"text_delta","agent":"pi","delta":"after"}`,
		`{"type":"message_end"}`,
	})
}

// A stream failure ends the loop: one error event, then message_end. The
// acknowledgement has already gone out, so the client still learns the session.
func TestHandlePromptStreamErrorStopsTheLoop(t *testing.T) {
	s := cogServer(t, &cogFailingLLM{err: errors.New("model exploded")})

	got := cogPrompt(t, s, `{"text":"hello"}`)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")

	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3:\n%s", len(lines), got)
	}
	if lines[0] != `{"jsonrpc":"2.0","result":{"session_id":"SESSION"},"id":1}` {
		t.Errorf("ack = %s", lines[0])
	}
	if !strings.HasPrefix(lines[1], `{"type":"error","content":"`) || !strings.Contains(lines[1], "model exploded") {
		t.Errorf("error event = %s", lines[1])
	}
	if lines[2] != `{"type":"message_end"}` {
		t.Errorf("message_end = %s", lines[2])
	}
}

// The rejection paths write a JSON-RPC error and no events at all — not even
// message_end, because the agent is never run.
func TestHandlePromptRejections(t *testing.T) {
	tests := []struct {
		name   string
		params string
		want   string
	}{
		{
			name:   "params that are not an object",
			params: `"nope"`,
			want:   `{"jsonrpc":"2.0","error":{"code":-32602,"message":"invalid params: json: cannot unmarshal string into Go value of type jsonrpc.PromptParams"},"id":1}`,
		},
		{
			name:   "params with the wrong type for text",
			params: `{"text":7}`,
			want:   `{"jsonrpc":"2.0","error":{"code":-32602,"message":"invalid params: json: cannot unmarshal number into Go struct field PromptParams.text of type string"},"id":1}`,
		},
		{
			name:   "an empty text",
			params: `{"text":""}`,
			want:   `{"jsonrpc":"2.0","error":{"code":-32602,"message":"params.text is required"},"id":1}`,
		},
		{
			name:   "a missing text",
			params: `{"session_id":"s1"}`,
			want:   `{"jsonrpc":"2.0","error":{"code":-32602,"message":"params.text is required"},"id":1}`,
		},
		{
			name:   "text absent but session present is still rejected",
			params: `{}`,
			want:   `{"jsonrpc":"2.0","error":{"code":-32602,"message":"params.text is required"},"id":1}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := cogServer(t, &cogScriptedLLM{turns: []*genai.Content{
				genai.NewContentFromText("unreached", genai.RoleModel),
			}})
			cogAssertStream(t, cogPrompt(t, s, tt.params), []string{tt.want})
		})
	}
}

// An empty params field is decoded as a nil RawMessage, which is a decode
// failure rather than an empty object.
func TestHandlePromptNilParams(t *testing.T) {
	s := cogServer(t, &cogScriptedLLM{turns: []*genai.Content{
		genai.NewContentFromText("unreached", genai.RoleModel),
	}})

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	s.handlePrompt(context.Background(), nil, enc, Request{JSONRPC: "2.0", Method: "prompt", ID: 1})

	got := buf.String()
	if !strings.HasPrefix(got, `{"jsonrpc":"2.0","error":{"code":-32602,"message":"invalid params: `) {
		t.Errorf("got %s, want an invalid-params error", got)
	}
	if strings.Contains(got, "message_end") {
		t.Error("the agent ran despite unusable params")
	}
}

// A client-supplied session id is used as given and echoed back; no session is
// created for it.
func TestHandlePromptHonorsSuppliedSessionID(t *testing.T) {
	s := cogServer(t, &cogScriptedLLM{turns: []*genai.Content{
		genai.NewContentFromText("hi", genai.RoleModel),
	}})

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	req := Request{JSONRPC: "2.0", Method: "prompt", Params: json.RawMessage(`{"text":"hello","session_id":"caller-supplied"}`), ID: 42}
	s.handlePrompt(context.Background(), nil, enc, req)

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if lines[0] != `{"jsonrpc":"2.0","result":{"session_id":"caller-supplied"},"id":42}` {
		t.Errorf("ack = %s", lines[0])
	}
	if lines[len(lines)-1] != `{"type":"message_end"}` {
		t.Errorf("last line = %s, want message_end", lines[len(lines)-1])
	}
}

// The request ID is echoed verbatim, including the JSON-native shapes a client
// may use for it.
func TestHandlePromptEchoesRequestID(t *testing.T) {
	tests := []struct {
		name string
		id   any
		want string
	}{
		{"a number", 7, `"id":7`},
		{"a string", "abc", `"id":"abc"`},
		{"null", nil, `"id":null`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := cogServer(t, &cogScriptedLLM{turns: []*genai.Content{
				genai.NewContentFromText("hi", genai.RoleModel),
			}})
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			req := Request{JSONRPC: "2.0", Method: "prompt", Params: json.RawMessage(`{"text":"x"}`), ID: tt.id}
			s.handlePrompt(context.Background(), nil, enc, req)

			if !strings.Contains(strings.Split(buf.String(), "\n")[0], tt.want) {
				t.Errorf("ack = %s, want it to carry %s", strings.Split(buf.String(), "\n")[0], tt.want)
			}
		})
	}
}

// Two prompts arriving on separate connections run one after the other: runMu
// is held for a whole stream, so neither run is interleaved with the other.
//
// Each connection gets its own encoder in handleConn, and handleConn dispatches
// a connection's requests sequentially, so two handlePrompt calls never share an
// encoder in production. This test models that: one buffer and one encoder per
// simulated connection. Sharing a single encoder across both goroutines would
// race on the acknowledgement write, which handlePrompt performs before
// streamRun takes runMu -- a race the server itself cannot reach.
func TestHandlePromptStreamsAreNotInterleaved(t *testing.T) {
	s := cogServer(t, &cogScriptedLLM{turns: []*genai.Content{
		genai.NewContentFromText("one", genai.RoleModel),
	}})

	bufs := make([]bytes.Buffer, 2)
	done := make(chan struct{}, len(bufs))
	for i := range bufs {
		go func() {
			s.handlePrompt(context.Background(), nil, json.NewEncoder(&bufs[i]), Request{
				JSONRPC: "2.0", Method: "prompt",
				Params: json.RawMessage(`{"text":"hello"}`), ID: 1,
			})
			done <- struct{}{}
		}()
	}
	for range bufs {
		<-done
	}

	// Each connection sees its own complete, self-contained stream: the
	// acknowledgement followed by exactly one run's events, in order.
	for i := range bufs {
		got := cogSessionIDRe.ReplaceAllString(bufs[i].String(), `"session_id":"SESSION"`)
		lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
		if len(lines) != 4 {
			t.Errorf("connection %d: got %d lines, want 4:\n%s", i, len(lines), got)
			continue
		}
		want := []string{
			`{"jsonrpc":"2.0","result":{"session_id":"SESSION"},"id":1}`,
			`{"type":"message_start","agent":"pi","role":"model"}`,
			`{"type":"text_delta","agent":"pi","delta":"one"}`,
			`{"type":"message_end"}`,
		}
		for j, w := range want {
			if lines[j] != w {
				t.Errorf("connection %d line %d = %s, want %s", i, j, lines[j], w)
			}
		}
	}
}

// encodePart carries the per-part mapping that the stream loop used to hold
// inline. These drive it directly, which is the only way to reach the
// marshal-failure fallback: a response the agent produces always marshals.
func TestEncodePart(t *testing.T) {
	tests := []struct {
		name string
		part *genai.Part
		want []string
	}{
		{
			name: "text becomes one delta",
			part: &genai.Part{Text: "hello"},
			want: []string{`{"type":"text_delta","agent":"pi","delta":"hello"}`},
		},
		{
			name: "an empty part produces nothing",
			part: &genai.Part{},
			want: nil,
		},
		{
			name: "a call carries its name and arguments",
			part: &genai.Part{FunctionCall: &genai.FunctionCall{Name: "search", Args: map[string]any{"q": "x", "n": 2}}},
			want: []string{`{"type":"tool_call","agent":"pi","tool_name":"search","tool_input":{"n":2,"q":"x"}}`},
		},
		{
			// Args is a nil map inside an any field, which omitempty does not
			// drop, so the null reaches the client.
			name: "a call with no arguments still sends a null tool_input",
			part: &genai.Part{FunctionCall: &genai.FunctionCall{Name: "ping"}},
			want: []string{`{"type":"tool_call","agent":"pi","tool_name":"ping","tool_input":null}`},
		},
		{
			name: "a response is marshaled into content",
			part: &genai.Part{FunctionResponse: &genai.FunctionResponse{Name: "search", Response: map[string]any{"n": 1}}},
			want: []string{`{"type":"tool_result","agent":"pi","content":"{\"n\":1}","tool_name":"search"}`},
		},
		{
			name: "a nil response marshals to null",
			part: &genai.Part{FunctionResponse: &genai.FunctionResponse{Name: "search"}},
			want: []string{`{"type":"tool_result","agent":"pi","content":"null","tool_name":"search"}`},
		},
		{
			name: "all three shapes emit in order",
			part: &genai.Part{
				Text:             "t",
				FunctionCall:     &genai.FunctionCall{Name: "c"},
				FunctionResponse: &genai.FunctionResponse{Name: "r", Response: map[string]any{"ok": true}},
			},
			want: []string{
				`{"type":"text_delta","agent":"pi","delta":"t"}`,
				`{"type":"tool_call","agent":"pi","tool_name":"c","tool_input":null}`,
				`{"type":"tool_result","agent":"pi","content":"{\"ok\":true}","tool_name":"r"}`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			encodePart(json.NewEncoder(&buf), "pi", tt.part)

			got := buf.String()
			wantStr := ""
			if len(tt.want) > 0 {
				wantStr = strings.Join(tt.want, "\n") + "\n"
			}
			if got != wantStr {
				t.Errorf("encodePart\n got: %q\nwant: %q", got, wantStr)
			}
		})
	}
}

// A response that will not marshal falls back to its Go rendering rather than
// dropping the tool_result event entirely.
func TestEncodePartUnmarshalableResponse(t *testing.T) {
	part := &genai.Part{FunctionResponse: &genai.FunctionResponse{
		Name:     "broken",
		Response: map[string]any{"ch": make(chan int)},
	}}

	var buf bytes.Buffer
	encodePart(json.NewEncoder(&buf), "pi", part)

	var ev Event
	if err := json.Unmarshal(buf.Bytes(), &ev); err != nil {
		t.Fatalf("decoding the event: %v (raw: %s)", err, buf.String())
	}
	if ev.Type != "tool_result" {
		t.Errorf("type = %q, want tool_result", ev.Type)
	}
	if ev.ToolName != "broken" {
		t.Errorf("tool_name = %q, want broken", ev.ToolName)
	}
	if !strings.HasPrefix(ev.Content, "map[ch:0x") {
		t.Errorf("content = %q, want the Go rendering of the map", ev.Content)
	}
}

// writeError is the single shape every rejection uses.
func TestWriteError(t *testing.T) {
	var buf bytes.Buffer
	writeError(json.NewEncoder(&buf), "req-9", -32000, "creating session: disk full")

	want := `{"jsonrpc":"2.0","error":{"code":-32000,"message":"creating session: disk full"},"id":"req-9"}` + "\n"
	if buf.String() != want {
		t.Errorf("writeError\n got: %s\nwant: %s", buf.String(), want)
	}
}
