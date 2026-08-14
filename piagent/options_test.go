package piagent

import (
	"testing"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	adktool "google.golang.org/adk/v2/tool"
)

// TestDefaultsReadConventionsButDoNotWriteSharedState pins the one line this
// package draws differently from the CLI. Skills and subagents read .pi-go/,
// which is why an embedder reaches for this package at all. Memory and palace
// write to the stores a user's real pi sessions use, and an embedder's process
// is not a pi session — so those are opt-in.
func TestDefaultsReadConventionsButDoNotWriteSharedState(t *testing.T) {
	o := defaultOptions()
	if o.lspMode != LSPMin {
		t.Errorf("default LSP mode = %q, want %q", o.lspMode, LSPMin)
	}
	for name, on := range map[string]bool{
		"skills":    o.skillsEnabled,
		"subagents": o.subagentEnabled,
	} {
		if !on {
			t.Errorf("%s is off by default; reading pi-go's conventions should not need opting in", name)
		}
	}
	for name, on := range map[string]bool{
		"memory": o.memoryEnabled,
		"palace": o.palaceEnabled,
	} {
		if on {
			t.Errorf("%s is on by default; writing to the user's shared ~/.pi-go stores must be opt-in", name)
		}
	}
}

// stubToolset is an inert ADK toolset, enough to prove the option stored it.
type stubToolset struct{ adktool.Toolset }

func TestOptionsAreApplied(t *testing.T) {
	fake := &fakeLLM{name: "fake"}
	beforeTool := func(_ adkagent.Context, _ adktool.Tool, _ map[string]any) (map[string]any, error) {
		return nil, nil
	}
	afterTool := func(_ adkagent.Context, _ adktool.Tool, _, _ map[string]any, _ error) (map[string]any, error) {
		return nil, nil
	}
	beforeModel := func(adkagent.Context, *model.LLMRequest) (*model.LLMResponse, error) {
		return nil, nil
	}
	afterModel := func(adkagent.Context, *model.LLMResponse, error) (*model.LLMResponse, error) {
		return nil, nil
	}

	o := defaultOptions()
	for _, opt := range []Option{
		WithWorkingDir("/work"),
		WithExtraSandboxDirs("/data", "/cache"),
		WithSessionDir("/sessions"),
		WithModel(fake),
		WithInstruction("BASE"),
		WithExtraInstruction("EXTRA"),
		WithTools(namedTool{name: "custom"}),
		WithToolsets(stubToolset{}),
		WithBeforeToolCallbacks(beforeTool),
		WithAfterToolCallbacks(afterTool),
		WithBeforeModelCallbacks(llmagent.BeforeModelCallback(beforeModel)),
		WithAfterModelCallbacks(llmagent.AfterModelCallback(afterModel)),
		WithLSP(LSPFull),
		WithMemory(true),
		WithPalace(true),
		WithSkills(false),
		WithSubagents(false),
		WithAgentEvents(func(string, string, string) {}),
	} {
		opt(&o)
	}

	checks := []struct {
		name string
		ok   bool
	}{
		{"workDir", o.workDir == "/work"},
		{"extraSandbox", len(o.extraSandbox) == 2},
		{"sessionDir", o.sessionDir == "/sessions"},
		{"model", o.model == fake},
		{"instruction", o.instruction == "BASE"},
		{"extraPrompt", o.extraPrompt == "EXTRA"},
		{"tools", len(o.tools) == 1},
		{"toolsets", len(o.toolsets) == 1},
		{"beforeTool", len(o.beforeTool) == 1},
		{"afterTool", len(o.afterTool) == 1},
		{"beforeModel", len(o.beforeModel) == 1},
		{"afterModel", len(o.afterModel) == 1},
		{"lspMode", o.lspMode == LSPFull},
		{"memory", o.memoryEnabled},
		{"palace", o.palaceEnabled},
		{"skills", !o.skillsEnabled},
		{"subagents", !o.subagentEnabled},
		{"agentEvents", o.onAgentEvent != nil},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("option %s was not applied", c.name)
		}
	}
}

func TestExtraOptionsAccumulate(t *testing.T) {
	o := defaultOptions()
	WithExtraSandboxDirs("/a")(&o)
	WithExtraSandboxDirs("/b")(&o)
	WithTools(namedTool{name: "one"})(&o)
	WithTools(namedTool{name: "two"})(&o)

	if len(o.extraSandbox) != 2 || len(o.tools) != 2 {
		t.Errorf("repeated options replaced instead of accumulating: %d dirs, %d tools",
			len(o.extraSandbox), len(o.tools))
	}
}
