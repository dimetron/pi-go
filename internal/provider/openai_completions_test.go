package provider

import (
	"testing"

	"google.golang.org/genai"
)

func TestOaiTextMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		role          string
		wantAssistant bool
	}{
		{name: "model maps to assistant", role: "model", wantAssistant: true},
		{name: "assistant stays assistant", role: "assistant", wantAssistant: true},
		{name: "user maps to user", role: "user"},
		{name: "unknown role maps to user", role: "narrator"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := oaiTextMessage(tt.role, "hello")
			if tt.wantAssistant {
				if got.OfAssistant == nil {
					t.Fatalf("oaiTextMessage(%q) produced no assistant message", tt.role)
				}
				if text := got.OfAssistant.Content.OfString.Value; text != "hello" {
					t.Errorf("assistant content = %q, want %q", text, "hello")
				}
				return
			}
			if got.OfAssistant != nil {
				t.Fatalf("oaiTextMessage(%q) produced an assistant message, want user", tt.role)
			}
			if got.OfUser == nil {
				t.Fatalf("oaiTextMessage(%q) produced no user message", tt.role)
			}
		})
	}
}

func TestOaiToolCallMessagesSkipsCallsWithoutID(t *testing.T) {
	t.Parallel()

	calls := []*genai.FunctionCall{
		nil,
		{ID: "   ", Name: "blank"},
		{ID: "keep", Name: "kept"},
	}

	got := oaiToolCallMessages(nil, calls, nil)

	// One assistant message plus one tool message for the single usable call.
	if len(got) != 2 {
		t.Fatalf("oaiToolCallMessages() returned %d messages, want 2", len(got))
	}
	asst := got[0].OfAssistant
	if asst == nil {
		t.Fatal("first message is not an assistant message")
	}
	if len(asst.ToolCalls) != 1 {
		t.Fatalf("assistant carries %d tool calls, want 1", len(asst.ToolCalls))
	}
	if id := asst.ToolCalls[0].OfFunction.ID; id != "keep" {
		t.Errorf("tool call ID = %q, want %q", id, "keep")
	}
	if asst.Content.OfString.Valid() {
		t.Errorf("assistant content = %q, want it unset when there is no text",
			asst.Content.OfString.Value)
	}
	if got[1].OfTool == nil {
		t.Fatal("second message is not a tool message")
	}
}

func TestOaiToolCallMessagesMissingResponsePlaceholder(t *testing.T) {
	t.Parallel()

	calls := []*genai.FunctionCall{{ID: "unanswered", Name: "f"}}
	got := oaiToolCallMessages([]string{"thinking out loud"}, calls, nil)

	if len(got) != 2 {
		t.Fatalf("oaiToolCallMessages() returned %d messages, want 2", len(got))
	}
	if text := got[0].OfAssistant.Content.OfString.Value; text != "thinking out loud" {
		t.Errorf("assistant content = %q, want the joined text parts", text)
	}
	want := "No response available for this function call."
	if got := got[1].OfTool.Content.OfString.Value; got != want {
		t.Errorf("tool content = %q, want the placeholder %q", got, want)
	}
}
