package tools

import (
	"google.golang.org/adk/tool"

	"github.com/dimetron/pi-go/internal/subagent"
)

// AgentEventCallback is the legacy callback type for subagent events.
// Deprecated: Use SubagentEventCallback instead.
type AgentEventCallback func(agentID, eventType, content string)

// AgentTools returns tools that require an orchestrator (the subagent tool).
// The optional onEvent callback is invoked for each subagent event.
func AgentTools(orch *subagent.Orchestrator, onEvent AgentEventCallback) ([]tool.Tool, error) {
	// Wrap the legacy callback into the new SubagentEventCallback.
	var cb SubagentEventCallback
	if onEvent != nil {
		cb = func(ev SubagentEvent) {
			onEvent(ev.AgentID, ev.Kind, ev.Content)
		}
	}
	return SubagentTools(orch, cb)
}
