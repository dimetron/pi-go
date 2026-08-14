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
//	m, err := pimodels.New(ctx, "claude-sonnet-5")
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
//   - Observation memory and the memory palace, each behaving exactly as they
//     do under the CLI (see "Gated subsystems").
//   - Sessions persisted under ~/.pi-go/sessions, so a session started by an
//     embedder is visible to `pi --resume` and vice versa.
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
//   - Palace tools register only when the palace database exists and holds at
//     least one drawer. An empty palace still gets opened, so the observation
//     bridge can fill it and the tools appear on a later run.
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
// The CLI's two-stage auto-compaction pre-turn hook, which lives in
// internal/cli and would have to be extracted first; embedders that need it
// can install their own via ADK.
//
// A process-global HTTP trace sink, because a library has no business
// claiming one.
//
// The daily-token guardrail, which wraps the model rather than the agent and
// so belongs with whoever built the model.
package piagent
