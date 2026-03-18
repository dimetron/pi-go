package subagent

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// AgentScope defines the scope for agent discovery.
type AgentScope string

const (
	ScopeBoth    AgentScope = "both"    // Bundled + user/project
	ScopeBundled AgentScope = "bundled" // Only embedded agents
	ScopeProject AgentScope = "project" // Only user/project agents
)

// AgentConfig represents a parsed agent definition from markdown.
type AgentConfig struct {
	Name        string   // Agent identifier (e.g., "explore", "plan")
	Description string   // One-line description from frontmatter
	Role        string   // Config role name for model resolution (e.g., "smol", "plan", "slow")
	Worktree    bool     // Whether this agent runs in an isolated git worktree
	Timeout     int      // Absolute timeout in milliseconds (0 = use default)
	Instruction string   // System prompt (markdown body)
	Tools       []string // Allowed tool names (empty = all tools)
	Source      string   // "bundled", "user", or "project"
}

// AgentDiscoveryResult contains all discovered agents.
type AgentDiscoveryResult struct {
	Bundled []AgentConfig
	User    []AgentConfig
	Project []AgentConfig
	All     []AgentConfig // Merged with priority: project > user > bundled
}

// ParseAgentFile parses a single agent markdown file.
// Expected format:
// ---
// name: agent-name
// description: One-line description
// role: smol
// worktree: false
// tools: read, write, edit
// ---
// Markdown instruction body...
func ParseAgentFile(path string) (AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return AgentConfig{}, err
	}
	return parseAgentContent(string(data), path)
}

// ParseAgentFileFromFS parses an agent file from an embedded filesystem.
func ParseAgentFileFromFS(fsys fs.FS, path string) (AgentConfig, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return AgentConfig{}, err
	}
	return parseAgentContent(string(data), path)
}

// parseAgentContent is the shared parsing logic for agent markdown content.
func parseAgentContent(content, path string) (AgentConfig, error) {
	// Derive default name from filename: explore.md → explore
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	cfg := AgentConfig{Name: name}

	scanner := bufio.NewScanner(strings.NewReader(content))
	inFrontmatter := false
	frontmatterDone := false
	var body strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "---" && !frontmatterDone {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			// End of frontmatter.
			inFrontmatter = false
			frontmatterDone = true
			continue
		}

		if inFrontmatter {
			key, value, ok := parseAgentFrontmatterLine(line)
			if !ok {
				continue
			}
			switch key {
			case "name":
				cfg.Name = value
			case "description":
				cfg.Description = value
			case "role":
				cfg.Role = value
			case "worktree":
				cfg.Worktree = strings.ToLower(value) == "true"
			case "timeout":
				if ms, err := strconv.Atoi(value); err == nil && ms > 0 {
					cfg.Timeout = ms
				}
			case "tools":
				for _, t := range strings.Split(value, ",") {
					t = strings.TrimSpace(t)
					if t != "" {
						cfg.Tools = append(cfg.Tools, t)
					}
				}
			}
		} else {
			body.WriteString(line)
			body.WriteString("\n")
		}
	}

	if err := scanner.Err(); err != nil {
		return AgentConfig{}, err
	}

	cfg.Instruction = strings.TrimSpace(body.String())
	return cfg, nil
}

// parseAgentFrontmatterLine parses "key: value" from a frontmatter line.
func parseAgentFrontmatterLine(line string) (key, value string, ok bool) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

// LoadAgentsFromDir loads all agent markdown files from a directory.
// Returns empty slice if directory doesn't exist (not an error).
func LoadAgentsFromDir(dir string) ([]AgentConfig, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading agents dir %s: %w", dir, err)
	}

	var agents []AgentConfig
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		agent, err := ParseAgentFile(path)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		agents = append(agents, agent)
	}
	return agents, nil
}

// findNearestProjectAgentsDir walks up from cwd looking for .pi-go/agents/.
func findNearestProjectAgentsDir(cwd string) (string, error) {
	dir := cwd
	for {
		agentsDir := filepath.Join(dir, ".pi-go", "agents")
		if info, err := os.Stat(agentsDir); err == nil && info.IsDir() {
			return agentsDir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // Reached root
		}
		dir = parent
	}
	return "", os.ErrNotExist
}

// DiscoverAgents loads agents from bundled, user, and project directories.
// Priority: project > user > bundled (later sources override earlier ones by name).
func DiscoverAgents(cwd string, scope AgentScope) (*AgentDiscoveryResult, error) {
	result := &AgentDiscoveryResult{}

	// Load bundled agents
	bundledAgents, err := LoadBundledAgents()
	if err != nil {
		return nil, fmt.Errorf("loading bundled agents: %w", err)
	}
	result.Bundled = bundledAgents

	// Load user agents (~/.pi-go/agents/)
	homeDir, err := os.UserHomeDir()
	if err == nil {
		userDir := filepath.Join(homeDir, ".pi-go", "agents")
		userAgents, err := LoadAgentsFromDir(userDir)
		if err != nil {
			return nil, fmt.Errorf("loading user agents: %w", err)
		}
		for i := range userAgents {
			userAgents[i].Source = "user"
		}
		result.User = userAgents
	}

	// Load project agents (.pi-go/agents/ in nearest ancestor)
	projectDir, err := findNearestProjectAgentsDir(cwd)
	if err == nil {
		projectAgents, err := LoadAgentsFromDir(projectDir)
		if err != nil {
			return nil, fmt.Errorf("loading project agents: %w", err)
		}
		for i := range projectAgents {
			projectAgents[i].Source = "project"
		}
		result.Project = projectAgents
	}

	// Merge all agents with priority: project > user > bundled
	seen := make(map[string]int) // name → index in All
	for _, agent := range result.Bundled {
		if idx, ok := seen[agent.Name]; ok {
			result.All[idx] = agent // bundled is lowest priority
		} else {
			seen[agent.Name] = len(result.All)
			result.All = append(result.All, agent)
		}
	}
	for _, agent := range result.User {
		if idx, ok := seen[agent.Name]; ok {
			result.All[idx] = agent // user overrides bundled
		} else {
			seen[agent.Name] = len(result.All)
			result.All = append(result.All, agent)
		}
	}
	for _, agent := range result.Project {
		if idx, ok := seen[agent.Name]; ok {
			result.All[idx] = agent // project overrides user
		} else {
			seen[agent.Name] = len(result.All)
			result.All = append(result.All, agent)
		}
	}

	// Filter based on scope
	if scope == ScopeBundled {
		result.All = result.Bundled
	} else if scope == ScopeProject {
		result.All = append(result.Project, result.User...)
	}

	return result, nil
}

// FindAgent looks up an agent by name from a discovery result.
func FindAgent(result *AgentDiscoveryResult, name string) (AgentConfig, bool) {
	for _, agent := range result.All {
		if agent.Name == name {
			return agent, true
		}
	}
	return AgentConfig{}, false
}

// AgentTypes returns a map of all bundled agent configurations.
// This is a convenience wrapper around LoadBundledAgents for test access.
func AgentTypes() map[string]AgentConfig {
	agents, err := LoadBundledAgents()
	if err != nil {
		return nil
	}
	m := make(map[string]AgentConfig, len(agents))
	for _, a := range agents {
		m[a.Name] = a
	}
	return m
}

// ValidateType validates that an agent type name exists in the bundled agents.
// Returns an error if the agent type is not found.
func ValidateType(name string) error {
	agents, err := LoadBundledAgents()
	if err != nil {
		return err
	}
	for _, a := range agents {
		if a.Name == name {
			return nil
		}
	}
	return fmt.Errorf("unknown agent type: %s", name)
}
