package webserver

import (
	"testing"
)

func TestNewPtyBridge(t *testing.T) {
	bridge := NewPtyBridge("/tmp/test-project", "test-model", "http://localhost:11434", nil, false, nil)
	if bridge == nil {
		t.Fatal("NewPtyBridge should not return nil")
	}
	if bridge.project != "/tmp/test-project" {
		t.Errorf("expected project /tmp/test-project, got %q", bridge.project)
	}
	if bridge.model != "test-model" {
		t.Errorf("expected model test-model, got %q", bridge.model)
	}
	if bridge.baseURL != "http://localhost:11434" {
		t.Errorf("expected baseURL http://localhost:11434, got %q", bridge.baseURL)
	}
	if bridge.done == nil {
		t.Error("done channel should be initialized")
	}
}

func TestPtyBridge_Close(t *testing.T) {
	bridge := NewPtyBridge("/tmp/test-project", "", "", nil, false, nil)

	// Close should not panic even when called multiple times
	if err := bridge.Close(); err != nil {
		t.Errorf("first Close should not return error: %v", err)
	}
	if err := bridge.Close(); err != nil {
		t.Errorf("second Close should not panic or return error: %v", err)
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

func TestPtyPool_CloseAll(t *testing.T) {
	pool := NewPtyPool(nil)
	pool.CloseAll()
}
