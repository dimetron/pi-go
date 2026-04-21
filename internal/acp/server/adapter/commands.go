package adapter

import (
	acp "github.com/coder/acp-go-sdk"

	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/subagent"
)

// metaCommands are always advertised, independent of which skills or
// sub-agents are loaded. They map to built-in pi behaviors handled by the
// runtime — resetting conversation history, summarizing it, or listing
// available commands. The list is prefixed-unique from skill and sub-agent
// names because the loaders validate those against filesystem rules.
var metaCommands = []acp.AvailableCommand{
	{Name: "clear", Description: "Reset the conversation"},
	{Name: "compact", Description: "Summarize and shorten history"},
	{Name: "help", Description: "List available commands"},
}

// BuildAvailableCommands assembles the slash-command list advertised in
// InitializeResponse.AvailableCommands. The result is the concatenation of
// meta commands, one entry per discovered skill, and one per discovered
// sub-agent — in that order. Input order within each group is preserved
// because discovery is already deterministic (project > user > bundled).
//
// Wiring into Initialize is Zed-09's responsibility; this function is
// pure so it can be unit-tested without spinning up an ACP server.
func BuildAvailableCommands(skills []extension.Skill, subagents []subagent.AgentConfig) []acp.AvailableCommand {
	out := make([]acp.AvailableCommand, 0, len(metaCommands)+len(skills)+len(subagents))
	out = append(out, metaCommands...)
	for _, sk := range skills {
		if sk.Name == "" {
			continue
		}
		out = append(out, acp.AvailableCommand{
			Name:        sk.Name,
			Description: sk.Description,
		})
	}
	for _, sa := range subagents {
		if sa.Name == "" {
			continue
		}
		out = append(out, acp.AvailableCommand{
			Name:        sa.Name,
			Description: sa.Description,
		})
	}
	return out
}
