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
// meta commands, custom commands, one entry per discovered skill, and one
// per discovered sub-agent — in that order. A custom command whose name
// matches a meta command is omitted, and a skill whose name matches a custom
// command is omitted: the same precedence the TUI uses when it runs them.
// Input order within each group is preserved because discovery is already
// deterministic (project > user > plugin).
//
// Wiring into Initialize is Zed-09's responsibility; this function is
// pure so it can be unit-tested without spinning up an ACP server.
func BuildAvailableCommands(skills []extension.Skill, subagents []subagent.AgentConfig, commands []extension.Command) []acp.AvailableCommand {
	out := make([]acp.AvailableCommand, 0, len(metaCommands)+len(commands)+len(skills)+len(subagents))
	meta := make(map[string]struct{}, len(metaCommands))
	for _, m := range metaCommands {
		meta[m.Name] = struct{}{}
	}
	out = append(out, metaCommands...)
	taken := make(map[string]struct{}, len(commands))
	for _, cmd := range commands {
		if cmd.Name == "" {
			continue
		}
		if _, isMeta := meta[cmd.Name]; isMeta {
			continue
		}
		if _, seen := taken[cmd.Name]; seen {
			continue
		}
		taken[cmd.Name] = struct{}{}
		out = append(out, acp.AvailableCommand{
			Name:        cmd.Name,
			Description: cmd.Summary(),
		})
	}
	for _, sk := range skills {
		if sk.Name == "" {
			continue
		}
		if _, overridden := taken[sk.Name]; overridden {
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
