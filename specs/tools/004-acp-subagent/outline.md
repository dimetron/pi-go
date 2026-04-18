# ACP Structure Outline

## High-Level Slices

1. **ACP shared core scaffolding**
    - Add ACP dependency and create shared package layout for client/server/common helpers.
    - Define common runtime types: run request, events, results, session interfaces.
    - Add basic validation and smoke tests.

2. **ACP client subprocess runner**
    - Implement local stdio subprocess launch for ACP agents.
    - Create client-side ACP connection and basic initialize/session/prompt flow.
    - Verify a prompt can run end-to-end against a deterministic local ACP example agent.

3. **ACP client streaming and callbacks**
    - Implement session update streaming translation.
    - Add file read/write callbacks, permission handling, and terminal lifecycle support.
    - Verify streamed output and callback-driven ACP interactions work.

4. **pi ACP server skeleton**
    - Add a dedicated ACP server entrypoint for pi over stdio.
    - Implement `acp.Agent` wrapper around a minimal pi execution bridge.
    - Verify an ACP client can initialize pi and complete a basic prompt turn.

5. **pi ACP server full-surface support**
    - Expand server support for session load/cancel, modes, progress/tool streaming, and supported extensions.
    - Verify server-side ACP surface works across targeted protocol flows.

6. **Bidirectional hardening and UX**
    - Add CLI/user-facing entrypoints, docs, and failure handling polish for both directions.
    - Verify build, tests, and cross-path stability.

## Order of Changes and Testing

1. Establish shared ACP package and compile/tests.
2. Implement client mode first and validate against local ACP example agent.
3. Add client-side callbacks and streaming until client path is realistically usable with ACP agents.
4. Implement server mode skeleton and validate with a local ACP client.
5. Expand server mode toward full ACP coverage in independent slices.
6. Final hardening, docs, and gates.

## Key Type Signatures

```go
package acp

type RunRequest struct {
    Command string
    Args    []string
    Env     []string
    Prompt  string
}

type Event struct {
    Type      string `json:"type"`
    Content   string `json:"content,omitempty"`
    Error     string `json:"error,omitempty"`
    SessionID string `json:"session_id,omitempty"`
}

type RunResult struct {
    Status    string `json:"status"`
    Result    string `json:"result,omitempty"`
    Error     string `json:"error,omitempty"`
    SessionID string `json:"session_id,omitempty"`
}

type RunningSession interface {
    Events() <-chan Event
    Wait() (RunResult, error)
    Cancel()
}

type ClientRunner interface {
    Start(ctx context.Context, req RunRequest) (RunningSession, error)
}

type PiAgent interface {
    RunPrompt(ctx context.Context, prompt string, onEvent func(Event)) (RunResult, error)
}
```

Potential server-side bridge shape:

```go
package acpserver

type AgentAdapter struct {
    Pi PiAgent
}
```

Potential client-side callback helpers:

```go
package acpclient

type FileBridge interface {
    ReadTextFile(ctx context.Context, path string, line, limit *int) (string, error)
    WriteTextFile(ctx context.Context, path, content string) error
}

type TerminalBridge interface {
    Create(ctx context.Context, req CreateTerminalRequest) (TerminalRef, error)
    Output(ctx context.Context, terminalID string) (string, bool, error)
    Wait(ctx context.Context, terminalID string) error
    Kill(ctx context.Context, terminalID string) error
    Release(ctx context.Context, terminalID string) error
}
```
