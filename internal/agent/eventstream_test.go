package agent

import (
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// textEvent builds the shape ADK yields for a turn of assistant text.
func textEvent(role, text string, partial bool) *session.Event {
	return &session.Event{LLMResponse: model.LLMResponse{
		Content: genai.NewContentFromText(text, genai.Role(role)),
		Partial: partial,
	}}
}

// feed runs a sequence of events through a StreamDedup the way every consumer
// does, and returns the text that survived.
func feed(d *StreamDedup, events []*session.Event) []string {
	var out []string
	for _, ev := range events {
		d.BeginEvent(ev)
		for _, part := range ev.Content.Parts {
			if part.Text == "" {
				continue
			}
			if d.SkipText(ev) {
				continue
			}
			out = append(out, part.Text)
		}
	}
	return out
}

func TestStreamDedup(t *testing.T) {
	tests := []struct {
		name   string
		events []*session.Event
		want   []string
	}{
		{
			// The bug this exists for: minimax-m3:cloud answering "alpha bravo"
			// arrived once as a delta and once as the aggregate, and print mode
			// showed "alpha bravoalpha bravo".
			name: "aggregate after deltas is dropped",
			events: []*session.Event{
				textEvent("model", "alpha ", true),
				textEvent("model", "bravo", true),
				textEvent("model", "alpha bravo", false),
			},
			want: []string{"alpha ", "bravo"},
		},
		{
			// Non-streaming providers send only the aggregate. Dropping it would
			// blank the reply entirely — strictly worse than the double-print.
			name: "bare aggregate with no deltas passes through",
			events: []*session.Event{
				textEvent("model", "alpha bravo", false),
			},
			want: []string{"alpha bravo"},
		},
		{
			// After a tool round-trip the next turn may arrive as a bare
			// aggregate. The user/tool event must clear the streamed flag or
			// that turn is silently swallowed.
			name: "tool round-trip resets between turns",
			events: []*session.Event{
				textEvent("model", "checking", true),
				textEvent("model", "checking", false),
				textEvent("user", "tool result", false),
				textEvent("model", "done", false),
			},
			want: []string{"checking", "tool result", "done"},
		},
		{
			name: "consecutive streamed turns each drop their aggregate",
			events: []*session.Event{
				textEvent("model", "one", true),
				textEvent("model", "one", false),
				textEvent("user", "tool result", false),
				textEvent("model", "two", true),
				textEvent("model", "two", false),
			},
			want: []string{"one", "tool result", "two"},
		},
		{
			// Thinking text is model-authored, so it must not reset the guard
			// for the answer that follows it.
			name: "thinking role does not reset the guard",
			events: []*session.Event{
				textEvent("model", "hi", true),
				textEvent("thinking", "reasoning", true),
				textEvent("model", "hi", false),
			},
			want: []string{"hi", "reasoning"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d StreamDedup
			got := feed(&d, tt.events)
			if len(got) != len(tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %q, want %q", got, tt.want)
				}
			}
		})
	}
}

func TestStreamDedup_NilSafe(t *testing.T) {
	var d StreamDedup
	d.BeginEvent(nil)
	d.BeginEvent(&session.Event{})
	if d.SkipText(nil) {
		t.Error("SkipText(nil) = true, want false")
	}
}

func TestEventError(t *testing.T) {
	tests := []struct {
		name string
		ev   *session.Event
		want string // "" means no error
	}{
		{
			name: "nil event",
			ev:   nil,
		},
		{
			name: "ordinary content event",
			ev: &session.Event{LLMResponse: model.LLMResponse{
				Content: genai.NewContentFromText("hi", genai.RoleModel),
			}},
		},
		{
			// The gpt-5.6-terra 400 from 2026-07-28: providers wrap the HTTP
			// failure as STREAM_ERROR, so the code adds nothing the message
			// doesn't already say.
			name: "stream error drops the redundant code prefix",
			ev: &session.Event{LLMResponse: model.LLMResponse{
				ErrorCode:    "STREAM_ERROR",
				ErrorMessage: `POST "https://api.openai.com/v1/chat/completions": 400 Bad Request`,
			}},
			want: `POST "https://api.openai.com/v1/chat/completions": 400 Bad Request`,
		},
		{
			name: "named code is kept",
			ev: &session.Event{LLMResponse: model.LLMResponse{
				ErrorCode:    "DAILY_LIMIT_EXCEEDED",
				ErrorMessage: "spent $10.00 of $5.00 budget",
			}},
			want: "DAILY_LIMIT_EXCEEDED: spent $10.00 of $5.00 budget",
		},
		{
			name: "code with no message still surfaces",
			ev: &session.Event{LLMResponse: model.LLMResponse{
				ErrorCode: "API_ERROR",
			}},
			want: "API_ERROR",
		},
		{
			name: "blank message is treated as absent",
			ev: &session.Event{LLMResponse: model.LLMResponse{
				ErrorCode:    "API_ERROR",
				ErrorMessage: "   \n",
			}},
			want: "API_ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EventError(tt.ev)
			if tt.want == "" {
				if got != nil {
					t.Fatalf("EventError() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("EventError() = nil, want %q", tt.want)
			}
			if got.Error() != tt.want {
				t.Errorf("EventError() = %q, want %q", got.Error(), tt.want)
			}
		})
	}
}
