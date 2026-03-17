package subagent

import (
	"embed"
	"io/fs"
)

// bundledFS embeds all markdown files in the bundled directory.
//
//go:embed bundled/*.md
var bundledFS embed.FS

// LoadBundledAgents loads all embedded agent definitions.
// Returns agents with Source set to "bundled".
func LoadBundledAgents() ([]AgentConfig, error) {
	var agents []AgentConfig

	entries, err := fs.ReadDir(bundledFS, "bundled")
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if len(name) < 4 || name[len(name)-3:] != ".md" {
			continue
		}

		path := "bundled/" + name
		agent, err := ParseAgentFileFromFS(bundledFS, path)
		if err != nil {
			// Skip unparseable files but continue
			continue
		}
		agent.Source = "bundled"
		agents = append(agents, agent)
	}

	return agents, nil
}