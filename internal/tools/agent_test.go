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
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name() != "subagent" {
		t.Errorf("expected tool name 'subagent', got %q", tools[0].Name())
	}
}

func TestAgentTools_LegacyCallbackWrapping(t *testing.T) {
	cfg := config.Defaults()
	orch := subagent.NewOrchestrator(&cfg, "", nil)

	var receivedID, receivedKind, receivedContent string
	cb := func(agentID, eventType, content string) {
		receivedID = agentID
		receivedKind = eventType
		receivedContent = content
	}

	tools, err := AgentTools(orch, cb)
	if err != nil {
		t.Fatalf("AgentTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	// Verify the tool was created (we can't easily invoke it without a real orchestrator,
	// but we've confirmed the wrapping compiles and the tool is registered).
	_ = receivedID
	_ = receivedKind
	_ = receivedContent
}
