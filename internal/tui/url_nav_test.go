package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestView_UsesRealBubbleTeaCursorForInput(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.Text = "http://somehost/path"
	im.CursorPos = len("http://some")

	m := &model{
		width:      80,
		height:     20,
		inputModel: im,
		chatModel:  NewChatModel(nil),
		statusModel: StatusModel{
			Width: 80,
		},
	}

	view := m.View()
	if view.Cursor == nil {
		t.Fatal("expected view to expose a real Bubble Tea cursor for focused input")
	}
	if view.Cursor.Shape != tea.CursorBar {
		t.Fatalf("expected bar cursor, got shape %v", view.Cursor.Shape)
	}
	if view.Cursor.X <= 2 || view.Cursor.Y <= 0 {
		t.Fatalf("expected cursor to be positioned inside the input line, got %+v", view.Cursor.Position)
	}

	m.running = true
	view = m.View()
	if view.Cursor != nil {
		t.Fatalf("expected no editable input cursor while running, got %+v", view.Cursor)
	}
}
