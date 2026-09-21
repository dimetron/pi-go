package tui

import (
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/logger"
)

// groundingToolName is the label the chat renders for a Gemini grounded search.
const groundingToolName = agent.GroundingToolName

// emitGroundingEvents surfaces a server-side search as a synthetic tool
// call/result pair, so a search-backed answer shows the query it ran and the
// sources it used. Gemini performs grounding inside the API and OpenAI's
// web_search runs inside the Responses API; neither emits a FunctionCall, so
// without this the search is invisible: the model simply answers with fresh
// facts and no indication it went to the web.
//
// providerName decides the reported tool name. The two searches hit different
// indexes, so labeling an OpenAI search google_search would misstate where the
// facts came from. Empty falls back to the Gemini name.
//
// Does nothing when the response was not grounded, or when this search was
// already reported for the current turn — GroundingMetadata is repeated on every
// streamed chunk of the response it grounds.
func (m *model) emitGroundingEvents(ch chan agentMsg, gm *genai.GroundingMetadata, providerName string, seen map[string]bool, log *logger.Logger) {
	if gm == nil || len(gm.WebSearchQueries) == 0 {
		return
	}
	key := agent.GroundingQueryKey(gm.WebSearchQueries)
	if seen[key] {
		return
	}
	seen[key] = true
	toolName := agent.GroundingToolNameFor(providerName)

	args := map[string]any{"query": agent.GroundingQuery(gm)}
	if log != nil {
		log.ToolCall("grounding", toolName, args)
		// Full-fidelity sources (with the redirect URIs) go to the log; the chat
		// shows labels only, since the URIs are opaque and 200 chars wide.
		log.ToolResult("grounding", toolName, agent.GroundingSources(gm))
	}

	ch <- agentToolCallMsg{name: toolName, args: args}
	ch <- agentToolResultMsg{name: toolName, content: agent.GroundingSummary(gm)}
}
