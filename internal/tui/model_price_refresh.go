package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/provider"
)

// modelPriceRefreshDoneMsg carries the result of an async /model-price-refresh.
type modelPriceRefreshDoneMsg struct {
	err error
}

// handleModelPriceRefreshCommand fetches a fresh models.dev pricing snapshot
// on demand and reports the outcome in the chat. It runs asynchronously so a
// slow models.dev never blocks the TUI.
func (m *model) handleModelPriceRefreshCommand(args []string) (tea.Model, tea.Cmd) {
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "thinking",
		content: "Refreshing model prices from models.dev...",
	})
	m.inputModel.Clear()

	ctx := m.ctx
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return modelPriceRefreshDoneMsg{err: provider.RefreshPricing(ctx)}
	}
}

// handleModelPriceRefreshDone replaces the "Refreshing..." placeholder with the
// outcome of the refresh.
func (m *model) handleModelPriceRefreshDone(msg modelPriceRefreshDoneMsg) (tea.Model, tea.Cmd) {
	content := "✓ Model prices refreshed from models.dev."
	if msg.err != nil {
		content = fmt.Sprintf("✗ Model price refresh failed: %v", msg.err)
	}

	reply := message{role: "assistant", content: content}
	if n := len(m.chatModel.Messages); n > 0 && m.chatModel.Messages[n-1].role == "thinking" {
		m.chatModel.Messages[n-1] = reply
	} else {
		m.chatModel.Messages = append(m.chatModel.Messages, reply)
	}
	return m, nil
}
