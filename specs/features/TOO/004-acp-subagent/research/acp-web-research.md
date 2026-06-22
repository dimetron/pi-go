# ACP Web Research

## Scope

Objective findings gathered from ACP public docs, the `coder/acp-go-sdk` repository/docs, and ACP ecosystem pages.

## ACP role model

Public ACP descriptions characterize the protocol as:

- a JSON-RPC 2.0 protocol
- between **clients** and **agents**
- commonly used over **stdio** for local subprocess agents, with HTTP also mentioned as a transport in the broader
  protocol docs/ecosystem

A third-party overview summarizes the role split as:

1. clients manage the user environment
2. agents perform reasoning and tool execution
3. communication may happen over stdio or HTTP

## Local stdio client pattern from acp-go-sdk

The `coder/acp-go-sdk` README and package docs state that an ACP client in Go should:

- implement the `acp.Client` interface
- optionally implement `acp.ClientTerminal` for terminal features
- launch or connect to the agent process over stdio
- create a connection with `acp.NewClientSideConnection(client, stdin, stdout)`
- call `Initialize`, `NewSession`, and `Prompt`
- handle streamed updates during a turn

The SDK example list explicitly includes:

- `go run ./example/agent` — minimal ACP agent over stdio
- `go run ./example/client` — client that connects to a running agent and streams a sample turn
- `go run ./example/claude-code` — bridge to Claude Code
- `go run ./example/gemini` — bridge to Gemini CLI in ACP mode

## Client-side capability surface visible in docs/examples

From the README, package docs, and extracted example snippets, the client side may need to service agent-initiated
requests for:

- file reads: `fs/read_text_file`
- file writes: `fs/write_text_file`
- permission prompts: `session/request_permission`
- terminal lifecycle: `terminal/create`, `terminal/output`, `terminal/wait_for_exit`, `terminal/kill`,
  `terminal/release`
- update notifications: `session/update`

The `example_client_test.go` snippets show a client example implementing methods for file access, permission requests,
terminal output/waiting, and update handling.

## Session flow observed in public materials

The documented flow for a basic client session is:

1. launch/connect to agent process
2. `Initialize`
3. `NewSession`
4. `Prompt`
5. stream updates through callbacks/notifications

Protocol overview pages additionally mention `session/load` and `session/cancel` in the protocol surface.

## Extension mechanism

The SDK documents ACP extension methods:

- names start with `_`
- both client and agent may implement `acp.ExtensionMethodHandler`
- both sides can call `CallExtension` and `NotifyExtension`
- `_meta` capability fields are used to advertise extension support

## Ecosystem signal relevant to user goal

The ACP agents page lists many ACP-capable agents/tools and explicitly includes:

- Claude Agent via a Zed ACP adapter
- Codex CLI via a Zed ACP adapter
- Gemini CLI
- Pi via a `pi-acp` adapter reference

This supports the user’s stated goal that Claude Code or Codex be usable through an ACP path, though the cited page
lists adapters/ecosystem entries rather than a pi-go-native integration.

## Limits of this research

These findings are based on public README/docs/index pages and extracted snippets, not on a pinned ACP spec version
vendored in this repo.
No local ACP code exists yet in this repository.
