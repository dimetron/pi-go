package server

import (
	"os"
	"path/filepath"
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

	skills, _ := extension.LoadSkills(discoverSkillDirs(cwd)...)

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
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func discoverSkillDirs(cwd string) []string {
	dirs := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)

	if homeDir, err := os.UserHomeDir(); err == nil {
		userDir := filepath.Join(homeDir, ".pi-go", "skills")
		dirs = appendUniqueDir(dirs, seen, userDir)
	}

	for _, rel := range []string{
		filepath.Join(".pi-go", "skills"),
		filepath.Join(".claude", "skills"),
		filepath.Join(".cursor", "skills"),
	} {
		if dir := findNearestDir(cwd, rel); dir != "" {
			dirs = appendUniqueDir(dirs, seen, dir)
		}
	}

	return dirs
}

func appendUniqueDir(dirs []string, seen map[string]struct{}, dir string) []string {
	if dir == "" {
		return dirs
	}
	if _, ok := seen[dir]; ok {
		return dirs
	}
	seen[dir] = struct{}{}
	return append(dirs, dir)
}

func findNearestDir(start, rel string) string {
	dir := start
	for {
		candidate := filepath.Join(dir, rel)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
