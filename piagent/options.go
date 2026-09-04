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
	model           model.LLM
	instruction     string
	extraPrompt     string
	tools           []tool.Tool
	toolsets        []tool.Toolset
	beforeTool      []llmagent.BeforeToolCallback
	afterTool       []llmagent.AfterToolCallback
	beforeModel     []llmagent.BeforeModelCallback
	afterModel      []llmagent.AfterModelCallback
	beforeTurn      []BeforeTurnFunc
	afterTurn       []AfterTurnFunc
	lspMode         LSPMode
	memoryEnabled   bool
	palaceEnabled   bool
	skillsEnabled   bool
	subagentEnabled bool
	onAgentEvent    AgentEventFunc
	contextWindow   int64
}

// defaultOptions splits pi-go's conventions along one line: reading them is on
// by default, writing to shared state is not.
//
// Skills and subagents read .pi-go/, which is the whole point of embedding
// this agent rather than writing your own. Memory and palace write to
// ~/.pi-go/memory and ~/.pi-go/palace.db — the same stores the user's real pi
// sessions use — and an embedder's process is not a pi session. Silently
// interleaving its observations with the user's is a surprise no amount of
// documentation fixes, so it is opt-in. This is the one place piagent
// deliberately differs from the CLI's defaults.
func defaultOptions() options {
	return options{
		lspMode:         LSPMin,
		memoryEnabled:   false,
		palaceEnabled:   false,
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

// WithModel supplies the model the agent runs on. It is required: piagent
// never constructs a provider, so there is no default to fall back to and
// [New] returns [ErrNoModel] without it.
//
// Any ADK model works. pi-go's own providers — credentials, base URLs,
// transport options, thinking level, token metering — live in a separate
// package that builds one for you:
//
//	m, err := pimodels.FromConfig(ctx, "")   // the model a pi session would pick
//	ag, err := piagent.New(ctx, piagent.WithModel(m))
//
// Keeping the seam at ADK's interface means neither package depends on the
// other, and a change to provider handling is not a breaking change here. It
// is also what makes an embed testable: a fake model.LLM drives a whole turn
// with no network.
func WithModel(m model.LLM) Option {
	return func(o *options) { o.model = m }
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

// WithBeforeTurn adds hooks that run before each turn is dispatched, in the
// order given. The first to return an error aborts the turn — nothing reaches
// the model, and the error surfaces through the event sequence.
//
// Use it for admission control: budget and quota checks, rate limiting,
// moderating the outgoing message, or seeding session state.
//
// A hook runs when the caller starts iterating, not when Run returns. A
// sequence nobody ranges over is not a turn.
func WithBeforeTurn(fns ...BeforeTurnFunc) Option {
	return func(o *options) { o.beforeTurn = append(o.beforeTurn, fns...) }
}

// WithAfterTurn adds hooks that run once a turn has finished — including when
// it failed, and when the caller broke out of the loop early, which
// [TurnInfo].Abandoned reports. They cannot change the outcome.
//
// Use it for metrics, audit trails and persistence. This is the headless
// equivalent of pi-go's turn_complete lifecycle hook, which otherwise fires
// only from the TUI.
func WithAfterTurn(fns ...AfterTurnFunc) Option {
	return func(o *options) { o.afterTurn = append(o.afterTurn, fns...) }
}

// WithLSP selects the LSP tool surface. Even [LSPFull] registers nothing when
// no language server for the workspace is installed.
func WithLSP(mode LSPMode) Option {
	return func(o *options) { o.lspMode = mode }
}

// WithMemory turns on observation memory — the SQLite store under
// ~/.pi-go/memory, its background worker, and the memory search tools.
//
// Off by default, unlike the CLI. That store is shared with the user's real pi
// sessions, and an embedder's process is not one of them; opt in when the
// embedder is meant to contribute to it.
func WithMemory(enabled bool) Option {
	return func(o *options) { o.memoryEnabled = enabled }
}

// WithPalace turns on the memory palace. Off by default for the same reason as
// [WithMemory] — ~/.pi-go/palace.db is the user's. Once on, its tools still
// only register when the palace holds at least one drawer.
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

// WithContextWindow states the model's context window in tokens, which is what
// switches auto-compaction on.
//
// piagent cannot look this up. Resolving a window means consulting pi-go's
// model catalog, and this package is deliberately kept off provider
// construction — so the number is stated by whoever built the model. An
// embedder using pimodels has it to hand:
//
//	m, _ := pimodels.New(ctx, "gemini-3.8-flash")
//	ag, _ := piagent.New(ctx,
//		piagent.WithModel(m),
//		piagent.WithContextWindow(provider.ContextWindowSizeFor("gemini", m.Name())),
//	)
//
// Without it, and without context_window in ~/.pi-go/config.json, the window
// is unknown and compaction never fires — the transcript grows until the
// provider rejects it. A non-positive size is ignored.
func WithContextWindow(tokens int64) Option {
	return func(o *options) { o.contextWindow = tokens }
}
