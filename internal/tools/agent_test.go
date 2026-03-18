package tools

import (
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/subagent"
)

func TestAgentTool_Registration(t *testing.T) {
	cfg := config.Defaults()
	cfg.Roles["smol"] = config.RoleConfig{Model: "claude-haiku"}
	cfg.Roles["slow"] = config.RoleConfig{Model: "claude-opus"}
	cfg.Roles["plan"] = config.RoleConfig{Model: "claude-sonnet"}

	orch := subagent.NewOrchestrator(&cfg, "", nil)

	tools, err := AgentTools(orch, nil)
	if err != nil {
		t.Fatalf("AgentTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 agent tool, got %d", len(tools))
	}
	if tools[0].Name() != "agent" {
		t.Errorf("expected tool name 'agent', got %q", tools[0].Name())
	}
}

func TestNewAgentTool(t *testing.T) {
	cfg := config.Defaults()
	orch := subagent.NewOrchestrator(&cfg, "", nil)

	tool, err := NewAgentTool(orch, nil)
	if err != nil {
		t.Fatalf("NewAgentTool: %v", err)
	}
	if tool.Name() != "agent" {
		t.Errorf("expected 'agent', got %q", tool.Name())
	}
}
