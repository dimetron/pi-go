package webserver

import (
	"testing"
)

func TestNewPtyBridge(t *testing.T) {
	bridge := NewPtyBridge("/tmp/test-project")
	if bridge == nil {
		t.Fatal("NewPtyBridge should not return nil")
	}
	if bridge.project != "/tmp/test-project" {
		t.Errorf("expected project /tmp/test-project, got %q", bridge.project)
	}
	if bridge.done == nil {
		t.Error("done channel should be initialized")
	}
}

func TestPtyBridge_Close(t *testing.T) {
	bridge := NewPtyBridge("/tmp/test-project")

	// Close should not panic
	err := bridge.Close()
	if err != nil {
		t.Errorf("Close should not return error: %v", err)
	}
}

func TestWSMessage_JSON(t *testing.T) {
	msg := WSMessage{
		Type: "input",
		Data: "hello",
	}

	if msg.Type != "input" {
		t.Errorf("expected type input, got %q", msg.Type)
	}
	if msg.Data != "hello" {
		t.Errorf("expected data hello, got %q", msg.Data)
	}
}
