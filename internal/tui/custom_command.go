package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/extension"
)

// handleCustomCommand expands a markdown command template and submits it as
// the next user turn. Arguments are the same whitespace-separated fields the
// rest of the slash-command parser already produces.
func (m *model) handleCustomCommand(cmd extension.Command, args []string) (tea.Model, tea.Cmd) {
	prompt := extension.ExpandCommand(cmd.Body, args)
	if strings.TrimSpace(prompt) == "" {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Command `/%s` has an empty prompt.", cmd.Name),
		})
		return m, nil
	}

	m.inputModel.Clear()
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "tool",
		tool:    "command",
		toolIn:  cmd.Name,
		content: "Expanded command",
	})
	return m.submitPrompt(prompt, nil)
}
