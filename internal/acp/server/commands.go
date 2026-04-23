package server

import (
	"strings"

	acp "github.com/coder/acp-go-sdk"

	"github.com/dimetron/pi-go/internal/acp/server/adapter"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/subagent"
)

// DiscoverAvailableCommands resolves slash commands for a specific session cwd.
// User skills are always included, while project skills are discovered from the
// nearest ancestor directories of the session working directory.
func DiscoverAvailableCommands(cwd string) []acp.AvailableCommand {
	cwd = normalizeDiscoveryCWD(cwd)

	skills, _ := extension.LoadSkills(extension.DefaultSkillDirsIn(cwd)...)

	var subagents []subagent.AgentConfig
	if discovery, err := subagent.DiscoverAgents(cwd, subagent.ScopeBoth); err == nil && discovery != nil {
		subagents = discovery.All
	}

	return adapter.BuildAvailableCommands(skills, subagents)
}

func normalizeDiscoveryCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd != "" {
		return cwd
	}
	return "."
}
