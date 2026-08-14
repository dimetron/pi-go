package piagent

import (
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
)

// LSPMode selects how much of the LSP tool surface is advertised to the model.
// The declarations are billed on every request, so the default is deliberately
// narrow.
type LSPMode string

const (
	// LSPOff registers no LSP tools.
	LSPOff LSPMode = "off"
	// LSPMin registers symbols and diagnostics only. This is the default, and
	// covers ~90% of the LSP calls observed in practice.
	LSPMin LSPMode = "min"
	// LSPFull registers all seven LSP tools.
	LSPFull LSPMode = "full"
)

// AgentEventFunc receives progress from subagents and from backgrounded bash
// commands: an identifier for the emitter, a kind ("text", "tool", "done", …)
// and the payload. It is called from the agent's goroutines, so it must be
// safe for concurrent use and must not block.
type AgentEventFunc func(id, kind, content string)

// options is the resolved configuration [New] builds from. It is unexported so
// that every field stays changeable; embedders reach it only through [Option].
type options struct {
	workDir         string
	extraSandbox    []string
	sessionDir      string
	modelName       string
	baseURL         string
	apiKey          string
	llm             model.LLM
	instruction     string
	extraPrompt     string
	tools           []tool.Tool
	toolsets        []tool.Toolset
	beforeTool      []llmagent.BeforeToolCallback
	afterTool       []llmagent.AfterToolCallback
	beforeModel     []llmagent.BeforeModelCallback
	afterModel      []llmagent.AfterModelCallback
	lspMode         LSPMode
	memoryEnabled   bool
	palaceEnabled   bool
	skillsEnabled   bool
	subagentEnabled bool
	onAgentEvent    AgentEventFunc
}

// defaultOptions mirrors the CLI's defaults: every optional subsystem on, LSP
// narrow, and everything else resolved from configuration at build time.
func defaultOptions() options {
	return options{
		lspMode:         LSPMin,
		memoryEnabled:   true,
		palaceEnabled:   true,
		skillsEnabled:   true,
		subagentEnabled: true,
	}
}

// Option configures [New].
type Option func(*options)

// WithWorkingDir sets the directory the agent works in. It becomes the sandbox
// root, the starting point for project-context and skill discovery, and the
// repository root reported to subagents. Defaults to the process working
// directory.
func WithWorkingDir(dir string) Option {
	return func(o *options) { o.workDir = dir }
}

// WithExtraSandboxDirs grants the agent's file tools access to directories
// outside the working directory. Without this, everything outside the sandbox
// root (and ~/.pi-go, which is always added) is denied.
func WithExtraSandboxDirs(dirs ...string) Option {
	return func(o *options) { o.extraSandbox = append(o.extraSandbox, dirs...) }
}

// WithSessionDir overrides where sessions are persisted. The default,
// ~/.pi-go/sessions, is what the pi CLI reads, so leaving it alone lets `pi
// --resume` pick up a session an embedder started.
func WithSessionDir(dir string) Option {
	return func(o *options) { o.sessionDir = dir }
}

// WithModel overrides the model name from configuration, e.g.
// "claude-sonnet-5" or "ollama/qwen3". Ignored when [WithLLM] is used.
func WithModel(name string) Option {
	return func(o *options) { o.modelName = name }
}

// WithBaseURL points the provider at a custom OpenAI-compatible endpoint.
// Ignored when [WithLLM] is used.
func WithBaseURL(url string) Option {
	return func(o *options) { o.baseURL = url }
}

// WithAPIKey supplies the provider credential directly, for embedders that
// hold keys somewhere other than the environment or ~/.pi-go/.env. Ignored
// when [WithLLM] is used.
func WithAPIKey(key string) Option {
	return func(o *options) { o.apiKey = key }
}

// WithLLM supplies a ready ADK model, bypassing provider resolution entirely.
// Use it to drive the agent from a model you already constructed — or from a
// fake, which is how you test an embed without a network.
//
// The daily-token guardrail is not applied to an injected model: metering
// someone else's model is their decision, not ours.
func WithLLM(llm model.LLM) Option {
	return func(o *options) { o.llm = llm }
}

// WithInstruction replaces pi-go's built-in system prompt. Project context
// files and the skills menu are still appended, so the conventions survive.
// Most embedders want [WithExtraInstruction] instead.
func WithInstruction(s string) Option {
	return func(o *options) { o.instruction = s }
}

// WithExtraInstruction appends application-specific text to the assembled
// system prompt, after the built-in prompt, project rules and skills menu.
func WithExtraInstruction(s string) Option {
	return func(o *options) { o.extraPrompt = s }
}

// WithTools adds tools alongside pi-go's core set.
func WithTools(ts ...tool.Tool) Option {
	return func(o *options) { o.tools = append(o.tools, ts...) }
}

// WithToolsets adds toolsets alongside the MCP and A2A toolsets built from
// configuration.
func WithToolsets(ts ...tool.Toolset) Option {
	return func(o *options) { o.toolsets = append(o.toolsets, ts...) }
}

// WithBeforeToolCallbacks adds before-tool callbacks after pi-go's own. ADK
// runs them in order until one returns a non-nil result, which short-circuits
// the tool call and becomes its result.
func WithBeforeToolCallbacks(cbs ...llmagent.BeforeToolCallback) Option {
	return func(o *options) { o.beforeTool = append(o.beforeTool, cbs...) }
}

// WithAfterToolCallbacks adds after-tool callbacks after pi-go's own, so they
// see the compacted and deduplicated result.
//
// These do not get ADK's first-non-nil-wins semantics. piagent composes the
// whole chain into one callback: return (nil, nil) to leave the result alone,
// (m, nil) to replace it for every later callback and for the model, or a
// non-nil error to abort the turn. See the package documentation for why.
func WithAfterToolCallbacks(cbs ...llmagent.AfterToolCallback) Option {
	return func(o *options) { o.afterTool = append(o.afterTool, cbs...) }
}

// WithBeforeModelCallbacks adds before-model callbacks after pi-go's own.
// Returning a non-nil response from one skips the LLM call.
func WithBeforeModelCallbacks(cbs ...llmagent.BeforeModelCallback) Option {
	return func(o *options) { o.beforeModel = append(o.beforeModel, cbs...) }
}

// WithAfterModelCallbacks adds after-model callbacks after pi-go's own.
func WithAfterModelCallbacks(cbs ...llmagent.AfterModelCallback) Option {
	return func(o *options) { o.afterModel = append(o.afterModel, cbs...) }
}

// WithLSP selects the LSP tool surface. Even [LSPFull] registers nothing when
// no language server for the workspace is installed.
func WithLSP(mode LSPMode) Option {
	return func(o *options) { o.lspMode = mode }
}

// WithMemory enables or disables observation memory — the SQLite store under
// ~/.pi-go/memory, its background worker, and the memory search tools.
// Enabled by default, matching the CLI.
func WithMemory(enabled bool) Option {
	return func(o *options) { o.memoryEnabled = enabled }
}

// WithPalace enables or disables the memory palace. Enabled by default, but
// its tools still only register when the palace holds at least one drawer.
func WithPalace(enabled bool) Option {
	return func(o *options) { o.palaceEnabled = enabled }
}

// WithSkills enables or disables skill discovery and the "Available Skills"
// section of the system prompt. Enabled by default.
func WithSkills(enabled bool) Option {
	return func(o *options) { o.skillsEnabled = enabled }
}

// WithSubagents enables or disables subagent discovery and the tools that
// spawn them. Enabled by default.
func WithSubagents(enabled bool) Option {
	return func(o *options) { o.subagentEnabled = enabled }
}

// WithAgentEvents installs a sink for subagent and background-bash progress.
// Without one those events are discarded.
func WithAgentEvents(fn AgentEventFunc) Option {
	return func(o *options) { o.onAgentEvent = fn }
}
