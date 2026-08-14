// Package piagent embeds pi-go's coding agent in a third-party Go program.
//
// Every other package in this module lives under internal/ and is therefore
// unreachable from outside. piagent is the one importable façade: it assembles
// the same agent the pi CLI runs in its headless (--mode print) path, minus the
// terminal UI, and hands it back as a small type you can drive from your own
// code.
//
// # Zero configuration
//
// The shortest useful program is:
//
//	ag, err := piagent.New(ctx)
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
// That call alone gives you:
//
//   - Configuration from ~/.pi-go/config.json plus any project override, with
//     the model, provider, base URL, API key and thinking level resolved the
//     way the CLI resolves them.
//   - A filesystem sandbox rooted at the working directory (plus ~/.pi-go), and
//     the core tool set: read, write, edit, bash with its supervisor and the
//     bash control tools, search, and the rest of pi-go's built-ins.
//   - Skills discovered from .pi-go/skills/, the user directory and the bundled
//     set, summarized into the system prompt.
//   - Subagents discovered from .pi-go/agents plus the bundled set, wired to an
//     orchestrator and exposed as tools.
//   - Project context files (AGENT.md, AGENTS.md, CLAUDE.md, .pi-go/AGENTS.md)
//     discovered from the working directory up to the filesystem root.
//   - MCP servers and A2A agents from the configuration, as ADK toolsets.
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
// Two things the CLI does are not reproduced here. The two-stage
// auto-compaction pre-turn hook lives in internal/cli and would have to be
// extracted first; embedders that need it can install their own via ADK. And
// piagent never installs a process-global HTTP trace sink, because a library
// has no business claiming one.
package piagent
