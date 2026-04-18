package acp

import "testing"

func TestRunRequestValidate(t *testing.T) {
	t.Run("zero value invalid", func(t *testing.T) {
		var req RunRequest
		if err := req.Validate(); err == nil {
			t.Fatal("expected error for zero-value request")
		}
	})

	t.Run("rejects blank command entry", func(t *testing.T) {
		req := RunRequest{Command: []string{"pi", " "}, Prompt: "hello"}
		if err := req.Validate(); err == nil {
			t.Fatal("expected error for blank command entry")
		}
	})

	t.Run("valid request", func(t *testing.T) {
		req := RunRequest{Command: []string{"pi", "serve"}, Prompt: "hello"}
		if err := req.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})
}

func TestEventValidate(t *testing.T) {
	t.Run("zero value invalid", func(t *testing.T) {
		var evt Event
		if err := evt.Validate(); err == nil {
			t.Fatal("expected error for zero-value event")
		}
	})

	t.Run("message requires content", func(t *testing.T) {
		evt := Event{Type: EventTypeMessage}
		if err := evt.Validate(); err == nil {
			t.Fatal("expected error for missing content")
		}
	})

	t.Run("error requires error text", func(t *testing.T) {
		evt := Event{Type: EventTypeError}
		if err := evt.Validate(); err == nil {
			t.Fatal("expected error for missing error text")
		}
	})

	t.Run("valid tool event", func(t *testing.T) {
		evt := Event{Type: EventTypeTool, Content: "bash"}
		if err := evt.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})
}

func TestRunResultValidate(t *testing.T) {
	t.Run("zero value invalid", func(t *testing.T) {
		var result RunResult
		if err := result.Validate(); err == nil {
			t.Fatal("expected error for zero-value result")
		}
	})

	t.Run("success requires result text", func(t *testing.T) {
		result := RunResult{Status: StatusSuccess}
		if err := result.Validate(); err == nil {
			t.Fatal("expected error for missing result")
		}
	})

	t.Run("error requires error text", func(t *testing.T) {
		result := RunResult{Status: StatusError}
		if err := result.Validate(); err == nil {
			t.Fatal("expected error for missing error")
		}
	})

	t.Run("valid success result", func(t *testing.T) {
		result := RunResult{Status: StatusSuccess, Result: "done", SessionID: "s1"}
		if err := result.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})
}
