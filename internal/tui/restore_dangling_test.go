package tui

import (
	"testing"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// TestRestoredDanglingCallIsNotPending guards the visible half
// of the interrupted-call report: restoreTranscript builds a tool card for a
// FunctionCall, and with no FunctionResponse to fill it the card keeps an empty
// content for the rest of the session — which the renderer draws as a blinking
// in-progress bullet (tool_display.go:254), not as a call that never finished.
//
// The session this mirrors is a real one: 2,109 calls across 574 sessions carry
// no response, and 180 of them are the last event in their file.
func TestRestoredDanglingCallIsNotPending(t *testing.T) {
	call := event("model", &genai.Part{FunctionCall: &genai.FunctionCall{
		ID: "call_dangling_1", Name: "bash",
		Args: map[string]any{"command": "kubectl apply -f harness.yaml"},
	}})

	msgs := restoreTranscript([]*session.Event{
		textEvent("user", "deploy"),
		call,
		textEvent("user", "actually use the staging cluster"),
	})

	var card *message
	for i := range msgs {
		if msgs[i].role == "tool" {
			card = &msgs[i]
		}
	}
	if card == nil {
		t.Fatal("harness is wrong: no tool card was restored for the call")
	}
	t.Logf("restored card: tool=%q toolID=%q content=%q", card.tool, card.toolID, card.content)
	if card.content == "" {
		t.Fatal("interrupted call restores as an empty card, which renders as still-running")
	}
	if card.toolID != "call_dangling_1" {
		t.Fatalf("card lost its call ID: %q", card.toolID)
	}
}
