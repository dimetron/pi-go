package subagent

import (
	"testing"
)

func TestAgentTypes_AllDefined(t *testing.T) {
	expected := []string{"explore", "plan", "designer", "reviewer", "task", "quick_task"}
	for _, name := range expected {
		if _, ok := AgentTypes[name]; !ok {
			t.Errorf("agent type %q not defined", name)
		}
	}
	if len(AgentTypes) != len(expected) {
		t.Errorf("expected %d agent types, got %d", len(expected), len(AgentTypes))
	}
}

func TestAgentTypes_RoleMappings(t *testing.T) {
	validRoles := map[string]bool{
		"default": true,
		"smol":    true,
		"slow":    true,
		"plan":    true,
		"commit":  true,
	}

	for name, def := range AgentTypes {
		if def.Role == "" {
			t.Errorf("agent type %q has empty role", name)
		}
		if !validRoles[def.Role] {
			t.Errorf("agent type %q maps to unknown role %q", name, def.Role)
		}
	}
}

func TestAgentTypes_HaveInstructions(t *testing.T) {
	for name, def := range AgentTypes {
		if def.Instruction == "" {
			t.Errorf("agent type %q has empty instruction", name)
		}
		if len(def.Tools) == 0 {
			t.Errorf("agent type %q has no tools", name)
		}
	}
}

func TestAgentTypes_WorktreeTypes(t *testing.T) {
	// designer and task require worktrees.
	worktreeTypes := map[string]bool{"designer": true, "task": true}
	for name, def := range AgentTypes {
		if worktreeTypes[name] && !def.Worktree {
			t.Errorf("agent type %q should require worktree", name)
		}
		if !worktreeTypes[name] && def.Worktree {
			t.Errorf("agent type %q should NOT require worktree", name)
		}
	}
}

func TestValidateType_Valid(t *testing.T) {
	for name := range AgentTypes {
		if err := ValidateType(name); err != nil {
			t.Errorf("ValidateType(%q) unexpected error: %v", name, err)
		}
	}
}

func TestValidateType_Invalid(t *testing.T) {
	err := ValidateType("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}
