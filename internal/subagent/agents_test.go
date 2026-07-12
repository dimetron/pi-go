package subagent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAgentFile(t *testing.T) {
	// Create a temp agent file
	tmpDir := t.TempDir()
	agentFile := filepath.Join(tmpDir, "explore.md")
	agentContent := `---
name: explore
description: Quick exploration agent
role: smol
worktree: false
tools: read, grep, find, tree, ls
---
You are an exploration agent. Your job is to find information.

Strategy:
1. Orient with tree
2. Narrow with grep
3. Answer concisely.`

	err := os.WriteFile(agentFile, []byte(agentContent), 0644)
	if err != nil {
		t.Fatal(err)
	}

	agent, err := ParseAgentFile(agentFile)
	if err != nil {
		t.Fatalf("ParseAgentFile failed: %v", err)
	}

	if agent.Name != "explore" {
		t.Errorf("expected name 'explore', got %q", agent.Name)
	}
	if agent.Description != "Quick exploration agent" {
		t.Errorf("expected description 'Quick exploration agent', got %q", agent.Description)
	}
	if agent.Role != "smol" {
		t.Errorf("expected role 'smol', got %q", agent.Role)
	}
	if agent.Worktree != false {
		t.Errorf("expected worktree false, got %v", agent.Worktree)
	}
	if len(agent.Tools) != 5 {
		t.Errorf("expected 5 tools, got %d: %v", len(agent.Tools), agent.Tools)
	}
	if agent.Instruction == "" {
		t.Error("expected non-empty instruction")
	}
}

func TestParseAgentFile_MissingFields(t *testing.T) {
	tmpDir := t.TempDir()
	agentFile := filepath.Join(tmpDir, "test.md")
	agentContent := `---
name: test-agent
---
Just the instruction body.`

	err := os.WriteFile(agentFile, []byte(agentContent), 0644)
	if err != nil {
		t.Fatal(err)
	}

	agent, err := ParseAgentFile(agentFile)
	if err != nil {
		t.Fatalf("ParseAgentFile failed: %v", err)
	}

	if agent.Name != "test-agent" {
		t.Errorf("expected name 'test-agent', got %q", agent.Name)
	}
	if agent.Description != "" {
		t.Errorf("expected empty description, got %q", agent.Description)
	}
	if agent.Role != "" {
		t.Errorf("expected empty role, got %q", agent.Role)
	}
	if agent.Worktree != false {
		t.Errorf("expected default worktree false, got %v", agent.Worktree)
	}
	if len(agent.Tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(agent.Tools))
	}
	if agent.Instruction != "Just the instruction body." {
		t.Errorf("expected instruction 'Just the instruction body.', got %q", agent.Instruction)
	}
}

func TestParseAgentFile_EmptyBody(t *testing.T) {
	tmpDir := t.TempDir()
	agentFile := filepath.Join(tmpDir, "empty.md")
	agentContent := `---
name: empty
description: No body agent
role: default
worktree: true
tools: read
---`

	err := os.WriteFile(agentFile, []byte(agentContent), 0644)
	if err != nil {
		t.Fatal(err)
	}

	agent, err := ParseAgentFile(agentFile)
	if err != nil {
		t.Fatalf("ParseAgentFile failed: %v", err)
	}

	if agent.Name != "empty" {
		t.Errorf("expected name 'empty', got %q", agent.Name)
	}
	if agent.Instruction != "" {
		t.Errorf("expected empty instruction, got %q", agent.Instruction)
	}
}

func TestLoadAgentsFromDir(t *testing.T) {
	tmpDir := t.TempDir()

	// Create multiple agent files
	agents := map[string]string{
		"explore.md": `---
name: explore
role: smol
---
Explore agent`,
		"plan.md": `---
name: plan
role: plan
---
Plan agent`,
		"invalid.txt": "not an agent", // Should be ignored
	}

	for filename, content := range agents {
		path := filepath.Join(tmpDir, filename)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	loaded, err := LoadAgentsFromDir(tmpDir)
	if err != nil {
		t.Fatalf("LoadAgentsFromDir failed: %v", err)
	}

	if len(loaded) != 2 {
		t.Errorf("expected 2 agents, got %d: %v", len(loaded), loaded)
	}

	// Check names are correct
	names := make(map[string]bool)
	for _, a := range loaded {
		names[a.Name] = true
	}
	if !names["explore"] {
		t.Error("missing 'explore' agent")
	}
	if !names["plan"] {
		t.Error("missing 'plan' agent")
	}
}

func TestLoadAgentsFromDir_EmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	loaded, err := LoadAgentsFromDir(tmpDir)
	if err != nil {
		t.Fatalf("LoadAgentsFromDir failed: %v", err)
	}

	if len(loaded) != 0 {
		t.Errorf("expected 0 agents, got %d", len(loaded))
	}
}

func TestLoadAgentsFromDir_NonExistent(t *testing.T) {
	loaded, err := LoadAgentsFromDir("/nonexistent/path")
	if err != nil {
		t.Fatalf("LoadAgentsFromDir failed: %v", err)
	}

	if loaded != nil {
		t.Errorf("expected nil for non-existent dir, got %v", loaded)
	}
}

func TestDiscoverAgents_ProjectDir(t *testing.T) {
	// Create a proper project structure: .pi-go/agents inside a project dir
	tmpDir := t.TempDir()
	projectRoot := filepath.Join(tmpDir, "myproject")
	agentsDir := filepath.Join(projectRoot, ".pi-go", "agents")
	os.MkdirAll(agentsDir, 0755)

	// Project has explore agent
	projectContent := `---
name: explore
description: Project explore
role: plan
---
Project instruction`
	os.WriteFile(filepath.Join(agentsDir, "explore.md"), []byte(projectContent), 0755)

	result, err := DiscoverAgents(projectRoot, ScopeProject)
	if err != nil {
		t.Fatalf("DiscoverAgents failed: %v", err)
	}

	// Should find project agent
	if len(result.Project) != 1 {
		t.Errorf("expected 1 project agent, got %d", len(result.Project))
	}

	// Should be in All as well (merged)
	if len(result.All) != 1 {
		t.Errorf("expected 1 agent in All, got %d", len(result.All))
	}

	if result.All[0].Description != "Project explore" {
		t.Errorf("expected 'Project explore', got %q", result.All[0].Description)
	}
}

func TestFindAgent(t *testing.T) {
	result := &AgentDiscoveryResult{
		All: []AgentConfig{
			{Name: "explore", Role: "smol"},
			{Name: "plan", Role: "plan"},
		},
	}

	found, ok := FindAgent(result, "explore")
	if !ok {
		t.Fatal("expected to find 'explore' agent")
	}
	if found.Role != "smol" {
		t.Errorf("expected role 'smol', got %q", found.Role)
	}

	_, ok = FindAgent(result, "nonexistent")
	if ok {
		t.Error("expected not to find nonexistent agent")
	}
}

func TestLoadBundledAgents(t *testing.T) {
	agents, err := LoadBundledAgents()
	if err != nil {
		t.Fatalf("LoadBundledAgents failed: %v", err)
	}

	// Expected 17 agents: explore, plan, designer, task, quick-task, worker, code-reviewer, spec-reviewer, memory-compressor, discovery, claude, gemini, cursor, copilot, architect, codex, codex-review
	if len(agents) != 17 {
		t.Errorf("expected 17 bundled agents, got %d: %v", len(agents), agentNames(agents))
	}

	// Verify all agents have required fields
	expectedNames := map[string]bool{
		"explore":           false,
		"plan":              false,
		"designer":          false,
		"task":              false,
		"quick-task":        false,
		"worker":            false,
		"code-reviewer":     false,
		"spec-reviewer":     false,
		"memory-compressor": false,
		"discovery":         false,
		"claude":            false,
		"gemini":            false,
		"cursor":            false,
		"copilot":           false,
		"architect":         false,
		"codex":             false,
		"codex-review":      false,
	}

	for _, agent := range agents {
		if agent.Name == "" {
			t.Error("agent has empty name")
		}
		if agent.Source != "bundled" {
			t.Errorf("expected source 'bundled', got %q for agent %s", agent.Source, agent.Name)
		}
		expectedNames[agent.Name] = true
	}

	for name, found := range expectedNames {
		if !found {
			t.Errorf("missing bundled agent: %s", name)
		}
	}
}

func TestDiscoverAgents_Bundled(t *testing.T) {
	result, err := DiscoverAgents(".", ScopeBundled)
	if err != nil {
		t.Fatalf("DiscoverAgents failed: %v", err)
	}

	// Should have 17 bundled agents
	if len(result.Bundled) != 17 {
		t.Errorf("expected 17 bundled agents, got %d", len(result.Bundled))
	}

	// All should have bundled source
	for _, agent := range result.Bundled {
		if agent.Source != "bundled" {
			t.Errorf("expected source 'bundled', got %q", agent.Source)
		}
	}
}

func TestDiscoverAgents_Both(t *testing.T) {
	result, err := DiscoverAgents(".", ScopeBoth)
	if err != nil {
		t.Fatalf("DiscoverAgents failed: %v", err)
	}

	// Should have bundled agents
	if len(result.Bundled) != 17 {
		t.Errorf("expected 17 bundled agents, got %d", len(result.Bundled))
	}

	// All should be in merged All slice
	if len(result.All) < 17 {
		t.Errorf("expected at least 17 agents in All, got %d", len(result.All))
	}
}

func agentNames(agents []AgentConfig) []string {
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name
	}
	return names
}

func TestLoadBundledAgents_HasExpectedTypes(t *testing.T) {
	agents, err := LoadBundledAgents()
	if err != nil {
		t.Fatalf("LoadBundledAgents() error: %v", err)
	}
	if len(agents) < 6 {
		t.Errorf("expected at least 6 bundled agents, got %d", len(agents))
	}

	nameSet := make(map[string]bool)
	for _, a := range agents {
		nameSet[a.Name] = true
	}
	if !nameSet["explore"] {
		t.Error("missing 'explore' in bundled agents")
	}
	if !nameSet["task"] {
		t.Error("missing 'task' in bundled agents")
	}
}

func TestParseAgentFile_WithTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	agentFile := filepath.Join(tmpDir, "timed.md")
	agentContent := `---
name: timed-agent
description: Agent with timeout
role: smol
timeout: 30000
worktree: true
---
Agent with a 30s timeout.`

	if err := os.WriteFile(agentFile, []byte(agentContent), 0644); err != nil {
		t.Fatal(err)
	}

	agent, err := ParseAgentFile(agentFile)
	if err != nil {
		t.Fatalf("ParseAgentFile failed: %v", err)
	}

	if agent.Timeout != 30000 {
		t.Errorf("expected timeout 30000, got %d", agent.Timeout)
	}
	if !agent.Worktree {
		t.Error("expected worktree true")
	}
}

func TestParseAgentFile_NoFrontmatter(t *testing.T) {
	tmpDir := t.TempDir()
	agentFile := filepath.Join(tmpDir, "no-fm.md")
	agentContent := "Just a plain markdown file without frontmatter."

	if err := os.WriteFile(agentFile, []byte(agentContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Parser treats everything as body text when there's no frontmatter.
	// Name is derived from filename.
	agent, err := ParseAgentFile(agentFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agent.Name != "no-fm" {
		t.Errorf("expected name derived from filename 'no-fm', got %q", agent.Name)
	}
	if agent.Instruction != agentContent {
		t.Errorf("expected instruction = file content, got %q", agent.Instruction)
	}
}

func TestParseAgentFile_FileNotFound(t *testing.T) {
	_, err := ParseAgentFile("/nonexistent/path/agent.md")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestParseAgentFrontmatterLine(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantKey   string
		wantValue string
		wantOK    bool
	}{
		{"valid key-value", "name: explore", "name", "explore", true},
		{"value with spaces", "description: A long description here", "description", "A long description here", true},
		{"empty value", "role:", "role", "", true},
		{"value with colon", "description: foo: bar", "description", "foo: bar", true},
		{"no colon", "just-a-line", "", "", false},
		{"empty line", "", "", "", false},
		{"only whitespace", "   ", "", "", false},
		{"leading spaces", "  name: test", "name", "test", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, value, ok := parseAgentFrontmatterLine(tt.line)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if key != tt.wantKey {
				t.Errorf("key = %q, want %q", key, tt.wantKey)
			}
			if value != tt.wantValue {
				t.Errorf("value = %q, want %q", value, tt.wantValue)
			}
		})
	}
}

func TestParseAgentContent_UnknownFrontmatterKeys(t *testing.T) {
	content := `---
name: test
unknown_key: some_value
another: 42
description: valid desc
---
Body text.`
	cfg, err := parseAgentContent(content, "test.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name != "test" {
		t.Errorf("name = %q, want test", cfg.Name)
	}
	if cfg.Description != "valid desc" {
		t.Errorf("description = %q, want 'valid desc'", cfg.Description)
	}
}

func TestParseAgentContent_InvalidTimeout(t *testing.T) {
	content := `---
name: test
timeout: notanumber
---
Body.`
	cfg, err := parseAgentContent(content, "test.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Timeout != 0 {
		t.Errorf("timeout = %d, want 0 (invalid should be ignored)", cfg.Timeout)
	}
}

func TestParseAgentContent_NegativeTimeout(t *testing.T) {
	content := `---
name: test
timeout: -500
---
Body.`
	cfg, err := parseAgentContent(content, "test.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Timeout != 0 {
		t.Errorf("timeout = %d, want 0 (negative should be ignored)", cfg.Timeout)
	}
}

func TestParseAgentContent_ToolsWithEmptyEntries(t *testing.T) {
	content := `---
name: test
tools: read, , write, , grep
---
Body.`
	cfg, err := parseAgentContent(content, "test.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Tools) != 3 {
		t.Errorf("expected 3 tools (skipping empty), got %d: %v", len(cfg.Tools), cfg.Tools)
	}
}

func TestParseAgentContent_WorktreeTrue(t *testing.T) {
	content := `---
name: test
worktree: TRUE
---
Body.`
	cfg, err := parseAgentContent(content, "test.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Worktree {
		t.Error("expected worktree true for case-insensitive 'TRUE'")
	}
}
