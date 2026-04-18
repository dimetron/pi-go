# ACP Bidirectional Support

## Objective

Add Agent Client Protocol support to pi-go in both directions. pi should be able to act as an ACP client for local
ACP-capable coding agents, and also act as an ACP server/agent so external ACP clients can drive pi over stdio.

## Key Requirements

1. **Bidirectional ACP** — implement both ACP client mode and ACP server mode in Go.
2. **Local stdio first** — the required v1 transport is local subprocess/stdin-stdout operation.
3. **Shared core** — client and server paths should reuse common ACP runtime/helpers where practical.
4. **Full ACP server surface** — server mode should target full ACP coverage in v1 as far as the SDK and pi runtime can
   support it.
5. **Client callbacks** — ACP client mode must support the necessary local file, terminal, permission, and streaming
   interactions needed by ACP agents.
6. **External agent interoperability** — Claude Code or Codex ACP-capable paths should be reachable through pi’s ACP
   client support.

## Acceptance Criteria

### ACP Client

- Given a local ACP-capable agent command and a prompt, when the pi ACP client entrypoint runs it, then pi launches the
  local ACP subprocess, initializes an ACP session, sends the prompt, and returns a structured result.
- Given an ACP-backed agent that streams message updates, when pi consumes the ACP session, then the ACP client path
  emits compatible local events and accumulates a final textual result.
- Given an ACP-backed agent requests supported client-side capabilities such as file access or terminal operations, when
  pi services those requests locally, then the ACP turn can continue successfully through the ACP client path.

### ACP Server

- Given pi is started in ACP server mode, when an ACP client connects over stdio and sends initialize, session, prompt,
  load, cancel, mode, and supported extension requests, then pi responds as an ACP agent with streamed updates and final
  responses across the supported ACP surface.

### Bidirectional Outcome

- Given pi ACP support is implemented, when users need either to drive external ACP agents or to drive pi through ACP,
  then both client and server paths are available in the first version.

## Implementation Slices

1. **Shared ACP scaffolding** — add `github.com/coder/acp-go-sdk`, create `internal/acp` shared types and validation,
   verify: `go test ./internal/acp/...`
2. **ACP client runner** — launch local ACP subprocesses over stdio and run initialize/session/prompt flow, verify:
   `go test ./internal/acp/client/...`
3. **ACP client integration target** — add deterministic integration coverage for a local ACP prompt turn, verify:
   `go test ./internal/acp/client/...`
4. **ACP client file callbacks** — implement file read/write callback support, verify:
   `go test ./internal/acp/client/...`
5. **ACP client terminal and permission callbacks** — implement terminal lifecycle and permission policy support,
   verify: `go test ./internal/acp/client/...`
6. **ACP client streaming polish** — expand event translation and integrated callback behavior, verify:
   `go test ./internal/acp/client/...`
7. **pi ACP server skeleton** — add ACP server package and stdio entrypoint for pi, verify:
   `go test ./internal/acp/server/...`
8. **ACP server integration** — verify a local ACP client can initialize pi and complete a prompt turn, verify:
   `go test ./internal/acp/server/...`
9. **ACP server load and cancel** — support session load and cancellation flows, verify:
   `go test ./internal/acp/server/...`
10. **ACP server modes and extensions** — add mode-related flows and extension handling, verify:
    `go test ./internal/acp/server/...`
11. **ACP server streaming alignment** — translate pi progress/tool activity into ACP updates, verify:
    `go test ./internal/acp/server/...`
12. **User-facing entrypoints and hardening** — finalize CLI wiring, docs, and bidirectional stability, verify:
    `make build && make test`

## Gates

- **build**: `make build`
- **test**: `make test`
- **vet**: `make vet`
- **lint**: `make lint`

## Reference

- Design: `specs/tools/004-acp-subagent/design.md`
- Outline: `specs/tools/004-acp-subagent/outline.md`
- Plan: `specs/tools/004-acp-subagent/plan.md`
- Requirements: `specs/tools/004-acp-subagent/requirements.md`
- Research: `specs/tools/004-acp-subagent/research/`

## Constraints

- v1 is local stdio first; remote ACP transport is not required.
- pi must support both ACP client and ACP server roles in the first version.
- server mode should target full ACP surface coverage as far as the SDK and pi runtime support it.
- the implementation should be shaped for later reuse with subagent integration, but full unification with current
  subagent execution is not required in this first version.
