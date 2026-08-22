package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	ollamaapi "github.com/ollama/ollama/api"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// This file pins the behavior of the genai.Content -> wire-format translation
// layer and of the streaming event dispatchers, so the complexity refactor of
// antContentsToMessages / ollamaContentsToMessages / oaiContentsToMessages /
// oaiContentsToResponsesInput and the *RunStreaming* loops is demonstrably
// behavior-preserving.

// -----------------------------------------------------------------------------
// Shared genai-side helpers
// -----------------------------------------------------------------------------

func TestGenaiSystemInstruction(t *testing.T) {
	tests := []struct {
		name   string
		config *genai.GenerateContentConfig
		want   string
	}{
		{name: "nil config", config: nil, want: ""},
		{
			name:   "nil system instruction",
			config: &genai.GenerateContentConfig{},
			want:   "",
		},
		{
			name:   "no parts",
			config: &genai.GenerateContentConfig{SystemInstruction: &genai.Content{}},
			want:   "",
		},
		{
			name: "single part",
			config: &genai.GenerateContentConfig{
				SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: "be terse"}}},
			},
			want: "be terse",
		},
		{
			name: "joins parts and skips nil and empty",
			config: &genai.GenerateContentConfig{
				SystemInstruction: &genai.Content{Parts: []*genai.Part{
					{Text: "one"},
					nil,
					{Text: ""},
					{Text: "two"},
				}},
			},
			want: "one\ntwo",
		},
		{
			name: "surrounding whitespace trimmed",
			config: &genai.GenerateContentConfig{
				SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: "  padded  "}}},
			},
			want: "padded",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := genaiSystemInstruction(tt.config); got != tt.want {
				t.Errorf("genaiSystemInstruction() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenaiFunctionResponses(t *testing.T) {
	contents := []*genai.Content{
		nil,
		{Role: "user", Parts: nil},
		{Role: "user", Parts: []*genai.Part{
			nil,
			{Text: "not a response"},
			{FunctionResponse: &genai.FunctionResponse{ID: "a", Response: map[string]any{"result": "ra"}}},
		}},
		{Role: "user", Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{ID: "b", Response: map[string]any{"result": "rb"}}},
		}},
	}

	got := genaiFunctionResponses(contents)
	if len(got) != 2 {
		t.Fatalf("got %d responses, want 2: %v", len(got), got)
	}
	for _, id := range []string{"a", "b"} {
		if got[id] == nil {
			t.Errorf("missing function response %q", id)
		}
	}
}

func TestGenaiFunctionResponses_LastWins(t *testing.T) {
	// Two responses share an ID; the later one must win, as the original
	// map-assignment loop did.
	contents := []*genai.Content{
		{Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{ID: "dup", Response: map[string]any{"result": "first"}}}}},
		{Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{ID: "dup", Response: map[string]any{"result": "second"}}}}},
	}
	got := genaiFunctionResponses(contents)
	if len(got) != 1 {
		t.Fatalf("got %d responses, want 1", len(got))
	}
	if payload := oaiFunctionResponseContent(got["dup"].Response); payload != "second" {
		t.Errorf("Response = %v, want %q", payload, "second")
	}
}

func TestGenaiSplitParts(t *testing.T) {
	call := &genai.FunctionCall{ID: "c1", Name: "fn"}
	tests := []struct {
		name      string
		parts     []*genai.Part
		wantText  []string
		wantCalls int
	}{
		{name: "nil parts", parts: nil, wantText: nil, wantCalls: 0},
		{name: "only nils", parts: []*genai.Part{nil, nil}, wantText: nil, wantCalls: 0},
		{
			name:      "text only",
			parts:     []*genai.Part{{Text: "a"}, {Text: "b"}},
			wantText:  []string{"a", "b"},
			wantCalls: 0,
		},
		{
			name:      "calls only",
			parts:     []*genai.Part{{FunctionCall: call}},
			wantText:  nil,
			wantCalls: 1,
		},
		{
			name:      "mixed, nils skipped",
			parts:     []*genai.Part{nil, {Text: "a"}, {FunctionCall: call}, {Text: "b"}},
			wantText:  []string{"a", "b"},
			wantCalls: 1,
		},
		{
			// A part carrying text is never also read as a function call:
			// the original used if/else-if, not two ifs.
			name:      "text wins over call on same part",
			parts:     []*genai.Part{{Text: "a", FunctionCall: call}},
			wantText:  []string{"a"},
			wantCalls: 0,
		},
		{
			name:      "function response part is neither",
			parts:     []*genai.Part{{FunctionResponse: &genai.FunctionResponse{ID: "c1"}}},
			wantText:  nil,
			wantCalls: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, calls := genaiSplitParts(tt.parts)
			if len(text) != len(tt.wantText) {
				t.Fatalf("text = %v, want %v", text, tt.wantText)
			}
			for i := range text {
				if text[i] != tt.wantText[i] {
					t.Errorf("text[%d] = %q, want %q", i, text[i], tt.wantText[i])
				}
			}
			if len(calls) != tt.wantCalls {
				t.Errorf("calls = %d, want %d", len(calls), tt.wantCalls)
			}
		})
	}
}

func TestGenaiIsAssistantRole(t *testing.T) {
	tests := []struct {
		role string
		want bool
	}{
		{"model", true},
		{"assistant", true},
		{"user", false},
		{"system", false},
		{"", false},
		{"Model", false},      // the original compared exactly, no folding
		{"assistant ", false}, // callers trim before calling
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			if got := genaiIsAssistantRole(tt.role); got != tt.want {
				t.Errorf("genaiIsAssistantRole(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Golden wire payloads: one fixture, four providers
// -----------------------------------------------------------------------------

// conversionFixture is the shared input for the golden tests below. It covers
// every content-block kind the converters branch on: a skipped nil content, a
// skipped system turn, a multi-part user text turn containing a nil part, an
// assistant turn mixing text with a function call, the matching function
// response turn, and a trailing assistant text turn.
func conversionFixture() []*genai.Content {
	return []*genai.Content{
		nil,
		{Role: "system", Parts: []*genai.Part{{Text: "skipped"}}},
		{Role: "user", Parts: []*genai.Part{nil, {Text: "first"}, {Text: "second"}}},
		{Role: "model", Parts: []*genai.Part{
			{Text: "let me check"},
			{FunctionCall: &genai.FunctionCall{
				ID:   "call_1",
				Name: "lookup",
				Args: map[string]any{"q": "x"},
			}},
		}},
		{Role: "user", Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{
				ID:       "call_1",
				Response: map[string]any{"result": "ok"},
			}},
		}},
		{Role: "assistant", Parts: []*genai.Part{{Text: "done"}}},
	}
}

func fixtureConfig() *genai.GenerateContentConfig {
	return &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: "sys one"}, {Text: "sys two"}}},
	}
}

// marshalGolden renders v as indented JSON for comparison against a pinned
// payload.
func marshalGolden(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	pretty, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatalf("marshal indent: %v", err)
	}
	return string(pretty)
}

func TestAntContentsToMessages_Golden(t *testing.T) {
	messages, system := antContentsToMessages(conversionFixture(), fixtureConfig())

	if system != "sys one\nsys two" {
		t.Errorf("system = %q, want %q", system, "sys one\nsys two")
	}
	// user text, assistant tool_use, user tool_result, assistant text.
	if len(messages) != 4 {
		t.Fatalf("got %d messages, want 4:\n%s", len(messages), marshalGolden(t, messages))
	}

	roles := []anthropic.MessageParamRole{
		anthropic.MessageParamRoleUser,
		anthropic.MessageParamRoleAssistant,
		anthropic.MessageParamRoleUser,
		anthropic.MessageParamRoleAssistant,
	}
	for i, want := range roles {
		if messages[i].Role != want {
			t.Errorf("messages[%d].Role = %q, want %q", i, messages[i].Role, want)
		}
	}

	got := marshalGolden(t, messages)
	want := `[
  {
    "content": [
      {
        "text": "first\nsecond",
        "type": "text"
      }
    ],
    "role": "user"
  },
  {
    "content": [
      {
        "text": "let me check",
        "type": "text"
      },
      {
        "id": "call_1",
        "input": {
          "q": "x"
        },
        "name": "lookup",
        "type": "tool_use"
      }
    ],
    "role": "assistant"
  },
  {
    "content": [
      {
        "content": [
          {
            "text": "ok",
            "type": "text"
          }
        ],
        "is_error": false,
        "tool_use_id": "call_1",
        "type": "tool_result"
      }
    ],
    "role": "user"
  },
  {
    "content": [
      {
        "text": "done",
        "type": "text"
      }
    ],
    "role": "assistant"
  }
]`
	if got != want {
		t.Errorf("anthropic wire payload mismatch:\ngot:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestOllamaContentsToMessages_Golden(t *testing.T) {
	messages, system := ollamaContentsToMessages(conversionFixture(), fixtureConfig())

	if system != "sys one\nsys two" {
		t.Errorf("system = %q, want %q", system, "sys one\nsys two")
	}
	// user text, assistant with tool_calls, tool result, assistant text.
	if len(messages) != 4 {
		t.Fatalf("got %d messages, want 4:\n%s", len(messages), marshalGolden(t, messages))
	}

	wantRoles := []string{"user", "assistant", "tool", "assistant"}
	for i, want := range wantRoles {
		if messages[i].Role != want {
			t.Errorf("messages[%d].Role = %q, want %q", i, messages[i].Role, want)
		}
	}
	if messages[0].Content != "first\nsecond" {
		t.Errorf("user content = %q, want %q", messages[0].Content, "first\nsecond")
	}
	if messages[1].Content != "let me check" {
		t.Errorf("assistant content = %q, want %q", messages[1].Content, "let me check")
	}
	if len(messages[1].ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(messages[1].ToolCalls))
	}
	tc := messages[1].ToolCalls[0]
	if tc.ID != "call_1" || tc.Function.Name != "lookup" {
		t.Errorf("tool call = {%q %q}, want {call_1 lookup}", tc.ID, tc.Function.Name)
	}
	if got := tc.Function.Arguments.ToMap()["q"]; got != "x" {
		t.Errorf("tool call arg q = %v, want %q", got, "x")
	}
	if messages[2].Content != "ok" || messages[2].ToolCallID != "call_1" {
		t.Errorf("tool result = {%q %q}, want {ok call_1}", messages[2].Content, messages[2].ToolCallID)
	}
	if messages[3].Content != "done" {
		t.Errorf("final assistant content = %q, want %q", messages[3].Content, "done")
	}
}

func TestOaiContentsToMessages_Golden(t *testing.T) {
	messages, system := oaiContentsToMessages(conversionFixture(), fixtureConfig())

	if system != "sys one\nsys two" {
		t.Errorf("system = %q, want %q", system, "sys one\nsys two")
	}
	// user text, assistant with tool_calls, tool message, assistant text.
	if len(messages) != 4 {
		t.Fatalf("got %d messages, want 4:\n%s", len(messages), marshalGolden(t, messages))
	}

	got := marshalGolden(t, messages)
	want := `[
  {
    "content": "first\nsecond",
    "role": "user"
  },
  {
    "content": "let me check",
    "role": "assistant",
    "tool_calls": [
      {
        "function": {
          "arguments": "{\"q\":\"x\"}",
          "name": "lookup"
        },
        "id": "call_1",
        "type": "function"
      }
    ]
  },
  {
    "content": "ok",
    "role": "tool",
    "tool_call_id": "call_1"
  },
  {
    "content": "done",
    "role": "assistant"
  }
]`
	if got != want {
		t.Errorf("openai chat completions wire payload mismatch:\ngot:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestOaiContentsToResponsesInput_Golden(t *testing.T) {
	input, instructions, err := oaiContentsToResponsesInput(conversionFixture(), fixtureConfig())
	if err != nil {
		t.Fatalf("oaiContentsToResponsesInput: %v", err)
	}
	if instructions != "sys one\nsys two" {
		t.Errorf("instructions = %q, want %q", instructions, "sys one\nsys two")
	}

	items := input.OfInputItemList
	// user message, assistant message, function_call, function_call_output,
	// assistant message.
	if len(items) != 5 {
		t.Fatalf("got %d items, want 5:\n%s", len(items), marshalGolden(t, items))
	}

	got := marshalGolden(t, items)
	want := `[
  {
    "content": "first\nsecond",
    "role": "user"
  },
  {
    "content": "let me check",
    "role": "assistant"
  },
  {
    "arguments": "{\"q\":\"x\"}",
    "call_id": "call_1",
    "name": "lookup",
    "type": "function_call"
  },
  {
    "call_id": "call_1",
    "output": "ok",
    "type": "function_call_output"
  },
  {
    "content": "done",
    "role": "assistant"
  }
]`
	if got != want {
		t.Errorf("openai responses wire payload mismatch:\ngot:\n%s\n\nwant:\n%s", got, want)
	}
}

// -----------------------------------------------------------------------------
// Per-provider branch behavior
// -----------------------------------------------------------------------------

func TestContentsToMessages_EmptyConversationGetsPlaceholder(t *testing.T) {
	// Anthropic and Ollama both inject a minimal user message when the
	// conversation yields none, so the endpoint does not reject the request.
	config := fixtureConfig()
	onlySystem := []*genai.Content{
		{Role: "system", Parts: []*genai.Part{{Text: "skipped"}}},
	}

	antMessages, _ := antContentsToMessages(onlySystem, config)
	if len(antMessages) != 1 || antMessages[0].Role != anthropic.MessageParamRoleUser {
		t.Fatalf("anthropic placeholder = %s", marshalGolden(t, antMessages))
	}
	if got := marshalGolden(t, antMessages); got != `[
  {
    "content": [
      {
        "text": "Hello",
        "type": "text"
      }
    ],
    "role": "user"
  }
]` {
		t.Errorf("anthropic placeholder payload:\n%s", got)
	}

	ollamaMessages, _ := ollamaContentsToMessages(onlySystem, config)
	if len(ollamaMessages) != 1 {
		t.Fatalf("got %d ollama messages, want 1", len(ollamaMessages))
	}
	if ollamaMessages[0].Role != "user" || ollamaMessages[0].Content != "Hello" {
		t.Errorf("ollama placeholder = {%q %q}, want {user Hello}",
			ollamaMessages[0].Role, ollamaMessages[0].Content)
	}

	// The OpenAI Chat Completions path does NOT inject a placeholder.
	oaiMessages, _ := oaiContentsToMessages(onlySystem, config)
	if len(oaiMessages) != 0 {
		t.Errorf("openai messages = %d, want 0", len(oaiMessages))
	}
}

func TestContentsToMessages_ToolCallOnUserRoleFallsThroughToText(t *testing.T) {
	// A function call on a non-assistant role does not take the tool branch.
	// With text alongside it, the turn renders as a plain user message.
	contents := []*genai.Content{{
		Role: "user",
		Parts: []*genai.Part{
			{Text: "hi"},
			{FunctionCall: &genai.FunctionCall{ID: "c1", Name: "fn"}},
		},
	}}

	antMessages, _ := antContentsToMessages(contents, nil)
	if len(antMessages) != 1 || antMessages[0].Role != anthropic.MessageParamRoleUser {
		t.Fatalf("anthropic = %s", marshalGolden(t, antMessages))
	}

	ollamaMessages, _ := ollamaContentsToMessages(contents, nil)
	if len(ollamaMessages) != 1 || ollamaMessages[0].Role != "user" || ollamaMessages[0].Content != "hi" {
		t.Fatalf("ollama = %s", marshalGolden(t, ollamaMessages))
	}

	oaiMessages, _ := oaiContentsToMessages(contents, nil)
	if len(oaiMessages) != 1 || oaiMessages[0].OfUser == nil {
		t.Fatalf("openai = %s", marshalGolden(t, oaiMessages))
	}
}

func TestContentsToMessages_ToolCallWithNoTextEmitsNoAssistantText(t *testing.T) {
	contents := []*genai.Content{
		{Role: "model", Parts: []*genai.Part{
			{FunctionCall: &genai.FunctionCall{ID: "c1", Name: "fn", Args: map[string]any{}}},
		}},
		{Role: "user", Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{ID: "c1", Response: map[string]any{"result": "raw text"}}},
		}},
	}

	antMessages, _ := antContentsToMessages(contents, nil)
	if len(antMessages) != 2 {
		t.Fatalf("anthropic messages = %d, want 2", len(antMessages))
	}
	if len(antMessages[0].Content) != 1 {
		t.Errorf("anthropic assistant blocks = %d, want 1 (tool_use only)", len(antMessages[0].Content))
	}

	ollamaMessages, _ := ollamaContentsToMessages(contents, nil)
	if len(ollamaMessages) != 2 {
		t.Fatalf("ollama messages = %d, want 2", len(ollamaMessages))
	}
	if ollamaMessages[0].Content != "" {
		t.Errorf("ollama assistant content = %q, want empty", ollamaMessages[0].Content)
	}
	if ollamaMessages[1].Content != "raw text" {
		t.Errorf("ollama tool content = %q, want %q", ollamaMessages[1].Content, "raw text")
	}
}

func TestAntToolUseMessages_UnmatchedCallGetsPlaceholderResult(t *testing.T) {
	calls := []*genai.FunctionCall{{ID: "missing", Name: "fn", Args: map[string]any{"a": 1}}}
	messages := antToolUseMessages(nil, calls, map[string]*genai.FunctionResponse{})

	if len(messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(messages))
	}
	got := marshalGolden(t, messages[1])
	want := `{
  "content": [
    {
      "content": [
        {
          "text": "No response available for this function call.",
          "type": "text"
        }
      ],
      "is_error": false,
      "tool_use_id": "missing",
      "type": "tool_result"
    }
  ],
  "role": "user"
}`
	if got != want {
		t.Errorf("placeholder tool_result mismatch:\ngot:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestAntToolUseMessages_NilArgsBecomeEmptyObject(t *testing.T) {
	// json.Unmarshal of "null" leaves inputMap nil, which the original
	// replaced with an empty map so the wire payload is `{}` not `null`.
	calls := []*genai.FunctionCall{{ID: "c1", Name: "fn", Args: nil}}
	messages := antToolUseMessages(nil, calls, nil)

	got := marshalGolden(t, messages[0])
	want := `{
  "content": [
    {
      "id": "c1",
      "input": {},
      "name": "fn",
      "type": "tool_use"
    }
  ],
  "role": "assistant"
}`
	if got != want {
		t.Errorf("nil-args tool_use mismatch:\ngot:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestOllamaToolCallMessages_UnmatchedCallGetsEmptyResult(t *testing.T) {
	// Unlike Anthropic, Ollama leaves an unmatched result empty.
	calls := []*genai.FunctionCall{{ID: "missing", Name: "fn"}}
	messages := ollamaToolCallMessages(nil, calls, map[string]*genai.FunctionResponse{})

	if len(messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(messages))
	}
	if messages[1].Role != "tool" || messages[1].Content != "" {
		t.Errorf("tool message = {%q %q}, want {tool \"\"}", messages[1].Role, messages[1].Content)
	}
}

func TestOaiToolCallMessages_SkipsCallsWithoutID(t *testing.T) {
	calls := []*genai.FunctionCall{
		nil,
		{ID: "  ", Name: "blank"},
		{ID: "keep", Name: "fn", Args: map[string]any{"a": 1}},
	}
	messages := oaiToolCallMessages([]string{"text"}, calls, map[string]*genai.FunctionResponse{})

	// assistant + exactly one tool message.
	if len(messages) != 2 {
		t.Fatalf("got %d messages, want 2:\n%s", len(messages), marshalGolden(t, messages))
	}
	if messages[0].OfAssistant == nil {
		t.Fatal("first message is not an assistant message")
	}
	if n := len(messages[0].OfAssistant.ToolCalls); n != 1 {
		t.Errorf("tool calls = %d, want 1", n)
	}
	if messages[0].OfAssistant.Content.OfString.Value != "text" {
		t.Errorf("assistant content = %q, want %q",
			messages[0].OfAssistant.Content.OfString.Value, "text")
	}
}

func TestTextMessageRoleMapping(t *testing.T) {
	tests := []struct {
		role          string
		wantAssistant bool
	}{
		{"model", true},
		{"assistant", true},
		{"user", false},
		{"", false},
		{"tool", false},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			antMsg := antTextMessage(tt.role, "hello")
			wantAnt := anthropic.MessageParamRoleUser
			if tt.wantAssistant {
				wantAnt = anthropic.MessageParamRoleAssistant
			}
			if antMsg.Role != wantAnt {
				t.Errorf("antTextMessage role = %q, want %q", antMsg.Role, wantAnt)
			}
			if len(antMsg.Content) != 1 {
				t.Errorf("antTextMessage blocks = %d, want 1", len(antMsg.Content))
			}

			ollamaMsg := ollamaTextMessage(tt.role, "hello")
			wantOllama := "user"
			if tt.wantAssistant {
				wantOllama = "assistant"
			}
			if ollamaMsg.Role != wantOllama {
				t.Errorf("ollamaTextMessage role = %q, want %q", ollamaMsg.Role, wantOllama)
			}
			if ollamaMsg.Content != "hello" {
				t.Errorf("ollamaTextMessage content = %q, want %q", ollamaMsg.Content, "hello")
			}

			oaiMsg := oaiTextMessage(tt.role, "hello")
			if tt.wantAssistant {
				if oaiMsg.OfAssistant == nil {
					t.Errorf("oaiTextMessage(%q) is not an assistant message", tt.role)
				} else if oaiMsg.OfAssistant.Content.OfString.Value != "hello" {
					t.Errorf("oaiTextMessage content = %q, want %q",
						oaiMsg.OfAssistant.Content.OfString.Value, "hello")
				}
			} else if oaiMsg.OfUser == nil {
				t.Errorf("oaiTextMessage(%q) is not a user message", tt.role)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Responses input assembly
// -----------------------------------------------------------------------------

func TestAddTrimmedID(t *testing.T) {
	set := map[string]struct{}{}
	addTrimmedID(set, "")
	addTrimmedID(set, "   ")
	addTrimmedID(set, "  padded  ")
	addTrimmedID(set, "plain")

	if len(set) != 2 {
		t.Fatalf("set = %v, want 2 entries", set)
	}
	for _, id := range []string{"padded", "plain"} {
		if _, ok := set[id]; !ok {
			t.Errorf("missing %q in %v", id, set)
		}
	}
}

func TestOaiResponsesPairedIDs(t *testing.T) {
	contents := []*genai.Content{
		nil,
		{Parts: []*genai.Part{
			nil,
			{Text: "text only"},
			{FunctionCall: &genai.FunctionCall{ID: " paired "}},
			{FunctionCall: &genai.FunctionCall{ID: ""}},
			{FunctionCall: &genai.FunctionCall{ID: "orphan_call"}},
		}},
		{Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{ID: "paired"}},
			{FunctionResponse: &genai.FunctionResponse{ID: "   "}},
			{FunctionResponse: &genai.FunctionResponse{ID: "orphan_output"}},
		}},
	}

	callIDs, responseIDs := oaiResponsesPairedIDs(contents)

	wantCalls := map[string]bool{"paired": true, "orphan_call": true}
	if len(callIDs) != len(wantCalls) {
		t.Errorf("callIDs = %v, want %v", callIDs, wantCalls)
	}
	for id := range wantCalls {
		if _, ok := callIDs[id]; !ok {
			t.Errorf("callIDs missing %q", id)
		}
	}

	wantResponses := map[string]bool{"paired": true, "orphan_output": true}
	if len(responseIDs) != len(wantResponses) {
		t.Errorf("responseIDs = %v, want %v", responseIDs, wantResponses)
	}
	for id := range wantResponses {
		if _, ok := responseIDs[id]; !ok {
			t.Errorf("responseIDs missing %q", id)
		}
	}
}

func TestOaiContentsToResponsesInput_DropsUnpairedHalves(t *testing.T) {
	// A canceled turn leaves a function call with no output, and an output
	// with no call. Both halves must be dropped, but the surrounding text
	// must survive.
	contents := []*genai.Content{
		{Role: "model", Parts: []*genai.Part{
			{Text: "before"},
			{FunctionCall: &genai.FunctionCall{ID: "orphan_call", Name: "fn"}},
			{Text: "after"},
		}},
		{Role: "user", Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{ID: "orphan_output", Response: map[string]any{"result": "x"}}},
		}},
	}

	input, _, err := oaiContentsToResponsesInput(contents, nil)
	if err != nil {
		t.Fatalf("oaiContentsToResponsesInput: %v", err)
	}

	got := marshalGolden(t, input.OfInputItemList)
	want := `[
  {
    "content": "before",
    "role": "assistant"
  },
  {
    "content": "after",
    "role": "assistant"
  }
]`
	if got != want {
		t.Errorf("unpaired-halves payload mismatch:\ngot:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestOaiAppendResponsesItems_FlushesTextBeforeCalls(t *testing.T) {
	// Text accumulated before a function call must be flushed as its own
	// message so the items keep their original order.
	content := &genai.Content{
		Role: "model",
		Parts: []*genai.Part{
			{Text: "a"},
			{Text: "b"},
			{FunctionCall: &genai.FunctionCall{ID: "c1", Name: "fn", Args: map[string]any{}}},
			{Text: "c"},
		},
	}
	callIDs := map[string]struct{}{"c1": {}}
	responseIDs := map[string]struct{}{"c1": {}}

	items := oaiAppendResponsesItems(responses.ResponseInputParam{}, content, callIDs, responseIDs)

	got := marshalGolden(t, items)
	want := `[
  {
    "content": "a\nb",
    "role": "assistant"
  },
  {
    "arguments": "{}",
    "call_id": "c1",
    "name": "fn",
    "type": "function_call"
  },
  {
    "content": "c",
    "role": "assistant"
  }
]`
	if got != want {
		t.Errorf("flush ordering mismatch:\ngot:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestOaiResponsesReasoning(t *testing.T) {
	budget := func(v int32) *genai.GenerateContentConfig {
		return &genai.GenerateContentConfig{
			ThinkingConfig: &genai.ThinkingConfig{ThinkingBudget: &v},
		}
	}
	tests := []struct {
		name       string
		config     *genai.GenerateContentConfig
		wantOK     bool
		wantEffort shared.ReasoningEffort
	}{
		{name: "nil config", config: nil},
		{name: "nil thinking config", config: &genai.GenerateContentConfig{}},
		{
			name:   "nil budget",
			config: &genai.GenerateContentConfig{ThinkingConfig: &genai.ThinkingConfig{}},
		},
		{name: "zero budget", config: budget(0)},
		{name: "negative budget", config: budget(-1)},
		{name: "low", config: budget(1), wantOK: true, wantEffort: shared.ReasoningEffortLow},
		{name: "low upper bound", config: budget(1999), wantOK: true, wantEffort: shared.ReasoningEffortLow},
		{name: "medium lower bound", config: budget(2000), wantOK: true, wantEffort: shared.ReasoningEffortMedium},
		{name: "medium upper bound", config: budget(7999), wantOK: true, wantEffort: shared.ReasoningEffortMedium},
		{name: "high lower bound", config: budget(8000), wantOK: true, wantEffort: shared.ReasoningEffortHigh},
		{name: "high", config: budget(100000), wantOK: true, wantEffort: shared.ReasoningEffortHigh},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := oaiResponsesReasoning(tt.config)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got.Effort != tt.wantEffort {
				t.Errorf("effort = %q, want %q", got.Effort, tt.wantEffort)
			}
			if !ok && got.Effort != "" {
				t.Errorf("effort = %q, want empty when not configured", got.Effort)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Anthropic streaming event handlers
// -----------------------------------------------------------------------------

// decodeAntEvent decodes a raw SSE event payload into T. The SDK's As*Delta
// accessors read from the raw JSON, so events must be built by unmarshalling
// rather than by setting struct fields.
func decodeAntEvent[T any](t *testing.T, raw string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	return v
}

// collectYield returns a yield func that records every response it is handed
// and reports stop after the given number of calls (0 means never stop).
func collectYield(got *[]*model.LLMResponse, stopAfter int) func(*model.LLMResponse, error) bool {
	return func(r *model.LLMResponse, _ error) bool {
		*got = append(*got, r)
		return stopAfter == 0 || len(*got) < stopAfter
	}
}

func TestAntHandleContentBlockDelta(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantContinue bool
		wantYields   int
		wantRole     string
		wantText     string
		wantAccText  string
		wantToolJSON string
	}{
		{
			name:         "text delta accumulates and yields",
			raw:          `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
			wantContinue: true,
			wantYields:   1,
			wantRole:     string(genai.RoleModel),
			wantText:     "hello",
			wantAccText:  "hello",
		},
		{
			name:         "thinking delta yields but does not accumulate text",
			raw:          `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"pondering"}}`,
			wantContinue: true,
			wantYields:   1,
			wantRole:     "thinking",
			wantText:     "pondering",
			wantAccText:  "",
		},
		{
			name:         "input json delta appends to known tool block",
			raw:          `{"type":"content_block_delta","index":7,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`,
			wantContinue: true,
			wantYields:   0,
			wantToolJSON: `{"a":`,
		},
		{
			name:         "unknown delta type is ignored",
			raw:          `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`,
			wantContinue: true,
			wantYields:   0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &antStreamState{toolUse: map[int]antToolUseAcc{7: {id: "t7", name: "fn"}}}
			var got []*model.LLMResponse

			e := decodeAntEvent[anthropic.ContentBlockDeltaEvent](t, tt.raw)
			cont := antHandleContentBlockDelta(e, state, collectYield(&got, 0))

			if cont != tt.wantContinue {
				t.Errorf("continue = %v, want %v", cont, tt.wantContinue)
			}
			if len(got) != tt.wantYields {
				t.Fatalf("yields = %d, want %d", len(got), tt.wantYields)
			}
			if tt.wantYields > 0 {
				r := got[0]
				if !r.Partial || r.TurnComplete {
					t.Errorf("Partial=%v TurnComplete=%v, want true/false", r.Partial, r.TurnComplete)
				}
				if r.Content.Role != tt.wantRole {
					t.Errorf("role = %q, want %q", r.Content.Role, tt.wantRole)
				}
				if r.Content.Parts[0].Text != tt.wantText {
					t.Errorf("text = %q, want %q", r.Content.Parts[0].Text, tt.wantText)
				}
			}
			if state.text != tt.wantAccText {
				t.Errorf("state.text = %q, want %q", state.text, tt.wantAccText)
			}
			if state.toolUse[7].inputJSON != tt.wantToolJSON {
				t.Errorf("state.toolUse[7].inputJSON = %q, want %q",
					state.toolUse[7].inputJSON, tt.wantToolJSON)
			}
		})
	}
}

func TestAntHandleContentBlockDelta_UnknownToolIndexIgnored(t *testing.T) {
	state := &antStreamState{toolUse: map[int]antToolUseAcc{}}
	var got []*model.LLMResponse

	e := decodeAntEvent[anthropic.ContentBlockDeltaEvent](t,
		`{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"x"}}`)
	if !antHandleContentBlockDelta(e, state, collectYield(&got, 0)) {
		t.Error("continue = false, want true")
	}
	if len(state.toolUse) != 0 {
		t.Errorf("toolUse = %v, want no entry created", state.toolUse)
	}
}

func TestAntHandleContentBlockDelta_StopsWhenConsumerStops(t *testing.T) {
	for _, tt := range []struct{ name, raw string }{
		{"text", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`},
		{"thinking", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hm"}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := &antStreamState{toolUse: map[int]antToolUseAcc{}}
			stop := func(*model.LLMResponse, error) bool { return false }

			e := decodeAntEvent[anthropic.ContentBlockDeltaEvent](t, tt.raw)
			if antHandleContentBlockDelta(e, state, stop) {
				t.Error("continue = true, want false when the consumer stops")
			}
		})
	}
}

func TestAntApplyMessageDelta(t *testing.T) {
	state := &antStreamState{cacheReadTokens: 10, cacheCreationTokens: 20}

	var e anthropic.MessageDeltaEvent
	e.Delta.StopReason = anthropic.StopReasonMaxTokens
	e.Usage.OutputTokens = 42
	e.Usage.CacheReadInputTokens = 5      // lower: must not overwrite
	e.Usage.CacheCreationInputTokens = 30 // higher: must overwrite
	antApplyMessageDelta(e, state)

	if state.stopReason != anthropic.StopReasonMaxTokens {
		t.Errorf("stopReason = %q, want %q", state.stopReason, anthropic.StopReasonMaxTokens)
	}
	if state.outputTokens != 42 {
		t.Errorf("outputTokens = %d, want 42", state.outputTokens)
	}
	if state.cacheReadTokens != 10 {
		t.Errorf("cacheReadTokens = %d, want 10 (lower value must not overwrite)", state.cacheReadTokens)
	}
	if state.cacheCreationTokens != 30 {
		t.Errorf("cacheCreationTokens = %d, want 30", state.cacheCreationTokens)
	}
}

func TestAntFinishStream(t *testing.T) {
	t.Run("success yields final response", func(t *testing.T) {
		state := &antStreamState{
			text:         "answer",
			toolUse:      map[int]antToolUseAcc{},
			stopReason:   anthropic.StopReasonEndTurn,
			inputTokens:  3,
			outputTokens: 4,
		}
		var got []*model.LLMResponse
		antFinishStream(context.Background(), nil, state, collectYield(&got, 0))

		if len(got) != 1 {
			t.Fatalf("yields = %d, want 1", len(got))
		}
		if !got[0].TurnComplete || got[0].Partial {
			t.Errorf("TurnComplete=%v Partial=%v, want true/false", got[0].TurnComplete, got[0].Partial)
		}
		if got[0].Content.Parts[0].Text != "answer" {
			t.Errorf("text = %q, want %q", got[0].Content.Parts[0].Text, "answer")
		}
	})

	t.Run("error yields STREAM_ERROR", func(t *testing.T) {
		state := &antStreamState{toolUse: map[int]antToolUseAcc{}}
		var got []*model.LLMResponse
		antFinishStream(context.Background(), errors.New("boom"), state, collectYield(&got, 0))

		if len(got) != 1 {
			t.Fatalf("yields = %d, want 1", len(got))
		}
		if got[0].ErrorCode != "STREAM_ERROR" || got[0].ErrorMessage != "boom" {
			t.Errorf("got {%q %q}, want {STREAM_ERROR boom}", got[0].ErrorCode, got[0].ErrorMessage)
		}
	})

	t.Run("canceled context yields cancellation marker", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		state := &antStreamState{toolUse: map[int]antToolUseAcc{}}
		var got []*model.LLMResponse
		antFinishStream(ctx, errors.New("boom"), state, collectYield(&got, 0))

		if len(got) != 1 {
			t.Fatalf("yields = %d, want 1", len(got))
		}
		want := canceledResponse()
		if got[0].ErrorCode != want.ErrorCode {
			t.Errorf("ErrorCode = %q, want %q", got[0].ErrorCode, want.ErrorCode)
		}
		if got[0].ErrorCode == "STREAM_ERROR" {
			t.Error("cancellation must not surface as STREAM_ERROR")
		}
	})
}

func TestAntHandleBetaContentBlockStart(t *testing.T) {
	t.Run("tool use records id and name", func(t *testing.T) {
		state := &antStreamState{toolUse: map[int]antToolUseAcc{}}
		var got []*model.LLMResponse

		e := decodeAntEvent[anthropic.BetaRawContentBlockStartEvent](t,
			`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tu_1","name":"fn","input":{}}}`)
		if !antHandleBetaContentBlockStart(e, state, collectYield(&got, 0)) {
			t.Error("continue = false, want true")
		}
		if state.toolUse[2].id != "tu_1" || state.toolUse[2].name != "fn" {
			t.Errorf("toolUse[2] = %+v, want {tu_1 fn}", state.toolUse[2])
		}
		if len(got) != 0 {
			t.Errorf("yields = %d, want 0", len(got))
		}
	})

	t.Run("advisor result yields advisor role", func(t *testing.T) {
		state := &antStreamState{toolUse: map[int]antToolUseAcc{}}
		var got []*model.LLMResponse

		e := decodeAntEvent[anthropic.BetaRawContentBlockStartEvent](t,
			`{"type":"content_block_start","index":0,"content_block":{"type":"advisor_tool_result","tool_use_id":"adv_1","content":{"type":"advisor_result","text":"use option B"}}}`)
		if !antHandleBetaContentBlockStart(e, state, collectYield(&got, 0)) {
			t.Error("continue = false, want true")
		}
		if len(got) != 1 {
			t.Fatalf("yields = %d, want 1", len(got))
		}
		if got[0].Content.Role != "advisor" {
			t.Errorf("role = %q, want %q", got[0].Content.Role, "advisor")
		}
		if got[0].Content.Parts[0].Text != "use option B" {
			t.Errorf("text = %q, want %q", got[0].Content.Parts[0].Text, "use option B")
		}
	})

	t.Run("advisor result stops when consumer stops", func(t *testing.T) {
		state := &antStreamState{toolUse: map[int]antToolUseAcc{}}
		stop := func(*model.LLMResponse, error) bool { return false }

		e := decodeAntEvent[anthropic.BetaRawContentBlockStartEvent](t,
			`{"type":"content_block_start","index":0,"content_block":{"type":"advisor_tool_result","tool_use_id":"adv_1","content":{"type":"advisor_result","text":"advice"}}}`)
		if antHandleBetaContentBlockStart(e, state, stop) {
			t.Error("continue = true, want false")
		}
	})

	t.Run("empty advisor text yields nothing", func(t *testing.T) {
		state := &antStreamState{toolUse: map[int]antToolUseAcc{}}
		var got []*model.LLMResponse

		e := decodeAntEvent[anthropic.BetaRawContentBlockStartEvent](t,
			`{"type":"content_block_start","index":0,"content_block":{"type":"advisor_tool_result","tool_use_id":"adv_1","content":{"type":"advisor_result","text":""}}}`)
		if !antHandleBetaContentBlockStart(e, state, collectYield(&got, 0)) {
			t.Error("continue = false, want true")
		}
		if len(got) != 0 {
			t.Errorf("yields = %d, want 0", len(got))
		}
	})

	t.Run("text block is ignored", func(t *testing.T) {
		state := &antStreamState{toolUse: map[int]antToolUseAcc{}}
		var got []*model.LLMResponse

		e := decodeAntEvent[anthropic.BetaRawContentBlockStartEvent](t,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		if !antHandleBetaContentBlockStart(e, state, collectYield(&got, 0)) {
			t.Error("continue = false, want true")
		}
		if len(state.toolUse) != 0 || len(got) != 0 {
			t.Errorf("toolUse=%v yields=%d, want empty/0", state.toolUse, len(got))
		}
	})
}

func TestAntHandleBetaContentBlockDelta(t *testing.T) {
	t.Run("text delta accumulates and yields", func(t *testing.T) {
		state := &antStreamState{toolUse: map[int]antToolUseAcc{}}
		var got []*model.LLMResponse

		e := decodeAntEvent[anthropic.BetaRawContentBlockDeltaEvent](t,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`)
		if !antHandleBetaContentBlockDelta(e, state, collectYield(&got, 0)) {
			t.Error("continue = false, want true")
		}
		if state.text != "hi" {
			t.Errorf("state.text = %q, want %q", state.text, "hi")
		}
		if len(got) != 1 || got[0].Content.Role != string(genai.RoleModel) {
			t.Fatalf("yields = %d, want 1 with model role", len(got))
		}
	})

	t.Run("thinking delta yields without accumulating", func(t *testing.T) {
		state := &antStreamState{toolUse: map[int]antToolUseAcc{}}
		var got []*model.LLMResponse

		e := decodeAntEvent[anthropic.BetaRawContentBlockDeltaEvent](t,
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hm"}}`)
		if !antHandleBetaContentBlockDelta(e, state, collectYield(&got, 0)) {
			t.Error("continue = false, want true")
		}
		if state.text != "" {
			t.Errorf("state.text = %q, want empty", state.text)
		}
		if len(got) != 1 || got[0].Content.Role != "thinking" {
			t.Fatalf("yields = %d, want 1 with thinking role", len(got))
		}
	})

	t.Run("input json delta appends to known block", func(t *testing.T) {
		state := &antStreamState{toolUse: map[int]antToolUseAcc{4: {id: "t", inputJSON: `{"a"`}}}
		var got []*model.LLMResponse

		e := decodeAntEvent[anthropic.BetaRawContentBlockDeltaEvent](t,
			`{"type":"content_block_delta","index":4,"delta":{"type":"input_json_delta","partial_json":":1}"}}`)
		if !antHandleBetaContentBlockDelta(e, state, collectYield(&got, 0)) {
			t.Error("continue = false, want true")
		}
		if state.toolUse[4].inputJSON != `{"a":1}` {
			t.Errorf("inputJSON = %q, want %q", state.toolUse[4].inputJSON, `{"a":1}`)
		}
	})

	t.Run("stops when consumer stops", func(t *testing.T) {
		state := &antStreamState{toolUse: map[int]antToolUseAcc{}}
		stop := func(*model.LLMResponse, error) bool { return false }

		e := decodeAntEvent[anthropic.BetaRawContentBlockDeltaEvent](t,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`)
		if antHandleBetaContentBlockDelta(e, state, stop) {
			t.Error("continue = true, want false")
		}
	})
}

// -----------------------------------------------------------------------------
// Ollama streaming accumulator
// -----------------------------------------------------------------------------

func TestOllamaStreamState_HandleChunk(t *testing.T) {
	var got []*model.LLMResponse
	state := &ollamaStreamState{yield: collectYield(&got, 0)}

	// Out-of-band reasoning, then inline reasoning, then the answer.
	chunks := []ollamaapi.ChatResponse{
		{Message: ollamaapi.Message{Thinking: "out of band"}},
		{Message: ollamaapi.Message{Content: "<think>inline</think>answer"}},
		{
			Message:    ollamaapi.Message{ToolCalls: []ollamaapi.ToolCall{{ID: "tc1", Function: ollamaapi.ToolCallFunction{Name: "fn"}}}},
			Done:       true,
			DoneReason: "stop",
			Metrics:    ollamaapi.Metrics{PromptEvalCount: 11, EvalCount: 22},
		},
	}
	for i, c := range chunks {
		if err := state.handleChunk(c); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
	}

	if state.aggregatedThinking != "out of bandinline" {
		t.Errorf("thinking = %q, want %q", state.aggregatedThinking, "out of bandinline")
	}
	if state.aggregatedText != "answer" {
		t.Errorf("text = %q, want %q", state.aggregatedText, "answer")
	}
	if len(state.toolCalls) != 1 {
		t.Errorf("toolCalls = %d, want 1", len(state.toolCalls))
	}
	if state.doneReason != "stop" || state.promptTokens != 11 || state.evalTokens != 22 {
		t.Errorf("metrics = {%q %d %d}, want {stop 11 22}",
			state.doneReason, state.promptTokens, state.evalTokens)
	}

	// Three partials: out-of-band thinking, inline thinking, answer text.
	if len(got) != 3 {
		t.Fatalf("yields = %d, want 3", len(got))
	}
	wantRoles := []string{"thinking", "thinking", string(genai.RoleModel)}
	for i, want := range wantRoles {
		if got[i].Content.Role != want {
			t.Errorf("yield %d role = %q, want %q", i, got[i].Content.Role, want)
		}
	}
}

func TestOllamaStreamState_EmitSkipsEmptyText(t *testing.T) {
	var got []*model.LLMResponse
	state := &ollamaStreamState{yield: collectYield(&got, 0)}

	if err := state.emitThinking(""); err != nil {
		t.Fatalf("emitThinking: %v", err)
	}
	if err := state.emitText(""); err != nil {
		t.Fatalf("emitText: %v", err)
	}
	if err := state.emitSplit("", ""); err != nil {
		t.Fatalf("emitSplit: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("yields = %d, want 0 for empty text", len(got))
	}
}

func TestOllamaStreamState_EmitPropagatesCancel(t *testing.T) {
	stop := func(*model.LLMResponse, error) bool { return false }

	t.Run("thinking", func(t *testing.T) {
		state := &ollamaStreamState{yield: stop}
		if err := state.emitThinking("x"); err == nil {
			t.Error("emitThinking err = nil, want cancellation error")
		}
	})
	t.Run("text", func(t *testing.T) {
		state := &ollamaStreamState{yield: stop}
		if err := state.emitText("x"); err == nil {
			t.Error("emitText err = nil, want cancellation error")
		}
	})
	t.Run("split stops on thinking half", func(t *testing.T) {
		state := &ollamaStreamState{yield: stop}
		if err := state.emitSplit("thinking", "text"); err == nil {
			t.Error("emitSplit err = nil, want cancellation error")
		}
		if state.aggregatedText != "" {
			t.Errorf("text = %q, want empty (the text half must not run)", state.aggregatedText)
		}
	})
	t.Run("handleChunk propagates", func(t *testing.T) {
		state := &ollamaStreamState{yield: stop}
		if err := state.handleChunk(ollamaapi.ChatResponse{
			Message: ollamaapi.Message{Thinking: "x"},
		}); err == nil {
			t.Error("handleChunk err = nil, want cancellation error")
		}
	})
	t.Run("handleChunk propagates from content", func(t *testing.T) {
		state := &ollamaStreamState{yield: stop}
		if err := state.handleChunk(ollamaapi.ChatResponse{
			Message: ollamaapi.Message{Content: "plain"},
		}); err == nil {
			t.Error("handleChunk err = nil, want cancellation error")
		}
	})
	t.Run("handleChunk propagates from flush", func(t *testing.T) {
		state := &ollamaStreamState{yield: stop}
		// A trailing partial tag is held back in the splitter's carry, so the
		// final Done chunk flushes it and emits.
		if _, text := state.splitter.split("hello<thi"); text != "hello" {
			t.Fatalf("split text = %q, want %q", text, "hello")
		}
		if err := state.handleChunk(ollamaapi.ChatResponse{Done: true}); err == nil {
			t.Error("handleChunk err = nil, want cancellation error")
		}
	})
}

func TestOllamaStreamState_FinalParts(t *testing.T) {
	tests := []struct {
		name      string
		state     *ollamaStreamState
		wantTexts []string
		wantCalls int
	}{
		{
			name:      "text wins over thinking",
			state:     &ollamaStreamState{aggregatedText: "answer", aggregatedThinking: "reasoning"},
			wantTexts: []string{"answer"},
		},
		{
			name:      "thinking fallback when no text and no tool calls",
			state:     &ollamaStreamState{aggregatedThinking: "reasoning"},
			wantTexts: []string{"reasoning"},
		},
		{
			name: "no thinking fallback when a tool was called",
			state: &ollamaStreamState{
				aggregatedThinking: "reasoning",
				toolCalls:          []ollamaapi.ToolCall{{ID: "tc1"}},
			},
			wantTexts: nil,
			wantCalls: 1,
		},
		{
			name:      "empty state yields no parts",
			state:     &ollamaStreamState{},
			wantTexts: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parts := tt.state.finalParts()
			if len(parts) != len(tt.wantTexts)+tt.wantCalls {
				t.Fatalf("parts = %d, want %d", len(parts), len(tt.wantTexts)+tt.wantCalls)
			}
			for i, want := range tt.wantTexts {
				if parts[i].Text != want {
					t.Errorf("parts[%d].Text = %q, want %q", i, parts[i].Text, want)
				}
			}
		})
	}
}

func TestOllamaStreamState_FinalResponse(t *testing.T) {
	t.Run("usage present", func(t *testing.T) {
		state := &ollamaStreamState{
			aggregatedText: "answer",
			doneReason:     "length",
			promptTokens:   7,
			evalTokens:     9,
		}
		resp := state.finalResponse()

		if resp.Partial || !resp.TurnComplete {
			t.Errorf("Partial=%v TurnComplete=%v, want false/true", resp.Partial, resp.TurnComplete)
		}
		if resp.FinishReason != genai.FinishReasonMaxTokens {
			t.Errorf("FinishReason = %q, want %q", resp.FinishReason, genai.FinishReasonMaxTokens)
		}
		if resp.UsageMetadata == nil {
			t.Fatal("UsageMetadata = nil, want populated")
		}
		if resp.UsageMetadata.PromptTokenCount != 7 || resp.UsageMetadata.CandidatesTokenCount != 9 {
			t.Errorf("usage = {%d %d}, want {7 9}",
				resp.UsageMetadata.PromptTokenCount, resp.UsageMetadata.CandidatesTokenCount)
		}
	})

	t.Run("no usage when both counts are zero", func(t *testing.T) {
		resp := (&ollamaStreamState{aggregatedText: "x"}).finalResponse()
		if resp.UsageMetadata != nil {
			t.Errorf("UsageMetadata = %+v, want nil", resp.UsageMetadata)
		}
		if resp.FinishReason != genai.FinishReasonStop {
			t.Errorf("FinishReason = %q, want %q", resp.FinishReason, genai.FinishReasonStop)
		}
	})
}

// -----------------------------------------------------------------------------
// Gemini client configuration
// -----------------------------------------------------------------------------

func TestGeminiAPIKey(t *testing.T) {
	tests := []struct {
		name   string
		gemini string
		google string
		want   string
	}{
		{name: "neither set", want: ""},
		{name: "gemini only", gemini: "g1", want: "g1"},
		{name: "google only", google: "g2", want: "g2"},
		{name: "gemini wins", gemini: "g1", google: "g2", want: "g1"},
		{name: "empty gemini falls through", gemini: "", google: "g2", want: "g2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GEMINI_API_KEY", tt.gemini)
			t.Setenv("GOOGLE_API_KEY", tt.google)
			if got := geminiAPIKey(); got != tt.want {
				t.Errorf("geminiAPIKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGeminiHTTPOptions(t *testing.T) {
	t.Run("no overrides", func(t *testing.T) {
		opts, overridden := geminiHTTPOptions("", nil)
		if overridden {
			t.Error("overridden = true, want false")
		}
		if opts.BaseURL != "" || opts.Headers != nil {
			t.Errorf("opts = %+v, want zero value", opts)
		}
	})

	t.Run("empty extra headers is not an override", func(t *testing.T) {
		_, overridden := geminiHTTPOptions("", &LLMOptions{ExtraHeaders: map[string]string{}})
		if overridden {
			t.Error("overridden = true, want false for an empty header map")
		}
	})

	t.Run("base url only", func(t *testing.T) {
		opts, overridden := geminiHTTPOptions("https://example.test", nil)
		if !overridden {
			t.Error("overridden = false, want true")
		}
		if opts.BaseURL != "https://example.test" {
			t.Errorf("BaseURL = %q, want %q", opts.BaseURL, "https://example.test")
		}
		if opts.Headers != nil {
			t.Errorf("Headers = %v, want nil", opts.Headers)
		}
	})

	t.Run("headers only", func(t *testing.T) {
		opts, overridden := geminiHTTPOptions("", &LLMOptions{
			ExtraHeaders: map[string]string{"X-Token": "abc"},
		})
		if !overridden {
			t.Error("overridden = false, want true")
		}
		if opts.BaseURL != "" {
			t.Errorf("BaseURL = %q, want empty", opts.BaseURL)
		}
		if got := opts.Headers.Get("X-Token"); got != "abc" {
			t.Errorf("X-Token = %q, want %q", got, "abc")
		}
	})

	t.Run("both", func(t *testing.T) {
		opts, overridden := geminiHTTPOptions("https://example.test", &LLMOptions{
			ExtraHeaders: map[string]string{"X-Token": "abc"},
		})
		if !overridden {
			t.Error("overridden = false, want true")
		}
		if opts.BaseURL != "https://example.test" || opts.Headers.Get("X-Token") != "abc" {
			t.Errorf("opts = %+v, want both set", opts)
		}
		if _, ok := opts.Headers[http.CanonicalHeaderKey("X-Token")]; !ok {
			t.Error("header key was not canonicalized via Header.Set")
		}
	})
}

func TestGeminiNeedsHTTPClient(t *testing.T) {
	tests := []struct {
		name string
		opts *LLMOptions
		want bool
	}{
		{name: "nil opts", opts: nil, want: false},
		{name: "empty opts", opts: &LLMOptions{}, want: false},
		{name: "insecure skip tls", opts: &LLMOptions{InsecureSkipTLS: true}, want: true},
		{name: "ca cert path", opts: &LLMOptions{CACertPath: "/tmp/ca.pem"}, want: true},
		{name: "connect timeout", opts: &LLMOptions{ConnectTimeout: 1}, want: true},
		{name: "negative connect timeout", opts: &LLMOptions{ConnectTimeout: -1}, want: false},
		{name: "extra headers alone do not need a client", opts: &LLMOptions{
			ExtraHeaders: map[string]string{"X": "y"},
		}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := geminiNeedsHTTPClient(tt.opts); got != tt.want {
				t.Errorf("geminiNeedsHTTPClient() = %v, want %v", got, tt.want)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Responses streaming event handlers
// -----------------------------------------------------------------------------

func TestApplyResponsesCompleted(t *testing.T) {
	t.Run("populates state", func(t *testing.T) {
		state := &responsesStreamState{toolCalls: map[int64]toolCallAcc{}}
		resp := &responses.Response{ID: "resp_1", Status: responses.ResponseStatusCompleted}
		resp.Usage.InputTokens = 11
		resp.Usage.OutputTokens = 22
		resp.Usage.InputTokensDetails.CachedTokens = 3

		applyResponsesCompleted(state, resp)

		if state.responseID != "resp_1" {
			t.Errorf("responseID = %q, want %q", state.responseID, "resp_1")
		}
		if state.promptTokens != 11 || state.completionTokens != 22 || state.cachedTokens != 3 {
			t.Errorf("usage = {%d %d %d}, want {11 22 3}",
				state.promptTokens, state.completionTokens, state.cachedTokens)
		}
		if state.finishReason != string(responses.ResponseStatusCompleted) {
			t.Errorf("finishReason = %q, want %q", state.finishReason, responses.ResponseStatusCompleted)
		}
	})

	t.Run("zero values leave prior state intact", func(t *testing.T) {
		state := &responsesStreamState{
			toolCalls:        map[int64]toolCallAcc{},
			responseID:       "old",
			promptTokens:     5,
			completionTokens: 6,
			cachedTokens:     7,
		}
		applyResponsesCompleted(state, &responses.Response{})

		if state.responseID != "old" {
			t.Errorf("responseID = %q, want %q", state.responseID, "old")
		}
		if state.promptTokens != 5 || state.completionTokens != 6 || state.cachedTokens != 7 {
			t.Errorf("usage = {%d %d %d}, want {5 6 7}",
				state.promptTokens, state.completionTokens, state.cachedTokens)
		}
		if state.finishReason != "" {
			t.Errorf("finishReason = %q, want empty", state.finishReason)
		}
	})
}

func TestApplyResponsesToolCallEvent(t *testing.T) {
	t.Run("argument deltas append", func(t *testing.T) {
		state := &responsesStreamState{toolCalls: map[int64]toolCallAcc{}}

		applyResponsesToolCallEvent(state, responses.ResponseStreamEventUnion{
			Type: "response.function_call_arguments.delta", OutputIndex: 0, Delta: `{"a"`,
		})
		applyResponsesToolCallEvent(state, responses.ResponseStreamEventUnion{
			Type: "response.function_call_arguments.delta", OutputIndex: 0, Delta: `:1}`,
		})

		if got := state.toolCalls[0].arguments; got != `{"a":1}` {
			t.Errorf("arguments = %q, want %q", got, `{"a":1}`)
		}
	})

	t.Run("done overwrites accumulated arguments", func(t *testing.T) {
		state := &responsesStreamState{toolCalls: map[int64]toolCallAcc{0: {arguments: "partial"}}}

		applyResponsesToolCallEvent(state, responses.ResponseStreamEventUnion{
			Type: "response.function_call_arguments.done", OutputIndex: 0,
			Name: "fn", Arguments: `{"full":true}`,
		})

		acc := state.toolCalls[0]
		if acc.arguments != `{"full":true}` {
			t.Errorf("arguments = %q, want %q", acc.arguments, `{"full":true}`)
		}
		if acc.name != "fn" {
			t.Errorf("name = %q, want %q", acc.name, "fn")
		}
	})

	t.Run("output item added records header", func(t *testing.T) {
		state := &responsesStreamState{toolCalls: map[int64]toolCallAcc{}}
		evt := decodeAntEvent[responses.ResponseStreamEventUnion](t, `{
			"type": "response.output_item.added",
			"output_index": 2,
			"item": {"type": "function_call", "call_id": "call_9", "name": "lookup", "arguments": "{}"}
		}`)

		applyResponsesToolCallEvent(state, evt)

		acc := state.toolCalls[2]
		if acc.id != "call_9" || acc.name != "lookup" {
			t.Errorf("acc = %+v, want {call_9 lookup}", acc)
		}
		if acc.arguments != "" {
			t.Errorf("arguments = %q, want empty (added must not carry args)", acc.arguments)
		}
	})

	t.Run("output item done carries fallback arguments", func(t *testing.T) {
		state := &responsesStreamState{toolCalls: map[int64]toolCallAcc{}}
		evt := decodeAntEvent[responses.ResponseStreamEventUnion](t, `{
			"type": "response.output_item.done",
			"output_index": 1,
			"item": {"type": "function_call", "call_id": "call_3", "name": "fn", "arguments": "{\"z\":9}"}
		}`)

		applyResponsesToolCallEvent(state, evt)

		acc := state.toolCalls[1]
		if acc.id != "call_3" || acc.name != "fn" || acc.arguments != `{"z":9}` {
			t.Errorf("acc = %+v, want {call_3 fn {\"z\":9}}", acc)
		}
	})

	t.Run("unrelated events are ignored", func(t *testing.T) {
		state := &responsesStreamState{toolCalls: map[int64]toolCallAcc{}}
		for _, typ := range []string{
			"response.output_text.delta",
			"response.reasoning_text.delta",
			"response.completed",
			"error",
		} {
			applyResponsesToolCallEvent(state, responses.ResponseStreamEventUnion{
				Type: typ, OutputIndex: 0, Delta: "ignored",
			})
		}
		if len(state.toolCalls) != 0 {
			t.Errorf("toolCalls = %v, want empty", state.toolCalls)
		}
	})
}

func TestFinishResponsesStream(t *testing.T) {
	t.Run("stores response id and yields final", func(t *testing.T) {
		m := &openaiModel{}
		state := &responsesStreamState{
			toolCalls:        map[int64]toolCallAcc{},
			text:             "answer",
			finishReason:     "completed",
			promptTokens:     4,
			completionTokens: 5,
			cachedTokens:     1,
		}
		var got []*model.LLMResponse

		m.finishResponsesStream(state, &responses.Response{ID: "resp_x"}, collectYield(&got, 0))

		if len(got) != 1 {
			t.Fatalf("yields = %d, want 1", len(got))
		}
		if !got[0].TurnComplete || got[0].Partial {
			t.Errorf("TurnComplete=%v Partial=%v, want true/false", got[0].TurnComplete, got[0].Partial)
		}
		if got[0].Content.Parts[0].Text != "answer" {
			t.Errorf("text = %q, want %q", got[0].Content.Parts[0].Text, "answer")
		}
		if got[0].UsageMetadata == nil || got[0].UsageMetadata.CachedContentTokenCount != 1 {
			t.Errorf("usage = %+v, want cached=1", got[0].UsageMetadata)
		}
		if m.responseState == nil || m.responseState.previousResponseID != "resp_x" {
			t.Errorf("responseState = %+v, want previousResponseID resp_x", m.responseState)
		}
	})

	t.Run("nil final response leaves state untouched", func(t *testing.T) {
		m := &openaiModel{}
		state := &responsesStreamState{toolCalls: map[int64]toolCallAcc{}}
		var got []*model.LLMResponse

		m.finishResponsesStream(state, nil, collectYield(&got, 0))

		if len(got) != 1 {
			t.Fatalf("yields = %d, want 1", len(got))
		}
		if got[0].UsageMetadata != nil {
			t.Errorf("UsageMetadata = %+v, want nil when no tokens were reported", got[0].UsageMetadata)
		}
		if m.responseState != nil {
			t.Errorf("responseState = %+v, want nil", m.responseState)
		}
	})

	t.Run("blank response id is not stored", func(t *testing.T) {
		m := &openaiModel{}
		state := &responsesStreamState{toolCalls: map[int64]toolCallAcc{}}
		var got []*model.LLMResponse

		m.finishResponsesStream(state, &responses.Response{ID: ""}, collectYield(&got, 0))

		if m.responseState != nil {
			t.Errorf("responseState = %+v, want nil", m.responseState)
		}
	})
}

func TestBuildResponsesParams(t *testing.T) {
	budget := int32(9000)
	req := &model.LLMRequest{
		Contents: conversionFixture(),
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: "sys"}}},
			Tools: []*genai.Tool{{
				FunctionDeclarations: []*genai.FunctionDeclaration{{Name: "fn", Description: "d"}},
			}},
			ThinkingConfig: &genai.ThinkingConfig{ThinkingBudget: &budget},
		},
	}

	t.Run("platform backend", func(t *testing.T) {
		m := &openaiModel{}
		params, sentPrev, err := m.buildResponsesParams(req, "gpt-5.1")
		if err != nil {
			t.Fatalf("buildResponsesParams: %v", err)
		}
		if params.Model != "gpt-5.1" {
			t.Errorf("Model = %q, want %q", params.Model, "gpt-5.1")
		}
		if params.Store.Value {
			t.Error("Store = true, want false (responses are never persisted by default)")
		}
		if params.Instructions.Value != "sys" {
			t.Errorf("Instructions = %q, want %q", params.Instructions.Value, "sys")
		}
		if len(params.Tools) != 1 {
			t.Errorf("Tools = %d, want 1", len(params.Tools))
		}
		if params.Reasoning.Effort != shared.ReasoningEffortHigh {
			t.Errorf("Reasoning.Effort = %q, want %q", params.Reasoning.Effort, shared.ReasoningEffortHigh)
		}
		if len(params.Include) != 0 {
			t.Errorf("Include = %v, want empty for the platform backend", params.Include)
		}
		// store=false, so the retained pointer is never threaded through.
		if sentPrev {
			t.Error("sentPreviousResponseID = true, want false when store is false")
		}
		if params.PreviousResponseID.Valid() {
			t.Error("PreviousResponseID set, want unset")
		}
	})

	t.Run("codex backend opts into encrypted reasoning", func(t *testing.T) {
		m := &openaiModel{codexBackend: true}
		params, _, err := m.buildResponsesParams(req, "gpt-5.1-codex")
		if err != nil {
			t.Fatalf("buildResponsesParams: %v", err)
		}
		if len(params.Include) != 1 ||
			params.Include[0] != responses.ResponseIncludableReasoningEncryptedContent {
			t.Errorf("Include = %v, want reasoning.encrypted_content", params.Include)
		}
	})

	t.Run("no instructions, tools or reasoning", func(t *testing.T) {
		m := &openaiModel{}
		bare := &model.LLMRequest{Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "hi"}}},
		}}
		params, _, err := m.buildResponsesParams(bare, "gpt-5.1")
		if err != nil {
			t.Fatalf("buildResponsesParams: %v", err)
		}
		if params.Instructions.Valid() {
			t.Error("Instructions set, want unset")
		}
		if len(params.Tools) != 0 {
			t.Errorf("Tools = %d, want 0", len(params.Tools))
		}
		if params.Reasoning.Effort != "" {
			t.Errorf("Reasoning.Effort = %q, want empty", params.Reasoning.Effort)
		}
	})
}
