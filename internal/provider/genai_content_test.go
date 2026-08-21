package provider

import (
	"slices"
	"testing"

	"google.golang.org/genai"
)

func TestGenaiSystemInstruction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config *genai.GenerateContentConfig
		want   string
	}{
		{
			name:   "nil config",
			config: nil,
			want:   "",
		},
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
			name: "nil and empty parts are skipped",
			config: &genai.GenerateContentConfig{SystemInstruction: &genai.Content{
				Parts: []*genai.Part{nil, {Text: ""}, {Text: "be brief"}},
			}},
			want: "be brief",
		},
		{
			// Trimming applies to the joined result, so leading whitespace on
			// the first part goes too — only interior spacing survives.
			name: "parts joined by newline, result trimmed at both ends",
			config: &genai.GenerateContentConfig{SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: "  first"}, {Text: "  second  "}},
			}},
			want: "first\n  second",
		},
		{
			name: "whitespace-only text collapses to empty",
			config: &genai.GenerateContentConfig{SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: "   "}},
			}},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := genaiSystemInstruction(tt.config); got != tt.want {
				t.Errorf("genaiSystemInstruction() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenaiFunctionResponses(t *testing.T) {
	t.Parallel()

	first := &genai.FunctionResponse{ID: "dup", Name: "first"}
	last := &genai.FunctionResponse{ID: "dup", Name: "last"}

	contents := []*genai.Content{
		nil,
		{Parts: nil},
		{Parts: []*genai.Part{nil, {Text: "no response here"}}},
		{Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{ID: "a", Name: "alpha"}}}},
		{Parts: []*genai.Part{{FunctionResponse: first}, {FunctionResponse: last}}},
	}

	got := genaiFunctionResponses(contents)
	if len(got) != 2 {
		t.Fatalf("genaiFunctionResponses() has %d entries, want 2: %v", len(got), got)
	}
	if got["a"] == nil || got["a"].Name != "alpha" {
		t.Errorf("entry %q = %v, want the alpha response", "a", got["a"])
	}
	if got["dup"] != last {
		t.Errorf("entry %q = %v, want the last response for a repeated ID", "dup", got["dup"])
	}
}

func TestGenaiFunctionResponsesEmpty(t *testing.T) {
	t.Parallel()

	got := genaiFunctionResponses(nil)
	if got == nil {
		t.Fatal("genaiFunctionResponses(nil) = nil, want an empty non-nil map")
	}
	if len(got) != 0 {
		t.Errorf("genaiFunctionResponses(nil) has %d entries, want 0", len(got))
	}
}

func TestGenaiSplitParts(t *testing.T) {
	t.Parallel()

	callA := &genai.FunctionCall{ID: "a", Name: "alpha"}
	callB := &genai.FunctionCall{ID: "b", Name: "beta"}

	tests := []struct {
		name      string
		content   *genai.Content
		wantText  []string
		wantCalls []*genai.FunctionCall
	}{
		{
			name:    "no parts",
			content: &genai.Content{},
		},
		{
			name:     "nil parts skipped",
			content:  &genai.Content{Parts: []*genai.Part{nil, {Text: "hi"}, nil}},
			wantText: []string{"hi"},
		},
		{
			name: "text and calls separated in order",
			content: &genai.Content{Parts: []*genai.Part{
				{Text: "one"},
				{FunctionCall: callA},
				{Text: "two"},
				{FunctionCall: callB},
			}},
			wantText:  []string{"one", "two"},
			wantCalls: []*genai.FunctionCall{callA, callB},
		},
		{
			// Text wins when a part carries both — the precedence every
			// provider converter has always used.
			name: "text takes precedence over a call on the same part",
			content: &genai.Content{Parts: []*genai.Part{
				{Text: "inline", FunctionCall: callA},
			}},
			wantText: []string{"inline"},
		},
		{
			name: "function response parts are ignored",
			content: &genai.Content{Parts: []*genai.Part{
				{FunctionResponse: &genai.FunctionResponse{ID: "a"}},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotText, gotCalls := genaiSplitParts(tt.content)
			if !slices.Equal(gotText, tt.wantText) {
				t.Errorf("text = %q, want %q", gotText, tt.wantText)
			}
			if !slices.Equal(gotCalls, tt.wantCalls) {
				t.Errorf("calls = %v, want %v", gotCalls, tt.wantCalls)
			}
		})
	}
}

func TestGenaiRoleIsAssistant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		role string
		want bool
	}{
		{"model", true},
		{"assistant", true},
		{"user", false},
		{"tool", false},
		{"", false},
		{"Model", false},      // compared verbatim, no case folding
		{" assistant", false}, // callers trim before calling
	}

	for _, tt := range tests {
		t.Run("role="+tt.role, func(t *testing.T) {
			t.Parallel()
			if got := genaiRoleIsAssistant(tt.role); got != tt.want {
				t.Errorf("genaiRoleIsAssistant(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}
