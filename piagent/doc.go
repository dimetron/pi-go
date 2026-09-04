// Package piagent embeds pi-go's coding agent in a third-party Go program.
//
// Every other package in this module lives under internal/ and is therefore
// unreachable from outside. piagent is the one importable façade: it assembles
// the same agent the pi CLI runs in its headless (--mode print) path, minus the
// terminal UI, and hands it back as a small type you can drive from your own
// code.
//
// # The model comes from outside
//
// piagent never constructs a provider. The model arrives through [WithModel]
// as a google.golang.org/adk/v2/model.LLM — the interface the agent already
// runs on — and [New] returns [ErrNoModel] without one. pi-go's own providers
// (credentials, base URLs, transport options, thinking level, token metering)
// live in a separate package that builds one:
//
//	m, err := pimodels.FromConfig(ctx, "")   // the model a pi session would pick
//	if err != nil {
//		return err
//	}
//
//	ag, err := piagent.New(ctx, piagent.WithModel(m))
//	if err != nil {
//		return err
//	}
//	defer ag.Close()
//
//	sid, err := ag.NewSession(ctx)
//	if err != nil {
//		return err
//	}
//	answer, err := ag.Ask(ctx, sid, "what does this repo do?")
//
// Keeping the seam at ADK's interface means neither package imports the other,
// a change to provider handling is not a breaking change here, and a fake
// model.LLM drives a whole turn in a test with no network.
//
// # What one option buys
//
// That single option aside, everything else is resolved from pi-go's
// conventions:
//
//   - Hooks, MCP servers, A2A agents, memory, palace and compactor settings
//     from ~/.pi-go/config.json plus any project override.
//   - A filesystem sandbox rooted at the working directory (plus ~/.pi-go), and
//     the core tool set: read, write, edit, bash with its supervisor and the
//     bash control tools, search, and the rest of pi-go's built-ins.
//   - Skills discovered from .pi-go/skills/, the user directory and the bundled
//     set, summarized into the system prompt.
//   - Subagents discovered from .pi-go/agents plus the bundled set, wired to an
//     orchestrator and exposed as tools.
//   - Project context files (AGENT.md, AGENTS.md, CLAUDE.md, .pi-go/AGENTS.md)
//     discovered from the working directory up to the filesystem root.
//   - MCP servers and A2A agents as ADK toolsets.
//   - Sessions persisted under ~/.pi-go/sessions, so a session started by an
//     embedder is visible to `pi --resume` and vice versa.
//
// # One deliberate difference from the CLI
//
// Observation memory and the memory palace are OFF by default here, where the
// CLI has both on. They are the only subsystems that write to state shared
// with the user: ~/.pi-go/memory/claude-mem.db and ~/.pi-go/palace.db are the
// same stores a real pi session reads and writes. An embedder's process is not
// a pi session, and silently interleaving its observations with the user's is
// a surprise that documentation cannot undo — so it is opt-in, via
// [WithMemory] and [WithPalace].
//
// Skills and subagent discovery stay on. Those read .pi-go/, which is the
// whole reason to embed this agent instead of writing your own. Reading a
// convention and writing to someone's store are different things.
//
// # Gated subsystems
//
// Tool declarations are billed on every request — roughly 7.4k tokens before
// gating — so two groups only register when they can pay for themselves, and
// piagent keeps those gates rather than turning everything on:
//
//   - LSP tools register only when [WithLSP] is not [LSPOff] and a language
//     server for the workspace is actually installed. The default mode is
//     [LSPMin], which advertises two tools (symbols and diagnostics); [LSPFull]
//     advertises all seven. The after-tool LSP hook is wired either way and
//     costs nothing when no server starts.
//   - Palace tools, once [WithPalace] is on, register only when the palace
//     database exists and holds at least one drawer. An empty palace still gets
//     opened, so the observation bridge can fill it and the tools appear on a
//     later run.
//
// # Callback composition
//
// ADK ends its after-tool callback chain at the first callback that returns a
// non-nil result. pi-go's own after-tool callbacks all return the result map,
// so handing ADK a slice of them silently runs only the first. piagent
// therefore composes every after-tool callback — its own and yours — into a
// single ADK callback with explicit semantics:
//
//   - returning (nil, nil) leaves the result unchanged and continues the chain;
//   - returning (m, nil) makes m the result seen by every later callback and,
//     ultimately, by the model;
//   - returning a non-nil error aborts the chain and surfaces the error.
//
// Callbacks supplied through [WithAfterToolCallbacks] run after pi-go's, so
// they observe the compacted and deduplicated result rather than the raw one.
//
// Before-tool and model callbacks are passed to ADK as slices: for those,
// ADK's "run until one intervenes" semantics are already what you want.
//
// # Turn hooks
//
// Tool and model callbacks fire inside a turn. [WithBeforeTurn] and
// [WithAfterTurn] bracket the whole turn instead, which is the level metrics,
// audit trails and admission control actually work at:
//
//   - A before-turn hook sees the session ID and the outgoing message, and
//     returning an error aborts the turn before it reaches the model. Budget
//     checks, rate limits and moderation belong here.
//   - An after-turn hook receives a [TurnInfo] — duration, event and tool-call
//     counts, the failure if there was one, and whether the caller abandoned
//     the turn by breaking out of the loop early.
//
// Both fire when the caller starts iterating, not when Run returns: a sequence
// nobody ranges over is not a turn, and recording one would be a lie in
// whatever the hook writes down. An abandoned turn still reports, because a
// caller that stops consuming has still spent the tokens.
//
// This is the headless equivalent of pi-go's turn_complete lifecycle hook,
// which is otherwise dispatched only from the TUI.
//
// # Lifetime
//
// [Agent.Close] releases everything [New] acquired — sandbox, bash supervisor
// and any backgrounded process trees, subagent orchestrator, LSP manager,
// memory worker and store, palace, and the session log. Always defer it.
//
// # Deliberately not included
//
// Provider construction, in any form: no model name, base URL, API key or
// credential option exists here, because each one would drag provider
// resolution into this package's public surface.
//
// A process-global HTTP trace sink, because a library has no business
// claiming one.
//
// Config-driven shell hooks as a programmatic option. Those still load from
// ~/.pi-go/config.json and run for tool calls, but an embedder writing Go gets
// a Go func rather than a subprocess: [WithBeforeTurn] and
// [WithAfterToolCallbacks] do the same work without the fork.
//
// The daily-token guardrail, which wraps the model rather than the agent and
// so belongs with whoever built the model. piagent does wrap the model in an
// unlimited tracker, but only to measure: nothing here caps an embedder's
// spend.
//
// # Auto-compaction
//
// The two-stage compaction the CLI runs — shed superseded tool results at the
// lower threshold, summarize the transcript at the upper one — is installed
// here too, as a pre-turn hook. An embedded agent re-sends its whole
// transcript on every turn exactly as an interactive one does, so a long
// session outgrows its context window just as fast.
//
// It reads its thresholds from the auto_compact block of ~/.pi-go/config.json
// and is on by default. Two things switch it off: setting auto_compact.enabled
// to false, and a context window that cannot be resolved. The window comes
// from the embedded model catalog, because piagent is handed a finished model
// and never learns its base URL; set context_window in config.json when the
// catalog does not know the model.
//
// # Finding the provider behind a model
//
// Two things need to know which provider a model talks to: the OpenTelemetry
// gen_ai.provider.name span attribute, and whether Gemini's server-side
// grounding tool is worth registering. piagent asks the model itself, by
// asserting the shape interface{ Provider() string } — the shape, never a
// named type from another package, so the dependency stays on ADK alone. A
// model that cannot answer falls back to a prefix match on its name, and an
// unrecognized name simply gets neither the attribute nor the tool.
package piagent
