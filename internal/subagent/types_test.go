package subagent

import (
	"testing"
)

func TestBundledAgents_AllDefined(t *testing.T) {
	agents, err := LoadBundledAgents()
	if err != nil {
		t.Fatalf("LoadBundledAgents failed: %v", err)
	}

	// Should have exactly 8 bundled agents
	expected := []string{"explore", "plan", "designer", "task", "quick-task", "worker", "code-reviewer", "spec-reviewer"}
	if len(agents) != len(expected) {
		t.Errorf("expected %d bundled agents, got %d: %v", len(expected), len(agents), agentNames(agents))
	}

	// Check all expected agents exist
	for _, name := range expected {
		found := false
		for _, a := range agents {
			if a.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected agent %q not found", name)
		}
	}
}

func TestBundledAgents_RoleMappings(t *testing.T) {
	validRoles := map[string]bool{
		"default": true,
		"smol":    true,
		"slow":    true,
		"plan":    true,
	}

	agents, err := LoadBundledAgents()
	if err != nil {
		t.Fatalf("LoadBundledAgents failed: %v", err)
	}

	for _, agent := range agents {
		if agent.Role == "" {
			t.Errorf("agent %q has empty role", agent.Name)
		}
		if !validRoles[agent.Role] {
			t.Errorf("agent %q maps to unknown role %q", agent.Name, agent.Role)
		}
	}
}

func TestBundledAgents_HaveInstructions(t *testing.T) {
	agents, err := LoadBundledAgents()
	if err != nil {
		t.Fatalf("LoadBundledAgents failed: %v", err)
	}

	for _, agent := range agents {
		if agent.Instruction == "" {
			t.Errorf("agent %q has empty instruction", agent.Name)
		}
		if len(agent.Tools) == 0 {
			t.Errorf("agent %q has no tools", agent.Name)
		}
	}
}

func TestBundledAgents_WorktreeTypes(t *testing.T) {
	// designer and task require worktrees.
	worktreeTypes := map[string]bool{"designer": true, "task": true}

	agents, err := LoadBundledAgents()
	if err != nil {
		t.Fatalf("LoadBundledAgents failed: %v", err)
	}

	for _, agent := range agents {
		if worktreeTypes[agent.Name] && !agent.Worktree {
			t.Errorf("agent %q should require worktree", agent.Name)
		}
		if !worktreeTypes[agent.Name] && agent.Worktree {
			t.Errorf("agent %q should NOT require worktree", agent.Name)
		}
	}
}

func TestAgentInput_ToSpawnInput(t *testing.T) {
	// Test valid agent type
	input := AgentInput{Type: "explore", Prompt: "test prompt"}
	spawnInput, err := input.ToSpawnInput()
	if err != nil {
		t.Fatalf("ToSpawnInput failed: %v", err)
	}
	if spawnInput.Agent.Name != "explore" {
		t.Errorf("expected agent name 'explore', got %q", spawnInput.Agent.Name)
	}
	if spawnInput.Prompt != "test prompt" {
		t.Errorf("expected prompt 'test prompt', got %q", spawnInput.Prompt)
	}

	// Test invalid agent type
	inputInvalid := AgentInput{Type: "nonexistent", Prompt: "test"}
	_, err = inputInvalid.ToSpawnInput()
	if err == nil {
		t.Fatal("expected error for invalid agent type")
	}
}