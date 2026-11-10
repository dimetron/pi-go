package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/dimetron/pi-go/internal/config"
)

// TestPickersRenderInAFrame composes a real frame with each picker open.
//
// The per-picker tests assert on state — which rows, which selection, what the
// model switched to — and a frame that never drew the popup would pass all of
// them. This is the check that the popup reaches the screen, and that adding it
// did not break the one invariant every row has to hold: it is exactly as wide
// as the terminal.
func TestPickersRenderInAFrame(t *testing.T) {
	assertFrame := func(t *testing.T, m *model, header string) {
		t.Helper()
		m.width, m.height = 100, 40
		m.chatModel.Width = 100
		view := m.View().Content

		if !strings.Contains(view, header) {
			t.Errorf("%s popup header missing from the frame", header)
		}
		for i, line := range strings.Split(view, "\n") {
			if got := ansi.StringWidth(line); got != 100 {
				t.Errorf("row %d is %d cells wide, want 100: %q", i, got, line)
			}
		}
	}

	t.Run("timeline", func(t *testing.T) {
		m := buildTranscript(t, 100)
		m.handleTimelineCommand()
		if m.searchPopup == nil {
			t.Fatal("/timeline did not open the picker")
		}
		assertFrame(t, m, "Timeline")
	})

	t.Run("theme", func(t *testing.T) {
		m := themePickerModel(t)
		if m.searchPopup == nil {
			t.Fatal("/theme did not open the picker")
		}
		assertFrame(t, m, "Themes")
	})

	t.Run("model", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		m := &model{
			ctx:        ctx,
			cancel:     cancel,
			inputModel: NewInputModel(nil, nil, nil, ""),
			chatModel:  ChatModel{Messages: make([]message, 0)},
			width:      100,
			height:     40,
			cfg: Config{
				ModelName:  "current",
				ActiveRole: "default",
				Roles: map[string]config.RoleConfig{
					"default": {Model: "claude-sonnet-4-6"},
					"smol":    {Model: "gemini-2.5-flash"},
				},
			},
		}
		_, _ = m.handleModelCommand(nil)
		if m.searchPopup == nil {
			t.Fatal("/model did not open the picker")
		}
		assertFrame(t, m, "Models")
	})
}
