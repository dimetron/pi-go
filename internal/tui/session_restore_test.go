package tui

import (
	"testing"

	adkmodel "google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

func event(role string, parts ...*genai.Part) *session.Event {
	return &session.Event{LLMResponse: adkmodel.LLMResponse{
		Content: &genai.Content{Role: role, Parts: parts},
	}}
}

func textEvent(role, text string) *session.Event {
	return event(role, &genai.Part{Text: text})
}

func callPart(name string, args map[string]any) *genai.Part {
	return &genai.Part{FunctionCall: &genai.FunctionCall{Name: name, Args: args}}
}

func resultPart(name string, resp map[string]any) *genai.Part {
	return &genai.Part{FunctionResponse: &genai.FunctionResponse{Name: name, Response: resp}}
}

func TestRestoreTranscript_UserAndAssistant(t *testing.T) {
	msgs := restoreTranscript([]*session.Event{
		textEvent("user", "hello"),
		textEvent("model", "hi "),
		textEvent("model", "there"),
	})

	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(msgs), msgs)
	}
	if msgs[0].role != "user" || msgs[0].content != "hello" {
		t.Errorf("first message = %+v, want user/hello", msgs[0])
	}
	// Consecutive assistant chunks merge, as streaming would have merged them.
	if msgs[1].role != "assistant" || msgs[1].content != "hi there" {
		t.Errorf("second message = %+v, want assistant/%q", msgs[1], "hi there")
	}
}

func TestRestoreTranscript_SkipsThinking(t *testing.T) {
	msgs := restoreTranscript([]*session.Event{
		textEvent("thinking", "scratch work"),
		textEvent("model", "answer"),
	})

	if len(msgs) != 1 || msgs[0].content != "answer" {
		t.Fatalf("got %+v, want only the answer", msgs)
	}
}

func TestRestoreTranscript_ToolCallPairsWithResult(t *testing.T) {
	msgs := restoreTranscript([]*session.Event{
		event("model", callPart("read", map[string]any{"path": "main.go"})),
		event("user", resultPart("read", map[string]any{"content": "package main"})),
	})

	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1 tool message: %+v", len(msgs), msgs)
	}
	if msgs[0].role != "tool" || msgs[0].tool != "read" {
		t.Fatalf("got %+v, want a read tool message", msgs[0])
	}
	if msgs[0].content == "" {
		t.Error("tool result was not attached to its call")
	}
}

// A result must fill in its own call, not the nearest one, or parallel calls to
// the same tool would show each other's output.
func TestRestoreTranscript_ResultFillsOldestOpenCall(t *testing.T) {
	msgs := restoreTranscript([]*session.Event{
		event("model", callPart("grep", nil)),
		event("model", callPart("grep", nil)),
		event("user", resultPart("grep", map[string]any{"hits": "first"})),
		event("user", resultPart("grep", map[string]any{"hits": "second"})),
	})

	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(msgs), msgs)
	}
	for i, m := range msgs {
		if m.content == "" {
			t.Errorf("call %d left unanswered", i)
		}
	}
	if msgs[0].content == msgs[1].content {
		t.Errorf("both calls got the same result: %q", msgs[0].content)
	}
}

// Text after a tool call starts a new message, so rendered order matches event
// order instead of folding the reply back above the tool card.
func TestRestoreTranscript_TextAfterToolStartsNewMessage(t *testing.T) {
	msgs := restoreTranscript([]*session.Event{
		textEvent("model", "looking"),
		event("model", callPart("ls", nil)),
		textEvent("model", "done"),
	})

	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3: %+v", len(msgs), msgs)
	}
	if msgs[0].content != "looking" || msgs[1].role != "tool" || msgs[2].content != "done" {
		t.Errorf("order not preserved: %+v", msgs)
	}
}

func TestRestoreTranscript_SkipsEmptyEvents(t *testing.T) {
	if msgs := restoreTranscript([]*session.Event{nil, {}, textEvent("model", "")}); len(msgs) != 0 {
		t.Fatalf("got %+v, want no messages", msgs)
	}
}
