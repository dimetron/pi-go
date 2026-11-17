package cli

import (
	"os"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/provider"

	adktool "google.golang.org/adk/v2/tool"
)

// compactContextWindow is the window at or below which a session starts on the
// compact profile: a short system prompt and the essential tools only.
//
// The default start does not fit a small model at all. Measured against
// `fm serve` (8,192 tokens): the default system prompt was 18,312 tokens and
// the tool declarations 8,108, so the request was refused whichever one was
// shortened — passing a one-line --system still left 9,044. The compact start
// is about 1,700. 16K is the line because the default start (about 26K) cannot
// fit below it either, and nothing in the catalog sits between 16K and 32K.
const compactContextWindow = 16_384

// essentialToolNames is what a coding turn cannot do without: look, search,
// change, run. Both spellings of the search tool are listed because it
// registers as "ripgrep" or "grep" depending on the host.
var essentialToolNames = map[string]bool{
	"read":    true,
	"write":   true,
	"edit":    true,
	"bash":    true,
	"ripgrep": true,
	"grep":    true,
	"find":    true,
	"ls":      true,
}

// useCompactProfile reports whether the model's window is too small for the
// default start. An unknown window (0) is not small: guessing would strip the
// tools from every model the catalog has not heard of.
func useCompactProfile(providerName, modelName string) bool {
	window := provider.ContextWindowSizeFor(providerName, modelName)
	return window > 0 && window <= compactContextWindow
}

// essentialTools keeps the tools named in essentialToolNames, in their
// original order.
func essentialTools(all []adktool.Tool) []adktool.Tool {
	kept := make([]adktool.Tool, 0, len(essentialToolNames))
	for _, t := range all {
		if essentialToolNames[t.Name()] {
			kept = append(kept, t)
		}
	}
	return kept
}

// compactInstructionParts is the system prompt for the compact profile. It
// carries no project rules and no skills menu: on an 8K window those two alone
// are a third of the budget, and the transcript needs the room more. --system
// still replaces the base outright.
func compactInstructionParts() agent.InstructionParts {
	if flagSystem != "" {
		return agent.InstructionParts{Base: flagSystem}
	}
	cwd, _ := os.Getwd()
	return agent.CompactInstructionParts(cwd)
}
