package eval

import (
	"fmt"
	"os"
	"sort"

	adktool "google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/lsp"
	"github.com/dimetron/pi-go/internal/memory"
	"github.com/dimetron/pi-go/internal/palace"
	"github.com/dimetron/pi-go/internal/subagent"
	"github.com/dimetron/pi-go/internal/tools"
)

// ToolInfo describes one tool the pi agent can expose to the model.
type ToolInfo struct {
	// Name is the function name the model calls — exactly what lands in
	// ATIF tool_calls[].function_name.
	Name string `json:"name"`
	// Group is the constructor family the tool comes from (core, bash-control,
	// subagent, memory, lsp, llms, a2a, palace, provider).
	Group string `json:"group"`
	// Requires names the capability the tool is gated on in a real session:
	// "" when it is always registered in print mode, otherwise the config or
	// environment a session needs before the tool is advertised at all.
	Requires string `json:"requires,omitempty"`
}

// Inventory enumerates every tool the pi agent can register, by constructing
// them through the same constructors the CLI uses (tools.CoreTools,
// tools.BashControlTools, tools.SubagentTools, tools.MemoryTools,
// tools.LSPToolsFor, tools.LLMSTools, tools.A2ATools, palace.PalaceTools and
// the Gemini grounding tool). It is the source of truth for "which tools
// exist": a tool added to any of those constructors shows up here without
// anyone editing a list, which is what lets the coverage check catch a new
// tool that has no eval scenario.
//
// Tools are constructed against a throwaway sandbox rooted at dir and never
// run, so the call is cheap and side-effect free. Optional groups are built
// with an empty configuration (no LSP server, no llms sources, no A2A agents,
// an in-memory observation store, a zero palace): the tool objects exist
// regardless of whether the capability behind them would work, which is
// exactly what is needed to learn their names.
func Inventory(dir string) ([]ToolInfo, error) {
	var out []ToolInfo
	add := func(group, requires string, ts []adktool.Tool) {
		for _, t := range ts {
			out = append(out, ToolInfo{Name: t.Name(), Group: group, Requires: requires})
		}
	}

	sb, err := tools.NewSandbox(dir)
	if err != nil {
		return nil, fmt.Errorf("inventory sandbox: %w", err)
	}
	defer func() { _ = sb.Close() }()

	sup := tools.NewBashSupervisor()
	core, err := tools.CoreTools(sb, tools.WithBashSupervisor(sup))
	if err != nil {
		return nil, fmt.Errorf("inventory core tools: %w", err)
	}
	add("core", "", core)

	bashCtl, err := tools.BashControlTools(sup)
	if err != nil {
		return nil, fmt.Errorf("inventory bash control tools: %w", err)
	}
	add("bash-control", "", bashCtl)

	cfg := config.Defaults()
	orch := subagent.NewOrchestrator(&cfg, "", nil)
	defer orch.Shutdown()
	sub, err := tools.SubagentTools(orch, nil)
	if err != nil {
		return nil, fmt.Errorf("inventory subagent tools: %w", err)
	}
	add("subagent", "", sub)

	memDB, err := memory.OpenDB(":memory:")
	if err != nil {
		return nil, fmt.Errorf("inventory memory store: %w", err)
	}
	memStore := memory.NewSQLiteStore(memDB)
	memTools, err := tools.MemoryTools(memStore)
	_ = memStore.Close()
	if err != nil {
		return nil, fmt.Errorf("inventory memory tools: %w", err)
	}
	add("memory", "memory", memTools)

	lspTools, err := tools.LSPToolsFor(lsp.NewManager(nil), tools.LSPFull)
	if err != nil {
		return nil, fmt.Errorf("inventory lsp tools: %w", err)
	}
	add("lsp", "lsp", lspTools)

	add("llms", "llms-config", tools.LLMSTools(tools.NewLLMSToolset(&config.LLMSConfig{})))
	add("a2a", "a2a-config", tools.A2ATools(tools.NewClientCache(&config.A2AConfig{})))

	palaceTools, err := palace.PalaceTools(&palace.Palace{})
	if err != nil {
		return nil, fmt.Errorf("inventory palace tools: %w", err)
	}
	add("palace", "palace", palaceTools)

	// The grounding tool is provider-supplied (Gemini only); it is gated on
	// the PI_NO_GROUNDING kill switch, which must not hide it from the
	// inventory.
	if gt, ok := withEnvUnset("PI_NO_GROUNDING", func() (adktool.Tool, bool) {
		return agent.GeminiGroundingTool("gemini")
	}); ok {
		add("provider", "gemini", []adktool.Tool{gt})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// withEnvUnset runs fn with the named environment variable cleared and
// restores it afterwards.
func withEnvUnset[T any](name string, fn func() (T, bool)) (T, bool) {
	prev, had := os.LookupEnv(name)
	_ = os.Unsetenv(name)
	defer func() {
		if had {
			_ = os.Setenv(name, prev)
		}
	}()
	return fn()
}

// ToolNames returns the names of an inventory in order.
func ToolNames(inv []ToolInfo) []string {
	names := make([]string, 0, len(inv))
	for _, ti := range inv {
		names = append(names, ti.Name)
	}
	return names
}
