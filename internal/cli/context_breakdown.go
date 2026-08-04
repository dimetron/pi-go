package cli

import (
	"encoding/json"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/subagent"
	"github.com/dimetron/pi-go/internal/tui"

	adktool "google.golang.org/adk/v2/tool"
	"google.golang.org/genai"
)

// Fixed context overhead is measured here rather than in the TUI because this
// is where the pieces exist as the model will actually see them: the resolved
// instruction, the built tool declarations, the discovered subagents. Measuring
// them anywhere else would mean re-deriving them and risking a number that
// quietly disagrees with the request on the wire.

// tokensFromChars converts a byte length to the usual ~4 chars/token estimate.
// This matches estimateEventTokens in internal/session, so the gauge's sections
// and the compactor's thresholds speak the same units.
func tokensFromChars(n int) int64 {
	return int64(n / 4)
}

// buildContextBreakdown measures the fixed per-session context overhead.
// Conversation is left zero — the TUI derives it per-render from the prompt
// size the provider reports.
func buildContextBreakdown(
	parts agent.InstructionParts,
	coreTools []adktool.Tool,
	toolsets []adktool.Toolset,
	skills []extension.Skill,
	agents []subagent.AgentConfig,
) *tui.ContextBreakdown {
	b := &tui.ContextBreakdown{
		SystemPrompt: tokensFromChars(len(parts.Base)),
		Rules:        tokensFromChars(len(parts.Rules)),
		Skills:       tokensFromChars(len(parts.Skills)),
	}

	// Tool declarations are what actually goes on the wire, so measure the
	// serialized declaration rather than the Go value.
	b.ToolDefs = tokensFromChars(declaredToolBytes(coreTools))
	b.MCPTools = tokensFromChars(toolsetBytes(toolsets))
	b.Subagents = tokensFromChars(subagentBytes(agents))

	// A skills menu is already counted in parts.Skills; the slice is only used
	// when the instruction had none (a caller that injected skills separately).
	if b.Skills == 0 && len(skills) > 0 {
		b.Skills = tokensFromChars(skillMenuBytes(skills))
	}

	return b
}

// declarationBytes returns the serialized size of a tool's model-facing
// declaration, or 0 for a tool that does not expose one.
func declarationBytes(t adktool.Tool) int {
	d, ok := t.(interface {
		Declaration() *genai.FunctionDeclaration
	})
	if !ok {
		return 0
	}
	decl := d.Declaration()
	if decl == nil {
		return 0
	}
	enc, err := json.Marshal(decl)
	if err != nil {
		return 0
	}
	return len(enc)
}

func declaredToolBytes(tools []adktool.Tool) int {
	total := 0
	for _, t := range tools {
		total += declarationBytes(t)
	}
	return total
}

// toolsetBytes measures MCP and other dynamic toolsets from their already-loaded
// tools. Toolset.Tools takes an invocation context and can block on a network
// round-trip, so measurement reads the cache instead: a server still connecting
// contributes 0, making this figure a floor rather than a stall.
func toolsetBytes(toolsets []adktool.Toolset) int {
	return declaredToolBytes(extension.LoadedToolsetTools(toolsets))
}

// subagentBytes measures what the subagent tool advertises to the model: each
// agent's name and description. Agent instructions are not counted — they are
// spent in the subagent's own context, not the parent's.
func subagentBytes(agents []subagent.AgentConfig) int {
	total := 0
	for _, a := range agents {
		total += len(a.Name) + len(a.Description)
	}
	return total
}

func skillMenuBytes(skills []extension.Skill) int {
	total := 0
	for _, s := range skills {
		total += len(s.Name) + len(s.Description) + 4 // "- /" and ": "
	}
	return total
}
