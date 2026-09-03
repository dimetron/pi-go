package tui

import (
	"context"
	"strings"
	"testing"
)

func TestHandleModelPriceRefreshCommand_AddsPlaceholder(t *testing.T) {
	m := &model{
		ctx:        context.Background(),
		inputModel: InputModel{Text: "/model-price-refresh"},
		chatModel:  ChatModel{Messages: make([]message, 0)},
	}

	newM, cmd := m.handleModelPriceRefreshCommand(nil)
	mm := newM.(*model)

	if cmd == nil {
		t.Error("expected non-nil cmd for async refresh")
	}
	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 placeholder message, got %d", len(mm.chatModel.Messages))
	}
	if mm.chatModel.Messages[0].role != "thinking" {
		t.Errorf("expected role 'thinking', got %q", mm.chatModel.Messages[0].role)
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "Refreshing model prices") {
		t.Errorf("expected refresh placeholder, got %q", mm.chatModel.Messages[0].content)
	}
	if mm.inputModel.Text != "" {
		t.Errorf("input should be cleared, got %q", mm.inputModel.Text)
	}
}

func TestHandleModelPriceRefreshDone_ReplacesPlaceholder(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: []message{
			{role: "thinking", content: "Refreshing model prices from models.dev..."},
		}},
	}

	newM, _ := m.handleModelPriceRefreshDone(modelPriceRefreshDoneMsg{err: nil})
	mm := newM.(*model)

	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message (replaced), got %d", len(mm.chatModel.Messages))
	}
	if mm.chatModel.Messages[0].role != "assistant" {
		t.Errorf("expected role 'assistant', got %q", mm.chatModel.Messages[0].role)
	}
	if !strings.Contains(mm.chatModel.Messages[0].content, "✓ Model prices refreshed") {
		t.Errorf("expected success message, got %q", mm.chatModel.Messages[0].content)
	}
}

func TestHandleModelPriceRefreshDone_Error(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: []message{
			{role: "thinking", content: "Refreshing model prices from models.dev..."},
		}},
	}

	newM, _ := m.handleModelPriceRefreshDone(modelPriceRefreshDoneMsg{err: context.DeadlineExceeded})
	mm := newM.(*model)

	if !strings.Contains(mm.chatModel.Messages[0].content, "✗ Model price refresh failed") {
		t.Errorf("expected failure message, got %q", mm.chatModel.Messages[0].content)
	}
}
