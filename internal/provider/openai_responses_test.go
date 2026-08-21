package provider

import (
	"testing"

	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"google.golang.org/genai"
)

func TestOaiResponsesPairedIDs(t *testing.T) {
	t.Parallel()

	contents := []*genai.Content{
		nil,
		{Parts: []*genai.Part{nil}},
		{Parts: []*genai.Part{
			{FunctionCall: &genai.FunctionCall{ID: "paired"}},
			{FunctionCall: &genai.FunctionCall{ID: "  "}}, // blank: ignored
			{FunctionCall: &genai.FunctionCall{ID: "orphan-call"}},
		}},
		{Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{ID: "paired"}},
			{FunctionResponse: &genai.FunctionResponse{ID: ""}}, // blank: ignored
			{FunctionResponse: &genai.FunctionResponse{ID: "orphan-output"}},
		}},
	}

	callIDs, responseIDs := oaiResponsesPairedIDs(contents)

	wantCalls := []string{"paired", "orphan-call"}
	if len(callIDs) != len(wantCalls) {
		t.Errorf("callIDs = %v, want exactly %v", callIDs, wantCalls)
	}
	for _, id := range wantCalls {
		if _, ok := callIDs[id]; !ok {
			t.Errorf("callIDs is missing %q", id)
		}
	}

	wantResponses := []string{"paired", "orphan-output"}
	if len(responseIDs) != len(wantResponses) {
		t.Errorf("responseIDs = %v, want exactly %v", responseIDs, wantResponses)
	}
	for _, id := range wantResponses {
		if _, ok := responseIDs[id]; !ok {
			t.Errorf("responseIDs is missing %q", id)
		}
	}
}

func TestOaiAppendResponsesItems(t *testing.T) {
	t.Parallel()

	paired := map[string]struct{}{"paired": {}}

	tests := []struct {
		name        string
		content     *genai.Content
		callIDs     map[string]struct{}
		responseIDs map[string]struct{}
		wantItems   int
	}{
		{
			name:      "consecutive text parts coalesce into one message",
			content:   &genai.Content{Role: "user", Parts: []*genai.Part{{Text: "a"}, {Text: "b"}}},
			wantItems: 1,
		},
		{
			name:      "nil parts are skipped",
			content:   &genai.Content{Role: "user", Parts: []*genai.Part{nil, {Text: "a"}, nil}},
			wantItems: 1,
		},
		{
			name:      "content with no parts emits nothing",
			content:   &genai.Content{Role: "user"},
			wantItems: 0,
		},
		{
			name: "a call flushes pending text, so text precedes the call",
			content: &genai.Content{Role: "model", Parts: []*genai.Part{
				{Text: "before"},
				{FunctionCall: &genai.FunctionCall{ID: "paired", Name: "f"}},
				{Text: "after"},
			}},
			responseIDs: paired,
			wantItems:   3, // text, call, text
		},
		{
			name: "an unpaired call is dropped but its text still flushes",
			content: &genai.Content{Role: "model", Parts: []*genai.Part{
				{Text: "before"},
				{FunctionCall: &genai.FunctionCall{ID: "orphan", Name: "f"}},
			}},
			responseIDs: paired,
			wantItems:   1, // just the text
		},
		{
			name: "a call with a blank ID is dropped",
			content: &genai.Content{Role: "model", Parts: []*genai.Part{
				{FunctionCall: &genai.FunctionCall{ID: "  ", Name: "f"}},
			}},
			responseIDs: paired,
			wantItems:   0,
		},
		{
			name: "a paired output is emitted",
			content: &genai.Content{Role: "user", Parts: []*genai.Part{
				{FunctionResponse: &genai.FunctionResponse{ID: "paired"}},
			}},
			callIDs:   paired,
			wantItems: 1,
		},
		{
			name: "an unpaired output is dropped",
			content: &genai.Content{Role: "user", Parts: []*genai.Part{
				{FunctionResponse: &genai.FunctionResponse{ID: "orphan"}},
			}},
			callIDs:   paired,
			wantItems: 0,
		},
		{
			name: "an output with a blank ID is dropped",
			content: &genai.Content{Role: "user", Parts: []*genai.Part{
				{FunctionResponse: &genai.FunctionResponse{ID: ""}},
			}},
			callIDs:   paired,
			wantItems: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := oaiAppendResponsesItems(nil, tt.content, tt.callIDs, tt.responseIDs)
			if len(got) != tt.wantItems {
				t.Errorf("oaiAppendResponsesItems() produced %d items, want %d", len(got), tt.wantItems)
			}
		})
	}
}

func TestOaiAppendResponsesItemsGrowsExistingList(t *testing.T) {
	t.Parallel()

	seed := oaiAppendResponsesItems(nil, &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{{Text: "first"}},
	}, nil, nil)

	got := oaiAppendResponsesItems(seed, &genai.Content{
		Role:  "model",
		Parts: []*genai.Part{{Text: "second"}},
	}, nil, nil)

	if len(got) != 2 {
		t.Fatalf("second call produced %d items, want the seed item plus one", len(got))
	}
}

func TestApplyResponsesCompleted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		initial responsesStreamState
		resp    responses.Response
		want    responsesStreamState
	}{
		{
			name: "usage and status are recorded",
			resp: responses.Response{
				Status: "completed",
				Usage: responses.ResponseUsage{
					InputTokens:        10,
					OutputTokens:       20,
					InputTokensDetails: responses.ResponseUsageInputTokensDetails{CachedTokens: 5},
				},
			},
			want: responsesStreamState{
				promptTokens: 10, completionTokens: 20, cachedTokens: 5, finishReason: "completed",
			},
		},
		{
			// A backend that omits counts in the final event must not wipe
			// counts already accumulated.
			name:    "zero counts do not clobber earlier values",
			initial: responsesStreamState{promptTokens: 7, completionTokens: 8, cachedTokens: 9},
			resp:    responses.Response{Status: "incomplete"},
			want: responsesStreamState{
				promptTokens: 7, completionTokens: 8, cachedTokens: 9, finishReason: "incomplete",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.initial
			applyResponsesCompleted(&got, &tt.resp)
			if got.promptTokens != tt.want.promptTokens ||
				got.completionTokens != tt.want.completionTokens ||
				got.cachedTokens != tt.want.cachedTokens ||
				got.finishReason != tt.want.finishReason {
				t.Errorf("applyResponsesCompleted() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResponsesUsageMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		state   responsesStreamState
		wantNil bool
	}{
		{name: "no usage reported", state: responsesStreamState{}, wantNil: true},
		{
			// Cached tokens alone are not usage: the original guard only ever
			// looked at prompt and completion counts.
			name:    "cached tokens alone are not usage",
			state:   responsesStreamState{cachedTokens: 5},
			wantNil: true,
		},
		{name: "prompt tokens only", state: responsesStreamState{promptTokens: 3}},
		{name: "completion tokens only", state: responsesStreamState{completionTokens: 4}},
		{name: "both", state: responsesStreamState{promptTokens: 3, completionTokens: 4, cachedTokens: 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := responsesUsageMetadata(&tt.state)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("responsesUsageMetadata() = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("responsesUsageMetadata() = nil, want usage metadata")
			}
			if int64(got.PromptTokenCount) != tt.state.promptTokens {
				t.Errorf("PromptTokenCount = %d, want %d", got.PromptTokenCount, tt.state.promptTokens)
			}
			if int64(got.CandidatesTokenCount) != tt.state.completionTokens {
				t.Errorf("CandidatesTokenCount = %d, want %d", got.CandidatesTokenCount, tt.state.completionTokens)
			}
			if int64(got.CachedContentTokenCount) != tt.state.cachedTokens {
				t.Errorf("CachedContentTokenCount = %d, want %d", got.CachedContentTokenCount, tt.state.cachedTokens)
			}
		})
	}
}

func TestResponsesPartialResponse(t *testing.T) {
	t.Parallel()

	got := responsesPartialResponse("thinking", "tok")
	if !got.Partial || got.TurnComplete {
		t.Errorf("Partial=%v TurnComplete=%v, want true/false", got.Partial, got.TurnComplete)
	}
	if got.Content.Role != "thinking" {
		t.Errorf("role = %q, want %q", got.Content.Role, "thinking")
	}
	if len(got.Content.Parts) != 1 || got.Content.Parts[0].Text != "tok" {
		t.Errorf("parts = %v, want a single %q part", got.Content.Parts, "tok")
	}
}

func TestApplyResponsesToolCallEvent(t *testing.T) {
	t.Parallel()

	newState := func() *responsesStreamState {
		return &responsesStreamState{toolCalls: make(map[int64]toolCallAcc)}
	}

	t.Run("argument deltas accumulate", func(t *testing.T) {
		t.Parallel()
		s := newState()
		for _, delta := range []string{`{"a`, `":1`, `}`} {
			applyResponsesToolCallEvent(s, &responses.ResponseStreamEventUnion{
				Type:        "response.function_call_arguments.delta",
				OutputIndex: 0,
				Delta:       delta,
			})
		}
		if got := s.toolCalls[0].arguments; got != `{"a":1}` {
			t.Errorf("arguments = %q, want the concatenated deltas", got)
		}
	})

	t.Run("arguments done overwrites accumulated deltas", func(t *testing.T) {
		t.Parallel()
		s := newState()
		applyResponsesToolCallEvent(s, &responses.ResponseStreamEventUnion{
			Type: "response.function_call_arguments.delta", Delta: "part",
		})
		applyResponsesToolCallEvent(s, &responses.ResponseStreamEventUnion{
			Type: "response.function_call_arguments.done", Name: "f", Arguments: `{"whole":true}`,
		})
		if got := s.toolCalls[0].arguments; got != `{"whole":true}` {
			t.Errorf("arguments = %q, want the authoritative complete payload", got)
		}
		if got := s.toolCalls[0].name; got != "f" {
			t.Errorf("name = %q, want %q", got, "f")
		}
	})

	t.Run("output_item.added does not clobber accumulated arguments", func(t *testing.T) {
		t.Parallel()
		s := newState()
		applyResponsesToolCallEvent(s, &responses.ResponseStreamEventUnion{
			Type: "response.function_call_arguments.delta", Delta: `{"kept":1}`,
		})
		applyResponsesToolCallEvent(s, &responses.ResponseStreamEventUnion{
			Type: "response.output_item.added",
		})
		if got := s.toolCalls[0].arguments; got != `{"kept":1}` {
			t.Errorf("arguments = %q, want the deltas left untouched by the header event", got)
		}
	})

	t.Run("unknown event types are ignored", func(t *testing.T) {
		t.Parallel()
		s := newState()
		applyResponsesToolCallEvent(s, &responses.ResponseStreamEventUnion{Type: "response.created"})
		applyResponsesToolCallEvent(s, &responses.ResponseStreamEventUnion{Type: "response.output_text.done"})
		if len(s.toolCalls) != 0 {
			t.Errorf("toolCalls = %v, want no accumulator entries", s.toolCalls)
		}
	})
}

func TestResponsesReasoningEffort(t *testing.T) {
	t.Parallel()

	budget := func(n int32) *genai.GenerateContentConfig {
		return &genai.GenerateContentConfig{
			ThinkingConfig: &genai.ThinkingConfig{ThinkingBudget: &n},
		}
	}

	tests := []struct {
		name      string
		config    *genai.GenerateContentConfig
		want      shared.ReasoningEffort
		wantFound bool
	}{
		{name: "nil config", config: nil},
		{name: "no thinking config", config: &genai.GenerateContentConfig{}},
		{
			name:   "thinking config without a budget",
			config: &genai.GenerateContentConfig{ThinkingConfig: &genai.ThinkingConfig{}},
		},
		{name: "zero budget carries no effort", config: budget(0)},
		{name: "negative budget carries no effort", config: budget(-1)},
		{name: "just above zero is low", config: budget(1), want: shared.ReasoningEffortLow, wantFound: true},
		{name: "upper edge of low", config: budget(1999), want: shared.ReasoningEffortLow, wantFound: true},
		{name: "lower edge of medium", config: budget(2000), want: shared.ReasoningEffortMedium, wantFound: true},
		{name: "upper edge of medium", config: budget(7999), want: shared.ReasoningEffortMedium, wantFound: true},
		{name: "lower edge of high", config: budget(8000), want: shared.ReasoningEffortHigh, wantFound: true},
		{name: "well above high", config: budget(100000), want: shared.ReasoningEffortHigh, wantFound: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, found := responsesReasoningEffort(tt.config)
			if found != tt.wantFound {
				t.Fatalf("found = %v, want %v", found, tt.wantFound)
			}
			if got != tt.want {
				t.Errorf("effort = %q, want %q", got, tt.want)
			}
		})
	}
}
